package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func promisedCommentRerunFixture(t *testing.T, route string, terminal string) (workflowHumanCommentFixture, string, db.AgentTaskQueue) {
	t.Helper()
	ctx := context.Background()
	if route != "member" {
		f, acceptanceSource, svc := workflowPostacceptRequest(t, false)
		if route == "requested" {
			question := workflowPostacceptComment(t, f, "Can you explain the promised behavior before approval?")
			result, _ := json.Marshal(protocol.TaskCompletedPayload{TaskID: acceptanceSource, Output: "Review finished."})
			completed, transitioned, err := testHandler.TaskService.CompleteTaskWithTransition(ctx, parseUUID(acceptanceSource), result, "acceptance-source-session", "", "", false, "", "")
			if err != nil || !transitioned {
				t.Fatalf("complete requested acceptance source: transitioned=%v err=%v", transitioned, err)
			}
			testHandler.reconcileCommentsOnCompletion(ctx, completed)
			return promisedCommentRerunTerminal(t, f, question.ID, terminal)
		}
		dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, acceptanceSource)
		processed, err := svc.FinalizeNextRequestedAcceptance(ctx)
		if err != nil || !processed {
			t.Fatalf("finalize assigned-agent acceptance: processed=%v err=%v", processed, err)
		}
		return promisedCommentRerunSource(t, f, terminal)
	}
	f := workflowPostacceptFixture(t, false)
	workflowPostacceptHumanAccept(t, f, false)
	return promisedCommentRerunSource(t, f, terminal)
}

func promisedCommentRerunSource(t *testing.T, f workflowHumanCommentFixture, terminal string) (workflowHumanCommentFixture, string, db.AgentTaskQueue) {
	t.Helper()
	dbfx.Exec(t, `UPDATE agent SET custom_env='{"PROMISED_PRIVATE_CONFIG":"original-owner-config"}'::jsonb WHERE id=$1`, f.coordinatorID)
	question := workflowPostacceptComment(t, f, "Can you explain the promised accepted behavior?")
	return promisedCommentRerunTerminal(t, f, question.ID, terminal)
}

func promisedCommentRerunTerminal(t *testing.T, f workflowHumanCommentFixture, commentID, terminal string) (workflowHumanCommentFixture, string, db.AgentTaskQueue) {
	t.Helper()
	dbfx.Exec(t, `UPDATE agent SET custom_env='{"PROMISED_PRIVATE_CONFIG":"original-owner-config"}'::jsonb WHERE id=$1`, f.coordinatorID)
	sourceID := workflowPostacceptTaskID(t, f, commentID)
	workflowPostacceptStart(t, f, sourceID, commentID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status=$2,completed_at=now(),session_id='promised-reply-session',
		work_dir='/tmp/promised-reply-workdir',retained_context_invalidated=true WHERE id=$1`, sourceID, terminal)
	source, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(sourceID))
	if err != nil {
		t.Fatal(err)
	}
	return f, commentID, source
}

func TestWorkflowPromisedHumanCommentRerunPreservesExactOriginalAuthorProof(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, route := range []string{"member", "assigned", "requested"} {
		for _, terminal := range []string{"failed", "cancelled"} {
			t.Run(fmt.Sprintf("%s/%s", route, terminal), func(t *testing.T) {
				f, commentID, source := promisedCommentRerunFixture(t, route, terminal)
				var rerun AgentTaskResponse
				testutil.Call(t, testHandler.RerunIssue, withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/rerun",
					map[string]any{"task_id": uuidToString(source.ID)}), "id", f.issueID)).Want(http.StatusAccepted).JSON(&rerun)
				stored, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(rerun.ID))
				if err != nil {
					t.Fatal(err)
				}
				if stored.RerunOfTaskID != source.ID || stored.OriginatorUserID != source.OriginatorUserID || stored.AccountableUserID != source.AccountableUserID ||
					stored.DelegatedFromTaskID != source.DelegatedFromTaskID || stored.TriggerEvidenceKind != source.TriggerEvidenceKind || stored.TriggerEvidenceRefID != source.TriggerEvidenceRefID ||
					stored.TriggerCommentID != source.TriggerCommentID || !bytes.Equal(stored.Context, source.Context) || len(stored.DeliveredCommentIds) != 0 || stored.RetainedContextInvalidated || stored.CommentResumeFromTaskID.Valid {
					t.Fatalf("rerun lost original promised-input evidence or copied abandoned ancestry: source=%+v rerun=%+v", source, stored)
				}
				claimed := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
				if claimed.ID != rerun.ID || claimed.PriorSessionID != "promised-reply-session" || claimed.WorkflowProfileID == "" || claimed.Agent == nil || claimed.Agent.CustomEnv["PROMISED_PRIVATE_CONFIG"] != "original-owner-config" {
					t.Fatalf("original author's actual retry did not claim its exact conversation/config: %+v", claimed)
				}
				if _, err := testHandler.TaskService.StartTask(context.Background(), stored.ID); err != nil {
					t.Fatalf("start promised reply rerun: %v", err)
				}
				if dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='running' AND $2::uuid=ANY(delivered_comment_ids)`, stored.ID, commentID) != 1 {
					t.Fatal("promised rerun did not receive the original comment while running")
				}
			})
		}
	}
}

func TestWorkflowPromisedHumanCommentRerunRejectsDifferentMemberBeforeEnqueue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, route := range []string{"member", "assigned", "requested"} {
		for _, terminal := range []string{"failed", "cancelled"} {
			t.Run(fmt.Sprintf("%s/%s", route, terminal), func(t *testing.T) {
				f, _, source := promisedCommentRerunFixture(t, route, terminal)
				admin := dbfx.User(t, "Promised reply admin", "promised-reply-admin@multica.test")
				dbfx.Member(t, testWorkspaceID, admin, "admin")
				f.allowCoordinatorInvocation(t, admin)
				before := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issueID)
				var response map[string]any
				testutil.Call(t, testHandler.RerunIssue, withURLParam(newRequestAs(admin, http.MethodPost, "/api/issues/"+f.issueID+"/rerun",
					map[string]any{"task_id": uuidToString(source.ID)}), "id", f.issueID)).Want(http.StatusForbidden).JSON(&response)
				if response["error"] != "only the original comment author may retry this promised reply" {
					t.Fatalf("different member refusal did not identify the original-author fence: %+v", response)
				}
				if after := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issueID); after != before {
					t.Fatalf("refused member rerun created a task: before=%d after=%d", before, after)
				}
				unchanged, err := testHandler.Queries.GetAgentTask(context.Background(), source.ID)
				if err != nil || unchanged.Status != terminal || unchanged.OriginatorUserID != source.OriginatorUserID {
					t.Fatalf("refused rerun mutated original evidence: task=%+v err=%v", unchanged, err)
				}
			})
		}
	}
}

func TestWorkflowPromisedHumanCommentRerunRejectsWithdrawnOrSupersededInput(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, route := range []string{"member", "assigned", "requested"} {
		for _, stale := range []string{"deleted comment", "withdrawn question", "superseded candidate"} {
			t.Run(fmt.Sprintf("%s/%s", route, stale), func(t *testing.T) {
				f, commentID, source := promisedCommentRerunFixture(t, route, "failed")
				if stale == "deleted comment" {
					testutil.Call(t, testHandler.DeleteComment, withURLParam(newRequest(http.MethodDelete, "/api/comments/"+commentID, nil), "commentId", commentID)).Want(http.StatusNoContent)
				} else if stale == "withdrawn question" {
					workflowCommentEdit(t, commentID, "/note This question is withdrawn.")
				} else {
					current, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(f.issueID))
					if err != nil {
						t.Fatal(err)
					}
					testutil.Call(t, testHandler.RejectIssueWorkflow, withURLParam(newRequest(http.MethodPost,
						"/api/issues/"+f.issueID+"/workflow/rejections", map[string]any{
							"candidate_id": uuidToString(current.WorkflowCandidateID), "expected_revision": current.Revision,
							"kind": "in_scope_defect", "reason": "The promised candidate needs correction before another answer.",
							"resume_task_id": f.writerTaskID,
						}), "id", f.issueID)).Want(http.StatusOK)
				}
				// Put real new work in this recipient's queue. A stale rerun must
				// refuse before generic rerun cancellation can retire that work.
				newContent := "Please answer this new question instead."
				if stale == "superseded candidate" {
					newContent = fmt.Sprintf("[@Coordinator](mention://agent/%s) %s", f.coordinatorID, newContent)
				}
				newQuestion := workflowPostacceptComment(t, f, newContent)
				pendingID := workflowPostacceptTaskID(t, f, newQuestion.ID)
				pending, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(pendingID))
				if err != nil || pending.Status != "queued" {
					t.Fatalf("new question did not create pending work: %+v err=%v", pending, err)
				}
				before := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issueID)
				testutil.Call(t, testHandler.RerunIssue, withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/rerun",
					map[string]any{"task_id": uuidToString(source.ID)}), "id", f.issueID)).Want(http.StatusBadRequest)
				if after := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issueID); after != before {
					t.Fatalf("stale promise rerun created a task: before=%d after=%d", before, after)
				}
				unchanged, err := testHandler.Queries.GetAgentTask(context.Background(), pending.ID)
				if err != nil || unchanged.Status != pending.Status || unchanged.CompletedAt.Valid {
					t.Fatalf("stale promise rerun changed new pending work: %+v err=%v", unchanged, err)
				}
			})
		}
	}
}

func TestWorkflowPromisedHumanCommentRerunRequiresNewIntentAfterAnswer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, route := range []string{"member", "assigned", "requested"} {
		t.Run(route, func(t *testing.T) {
			f, _, source := promisedCommentRerunFixture(t, route, "completed")
			before := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issueID)
			var response map[string]any
			testutil.Call(t, testHandler.RerunIssue, withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/rerun",
				map[string]any{"task_id": uuidToString(source.ID)}), "id", f.issueID)).Want(http.StatusBadRequest).JSON(&response)
			if response["error"] != "this promised reply was already answered; add a new comment to request another answer" {
				t.Fatalf("answered reply refusal did not explain the needed new intent: %+v", response)
			}
			if after := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issueID); after != before {
				t.Fatalf("answered reply was silently enqueued again: before=%d after=%d", before, after)
			}
		})
	}
}

func TestWorkflowPromisedHumanCommentRerunPromotesLiveCoalescedInputWithFreshBirthEvidence(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, route := range []string{"member", "assigned"} {
		t.Run(route, func(t *testing.T) {
			var f workflowHumanCommentFixture
			if route == "member" {
				f = workflowPostacceptFixture(t, false)
				workflowPostacceptHumanAccept(t, f, false)
			} else {
				var acceptanceSource string
				var svc service.WorkflowAuthorityService
				f, acceptanceSource, svc = workflowPostacceptRequest(t, false)
				dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, acceptanceSource)
				if processed, err := svc.FinalizeNextRequestedAcceptance(context.Background()); err != nil || !processed {
					t.Fatalf("finalize assigned-agent acceptance: processed=%v err=%v", processed, err)
				}
			}
			survivor := workflowPostacceptComment(t, f, "Please explain this still-live question.")
			var withdrawn CommentResponse
			testutil.Call(t, testHandler.CreateComment, withURLParam(newRequest(http.MethodPost,
				"/api/issues/"+f.issueID+"/comments", map[string]any{"content": "Please explain this later question too.", "parent_id": survivor.ID}), "id", f.issueID)).Want(http.StatusCreated).JSON(&withdrawn)
			sourceID := workflowPostacceptTaskID(t, f, withdrawn.ID)
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(coalesced_comment_ids)`, sourceID, survivor.ID); got != 1 {
				t.Fatal("two real questions did not coalesce into one promised task")
			}
			workflowPostacceptStart(t, f, sourceID, withdrawn.ID)
			dbfx.Exec(t, `UPDATE agent_task_queue SET status='failed',completed_at=now(),session_id='coalesced-reply-session' WHERE id=$1`, sourceID)
			var sourceMarker string
			if err := testPool.QueryRow(context.Background(), `SELECT context->'workflow_comment_obligation'->>'comment_id'
				FROM agent_task_queue WHERE id=$1`, sourceID).Scan(&sourceMarker); err != nil || sourceMarker != withdrawn.ID {
				t.Fatalf("coalesced source did not retain exact primary birth evidence: comment=%s err=%v", sourceMarker, err)
			}
			testutil.Call(t, testHandler.DeleteComment, withURLParam(newRequest(http.MethodDelete, "/api/comments/"+withdrawn.ID, nil), "commentId", withdrawn.ID)).Want(http.StatusNoContent)
			var rerun AgentTaskResponse
			testutil.Call(t, testHandler.RerunIssue, withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/rerun",
				map[string]any{"task_id": sourceID}), "id", f.issueID)).Want(http.StatusAccepted).JSON(&rerun)
			var markerComment string
			if err := testPool.QueryRow(context.Background(), `SELECT context->'workflow_comment_obligation'->>'comment_id'
				FROM agent_task_queue WHERE id=$1 AND trigger_comment_id=$2 AND rerun_of_task_id=$3`, rerun.ID, survivor.ID, sourceID).Scan(&markerComment); err != nil || markerComment != survivor.ID {
				t.Fatalf("promoted reply did not retain freshly minted exact birth evidence: comment=%s err=%v", markerComment, err)
			}
			workflowPostacceptStart(t, f, rerun.ID, survivor.ID)
		})
	}
}

func TestWorkflowPromisedHumanCommentRerunOldFailureCannotRepeatSuccessfulAnswer(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, route := range []string{"member", "assigned", "requested"} {
		t.Run(route, func(t *testing.T) {
			f, commentID, source := promisedCommentRerunFixture(t, route, "failed")
			var rerun AgentTaskResponse
			testutil.Call(t, testHandler.RerunIssue, withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/rerun",
				map[string]any{"task_id": uuidToString(source.ID)}), "id", f.issueID)).Want(http.StatusAccepted).JSON(&rerun)
			workflowPostacceptStart(t, f, rerun.ID, commentID)
			result, _ := json.Marshal(protocol.TaskCompletedPayload{TaskID: rerun.ID, Output: "The promised question has been answered."})
			completed, transitioned, err := testHandler.TaskService.CompleteTaskWithTransition(context.Background(), parseUUID(rerun.ID), result,
				"successful-reply-session", "", "", false, "", "")
			if err != nil || !transitioned {
				t.Fatalf("complete actual successful retry: transitioned=%v err=%v", transitioned, err)
			}
			testHandler.reconcileCommentsOnCompletion(context.Background(), completed)
			newQuestion := workflowPostacceptComment(t, f, "Please answer this separate new question next.")
			pendingID := workflowPostacceptTaskID(t, f, newQuestion.ID)
			before := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issueID)
			testutil.Call(t, testHandler.RerunIssue, withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/rerun",
				map[string]any{"task_id": uuidToString(source.ID)}), "id", f.issueID)).Want(http.StatusBadRequest)
			if after := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issueID); after != before {
				t.Fatalf("old failed reply created a duplicate answer after successful retry: before=%d after=%d", before, after)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='completed'
				AND $2::uuid=ANY(delivered_comment_ids)`, rerun.ID, commentID); got != 1 {
				t.Fatal("refused old-failure rerun changed the successful answer receipt")
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='queued'
				AND completed_at IS NULL`, pendingID); got != 1 {
				t.Fatal("refused old-failure rerun cancelled the separate new question")
			}
		})
	}
}

func TestWorkflowPromisedHumanCommentRerunNullPrimaryRejectsUnrecordedCoalescedInput(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := workflowPostacceptFixture(t, false)
	workflowPostacceptHumanAccept(t, f, false)
	anchor := workflowPostacceptComment(t, f, "/note Thread anchor.")
	var primary CommentResponse
	testutil.Call(t, testHandler.CreateComment, withURLParam(newRequest(http.MethodPost,
		"/api/issues/"+f.issueID+"/comments", map[string]any{"content": "Please explain this promised input.", "parent_id": anchor.ID}), "id", f.issueID)).Want(http.StatusCreated).JSON(&primary)
	sourceID := workflowPostacceptTaskID(t, f, primary.ID)
	workflowPostacceptStart(t, f, sourceID, primary.ID)
	// Model the real losing-enqueue path: a saved ordinary comment is appended
	// to the active task's plan without acquiring workflow source-plan proof.
	unrecorded := dbfx.Comment(t, f.issueID, fmt.Sprintf("[@Coordinator](mention://agent/%s) An ordinary explicit question.", f.coordinatorID), testutil.Cols{"parent_id": anchor.ID})
	registered, err := testHandler.Queries.RegisterPlannedCommentForActiveTask(context.Background(), db.RegisterPlannedCommentForActiveTaskParams{
		CommentID: parseUUID(unrecorded), IssueID: parseUUID(f.issueID), AgentID: parseUUID(f.coordinatorID),
	})
	if err != nil || registered.ID != parseUUID(sourceID) {
		t.Fatalf("register ordinary lost-race input: task=%+v err=%v", registered, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(coalesced_comment_ids)`, f.coordinatorTaskID, unrecorded); got != 0 {
		t.Fatal("ordinary lost-race input unexpectedly acquired coordinator source proof")
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='failed',completed_at=now() WHERE id=$1`, sourceID)
	testutil.Call(t, testHandler.DeleteComment, withURLParam(newRequest(http.MethodDelete, "/api/comments/"+primary.ID, nil), "commentId", primary.ID)).Want(http.StatusNoContent)
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND trigger_comment_id IS NULL
		AND $2::uuid=ANY(coalesced_comment_ids)`, sourceID, unrecorded); got != 1 {
		t.Fatal("fixture did not retain the null-primary ordinary coalesced input")
	}
	before := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issueID)
	testutil.Call(t, testHandler.RerunIssue, withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/rerun",
		map[string]any{"task_id": sourceID}), "id", f.issueID)).Want(http.StatusBadRequest)
	if after := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, f.issueID); after != before {
		t.Fatalf("unrecorded coalesced input borrowed workflow reply authority: before=%d after=%d", before, after)
	}
}

func TestWorkflowPromisedHumanCommentRerunAfterRuntimeRebindKeepsPolicyWithoutOldSession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, route := range []string{"member", "assigned", "requested"} {
		for _, terminal := range []string{"failed", "cancelled"} {
			t.Run(route+"/"+terminal, func(t *testing.T) {
				ctx := context.Background()
				f, commentID, source := promisedCommentRerunFixture(t, route, terminal)
				if !source.WorkflowProfileID.Valid || source.WorkflowPolicyVersion.String == "" || !source.SessionID.Valid || !source.WorkDir.Valid {
					t.Fatalf("original reply did not pin its policy and provider context before rebinding: %+v", source)
				}
				originalIssue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
				if err != nil {
					t.Fatal(err)
				}
				oldRuntime, err := testHandler.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
					ID: source.RuntimeID, WorkspaceID: originalIssue.WorkspaceID,
				})
				if err != nil {
					t.Fatal(err)
				}
				newRuntimeID := dbfx.Runtime(t, "Rebound promised-reply runtime", testutil.Cols{"provider": oldRuntime.Provider, "runtime_mode": oldRuntime.RuntimeMode})
				var rebound AgentResponse
				testutil.Call(t, testHandler.UpdateAgent, withURLParam(newRequest(http.MethodPut,
					"/api/agents/"+f.coordinatorID, map[string]any{"runtime_id": newRuntimeID}), "id", f.coordinatorID)).Want(http.StatusOK).JSON(&rebound)
				if rebound.ID != f.coordinatorID || rebound.RuntimeID != newRuntimeID {
					t.Fatalf("actual same-agent runtime rebind failed: %+v", rebound)
				}
				var rerun AgentTaskResponse
				testutil.Call(t, testHandler.RerunIssue, withURLParam(newRequest(http.MethodPost,
					"/api/issues/"+f.issueID+"/rerun", map[string]any{"task_id": uuidToString(source.ID)}), "id", f.issueID)).Want(http.StatusAccepted).JSON(&rerun)
				f.runtimeID = newRuntimeID
				claimed := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1+","+protocol.DaemonCapabilityRetainedContextResetV1)
				if claimed.ID != rerun.ID || claimed.RuntimeID != newRuntimeID || claimed.AgentID != f.coordinatorID ||
					claimed.WorkflowProfileID != uuidToString(source.WorkflowProfileID) || claimed.WorkflowPolicyVersion != source.WorkflowPolicyVersion.String ||
					claimed.PriorSessionID != "" || claimed.PriorWorkDir != source.WorkDir.String || claimed.Agent == nil ||
					claimed.Agent.CustomEnv["PROMISED_PRIVATE_CONFIG"] != "original-owner-config" {
					t.Fatalf("rebound rerun lost policy/author configuration or borrowed the old runtime's provider session: %+v", claimed)
				}
				stored, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(rerun.ID))
				if err != nil || stored.RuntimeID != parseUUID(newRuntimeID) || stored.RerunOfTaskID != source.ID ||
					stored.OriginatorUserID != source.OriginatorUserID || stored.AccountableUserID != source.AccountableUserID ||
					stored.WorkflowProfileID != source.WorkflowProfileID || stored.WorkflowPolicyVersion != source.WorkflowPolicyVersion ||
					stored.SessionID.Valid || stored.CommentResumeFromTaskID.Valid || stored.RetainedContextInvalidated {
					t.Fatalf("rebound task copied stale provider ancestry or changed original policy/author: task=%+v err=%v", stored, err)
				}
				if _, err := testHandler.TaskService.StartTask(ctx, stored.ID); err != nil {
					t.Fatalf("start current-runtime promised reply: %v", err)
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND runtime_id=$2
					AND status='running' AND $3::uuid=ANY(delivered_comment_ids)`, stored.ID, newRuntimeID, commentID); got != 1 {
					t.Fatal("rebound current-runtime attempt did not start with the promised human input")
				}
				oldSource, err := testHandler.Queries.GetAgentTask(ctx, source.ID)
				if err != nil || oldSource.Status != terminal || oldSource.RuntimeID != source.RuntimeID || oldSource.SessionID != source.SessionID ||
					oldSource.WorkDir != source.WorkDir || oldSource.WorkflowProfileID != source.WorkflowProfileID || !bytes.Equal(oldSource.Context, source.Context) {
					t.Fatalf("current-runtime retry rewrote historical execution evidence: source=%+v err=%v", oldSource, err)
				}
				currentIssue, err := testHandler.Queries.GetIssue(ctx, originalIssue.ID)
				if err != nil || currentIssue.WorkflowCandidateID != originalIssue.WorkflowCandidateID || !bytes.Equal(currentIssue.WorkflowPolicy, originalIssue.WorkflowPolicy) {
					t.Fatalf("runtime rebind changed promised candidate authority: issue=%+v err=%v", currentIssue, err)
				}
			})
		}
	}
}

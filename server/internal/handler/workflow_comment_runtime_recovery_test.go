package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func workflowRuntimeReplyFixture(t *testing.T, route string) (workflowHumanCommentFixture, string, db.AgentTaskQueue) {
	t.Helper()
	ctx := context.Background()
	var f workflowHumanCommentFixture
	var sourceID string
	if route == "member" {
		f = workflowPostacceptFixture(t, false)
		workflowPostacceptHumanAccept(t, f, false)
		sourceID = f.coordinatorTaskID
	} else {
		var svc = testHandler.workflowAuthorityService()
		f, sourceID, svc = workflowPostacceptRequest(t, false)
		dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, sourceID)
		if route == "assigned" {
			if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
				t.Fatalf("finalize assigned reply fixture: processed=%v error=%v", processed, err)
			}
		}
	}
	comment := workflowPostacceptComment(t, f, "Please explain the promised result before proceeding.")
	taskID := workflowPostacceptTaskID(t, f, comment.ID)
	// Bind the existing selected profile without claiming the reply. Replacement
	// must preserve these bytes while rebuilding its provider context.
	dbfx.Exec(t, `UPDATE agent_task_queue SET workflow_profile_id=source.workflow_profile_id,
	 workflow_policy_version=source.workflow_policy_version FROM agent_task_queue source
	 WHERE agent_task_queue.id=$1 AND source.id=$2`, taskID, sourceID)
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil || !task.WorkflowProfileID.Valid {
		t.Fatalf("bound unclaimed reply: task=%s profile=%s error=%v", taskID, uuidToString(task.WorkflowProfileID), err)
	}
	return f, comment.ID, task
}

func rebindWorkflowReplyRuntime(t *testing.T, f workflowHumanCommentFixture) string {
	t.Helper()
	runtime, err := testHandler.Queries.GetAgentRuntimeForWorkspace(context.Background(), db.GetAgentRuntimeForWorkspaceParams{
		ID: parseUUID(f.runtimeID), WorkspaceID: parseUUID(testWorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	newID := dbfx.Runtime(t, "Current runtime for promised reply", testutil.Cols{
		"provider": runtime.Provider, "runtime_mode": runtime.RuntimeMode})
	testutil.Call(t, testHandler.UpdateAgent, withURLParam(newRequest(http.MethodPut,
		"/api/agents/"+f.coordinatorID, map[string]any{"runtime_id": newID}), "id", f.coordinatorID)).Want(http.StatusOK)
	return newID
}

func TestWorkflowRecordedCommentRuntimeRecoveryReplacesOnlyUnclaimedPlans(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	for _, route := range []string{"member", "assigned", "requested"} {
		for _, status := range []string{"queued", "deferred"} {
			t.Run(route+"/"+status, func(t *testing.T) {
				ctx := context.Background()
				f, commentID, original := workflowRuntimeReplyFixture(t, route)
				if status == "deferred" {
					dbfx.Exec(t, `UPDATE agent_task_queue SET status='deferred',fire_at=now() WHERE id=$1`, original.ID)
				}
				newRuntimeID := rebindWorkflowReplyRuntime(t, f)
				worker := NewWorkflowDeliveryWorker(testHandler)
				if recovered, err := worker.RecoverNextRecordedWorkflowComment(ctx); err != nil || !recovered {
					t.Fatalf("replace stale %s reply: recovered=%v error=%v", status, recovered, err)
				}
				if recovered, err := worker.RecoverNextRecordedWorkflowComment(ctx); err != nil || recovered {
					t.Fatalf("second tick duplicated replacement: recovered=%v error=%v", recovered, err)
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='cancelled'
				 AND cancelled_by_type='system' AND dispatched_at IS NULL AND started_at IS NULL
				 AND retained_context_invalidated AND context ? 'workflow_comment_runtime_retired'`, original.ID); got != 1 {
					t.Fatal("stale unclaimed plan was not durably retired")
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM activity_log WHERE issue_id=$1
				 AND action='workflow_comment_runtime_recovered' AND details->>'retired_task_id'=$2`, f.issueID, uuidToString(original.ID)); got != 1 {
					t.Fatalf("replacement has %d runtime recovery audit records", got)
				}
				claim := claimWorkflowTask(t, newRuntimeID, protocol.DaemonCapabilityPlatformSkillV1)
				if claim.ID == uuidToString(original.ID) || claim.RuntimeID != newRuntimeID || claim.AgentID != f.coordinatorID ||
					claim.WorkflowProfileID != uuidToString(original.WorkflowProfileID) || claim.WorkflowPolicyVersion != original.WorkflowPolicyVersion.String || claim.PriorSessionID != "" {
					t.Fatalf("replacement contract: task=%s runtime=%s profile=%s policy=%s prior_session=%q", claim.ID, claim.RuntimeID, claim.WorkflowProfileID, claim.WorkflowPolicyVersion, claim.PriorSessionID)
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1
				 AND originator_user_id=$2 AND accountable_user_id=$2 AND force_fresh_session
				 AND rerun_of_task_id IS NULL AND retry_of_task_id IS NULL AND comment_resume_from_task_id IS NULL
				 AND $3::uuid=ANY(delivered_comment_ids)`, claim.ID, testUserID, commentID); got != 1 {
					t.Fatal("replacement lost the original human input or inherited provider ancestry")
				}
				if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(claim.ID)); err != nil {
					t.Fatalf("start current-runtime reply: %v", err)
				}
			})
		}
	}
}

func TestWorkflowRecordedCommentRuntimeRecoveryPreservesClaimedLeases(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	for _, route := range []string{"member", "assigned", "requested"} {
		for _, status := range []string{"dispatched", "running", "waiting_local_directory"} {
			t.Run(route+"/"+status, func(t *testing.T) {
				ctx := context.Background()
				f, commentID, original := workflowRuntimeReplyFixture(t, route)
				claim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
				if claim.ID != uuidToString(original.ID) {
					t.Fatalf("wrong original claim: task=%s agent=%s", claim.ID, claim.AgentID)
				}
				if status != "dispatched" {
					if _, err := testHandler.TaskService.StartTask(ctx, original.ID); err != nil {
						t.Fatal(err)
					}
					if status == "waiting_local_directory" {
						dbfx.Exec(t, `UPDATE agent_task_queue SET status='waiting_local_directory' WHERE id=$1`, original.ID)
					}
				}
				before, err := testHandler.Queries.GetAgentTask(ctx, original.ID)
				if err != nil {
					t.Fatal(err)
				}
				newRuntimeID := rebindWorkflowReplyRuntime(t, f)
				worker := NewWorkflowDeliveryWorker(testHandler)
				for tick := 0; tick < 2; tick++ {
					if recovered, err := worker.RecoverNextRecordedWorkflowComment(ctx); err != nil || recovered {
						t.Fatalf("leased reply duplicated on tick %d: recovered=%v error=%v", tick, recovered, err)
					}
				}
				after, err := testHandler.Queries.GetAgentTask(ctx, original.ID)
				if err != nil || after.Status != status || after.RuntimeID != before.RuntimeID || after.DispatchedAt != before.DispatchedAt ||
					after.WorkflowProfileID != before.WorkflowProfileID || after.WorkflowPolicyVersion != before.WorkflowPolicyVersion {
					t.Fatalf("retained lease changed: task=%s status=%s runtime=%s profile=%s error=%v", uuidToString(after.ID), after.Status, uuidToString(after.RuntimeID), uuidToString(after.WorkflowProfileID), err)
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND runtime_id=$2
				 AND (trigger_comment_id=$3 OR $3::uuid=ANY(coalesced_comment_ids))`, f.issueID, newRuntimeID, commentID); got != 0 {
					t.Fatalf("retained lease spawned %d current-runtime duplicate replies", got)
				}
				if status == "dispatched" {
					if _, err := testHandler.TaskService.StartTask(ctx, original.ID); err != nil {
						t.Fatalf("start retained old-runtime claim after default rebind: %v", err)
					}
				}
			})
		}
	}
}

func TestWorkflowRecordedCommentRuntimeRecoveryPinsRetryAndRerunLineage(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	for _, lineage := range []string{"retry", "rerun"} {
		t.Run(lineage, func(t *testing.T) {
			ctx := context.Background()
			f, commentID, source := promisedCommentRerunFixture(t, "member", "failed")
			var stale db.AgentTaskQueue
			if lineage == "retry" {
				var err error
				stale, err = testHandler.Queries.CreateRetryTask(ctx, db.CreateRetryTaskParams{ID: source.ID})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var response AgentTaskResponse
				testutil.Call(t, testHandler.RerunIssue, withURLParam(newRequest(http.MethodPost,
					"/api/issues/"+f.issueID+"/rerun", map[string]any{"task_id": uuidToString(source.ID)}), "id", f.issueID)).
					Want(http.StatusAccepted).JSON(&response)
				var err error
				stale, err = testHandler.Queries.GetAgentTask(ctx, parseUUID(response.ID))
				if err != nil {
					t.Fatal(err)
				}
			}
			if stale.WorkflowProfileID.Valid || !stale.RetryOfTaskID.Valid && !stale.RerunOfTaskID.Valid {
				t.Fatalf("expected unbound %s: task=%s profile=%s retry=%s rerun=%s", lineage, uuidToString(stale.ID), uuidToString(stale.WorkflowProfileID), uuidToString(stale.RetryOfTaskID), uuidToString(stale.RerunOfTaskID))
			}
			newRuntimeID := rebindWorkflowReplyRuntime(t, f)
			if recovered, err := NewWorkflowDeliveryWorker(testHandler).RecoverNextRecordedWorkflowComment(ctx); err != nil || !recovered {
				t.Fatalf("recover %s lineage: recovered=%v error=%v", lineage, recovered, err)
			}
			var replacementID string
			dbfx.QueryRow(t, `SELECT id::text FROM agent_task_queue WHERE issue_id=$1 AND runtime_id=$2 AND status='queued'`, f.issueID, newRuntimeID).Scan(&replacementID)
			replacement, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(replacementID))
			if err != nil || replacement.WorkflowProfileID != source.WorkflowProfileID || replacement.WorkflowPolicyVersion != source.WorkflowPolicyVersion ||
				replacement.RerunOfTaskID.Valid || replacement.RetryOfTaskID.Valid {
				t.Fatalf("replacement contract: task=%s profile=%s want_profile=%s policy=%s retry=%s rerun=%s error=%v", replacementID, uuidToString(replacement.WorkflowProfileID), uuidToString(source.WorkflowProfileID), replacement.WorkflowPolicyVersion.String, uuidToString(replacement.RetryOfTaskID), uuidToString(replacement.RerunOfTaskID), err)
			}
			claim := claimWorkflowTask(t, newRuntimeID, protocol.DaemonCapabilityPlatformSkillV1)
			if claim.ID != replacementID || claim.PriorSessionID != "" || claim.WorkflowProfileID != uuidToString(source.WorkflowProfileID) {
				t.Fatalf("replacement %s claim: task=%s profile=%s prior_session=%q", lineage, claim.ID, claim.WorkflowProfileID, claim.PriorSessionID)
			}
			if _, err := testHandler.TaskService.StartTask(ctx, replacement.ID); err != nil {
				t.Fatal(err)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(delivered_comment_ids)`, replacement.ID, commentID); got != 1 {
				t.Fatal("fresh lineage replacement did not receive the original human input")
			}
		})
	}
}

func TestWorkflowRecordedCommentRuntimeRecoveryKeepsCoalescedWinnerContract(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	ctx := context.Background()
	f, commentID, stale := workflowRuntimeReplyFixture(t, "member")
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='deferred',fire_at=now() WHERE id=$1`, stale.ID)
	newRuntimeID := rebindWorkflowReplyRuntime(t, f)
	f.runtimeID = newRuntimeID
	turn := workflowPostacceptComment(t, f, "Can you answer this separate current-runtime question?")
	turnID := workflowPostacceptTaskID(t, f, turn.ID)
	workflowPostacceptStart(t, f, turnID, turn.ID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now(),session_id='legitimate-current-runtime-session' WHERE id=$1`, turnID)
	var reply CommentResponse
	testutil.Call(t, testHandler.CreateComment, withURLParam(newRequest(http.MethodPost,
		"/api/issues/"+f.issueID+"/comments", map[string]any{"content": "A later reply in the waiting conversation.", "parent_id": commentID}), "id", f.issueID)).
		Want(http.StatusCreated).JSON(&reply)
	winnerID := workflowPostacceptTaskID(t, f, reply.ID)
	winner, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(winnerID))
	if err != nil || winner.RerunOfTaskID != parseUUID(turnID) {
		t.Fatalf("current-runtime winner lineage: task=%s rerun=%s want_rerun=%s error=%v", winnerID, uuidToString(winner.RerunOfTaskID), turnID, err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	queuedEvents := 0
	bus.Subscribe(protocol.EventTaskQueued, func(events.Event) { queuedEvents++ })
	local := &service.TaskService{Queries: testHandler.Queries, TxStarter: testPool, Bus: bus}
	if replaced, err := local.ReplaceUnclaimedWorkflowCommentRuntime(ctx, issue, parseUUID(f.coordinatorID), issue.WorkflowCandidateID,
		parseUUID(commentID), f.second.ID, parseUUID(f.coordinatorTaskID)); err != nil || !replaced {
		t.Fatalf("coalesce retired reply into winner: replaced=%v error=%v", replaced, err)
	}
	after, err := testHandler.Queries.GetAgentTask(ctx, winner.ID)
	if err != nil || after.WorkflowProfileID != winner.WorkflowProfileID || after.WorkflowPolicyVersion != winner.WorkflowPolicyVersion ||
		after.RerunOfTaskID != winner.RerunOfTaskID || after.RetryOfTaskID != winner.RetryOfTaskID || queuedEvents != 0 {
		t.Fatalf("coalesced winner contract: task=%s profile=%s policy=%s rerun=%s retry=%s queued_events=%d error=%v", winnerID, uuidToString(after.WorkflowProfileID), after.WorkflowPolicyVersion.String, uuidToString(after.RerunOfTaskID), uuidToString(after.RetryOfTaskID), queuedEvents, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND runtime_id=$2 AND status='queued'`, f.issueID, newRuntimeID); got != 1 {
		t.Fatalf("coalescing produced %d queued winners", got)
	}
	claim := claimWorkflowTask(t, newRuntimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claim.ID != winnerID || claim.PriorSessionID != "legitimate-current-runtime-session" {
		t.Fatalf("existing winner session: task=%s prior_session=%q", claim.ID, claim.PriorSessionID)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, winner.ID); err != nil {
		t.Fatal(err)
	}
}

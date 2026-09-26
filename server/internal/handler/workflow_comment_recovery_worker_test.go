package handler

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func assertRecoveredWorkflowComment(t *testing.T, worker *WorkflowDeliveryWorker, issueID, runtimeID, agentID, commentID string) string {
	t.Helper()
	ctx := context.Background()
	if recovered, err := worker.RecoverNextRecordedWorkflowComment(ctx); err != nil || !recovered {
		t.Fatalf("restart recovery: recovered=%v error=%v", recovered, err)
	}
	if recovered, err := worker.RecoverNextRecordedWorkflowComment(ctx); err != nil || recovered {
		t.Fatalf("second tick duplicated recorded input: recovered=%v error=%v", recovered, err)
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND agent_id=$2 AND trigger_comment_id=$3`,
		issueID, agentID, commentID); count != 1 {
		t.Fatalf("recorded input produced %d conversations", count)
	}
	claim := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claim.AgentID != agentID {
		t.Fatalf("recorded input claimed by %s, want %s", claim.AgentID, agentID)
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(delivered_comment_ids)`,
		claim.ID, commentID); count != 1 {
		t.Fatal("recovered claim did not actually deliver the promised input")
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(claim.ID)); err != nil {
		t.Fatal(err)
	}
	return claim.ID
}

func TestWorkflowRecordedCommentRecoveryResumesAcceptedDelivery(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		name := "member_handoff"
		if assigned {
			name = "assigned_agent"
		}
		t.Run(name, func(t *testing.T) {
			if testHandler == nil {
				t.Skip("handler test fixture unavailable")
			}
			var mutations atomic.Int32
			var merged atomic.Bool
			server := feedbackDeliveryProvider(t, &mutations, &merged, true)
			defer server.Close()
			f := workflowDeliveryFeedbackCandidate(t, server, true)
			_, comment := recordPromisedWorkflowDeliveryFeedback(t, f, assigned, "Explain this accepted candidate before delivery.")
			// A new worker models restarting after the save committed and before
			// the HTTP handler reached its separate enqueue transaction.
			worker := NewWorkflowDeliveryWorker(testHandler)
			worker.client = server.Client()
			if worked, err := worker.ProcessNext(context.Background()); err != nil || !worked || mutations.Load() != 0 {
				t.Fatalf("saved intent did not pause delivery: worked=%v error=%v mutations=%d", worked, err, mutations.Load())
			}
			taskID := assertRecoveredWorkflowComment(t, worker, f.issueID, f.runtimeID, f.coordinatorID, uuidToString(comment.ID))
			dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
			dbfx.Exec(t, `UPDATE issue_workflow_delivery SET next_attempt_at=now() WHERE id=$1`, f.deliveryID)
			if worked, err := worker.ProcessNext(context.Background()); err != nil || !worked || mutations.Load() != 1 {
				t.Fatalf("answered recovered input did not resume merge: worked=%v error=%v mutations=%d", worked, err, mutations.Load())
			}
			if recovered, err := worker.RecoverNextRecordedWorkflowComment(context.Background()); err != nil || recovered {
				t.Fatalf("completed receipt replayed again: recovered=%v error=%v", recovered, err)
			}
		})
	}
}

func TestWorkflowRecordedCommentRecoveryResumesRequestedAcceptance(t *testing.T) {
	f, sourceID, svc := workflowPostacceptRequest(t, true)
	ctx := context.Background()
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM issue WHERE id=$1 FOR UPDATE`, issue.ID); err != nil {
		t.Fatal(err)
	}
	comment, err := testHandler.Queries.WithTx(tx).CreateComment(ctx, db.CreateCommentParams{ID: dbid.NewV7(),
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, AuthorType: "member", AuthorID: parseUUID(testUserID),
		Type: "comment", Content: "Please explain the result before approval finishes."})
	if err != nil {
		t.Fatal(err)
	}
	if matched, err := service.RecordRequestedAcceptanceComment(ctx, tx, issue, parseUUID(f.coordinatorID), comment.ID); err != nil || !matched {
		t.Fatalf("record requested input: matched=%v error=%v", matched, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, sourceID)
	if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
		t.Fatalf("defer request across crash gap: processed=%v error=%v", processed, err)
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='requested'`, f.issueID); count != 1 {
		t.Fatal("request finalized before the promised question was answered")
	}
	worker := NewWorkflowDeliveryWorker(testHandler)
	taskID := assertRecoveredWorkflowComment(t, worker, f.issueID, f.runtimeID, f.coordinatorID, uuidToString(comment.ID))
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET next_attempt_at=now() WHERE issue_id=$1 AND state='requested'`, f.issueID)
	if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
		t.Fatalf("finalize answered request: processed=%v error=%v", processed, err)
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, f.issueID); count != 1 {
		t.Fatal("answered recovered question required a second acceptance")
	}
}

func TestWorkflowRecordedCommentRecoveryAfterAcceptanceClosesNewBirth(t *testing.T) {
	f := workflowPostacceptFixture(t, true)
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM issue WHERE id=$1 FOR UPDATE`, f.issueID); err != nil {
		t.Fatal(err)
	}
	q := testHandler.Queries.WithTx(tx)
	issue, err := q.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	comment, err := q.CreateComment(ctx, db.CreateCommentParams{ID: dbid.NewV7(), IssueID: issue.ID,
		WorkspaceID: issue.WorkspaceID, AuthorType: "member", AuthorID: parseUUID(testUserID),
		Type: "comment", Content: "Please explain the result you just presented."})
	if err != nil {
		t.Fatal(err)
	}
	if matched, err := service.RecordWorkflowHumanComment(ctx, tx, issue, parseUUID(f.coordinatorID),
		f.second.ID, parseUUID(f.coordinatorTaskID), issue.WorkflowCandidateID, comment.ID); err != nil || !matched {
		t.Fatalf("record preacceptance human input: matched=%v error=%v", matched, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	acceptanceID := workflowPostacceptHumanAccept(t, f, true)
	var newBirth, recorded bool
	dbfx.QueryRow(t, `SELECT workflow_human_comment_handoff_eligible($1,$2),workflow_recorded_comment_input_current($1,$3,$4)`,
		f.issueID, f.second.ID, f.coordinatorID, comment.ID).Scan(&newBirth, &recorded)
	if newBirth || !recorded {
		t.Fatalf("expected an existing promise after new birth closed: new=%v recorded=%v", newBirth, recorded)
	}
	worker := NewWorkflowDeliveryWorker(testHandler)
	taskID := assertRecoveredWorkflowComment(t, worker, f.issueID, f.runtimeID, f.coordinatorID, uuidToString(comment.ID))
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET outcome_next_attempt_at=now() WHERE id=$1`, acceptanceID)
	if processed, err := testHandler.workflowAuthorityService().RetryNextWorkflowCompletionDispatch(ctx); err != nil || !processed {
		t.Fatalf("answered promise did not release completed no-PR outcome: processed=%v error=%v", processed, err)
	}
	issue, err = testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil || issue.Status != "done" {
		t.Fatalf("answered no-PR promise did not finish: status=%s error=%v", issue.Status, err)
	}
}

func TestWorkflowRecordedCommentRecoveryWaitsForActiveConversationReplay(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	ctx := context.Background()
	var mutations atomic.Int32
	var merged atomic.Bool
	server := feedbackDeliveryProvider(t, &mutations, &merged, true)
	defer server.Close()
	f := workflowDeliveryFeedbackCandidate(t, server, true)
	// Model a turn dispatched before the accepted candidate's new thread reply.
	parentID := dbfx.Comment(t, f.issueID, "An earlier question in this conversation.")
	dbfx.Exec(t, `UPDATE comment SET created_at=now()-interval '2 minutes',updated_at=now()-interval '2 minutes' WHERE id=$1`, parentID)
	headSHA := testHandler.TaskService.ResolveIssueReviewSHAParam(ctx, parseUUID(f.issueID))
	activeID := dbfx.Task(t, f.coordinatorID, testutil.Cols{"issue_id": f.issueID, "runtime_id": f.runtimeID,
		"status": "dispatched", "created_at": testutil.Raw("now()-interval '1 minute'"),
		"dispatched_at":      testutil.Raw("now()-interval '1 minute'"),
		"trigger_comment_id": parentID, "comment_thread_id": parentID,
		"delivered_comment_ids": testutil.Raw("ARRAY['" + parentID + "']::uuid[]"),
		"context":               testutil.Raw("jsonb_build_object('head_sha','" + headSHA.String + "')"),
		"originator_user_id":    testUserID, "accountable_user_id": testUserID,
		"workflow_policy_version": testutil.Raw("(SELECT workflow_policy_version FROM agent_task_queue WHERE id='" + f.coordinatorTaskID + "')"),
		"workflow_profile_id":     testutil.Raw("(SELECT workflow_profile_id FROM agent_task_queue WHERE id='" + f.coordinatorTaskID + "')")})
	_, comment := recordPromisedWorkflowDeliveryFeedback(t, f, true, "Explain this accepted candidate after your current turn.")
	dbfx.Exec(t, `UPDATE comment SET parent_id=$2 WHERE id=$1`, comment.ID, parentID)
	worker := NewWorkflowDeliveryWorker(testHandler)
	for tick := 0; tick < 2; tick++ {
		if recovered, err := worker.RecoverNextRecordedWorkflowComment(ctx); err != nil || recovered {
			t.Fatalf("active deferral tick %d advanced/spun: recovered=%v error=%v", tick, recovered, err)
		}
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND trigger_comment_id=$2`, f.issueID, comment.ID); count != 0 {
		t.Fatalf("active turn produced %d duplicate conversations", count)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, activeID)
	completed, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(activeID))
	if err != nil {
		t.Fatal(err)
	}
	testHandler.reconcileCommentsOnCompletion(ctx, &completed)
	claim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claim.AgentID != f.coordinatorID || dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(delivered_comment_ids)`, claim.ID, comment.ID) != 1 {
		t.Fatalf("completion replay lost deferred recorded input: %+v", claim)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(claim.ID)); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowRecordedCommentRecoveryPreservesUnavailableAndWithdrawnIntent(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		for _, change := range []string{"archived", "runtime_missing", "deleted", "note", "withdrawn", "frozen", "superseded"} {
			name := "member_handoff/" + change
			if assigned {
				name = "assigned_agent/" + change
			}
			t.Run(name, func(t *testing.T) {
				if testHandler == nil {
					t.Skip("handler test fixture unavailable")
				}
				var mutations atomic.Int32
				var merged atomic.Bool
				server := feedbackDeliveryProvider(t, &mutations, &merged, true)
				defer server.Close()
				f := workflowDeliveryFeedbackCandidate(t, server, true)
				issue, comment := recordPromisedWorkflowDeliveryFeedback(t, f, assigned, "Explain the promised result before delivery.")
				blocked := change == "archived" || change == "runtime_missing"
				switch change {
				case "archived":
					dbfx.Exec(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, f.coordinatorID)
				case "runtime_missing":
					dbfx.Exec(t, `UPDATE agent SET runtime_id=NULL WHERE id=$1`, f.coordinatorID)
				case "deleted":
					dbfx.Exec(t, `UPDATE comment SET deleted_at=now() WHERE id=$1`, comment.ID)
				case "note":
					dbfx.Exec(t, `UPDATE comment SET content='/note Withdraw this agent input' WHERE id=$1`, comment.ID)
				case "withdrawn":
					tx, err := testPool.Begin(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(context.Background())
					if _, err := tx.Exec(context.Background(), `SELECT id FROM issue WHERE id=$1 FOR UPDATE`, issue.ID); err != nil {
						t.Fatal(err)
					}
					if err := service.ReconcileWorkflowCommentRecipients(context.Background(), tx, issue, comment.ID, nil); err != nil {
						t.Fatal(err)
					}
					if err := tx.Commit(context.Background()); err != nil {
						t.Fatal(err)
					}
				case "frozen":
					dbfx.Exec(t, `UPDATE issue SET workflow_frozen=true WHERE id=$1`, f.issueID)
				case "superseded":
					current, err := testHandler.Queries.GetIssue(context.Background(), issue.ID)
					if err != nil {
						t.Fatal(err)
					}
					if err := testHandler.workflowAuthorityService().RejectWorkflow(context.Background(), issue.WorkspaceID, issue.ID,
						service.WorkflowActor{Type: "member", ID: testUserID}, service.WorkflowRejectionInput{
							CandidateID: f.candidateID, ExpectedRevision: current.Revision, Kind: "in_scope_defect",
							Reason: "Replace this candidate before delivery", ResumeTaskID: f.writerTaskID,
						}); err != nil {
						t.Fatalf("supersede accepted candidate through rejection: %v", err)
					}
				}
				worker := NewWorkflowDeliveryWorker(testHandler)
				worker.client = server.Client()
				if recovered, err := worker.RecoverNextRecordedWorkflowComment(context.Background()); err != nil || recovered != blocked {
					t.Fatalf("change=%s recovery=%v want=%v error=%v", change, recovered, blocked, err)
				}
				if count := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND trigger_comment_id=$2`, f.issueID, comment.ID); count != 0 {
					t.Fatalf("change=%s created %d unauthorized conversations", change, count)
				}
				if blocked {
					if count := dbfx.Count(t, `SELECT count(*) FROM activity_log WHERE issue_id=$1 AND action='workflow_comment_recovery_blocked'
					 AND details->>'comment_id'=$2`, f.issueID, uuidToString(comment.ID)); count != 1 {
						t.Fatalf("unavailable recorded recipient has %d truthful blockers", count)
					}
					if recovered, err := worker.RecoverNextRecordedWorkflowComment(context.Background()); err != nil || recovered {
						t.Fatalf("blocked input retried without poll backoff: recovered=%v error=%v", recovered, err)
					}
					if worked, err := worker.ProcessNext(context.Background()); err != nil || !worked || mutations.Load() != 0 {
						t.Fatalf("unavailable promise permitted provider mutation: worked=%v error=%v mutations=%d", worked, err, mutations.Load())
					}
				}
			})
		}
	}
}

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func workflowCommentPending(t *testing.T, f workflowHumanCommentFixture) bool {
	t.Helper()
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	issue, err := testHandler.Queries.WithTx(tx).GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := service.WorkflowHasPendingHumanFeedback(ctx, tx, issue)
	if err != nil {
		t.Fatal(err)
	}
	return pending
}

func workflowCommentEdit(t *testing.T, commentID, content string) CommentResponse {
	t.Helper()
	var response CommentResponse
	testutil.Call(t, testHandler.UpdateComment, withURLParam(newRequest(http.MethodPatch, "/api/comments/"+commentID,
		map[string]any{"content": content}), "commentId", commentID)).Want(http.StatusOK).JSON(&response)
	return response
}

func TestWorkflowEditedCommentRetiresOnlyWithdrawnRecipientProof(t *testing.T) {
	for _, route := range []string{"member", "assigned", "requested"} {
		for _, withdrawal := range []string{"member", "all", "other_agent", "note", "delete"} {
			t.Run(route+"/"+withdrawal, func(t *testing.T) {
				f := workflowHumanCommentFixture{}
				var sourceID string
				if route == "member" {
					f = workflowPostacceptFixture(t, false)
					workflowPostacceptHumanAccept(t, f, false)
				} else {
					var svc service.WorkflowAuthorityService
					f, sourceID, svc = workflowPostacceptRequest(t, false)
					if route == "assigned" {
						dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, sourceID)
						processed, err := svc.FinalizeNextRequestedAcceptance(context.Background())
						if err != nil || !processed {
							t.Fatalf("finalize: %v %v", processed, err)
						}
					}
				}
				question := workflowPostacceptComment(t, f, "Can you explain this candidate before delivery?")
				if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND agent_id=$2
      AND status IN ('queued','deferred','dispatched','running') AND ($3::uuid=trigger_comment_id OR $3::uuid=ANY(coalesced_comment_ids))`, f.issueID, f.coordinatorID, question.ID); got == 0 {
					t.Fatal("question acquired no exact replay/task plan")
				}
				content := ""
				switch withdrawal {
				case "member":
					content = fmt.Sprintf("[@Person](mention://member/%s) please answer instead.", testUserID)
				case "all":
					content = "[@all](mention://all/all) this is now a human discussion."
				case "other_agent":
					other := dbfx.Agent(t, "Other discussion recipient", f.runtimeID)
					content = fmt.Sprintf("[@Other](mention://agent/%s) please answer instead.", other)
				case "note":
					content = "/note I am withdrawing the agent question."
				case "delete":
					testutil.Call(t, testHandler.DeleteComment, withURLParam(newRequest(http.MethodDelete, "/api/comments/"+question.ID, nil), "commentId", question.ID)).Want(http.StatusNoContent)
				}
				if content != "" {
					workflowCommentEdit(t, question.ID, content)
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance a,jsonb_array_elements(a.human_comment_obligations) entry
     WHERE a.issue_id=$1 AND entry->>'comment_id'=$2 AND entry->>'agent_id'=$3`, f.issueID, question.ID, f.coordinatorID); got != 0 {
					t.Fatal("withdrawn accepted recipient proof remained")
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue source WHERE source.issue_id=$1 AND source.agent_id=$2
     AND $3::uuid=ANY(source.coalesced_comment_ids) AND (
      EXISTS(SELECT 1 FROM issue_workflow_acceptance a WHERE a.source_task_id=source.id AND a.state='requested') OR
      EXISTS(SELECT 1 FROM issue_wakeup w WHERE w.filter_task_id=source.id AND w.handoff IS NOT NULL))`, f.issueID, f.coordinatorID, question.ID); got != 0 {
					t.Fatal("withdrawn requested/coordinator source plan remained")
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND agent_id=$2
      AND trigger_comment_id=$3 AND status IN ('queued','deferred','dispatched','running')`, f.issueID, f.coordinatorID, question.ID); got != 0 {
					t.Fatal("withdrawn assigned task remained active")
				}
				if route != "requested" && workflowCommentPending(t, f) {
					t.Fatal("withdrawn recipient still paused delivery")
				}
			})
		}
	}
}

func TestWorkflowEditedNoteRecordsNewAcceptedAssignedQuestion(t *testing.T) {
	f, source, svc := workflowPostacceptRequest(t, false)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, source)
	if processed, err := svc.FinalizeNextRequestedAcceptance(context.Background()); err != nil || !processed {
		t.Fatalf("finalize: %v %v", processed, err)
	}
	note := workflowPostacceptComment(t, f, "/note A private human note.")
	if workflowCommentPending(t, f) {
		t.Fatal("note paused delivery")
	}
	edited := workflowCommentEdit(t, note.ID, "Can you answer this new assigned-agent question?")
	if len(edited.TriggerOutcomes) != 1 || edited.TriggerOutcomes[0].Status != DispatchQueued {
		t.Fatalf("edited question dispatch: %+v", edited.TriggerOutcomes)
	}
	if !workflowCommentPending(t, f) {
		t.Fatal("edited question acquired no delivery pause")
	}
	task := workflowPostacceptTaskID(t, f, note.ID)
	workflowPostacceptStart(t, f, task, note.ID)
}

func TestWorkflowExplicitCoordinatorMentionMatchesImplicitHumanConversation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, edited := range []bool{false, true} {
			t.Run(fmt.Sprintf("explicit=%v/edited=%v", explicit, edited), func(t *testing.T) {
				f := workflowPostacceptFixture(t, false)
				workflowPostacceptHumanAccept(t, f, false)
				content := "Can you explain this accepted candidate's tradeoff?"
				if explicit {
					content = fmt.Sprintf("[@Coordinator](mention://agent/%s) %s", f.coordinatorID, content)
				}
				var response CommentResponse
				if edited {
					note := workflowPostacceptComment(t, f, "/note This draft is not agent input.")
					response = workflowCommentEdit(t, note.ID, content)
				} else {
					response = workflowPostacceptComment(t, f, content)
				}
				if len(response.TriggerOutcomes) != 1 || response.TriggerOutcomes[0].TargetID != f.coordinatorID ||
					response.TriggerOutcomes[0].Status != DispatchQueued {
					t.Fatalf("coordinator dispatch: %+v", response.TriggerOutcomes)
				}
				taskID := workflowPostacceptTaskID(t, f, response.ID)
				if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue task JOIN agent_task_queue source ON source.id=$2
					WHERE task.id=$1 AND task.agent_id=$3 AND task.trigger_evidence_kind='workflow_human_comment'
					AND task.trigger_evidence_ref_id=$4 AND task.delegated_from_task_id=source.id
					AND $5::uuid=ANY(source.coalesced_comment_ids)
					AND task.originator_user_id=$6 AND task.accountable_user_id=$6
					AND workflow_human_comment_task_current(task.id,task.issue_id)`, taskID, f.coordinatorTaskID,
					f.coordinatorID, f.second.ID, response.ID, testUserID); got != 1 {
					t.Fatal("coordinator mention lacks the exact recorded member handoff proof")
				}
				workflowPostacceptStart(t, f, taskID, response.ID)
				if got := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND assignee_type='member' AND assignee_id=$2`,
					f.issueID, testUserID); got != 1 {
					t.Fatal("conversation changed the human assignment")
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, f.issueID); got != 1 {
					t.Fatal("question changed recorded acceptance")
				}
			})
		}
	}
}

func TestWorkflowExplicitCoordinatorMentionDoesNotWidenRecipientsOrCompletedScope(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprintf("completed=%v", completed), func(t *testing.T) {
			f := workflowPostacceptFixture(t, completed)
			workflowPostacceptHumanAccept(t, f, completed)
			target := f.coordinatorID
			if !completed {
				target = dbfx.Agent(t, "Unrelated explicit recipient", f.runtimeID)
			}
			response := workflowPostacceptComment(t, f, fmt.Sprintf("[@Agent](mention://agent/%s) Please answer this new question.", target))
			if len(response.TriggerOutcomes) != 1 || response.TriggerOutcomes[0].TargetID != target ||
				response.TriggerOutcomes[0].Status != DispatchBlocked {
				t.Fatalf("ineligible explicit recipient: %+v", response.TriggerOutcomes)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND trigger_comment_id=$2`, f.issueID, response.ID); got != 0 {
				t.Fatal("ineligible mention created an unclaimable task")
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(coalesced_comment_ids)`,
				f.coordinatorTaskID, response.ID); got != 0 {
				t.Fatal("ineligible mention acquired coordinator source-plan authority")
			}
		})
	}
}

type workflowCommentBeginGate struct {
	base             txStarter
	entered, release chan struct{}
}

func (g workflowCommentBeginGate) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := g.base.Begin(ctx)
	if err != nil {
		return nil, err
	}
	close(g.entered)
	select {
	case <-g.release:
		return tx, nil
	case <-ctx.Done():
		tx.Rollback(ctx)
		return nil, ctx.Err()
	}
}

func TestWorkflowCommentLockedRoutingAfterFinalizerWins(t *testing.T) {
	f, source, svc := workflowPostacceptRequest(t, false)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, source)
	gate := workflowCommentBeginGate{base: testHandler.TxStarter, entered: make(chan struct{}), release: make(chan struct{})}
	h := *testHandler
	h.TxStarter = gate
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.CreateComment(w, withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/comments", map[string]any{"content": "A question saved after the approval lock wins."}), "id", f.issueID))
		done <- w
	}()
	<-gate.entered
	if processed, err := svc.FinalizeNextRequestedAcceptance(context.Background()); err != nil || !processed {
		close(gate.release)
		t.Fatalf("finalize: %v %v", processed, err)
	}
	close(gate.release)
	w := <-done
	if w.Code != http.StatusCreated {
		t.Fatalf("comment: %d %s", w.Code, w.Body.String())
	}
	var response CommentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.TriggerOutcomes) != 1 || response.TriggerOutcomes[0].Status != DispatchQueued {
		t.Fatalf("stale preview stranded accepted question: %+v", response.TriggerOutcomes)
	}
	if !workflowCommentPending(t, f) {
		t.Fatal("finalizer-first question acquired no durable delivery pause")
	}
	task := workflowPostacceptTaskID(t, f, response.ID)
	workflowPostacceptStart(t, f, task, response.ID)
}

func TestWorkflowCommentLockedRoutingRefusesUnavailableAcceptedRecipient(t *testing.T) {
	f, source, svc := workflowPostacceptRequest(t, false)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, source)
	if processed, err := svc.FinalizeNextRequestedAcceptance(context.Background()); err != nil || !processed {
		t.Fatalf("finalize: %v %v", processed, err)
	}
	gate := workflowCommentBeginGate{base: testHandler.TxStarter, entered: make(chan struct{}), release: make(chan struct{})}
	h := *testHandler
	h.TxStarter = gate
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.CreateComment(w, withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/comments", map[string]any{"content": "This recipient becomes unavailable before the write lock."}), "id", f.issueID))
		done <- w
	}()
	<-gate.entered
	dbfx.Exec(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, f.coordinatorID)
	close(gate.release)
	w := <-done
	if w.Code != http.StatusCreated {
		t.Fatalf("comment: %d %s", w.Code, w.Body.String())
	}
	var response CommentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.TriggerOutcomes) != 1 || response.TriggerOutcomes[0].Status != DispatchBlocked {
		t.Fatalf("unavailable target promised a reply: %+v", response.TriggerOutcomes)
	}
	if workflowCommentPending(t, f) {
		t.Fatal("unavailable recipient orphaned a durable obligation")
	}
}

func TestWorkflowOutcomeWaitsForPromisedQuestionBeforeChangingRecipient(t *testing.T) {
	for _, route := range []string{"member", "assigned"} {
		t.Run(route, func(t *testing.T) {
			f := workflowHumanCommentFixture{}
			if route == "member" {
				f = workflowPostacceptFixtureWithOutcome(t, false, true)
				workflowPostacceptHumanAccept(t, f, false)
			} else {
				var source string
				var svc service.WorkflowAuthorityService
				f, source, svc = workflowPostacceptRequestWithOutcome(t, false, true)
				dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, source)
				if processed, err := svc.FinalizeNextRequestedAcceptance(context.Background()); err != nil || !processed {
					t.Fatalf("finalize: %v %v", processed, err)
				}
			}
			var other string
			if err := testPool.QueryRow(context.Background(), `SELECT outcome_agent_id::text FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, f.issueID).Scan(&other); err != nil {
				t.Fatal(err)
			}
			question := workflowPostacceptComment(t, f, "Please answer this before moving to the distinct outcome agent.")
			task := workflowPostacceptTaskID(t, f, question.ID)
			ctx := context.Background()
			tx, err := testPool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			q := testHandler.Queries.WithTx(tx)
			issue, err := q.LockWakeupIssue(ctx, parseUUID(f.issueID))
			if err != nil {
				t.Fatal(err)
			}
			var acceptanceID string
			if err = tx.QueryRow(ctx, `UPDATE issue_workflow_delivery SET status='delivered',merged_at=now() WHERE issue_id=$1 RETURNING acceptance_id::text`, f.issueID).Scan(&acceptanceID); err != nil {
				t.Fatal(err)
			}
			outcome, closed, _, err := service.TryReconcileWorkflowCompletion(ctx, tx, q, issue, parseUUID(acceptanceID))
			if err != nil || outcome != nil || closed {
				t.Fatalf("pending question was superseded: task=%v done=%v err=%v", outcome, closed, err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND assignee_id=$2`, f.issueID, issue.AssigneeID); got != 1 {
				t.Fatal("provider completion replaced promised recipient")
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND last_error_class='human_feedback_pending'`, acceptanceID); got != 1 {
				t.Fatal("pending question has no durable retry state")
			}
			workflowPostacceptStart(t, f, task, question.ID)
			dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, task)
			dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET outcome_next_attempt_at=now()-interval '1 second' WHERE id=$1`, acceptanceID)
			svc := testHandler.workflowAuthorityService()
			if processed, err := svc.RetryNextWorkflowCompletionDispatch(ctx); err != nil || !processed {
				t.Fatalf("resume outcome after answer: %v %v", processed, err)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM issue i JOIN issue_workflow_acceptance a ON a.issue_id=i.id
    JOIN agent_task_queue outcome ON outcome.id=a.outcome_task_id WHERE i.id=$1 AND i.assignee_type='agent'
    AND i.assignee_id=$2 AND outcome.agent_id=$2 AND a.last_error_class IS NULL`, f.issueID, other); got != 1 {
				t.Fatal("answered question did not release distinct outcome assignment")
			}
		})
	}
}

func TestWorkflowNoPRAcceptanceRetainsApprovalWhileQuestionDefersOutcome(t *testing.T) {
	f := workflowPostacceptFixture(t, true)
	question := workflowPostacceptComment(t, f, "Please answer before starting the remaining outcome work.")
	task := workflowPostacceptTaskID(t, f, question.ID)
	acceptanceID := workflowPostacceptHumanAccept(t, f, false)
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND state='accepted'
   AND last_error_class='human_feedback_pending' AND outcome_task_id IS NULL`, acceptanceID); got != 1 {
		t.Fatal("question collision rolled back approval or lost durable outcome retry")
	}
	workflowPostacceptStart(t, f, task, question.ID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, task)
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET outcome_next_attempt_at=now()-interval '1 second' WHERE id=$1`, acceptanceID)
	svc := testHandler.workflowAuthorityService()
	if processed, err := svc.RetryNextWorkflowCompletionDispatch(context.Background()); err != nil || !processed {
		t.Fatalf("outcome retry: %v %v", processed, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND state='accepted' AND outcome_task_id IS NOT NULL`, acceptanceID); got != 1 {
		t.Fatal("answer did not release retained outcome work")
	}
}

func TestWorkflowAdminEditDoesNotBorrowOriginalMemberInvocation(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprintf("accepted=%v", accepted), func(t *testing.T) {
			f := workflowHumanCommentFixture{}
			if accepted {
				var source string
				var svc service.WorkflowAuthorityService
				f, source, svc = workflowPostacceptRequest(t, false)
				dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, source)
				if processed, err := svc.FinalizeNextRequestedAcceptance(context.Background()); err != nil || !processed {
					t.Fatalf("finalize: %v %v", processed, err)
				}
			} else {
				f = setupWorkflowHumanCommentFixture(t)
				dbfx.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2 WHERE id=$1`, f.issueID, f.coordinatorID)
			}
			author := dbfx.User(t, "Original private recipient member", "original-route@multica.test")
			dbfx.Member(t, testWorkspaceID, author, "admin")
			f.allowCoordinatorInvocation(t, author)
			var question CommentResponse
			testutil.Call(t, testHandler.CreateComment, withURLParam(newRequestAs(author, http.MethodPost, "/api/issues/"+f.issueID+"/comments",
				map[string]any{"content": "My original question for the permitted coordinator."}), "id", f.issueID)).Want(http.StatusCreated).JSON(&question)
			oldTask := workflowPostacceptTaskID(t, f, question.ID)
			newAgent := dbfx.Agent(t, "Editor private target", f.runtimeID)
			workflowCommentEdit(t, question.ID, fmt.Sprintf("[@Private](mention://agent/%s) edited by another human.", newAgent))
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND agent_id=$2 AND trigger_comment_id=$3`, f.issueID, newAgent, question.ID); got != 0 {
				t.Fatal("admin edit borrowed original human attribution/private credentials")
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='cancelled' AND originator_user_id=$2 AND accountable_user_id=$2`, oldTask, author); got != 1 {
				t.Fatal("old task was restamped or was not withdrawn")
			}
			if accepted && workflowCommentPending(t, f) {
				t.Fatal("admin reroute orphaned original recipient proof")
			}
		})
	}
}

func TestWorkflowNoPROutcomeCompleteWaitsForPromisedQuestion(t *testing.T) {
	for _, assigned := range []bool{false, true} {
		for _, withdraw := range []bool{false, true} {
			t.Run(fmt.Sprintf("assigned=%v/withdraw=%v", assigned, withdraw), func(t *testing.T) {
				f := workflowPostacceptFixture(t, true)
				if assigned {
					dbfx.Exec(t, `UPDATE issue_wakeup SET enabled=false,disabled_at=now() WHERE id=$1`, f.second.ID)
					dbfx.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2,revision=revision+1 WHERE id=$1`, f.issueID, f.coordinatorID)
				}
				question := workflowPostacceptComment(t, f, "Please answer this question before final completion.")
				task := workflowPostacceptTaskID(t, f, question.ID)
				acceptanceID := workflowPostacceptHumanAccept(t, f, true)
				if got := dbfx.Count(t, `SELECT count(*) FROM issue i JOIN issue_workflow_acceptance a ON a.issue_id=i.id
      WHERE i.id=$1 AND a.id=$2 AND i.status=a.accepted_status_key AND a.outcome_complete
       AND a.last_error_class='human_feedback_pending'`, f.issueID, acceptanceID); got != 1 {
					t.Fatal("human acceptance closed before promised conversation resolved")
				}
				if withdraw {
					testutil.Call(t, testHandler.DeleteComment, withURLParam(newRequest(http.MethodDelete, "/api/comments/"+question.ID, nil), "commentId", question.ID)).Want(http.StatusNoContent)
				} else {
					workflowPostacceptStart(t, f, task, question.ID)
					dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, task)
				}
				dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET outcome_next_attempt_at=now()-interval '1 second' WHERE id=$1`, acceptanceID)
				svc := testHandler.workflowAuthorityService()
				if processed, err := svc.RetryNextWorkflowCompletionDispatch(context.Background()); err != nil || !processed {
					t.Fatalf("finalclose retry: %v %v", processed, err)
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM issue i JOIN issue_workflow_acceptance a ON a.issue_id=i.id
      WHERE i.id=$1 AND i.status='done' AND a.id=$2 AND a.last_error_class IS NULL`, f.issueID, acceptanceID); got != 1 {
					t.Fatal("answer/withdrawal did not release final close")
				}
			})
		}
	}
}

type workflowCommentCommitGate struct {
	base               txStarter
	committed, release chan struct{}
}
type workflowCommentGatedTx struct {
	pgx.Tx
	committed, release chan struct{}
}

func (g workflowCommentCommitGate) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := g.base.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return workflowCommentGatedTx{Tx: tx, committed: g.committed, release: g.release}, nil
}
func (g workflowCommentGatedTx) Commit(ctx context.Context) error {
	if err := g.Tx.Commit(ctx); err != nil {
		return err
	}
	close(g.committed)
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestWorkflowRequestedAcceptanceWaitsForEditedVersionAfterSourceDeliveredOriginal(t *testing.T) {
	f := workflowPostacceptFixture(t, true)
	ctx := context.Background()
	original := workflowPostacceptComment(t, f, "Please evaluate this candidate under the configured acceptance policy.")
	source := workflowPostacceptTaskID(t, f, original.ID)
	workflowPostacceptStart(t, f, source, original.ID)
	// The same exact coordinator may become the current assignee without a
	// ceremonial additional run. Its real receipt still describes the old body.
	dbfx.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2,revision=revision+1 WHERE id=$1`, f.issueID, f.coordinatorID)
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	svc := testHandler.workflowAuthorityService()
	complete := true
	if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issue.ID, service.WorkflowActor{Type: "agent", ID: f.coordinatorID, SourceTaskID: source}, service.WorkflowAcceptanceInput{
		CandidateID: uuidToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision, OutcomeComplete: &complete, ClassificationReason: "The configured candidate remains unchanged.",
	}); err != nil || state != "requested" {
		t.Fatalf("request: %s %v", state, err)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, source)
	gate := workflowCommentCommitGate{base: testHandler.TxStarter, committed: make(chan struct{}), release: make(chan struct{})}
	h := *testHandler
	h.TxStarter = gate
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.UpdateComment(w, withURLParam(newRequest(http.MethodPatch, "/api/comments/"+original.ID, map[string]any{"content": "The delivered question has a new detail that must be answered first."}), "commentId", original.ID))
		done <- w
	}()
	<-gate.committed
	processed, err := svc.FinalizeNextRequestedAcceptance(ctx)
	if err != nil || !processed {
		close(gate.release)
		t.Fatalf("finalize edited input: %v %v", processed, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='requested' AND last_error_class='human_comment_pending'`, f.issueID); got != 1 {
		close(gate.release)
		t.Fatal("old delivered receipt consumed edited input")
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue source JOIN comment c ON c.id=$2
  WHERE source.id=$1 AND c.id=ANY(source.delivered_comment_ids) AND source.dispatched_at<c.updated_at`, source, original.ID); got != 1 {
		close(gate.release)
		t.Fatal("original claim receipt was modified")
	}
	close(gate.release)
	w := <-done
	if w.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", w.Code, w.Body.String())
	}
	var response CommentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	followup := workflowPostacceptTaskID(t, f, original.ID)
	workflowPostacceptStart(t, f, followup, original.ID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, followup)
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET next_attempt_at=now() WHERE issue_id=$1`, f.issueID)
	if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
		t.Fatalf("approval after edited question answered: %v %v", processed, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, f.issueID); got != 1 {
		t.Fatal("unchanged approval did not finalize after edited answer")
	}
}

func TestWorkflowAgentOutcomeAcknowledgmentWaitsForPromisedInput(t *testing.T) {
	for _, correction := range []bool{false, true} {
		t.Run(fmt.Sprintf("correction=%v", correction), func(t *testing.T) {
			f := workflowPostacceptFixture(t, true)
			acceptanceID := workflowPostacceptHumanAccept(t, f, false)
			var outcome string
			if err := testPool.QueryRow(context.Background(), `SELECT outcome_task_id::text FROM issue_workflow_acceptance WHERE id=$1`, acceptanceID).Scan(&outcome); err != nil {
				t.Fatal(err)
			}
			// Outcome tasks are ordinary exact native tasks; claim through the real
			// daemon endpoint before recording the agent's pending acknowledgment.
			claimed := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
			if claimed.ID != outcome {
				t.Fatalf("outcome claim: %+v", claimed)
			}
			if _, err := testHandler.TaskService.StartTask(context.Background(), parseUUID(outcome)); err != nil {
				t.Fatal(err)
			}
			current, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(f.issueID))
			if err != nil {
				t.Fatal(err)
			}
			req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/workflow/outcome", map[string]any{
				"candidate_id": uuidToString(current.WorkflowCandidateID), "expected_revision": current.Revision, "reason": "The assigned outcome work is complete.",
			}), "id", f.issueID)
			req = withURLParams(req, "acceptanceID", acceptanceID)
			req.Header.Set("X-Agent-ID", f.coordinatorID)
			req.Header.Set("X-Task-ID", outcome)
			req.Header.Set("X-Actor-Source", "task_token")
			testutil.Call(t, testHandler.CompleteIssueWorkflowOutcome, req).Want(http.StatusOK)
			question := workflowPostacceptComment(t, f, "Please classify this human input before the outcome acknowledgment.")
			dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, outcome)
			svc := testHandler.workflowAuthorityService()
			if processed, err := svc.FinalizeNextOutcomeAcknowledgment(context.Background()); err != nil || !processed {
				t.Fatalf("defer outcome acknowledgment: %v %v", processed, err)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND NOT outcome_complete AND outcome_request_task_id=$2 AND last_error_class='human_feedback_pending'`, acceptanceID, outcome); got != 1 {
				t.Fatal("automatic acknowledgment consumed unclassified input")
			}
			task := workflowPostacceptTaskID(t, f, question.ID)
			workflowPostacceptStart(t, f, task, question.ID)
			if correction {
				testutil.Call(t, testHandler.ContinueIssueWorkflowFeedback, workflowPostacceptCorrectionRequest(t, f, task, question.ID)).Want(http.StatusOK)
				if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND state='revoked' AND NOT outcome_complete`, acceptanceID); got != 1 {
					t.Fatal("promised correction lost its outcome window")
				}
			} else {
				dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, task)
				dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET outcome_next_attempt_at=now()-interval '1 second' WHERE id=$1`, acceptanceID)
				if processed, err := svc.FinalizeNextOutcomeAcknowledgment(context.Background()); err != nil || !processed {
					t.Fatalf("finish acknowledgment after answer: %v %v", processed, err)
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM issue i JOIN issue_workflow_acceptance a ON a.issue_id=i.id WHERE i.id=$1 AND a.id=$2 AND a.outcome_complete AND i.status='done'`, f.issueID, acceptanceID); got != 1 {
					t.Fatal("answered input did not release final outcome")
				}
			}
		})
	}
}

package handler

import (
	"context"
	"github.com/multica-ai/multica/server/internal/service"
	"sync/atomic"
	"testing"
)

func TestWorkflowDeliveryRetryFollowsHumanIntentAcrossStatuses(t *testing.T) {
	if testHandler == nil {
		t.Skip("database required")
	}
	for _, state := range []string{"blocked", "retry"} {
		t.Run(state, func(t *testing.T) {
			var mutations atomic.Int32
			var merged atomic.Bool
			provider := feedbackDeliveryProvider(t, &mutations, &merged, true)
			defer provider.Close()
			f := workflowDeliveryFeedbackCandidate(t, provider, true)
			dbfx.Exec(t, `UPDATE issue SET status='todo',revision=revision+1 WHERE id=$1`, f.issueID)
			dbfx.Exec(t, `UPDATE issue_workflow_delivery SET status=$2,next_attempt_at=now()+interval '10 minutes' WHERE id=$1`, f.deliveryID, state)
			issue, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(f.issueID))
			if err != nil {
				t.Fatal(err)
			}
			changed, err := testHandler.workflowAuthorityService().RetryDelivery(context.Background(), issue.WorkspaceID, issue.ID, parseUUID(f.deliveryID), service.WorkflowActor{Type: "member", ID: testUserID}, service.WorkflowDeliveryRetryInput{CandidateID: f.candidateID, ExpectedRevision: issue.Revision, Reason: "Retry now after the prerequisite was corrected"})
			if err != nil || !changed {
				t.Fatalf("human retry refused: %v, %v", changed, err)
			}
			if n := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE id=$1 AND status='retry' AND next_attempt_at<=now()`, f.deliveryID); n != 1 {
				t.Fatalf("retry failed to reschedule: %d", n)
			}
			if mutations.Load() != 0 {
				t.Fatal("retry directly mutated provider")
			}
		})
	}
}

func TestWorkflowRecordedCommentRecoverySurvivesEditorialChange(t *testing.T) {
	if testHandler == nil {
		t.Skip("database required")
	}
	var mutations atomic.Int32
	var merged atomic.Bool
	provider := feedbackDeliveryProvider(t, &mutations, &merged, true)
	defer provider.Close()
	f := workflowDeliveryFeedbackCandidate(t, provider, true)
	_, comment := recordPromisedWorkflowDeliveryFeedback(t, f, false, "Explain the result before delivery")
	dbfx.Exec(t, `UPDATE issue SET title='Clearer ticket title',revision=revision+1 WHERE id=$1`, f.issueID)
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = provider.Client()
	assertRecoveredWorkflowComment(t, worker, f.issueID, f.runtimeID, f.coordinatorID, uuidToString(comment.ID))
}

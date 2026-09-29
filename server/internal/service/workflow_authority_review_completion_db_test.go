package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
)

func TestWorkflowHumanOnlyReviewWaitsForCompletion(t *testing.T) {
	f, svc, issueID, reviewerTask, reviewerAgent := workflowReviewOriginFixture(t, "fresh")
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := WorkflowActor{Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(reviewerTask)}
	if err := svc.RegisterReview(ctx, issue.WorkspaceID, issueID, reviewer, WorkflowReviewInput{
		CandidateID: util.UUIDToString(issue.WorkflowCandidateID), Verdict: "pass", PRReviewURLs: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	human := WorkflowActor{Type: "member", ID: f.UserID}
	for _, actor := range []WorkflowActor{reviewer, human} {
		view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(view.AcceptanceBlockers, "review_pending_completion") || slices.Contains(view.AcceptanceBlockers, "review_not_independent") {
			t.Fatalf("valid running review misclassified for %s: %v", actor.Type, view.AcceptanceBlockers)
		}
		if view.AvailableActions.AcceptHuman || view.AvailableActions.RequestTrivialAcceptance || view.AvailableActions.RequestReviewedAcceptance {
			t.Fatalf("human-only pending review exposed acceptance to %s: %+v", actor.Type, view.AvailableActions)
		}
	}
	request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision}
	if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, human, request); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("human acceptance while review running = %v, want conflict", err)
	}
	if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, reviewer, request); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("human-only reviewer acceptance = %v, want forbidden", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, issueID); got != 0 {
		t.Fatalf("pending review recorded %d acceptance rows", got)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, reviewerTask)
	view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, human)
	if err != nil || len(view.AcceptanceBlockers) != 0 || !view.AvailableActions.AcceptHuman {
		t.Fatalf("completed review did not release human acceptance: %+v, %v", view, err)
	}
	if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, human, request); err != nil || state != "accepted" {
		t.Fatalf("completed review human acceptance = %q, %v", state, err)
	}
}

func TestWorkflowInvalidRunningReviewIsNotPendingCompletion(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sql         string
		issueScoped bool
	}{
		{"failed reviewer", `UPDATE agent_task_queue SET status='failed' WHERE id=$1`, false},
		{"cancelled reviewer", `UPDATE agent_task_queue SET status='cancelled' WHERE id=$1`, false},
		{"writer incomplete", `UPDATE agent_task_queue SET status='running' WHERE id=(SELECT writer_task_id FROM issue_workflow_candidate WHERE issue_id=$1)`, true},
		{"shared provider session", `UPDATE agent_task_queue SET session_id='writer-session' WHERE id=$1`, false},
		{"missing reviewer session", `UPDATE agent_task_queue SET session_id=NULL WHERE id=$1`, false},
		{"missing writer session", `UPDATE agent_task_queue SET session_id=NULL WHERE id=(SELECT writer_task_id FROM issue_workflow_candidate WHERE issue_id=$1)`, true},
		{"not fresh", `UPDATE agent_task_queue SET force_fresh_session=false WHERE id=$1`, false},
		{"resumed origin", `UPDATE agent_task_queue SET context=jsonb_set(context,'{workflow_handoff,context_mode}','"resume"'::jsonb) WHERE id=$1`, false},
		{"missing genuine handoff", `UPDATE agent_task_queue SET trigger_evidence_ref_id=NULL WHERE id=$1`, false},
		{"same writer task", `UPDATE issue_workflow_review SET reviewer_task_id=(SELECT writer_task_id FROM issue_workflow_candidate WHERE issue_id=$1) WHERE issue_id=$1`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, svc, issueID, reviewerTask, reviewerAgent := workflowReviewOriginFixture(t, "fresh")
			ctx := context.Background()
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.RegisterReview(ctx, issue.WorkspaceID, issueID, WorkflowActor{
				Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(reviewerTask),
			}, WorkflowReviewInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), Verdict: "pass", PRReviewURLs: []string{}}); err != nil {
				t.Fatal(err)
			}
			target := reviewerTask
			if tc.issueScoped {
				target = issueID
			}
			f.Exec(t, tc.sql, target)
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(view.AcceptanceBlockers, "review_not_independent") || slices.Contains(view.AcceptanceBlockers, "review_pending_completion") || view.AvailableActions.AcceptHuman {
				t.Fatalf("invalid review classified as ordinary pending completion: %+v", view)
			}
			if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
				CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
			}); !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("invalid review human acceptance = %v, want conflict", err)
			}
		})
	}
}

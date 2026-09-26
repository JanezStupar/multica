package service

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func TestWorkflowReviewAndExceptionAuthorityAcrossStatusCategories(t *testing.T) {
	for _, tc := range []struct {
		status, category string
		allowed          bool
	}{
		{"backlog", "", true}, {"todo", "", true}, {"in_progress", "", true},
		{"in_review", "", true}, {"blocked", "", true},
		{"custom_unstarted", "unstarted", true}, {"custom_started", "started", true},
		{"cancelled", "", false}, {"done", "", false}, {"custom_closed", "closed", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			f, svc, issueID, taskID, reviewer := workflowReviewOriginFixture(t, "fresh")
			ctx := context.Background()
			if tc.category != "" {
				f.Insert(t, "issue_status", testutil.Cols{"workspace_id": f.WorkspaceID,
					"key": tc.status, "name": tc.status, "category": tc.category, "color": "#22c55e"})
			}
			baselineReviews := 0
			if tc.status == "done" {
				// Enrolled work reaches done only through legitimate completed
				// review and acceptance; do not bypass the completion trigger.
				before, err := f.q.GetIssue(ctx, issueID)
				if err != nil {
					t.Fatal(err)
				}
				if err := svc.RegisterReview(ctx, before.WorkspaceID, issueID,
					WorkflowActor{Type: "agent", ID: reviewer, SourceTaskID: util.UUIDToString(taskID)},
					WorkflowReviewInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID), Verdict: "pass", PRReviewURLs: []string{}}); err != nil {
					t.Fatal(err)
				}
				f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
				if state, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID,
					WorkflowActor{Type: "member", ID: f.UserID}, WorkflowAcceptanceInput{
						CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision,
					}); err != nil || state != "accepted" {
					t.Fatalf("complete reviewed fixture: state=%s error=%v", state, err)
				}
				baselineReviews = 1
			} else {
				f.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, issueID, tc.status)
			}
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			if issue.Status != tc.status {
				t.Fatalf("fixture status=%s, want %s", issue.Status, tc.status)
			}
			candidateID := util.UUIDToString(issue.WorkflowCandidateID)
			err = svc.RegisterReview(ctx, issue.WorkspaceID, issueID,
				WorkflowActor{Type: "agent", ID: reviewer, SourceTaskID: util.UUIDToString(taskID)},
				WorkflowReviewInput{CandidateID: candidateID, Verdict: "pass", PRReviewURLs: []string{}})
			if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("review status eligibility allowed=%v error=%v", tc.allowed, err)
			}
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			input := WorkflowExceptionInput{CandidateID: candidateID, ExpectedRevision: issue.Revision,
				Scope: "review", GrantDetails: map[string]any{"waive": true},
				Reason: "Review exception for this exact candidate", Consequences: "Waive its passing review requirement"}
			exceptionID, err := svc.GrantException(ctx, issue.WorkspaceID, issueID, actor, input)
			if tc.allowed && err != nil || !tc.allowed && !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("exception status eligibility allowed=%v error=%v", tc.allowed, err)
			}
			if !tc.allowed {
				if count := f.Count(t, `SELECT count(*) FROM issue_workflow_review WHERE issue_id=$1`, issueID); count != baselineReviews {
					t.Fatalf("terminal work changed review count from %d to %d", baselineReviews, count)
				}
				if count := f.Count(t, `SELECT count(*) FROM issue_workflow_exception WHERE issue_id=$1`, issueID); count != 0 {
					t.Fatalf("terminal work recorded %d exceptions", count)
				}
				return
			}
			if err := svc.RevokeException(ctx, issue.WorkspaceID, issueID, parseTestUUID(t, exceptionID), actor,
				WorkflowExceptionRevokeInput{ExpectedRevision: issue.Revision + 1,
					Reason: "Restore the candidate review requirement", Consequences: "A passing review is required"}); err != nil {
				t.Fatalf("revoke on %s: %v", tc.status, err)
			}
			if count := f.Count(t, `SELECT count(*) FROM issue_workflow_exception WHERE issue_id=$1 AND revoked_at IS NOT NULL`, issueID); count != 1 {
				t.Fatalf("revocation retained %d historical grants", count)
			}
		})
	}
}

func TestWorkflowStatusEligibilityDoesNotGrantArbitraryAgentAuthority(t *testing.T) {
	f, svc, issueID, taskID, _ := workflowReviewOriginFixture(t, "fresh")
	ctx := context.Background()
	f.Exec(t, `UPDATE issue SET status='blocked',revision=revision+1 WHERE id=$1`, issueID)
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "agent", ID: util.UUIDToString(dbid.NewV7()), SourceTaskID: util.UUIDToString(taskID)}
	candidateID := util.UUIDToString(issue.WorkflowCandidateID)
	if err := svc.RegisterReview(ctx, issue.WorkspaceID, issueID, actor,
		WorkflowReviewInput{CandidateID: candidateID, Verdict: "pass", PRReviewURLs: []string{}}); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("unassigned agent registered a review: %v", err)
	}
	if _, err := svc.GrantException(ctx, issue.WorkspaceID, issueID, actor, WorkflowExceptionInput{
		CandidateID: candidateID, ExpectedRevision: issue.Revision, Scope: "review",
		GrantDetails: map[string]any{"waive": true}, Reason: "Unconfigured supervisor", Consequences: "Waive review",
	}); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("unconfigured agent granted an exception: %v", err)
	}
	if count := f.Count(t, `SELECT count(*) FROM issue_workflow_review WHERE issue_id=$1`, issueID); count != 0 {
		t.Fatalf("arbitrary agent recorded %d reviews", count)
	}
	if count := f.Count(t, `SELECT count(*) FROM issue_workflow_exception WHERE issue_id=$1`, issueID); count != 0 {
		t.Fatalf("arbitrary agent recorded %d exceptions", count)
	}
}

func TestWorkflowExceptionRevocationDoesNotReviveCancelledWork(t *testing.T) {
	f, svc, issueID, _, _ := workflowReviewOriginFixture(t, "fresh")
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	id, err := svc.GrantException(ctx, issue.WorkspaceID, issueID, actor, WorkflowExceptionInput{
		CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
		Scope: "review", GrantDetails: map[string]any{"waive": true}, Reason: "Review unavailable", Consequences: "Waive review",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE issue SET status='cancelled',revision=revision+1 WHERE id=$1`, issueID)
	if err := svc.RevokeException(ctx, issue.WorkspaceID, issueID, parseTestUUID(t, id), actor,
		WorkflowExceptionRevokeInput{ExpectedRevision: issue.Revision + 2,
			Reason: "Try changing cancelled authority", Consequences: "Would remove review exception"}); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("cancelled work changed authority: %v", err)
	}
	if count := f.Count(t, `SELECT count(*) FROM issue_workflow_exception WHERE id=$1 AND revoked_at IS NULL`, id); count != 1 {
		t.Fatalf("cancelled work changed %d live grants", count)
	}
}

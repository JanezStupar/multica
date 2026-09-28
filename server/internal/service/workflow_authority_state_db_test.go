package service

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
)

func TestWorkflowStateDeliveryBlockerDistinguishesOrderFromMissingGrant(t *testing.T) {
	policy := WorkflowAuthorityPolicy{MultiPRMergeOrder: "explicit"}
	if got := workflowStateDeliveryBlocker(policy, "merge", 2, nil); got != "merge_order_required" {
		t.Fatalf("ordered merge blocker = %q", got)
	}
	if got := workflowStateDeliveryBlocker(policy, "ready", 2, nil); got != "" {
		t.Fatalf("ready delivery blocker = %q", got)
	}
	policy.MultiPRMergeOrder = ""
	if got := workflowStateDeliveryBlocker(policy, "merge", 2, nil); got != "merge_not_granted" {
		t.Fatalf("ungranted merge blocker = %q", got)
	}
	if got := workflowStateDeliveryBlocker(policy, "merge", 2, map[string]any{"action": "merge"}); got != "merge_order_required" {
		t.Fatalf("granted merge blocker = %q", got)
	}
}

func TestWorkflowAuthorityReadStateSeparatesReviewedAndTrivialRoutes(t *testing.T) {
	f, svc, issueID, acceptorTask := workflowAutonomousCandidateWithMode(t, "both")
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "agent", ID: util.UUIDToString(issue.AssigneeID), SourceTaskID: util.UUIDToString(acceptorTask)}
	view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
	if err != nil {
		t.Fatal(err)
	}
	if !view.AvailableActions.RequestTrivialAcceptance || !view.AvailableActions.RequestReviewedAcceptance {
		t.Fatalf("both configured routes not exposed: %+v", view.AvailableActions)
	}
	if view.DeliveryPreview == nil || view.DeliveryPreview.Action != "ready" {
		t.Fatalf("trivial delivery preview = %+v", view.DeliveryPreview)
	}
	if view.ReviewedDeliveryPreview == nil || view.ReviewedDeliveryPreview.Action != "merge" || view.ReviewedDeliveryPreview.MergeMethod != "squash" {
		t.Fatalf("reviewed delivery preview = %+v", view.ReviewedDeliveryPreview)
	}

	f.Exec(t, `UPDATE issue_workflow_review SET verdict='changes_requested' WHERE issue_id=$1 AND candidate_id=$2`, issueID, issue.WorkflowCandidateID)
	issue, err = f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GrantException(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: f.UserID}, WorkflowExceptionInput{
		CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
		Scope: "review", GrantDetails: map[string]any{"waive": true},
		Reason: "The exact candidate has a scoped review waiver.", Consequences: "Trivial acceptance may use the waiver; reviewed acceptance still requires an independent pass.",
	}); err != nil {
		t.Fatal(err)
	}
	view, err = svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
	if err != nil {
		t.Fatal(err)
	}
	if !view.AvailableActions.RequestTrivialAcceptance || view.AvailableActions.RequestReviewedAcceptance {
		t.Fatalf("waiver route availability = %+v", view.AvailableActions)
	}
	for _, blocker := range view.AcceptanceBlockers {
		if blocker == "review_not_independent" {
			t.Fatalf("reviewed-only blocker leaked while trivial waiver route is available: %v", view.AcceptanceBlockers)
		}
	}
}

func TestWorkflowAuthorityReadStateReviewedRouteRejectsReviewWaiver(t *testing.T) {
	f, svc, issueID, acceptorTask := workflowAutonomousCandidateWithMode(t, "reviewed")
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE issue_workflow_review SET verdict='changes_requested' WHERE issue_id=$1 AND candidate_id=$2`, issueID, issue.WorkflowCandidateID)
	issue, err = f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GrantException(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: f.UserID}, WorkflowExceptionInput{
		CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
		Scope: "review", GrantDetails: map[string]any{"waive": true},
		Reason: "The exact candidate has a scoped review waiver.", Consequences: "Reviewed acceptance must still require an independent pass.",
	}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, WorkflowActor{
		Type: "agent", ID: util.UUIDToString(issue.AssigneeID), SourceTaskID: util.UUIDToString(acceptorTask),
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.AvailableActions.RequestTrivialAcceptance || view.AvailableActions.RequestReviewedAcceptance {
		t.Fatalf("review waiver enabled an autonomous route: %+v", view.AvailableActions)
	}
	found := false
	for _, blocker := range view.AcceptanceBlockers {
		found = found || blocker == "review_not_independent"
	}
	if !found {
		t.Fatalf("reviewed route omitted independent-review blocker: %v", view.AcceptanceBlockers)
	}
	if view.DeliveryPreview == nil || view.DeliveryPreview.Action != "merge" || view.DeliveryPreview.MergeMethod != "squash" {
		t.Fatalf("reviewed primary delivery preview = %+v", view.DeliveryPreview)
	}
	if view.ReviewedDeliveryPreview == nil || view.ReviewedDeliveryPreview.Action != "merge" || view.ReviewedDeliveryPreview.MergeMethod != "squash" {
		t.Fatalf("reviewed delivery preview = %+v", view.ReviewedDeliveryPreview)
	}
}

func TestWorkflowAuthorityReadStateExceptionPreservesTrivialPreview(t *testing.T) {
	f, svc, issueID, acceptorTask := workflowAutonomousCandidateWithMode(t, "reviewed")
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "agent", ID: util.UUIDToString(issue.AssigneeID), SourceTaskID: util.UUIDToString(acceptorTask)}
	if _, err := svc.GrantException(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: f.UserID}, WorkflowExceptionInput{
		CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
		Scope: "acceptance", GrantDetails: map[string]any{"agent_actor_id": actor.ID},
		Reason: "Allow this exact agent to accept the candidate.", Consequences: "Both autonomous acceptance routes become available for this candidate.",
	}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
	if err != nil {
		t.Fatal(err)
	}
	if !view.AvailableActions.RequestTrivialAcceptance || !view.AvailableActions.RequestReviewedAcceptance {
		t.Fatalf("exception route availability = %+v", view.AvailableActions)
	}
	if view.DeliveryPreview == nil || view.DeliveryPreview.Action != "ready" {
		t.Fatalf("trivial delivery preview = %+v", view.DeliveryPreview)
	}
	if view.ReviewedDeliveryPreview == nil || view.ReviewedDeliveryPreview.Action != "merge" {
		t.Fatalf("reviewed delivery preview = %+v", view.ReviewedDeliveryPreview)
	}
}

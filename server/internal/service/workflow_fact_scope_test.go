package service

import (
	"context"
	"github.com/multica-ai/multica/server/internal/util"
	"testing"
)

func TestWorkflowEditorialChangesDoNotRevokeEvaluatedCandidate(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidate(t, true)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE issue SET title='Clarified presentation title',revision=revision+1 WHERE id=$1`, issueID)
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	view, err := svc.ReadState(ctx, current.WorkspaceID, issueID, actor)
	if err != nil || !view.AvailableActions.AcceptHuman {
		t.Fatalf("editorial change revoked evaluation: %+v, %v", view, err)
	}
	if _, err := svc.AcceptWorkflow(ctx, current.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: current.Revision}); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE issue SET title='Completed work with clearer title',revision=revision+1 WHERE id=$1`, issueID)
	after, err := f.q.GetIssue(ctx, issueID)
	if err != nil || after.Status != "done" || after.WorkflowCandidateID != before.WorkflowCandidateID {
		t.Fatalf("presentation edit changed completed work: %+v, %v", after, err)
	}
}

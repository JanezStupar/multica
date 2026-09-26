package service

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
)

func TestWorkflowRejectStateMatchesCandidateAndWriterEligibility(t *testing.T) {
	for _, condition := range []string{"valid", "scope_changed", "writer_failed", "writer_missing", "private_writer", "archived_writer", "writer_runtime_missing"} {
		t.Run(condition, func(t *testing.T) {
			f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t)
			ctx := context.Background()
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			wantError := ErrWorkflowAuthorityConflict
			writer, err := f.q.GetAgentTask(ctx, writerTask)
			if err != nil {
				t.Fatal(err)
			}
			switch condition {
			case "scope_changed":
				f.Exec(t, `UPDATE issue SET description='A different objective',revision=revision+1 WHERE id=$1`, issueID)
			case "writer_failed":
				f.Exec(t, `UPDATE agent_task_queue SET status='failed' WHERE id=$1`, writerTask)
			case "writer_missing":
				f.Exec(t, `DELETE FROM agent_task_queue WHERE id=$1`, writerTask)
			case "private_writer":
				actor.ID = f.member(t, "private-writer-rejector")
				f.Exec(t, `UPDATE agent SET visibility='private' WHERE id=$1`, writer.AgentID)
				f.Exec(t, `UPDATE issue SET assignee_type='member',assignee_id=$2,revision=revision+1 WHERE id=$1`, issueID, actor.ID)
				wantError = ErrWorkflowAuthorityForbidden
			case "archived_writer":
				f.Exec(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, writer.AgentID)
				wantError = ErrWorkflowAuthorityForbidden
			case "writer_runtime_missing":
				f.Exec(t, `UPDATE agent SET runtime_id=NULL WHERE id=$1`, writer.AgentID)
				wantError = ErrWorkflowAuthorityForbidden
			}
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			state, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			allowed := condition == "valid"
			if err != nil || state.AvailableActions.Reject != allowed {
				t.Fatalf("reject action disagrees with %s eligibility: %+v, %v", condition, state.AvailableActions, err)
			}
			err = svc.RejectWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowRejectionInput{
				CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
				Kind: "in_scope_defect", Reason: "The implementation still has a defect.", ResumeTaskID: util.UUIDToString(writerTask),
			})
			if allowed && err != nil || !allowed && !errors.Is(err, wantError) {
				t.Fatalf("reject mutation disagrees with %s eligibility: %v", condition, err)
			}
		})
	}
}

func TestWorkflowRejectFollowsStatusCategoryIncludingCustomDone(t *testing.T) {
	for _, status := range []string{"in_review", "blocked", "done", "custom_done", "cancelled", "custom_closed"} {
		t.Run(status, func(t *testing.T) {
			f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t)
			ctx := context.Background()
			if status == "custom_done" || status == "custom_closed" {
				category := "started"
				if status == "custom_closed" {
					category = "closed"
				}
				f.Insert(t, "issue_status", testutil.Cols{"workspace_id": f.WorkspaceID, "key": status, "name": status, "category": category, "color": "#22c55e"})
			}
			if status == "done" {
				before, err := f.q.GetIssue(ctx, issueID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: f.UserID}, WorkflowAcceptanceInput{
					CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision,
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				f.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, issueID, status)
			}
			if status == "custom_done" {
				// Model a preexisting custom-completed row without disabling the
				// production issue guard. Runtime APIs do not change categories.
				f.Exec(t, `UPDATE issue_status SET category='done' WHERE workspace_id=$1 AND key=$2`, f.WorkspaceID, status)
			}
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			state, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			allowed := status != "cancelled" && status != "custom_closed"
			if err != nil || state.AvailableActions.Reject != allowed {
				t.Fatalf("status %s reject availability: %+v, %v", status, state.AvailableActions, err)
			}
			err = svc.RejectWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowRejectionInput{
				CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
				Kind: "in_scope_defect", Reason: "Correct the implementation within the existing scope.", ResumeTaskID: util.UUIDToString(writerTask),
			})
			if allowed && err != nil || !allowed && !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("status %s reject mutation: %v", status, err)
			}
		})
	}
}

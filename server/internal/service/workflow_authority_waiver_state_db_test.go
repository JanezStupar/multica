package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func TestWorkflowReviewWaiverStateMatchesMemberMutation(t *testing.T) {
	for _, condition := range []string{"active_reviewer", "requested", "accepted", "existing_grant", "cancelled", "frozen", "scope_changed", "ordinary_member"} {
		t.Run(condition, func(t *testing.T) {
			f, svc, issueID, _, _ := workflowReviewOriginFixture(t, "fresh")
			ctx := context.Background()
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			input := WorkflowExceptionInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
				Scope: "review", GrantDetails: map[string]any{"waive": true}, Reason: "Review cannot finish for this candidate.", Consequences: "Waive only this candidate's review requirement."}
			allowed := condition == "active_reviewer" || condition == "requested"
			wantError := ErrWorkflowAuthorityConflict
			switch condition {
			case "requested", "accepted":
				f.Insert(t, "issue_workflow_acceptance", testutil.Cols{"id": dbid.NewV7(), "workspace_id": issue.WorkspaceID,
					"issue_id": issueID, "candidate_id": issue.WorkflowCandidateID, "mode": "human", "actor_type": "member",
					"actor_id": f.UserID, "state": condition, "policy_version": "test", "authority_snapshot": "{}"})
			case "existing_grant":
				if _, err := svc.GrantException(ctx, issue.WorkspaceID, issueID, actor, input); err != nil {
					t.Fatalf("create existing waiver: %v", err)
				}
			case "cancelled":
				f.Exec(t, `UPDATE issue SET status='cancelled',revision=revision+1 WHERE id=$1`, issueID)
			case "frozen":
				f.Exec(t, `UPDATE issue SET workflow_frozen=true,revision=revision+1 WHERE id=$1`, issueID)
			case "scope_changed":
				f.Exec(t, `UPDATE issue SET description='A different objective.',revision=revision+1 WHERE id=$1`, issueID)
			case "ordinary_member":
				actor.ID = f.member(t, "ordinary-waiver-member")
				wantError = ErrWorkflowAuthorityForbidden
			}
			issue, err = f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			input.ExpectedRevision = issue.Revision
			view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			if err != nil || view.AvailableActions.WaiveReview != allowed {
				t.Fatalf("waiver state disagrees with %s eligibility: %+v, %v", condition, view.AvailableActions, err)
			}
			_, err = svc.GrantException(ctx, issue.WorkspaceID, issueID, actor, input)
			if allowed && err != nil || !allowed && !errors.Is(err, wantError) {
				t.Fatalf("waiver mutation disagrees with state for %s: allowed=%v error=%v", condition, allowed, err)
			}
			if condition == "requested" && f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance
				WHERE issue_id=$1 AND state='blocked' AND revoked_at IS NOT NULL`, issueID) != 1 {
				t.Fatal("waiver did not block the prior requested decision")
			}
		})
	}
}

func workflowReviewWaiverSupervisorFixture(t *testing.T) (principalFixture, WorkflowAuthorityService, pgtype.UUID, pgtype.UUID, WorkflowActor) {
	t.Helper()
	f, wakeups, issueID, writerAgent, supervisorAgent := handoffFixture(t)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	old, err := wakeups.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || old == nil {
		t.Fatalf("decode fixture policy: %v", err)
	}
	source := old.Bundle
	source.ID = util.UUIDToString(dbid.NewV7())
	source.Files = append(source.Files, AgentSkillFileData{Path: "runtime/policy.json",
		Content: `{"format_version":1,"supervisors":[{"agent_id":"` + supervisorAgent + `","scopes":["review"]}]}`})
	pinned, err := wakeups.Tasks.NewIssueWorkflowPolicy(source)
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := json.Marshal(pinned)
	f.Exec(t, `UPDATE issue SET workflow_policy=$2 WHERE id=$1`, issueID, archive)
	writerTask := handoffSourceTask(t, f, issueID, writerAgent)
	f.Exec(t, `UPDATE agent_task_queue SET session_id='writer-session' WHERE id=$1`, writerTask)
	input := handoffInput(writerTask, parseTestUUID(t, supervisorAgent))
	input.Candidates = []HandoffCandidate{}
	handoff, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), writerTask, input)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, writerTask)
	wakeDispatch(t, wakeups, handoff)
	stored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: handoff.ID, WorkspaceID: handoff.WorkspaceID})
	if err != nil || !stored.LastTaskID.Valid {
		t.Fatalf("supervisor handoff did not create recipient: %+v, %v", stored, err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='running',started_at=now(),session_id='supervisor-session' WHERE id=$1`, stored.LastTaskID)
	bindWorkflowTestTask(t, f, issueID, stored.LastTaskID)
	f.Exec(t, `UPDATE issue SET status='blocked',revision=revision+1 WHERE id=$1`, issueID)
	return f, WorkflowAuthorityService{Tasks: wakeups.Tasks}, issueID, stored.LastTaskID,
		WorkflowActor{Type: "agent", ID: supervisorAgent, SourceTaskID: util.UUIDToString(stored.LastTaskID)}
}

func TestWorkflowReviewWaiverStateMatchesBoundSupervisorMutation(t *testing.T) {
	for _, condition := range []string{"bound", "missing_task", "missing_profile", "wrong_policy", "completed_task"} {
		t.Run(condition, func(t *testing.T) {
			f, svc, issueID, taskID, actor := workflowReviewWaiverSupervisorFixture(t)
			ctx := context.Background()
			switch condition {
			case "missing_task":
				actor.SourceTaskID = util.UUIDToString(dbid.NewV7())
			case "missing_profile":
				f.Exec(t, `UPDATE agent_task_queue SET workflow_profile_id=NULL WHERE id=$1`, taskID)
			case "wrong_policy":
				f.Exec(t, `UPDATE agent_task_queue SET workflow_policy_version='changed' WHERE id=$1`, taskID)
			case "completed_task":
				f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
			}
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			allowed := condition == "bound"
			view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			if err != nil || view.AvailableActions.WaiveReview != allowed {
				t.Fatalf("delegated waiver readback disagrees with task binding: %+v, %v", view.AvailableActions, err)
			}
			_, err = svc.GrantException(ctx, issue.WorkspaceID, issueID, actor, WorkflowExceptionInput{
				CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
				Scope: "review", GrantDetails: map[string]any{"waive": true}, Reason: "Scoped supervisor review decision.", Consequences: "Waive this exact candidate's review requirement.",
			})
			if allowed && err != nil || !allowed && !errors.Is(err, ErrWorkflowAuthorityForbidden) {
				t.Fatalf("delegated waiver mutation disagrees with %s state: %v", condition, err)
			}
		})
	}
}

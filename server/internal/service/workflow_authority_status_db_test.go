package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func TestWorkflowAuthorityAutonomousAcceptanceFollowsNonterminalStatusChanges(t *testing.T) {
	for _, status := range []string{"backlog", "todo", "in_progress", "in_review", "blocked", "custom_unstarted", "custom_started"} {
		t.Run(status, func(t *testing.T) {
			f, svc, issueID, acceptorTask := workflowAutonomousCandidate(t)
			ctx := context.Background()
			if status == "custom_unstarted" || status == "custom_started" {
				category := "started"
				if status == "custom_unstarted" {
					category = "unstarted"
				}
				f.Insert(t, "issue_status", testutil.Cols{"workspace_id": f.WorkspaceID, "key": status, "name": status, "category": category, "color": "#22c55e"})
			}
			f.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, issueID, status)
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "agent", ID: util.UUIDToString(issue.AssigneeID), SourceTaskID: util.UUIDToString(acceptorTask)}
			view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			if err != nil || !view.AvailableActions.RequestTrivialAcceptance || len(view.AcceptanceBlockers) != 0 {
				t.Fatalf("exact assigned authorized agent blocked by status: %+v, %v", view, err)
			}
			request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
				ClassificationReason: "The exact independently reviewed routine candidate is complete."}
			if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, request); err != nil || state != "requested" {
				t.Fatalf("request from %s: %q, %v", status, state, err)
			}
			// A real status edit advances revision after request ingestion. It
			// changes neither the reviewed scope nor the autonomous authority.
			finalizeStatus := "in_progress"
			if status == "in_progress" {
				finalizeStatus = "blocked"
			}
			f.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, issueID, finalizeStatus)
			f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, acceptorTask)
			beforeFinalize, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
				t.Fatalf("nonterminal status edit blocked delayed finalization: %v, %v", processed, err)
			}
			accepted, err := f.q.GetIssue(ctx, issueID)
			if err != nil || accepted.Status != "done" || accepted.Revision != beforeFinalize.Revision+1 ||
				f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted' AND issue_revision=$2`, issueID, accepted.Revision) != 1 {
				t.Fatalf("finalizer did not accept unchanged candidate at current revision: %+v, %v", accepted, err)
			}
		})
	}
}

func TestWorkflowAuthorityRunningReviewerMayRequestFromNonterminalStatus(t *testing.T) {
	f, svc, issueID, reviewerTask, reviewerAgent := workflowSameReviewerAcceptanceFixture(t)
	ctx := context.Background()
	f.Exec(t, `UPDATE issue SET status='blocked',revision=revision+1 WHERE id=$1`, issueID)
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(reviewerTask)}
	view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
	if err != nil || !view.AvailableActions.RequestTrivialAcceptance {
		t.Fatalf("running review loses exact request eligibility in blocked status: %+v, %v", view, err)
	}
	if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
		CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
		ClassificationReason: "Scoped trivial change; my independent final review passed.",
	}); err != nil || state != "requested" {
		t.Fatalf("running reviewer request from blocked: %q, %v", state, err)
	}
}

func TestWorkflowAuthorityAutonomousAcceptanceRejectsClosedStatuses(t *testing.T) {
	for _, status := range []string{"cancelled", "custom_closed"} {
		t.Run(status, func(t *testing.T) {
			f, svc, issueID, acceptorTask := workflowAutonomousCandidate(t)
			ctx := context.Background()
			if status == "custom_closed" {
				f.Insert(t, "issue_status", testutil.Cols{"workspace_id": f.WorkspaceID, "key": status, "name": status, "category": "closed", "color": "#22c55e"})
			}
			f.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, issueID, status)
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "agent", ID: util.UUIDToString(issue.AssigneeID), SourceTaskID: util.UUIDToString(acceptorTask)}
			view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			if err != nil || view.AvailableActions.RequestTrivialAcceptance || !slices.Contains(view.AcceptanceBlockers, "terminal_status") {
				t.Fatalf("closed status exposes autonomous authority: %+v, %v", view, err)
			}
			if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
				CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
				ClassificationReason: "Exact reviewed routine candidate.",
			}); !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("autonomous request bypassed closed status: %v", err)
			}
			if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, issueID); got != 0 {
				t.Fatalf("closed status recorded %d acceptance rows", got)
			}
		})
	}
}

func TestWorkflowAuthorityAutonomousFinalizerStillRejectsMaterialChanges(t *testing.T) {
	for _, change := range []string{"cancelled", "custom_closed", "scope", "owner", "frozen", "policy", "candidate", "review", "source_profile", "source_policy", "profile_snapshot_missing"} {
		t.Run(change, func(t *testing.T) {
			f, svc, issueID, acceptorTask := workflowAutonomousCandidate(t)
			ctx := context.Background()
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "agent", ID: util.UUIDToString(issue.AssigneeID), SourceTaskID: util.UUIDToString(acceptorTask)}
			request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
				ClassificationReason: "Exact reviewed routine candidate."}
			if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, request); err != nil || state != "requested" {
				t.Fatalf("initial request: %q, %v", state, err)
			}
			// The frozen-work fence also protects terminal source transitions.
			// Complete first, then change facts before delayed finalization.
			f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, acceptorTask)
			switch change {
			case "cancelled", "custom_closed":
				if change == "custom_closed" {
					f.Insert(t, "issue_status", testutil.Cols{"workspace_id": f.WorkspaceID, "key": change, "name": change, "category": "closed", "color": "#22c55e"})
				}
				f.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, issueID, change)
			case "scope":
				f.Exec(t, `UPDATE issue SET description='The objective changed after approval.',revision=revision+1 WHERE id=$1`, issueID)
			case "owner":
				f.Exec(t, `UPDATE issue SET assignee_type='member',assignee_id=$2,revision=revision+1 WHERE id=$1`, issueID, f.UserID)
			case "frozen":
				f.Exec(t, `UPDATE issue SET workflow_frozen=true,revision=revision+1 WHERE id=$1`, issueID)
			case "policy":
				f.Exec(t, `UPDATE issue SET workflow_policy=jsonb_set(workflow_policy,'{version}','"changed"'::jsonb),revision=revision+1 WHERE id=$1`, issueID)
			case "candidate":
				f.Exec(t, `UPDATE issue SET workflow_candidate_id=$2,revision=revision+1 WHERE id=$1`, issueID, dbid.NewV7())
			case "review":
				f.Exec(t, `UPDATE issue_workflow_review SET verdict='changes_requested' WHERE issue_id=$1 AND candidate_id=$2`, issueID, issue.WorkflowCandidateID)
			case "source_profile":
				f.Exec(t, `UPDATE agent_task_queue SET workflow_profile_id=$2 WHERE id=$1`, acceptorTask, dbid.NewV7())
			case "source_policy":
				f.Exec(t, `UPDATE agent_task_queue SET workflow_policy_version='changed' WHERE id=$1`, acceptorTask)
			case "profile_snapshot_missing":
				f.Exec(t, `UPDATE issue_workflow_acceptance SET authority_snapshot=authority_snapshot-'selected_workflow_profile_id' WHERE issue_id=$1`, issueID)
			}
			beforeFinalize, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
				t.Fatalf("changed facts not reconciled: %v, %v", processed, err)
			}
			after, err := f.q.GetIssue(ctx, issueID)
			if err != nil || after.Revision != beforeFinalize.Revision || after.Status != beforeFinalize.Status ||
				f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='blocked'`, issueID) != 1 ||
				f.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE issue_id=$1`, issueID) != 0 {
				t.Fatalf("material change %s released acceptance or changed issue: %+v, %v", change, after, err)
			}
			if change == "source_profile" || change == "source_policy" || change == "profile_snapshot_missing" {
				wantReason := "source_task_profile_changed"
				if change == "profile_snapshot_missing" {
					wantReason = "acceptance_profile_snapshot_missing"
				}
				if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND last_error_class=$2`, issueID, wantReason); got != 1 {
					t.Fatalf("profile change blocker missing: %d rows with %s", got, wantReason)
				}
			}
		})
	}
}

func workflowAcceptancePinnedProfile(t *testing.T, f principalFixture, svc WorkflowAuthorityService, issueID, taskID pgtype.UUID) (IssueWorkflowProfile, pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := f.q.GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := svc.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || pinned == nil {
		t.Fatalf("decode pinned policy: %v", err)
	}
	profile := IssueWorkflowProfile{FormatVersion: 1, IssueID: util.UUIDToString(issueID), AgentID: util.UUIDToString(source.AgentID),
		PolicyVersion: pinned.Version, ExpectedProvider: "codex", CustomArgsDigest: IssueWorkflowCustomArgsDigest(nil), Skills: []AgentSkillData{pinned.Bundle}}
	raw, digest, err := profile.encodedAndDigest()
	if err != nil {
		t.Fatal(err)
	}
	f.Insert(t, "issue_workflow_profile", testutil.Cols{"id": source.WorkflowProfileID, "workspace_id": issue.WorkspaceID,
		"issue_id": issueID, "agent_id": source.AgentID, "policy_version": pinned.Version, "snapshot": raw, "digest": digest})
	return profile, source.WorkflowProfileID
}

func TestWorkflowAuthorityAutonomousFinalizerRequiresUnchangedSelectedProfile(t *testing.T) {
	f, svc, issueID, acceptorTask := workflowAutonomousCandidate(t)
	ctx := context.Background()
	profile, originalProfileID := workflowAcceptancePinnedProfile(t, f, svc, issueID, acceptorTask)
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "agent", ID: util.UUIDToString(issue.AssigneeID), SourceTaskID: util.UUIDToString(acceptorTask)}
	if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
		CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
		ClassificationReason: "Exact reviewed routine candidate.",
	}); err != nil || state != "requested" {
		t.Fatalf("initial request with selected profile: %q, %v", state, err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, acceptorTask)
	selection, err := svc.Tasks.ReselectIssueWorkflowProfile(ctx, IssueWorkflowProfileReselection{
		WorkspaceID: issue.WorkspaceID, IssueID: issueID, AgentID: issue.AssigneeID, ExpectedProfileID: originalProfileID,
		RequestID: dbid.NewV7(), ActorUserID: parseTestUUID(t, f.UserID),
		Reason: "The acceptance role needs revised instructions.", Consequences: "Reevaluate the pending decision.", Reconciliation: "Use the new acceptance role.",
	}, func(_ *db.Queries, _ *IssueWorkflowPolicy) (IssueWorkflowProfile, error) {
		changed := profile
		changed.AgentInstructions = "Reevaluate the acceptance decision against the updated role instructions."
		return changed, nil
	})
	if err != nil || selection == nil {
		t.Fatalf("explicit profile reselection after source success: %+v, %v", selection, err)
	}
	f.Cleanup(t, `DELETE FROM issue_workflow_profile WHERE id=$1`, selection.ProfileID)
	source, err := f.q.GetAgentTask(ctx, acceptorTask)
	if err != nil || source.WorkflowProfileID != originalProfileID {
		t.Fatalf("reselection rewrote immutable source pin: %+v, %v", source, err)
	}
	if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
		t.Fatalf("reselected profile decision not reconciled: %v, %v", processed, err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='blocked' AND last_error_class='selected_profile_changed'`, issueID); got != 1 {
		t.Fatalf("selected profile change released old decision: %d blocked rows", got)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE issue_id=$1`, issueID); got != 0 {
		t.Fatalf("selected profile change released %d delivery rows", got)
	}
}

func TestWorkflowAuthorityAutonomousFinalizerAllowsStableNullableOrNewerSelection(t *testing.T) {
	for _, selected := range []bool{false, true} {
		name := "no_selected_profile"
		if selected {
			name = "selected_profile_differs_from_retained_source"
		}
		t.Run(name, func(t *testing.T) {
			f, svc, issueID, acceptorTask := workflowAutonomousCandidate(t)
			ctx := context.Background()
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			if selected {
				profile, originalProfileID := workflowAcceptancePinnedProfile(t, f, svc, issueID, acceptorTask)
				profile.AgentInstructions = "The current selected role differs from the retained task's role."
				raw, digest, err := profile.encodedAndDigest()
				if err != nil {
					t.Fatal(err)
				}
				f.Insert(t, "issue_workflow_profile", testutil.Cols{"workspace_id": issue.WorkspaceID, "issue_id": issueID,
					"agent_id": issue.AssigneeID, "policy_version": profile.PolicyVersion, "revision": 2,
					"previous_profile_id": originalProfileID, "snapshot": raw, "digest": digest})
			}
			actor := WorkflowActor{Type: "agent", ID: util.UUIDToString(issue.AssigneeID), SourceTaskID: util.UUIDToString(acceptorTask)}
			if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
				CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
				ClassificationReason: "Exact reviewed routine candidate.",
			}); err != nil || state != "requested" {
				t.Fatalf("retained source request with stable selection: %q, %v", state, err)
			}
			f.Exec(t, `UPDATE agent SET model='future-default',thinking_level='high' WHERE id=$1`, issue.AssigneeID)
			f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, acceptorTask)
			if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
				t.Fatalf("stable selection not reconciled: %v, %v", processed, err)
			}
			if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, issueID); got != 1 {
				t.Fatalf("stable selection or global defaults blocked retained decision: %d accepted rows", got)
			}
		})
	}
}

func TestWorkflowFeedbackContinuationCannotReopenClosedIssue(t *testing.T) {
	for _, status := range []string{"cancelled", "custom_closed"} {
		t.Run(status, func(t *testing.T) {
			// The exact delivered comment remains with a running assigned
			// coordinator when the human closes the issue.
			f, svc, issueID, writerTask, commentID, actor := workflowFeedbackCandidate(t, "blocked")
			ctx := context.Background()
			if status == "custom_closed" {
				f.Insert(t, "issue_status", testutil.Cols{"workspace_id": f.WorkspaceID, "key": status, "name": status, "category": "closed", "color": "#22c55e"})
			}
			f.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, issueID, status)
			before, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			acceptanceID := f.Insert(t, "issue_workflow_acceptance", testutil.Cols{
				"id":           dbid.NewV7(),
				"workspace_id": before.WorkspaceID, "issue_id": issueID, "candidate_id": before.WorkflowCandidateID,
				"mode": "human", "actor_type": "member", "actor_id": f.UserID, "state": "accepted",
				"policy_version": "test", "authority_snapshot": "{}",
			})
			in := WorkflowFeedbackContinuationInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID),
				ExpectedRevision: before.Revision, CommentID: util.UUIDToString(commentID), Kind: "in_scope_defect"}
			if err := svc.ContinueWorkflowFeedback(ctx, before.WorkspaceID, issueID, actor, in); !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("running coordinator reopened %s from delivered comment: %v", status, err)
			}
			after, err := f.q.GetIssue(ctx, issueID)
			if err != nil || after.Revision != before.Revision || after.Status != before.Status ||
				after.WorkflowCandidateID != before.WorkflowCandidateID || after.AssigneeID != before.AssigneeID ||
				f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND state='accepted' AND revoked_at IS NULL`, acceptanceID) != 1 ||
				f.Count(t, `SELECT count(*) FROM issue_workflow_rejection WHERE issue_id=$1`, issueID) != 0 ||
				f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND rerun_of_task_id=$2`, issueID, writerTask) != 0 {
				t.Fatalf("closed feedback mutated candidate, ledger, or writer work: %+v, %v", after, err)
			}
		})
	}
}

func TestWorkflowFeedbackContinuationCanReworkAcceptedDoneWithoutDeliveredOutcome(t *testing.T) {
	f, svc, issueID, writerTask, commentID, actor := workflowFeedbackCandidate(t, "blocked")
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := svc.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || pinned == nil {
		t.Fatalf("decode pinned policy: %v", err)
	}
	acceptanceID := f.Insert(t, "issue_workflow_acceptance", testutil.Cols{
		"id":           dbid.NewV7(),
		"workspace_id": issue.WorkspaceID, "issue_id": issueID, "candidate_id": issue.WorkflowCandidateID,
		"mode": "human", "actor_type": "member", "actor_id": f.UserID, "state": "accepted",
		"policy_version": pinned.Version, "authority_snapshot": "{}", "issue_revision": issue.Revision + 1,
	})
	f.Exec(t, `UPDATE issue SET status='done',revision=revision+1 WHERE id=$1`, issueID)
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ContinueWorkflowFeedback(ctx, before.WorkspaceID, issueID, actor, WorkflowFeedbackContinuationInput{
		CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision,
		CommentID: util.UUIDToString(commentID), Kind: "in_scope_defect",
	}); err != nil {
		t.Fatalf("valid done rework blocked by closed-status guard: %v", err)
	}
	after, err := f.q.GetIssue(ctx, issueID)
	if err != nil || after.Status != "in_progress" || after.WorkflowCandidateID.Valid || after.Revision != before.Revision+1 ||
		f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND state='revoked' AND revoked_at IS NOT NULL`, acceptanceID) != 1 ||
		f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND rerun_of_task_id=$2 AND status='queued'`, issueID, writerTask) != 1 {
		t.Fatalf("valid done feedback did not revoke acceptance and resume writer: %+v, %v", after, err)
	}
}

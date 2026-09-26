package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
)

func TestWorkflowAuthorityHumanAcceptsAgentAssignedCandidateAcrossNonterminalStatuses(t *testing.T) {
	for _, status := range []string{"backlog", "todo", "in_progress", "in_review", "blocked", "custom_unstarted", "custom_started"} {
		t.Run(status, func(t *testing.T) {
			f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t)
			ctx := context.Background()
			writer, err := f.q.GetAgentTask(ctx, writerTask)
			if err != nil {
				t.Fatal(err)
			}
			if status == "custom_unstarted" || status == "custom_started" {
				category := "started"
				if status == "custom_unstarted" {
					category = "unstarted"
				}
				f.Insert(t, "issue_status", testutil.Cols{"workspace_id": f.WorkspaceID, "key": status, "name": status, "category": category, "color": "#22c55e"})
			}
			f.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2,status=$3,revision=revision+1 WHERE id=$1`, issueID, writer.AgentID, status)
			before, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			view, err := svc.ReadState(ctx, before.WorkspaceID, issueID, actor)
			if err != nil || !view.AvailableActions.AcceptHuman || len(view.AcceptanceBlockers) != 0 {
				t.Fatalf("authorized human blocked by status/agent assignment: %+v, %v", view, err)
			}
			request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision}
			if state, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, request); err != nil || state != "accepted" {
				t.Fatalf("human decision on reviewed agent-assigned candidate: %q, %v", state, err)
			}
			accepted, err := f.q.GetIssue(ctx, issueID)
			if err != nil || accepted.Status != "done" || accepted.Revision != before.Revision+1 ||
				accepted.AssigneeType.String != "agent" || accepted.AssigneeID != writer.AgentID {
				t.Fatalf("acceptance did not preserve assignment and finish candidate: %+v, %v", accepted, err)
			}
			if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND actor_type='member' AND actor_id=$2 AND state='accepted'`, issueID, f.UserID); got != 1 {
				t.Fatalf("human decision attributed to %d matching ledger rows, want one", got)
			}
		})
	}
}

func TestWorkflowAuthorityPolicyAllowedMemberDoesNotNeedHumanAssignment(t *testing.T) {
	for _, assignment := range []string{"another_member", "unassigned"} {
		t.Run(assignment, func(t *testing.T) {
			f, svc, issueID, _ := workflowReviewedHumanCandidate(t)
			ctx := context.Background()
			memberID := f.member(t, "policy-acceptor")
			if assignment == "unassigned" {
				f.Exec(t, `UPDATE issue SET assignee_type=NULL,assignee_id=NULL,revision=revision+1 WHERE id=$1`, issueID)
			}
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "member", ID: memberID}
			view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			if err != nil || !view.AvailableActions.AcceptHuman || len(view.AcceptanceBlockers) != 0 {
				t.Fatalf("policy-allowed member requires assignment: %+v, %v", view, err)
			}
			if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
				CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
			}); err != nil || state != "accepted" {
				t.Fatalf("policy-allowed member decision: %q, %v", state, err)
			}
		})
	}
}

func TestWorkflowAuthorityHumanAcceptanceDeniesExcludedAndNonmemberActors(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidateForRoles(t, false, []string{"owner", "admin"})
	ctx := context.Background()
	excludedMember := f.member(t, "excluded-acceptor")
	outsider := f.User(t, "outside acceptor", "outside-human-acceptor@multica.test")
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision}
	for _, actorID := range []string{excludedMember, outsider} {
		actor := WorkflowActor{Type: "member", ID: actorID}
		view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
		if err != nil || view.AvailableActions.AcceptHuman || len(view.AcceptanceBlockers) != 0 {
			t.Fatalf("actor authority confused with candidate blockers: %+v, %v", view, err)
		}
		if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, request); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
			t.Fatalf("unauthorized human accepted exact candidate: %v", err)
		}
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, issueID); got != 0 {
		t.Fatalf("denied actors recorded %d acceptance rows", got)
	}
}

func TestWorkflowAuthorityHumanAcceptanceStillRejectsActiveWork(t *testing.T) {
	f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t)
	ctx := context.Background()
	writer, err := f.q.GetAgentTask(ctx, writerTask)
	if err != nil {
		t.Fatal(err)
	}
	f.Task(t, util.UUIDToString(writer.AgentID), testutil.Cols{"issue_id": issueID,
		"runtime_id": writer.RuntimeID, "status": "running", "started_at": testutil.Raw("now()")})
	f.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2,status='blocked',revision=revision+1 WHERE id=$1`, issueID, writer.AgentID)
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
	if err != nil || view.AvailableActions.AcceptHuman || !slices.Contains(view.AcceptanceBlockers, "active_work") {
		t.Fatalf("active work is not an acceptance blocker: %+v, %v", view, err)
	}
	if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
		CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
	}); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("human decision bypassed active work: %v", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, issueID); got != 0 {
		t.Fatalf("active work recorded %d acceptance rows", got)
	}
}

func TestWorkflowAuthorityHumanAcceptanceStillRejectsPendingHandoff(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidate(t)
	ctx := context.Background()
	f.Exec(t, `UPDATE issue_wakeup SET enabled=true,disabled_at=NULL,handoff_completed_at=NULL
		WHERE id=(SELECT id FROM issue_wakeup WHERE issue_id=$1 AND handoff IS NOT NULL ORDER BY id DESC LIMIT 1)`, issueID)
	if got := f.Count(t, `SELECT count(*) FROM issue_wakeup WHERE issue_id=$1 AND handoff IS NOT NULL AND enabled AND handoff_completed_at IS NULL`, issueID); got != 1 {
		t.Fatalf("pending handoff fixture has %d pending handoffs, want one", got)
	}
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
	if err != nil || view.AvailableActions.AcceptHuman || !slices.Contains(view.AcceptanceBlockers, "pending_handoff") {
		t.Fatalf("pending handoff is not an acceptance blocker: %+v, %v", view, err)
	}
	if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
		CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
	}); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("human decision bypassed pending handoff: %v", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, issueID); got != 0 {
		t.Fatalf("pending handoff recorded %d acceptance rows", got)
	}
}

func TestWorkflowAuthorityHumanAcceptanceRejectsClosedStatuses(t *testing.T) {
	for _, status := range []string{"cancelled", "custom_closed"} {
		t.Run(status, func(t *testing.T) {
			f, svc, issueID, _ := workflowReviewedHumanCandidate(t)
			ctx := context.Background()
			if status == "custom_closed" {
				f.Insert(t, "issue_status", testutil.Cols{"workspace_id": f.WorkspaceID, "key": status, "name": status, "category": "closed", "color": "#22c55e"})
			}
			f.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, issueID, status)
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			if err != nil || view.AvailableActions.AcceptHuman || !slices.Contains(view.AcceptanceBlockers, "terminal_status") {
				t.Fatalf("closed status is not an acceptance blocker: %+v, %v", view, err)
			}
			if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
				CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision,
			}); !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("closed candidate accepted: %v", err)
			}
		})
	}
}

func TestWorkflowAuthorityHumanAcceptanceFormat2ReplayFromAcceptedAndDone(t *testing.T) {
	for _, complete := range []bool{false, true} {
		name := "accepted"
		if complete {
			name = "done"
		}
		t.Run(name, func(t *testing.T) {
			f, svc, issueID, _ := workflowReviewedHumanCandidate(t, true)
			ctx := context.Background()
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
				ExpectedRevision: issue.Revision, OutcomeComplete: &complete}
			if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, request); err != nil || state != "accepted" {
				t.Fatalf("initial format-2 acceptance: %q, %v", state, err)
			}
			beforeReplay, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := "pr_ready"
			if complete {
				wantStatus = "done"
			}
			if beforeReplay.Status != wantStatus {
				t.Fatalf("accepted status %q, want %q", beforeReplay.Status, wantStatus)
			}
			if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, request); err != nil || state != "accepted" {
				t.Fatalf("exact replay from %s: %q, %v", wantStatus, state, err)
			}
			changed := request
			changed.ExpectedRevision = beforeReplay.Revision
			if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, changed); !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("changed acceptance of accepted candidate: %v", err)
			}
			otherActor := WorkflowActor{Type: "member", ID: f.member(t, "second-acceptor")}
			if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, otherActor, request); !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("second actor accepted accepted candidate: %v", err)
			}
			view, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			if err != nil || view.AvailableActions.AcceptHuman || !slices.Contains(view.AcceptanceBlockers, "already_accepted") {
				t.Fatalf("accepted candidate exposes new acceptance action: %+v, %v", view, err)
			}
			afterReplay, err := f.q.GetIssue(ctx, issueID)
			if err != nil || afterReplay.Revision != beforeReplay.Revision ||
				f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, issueID) != 1 {
				t.Fatalf("replay changed candidate ledger/revision: %+v, %v", afterReplay, err)
			}
		})
	}
}

func TestWorkflowAuthorityHumanRejectsAgentAssignedCandidateAcrossNonterminalStatuses(t *testing.T) {
	for _, status := range []string{"backlog", "todo", "in_progress", "in_review", "blocked", "custom_unstarted", "custom_started"} {
		t.Run(status, func(t *testing.T) {
			f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t)
			ctx := context.Background()
			writer, err := f.q.GetAgentTask(ctx, writerTask)
			if err != nil {
				t.Fatal(err)
			}
			if status == "custom_unstarted" || status == "custom_started" {
				category := "started"
				if status == "custom_unstarted" {
					category = "unstarted"
				}
				f.Insert(t, "issue_status", testutil.Cols{"workspace_id": f.WorkspaceID, "key": status, "name": status, "category": category, "color": "#22c55e"})
			}
			f.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2,status=$3,revision=revision+1 WHERE id=$1`, issueID, writer.AgentID, status)
			before, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			request := WorkflowRejectionInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision,
				Kind: "in_scope_defect", Reason: "The reviewed candidate still needs the requested correction.", ResumeTaskID: util.UUIDToString(writerTask)}
			unauthorized := WorkflowActor{Type: "member", ID: f.member(t, "unassigned-rejector")}
			unauthorizedView, err := svc.ReadState(ctx, before.WorkspaceID, issueID, unauthorized)
			if err != nil || unauthorizedView.AvailableActions.Reject {
				t.Fatalf("unassigned ordinary member can reject: %+v, %v", unauthorizedView, err)
			}
			if err := svc.RejectWorkflow(ctx, before.WorkspaceID, issueID, unauthorized, request); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
				t.Fatalf("unassigned ordinary member rejected candidate: %v", err)
			}
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			view, err := svc.ReadState(ctx, before.WorkspaceID, issueID, actor)
			if err != nil || !view.AvailableActions.Reject {
				t.Fatalf("authorized human rejection blocked by status: %+v, %v", view, err)
			}
			if err := svc.RejectWorkflow(ctx, before.WorkspaceID, issueID, actor, request); err != nil {
				t.Fatalf("reject reviewed candidate from %s: %v", status, err)
			}
			current, err := f.q.GetIssue(ctx, issueID)
			if err != nil || current.Status != "in_progress" || current.WorkflowCandidateID.Valid || current.Revision != before.Revision+1 ||
				f.Count(t, `SELECT count(*) FROM issue_workflow_rejection WHERE issue_id=$1 AND candidate_id=$2 AND actor_id=$3`, issueID, before.WorkflowCandidateID, f.UserID) != 1 ||
				f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND rerun_of_task_id=$2 AND status='queued'`, issueID, writerTask) != 1 {
				t.Fatalf("human rejection did not supersede exact candidate and resume writer: %+v, %v", current, err)
			}
		})
	}
}

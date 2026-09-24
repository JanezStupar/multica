package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func bindWorkflowTestTask(t *testing.T, f principalFixture, issueID, taskID pgtype.UUID) {
	t.Helper()
	f.Exec(t, `UPDATE agent_task_queue SET
		workflow_policy_version=(SELECT workflow_policy->>'version' FROM issue WHERE id=$1),
		workflow_profile_id=$3 WHERE id=$2`, issueID, taskID, dbid.NewV7())
}

func workflowReviewedHumanCandidate(t *testing.T) (principalFixture, WorkflowAuthorityService, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	f, wakeups, issueID, writerAgent, reviewerAgent := handoffFixture(t)
	ctx := context.Background()
	writerTask := handoffSourceTask(t, f, issueID, writerAgent)
	f.Exec(t, `UPDATE agent_task_queue SET session_id='writer-session' WHERE id=$1`, writerTask)
	firstInput := handoffInput(writerTask, parseTestUUID(t, reviewerAgent))
	firstInput.Candidates = []HandoffCandidate{}
	first, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), writerTask, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, writerTask)
	wakeDispatch(t, wakeups, first)
	firstStored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: first.ID, WorkspaceID: first.WorkspaceID})
	if err != nil || !firstStored.LastTaskID.Valid {
		t.Fatalf("reviewer task not queued: %+v, %v", firstStored, err)
	}
	reviewerTask := firstStored.LastTaskID
	f.Exec(t, `UPDATE agent_task_queue SET status='running',started_at=now(),session_id='reviewer-session' WHERE id=$1`, reviewerTask)
	bindWorkflowTestTask(t, f, issueID, reviewerTask)
	state, err := f.q.GetIssue(ctx, issueID)
	if err != nil || !state.WorkflowCandidateID.Valid {
		t.Fatalf("candidate not registered: %+v, %v", state, err)
	}
	svc := WorkflowAuthorityService{Tasks: wakeups.Tasks}
	if err := svc.RegisterReview(ctx, state.WorkspaceID, issueID, WorkflowActor{
		Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(reviewerTask),
	}, WorkflowReviewInput{CandidateID: util.UUIDToString(state.WorkflowCandidateID), Verdict: "pass", PRReviewURLs: []string{}}); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, reviewerTask)
	secondInput := handoffInput(reviewerTask, pgtype.UUID{})
	secondInput.AgentID, secondInput.AssigneeType, secondInput.AssigneeID = "", "member", f.UserID
	secondInput.Candidates = []HandoffCandidate{}
	second, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), reviewerTask, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, wakeups, second)
	ready, err := f.q.GetIssue(ctx, issueID)
	if err != nil || ready.WorkflowCandidateID != state.WorkflowCandidateID || ready.Status != "in_review" || ready.AssigneeType.String != "member" {
		t.Fatalf("human handoff changed candidate or owner: %+v, %v", ready, err)
	}
	return f, svc, issueID, writerTask
}

func workflowAutonomousCandidate(t *testing.T) (principalFixture, WorkflowAuthorityService, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	f, wakeups, issueID, writerAgent, reviewerAgent := handoffFixture(t)
	acceptorAgent := f.privateAgentOwnedBy(t, f.UserID, "workflow-acceptor")
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
	source.Files = append(source.Files, AgentSkillFileData{Path: "runtime/policy.json", Content: `{"format_version":1,"autonomous_trivial":{"enabled":true,"acceptor_agent_ids":["` + acceptorAgent + `"]}}`})
	pinned, err := wakeups.Tasks.NewIssueWorkflowPolicy(source)
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := json.Marshal(pinned)
	f.Exec(t, `UPDATE issue SET workflow_policy=$2 WHERE id=$1`, issueID, archive)
	writerTask := handoffSourceTask(t, f, issueID, writerAgent)
	f.Exec(t, `UPDATE agent_task_queue SET session_id='writer-session' WHERE id=$1`, writerTask)
	firstInput := handoffInput(writerTask, parseTestUUID(t, reviewerAgent))
	firstInput.Candidates = []HandoffCandidate{}
	first, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), writerTask, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, writerTask)
	wakeDispatch(t, wakeups, first)
	firstStored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: first.ID, WorkspaceID: first.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	reviewerTask := firstStored.LastTaskID
	f.Exec(t, `UPDATE agent_task_queue SET status='running',started_at=now(),session_id='reviewer-session' WHERE id=$1`, reviewerTask)
	bindWorkflowTestTask(t, f, issueID, reviewerTask)
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	svc := WorkflowAuthorityService{Tasks: wakeups.Tasks}
	if err := svc.RegisterReview(ctx, current.WorkspaceID, issueID, WorkflowActor{
		Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(reviewerTask),
	}, WorkflowReviewInput{CandidateID: util.UUIDToString(current.WorkflowCandidateID), Verdict: "pass", PRReviewURLs: []string{}}); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, reviewerTask)
	secondInput := handoffInput(reviewerTask, parseTestUUID(t, acceptorAgent))
	secondInput.Candidates = []HandoffCandidate{}
	second, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), reviewerTask, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, wakeups, second)
	secondStored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: second.ID, WorkspaceID: second.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	acceptorTask := secondStored.LastTaskID
	f.Exec(t, `UPDATE agent_task_queue SET status='running',started_at=now(),session_id='acceptor-session' WHERE id=$1`, acceptorTask)
	bindWorkflowTestTask(t, f, issueID, acceptorTask)
	return f, svc, issueID, acceptorTask
}

func TestWorkflowAuthorityAutonomousRequestFinalizesOnlyAfterRequesterSuccess(t *testing.T) {
	for _, terminal := range []string{"completed", "failed"} {
		t.Run(terminal, func(t *testing.T) {
			f, svc, issueID, acceptorTask := workflowAutonomousCandidate(t)
			ctx := context.Background()
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "agent", ID: util.UUIDToString(issue.AssigneeID), SourceTaskID: util.UUIDToString(acceptorTask)}
			request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
				ExpectedRevision: issue.Revision, ClassificationReason: "Scoped routine change; the exact completed review passed."}
			state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, request)
			if err != nil || state != "requested" {
				t.Fatalf("autonomous request: %q, %v", state, err)
			}
			if got := f.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE issue_id=$1`, issueID); got != 0 {
				t.Fatalf("uncompleted requester released %d delivery intents", got)
			}
			if current, err := f.q.GetIssue(ctx, issueID); err != nil || current.Status == "done" {
				t.Fatalf("autonomous request completed while task still running: %+v, %v", current, err)
			}
			processed, err := svc.FinalizeNextRequestedAcceptance(ctx)
			if err != nil || processed {
				t.Fatalf("running request prematurely finalized: %v, %v", processed, err)
			}
			f.Exec(t, `UPDATE agent_task_queue SET status=$2,completed_at=now() WHERE id=$1`, acceptorTask, terminal)
			processed, err = svc.FinalizeNextRequestedAcceptance(ctx)
			if err != nil || !processed {
				t.Fatalf("terminal request not reconciled: %v, %v", processed, err)
			}
			final, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			accepted := terminal == "completed"
			if (final.Status == "done") != accepted {
				t.Fatalf("terminal %s produced issue status %s", terminal, final.Status)
			}
			wantState := "blocked"
			if accepted {
				wantState = "accepted"
			}
			if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state=$2`, issueID, wantState); got != 1 {
				t.Fatalf("terminal %s left %d %s acceptance rows", terminal, got, wantState)
			}
		})
	}
}

func TestWorkflowAuthorityCancellationBlocksPendingAutonomousAcceptance(t *testing.T) {
	f, svc, issueID, acceptorTask := workflowAutonomousCandidate(t)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "agent", ID: util.UUIDToString(issue.AssigneeID), SourceTaskID: util.UUIDToString(acceptorTask)}
	request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
		ExpectedRevision: issue.Revision, ClassificationReason: "Scoped routine change with completed independent review."}
	state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, request)
	if err != nil || state != "requested" {
		t.Fatalf("autonomous request: %q, %v", state, err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, acceptorTask)
	f.Exec(t, `UPDATE issue SET status='cancelled',revision=revision+1 WHERE id=$1`, issueID)
	processed, err := svc.FinalizeNextRequestedAcceptance(ctx)
	if err != nil || !processed {
		t.Fatalf("cancelled acceptance was not reconciled: processed=%v err=%v", processed, err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='blocked'`, issueID); got != 1 {
		t.Fatalf("cancelled request has %d blocked ledger rows, want one", got)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE issue_id=$1`, issueID); got != 0 {
		t.Fatalf("cancelled request released %d delivery intents", got)
	}
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil || current.Status != "cancelled" {
		t.Fatalf("finalizer changed cancelled issue: %+v, %v", current, err)
	}
}

func TestWorkflowAuthorityNamedHumanAcceptanceException(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidate(t)
	ctx := context.Background()
	otherMember := f.member(t, "named-acceptor")
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision}
	if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: otherMember}, request); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("non-recipient accepted without scoped grant: %v", err)
	}
	_, err = svc.GrantException(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: f.UserID},
		WorkflowExceptionInput{CandidateID: request.CandidateID, ExpectedRevision: issue.Revision,
			Scope: "acceptance", GrantDetails: map[string]any{"human_actor_id": otherMember},
			Reason:       "The named member is the authorized acceptance owner.",
			Consequences: "This member may accept only this exact candidate."})
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.ReadState(ctx, current.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: otherMember})
	if err != nil || !view.AvailableActions.AcceptHuman || len(view.Exceptions) != 1 ||
		view.Exceptions[0].GrantDetails["human_actor_id"] != otherMember {
		t.Fatalf("named grant not visible/effective: %+v, %v", view, err)
	}
	request.ExpectedRevision = current.Revision
	state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: otherMember}, request)
	if err != nil || state != "accepted" {
		t.Fatalf("named grant acceptance: %q, %v", state, err)
	}
}

func TestWorkflowAuthorityRejectionFallsBackToCurrentRuntime(t *testing.T) {
	f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision}
	if state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, request); err != nil || state != "accepted" {
		t.Fatalf("accept before fallback: %q, %v", state, err)
	}
	writer, err := f.q.GetAgentTask(ctx, writerTask)
	if err != nil {
		t.Fatal(err)
	}
	newRuntime := parseTestUUID(t, f.Runtime(t, "replacement-writer-runtime", testutil.Cols{"owner_id": f.UserID}))
	f.Exec(t, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, writer.AgentID, newRuntime)
	accepted, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RejectWorkflow(ctx, issue.WorkspaceID, issueID, actor, WorkflowRejectionInput{
		CandidateID: request.CandidateID, ExpectedRevision: accepted.Revision, Kind: "in_scope_defect",
		Reason: "Reconcile the exact defect before another review.", ResumeTaskID: util.UUIDToString(writerTask),
	}); err != nil {
		t.Fatalf("runtime replacement blocked rejection: %v", err)
	}
	var mode, note string
	if err := f.Pool.QueryRow(ctx, `SELECT context_mode,continuity_note FROM issue_workflow_rejection WHERE issue_id=$1`, issueID).Scan(&mode, &note); err != nil || mode != "fresh" || !strings.Contains(note, "runtime changed") {
		t.Fatalf("continuity fallback not recorded: %s %q %v", mode, note, err)
	}
	var queuedRuntime, rerun pgtype.UUID
	var handoffNote pgtype.Text
	if err := f.Pool.QueryRow(ctx, `SELECT runtime_id,rerun_of_task_id,handoff_note FROM agent_task_queue
		WHERE issue_id=$1 AND status='queued' AND agent_id=$2 ORDER BY created_at DESC LIMIT 1`, issueID, writer.AgentID).Scan(
		&queuedRuntime, &rerun, &handoffNote); err != nil || queuedRuntime != newRuntime || rerun.Valid ||
		!strings.Contains(handoffNote.String, "runtime changed") {
		t.Fatalf("fresh continuation misbound after runtime change: %v %v %q %v", queuedRuntime, rerun, handoffNote.String, err)
	}
}

func TestWorkflowAuthorityHumanAcceptAndExactRejectionReplay(t *testing.T) {
	f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	view, err := svc.ReadState(ctx, before.WorkspaceID, issueID, actor)
	if err != nil || !view.AvailableActions.AcceptHuman || len(view.RetainedContextOptions) != 1 ||
		view.RetainedContextOptions[0].TaskID != util.UUIDToString(writerTask) {
		t.Fatalf("human workflow readback: %+v, %v", view, err)
	}
	if _, err := f.Pool.Exec(ctx, `UPDATE issue SET status='done',revision=revision+1 WHERE id=$1`, issueID); err == nil {
		t.Fatal("ordinary done write bypassed exact candidate acceptance fence")
	}
	request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision}
	state, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, request)
	if err != nil || state != "accepted" {
		t.Fatalf("human acceptance: %q, %v", state, err)
	}
	state, err = svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, request)
	if err != nil || state != "accepted" {
		t.Fatalf("exact acceptance replay: %q, %v", state, err)
	}
	changed := request
	changed.ClassificationReason = "changed after response"
	if _, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, changed); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("changed acceptance replay: %v", err)
	}
	accepted, err := f.q.GetIssue(ctx, issueID)
	if err != nil || accepted.Status != "done" || accepted.Revision != before.Revision+1 ||
		f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, issueID) != 1 {
		t.Fatalf("acceptance did not atomically finish exact issue revision: %+v, %v", accepted, err)
	}
	rejection := WorkflowRejectionInput{CandidateID: request.CandidateID, ExpectedRevision: accepted.Revision,
		Kind: "scope_change", Reason: "New request changes the objective; reconcile it before editing.", ResumeTaskID: util.UUIDToString(writerTask)}
	if err := svc.RejectWorkflow(ctx, accepted.WorkspaceID, issueID, actor, rejection); err != nil {
		t.Fatalf("reject accepted candidate: %v", err)
	}
	if err := svc.RejectWorkflow(ctx, accepted.WorkspaceID, issueID, actor, rejection); err != nil {
		t.Fatalf("exact rejection replay: %v", err)
	}
	reopened, err := f.q.GetIssue(ctx, issueID)
	if err != nil || reopened.Status != "in_progress" || reopened.WorkflowCandidateID.Valid ||
		f.Count(t, `SELECT count(*) FROM issue_workflow_rejection WHERE issue_id=$1`, issueID) != 1 ||
		f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND rerun_of_task_id=$2 AND status='queued'`, issueID, writerTask) != 1 {
		t.Fatalf("rejection did not atomically revoke and continue writer: %+v, %v", reopened, err)
	}
	claimed, err := svc.Tasks.ClaimTask(ctx, reopened.AssigneeID)
	if err != nil || claimed == nil || claimed.RerunOfTaskID != writerTask {
		t.Fatalf("rejection continuation did not pass native claim: %+v, %v", claimed, err)
	}
	started, err := svc.Tasks.StartTask(ctx, claimed.ID)
	if err != nil || started == nil || started.Status != "running" {
		t.Fatalf("rejection continuation did not pass native start: %+v, %v", started, err)
	}
}

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

func workflowCandidateReviewURLs(candidates []HandoffCandidate) []string {
	if len(candidates) == 0 {
		return []string{}
	}
	links := make([]string, len(candidates))
	for i, candidate := range candidates {
		links[i] = candidate.PRURL + "#review"
	}
	return links
}

func workflowReviewedHumanCandidate(t *testing.T, format2 ...bool) (principalFixture, WorkflowAuthorityService, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	return workflowReviewedHumanCandidateForRoles(t, len(format2) > 0 && format2[0], nil)
}

func workflowReviewedHumanCandidateForRoles(t *testing.T, format2 bool, roles []string) (principalFixture, WorkflowAuthorityService, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	return workflowReviewedHumanCandidateForCandidates(t, format2, roles, nil)
}

func workflowReviewedHumanCandidateForCandidates(t *testing.T, format2 bool, roles []string, candidates []HandoffCandidate) (principalFixture, WorkflowAuthorityService, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	f, wakeups, issueID, writerAgent, reviewerAgent := handoffFixture(t)
	ctx := context.Background()
	if format2 {
		f.Exec(t, `INSERT INTO issue_status(workspace_id,key,name,category,color,position)
			VALUES($1,'pr_ready','PR Ready','started','#22c55e',1)`, f.WorkspaceID)
	}
	if format2 || roles != nil {
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
		config := map[string]any{"format_version": 1}
		if format2 {
			config["format_version"], config["accepted_status_key"], config["outcome_agent_id"] = 2, "pr_ready", writerAgent
		}
		if roles != nil {
			config["human"] = map[string]any{"accept_roles": roles}
		}
		rawConfig, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		source.Files = append(source.Files, AgentSkillFileData{Path: "runtime/policy.json", Content: string(rawConfig)})
		pinned, err := wakeups.Tasks.NewIssueWorkflowPolicy(source)
		if err != nil {
			t.Fatal(err)
		}
		archive, _ := json.Marshal(pinned)
		f.Exec(t, `UPDATE issue SET workflow_policy=$2 WHERE id=$1`, issueID, archive)
	}
	writerTask := handoffSourceTask(t, f, issueID, writerAgent)
	f.Exec(t, `UPDATE agent_task_queue SET session_id='writer-session' WHERE id=$1`, writerTask)
	firstInput := handoffInput(writerTask, parseTestUUID(t, reviewerAgent))
	firstInput.Candidates = append([]HandoffCandidate(nil), candidates...)
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
	}, WorkflowReviewInput{CandidateID: util.UUIDToString(state.WorkflowCandidateID), Verdict: "pass", PRReviewURLs: workflowCandidateReviewURLs(candidates)}); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, reviewerTask)
	secondInput := handoffInput(reviewerTask, pgtype.UUID{})
	secondInput.AgentID, secondInput.AssigneeType, secondInput.AssigneeID = "", "member", f.UserID
	secondInput.Candidates = append([]HandoffCandidate(nil), candidates...)
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
	f, svc, issueID, writerTask := workflowReviewedHumanCandidateForRoles(t, false, []string{"owner", "admin"})
	ctx := context.Background()
	otherMember := f.member(t, "named-acceptor")
	writer, err := f.q.GetAgentTask(ctx, writerTask)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2,revision=revision+1 WHERE id=$1`, issueID, writer.AgentID)
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision}
	if _, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: otherMember}, request); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("policy-excluded member accepted without scoped grant: %v", err)
	}
	_, err = svc.GrantException(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: f.UserID},
		WorkflowExceptionInput{CandidateID: request.CandidateID, ExpectedRevision: issue.Revision,
			Scope: "acceptance", GrantDetails: map[string]any{"human_actor_id": otherMember},
			Reason:       "The named member is the authorized acceptance owner.",
			Consequences: "This member may accept only this exact candidate."})
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE issue SET status='blocked',revision=revision+1 WHERE id=$1`, issueID)
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
	unnamedMember := f.member(t, "unnamed-acceptor")
	unnamedActor := WorkflowActor{Type: "member", ID: unnamedMember}
	unnamedView, err := svc.ReadState(ctx, current.WorkspaceID, issueID, unnamedActor)
	if err != nil || unnamedView.AvailableActions.AcceptHuman || len(unnamedView.AcceptanceBlockers) != 0 {
		t.Fatalf("candidate grant leaked to unnamed member or global blockers: %+v, %v", unnamedView, err)
	}
	if _, err := svc.AcceptWorkflow(ctx, current.WorkspaceID, issueID, unnamedActor, request); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("candidate grant authorized unnamed member: %v", err)
	}
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

func TestWorkflowFormat2NoPRAcceptanceCompletesWithoutOutcomeCeremony(t *testing.T) {
	for _, sendFalse := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted", true: "legacy_false"}[sendFalse], func(t *testing.T) {
			f, svc, issueID, _ := workflowReviewedHumanCandidate(t, true)
			ctx := context.Background()
			before, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision}
			if sendFalse {
				incomplete := false
				request.OutcomeComplete = &incomplete
			}
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			if _, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, request); err != nil {
				t.Fatal(err)
			}
			current, err := f.q.GetIssue(ctx, issueID)
			if err != nil || current.Status != "done" || current.Revision != before.Revision+1 {
				t.Fatalf("explicit no-PR acceptance did not complete: %+v %v", current, err)
			}
			view, err := svc.ReadState(ctx, current.WorkspaceID, issueID, actor)
			if err != nil || view.Acceptance == nil || !view.Acceptance.OutcomeComplete || view.Acceptance.OutcomeTaskID != "" {
				t.Fatalf("completion manufactured an outcome run: %+v %v", view.Acceptance, err)
			}
		})
	}
}

func workflowLegacyOutcomeFixture(t *testing.T, status string) (principalFixture, WorkflowAuthorityService, db.Issue, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t, true)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := f.q.GetAgentTask(ctx, writerTask)
	if err != nil {
		t.Fatal(err)
	}
	acceptanceID := dbid.NewV7()
	taskID := parseTestUUID(t, f.Task(t, util.UUIDToString(writer.AgentID), testutil.Cols{
		"issue_id": util.UUIDToString(issueID), "runtime_id": util.UUIDToString(writer.RuntimeID), "status": status,
	}))
	f.Insert(t, "issue_workflow_acceptance", testutil.Cols{
		"id": util.UUIDToString(acceptanceID), "workspace_id": f.WorkspaceID, "issue_id": util.UUIDToString(issueID),
		"candidate_id": util.UUIDToString(issue.WorkflowCandidateID), "mode": "human", "actor_type": "member", "actor_id": f.UserID,
		"state": "accepted", "issue_revision": issue.Revision + 1,
		"policy_version":     testutil.Raw("(SELECT workflow_policy->>'version' FROM issue WHERE id='" + util.UUIDToString(issueID) + "')"),
		"authority_snapshot": "{}", "accepted_at": testutil.Raw("now()"), "completion_version": 2,
		"accepted_status_key": "pr_ready", "outcome_agent_id": util.UUIDToString(writer.AgentID), "outcome_task_id": util.UUIDToString(taskID),
	})
	f.Exec(t, `UPDATE agent_task_queue SET context=jsonb_build_object('workflow_outcome',
		jsonb_build_object('acceptance_id',$2::text,'candidate_id',$3::text)) WHERE id=$1`, taskID, acceptanceID, issue.WorkflowCandidateID)
	bindWorkflowTestTask(t, f, issueID, taskID)
	f.Exec(t, `UPDATE issue SET status='pr_ready',assignee_type='agent',assignee_id=$2,revision=revision+1 WHERE id=$1`, issueID, writer.AgentID)
	issue, err = f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	return f, svc, issue, acceptanceID, taskID
}

func TestWorkflowFormat2HumanCompletionRetiresAnyActiveOutcomeTask(t *testing.T) {
	for _, status := range []string{"queued", "deferred", "dispatched", "running", "waiting_local_directory"} {
		t.Run(status, func(t *testing.T) {
			f, svc, issue, acceptanceID, taskID := workflowLegacyOutcomeFixture(t, status)
			ctx := context.Background()
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			view, err := svc.ReadState(ctx, issue.WorkspaceID, issue.ID, actor)
			if err != nil || !view.AvailableActions.CompleteOutcome {
				t.Fatalf("human completion hidden: %+v %v", view.AvailableActions, err)
			}
			changed, err := svc.ChangeCompletion(ctx, issue.WorkspaceID, issue.ID, acceptanceID, actor, "complete",
				WorkflowCompletionActionInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision})
			if err != nil || !changed {
				t.Fatalf("human completion blocked by %s: %v %v", status, changed, err)
			}
			current, err := f.q.GetIssue(ctx, issue.ID)
			if err != nil || current.Status != "done" || current.Revision != issue.Revision+1 {
				t.Fatalf("completion: %+v %v", current, err)
			}
			task, err := f.q.GetAgentTask(ctx, taskID)
			if err != nil || task.Status != "cancelled" || !task.CompletedAt.Valid || !task.Error.Valid {
				t.Fatalf("outcome history: %+v %v", task, err)
			}
			view, err = svc.ReadState(ctx, issue.WorkspaceID, issue.ID, actor)
			if err != nil || view.Acceptance == nil || view.Acceptance.OutcomeTaskID != util.UUIDToString(taskID) || !view.Acceptance.OutcomeComplete {
				t.Fatalf("outcome evidence lost: %+v %v", view.Acceptance, err)
			}
			if err := svc.Tasks.ValidateWorkflowOutcomeTask(ctx, task); !errors.Is(err, ErrWorkflowAuthorityConflict) {
				t.Fatalf("retired outcome is still claimable: %v", err)
			}
		})
	}
}

func TestWorkflowFormat2ReconcileRetiresLegacyOutcome(t *testing.T) {
	f, svc, issue, acceptanceID, taskID := workflowLegacyOutcomeFixture(t, "running")
	ctx := context.Background()
	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM issue WHERE id=$1 FOR UPDATE`, issue.ID); err != nil {
		t.Fatal(err)
	}
	cancelled, done, err := ReconcileWorkflowCompletion(ctx, tx, f.q.WithTx(tx), issue, acceptanceID)
	if err != nil || !done || cancelled == nil || cancelled.ID != taskID {
		t.Fatalf("legacy reconciliation: %+v %v %v", cancelled, done, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	svc.Tasks.NotifyWorkflowCompletionTask(ctx, issue.WorkspaceID, cancelled)
	current, err := f.q.GetIssue(ctx, issue.ID)
	if err != nil || current.Status != "done" {
		t.Fatalf("reconciliation: %+v %v", current, err)
	}
}

func TestWorkflowFormat2AlreadyMergedAcceptanceReconcilesWithoutRetryMarker(t *testing.T) {
	f, svc, issue, acceptanceID, taskID := workflowLegacyOutcomeFixture(t, "running")
	ctx := context.Background()
	prs, _ := json.Marshal([]HandoffCandidate{{RepositoryURL: "https://forge.example/team/repo", PRURL: "https://forge.example/team/repo/pulls/1", CommitSHA: strings.Repeat("a", 40)}})
	f.Exec(t, `UPDATE issue_workflow_candidate SET pr_set=$2 WHERE id=$1`, issue.WorkflowCandidateID, prs)
	f.Insert(t, "issue_workflow_delivery", testutil.Cols{
		"id": util.UUIDToString(dbid.NewV7()), "workspace_id": f.WorkspaceID, "issue_id": util.UUIDToString(issue.ID), "acceptance_id": util.UUIDToString(acceptanceID),
		"candidate_id": util.UUIDToString(issue.WorkflowCandidateID), "ordinal": 0, "provider": "forgejo", "provider_binding_id": util.UUIDToString(dbid.NewV7()),
		"repository_url": "https://forge.example/team/repo", "pr_url": "https://forge.example/team/repo/pulls/1",
		"repo_owner": "team", "repo_name": "repo", "pr_number": 1, "expected_head_sha": strings.Repeat("a", 40),
		"action": "ready", "status": "delivered", "merged_at": testutil.Raw("now()"),
	})
	f.Insert(t, "issue_status", testutil.Cols{"workspace_id": f.WorkspaceID, "key": "another_started", "name": "Another started", "category": "started", "color": "#000000"})
	f.Exec(t, `UPDATE issue SET status='another_started',revision=revision+1 WHERE id=$1`, issue.ID)
	processed, err := svc.RetryNextWorkflowCompletionDispatch(ctx)
	if err != nil || !processed {
		t.Fatalf("already-merged sweep: %v %v", processed, err)
	}
	current, err := f.q.GetIssue(ctx, issue.ID)
	if err != nil || current.Status != "done" {
		t.Fatalf("stranded merged issue: %+v %v", current, err)
	}
	task, err := f.q.GetAgentTask(ctx, taskID)
	if err != nil || task.Status != "cancelled" {
		t.Fatalf("legacy run not retired: %+v %v", task, err)
	}
	if processed, err := svc.RetryNextWorkflowCompletionDispatch(ctx); err != nil || processed {
		t.Fatalf("completed sweep repeated: %v %v", processed, err)
	}
}

func TestWorkflowFormat2ReconciliationPreservesClosedStatus(t *testing.T) {
	f, svc, issue, acceptanceID, taskID := workflowLegacyOutcomeFixture(t, "running")
	ctx := context.Background()
	f.Exec(t, `UPDATE issue SET status='cancelled',revision=revision+1 WHERE id=$1`, issue.ID)
	if processed, err := svc.RetryNextWorkflowCompletionDispatch(ctx); err != nil || processed {
		t.Fatalf("closed issue selected: %v %v", processed, err)
	}
	issue, err := f.q.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	cancelled, done, err := ReconcileWorkflowCompletion(ctx, tx, f.q.WithTx(tx), issue, acceptanceID)
	if err != nil || done || cancelled != nil {
		t.Fatalf("closed issue reopened: %+v %v %v", cancelled, done, err)
	}
	task, err := f.q.GetAgentTask(ctx, taskID)
	if err != nil || task.Status != "running" {
		t.Fatalf("unrelated closed-state work changed: %+v %v", task, err)
	}
}

func TestWorkflowCompletionReasonCountsUnicodeCodepoints(t *testing.T) {
	if _, err := validateWorkflowActionReason(strings.Repeat("验", 500)); err != nil {
		t.Fatalf("valid multibyte reason: %v", err)
	}
	if _, err := validateWorkflowActionReason(strings.Repeat("验", 501)); !errors.Is(err, ErrWorkflowAuthorityInput) {
		t.Fatalf("overlong reason accepted: %v", err)
	}
}

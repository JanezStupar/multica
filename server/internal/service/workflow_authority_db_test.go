package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
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

func workflowReviewedHumanCandidate(t *testing.T, format2 ...bool) (principalFixture, WorkflowAuthorityService, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	f, wakeups, issueID, writerAgent, reviewerAgent := handoffFixture(t)
	ctx := context.Background()
	if len(format2) > 0 && format2[0] {
		f.Exec(t, `INSERT INTO issue_status(workspace_id,key,name,category,color,position)
			VALUES($1,'pr_ready','PR Ready','started','#22c55e',1)`, f.WorkspaceID)
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
		source.Files = append(source.Files, AgentSkillFileData{Path: "runtime/policy.json", Content: `{"format_version":2,"accepted_status_key":"pr_ready","outcome_agent_id":"` + writerAgent + `"}`})
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

func TestWorkflowFormat2NoPRRequiresCompletedOutcomeTask(t *testing.T) {
	f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t, true)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	complete := false
	request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID),
		ExpectedRevision: before.Revision, OutcomeComplete: &complete}
	if _, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, request); err != nil {
		t.Fatalf("format2 no-PR acceptance: %v", err)
	}
	ready, err := f.q.GetIssue(ctx, issueID)
	if err != nil || ready.Status != "pr_ready" {
		t.Fatalf("acceptance prematurely completed: %+v %v", ready, err)
	}
	view, err := svc.ReadState(ctx, ready.WorkspaceID, issueID, actor)
	if err != nil || view.Acceptance == nil || view.Acceptance.OutcomeTaskID == "" ||
		!view.Acceptance.OutcomeTaskActive || view.Acceptance.OutcomeComplete {
		t.Fatalf("outcome task unavailable: %+v %v", view.Acceptance, err)
	}
	outcomeAgent := ready.AssigneeID
	var resume pgtype.UUID
	if err := f.Pool.QueryRow(ctx, `SELECT rerun_of_task_id FROM agent_task_queue WHERE id=$1`,
		mustAuthorityUUID(view.Acceptance.OutcomeTaskID)).Scan(&resume); err != nil || resume != writerTask {
		t.Fatalf("same-agent writer continuity missing: %v %v", resume, err)
	}
	claimed, err := svc.Tasks.ClaimTask(ctx, outcomeAgent)
	if err != nil || claimed == nil || util.UUIDToString(claimed.ID) != view.Acceptance.OutcomeTaskID {
		var special, general bool
		_ = f.Pool.QueryRow(ctx, `SELECT workflow_outcome_task_claimable($1,$2),workflow_task_claimable($1,$2)`,
			mustAuthorityUUID(view.Acceptance.OutcomeTaskID), issueID).Scan(&special, &general)
		var details []byte
		_ = f.Pool.QueryRow(ctx, `SELECT jsonb_build_object(
			'frozen',i.workflow_frozen,'candidate_match',i.workflow_candidate_id=a.candidate_id,
			'policy_match',i.workflow_policy->>'version'=a.policy_version,
			'status_match',i.status=a.accepted_status_key,'version',a.completion_version,'state',a.state,
			'outcome_complete',a.outcome_complete,'task_match',a.outcome_task_id=t.id,
			'agent_match',a.outcome_agent_id=t.agent_id,'revoked',a.revoked_at IS NOT NULL,
			'task_issue_match',t.issue_id=i.id,'delivery_bad',EXISTS(SELECT 1 FROM issue_workflow_delivery d
			 WHERE d.acceptance_id=a.id AND (d.status<>'delivered' OR d.merged_at IS NULL)),
			'delivery_count',
			(SELECT count(*) FROM issue_workflow_delivery d WHERE d.acceptance_id=a.id),
			'pr_count',(SELECT jsonb_array_length(c.pr_set) FROM issue_workflow_candidate c WHERE c.id=a.candidate_id))
			FROM issue i JOIN issue_workflow_acceptance a ON a.issue_id=i.id
			JOIN agent_task_queue t ON t.id=$1 WHERE i.id=$2`,
			mustAuthorityUUID(view.Acceptance.OutcomeTaskID), issueID).Scan(&details)
		t.Fatalf("exact outcome task did not claim: %+v %v special=%v general=%v details=%s", claimed, err, special, general, details)
	}
	started, err := svc.Tasks.StartTask(ctx, claimed.ID)
	if err != nil || started == nil || started.Status != "running" {
		t.Fatalf("outcome task did not start: %+v %v", started, err)
	}
	bindWorkflowTestTask(t, f, issueID, claimed.ID)
	view, err = svc.ReadState(ctx, ready.WorkspaceID, issueID, WorkflowActor{Type: "agent",
		ID: util.UUIDToString(outcomeAgent), SourceTaskID: view.Acceptance.OutcomeTaskID})
	if err != nil || !view.AvailableActions.CompleteOutcome {
		t.Fatalf("running outcome task lacks completion action: %+v %v", view.AvailableActions, err)
	}
	changed, err := svc.ChangeCompletion(ctx, ready.WorkspaceID, issueID,
		mustAuthorityUUID(view.Acceptance.ID), WorkflowActor{Type: "agent", ID: util.UUIDToString(outcomeAgent),
			SourceTaskID: view.Acceptance.OutcomeTaskID}, "complete", WorkflowCompletionActionInput{
			CandidateID: request.CandidateID, ExpectedRevision: ready.Revision, Reason: "Deployment and QA passed."})
	if err != nil || !changed {
		t.Fatalf("outcome acknowledgment: %v %v", changed, err)
	}
	if issue, err := f.q.GetIssue(ctx, issueID); err != nil || issue.Status != "pr_ready" {
		t.Fatalf("running task acknowledgment completed early: %+v %v", issue, err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, claimed.ID)
	if processed, err := svc.FinalizeNextOutcomeAcknowledgment(ctx); err != nil || !processed {
		t.Fatalf("completed outcome task not finalized: %v %v", processed, err)
	}
	if issue, err := f.q.GetIssue(ctx, issueID); err != nil || issue.Status != "done" {
		t.Fatalf("completed outcome did not finish issue: %+v %v", issue, err)
	}
}

func TestWorkflowFormat2OutcomeProfileReselectionUsesFreshContext(t *testing.T) {
	f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t, true)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	var policyVersion string
	if err := f.Pool.QueryRow(ctx, `SELECT workflow_policy->>'version' FROM issue WHERE id=$1`, issueID).
		Scan(&policyVersion); err != nil {
		t.Fatal(err)
	}
	var outcomeAgent pgtype.UUID
	if err := f.Pool.QueryRow(ctx, `SELECT agent_id FROM agent_task_queue WHERE id=$1`, writerTask).
		Scan(&outcomeAgent); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `INSERT INTO issue_workflow_profile(id,workspace_id,issue_id,agent_id,policy_version,
		snapshot,digest,revision) VALUES($1,$2,$3,$4,$5,'{}'::jsonb,'selected-new-profile',1)`,
		dbid.NewV7(), before.WorkspaceID, issueID, outcomeAgent, policyVersion)
	incomplete := false
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	if _, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
		CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision,
		OutcomeComplete: &incomplete,
	}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.ReadState(ctx, before.WorkspaceID, issueID, actor)
	if err != nil || view.Acceptance == nil {
		t.Fatalf("outcome task missing: %+v %v", view.Acceptance, err)
	}
	var resume pgtype.UUID
	var note string
	if err := f.Pool.QueryRow(ctx, `SELECT rerun_of_task_id,handoff_note FROM agent_task_queue WHERE id=$1`,
		mustAuthorityUUID(view.Acceptance.OutcomeTaskID)).Scan(&resume, &note); err != nil || resume.Valid ||
		!strings.Contains(note, "selected agent profile changed") {
		t.Fatalf("reselected profile reused incompatible writer context: resume=%v note=%q err=%v", resume, note, err)
	}
}

func TestWorkflowFormat2FailedOutcomeTaskCanBeRetried(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidate(t, true)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	complete := false
	request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID),
		ExpectedRevision: before.Revision, OutcomeComplete: &complete}
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	if _, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, request); err != nil {
		t.Fatal(err)
	}
	ready, _ := f.q.GetIssue(ctx, issueID)
	view, _ := svc.ReadState(ctx, ready.WorkspaceID, issueID, actor)
	firstTask := view.Acceptance.OutcomeTaskID
	f.Exec(t, `UPDATE agent_task_queue SET status='failed',completed_at=now() WHERE id=$1`, mustAuthorityUUID(firstTask))
	view, err = svc.ReadState(ctx, ready.WorkspaceID, issueID, actor)
	if err != nil || !view.AvailableActions.RetryOutcome || view.Acceptance.OutcomeTaskActive {
		t.Fatalf("failed outcome state is not recoverable: %+v %v", view.Acceptance, err)
	}
	if changed, err := svc.ChangeCompletion(ctx, ready.WorkspaceID, issueID,
		mustAuthorityUUID(view.Acceptance.ID), actor, "retry-outcome", WorkflowCompletionActionInput{
			CandidateID: request.CandidateID, ExpectedRevision: ready.Revision, Reason: "Retry on available runtime."}); err != nil || !changed {
		t.Fatalf("retry failed outcome task: %v %v", changed, err)
	}
	if _, err := svc.ChangeCompletion(ctx, ready.WorkspaceID, issueID,
		mustAuthorityUUID(view.Acceptance.ID), actor, "retry-outcome", WorkflowCompletionActionInput{
			CandidateID: request.CandidateID, ExpectedRevision: ready.Revision, Reason: "Stale retry."}); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("retry reused stale issue revision: %v", err)
	}
	view, err = svc.ReadState(ctx, ready.WorkspaceID, issueID, actor)
	if err != nil || view.Acceptance.OutcomeTaskID == firstTask || !view.Acceptance.OutcomeTaskActive ||
		f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='failed'`, mustAuthorityUUID(firstTask)) != 1 {
		t.Fatalf("retry lost history or did not queue one replacement: %+v %v", view.Acceptance, err)
	}
}

func TestWorkflowFormat2DispatchFailureCanBeRescheduledAndRecovered(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidate(t, true)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	incomplete := false
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	if _, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
		CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision,
		OutcomeComplete: &incomplete,
	}); err != nil {
		t.Fatal(err)
	}
	ready, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.ReadState(ctx, ready.WorkspaceID, issueID, actor)
	if err != nil || view.Acceptance == nil || view.Acceptance.OutcomeTaskID == "" {
		t.Fatalf("first outcome task unavailable: %+v %v", view.Acceptance, err)
	}
	firstTask := view.Acceptance.OutcomeTaskID
	f.Exec(t, `UPDATE agent_task_queue SET status='cancelled',completed_at=now() WHERE id=$1`, mustAuthorityUUID(firstTask))
	f.Exec(t, `UPDATE issue_workflow_acceptance SET outcome_task_id=NULL,
		last_error_class='outcome_dispatch_failed',outcome_dispatch_attempt_count=1,
		outcome_next_attempt_at=now()+interval '1 hour' WHERE id=$1`, mustAuthorityUUID(view.Acceptance.ID))
	view, err = svc.ReadState(ctx, ready.WorkspaceID, issueID, actor)
	if err != nil || view.Acceptance.Blocker != "outcome_dispatch_failed" ||
		!view.AvailableActions.RetryOutcome || view.Acceptance.OutcomeTaskActive || view.Acceptance.OutcomePending {
		t.Fatalf("dispatch failure not visible or retryable: %+v %v", view, err)
	}
	if changed, err := svc.ChangeCompletion(ctx, ready.WorkspaceID, issueID,
		mustAuthorityUUID(view.Acceptance.ID), actor, "retry-outcome", WorkflowCompletionActionInput{
			CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: ready.Revision,
			Reason: "Outcome runtime is restored."}); err != nil || !changed {
		t.Fatalf("manual dispatch retry scheduling: %v %v", changed, err)
	}
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil || current.Revision != ready.Revision+1 {
		t.Fatalf("retry did not advance revision: %+v %v", current, err)
	}
	if worked, err := svc.RetryNextWorkflowCompletionDispatch(ctx); err != nil || !worked {
		t.Fatalf("scheduled dispatch did not recover: %v %v", worked, err)
	}
	view, err = svc.ReadState(ctx, ready.WorkspaceID, issueID, actor)
	if err != nil || view.Acceptance.OutcomeTaskID == "" || view.Acceptance.OutcomeTaskID == firstTask ||
		view.Acceptance.Blocker != "" || !view.Acceptance.OutcomeTaskActive {
		t.Fatalf("recovery did not queue one new outcome task: %+v %v", view.Acceptance, err)
	}
}

func TestWorkflowFormat2CompletionReconcileRetryKeepsExistingOutcomeTask(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidate(t, true)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	incomplete := false
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	if _, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
		CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision,
		OutcomeComplete: &incomplete,
	}); err != nil {
		t.Fatal(err)
	}
	ready, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.ReadState(ctx, ready.WorkspaceID, issueID, actor)
	if err != nil || view.Acceptance == nil || view.Acceptance.OutcomeTaskID == "" {
		t.Fatalf("retained outcome task missing: %+v %v", view.Acceptance, err)
	}
	taskID := mustAuthorityUUID(view.Acceptance.OutcomeTaskID)
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
	f.Exec(t, `UPDATE issue_workflow_acceptance SET outcome_complete=true,outcome_completed_at=now(),
		last_error_class='completion_reconcile_failed',outcome_dispatch_attempt_count=1,
		outcome_next_attempt_at=now()-interval '1 second' WHERE id=$1`, mustAuthorityUUID(view.Acceptance.ID))
	priorTasks := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, issueID)
	if worked, err := svc.RetryNextWorkflowCompletionDispatch(ctx); err != nil || !worked {
		t.Fatalf("completion retry skipped retained task: %v %v", worked, err)
	}
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil || current.Status != "done" ||
		f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1`, issueID) != priorTasks {
		t.Fatalf("completion retry did not finish without new task: %+v %v", current, err)
	}
}

func TestWorkflowFormat2HumanOutcomeAcknowledgmentCancelsQueuedTaskAndAdvancesRevision(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidate(t, true)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	incomplete := false
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	if _, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
		CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision,
		OutcomeComplete: &incomplete,
	}); err != nil {
		t.Fatal(err)
	}
	ready, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := svc.ReadState(ctx, ready.WorkspaceID, issueID, actor)
	if err != nil || view.Acceptance == nil || !view.Acceptance.OutcomeTaskActive {
		t.Fatalf("expected queued outcome task: %+v %v", view.Acceptance, err)
	}
	input := WorkflowCompletionActionInput{CandidateID: util.UUIDToString(before.WorkflowCandidateID),
		ExpectedRevision: ready.Revision, Reason: strings.Repeat("验", 500)}
	changed, err := svc.ChangeCompletion(ctx, ready.WorkspaceID, issueID,
		mustAuthorityUUID(view.Acceptance.ID), actor, "complete", input)
	if err != nil || !changed {
		t.Fatalf("500-codepoint reason rejected: %v %v", changed, err)
	}
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil || current.Status != "done" || current.Revision != ready.Revision+1 {
		t.Fatalf("human outcome did not finish at one new revision: %+v %v", current, err)
	}
	if count := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='cancelled'`,
		mustAuthorityUUID(view.Acceptance.OutcomeTaskID)); count != 1 {
		t.Fatalf("queued outcome task was not cancelled: %d", count)
	}
	if _, err := svc.ChangeCompletion(ctx, ready.WorkspaceID, issueID,
		mustAuthorityUUID(view.Acceptance.ID), actor, "complete", input); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("stale postaccept revision replay: %v", err)
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

func TestWorkflowFormat2HumanCompletionSerializesWithOutcomeClaim(t *testing.T) {
	for i := 0; i < 4; i++ {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			f, svc, issueID, _ := workflowReviewedHumanCandidate(t, true)
			ctx := context.Background()
			before, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			incomplete := false
			actor := WorkflowActor{Type: "member", ID: f.UserID}
			if _, err := svc.AcceptWorkflow(ctx, before.WorkspaceID, issueID, actor, WorkflowAcceptanceInput{
				CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: before.Revision,
				OutcomeComplete: &incomplete,
			}); err != nil {
				t.Fatal(err)
			}
			ready, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			view, err := svc.ReadState(ctx, ready.WorkspaceID, issueID, actor)
			if err != nil || view.Acceptance == nil {
				t.Fatalf("accepted state: %+v %v", view.Acceptance, err)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			var claim *db.AgentTaskQueue
			var claimErr, completionErr error
			var completed bool
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				claim, claimErr = svc.Tasks.ClaimTask(ctx, ready.AssigneeID)
			}()
			go func() {
				defer wg.Done()
				<-start
				completed, completionErr = svc.ChangeCompletion(ctx, ready.WorkspaceID, issueID,
					mustAuthorityUUID(view.Acceptance.ID), actor, "complete", WorkflowCompletionActionInput{
						CandidateID: util.UUIDToString(before.WorkflowCandidateID), ExpectedRevision: ready.Revision,
						Reason: "Human verified the actual outcome.",
					})
			}()
			close(start)
			wg.Wait()
			if claimErr != nil {
				t.Fatalf("claim race error: %v", claimErr)
			}
			if completed {
				if completionErr != nil || claim != nil {
					t.Fatalf("human completion succeeded beside dispatched task: claim=%+v err=%v", claim, completionErr)
				}
				current, err := f.q.GetIssue(ctx, issueID)
				if err != nil || current.Status != "done" ||
					f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='cancelled'`,
						mustAuthorityUUID(view.Acceptance.OutcomeTaskID)) != 1 {
					t.Fatalf("human completion race did not cancel queued task: %+v %v", current, err)
				}
			} else {
				if claim == nil || !errors.Is(completionErr, ErrWorkflowAuthorityConflict) {
					t.Fatalf("claim winner not fenced: claim=%+v completion=%v", claim, completionErr)
				}
				current, err := f.q.GetIssue(ctx, issueID)
				if err != nil || current.Status != "pr_ready" {
					t.Fatalf("claim winner completed issue: %+v %v", current, err)
				}
			}
		})
	}
}

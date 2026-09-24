package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func workflowSameReviewerAcceptanceFixture(t *testing.T) (principalFixture, WorkflowAuthorityService, pgtype.UUID, pgtype.UUID, string) {
	t.Helper()
	f, wakeups, issueID, writerAgent, reviewerAgent := handoffFixture(t)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	old, err := wakeups.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || old == nil {
		t.Fatalf("decode pinned policy: %v", err)
	}
	source := old.Bundle
	source.ID = util.UUIDToString(dbid.NewV7())
	source.Files = append(source.Files, AgentSkillFileData{Path: "runtime/policy.json", Content: `{"format_version":1,"autonomous_trivial":{"enabled":true,"acceptor_agent_ids":["` + reviewerAgent + `"]}}`})
	pinned, err := wakeups.Tasks.NewIssueWorkflowPolicy(source)
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := json.Marshal(pinned)
	f.Exec(t, `UPDATE issue SET workflow_policy=$2 WHERE id=$1`, issueID, archive)
	writerTask := handoffSourceTask(t, f, issueID, writerAgent)
	f.Exec(t, `UPDATE agent_task_queue SET session_id='writer-session' WHERE id=$1`, writerTask)
	in := handoffInput(writerTask, parseTestUUID(t, reviewerAgent))
	in.Candidates = []HandoffCandidate{}
	handoff, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), writerTask, in)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, writerTask)
	wakeDispatch(t, wakeups, handoff)
	stored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: handoff.ID, WorkspaceID: handoff.WorkspaceID})
	if err != nil || !stored.LastTaskID.Valid {
		t.Fatalf("fresh reviewer was not queued: %+v, %v", stored, err)
	}
	reviewerTask := stored.LastTaskID
	f.Exec(t, `UPDATE agent_task_queue SET status='running',started_at=now(),session_id='reviewer-session' WHERE id=$1`, reviewerTask)
	bindWorkflowTestTask(t, f, issueID, reviewerTask)
	current, err := f.q.GetIssue(ctx, issueID)
	if err != nil || !current.WorkflowCandidateID.Valid {
		t.Fatalf("review candidate unavailable: %+v, %v", current, err)
	}
	svc := WorkflowAuthorityService{Tasks: wakeups.Tasks}
	if err := svc.RegisterReview(ctx, current.WorkspaceID, issueID, WorkflowActor{
		Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(reviewerTask),
	}, WorkflowReviewInput{CandidateID: util.UUIDToString(current.WorkflowCandidateID), Verdict: "pass", PRReviewURLs: []string{}}); err != nil {
		t.Fatalf("register fresh review: %v", err)
	}
	return f, svc, issueID, reviewerTask, reviewerAgent
}

func TestWorkflowSameRunningReviewerCanRequestButOnlySuccessFinalizes(t *testing.T) {
	for _, terminal := range []string{"completed", "failed", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			f, svc, issueID, reviewerTask, reviewerAgent := workflowSameReviewerAcceptanceFixture(t)
			ctx := context.Background()
			issue, err := f.q.GetIssue(ctx, issueID)
			if err != nil {
				t.Fatal(err)
			}
			actor := WorkflowActor{Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(reviewerTask)}
			projection, err := svc.ReadState(ctx, issue.WorkspaceID, issueID, actor)
			if err != nil || !projection.AvailableActions.RequestTrivialAcceptance {
				t.Fatalf("running authorized reviewer request hidden: %+v, %v", projection.AvailableActions, err)
			}
			request := WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID),
				ExpectedRevision: issue.Revision, ClassificationReason: "Scoped trivial change; my independent final review passed."}
			state, err := svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, actor, request)
			if err != nil || state != "requested" {
				t.Fatalf("same running reviewer request=%q err=%v", state, err)
			}
			if got := f.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE issue_id=$1`, issueID); got != 0 {
				t.Fatalf("running reviewer released %d delivery intents", got)
			}
			if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || processed {
				t.Fatalf("running reviewer prematurely finalized: %v, %v", processed, err)
			}
			f.Exec(t, `UPDATE agent_task_queue SET status=$2,completed_at=now() WHERE id=$1`, reviewerTask, terminal)
			if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
				t.Fatalf("terminal reviewer not reconciled: %v, %v", processed, err)
			}
			accepted := terminal == "completed"
			wantState := "blocked"
			if accepted {
				wantState = "accepted"
			}
			if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state=$2`, issueID, wantState); got != 1 {
				t.Fatalf("terminal %s left %d %s acceptance records", terminal, got, wantState)
			}
			if got := f.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND status='done'`, issueID); (got == 1) != accepted {
				t.Fatalf("terminal %s completed issue=%v", terminal, got == 1)
			}
		})
	}
}

func TestWorkflowRunningReviewDoesNotSatisfyAnotherRequester(t *testing.T) {
	f, svc, issueID, reviewerTask, reviewerAgent := workflowSameReviewerAcceptanceFixture(t)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := svc.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || policy == nil {
		t.Fatalf("decode policy: %v", err)
	}
	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	candidate, err := loadCurrentWorkflowCandidate(ctx, tx, issue, policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workflowReviewSatisfied(ctx, tx, issue, candidate, true); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("strict completed-review guard accepted running review: %v", err)
	}
	if _, err := workflowReviewSatisfiedForRequest(ctx, tx, issue, candidate, true, dbid.NewV7()); !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("different requester used running review: %v", err)
	}
	if _, err := workflowReviewSatisfiedForRequest(ctx, tx, issue, candidate, true, reviewerTask); err != nil {
		t.Fatalf("same requester should be eligible for a pending request: %v", err)
	}
	projection, err := svc.ReadState(ctx, issue.WorkspaceID, issueID,
		WorkflowActor{Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(dbid.NewV7())})
	if err != nil {
		t.Fatal(err)
	}
	if projection.AvailableActions.RequestTrivialAcceptance {
		t.Fatal("unbound task was shown pending acceptance authority")
	}
}

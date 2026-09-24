package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func workflowReviewOriginFixture(t *testing.T, mode string) (principalFixture, WorkflowAuthorityService, pgtype.UUID, pgtype.UUID, string) {
	t.Helper()
	f, wakeups, issueID, writerAgent, reviewerAgent := handoffFixture(t)
	ctx := context.Background()
	writerTask := handoffSourceTask(t, f, issueID, writerAgent)
	f.Exec(t, `UPDATE agent_task_queue SET session_id='writer-session' WHERE id=$1`, writerTask)
	in := handoffInput(writerTask, parseTestUUID(t, reviewerAgent))
	in.Candidates = []HandoffCandidate{}
	if mode == "resume" {
		prior := f.Task(t, reviewerAgent, testutil.Cols{
			"issue_id": issueID, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + reviewerAgent + "')"),
			"status": "completed", "completed_at": testutil.Raw("now()"), "session_id": "old-review-fix-session",
		})
		in.ContextMode, in.ResumeTaskID = "resume", prior
	}
	w, err := wakeups.CreateHandoff(ctx, issueID, parseTestUUID(t, f.UserID), writerTask, in)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, writerTask)
	wakeDispatch(t, wakeups, w)
	stored, err := f.q.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: w.ID, WorkspaceID: w.WorkspaceID})
	if err != nil || !stored.LastTaskID.Valid {
		t.Fatalf("review handoff did not create recipient: %+v, %v", stored, err)
	}
	reviewerTask := stored.LastTaskID
	f.Exec(t, `UPDATE agent_task_queue SET status='running',started_at=now(),session_id='reviewer-session' WHERE id=$1`, reviewerTask)
	bindWorkflowTestTask(t, f, issueID, reviewerTask)
	return f, WorkflowAuthorityService{Tasks: wakeups.Tasks}, issueID, reviewerTask, reviewerAgent
}

func TestWorkflowReviewRejectsResumedReviewFixOrigin(t *testing.T) {
	f, svc, issueID, taskID, reviewerAgent := workflowReviewOriginFixture(t, "resume")
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	err = svc.RegisterReview(ctx, issue.WorkspaceID, issueID, WorkflowActor{
		Type: "agent", ID: reviewerAgent, SourceTaskID: util.UUIDToString(taskID),
	}, WorkflowReviewInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), Verdict: "pass", PRReviewURLs: []string{}})
	if !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("resume-origin reviewer attestation: %v, want forbidden", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_review WHERE issue_id=$1`, issueID); got != 0 {
		t.Fatalf("resumed review/fix task registered %d final reviews", got)
	}
}

func TestWorkflowReviewAllowsRetryOfFreshFinalOrigin(t *testing.T) {
	f, svc, issueID, rootTaskID, reviewerAgent := workflowReviewOriginFixture(t, "fresh")
	ctx := context.Background()
	root, err := f.q.GetAgentTask(ctx, rootTaskID)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET status='failed',completed_at=now() WHERE id=$1`, rootTaskID)
	retry := f.Task(t, reviewerAgent, testutil.Cols{
		"issue_id": issueID, "runtime_id": root.RuntimeID, "status": "running", "started_at": testutil.Raw("now()"),
		"session_id": "reviewer-retry-session", "retry_of_task_id": rootTaskID, "context": root.Context,
		"force_fresh_session": true, "trigger_evidence_kind": root.TriggerEvidenceKind,
		"trigger_evidence_ref_id": root.TriggerEvidenceRefID,
		"workflow_policy_version": root.WorkflowPolicyVersion, "workflow_profile_id": root.WorkflowProfileID,
	})
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	err = svc.RegisterReview(ctx, issue.WorkspaceID, issueID, WorkflowActor{
		Type: "agent", ID: reviewerAgent, SourceTaskID: retry,
	}, WorkflowReviewInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), Verdict: "pass", PRReviewURLs: []string{}})
	if err != nil {
		t.Fatalf("fresh-origin retry rejected: %v", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_review WHERE issue_id=$1 AND reviewer_task_id=$2`, issueID, retry); got != 1 {
		t.Fatalf("fresh-origin retry registered %d reviews, want one", got)
	}
}

func TestWorkflowAcceptanceRechecksFreshReviewOrigin(t *testing.T) {
	f, svc, issueID, _ := workflowReviewedHumanCandidate(t)
	ctx := context.Background()
	issue, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	f.Exec(t, `UPDATE agent_task_queue SET context=jsonb_set(context,'{workflow_handoff,context_mode}','"resume"'::jsonb)
		WHERE id=(SELECT reviewer_task_id FROM issue_workflow_review WHERE issue_id=$1 LIMIT 1)`, issueID)
	_, err = svc.AcceptWorkflow(ctx, issue.WorkspaceID, issueID, WorkflowActor{Type: "member", ID: f.UserID},
		WorkflowAcceptanceInput{CandidateID: util.UUIDToString(issue.WorkflowCandidateID), ExpectedRevision: issue.Revision})
	if !errors.Is(err, ErrWorkflowAuthorityConflict) {
		t.Fatalf("acceptance trusted invalidated review origin: %v, want conflict", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, issueID); got != 0 {
		t.Fatalf("invalidated review produced %d acceptance rows", got)
	}
}

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
)

func TestHumanDoneWorkflowRejectionSupersedesTerminalDecision(t *testing.T) {
	f, svc, issueID, writerTask := workflowReviewedHumanCandidate(t)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT set_config('multica.actor_type','member',true),set_config('multica.actor_id',$1,true)`, f.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
		VALUES($1,$2,'member',$3,'workflow_human_status_decision',
		jsonb_build_object('from_status',$4::text,'to_status','done','from_revision',$5::bigint,
		'to_revision',$5::bigint+1,'candidate_id',$6::uuid::text,'transaction_id',pg_current_xact_id()::text))`,
		before.WorkspaceID, issueID, f.UserID, before.Status, before.Revision, before.WorkflowCandidateID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE issue SET status='done',revision=revision+1 WHERE id=$1`, issueID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	closed, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	in := WorkflowRejectionInput{CandidateID: util.UUIDToString(closed.WorkflowCandidateID), ExpectedRevision: closed.Revision,
		Kind: "scope_change", Reason: "Reopen this completed work for a new request.", ResumeTaskID: util.UUIDToString(writerTask)}
	if err := svc.RejectWorkflow(ctx, closed.WorkspaceID, issueID,
		WorkflowActor{Type: "member", ID: f.UserID}, in); !errors.Is(err, ErrWorkflowAuthorityForbidden) {
		t.Fatalf("machine credential with member identity rejected human Done: %v", err)
	}
	if got := f.Count(t, `SELECT count(*) FROM activity_log WHERE issue_id=$1 AND action='workflow_human_status_decision'`, issueID); got != 1 {
		t.Fatalf("failed rejection added %d receipts", got)
	}
	if err := svc.RejectWorkflow(ctx, closed.WorkspaceID, issueID,
		WorkflowActor{Type: "member", ID: f.UserID, HumanCredential: true}, in); err != nil {
		t.Fatalf("authenticated human rejection: %v", err)
	}
	reopened, err := f.q.GetIssue(ctx, issueID)
	if err != nil || reopened.Status != "in_progress" || reopened.WorkflowCandidateID.Valid {
		t.Fatalf("human rejection did not reopen candidate: %+v %v", reopened, err)
	}
	if got := f.Count(t, `SELECT count(*) FROM activity_log WHERE issue_id=$1 AND action='workflow_human_status_decision'`, issueID); got != 2 {
		t.Fatalf("human rejection receipts = %d, want Done and supersession", got)
	}
	var lastHumanDone bool
	if err := f.Pool.QueryRow(ctx, `SELECT workflow_human_last_done($1)`, issueID).Scan(&lastHumanDone); err != nil || lastHumanDone {
		t.Fatalf("old human Done still fences later candidate cycles: %t %v", lastHumanDone, err)
	}
}

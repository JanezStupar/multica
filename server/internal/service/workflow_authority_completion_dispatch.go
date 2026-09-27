package service

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TryReconcileWorkflowCompletion isolates ticket reconciliation from a verified
// provider merge. A failed completion rolls back only its savepoint; the caller
// can still commit the merge fact and its durable retry marker.
func TryReconcileWorkflowCompletion(ctx context.Context, tx pgx.Tx, q *db.Queries, issue db.Issue,
	acceptanceID pgtype.UUID,
) (*db.AgentTaskQueue, bool, error, error) {
	if _, err := tx.Exec(ctx, "SAVEPOINT workflow_outcome_dispatch"); err != nil {
		return nil, false, nil, err
	}
	task, done, dispatchErr := ReconcileWorkflowCompletion(ctx, tx, q, issue, acceptanceID)
	if dispatchErr == nil {
		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT workflow_outcome_dispatch"); err != nil {
			return nil, false, nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET
			last_error_class=NULL,outcome_next_attempt_at=NULL,outcome_dispatch_attempt_count=0
			WHERE id=$1 AND last_error_class IN ('outcome_dispatch_failed','completion_reconcile_failed')`, acceptanceID); err != nil {
			return nil, false, nil, err
		}
		return task, done, nil, nil
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT workflow_outcome_dispatch"); err != nil {
		return nil, false, dispatchErr, err
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT workflow_outcome_dispatch"); err != nil {
		return nil, false, dispatchErr, err
	}
	var attempts int
	if err := tx.QueryRow(ctx, `SELECT outcome_dispatch_attempt_count
		FROM issue_workflow_acceptance WHERE id=$1 FOR UPDATE`, acceptanceID).Scan(&attempts); err != nil {
		return nil, false, dispatchErr, err
	}
	attempts++
	shift := attempts - 1
	if shift > 6 {
		shift = 6
	}
	delay := time.Duration(5*(1<<shift)) * time.Second
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	class := "completion_reconcile_failed"
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET last_error_class=$2,
		outcome_dispatch_attempt_count=$3,outcome_next_attempt_at=$4
		WHERE id=$1 AND revoked_at IS NULL`, acceptanceID, class, attempts, time.Now().Add(delay)); err != nil {
		return nil, false, dispatchErr, err
	}
	return nil, false, dispatchErr, nil
}

// RetryNextWorkflowCompletionDispatch reconciles already-delivered acceptances,
// including tickets retained by the former outcome-acknowledgment gate. It never
// dispatches or retries agent work.
func (s WorkflowAuthorityService) RetryNextWorkflowCompletionDispatch(ctx context.Context) (bool, error) {
	if s.Tasks == nil || s.Tasks.TxStarter == nil {
		return false, ErrWorkflowAuthorityUnavailable
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return true, err
	}
	defer tx.Rollback(ctx)
	var issueID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT a.issue_id FROM issue_workflow_acceptance a
		JOIN issue i ON i.id=a.issue_id AND i.workspace_id=a.workspace_id
		JOIN issue_workflow_candidate c ON c.id=a.candidate_id AND c.issue_id=a.issue_id
		JOIN issue_status status ON status.workspace_id=i.workspace_id AND status.key=i.status
		WHERE a.completion_version=2 AND a.state='accepted' AND a.revoked_at IS NULL
		AND i.workflow_candidate_id=a.candidate_id AND status.category IN ('unstarted','started') AND NOT i.workflow_frozen
		AND (a.outcome_next_attempt_at IS NULL OR a.outcome_next_attempt_at<=now())
		AND (SELECT count(*) FROM issue_workflow_delivery d WHERE d.acceptance_id=a.id
		     AND d.status='delivered' AND d.merged_at IS NOT NULL)=jsonb_array_length(c.pr_set)
		ORDER BY a.outcome_next_attempt_at NULLS FIRST,a.id LIMIT 1`).Scan(&issueID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var workspaceID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT workspace_id FROM issue WHERE id=$1 FOR UPDATE SKIP LOCKED`, issueID).Scan(&workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	q := s.Tasks.Queries.WithTx(tx)
	issue, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issueID, WorkspaceID: workspaceID})
	if err != nil {
		return true, err
	}
	var acceptanceID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT a.id FROM issue_workflow_acceptance a
		JOIN issue_workflow_candidate c ON c.id=a.candidate_id AND c.issue_id=a.issue_id
		WHERE a.issue_id=$1 AND a.workspace_id=$2 AND a.candidate_id=$3 AND a.state='accepted'
		AND a.revoked_at IS NULL AND a.completion_version=2
		AND (a.outcome_next_attempt_at IS NULL OR a.outcome_next_attempt_at<=now())
		AND (SELECT count(*) FROM issue_workflow_delivery d WHERE d.acceptance_id=a.id
		     AND d.status='delivered' AND d.merged_at IS NOT NULL)=jsonb_array_length(c.pr_set)
		ORDER BY a.outcome_next_attempt_at NULLS FIRST,a.id LIMIT 1 FOR UPDATE OF a`,
		issue.ID, workspaceID, issue.WorkflowCandidateID).Scan(&acceptanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	task, _, dispatchErr, err := TryReconcileWorkflowCompletion(ctx, tx, q, issue, acceptanceID)
	if err != nil {
		return true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return true, err
	}
	if task != nil {
		s.Tasks.NotifyWorkflowCompletionTask(ctx, issue.WorkspaceID, task)
	}
	s.PublishWorkflowIssueChange(ctx, issue, WorkflowActor{Type: "system"})
	if dispatchErr != nil {
		return true, dispatchErr
	}
	return true, nil
}

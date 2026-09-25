package service

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TryReconcileWorkflowCompletion isolates task creation from a previously
// verified provider merge. A failed enqueue rolls back only its savepoint;
// the caller can still commit the exact merged-head delivery fact and this
// durable retry marker in the owning issue transaction.
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
	var outcomeComplete bool
	if err := tx.QueryRow(ctx, `SELECT outcome_dispatch_attempt_count,outcome_complete
		FROM issue_workflow_acceptance WHERE id=$1 FOR UPDATE`, acceptanceID).Scan(&attempts, &outcomeComplete); err != nil {
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
	class := "outcome_dispatch_failed"
	if outcomeComplete {
		class = "completion_reconcile_failed"
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET last_error_class=$2,
		outcome_dispatch_attempt_count=$3,outcome_next_attempt_at=$4
		WHERE id=$1 AND revoked_at IS NULL`, acceptanceID, class, attempts, time.Now().Add(delay)); err != nil {
		return nil, false, dispatchErr, err
	}
	return nil, false, dispatchErr, nil
}

// RetryNextWorkflowCompletionDispatch is the durable, bounded retry path for
// a merge that succeeded while outcome-task dispatch did not. Executed tasks
// that failed or were cancelled are never automatically retried.
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
		WHERE a.completion_version=2 AND a.state='accepted' AND a.revoked_at IS NULL
		AND (a.last_error_class='completion_reconcile_failed' OR
		     a.last_error_class='outcome_dispatch_failed' AND a.outcome_task_id IS NULL)
		AND a.outcome_next_attempt_at<=now() AND i.workflow_candidate_id=a.candidate_id
		AND i.status=a.accepted_status_key AND NOT i.workflow_frozen
		ORDER BY a.outcome_next_attempt_at,a.id LIMIT 1`).Scan(&issueID)
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
	err = tx.QueryRow(ctx, `SELECT id FROM issue_workflow_acceptance
		WHERE issue_id=$1 AND workspace_id=$2 AND candidate_id=$3 AND state='accepted'
		AND revoked_at IS NULL AND completion_version=2
		AND (last_error_class='completion_reconcile_failed' OR
		     last_error_class='outcome_dispatch_failed' AND outcome_task_id IS NULL)
		AND outcome_next_attempt_at<=now() AND accepted_status_key=$4
		ORDER BY outcome_next_attempt_at,id LIMIT 1 FOR UPDATE`, issue.ID, workspaceID,
		issue.WorkflowCandidateID, issue.Status).Scan(&acceptanceID)
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
		s.Tasks.NotifyTaskEnqueued(ctx, *task)
	}
	s.PublishWorkflowIssueChange(ctx, issue, WorkflowActor{Type: "system"})
	if dispatchErr != nil {
		return true, dispatchErr
	}
	return true, nil
}

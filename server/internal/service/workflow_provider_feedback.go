package service

import (
	"context"

	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func workflowHasProviderFeedback(ctx context.Context, tx pgx.Tx, issue db.Issue) (bool, error) {
	var pending bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vcs_workflow_input input
 LEFT JOIN agent_task_queue task ON task.id=input.task_id AND task.issue_id=input.issue_id
 WHERE input.issue_id=$1 AND input.workspace_id=$2
 AND (input.processed_at IS NULL OR task.status IN ('queued','deferred','dispatched','running','waiting_local_directory')))
 OR EXISTS(SELECT 1 FROM agent_task_queue continuation WHERE continuation.issue_id=$1
 AND continuation.status IN ('queued','deferred','dispatched','running','waiting_local_directory')
 AND workflow_provider_feedback_task_current(continuation.id,$1))`, issue.ID, issue.WorkspaceID).Scan(&pending)
	return pending, err
}

// workflowProviderFeedbackTaskMatches is backed by server-written provider input
// records. Client context fields alone cannot create continuation authority.
func workflowProviderFeedbackTaskMatches(ctx context.Context, tx pgx.Tx, issue db.Issue, task db.AgentTaskQueue) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT workflow_provider_feedback_task_current($1,$2)`, task.ID, issue.ID).Scan(&allowed)
	return allowed, err
}

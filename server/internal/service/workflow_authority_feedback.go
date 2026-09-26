package service

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// WorkflowHasPendingHumanFeedback identifies unclassified input on the exact
// current candidate's completed human handoff or recorded assigned-agent route.
// Callers hold the issue lock so comment routing cannot enqueue a new obligation
// between this check and a provider mutation. Ordinary notes and unrelated work
// do not acquire candidate authority merely by remaining active.
func WorkflowHasPendingHumanFeedback(ctx context.Context, tx pgx.Tx, issue db.Issue) (bool, error) {
	if !issue.WorkflowCandidateID.Valid {
		return false, nil
	}
	if issue.AssigneeType.String == "agent" {
		return WorkflowHasAcceptedCommentObligation(ctx, tx, issue)
	}
	if issue.AssigneeType.String != "member" {
		return false, nil
	}
	var pending bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM agent_task_queue task
		JOIN agent active ON active.id=task.agent_id AND active.workspace_id=$2
		WHERE task.issue_id=$1
		AND task.status IN ('queued','deferred','dispatched','running','waiting_local_directory')
		AND task.context->'workflow_feedback'->>'candidate_id'=$3::uuid::text
		AND active.archived_at IS NULL AND active.runtime_id=task.runtime_id
		AND workflow_human_comment_task_current(task.id,$1))`,
		issue.ID, issue.WorkspaceID, issue.WorkflowCandidateID).Scan(&pending)
	if err != nil || pending {
		return pending, err
	}
	// Posting and coordinator enqueue are separate transactions. The comment
	// write records an exact source obligation before releasing its issue lock,
	// so delivery also waits during that small commit-to-enqueue interval.
	var sourceTaskID, agentID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT coordinator.id,coordinator.agent_id FROM issue_wakeup handoff
		JOIN agent_task_queue coordinator ON coordinator.id=handoff.filter_task_id
		WHERE handoff.issue_id=$1 AND handoff.workspace_id=$2
		AND workflow_human_comment_handoff_current($1,handoff.id)`, issue.ID, issue.WorkspaceID).
		Scan(&sourceTaskID, &agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return WorkflowHasRecordedCommentObligation(ctx, tx, issue, sourceTaskID, agentID)
}

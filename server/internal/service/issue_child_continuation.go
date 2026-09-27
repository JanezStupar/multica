package service

import (
	"context"
	"fmt"
	db "github.com/multica-ai/multica/server/pkg/db/generated"

	"github.com/jackc/pgx/v5/pgtype"
)

// ReconcileChildCompletions repairs a specifically selected parent's missing
// inputs. It deliberately does not sweep historical children on every tick.
// Current assignment, selected workflow and freeze/terminal boundaries are
// checked by the same transactional capture used for new child transitions.
func (s *IssueWakeupService) ReconcileChildCompletions(ctx context.Context, parentID, workspaceID pgtype.UUID) error {
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	parent, err := q.LockWakeupIssue(ctx, parentID)
	if err != nil {
		return err
	}
	if parent.WorkspaceID != workspaceID {
		return fmt.Errorf("parent is outside workspace")
	}
	rows, err := q.ListChildIssues(ctx, parent.ID)
	if err != nil {
		return err
	}
	for _, child := range rows {
		if _, err = tx.Exec(ctx, "SELECT capture_parent_child_completion($1)", child.ID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// UsesChildContinuation distinguishes selected workflow continuation from the
// native stage-barrier notification, preventing duplicate parent runs.
func (s *IssueWakeupService) UsesChildContinuation(issuePolicy []byte) bool {
	pinned, err := s.Tasks.DecodeIssueWorkflowPolicy(issuePolicy)
	if err != nil || pinned == nil {
		return false
	}
	policy, err := ParseWorkflowAuthorityPolicy(pinned.Bundle)
	return err == nil && policy.FormatVersion == 2
}

// childCompletionContinuation identifies server-recorded child input delivery,
// never a caller-supplied context flag alone. Task provenance survives the
// operational receipt retention window, including long-running writers.
func childCompletionContinuation(ctx context.Context, querier db.DBTX, issue db.Issue, task db.AgentTaskQueue) (bool, error) {
	if issue.AssigneeType.String != "agent" || issue.AssigneeID != task.AgentID {
		return false, nil
	}
	var allowed bool
	err := querier.QueryRow(ctx, `SELECT EXISTS (
  SELECT 1 FROM issue_wakeup w
  JOIN agent_task_queue delivered ON delivered.id=$4
   AND delivered.trigger_evidence_kind='issue_wakeup' AND delivered.trigger_evidence_ref_id=w.id
  JOIN agent_task_queue source ON source.id=$6 AND source.issue_id=w.issue_id AND source.agent_id=w.agent_id
  WHERE w.child_issue_id IS NOT NULL AND w.issue_id=$1 AND w.workspace_id=$2
   AND w.agent_id=$3 AND delivered.issue_id=w.issue_id AND delivered.agent_id=w.agent_id
   AND source.originator_user_id IS NOT NULL AND delivered.originator_user_id=source.originator_user_id
   AND delivered.accountable_user_id=source.originator_user_id
   AND w.id::text=$5::jsonb->>'wakeup_id'
   )`, issue.ID, issue.WorkspaceID, task.AgentID, task.ID, task.Context, task.DelegatedFromTaskID).Scan(&allowed)
	return allowed, err
}

package service

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RecordRequestedAcceptanceComment records a permission-checked member route
// before its comment transaction releases the issue lock. It changes only the
// source task's undelivered plan, never its claim receipt or human attribution.
// A completion/finalizer race therefore cannot erase the promised follow-up.
func RecordRequestedAcceptanceComment(ctx context.Context, tx pgx.Tx, issue db.Issue, agentID, commentID pgtype.UUID) (bool, error) {
	result, err := tx.Exec(ctx, `UPDATE agent_task_queue source
		SET coalesced_comment_ids=(SELECT COALESCE(array_agg(DISTINCT id),'{}'::uuid[])
			FROM unnest(array_append(source.coalesced_comment_ids,$3::uuid)) id WHERE id IS NOT NULL)
		FROM issue i,issue_workflow_acceptance a,issue_workflow_candidate candidate,comment c,member m
		WHERE i.id=$1 AND i.workspace_id=$4 AND i.assignee_type='agent' AND i.assignee_id=$2
		AND a.issue_id=i.id AND a.workspace_id=i.workspace_id AND a.candidate_id=i.workflow_candidate_id
		AND a.state='requested' AND a.revoked_at IS NULL AND a.actor_type='agent' AND a.actor_id=$2
		AND a.candidate_id=$5
		AND a.policy_version=i.workflow_policy->>'version'
		AND source.id=a.source_task_id AND source.issue_id=i.id AND source.agent_id=$2
		AND source.status IN ('dispatched','running','waiting_local_directory','completed')
		AND candidate.id=a.candidate_id AND candidate.issue_id=i.id AND candidate.workspace_id=i.workspace_id
		AND c.id=$3 AND c.issue_id=i.id AND c.workspace_id=i.workspace_id
		AND c.author_type='member' AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
		AND c.created_at>candidate.created_at AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
		AND m.workspace_id=i.workspace_id AND m.user_id=c.author_id`,
		issue.ID, agentID, commentID, issue.WorkspaceID, issue.WorkflowCandidateID)
	return err == nil && result.RowsAffected() > 0, err
}

// RecordWorkflowHumanComment pins a new member-handoff conversation before its
// saved comment becomes visible to the delivery worker. The completed source
// retains an undelivered plan until a follow-up's completed receipt covers it.
func RecordWorkflowHumanComment(ctx context.Context, tx pgx.Tx, issue db.Issue, agentID, handoffID, coordinatorTaskID, candidateID, commentID pgtype.UUID) (bool, error) {
	result, err := tx.Exec(ctx, `UPDATE agent_task_queue coordinator
		SET coalesced_comment_ids=(SELECT COALESCE(array_agg(DISTINCT id),'{}'::uuid[])
			FROM unnest(array_append(coordinator.coalesced_comment_ids,$6::uuid)) id WHERE id IS NOT NULL)
		FROM issue i,issue_wakeup w,comment c,member m
		WHERE i.id=$1 AND i.workspace_id=$7 AND i.workflow_candidate_id=$5
		AND w.id=$3 AND w.filter_task_id=$4 AND workflow_human_comment_handoff_eligible(i.id,w.id)
		AND coordinator.id=$4 AND coordinator.issue_id=i.id AND coordinator.agent_id=$2
		AND c.id=$6 AND c.issue_id=i.id AND c.workspace_id=i.workspace_id
		AND c.author_type='member' AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
		AND c.created_at>(SELECT created_at FROM issue_workflow_candidate WHERE id=$5)
		AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
		AND m.workspace_id=i.workspace_id AND m.user_id=c.author_id
		AND (c.author_id=i.assignee_id OR m.role IN ('owner','admin'))`,
		issue.ID, agentID, handoffID, coordinatorTaskID, candidateID, commentID, issue.WorkspaceID)
	return err == nil && result.RowsAffected() > 0, err
}

// RecordAcceptedAssignedComment pins only the permission-checked assigned agent
// while the exact accepted candidate still has delivery or outcome work. The
// ledger survives external completion solely to deliver this promised input.
func RecordAcceptedAssignedComment(ctx context.Context, tx pgx.Tx, issue db.Issue, agentID, commentID pgtype.UUID) (bool, error) {
	result, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance a
		SET human_comment_obligations=(SELECT jsonb_agg(DISTINCT entry) FROM jsonb_array_elements(
			a.human_comment_obligations || jsonb_build_array(jsonb_build_object('comment_id',$3::uuid::text,'agent_id',$2::uuid::text))) entry)
		FROM issue i,issue_workflow_candidate candidate,comment c,member m
		WHERE i.id=$1 AND i.workspace_id=$4 AND NOT i.workflow_frozen AND i.assignee_type='agent' AND i.assignee_id=$2
		AND a.issue_id=i.id AND a.workspace_id=i.workspace_id AND a.candidate_id=i.workflow_candidate_id
		AND a.state='accepted' AND a.revoked_at IS NULL AND a.candidate_id=$5 AND a.completion_version=2 AND a.accepted_status_key=i.status
		AND a.policy_version=i.workflow_policy->>'version'
		AND candidate.id=a.candidate_id AND candidate.issue_id=i.id AND candidate.workspace_id=i.workspace_id
		AND candidate.policy_version=a.policy_version AND a.authority_snapshot->>'scope_digest'=candidate.scope_digest
		AND EXISTS(SELECT 1 FROM issue_status status WHERE status.workspace_id=i.workspace_id
		  AND status.key=i.status AND status.category IN ('unstarted','started'))
		AND (NOT a.outcome_complete OR EXISTS(SELECT 1 FROM issue_workflow_delivery d
		  WHERE d.acceptance_id=a.id AND (d.status<>'delivered' OR d.merged_at IS NULL)))
		AND c.id=$3 AND c.issue_id=i.id AND c.workspace_id=i.workspace_id AND c.author_type='member'
		AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL AND c.created_at>candidate.created_at
		AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
		AND m.workspace_id=i.workspace_id AND m.user_id=c.author_id`, issue.ID, agentID, commentID, issue.WorkspaceID, issue.WorkflowCandidateID)
	return err == nil && result.RowsAffected() > 0, err
}

// Only a recorded live human input can defer approval: pending conversation
// plans, or exact inputs promised on the acceptance source but not yet covered
// by a completed follow-up's delivery receipt. Notes, deleted inputs and agent
// messages are not obligations. A delivered question still holds authority
// until its conversation task completes, allowing classification first.
func WorkflowHasCommentObligation(ctx context.Context, tx pgx.Tx, issue db.Issue, sourceTaskID, agentID pgtype.UUID) (bool, error) {
	return workflowHasCommentObligation(ctx, tx, issue, sourceTaskID, agentID, true)
}

// WorkflowHasRecordedCommentObligation limits the check to the exact input plan
// recorded atomically on a conversation's completed coordinator/source task.
func WorkflowHasRecordedCommentObligation(ctx context.Context, tx pgx.Tx, issue db.Issue, sourceTaskID, agentID pgtype.UUID) (bool, error) {
	return workflowHasCommentObligation(ctx, tx, issue, sourceTaskID, agentID, false)
}

func workflowHasCommentObligation(ctx context.Context, tx pgx.Tx, issue db.Issue, sourceTaskID, agentID pgtype.UUID, includePendingConversation bool) (bool, error) {
	var pending bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM comment c
		JOIN member m ON m.workspace_id=c.workspace_id AND m.user_id=c.author_id
		JOIN issue_workflow_candidate candidate ON candidate.id=$3 AND candidate.issue_id=$1 AND candidate.workspace_id=$2
		WHERE c.issue_id=$1 AND c.workspace_id=$2 AND c.author_type='member'
		AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
		AND c.created_at>candidate.created_at AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
		AND (
		  ($6 AND EXISTS(SELECT 1 FROM agent_task_queue conversation WHERE conversation.issue_id=$1 AND conversation.agent_id=$5
		    AND conversation.status IN ('queued','deferred','dispatched','running','waiting_local_directory')
		    AND (conversation.trigger_comment_id=c.id OR c.id=ANY(conversation.coalesced_comment_ids))))
		  OR (EXISTS(SELECT 1 FROM agent_task_queue source WHERE source.id=$4 AND source.issue_id=$1 AND source.agent_id=$5
		    AND c.id=ANY(source.coalesced_comment_ids) AND NOT (c.id=ANY(source.delivered_comment_ids) AND source.dispatched_at>=c.updated_at))
		    AND NOT EXISTS(SELECT 1 FROM agent_task_queue covered WHERE covered.issue_id=$1 AND covered.agent_id=$5
		      AND covered.id<>$4 AND covered.status='completed' AND c.id=ANY(covered.delivered_comment_ids)
		      AND covered.dispatched_at>=c.updated_at))
		))`, issue.ID, issue.WorkspaceID, issue.WorkflowCandidateID, sourceTaskID, agentID, includePendingConversation).Scan(&pending)
	return pending, err
}

// WorkflowHasAcceptedCommentObligation reads only the exact assigned-agent
// recipient proofs recorded by the comment transaction on current acceptance.
func WorkflowHasAcceptedCommentObligation(ctx context.Context, tx pgx.Tx, issue db.Issue) (bool, error) {
	var pending bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_workflow_acceptance a
		CROSS JOIN LATERAL jsonb_array_elements(a.human_comment_obligations) obligation
		JOIN comment c ON c.id::text=obligation->>'comment_id' AND c.issue_id=$1 AND c.workspace_id=$2
		WHERE a.issue_id=$1 AND a.workspace_id=$2 AND a.candidate_id=$3
		AND obligation->>'agent_id'=$4::uuid::text
		AND workflow_accepted_comment_input_recorded($1,$4,c.id)
		AND NOT EXISTS(SELECT 1 FROM agent_task_queue covered WHERE covered.issue_id=$1 AND covered.agent_id=$4
		  AND covered.status='completed' AND c.id=ANY(covered.delivered_comment_ids)
		  AND covered.dispatched_at>=c.updated_at))`, issue.ID, issue.WorkspaceID, issue.WorkflowCandidateID, issue.AssigneeID).Scan(&pending)
	return pending, err
}

// RetireUnpromisedCommentObligation removes only the attempted recipient whose
// dispatch returned blocked and acquired no pending conversation plan. A queued
// promise that later fails or is cancelled remains an obligation to the human.
func (s *TaskService) RetireUnpromisedCommentObligation(ctx context.Context, issue db.Issue, agentID, commentID pgtype.UUID) error {
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := lockWorkflowAuthorityIssue(ctx, tx, s.Queries.WithTx(tx), issue.WorkspaceID, issue.ID); err != nil {
		return err
	}
	var planned bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_queue t WHERE t.issue_id=$1 AND t.agent_id=$2
		AND t.status IN ('queued','deferred','dispatched','running','waiting_local_directory')
		AND t.trigger_comment_id IS NOT NULL AND (t.trigger_comment_id=$3 OR $3::uuid=ANY(t.coalesced_comment_ids))
		AND NOT EXISTS(SELECT 1 FROM issue_workflow_acceptance a WHERE a.issue_id=$1 AND a.source_task_id=t.id
		  AND a.state='requested' AND NOT EXISTS(SELECT 1 FROM comment input WHERE input.id=$3
		  AND $3::uuid=ANY(t.delivered_comment_ids) AND t.dispatched_at>=input.updated_at))))`,
		issue.ID, agentID, commentID).Scan(&planned); err != nil {
		return err
	}
	if planned {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET human_comment_obligations=
		(SELECT COALESCE(jsonb_agg(obligation),'[]'::jsonb) FROM jsonb_array_elements(human_comment_obligations) obligation
		 WHERE NOT (obligation->>'comment_id'=$2::uuid::text AND obligation->>'agent_id'=$3::uuid::text))
		WHERE issue_id=$1 AND workspace_id=$4`, issue.ID, commentID, agentID, issue.WorkspaceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_task_queue source SET coalesced_comment_ids=array_remove(coalesced_comment_ids,$3)
		WHERE source.issue_id=$1 AND source.agent_id=$2 AND NOT EXISTS(SELECT 1 FROM comment input WHERE input.id=$3
		 AND $3::uuid=ANY(source.delivered_comment_ids) AND source.dispatched_at>=input.updated_at)
		AND (EXISTS(SELECT 1 FROM issue_workflow_acceptance a WHERE a.issue_id=$1 AND a.source_task_id=source.id AND a.state='requested')
		 OR EXISTS(SELECT 1 FROM issue_wakeup handoff WHERE handoff.issue_id=$1 AND handoff.filter_task_id=source.id
		   AND workflow_human_comment_handoff_current($1,handoff.id)))`, issue.ID, agentID, commentID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReconcileWorkflowCommentRecipients retires only recipient proofs withdrawn by
// this explicit edit. Completed claim receipts remain history; other comments
// and their private human attribution are untouched. The caller holds the issue
// lock and has resolved the edited body against current invocation authority.
func ReconcileWorkflowCommentRecipients(ctx context.Context, tx pgx.Tx, issue db.Issue, commentID pgtype.UUID, recipients []pgtype.UUID) error {
	if recipients == nil {
		recipients = []pgtype.UUID{}
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET human_comment_obligations=
		(SELECT COALESCE(jsonb_agg(obligation),'[]'::jsonb) FROM jsonb_array_elements(human_comment_obligations) obligation
		 WHERE obligation->>'comment_id'<>$2::uuid::text
		 OR obligation->>'agent_id'=ANY(SELECT recipient::text FROM unnest($3::uuid[]) recipient))
		WHERE issue_id=$1 AND workspace_id=$4`, issue.ID, commentID, recipients, issue.WorkspaceID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE agent_task_queue source SET coalesced_comment_ids=array_remove(source.coalesced_comment_ids,$2)
		WHERE source.issue_id=$1 AND $2::uuid=ANY(source.coalesced_comment_ids)
		AND NOT source.agent_id=ANY($3::uuid[])
		AND (EXISTS(SELECT 1 FROM issue_workflow_acceptance a WHERE a.issue_id=$1 AND a.workspace_id=$4
		  AND a.source_task_id=source.id)
		 OR EXISTS(SELECT 1 FROM issue_wakeup handoff WHERE handoff.issue_id=$1 AND handoff.workspace_id=$4
		   AND handoff.filter_task_id=source.id AND handoff.handoff IS NOT NULL))`, issue.ID, commentID, recipients, issue.WorkspaceID)
	return err
}

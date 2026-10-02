package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ReplaceUnclaimedWorkflowCommentRuntime replaces an exact promised reply that
// never acquired a claim on the agent's former runtime. Retirement and the new
// checked enqueue commit together. Claimed tasks retain their original lease.
func (s *TaskService) ReplaceUnclaimedWorkflowCommentRuntime(ctx context.Context, issue db.Issue,
	agentID, candidateID, commentID, handoffID, sourceID pgtype.UUID,
) (bool, error) {
	if s == nil || s.TxStarter == nil {
		return false, ErrAttributionFailClosed
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: issue.WorkspaceID})
	if err != nil || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
		return false, ErrAttributionFailClosed
	}
	var owners bool
	if err := tx.QueryRow(ctx, `SELECT lock_task_owner_rows($1,$2,$3)`, agentID, issue.ID, agent.RuntimeID).Scan(&owners); err != nil {
		return false, err
	}
	if !owners {
		return false, ErrAttributionFailClosed
	}
	// A default-runtime update changes a non-key agent column. Hold its update
	// fence as well as the ownership fence before re-reading current routing.
	if _, err := tx.Exec(ctx, `SELECT id FROM agent WHERE id=$1 AND workspace_id=$2 FOR NO KEY UPDATE`, agentID, issue.WorkspaceID); err != nil {
		return false, err
	}
	agent, err = q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: issue.WorkspaceID})
	if err != nil || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
		return false, ErrAttributionFailClosed
	}
	if err := tx.QueryRow(ctx, `SELECT lock_task_owner_rows($1,$2,$3)`, agentID, issue.ID, agent.RuntimeID).Scan(&owners); err != nil {
		return false, err
	}
	if !owners {
		return false, ErrAttributionFailClosed
	}
	current, err := lockWorkflowAuthorityIssue(ctx, tx, q, issue.WorkspaceID, issue.ID)
	if err != nil || current.WorkflowFrozen || current.WorkflowCandidateID != candidateID {
		return false, ErrAttributionFailClosed
	}
	var inputCurrent bool
	if err := tx.QueryRow(ctx, `SELECT workflow_recorded_comment_input_current($1,$2,$3)`, issue.ID, agentID, commentID).Scan(&inputCurrent); err != nil {
		return false, err
	}
	if !inputCurrent {
		return false, ErrAttributionFailClosed
	}
	comment, err := q.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{ID: commentID, WorkspaceID: issue.WorkspaceID})
	if err != nil || comment.IssueID != issue.ID || comment.AuthorType != "member" {
		return false, ErrAttributionFailClosed
	}
	if err := (&IssueWakeupService{Tasks: s}).authorize(ctx, q, issue.WorkspaceID, comment.AuthorID, agent); err != nil {
		return false, err
	}
	policy, err := s.DecodeIssueWorkflowPolicy(current.WorkflowPolicy)
	if err != nil || policy == nil {
		return false, ErrAttributionFailClosed
	}
	var candidatePolicy string
	if err := tx.QueryRow(ctx, `SELECT policy_version FROM issue_workflow_candidate
	 WHERE id=$1 AND issue_id=$2 AND workspace_id=$3`, candidateID, issue.ID, issue.WorkspaceID).Scan(&candidatePolicy); err != nil {
		return false, err
	}
	if candidatePolicy != policy.Version {
		return false, ErrAttributionFailClosed
	}
	var staleID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT task.id FROM agent_task_queue task
	 WHERE task.issue_id=$1 AND task.agent_id=$2 AND task.status IN ('queued','deferred')
	 AND task.runtime_id IS DISTINCT FROM $3 AND task.dispatched_at IS NULL AND task.started_at IS NULL
	 AND (task.trigger_comment_id=$4 OR $4::uuid=ANY(task.coalesced_comment_ids))
	 AND task.originator_user_id=$5 AND task.accountable_user_id=$5
	 AND task.context->'workflow_comment_obligation'->>'candidate_id'=$6::uuid::text
	 AND (workflow_human_comment_task_current(task.id,$1) OR workflow_accepted_comment_task_current(task.id,$1)
	  OR workflow_requested_comment_task_current(task.id,$1))
	 ORDER BY task.created_at,task.id LIMIT 1 FOR UPDATE`, issue.ID, agentID, agent.RuntimeID,
		commentID, comment.AuthorID, candidateID).Scan(&staleID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	stale, err := q.GetAgentTask(ctx, staleID)
	if err != nil {
		return false, err
	}
	var covered bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_queue task
	 WHERE task.issue_id=$1 AND task.agent_id=$2 AND task.id<>$3
	 AND task.status IN ('queued','deferred','dispatched','running','waiting_local_directory')
	 AND (task.trigger_comment_id=$4 OR $4::uuid=ANY(task.coalesced_comment_ids)))`,
		issue.ID, agentID, stale.ID, commentID).Scan(&covered); err != nil {
		return false, err
	}
	if covered {
		return false, nil
	}
	profileID, profileVersion := stale.WorkflowProfileID, stale.WorkflowPolicyVersion
	lineageID := stale.RetryOfTaskID
	if !lineageID.Valid {
		lineageID = stale.RerunOfTaskID
	}
	if !profileID.Valid && lineageID.Valid {
		if err := tx.QueryRow(ctx, `SELECT workflow_profile_id,workflow_policy_version FROM agent_task_queue
		 WHERE id=$1 AND issue_id=$2 AND agent_id=$3`, lineageID, issue.ID, agentID).Scan(&profileID, &profileVersion); err != nil {
			return false, err
		}
		if !profileID.Valid || !profileVersion.Valid {
			return false, ErrAttributionFailClosed
		}
	}
	if profileVersion.Valid && profileVersion.String != policy.Version {
		return false, ErrAttributionFailClosed
	}
	if profileID.Valid {
		profile, err := q.GetIssueWorkflowProfileByID(ctx, db.GetIssueWorkflowProfileByIDParams{
			ID: profileID, WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, AgentID: agentID})
		if err != nil || profile.PolicyVersion != policy.Version {
			return false, ErrAttributionFailClosed
		}
		frozen, err := DecodeIssueWorkflowProfile(profile.Snapshot, profile.Digest,
			util.UUIDToString(issue.ID), util.UUIDToString(agentID), policy.Version)
		runtime, runtimeErr := q.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
			ID: agent.RuntimeID, WorkspaceID: issue.WorkspaceID})
		if err != nil || runtimeErr != nil || frozen.ExpectedProvider != runtime.Provider ||
			frozen.CustomArgsDigest != IssueWorkflowCustomArgsDigest(agent.CustomArgs) {
			return false, ErrAttributionFailClosed
		}
		profileVersion = pgtype.Text{String: profile.PolicyVersion, Valid: true}
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_task_queue SET status='cancelled',completed_at=now(),
	 error='Promised reply runtime changed before claim',cancelled_by_type='system',
	 retained_context_invalidated=true,prepare_lease_expires_at=NULL WHERE id=$1`, stale.ID); err != nil {
		return false, err
	}
	// These are the ordinary enqueue APIs with transaction-bound queries and
	// notifications withheld until the encompassing replacement commits. pgx
	// begins a savepoint for the member route's own transaction.
	transactional := &TaskService{Queries: q, TxStarter: tx, Bus: events.New(), Composio: s.Composio, FeatureFlags: s.FeatureFlags}
	var replacement db.AgentTaskQueue
	var coalesced bool
	if handoffID.Valid {
		replacement, coalesced, err = transactional.EnqueueWorkflowHumanComment(ctx, current, agentID, handoffID, sourceID, candidateID, commentID)
	} else {
		if current.AssigneeType.String != "agent" || current.AssigneeID != agentID {
			return false, ErrAttributionFailClosed
		}
		replacement, err = transactional.enqueueIssueTaskWithCommentPlan(ctx, current, commentID, nil,
			true, "", pgtype.UUID{}, pgtype.UUID{}, pgtype.Timestamptz{}, OriginDerived)
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_task_queue SET context=COALESCE(context,'{}'::jsonb)
	 ||jsonb_build_object('workflow_comment_runtime_retired',jsonb_build_object(
	  'candidate_id',$2::uuid::text,'replacement_task_id',$3::uuid::text,'runtime_id',$4::uuid::text)) WHERE id=$1`,
		stale.ID, candidateID, replacement.ID, agent.RuntimeID); err != nil {
		return false, err
	}
	// A runtime replacement starts a fresh provider context while retaining any
	// profile already pinned on the unclaimed plan. It is not a manual rerun.
	if !coalesced {
		if _, err := tx.Exec(ctx, `UPDATE agent_task_queue SET force_fresh_session=true,
		 rerun_of_task_id=NULL,retry_of_task_id=NULL,comment_resume_from_task_id=NULL,wakeup_resume_from_task_id=NULL,
		 workflow_profile_id=$2,workflow_policy_version=$3 WHERE id=$1`, replacement.ID,
			profileID, profileVersion); err != nil {
			return false, err
		}
	}
	details, err := json.Marshal(map[string]string{"candidate_id": util.UUIDToString(candidateID),
		"comment_id": util.UUIDToString(commentID), "agent_id": util.UUIDToString(agentID),
		"retired_task_id": util.UUIDToString(stale.ID), "replacement_task_id": util.UUIDToString(replacement.ID),
		"old_runtime_id": util.UUIDToString(stale.RuntimeID), "runtime_id": util.UUIDToString(agent.RuntimeID)})
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,action,details)
	 VALUES($1,$2,'system','workflow_comment_runtime_recovered',$3)`, issue.WorkspaceID, issue.ID, details); err != nil {
		return false, err
	}
	replacement, err = q.GetAgentTask(ctx, replacement.ID)
	if err != nil {
		return false, err
	}
	stale, err = q.GetAgentTask(ctx, stale.ID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	s.broadcastTaskEvent(ctx, protocol.EventTaskCancelled, stale)
	if !coalesced {
		s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, replacement)
		s.NotifyTaskEnqueued(ctx, replacement)
	}
	return true, nil
}

package handler

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// RecoverNextRecordedWorkflowComment materializes one conversation promised by
// the atomic comment write but missing its separate enqueue. Existing plans,
// including failed or cancelled conversations, belong to explicit task retry.
// This scan supplies no new recipient or acceptance authority.
func (w *WorkflowDeliveryWorker) RecoverNextRecordedWorkflowComment(ctx context.Context) (bool, error) {
	if w == nil || w.h == nil || w.h.DB == nil || w.h.TaskService == nil {
		return false, nil
	}
	var issueID, workspaceID, candidateID, agentID, commentID, sourceID, handoffID pgtype.UUID
	err := w.h.DB.QueryRow(ctx, `WITH recorded AS (
	 SELECT a.issue_id,a.workspace_id,a.candidate_id,agent.id agent_id,c.id comment_id,
	  NULL::uuid source_id,NULL::uuid handoff_id
	 FROM issue_workflow_acceptance a
	 CROSS JOIN LATERAL jsonb_array_elements(a.human_comment_obligations) obligation
	 JOIN agent ON agent.id::text=obligation->>'agent_id' AND agent.workspace_id=a.workspace_id
	 JOIN comment c ON c.id::text=obligation->>'comment_id' AND c.issue_id=a.issue_id AND c.workspace_id=a.workspace_id
	 WHERE a.state='accepted' AND a.revoked_at IS NULL
	 UNION ALL
	 SELECT handoff.issue_id,handoff.workspace_id,i.workflow_candidate_id,source.agent_id,c.id,source.id,handoff.id
	 FROM issue_wakeup handoff JOIN issue i ON i.id=handoff.issue_id AND i.workspace_id=handoff.workspace_id
	 JOIN agent_task_queue source ON source.id=handoff.filter_task_id AND source.issue_id=i.id
	 JOIN comment c ON c.id=ANY(source.coalesced_comment_ids) AND c.issue_id=i.id AND c.workspace_id=i.workspace_id
	 WHERE workflow_human_comment_handoff_current(i.id,handoff.id)
	 UNION ALL
	 SELECT a.issue_id,a.workspace_id,a.candidate_id,source.agent_id,c.id,source.id,NULL::uuid
	 FROM issue_workflow_acceptance a JOIN agent_task_queue source ON source.id=a.source_task_id AND source.issue_id=a.issue_id
	 JOIN comment c ON c.id=ANY(source.coalesced_comment_ids) AND c.issue_id=a.issue_id AND c.workspace_id=a.workspace_id
	 WHERE a.state='requested' AND a.revoked_at IS NULL AND source.status='completed'
	 AND a.actor_type='agent' AND a.actor_id=source.agent_id
	)
	SELECT r.issue_id,r.workspace_id,r.candidate_id,r.agent_id,r.comment_id,r.source_id,r.handoff_id
FROM recorded r JOIN issue i ON i.id=r.issue_id AND i.workspace_id=r.workspace_id AND i.workflow_candidate_id=r.candidate_id
JOIN agent recipient ON recipient.id=r.agent_id AND recipient.workspace_id=i.workspace_id
	JOIN issue_workflow_candidate candidate ON candidate.id=r.candidate_id AND candidate.issue_id=i.id AND candidate.workspace_id=i.workspace_id
	JOIN comment c ON c.id=r.comment_id AND c.issue_id=i.id AND c.workspace_id=i.workspace_id
	JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=c.author_id
	WHERE NOT i.workflow_frozen AND candidate.policy_version=i.workflow_policy->>'version'
	AND EXISTS(SELECT 1 FROM issue_status status WHERE status.workspace_id=i.workspace_id AND status.key=i.status
	 AND status.category IN ('unstarted','started','done'))
	AND c.author_type='member' AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
	AND c.created_at>candidate.created_at AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
	AND NOT EXISTS(SELECT 1 FROM agent_task_queue source WHERE source.id=r.source_id
	 AND c.id=ANY(source.delivered_comment_ids) AND source.dispatched_at>=c.updated_at)
	AND NOT EXISTS(SELECT 1 FROM agent_task_queue planned WHERE planned.issue_id=i.id AND planned.agent_id=r.agent_id
	 AND planned.id IS DISTINCT FROM r.source_id
	 AND (planned.trigger_comment_id=c.id OR c.id=ANY(planned.coalesced_comment_ids))
 AND ((planned.status IN ('queued','deferred','dispatched','running','waiting_local_directory')
   AND NOT ((planned.status IN ('queued','deferred') AND planned.runtime_id IS DISTINCT FROM recipient.runtime_id
    AND planned.dispatched_at IS NULL AND planned.started_at IS NULL
    AND planned.originator_user_id=c.author_id AND planned.accountable_user_id=c.author_id
    AND planned.context->'workflow_comment_obligation'->>'candidate_id'=r.candidate_id::text
    AND (workflow_human_comment_task_current(planned.id,i.id) OR workflow_accepted_comment_task_current(planned.id,i.id)
     OR workflow_requested_comment_task_current(planned.id,i.id))) IS TRUE))
  OR (planned.status IN ('failed','cancelled') AND (planned.completed_at IS NULL OR planned.completed_at>=c.updated_at)
   AND NOT EXISTS(SELECT 1 FROM agent_task_queue continuation WHERE continuation.issue_id=i.id AND continuation.agent_id=r.agent_id
    AND (continuation.retry_of_task_id=planned.id OR continuation.rerun_of_task_id=planned.id)
    AND continuation.status IN ('queued','deferred','dispatched','running','waiting_local_directory')
    AND (continuation.trigger_comment_id=c.id OR c.id=ANY(continuation.coalesced_comment_ids))
    AND continuation.context->'workflow_comment_obligation'->>'candidate_id'=r.candidate_id::text
    AND (workflow_human_comment_task_current(continuation.id,i.id) OR workflow_accepted_comment_task_current(continuation.id,i.id)
     OR workflow_requested_comment_task_current(continuation.id,i.id)))
   AND NOT ((planned.status='cancelled' AND planned.cancelled_by_type='system'
    AND planned.context->'workflow_comment_runtime_retired'->>'candidate_id'=r.candidate_id::text) IS TRUE))))
	AND NOT EXISTS(SELECT 1 FROM agent_task_queue covered WHERE covered.issue_id=i.id AND covered.agent_id=r.agent_id
	 AND covered.status='completed' AND c.id=ANY(covered.delivered_comment_ids) AND covered.dispatched_at>=c.updated_at)
	AND NOT EXISTS(SELECT 1 FROM activity_log retry WHERE retry.workspace_id=i.workspace_id AND retry.issue_id=i.id
	 AND retry.action IN ('workflow_comment_recovery_blocked','workflow_comment_recovery_deferred') AND retry.created_at>now()-interval '30 seconds'
	 AND retry.details->>'comment_id'=c.id::text AND retry.details->>'agent_id'=r.agent_id::text
	 AND retry.details->>'candidate_id'=r.candidate_id::text AND retry.details->>'comment_revision'=c.revision::text)
	ORDER BY c.created_at,c.id,r.agent_id LIMIT 1`).Scan(
		&issueID, &workspaceID, &candidateID, &agentID, &commentID, &sourceID, &handoffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	issue, err := w.h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issueID, WorkspaceID: workspaceID})
	if err != nil {
		return true, err
	}
	comment, err := w.h.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{ID: commentID, WorkspaceID: workspaceID})
	if err != nil {
		return true, err
	}
	if issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID || comment.DeletedAt.Valid || isNoteComment(comment.Content) {
		return false, nil
	}
	pinned, err := w.h.TaskService.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || pinned == nil {
		return false, err
	}
	recordAttempt := func(action string, reason DispatchReasonCode, publish bool) (bool, error) {
		details, err := json.Marshal(map[string]any{"candidate_id": candidateID, "comment_id": commentID,
			"agent_id": agentID, "comment_revision": comment.Revision, "reason": reason})
		if err != nil {
			return true, err
		}
		result, err := w.h.DB.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,action,details)
	 SELECT $1,$2,'system',$7,$3 FROM issue i JOIN comment c ON c.issue_id=i.id
	 WHERE i.id=$2 AND i.workspace_id=$1 AND i.workflow_candidate_id=$4 AND c.id=$5 AND c.revision=$6`,
			workspaceID, issueID, details, candidateID, commentID, comment.Revision, action)
		if publish && err == nil && result.RowsAffected() > 0 {
			w.h.workflowAuthorityService().PublishWorkflowIssueChange(ctx, issue, service.WorkflowActor{Type: "system"})
		}
		return true, err
	}
	blocked := func(reason DispatchReasonCode) (bool, error) {
		return recordAttempt("workflow_comment_recovery_blocked", reason, true)
	}
	var scopeDigest string
	if err := w.h.DB.QueryRow(ctx, `SELECT scope_digest FROM issue_workflow_candidate
	 WHERE id=$1 AND issue_id=$2 AND workspace_id=$3`, candidateID, issueID, workspaceID).Scan(&scopeDigest); err != nil {
		return true, err
	}
	if scopeDigest != service.WorkflowScopeDigest(issue, pinned.Version) {
		return blocked(ReasonTargetUnavailable)
	}
	var current bool
	if err := w.h.DB.QueryRow(ctx, `SELECT workflow_recorded_comment_input_current($1,$2,$3)`, issueID, agentID, commentID).Scan(&current); err != nil {
		return true, err
	}
	if !current {
		return blocked(ReasonTargetUnavailable)
	}
	author := uuidToString(comment.AuthorID)
	agent, err := w.h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: agentID, WorkspaceID: workspaceID})
	if err != nil || !agent.RuntimeID.Valid || agent.ArchivedAt.Valid {
		return blocked(ReasonTargetUnavailable)
	}
	if !w.h.canInvokeAgent(ctx, agent, "member", author, author, uuidToString(workspaceID)) {
		return blocked(ReasonInvocationNotAllowed)
	}
	if replaced, err := w.h.TaskService.ReplaceUnclaimedWorkflowCommentRuntime(ctx, issue, agentID,
		candidateID, commentID, handoffID, sourceID); err != nil {
		return blocked(commentEnqueueFailureReason(err))
	} else if replaced {
		return true, nil
	}
	// Recover the recorded route, rather than resolving new mentions or a new
	// implicit birth. A promised answer remains valid after external completion
	// even when the normal new-conversation route is closed.
	trigger := commentAgentTrigger{Agent: agent, Source: commentTriggerSourceIssueAssignee}
	if handoffID.Valid {
		trigger.Source = commentTriggerSourceWorkflowFeedback
		trigger.WorkflowFeedback = &workflowHumanCommentRoute{HandoffID: handoffID, CoordinatorTask: sourceID, CandidateID: candidateID}
	} else {
		if issue.AssigneeType.String != "agent" || issue.AssigneeID != agentID {
			return blocked(ReasonTargetUnavailable)
		}
		pending, err := w.h.hasPendingTaskForIssueAndAgent(ctx, issueID, agentID, commentTriggerComputeOptions{
			ThreadCommentID: comment.ID, ExcludeTriggerCommentID: comment.ID,
		})
		if err != nil {
			return true, err
		}
		trigger.AlreadyPending = pending
	}
	// TaskService takes its own owner/issue transaction fence and validates
	// the recorded proof again. Never hold an issue lock across this call.
	status, reason := w.h.resolveCommentTriggerEnqueue(ctx, issue, trigger, comment.ID)
	if status == DispatchBlocked {
		return blocked(reason)
	}
	if status == DispatchDeferred {
		var planned bool
		if err := w.h.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_queue task
		 WHERE task.issue_id=$1 AND task.agent_id=$2 AND task.id IS DISTINCT FROM $4::uuid
		 AND task.status IN ('queued','deferred','dispatched','running','waiting_local_directory')
		 AND (task.trigger_comment_id=$3 OR $3::uuid=ANY(task.coalesced_comment_ids)))`,
			issueID, agentID, commentID, sourceID).Scan(&planned); err != nil {
			return true, err
		}
		if !planned {
			// The ordinary resolver may defer a newer comment by timestamp to
			// an active turn without changing its immutable input receipt. Wait
			// for completion replay; a durable retry delay prevents busy polling.
			_, err := recordAttempt("workflow_comment_recovery_deferred", "", false)
			return false, err
		}
	}
	return true, nil
}

-- A fresh member assignment may resume the current agent after a consumed
-- human handoff. Other task origins retain the ordinary handoff lineage fence.
CREATE FUNCTION workflow_direct_human_assignment_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM agent_task_queue t
   JOIN issue i ON i.id=t.issue_id
   JOIN agent recipient ON recipient.id=t.agent_id AND recipient.workspace_id=i.workspace_id
   JOIN agent_runtime runtime ON runtime.id=t.runtime_id AND runtime.workspace_id=i.workspace_id
   JOIN member actor ON actor.workspace_id=i.workspace_id AND actor.user_id=t.originator_user_id
   -- Attribution can inherit a member creator even for an agent mutation.
   -- The latest run-triggering audit event proves an actual fresh assignment.
   JOIN activity_log assignment ON assignment.id=(SELECT latest.id FROM activity_log latest
     WHERE latest.issue_id=i.id AND latest.workspace_id=i.workspace_id
       AND latest.action='assignee_changed'
     ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1)
   JOIN activity_log trigger_actor ON trigger_actor.id=(SELECT latest.id FROM activity_log latest
     WHERE latest.issue_id=i.id AND latest.workspace_id=i.workspace_id
       AND latest.action IN ('assignee_changed','status_changed') AND latest.created_at<=t.created_at
     ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1)
   JOIN issue_wakeup w ON w.id=(SELECT latest.id FROM issue_wakeup latest
     WHERE latest.issue_id=i.id AND latest.workspace_id=i.workspace_id
       AND latest.handoff->>'assignee_type'='member'
       AND latest.handoff_completed_at IS NOT NULL AND latest.disabled_at IS NULL
     ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1)
   JOIN agent_task_queue source ON source.id=w.filter_task_id AND source.issue_id=i.id
     AND source.agent_id=w.agent_id
   WHERE t.id=candidate_id AND i.id=candidate_issue
     AND i.workflow_policy IS NOT NULL AND NOT i.workflow_frozen
     AND workflow_issue_executable(i.id) AND workflow_acceptance_claimable(i.id)
     AND i.assignee_type='agent' AND i.assignee_id=t.agent_id
     AND recipient.archived_at IS NULL AND recipient.runtime_id=t.runtime_id
     AND t.originator_source='direct_human' AND t.accountable_user_id=actor.user_id
     AND t.context->>'explicit_assignment_actor_user_id'=actor.user_id::text
     AND assignment.action='assignee_changed' AND assignment.actor_type='member'
     AND assignment.actor_id=actor.user_id
     AND trigger_actor.actor_type='member' AND trigger_actor.actor_id=actor.user_id
     AND assignment.details->>'to_type'='agent' AND assignment.details->>'to_id'=recipient.id::text
     AND assignment.created_at>w.handoff_completed_at AND assignment.created_at<=t.created_at
     AND t.trigger_evidence_kind='issue_assignment' AND t.trigger_evidence_ref_id=i.id
     AND t.trigger_comment_id IS NULL
     AND t.delegated_from_task_id IS NULL AND t.parent_task_id IS NULL
     AND t.retry_of_task_id IS NULL AND t.rerun_of_task_id IS NULL
     AND w.last_task_id IS NULL AND w.source_task_id=source.id
     AND w.filter_agent_id=source.agent_id
     AND w.handoff->>'outgoing_task_id'=source.id::text
     AND source.status='completed' AND t.created_at>w.handoff_completed_at
     AND NOT EXISTS (SELECT 1 FROM issue_wakeup pending
       WHERE pending.issue_id=i.id AND pending.workspace_id=i.workspace_id
         AND (pending.created_at,pending.id)>(w.created_at,w.id)
         AND pending.handoff IS NOT NULL
         AND pending.handoff_completed_at IS NULL AND pending.disabled_at IS NULL
         AND pending.enabled)
     AND NOT EXISTS (SELECT 1 FROM agent_task_queue active WHERE active.issue_id=i.id
       AND active.id<>t.id AND active.status IN ('dispatched','running','waiting_local_directory'))
 );
$$;

CREATE OR REPLACE FUNCTION workflow_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT workflow_direct_human_assignment_task_claimable(candidate_id,candidate_issue)
     OR workflow_direct_owner_comment_task_claimable(candidate_id,candidate_issue)
     OR (workflow_provider_feedback_task_current(candidate_id,candidate_issue)
       AND NOT EXISTS (SELECT 1 FROM agent_task_queue active WHERE active.issue_id=candidate_issue
         AND active.id<>candidate_id AND active.status IN ('dispatched','running','waiting_local_directory')))
     OR workflow_human_comment_task_claimable(candidate_id,candidate_issue)
     OR (workflow_requested_comment_task_current(candidate_id,candidate_issue)
       AND NOT EXISTS (SELECT 1 FROM agent_task_queue active WHERE active.issue_id=candidate_issue
         AND active.id<>candidate_id AND active.status IN ('dispatched','running','waiting_local_directory')))
     OR (workflow_accepted_comment_task_current(candidate_id,candidate_issue)
       AND NOT EXISTS (SELECT 1 FROM agent_task_queue active WHERE active.issue_id=candidate_issue
         AND active.id<>candidate_id AND active.status IN ('dispatched','running','waiting_local_directory')))
     OR workflow_outcome_task_claimable(candidate_id,candidate_issue)
     OR workflow_regular_task_claimable(candidate_id,candidate_issue);
$$;

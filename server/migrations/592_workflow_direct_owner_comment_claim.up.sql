-- A direct owner comment addressed to the current agent remains executable
-- after an earlier member handoff has been consumed. The ordinary handoff
-- lineage fence still governs agent-created, delegated, and generic tasks.
CREATE FUNCTION workflow_direct_owner_comment_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM agent_task_queue t
   JOIN issue i ON i.id=t.issue_id
   JOIN agent recipient ON recipient.id=t.agent_id AND recipient.workspace_id=i.workspace_id
   JOIN comment c ON c.id=t.trigger_comment_id AND c.issue_id=i.id AND c.workspace_id=i.workspace_id
   JOIN member author ON author.workspace_id=i.workspace_id AND author.user_id=c.author_id
   JOIN issue_wakeup w ON w.id=(SELECT latest.id FROM issue_wakeup latest
     WHERE latest.issue_id=i.id AND latest.workspace_id=i.workspace_id
       AND latest.handoff IS NOT NULL AND latest.disabled_at IS NULL
     ORDER BY latest.id DESC LIMIT 1)
   JOIN agent_task_queue source ON source.id=w.filter_task_id AND source.issue_id=i.id
     AND source.agent_id=w.agent_id
   WHERE t.id=candidate_id AND i.id=candidate_issue
     AND i.workflow_policy IS NOT NULL AND NOT i.workflow_frozen
     AND workflow_issue_executable(i.id) AND workflow_acceptance_claimable(i.id)
     AND i.status='in_review' AND i.assignee_type='agent' AND i.assignee_id=t.agent_id
     AND recipient.archived_at IS NULL AND recipient.runtime_id=t.runtime_id
     AND c.author_type='member' AND c.type IN ('comment','progress_update')
     AND c.deleted_at IS NULL AND c.source_task_id IS NULL
     AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
     AND author.role='owner'
     AND t.originator_source='direct_human' AND t.originator_user_id=c.author_id
     AND t.accountable_user_id=c.author_id
     AND t.trigger_evidence_kind='comment' AND t.trigger_evidence_ref_id=c.id
     AND t.delegated_from_task_id IS NULL AND t.parent_task_id IS NULL
     AND t.retry_of_task_id IS NULL AND t.rerun_of_task_id IS NULL
     AND t.created_at>=c.created_at
     AND w.last_task_id IS NULL AND w.handoff_completed_at IS NOT NULL
     AND w.source_task_id=source.id AND w.filter_agent_id=source.agent_id
     AND w.handoff->>'assignee_type'='member'
     AND w.handoff->>'outgoing_task_id'=source.id::text
     AND source.status='completed' AND c.created_at>w.handoff_completed_at
     AND NOT EXISTS (SELECT 1 FROM agent_task_queue active WHERE active.issue_id=i.id
       AND active.id<>t.id AND active.status IN ('dispatched','running','waiting_local_directory'))
 );
$$;

CREATE OR REPLACE FUNCTION workflow_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT workflow_direct_owner_comment_task_claimable(candidate_id,candidate_issue)
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

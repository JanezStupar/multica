-- A comment on a candidate handed to a person may wake only the coordinator
-- named by the latest completed member handoff. Its marker is created with the
-- task in one transaction. Claim and start both recheck these live facts under
-- the issue lock, so a newer handoff or candidate revocation closes the door.
CREATE FUNCTION workflow_human_comment_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM agent_task_queue t
   JOIN issue i ON i.id=t.issue_id
   JOIN issue_workflow_candidate candidate ON candidate.id=i.workflow_candidate_id AND candidate.issue_id=i.id
   JOIN issue_wakeup w ON w.id=(SELECT latest.id FROM issue_wakeup latest
     WHERE latest.issue_id=i.id AND latest.handoff IS NOT NULL AND latest.disabled_at IS NULL
     ORDER BY latest.id DESC LIMIT 1)
   JOIN agent_task_queue source ON source.id=w.filter_task_id AND source.issue_id=i.id
     AND source.agent_id=w.agent_id
   JOIN comment c ON c.id=t.trigger_comment_id AND c.issue_id=i.id AND c.workspace_id=i.workspace_id
   JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=c.author_id
   WHERE t.id=candidate_id AND i.id=candidate_issue AND workflow_issue_executable(i.id)
     AND i.workflow_policy IS NOT NULL AND NOT i.workflow_frozen
     AND candidate.policy_version=i.workflow_policy->>'version'
     AND i.assignee_type='member' AND i.assignee_id IS NOT NULL
     AND w.last_task_id IS NULL AND w.handoff_completed_at IS NOT NULL
     AND w.source_task_id=source.id AND w.filter_agent_id=source.agent_id
     AND w.handoff->>'assignee_type'='member' AND w.handoff->>'assignee_id'=i.assignee_id::text
     AND w.handoff->>'outgoing_task_id'=source.id::text
     AND source.status='completed' AND t.agent_id=source.agent_id
     AND t.delegated_from_task_id=source.id
     AND t.trigger_evidence_kind='workflow_human_comment' AND t.trigger_evidence_ref_id=w.id
     AND t.context->'workflow_feedback'->>'candidate_id'=candidate.id::text
     AND t.context->'workflow_feedback'->>'handoff_id'=w.id::text
     AND t.context->'workflow_feedback'->>'coordinator_task_id'=source.id::text
     AND t.context->'workflow_feedback'->>'comment_id'=c.id::text
     AND c.author_type='member' AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
     AND c.created_at>candidate.created_at
     AND t.originator_user_id=c.author_id AND t.accountable_user_id=c.author_id
     AND (i.assignee_id=c.author_id OR m.role IN ('owner','admin'))
     AND NOT EXISTS (SELECT 1 FROM agent_task_queue active WHERE active.issue_id=i.id
       AND active.id<>t.id AND active.status IN ('dispatched','running','waiting_local_directory'))
 );
$$;

CREATE OR REPLACE FUNCTION workflow_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT workflow_human_comment_task_claimable(candidate_id,candidate_issue)
     OR workflow_outcome_task_claimable(candidate_id,candidate_issue)
     OR workflow_regular_task_claimable(candidate_id,candidate_issue);
$$;

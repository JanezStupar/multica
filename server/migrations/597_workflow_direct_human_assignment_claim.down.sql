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

DROP FUNCTION workflow_direct_human_assignment_task_claimable(uuid,uuid);

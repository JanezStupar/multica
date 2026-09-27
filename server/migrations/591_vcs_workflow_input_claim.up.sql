-- Provider feedback is an authorized continuation of a retained writer, even
-- while the ticket awaits its human. The proof is server-owned input delivery.
CREATE FUNCTION workflow_provider_feedback_task_current(candidate_task uuid,candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 WITH RECURSIVE lineage AS (
  SELECT t.id,t.retry_of_task_id,t.rerun_of_task_id,t.issue_id,t.agent_id,t.runtime_id,t.originator_user_id,t.accountable_user_id,ARRAY[t.id] visited
  FROM agent_task_queue t WHERE t.id=candidate_task AND t.issue_id=candidate_issue
  UNION ALL
  SELECT parent.id,parent.retry_of_task_id,parent.rerun_of_task_id,parent.issue_id,parent.agent_id,parent.runtime_id,parent.originator_user_id,parent.accountable_user_id,child.visited||parent.id
  FROM lineage child JOIN agent_task_queue parent ON parent.id=COALESCE(child.retry_of_task_id,child.rerun_of_task_id)
  WHERE NOT parent.id=ANY(child.visited) AND parent.issue_id=child.issue_id AND parent.agent_id=child.agent_id
   AND parent.originator_user_id IS NOT DISTINCT FROM child.originator_user_id AND parent.accountable_user_id IS NOT DISTINCT FROM child.accountable_user_id
 )
 SELECT EXISTS (
  SELECT 1 FROM issue i JOIN issue_workflow_candidate candidate ON candidate.id=i.workflow_candidate_id
   AND candidate.issue_id=i.id AND candidate.workspace_id=i.workspace_id
  JOIN vcs_workflow_input input ON input.issue_id=i.id AND input.workspace_id=i.workspace_id AND input.task_id IN (SELECT id FROM lineage)
  JOIN agent_task_queue t ON t.id=candidate_task AND t.issue_id=i.id
  JOIN agent_task_queue source ON source.id=input.source_task_id AND source.id=candidate.writer_task_id AND source.issue_id=i.id
  JOIN agent active ON active.id=t.agent_id AND active.workspace_id=i.workspace_id
  LEFT JOIN member originator ON originator.workspace_id=i.workspace_id AND originator.user_id=t.originator_user_id
  WHERE i.id=candidate_issue AND NOT i.workflow_frozen AND i.workflow_policy IS NOT NULL
   AND candidate.policy_version=i.workflow_policy->>'version'
   AND EXISTS(SELECT 1 FROM issue_status s WHERE s.workspace_id=i.workspace_id AND s.key=i.status AND s.category NOT IN ('done','closed','cancelled'))
   AND i.status NOT IN ('done','cancelled')
   AND input.processed_at IS NOT NULL AND source.status='completed' AND source.agent_id=t.agent_id
   AND active.runtime_id=t.runtime_id AND active.archived_at IS NULL
   AND (t.originator_user_id IS NULL OR originator.user_id IS NOT NULL)
   AND t.originator_user_id IS NOT DISTINCT FROM source.originator_user_id AND t.accountable_user_id IS NOT DISTINCT FROM source.accountable_user_id
   AND t.delegated_from_task_id=source.id
   AND t.trigger_evidence_kind='vcs_pr_feedback'
   AND EXISTS(SELECT 1 FROM vcs_workflow_input primary_input WHERE primary_input.id=t.trigger_evidence_ref_id AND primary_input.task_id=input.task_id
     AND primary_input.issue_id=i.id AND primary_input.workspace_id=i.workspace_id)
   AND (t.id<>input.task_id OR t.rerun_of_task_id IS NULL OR (t.rerun_of_task_id=source.id AND source.runtime_id=t.runtime_id))
   AND (t.workflow_profile_id IS NULL OR EXISTS(SELECT 1 FROM issue_workflow_profile retained WHERE retained.id=t.workflow_profile_id
     AND retained.issue_id=i.id AND retained.workspace_id=i.workspace_id AND retained.agent_id=t.agent_id
     AND retained.policy_version=candidate.policy_version))
 );
$$;

CREATE OR REPLACE FUNCTION workflow_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT (workflow_provider_feedback_task_current(candidate_id,candidate_issue)
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

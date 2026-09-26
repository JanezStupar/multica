CREATE OR REPLACE FUNCTION workflow_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT workflow_outcome_task_claimable(candidate_id,candidate_issue)
     OR workflow_regular_task_claimable(candidate_id,candidate_issue);
$$;

DROP FUNCTION IF EXISTS workflow_human_comment_task_claimable(uuid, uuid);

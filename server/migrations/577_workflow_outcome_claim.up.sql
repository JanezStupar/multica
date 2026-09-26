-- Keep the existing handoff claim predicate intact, and admit only the exact
-- post-delivery outcome task recorded by a format-2 acceptance.
ALTER FUNCTION workflow_task_claimable(uuid, uuid) RENAME TO workflow_regular_task_claimable;

CREATE FUNCTION workflow_outcome_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM issue i
   JOIN issue_workflow_acceptance a ON a.issue_id=i.id AND a.workspace_id=i.workspace_id
   JOIN agent_task_queue t ON t.id=$1 AND t.issue_id=i.id
   WHERE i.id=$2 AND NOT i.workflow_frozen
     AND i.workflow_candidate_id=a.candidate_id
     AND i.workflow_policy->>'version'=a.policy_version
     AND i.status=a.accepted_status_key
     AND a.completion_version=2 AND a.state='accepted' AND a.revoked_at IS NULL
     AND NOT a.outcome_complete AND a.outcome_task_id=t.id
     AND a.outcome_agent_id=t.agent_id
     AND NOT EXISTS (SELECT 1 FROM issue_workflow_delivery d
       WHERE d.acceptance_id=a.id AND (d.status<>'delivered' OR d.merged_at IS NULL))
     AND (SELECT count(*) FROM issue_workflow_delivery d WHERE d.acceptance_id=a.id)=
         (SELECT jsonb_array_length(c.pr_set) FROM issue_workflow_candidate c
          WHERE c.id=a.candidate_id AND c.issue_id=i.id)
 );
$$;

-- sqlc does not model ALTER FUNCTION RENAME; OR REPLACE also permits its
-- schema parser to replace the original name. PostgreSQL creates it anew here.
CREATE OR REPLACE FUNCTION workflow_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT workflow_outcome_task_claimable(candidate_id,candidate_issue)
     OR workflow_regular_task_claimable(candidate_id,candidate_issue);
$$;

CREATE OR REPLACE FUNCTION workflow_acceptance_claimable(candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS (
   SELECT 1 FROM issue i
   WHERE i.id=candidate_issue AND i.workflow_policy IS NOT NULL
     AND (
       i.status IN ('done','cancelled')
       OR EXISTS (SELECT 1 FROM issue_status status
                  WHERE status.workspace_id=i.workspace_id AND status.key=i.status
                    AND status.category IN ('done','closed'))
       OR EXISTS (SELECT 1 FROM issue_workflow_acceptance a
                  WHERE a.issue_id=i.id AND a.workspace_id=i.workspace_id
                    AND a.candidate_id=i.workflow_candidate_id AND a.revoked_at IS NULL
                    AND (a.state='requested' OR a.state='accepted' AND a.completion_version=2))
     )
 );
$$;

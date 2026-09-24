-- Requested acceptance is an authority transition, so no new task may claim
-- the candidate while its exact issue/candidate request is unresolved.
-- Completed/closed enrolled work likewise has no executable queue.
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
                    AND a.candidate_id=i.workflow_candidate_id
                    AND a.state='requested' AND a.revoked_at IS NULL)
     )
 );
$$;

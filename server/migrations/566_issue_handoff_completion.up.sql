-- A member recipient has no agent task. Keep a durable consumed marker without
-- overloading last_task_id or changing the immutable handoff intent.
ALTER TABLE issue_wakeup ADD COLUMN handoff_completed_at timestamptz;

CREATE OR REPLACE FUNCTION workflow_handoff_claimable(candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT EXISTS (
   SELECT 1 FROM issue_wakeup w
   WHERE w.id=(SELECT latest.id FROM issue_wakeup latest
               WHERE latest.issue_id=candidate_issue AND latest.handoff IS NOT NULL
                 AND latest.disabled_at IS NULL ORDER BY latest.id DESC LIMIT 1)
     AND w.last_task_id IS NULL AND w.handoff_completed_at IS NOT NULL
 );
$$;

CREATE OR REPLACE FUNCTION workflow_handoff_claimable(candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$;
ALTER TABLE issue_wakeup DROP COLUMN IF EXISTS handoff_completed_at;

CREATE OR REPLACE FUNCTION workflow_acceptance_claimable(candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$;

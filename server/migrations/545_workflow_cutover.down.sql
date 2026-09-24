DROP TRIGGER IF EXISTS issue_workspace_workflow_default ON issue;
DROP FUNCTION IF EXISTS copy_workspace_workflow_default();
CREATE OR REPLACE FUNCTION workflow_issue_executable(candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true; $$;
ALTER TABLE issue DROP COLUMN IF EXISTS workflow_migrated_at;
ALTER TABLE issue DROP COLUMN IF EXISTS workflow_frozen;
ALTER TABLE workspace DROP COLUMN IF EXISTS workflow_cutover_at;
ALTER TABLE workspace DROP COLUMN IF EXISTS workflow_default_policy;

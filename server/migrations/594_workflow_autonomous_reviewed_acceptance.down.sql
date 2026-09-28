-- Existing reviewed rows prevent a safe rollback to the legacy constraint.
-- The migration runner must only apply this rollback after those rows have
-- been retired or migrated explicitly.
ALTER TABLE issue_workflow_acceptance
    DROP CONSTRAINT issue_workflow_acceptance_mode_check;

ALTER TABLE issue_workflow_acceptance
    ADD CONSTRAINT issue_workflow_acceptance_mode_check
    CHECK (mode IN ('human', 'trivial'));

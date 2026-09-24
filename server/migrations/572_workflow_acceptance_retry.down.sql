ALTER TABLE issue_workflow_acceptance
    DROP COLUMN IF EXISTS last_error_class,
    DROP COLUMN IF EXISTS next_attempt_at;

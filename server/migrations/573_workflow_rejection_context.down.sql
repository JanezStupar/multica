ALTER TABLE issue_workflow_rejection
    DROP COLUMN IF EXISTS continuity_note,
    DROP COLUMN IF EXISTS context_mode;

ALTER TABLE issue_workflow_rejection
    DROP COLUMN IF EXISTS comment_revision,
    DROP COLUMN IF EXISTS comment_id;

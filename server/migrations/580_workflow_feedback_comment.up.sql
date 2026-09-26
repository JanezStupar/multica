ALTER TABLE issue_workflow_rejection
    ADD COLUMN comment_id uuid,
    ADD COLUMN comment_revision bigint;

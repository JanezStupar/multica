-- Idempotency compares the full request intent, including whether a scoped
-- instruction was omitted, set, or explicitly cleared.
ALTER TABLE issue_workflow_profile ADD COLUMN request_digest text;

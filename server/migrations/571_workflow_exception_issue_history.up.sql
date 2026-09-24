CREATE INDEX CONCURRENTLY issue_workflow_exception_issue_history_idx ON issue_workflow_exception (workspace_id, issue_id, created_at DESC, id DESC);

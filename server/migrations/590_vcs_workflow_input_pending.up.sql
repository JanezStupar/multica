CREATE INDEX CONCURRENTLY vcs_workflow_input_pending ON vcs_workflow_input(next_attempt_at,created_at) WHERE processed_at IS NULL;

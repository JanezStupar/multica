CREATE INDEX CONCURRENTLY issue_workflow_delivery_due_idx ON issue_workflow_delivery(status, next_attempt_at) WHERE status IN ('pending', 'retry');

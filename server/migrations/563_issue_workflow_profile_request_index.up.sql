CREATE UNIQUE INDEX CONCURRENTLY issue_workflow_profile_request_idx ON issue_workflow_profile (workspace_id, issue_id, agent_id, request_id) WHERE request_id IS NOT NULL;

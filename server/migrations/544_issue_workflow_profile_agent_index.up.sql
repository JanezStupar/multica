CREATE UNIQUE INDEX CONCURRENTLY issue_workflow_profile_agent_idx ON issue_workflow_profile (workspace_id, issue_id, agent_id, policy_version);

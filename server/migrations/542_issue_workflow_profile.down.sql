ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS workflow_policy_version;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS workflow_profile_id;
DROP TABLE IF EXISTS issue_workflow_profile;

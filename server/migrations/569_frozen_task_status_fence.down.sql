DROP TRIGGER IF EXISTS agent_task_workflow_frozen_status ON agent_task_queue;
DROP FUNCTION IF EXISTS guard_frozen_issue_task_status();

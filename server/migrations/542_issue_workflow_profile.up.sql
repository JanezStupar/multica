-- A selected agent's behavioral inputs are captured on its first enrolled
-- issue claim. Runtime access, credentials and machine binding remain live.
CREATE TABLE issue_workflow_profile (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    issue_id uuid NOT NULL,
    agent_id uuid NOT NULL,
    policy_version text NOT NULL,
    snapshot jsonb NOT NULL,
    digest text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Each task records the effective immutable profile it was offered. A later
-- ticket policy migration must not reinterpret historical task executions.
ALTER TABLE agent_task_queue ADD COLUMN workflow_profile_id uuid;
ALTER TABLE agent_task_queue ADD COLUMN workflow_policy_version text;

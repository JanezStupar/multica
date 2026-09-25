ALTER TABLE issue_workflow_acceptance
    DROP CONSTRAINT IF EXISTS workflow_completion_v2_config,
    DROP COLUMN IF EXISTS outcome_requested_at,
    DROP COLUMN IF EXISTS outcome_request_task_id,
    DROP COLUMN IF EXISTS outcome_task_id,
    DROP COLUMN IF EXISTS outcome_completed_at,
    DROP COLUMN IF EXISTS outcome_complete,
    DROP COLUMN IF EXISTS released_at,
    DROP COLUMN IF EXISTS held_at,
    DROP COLUMN IF EXISTS hold_delivery,
    DROP COLUMN IF EXISTS outcome_agent_id,
    DROP COLUMN IF EXISTS accepted_status_key,
    DROP COLUMN IF EXISTS completion_version;

ALTER TABLE issue_workflow_profile
    DROP COLUMN IF EXISTS revision,
    DROP COLUMN IF EXISTS previous_profile_id,
    DROP COLUMN IF EXISTS request_id,
    DROP COLUMN IF EXISTS actor_user_id,
    DROP COLUMN IF EXISTS reason,
    DROP COLUMN IF EXISTS consequences,
    DROP COLUMN IF EXISTS reconciliation;

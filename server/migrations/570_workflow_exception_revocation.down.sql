ALTER TABLE issue_workflow_exception
    DROP COLUMN IF EXISTS revocation_reason,
    DROP COLUMN IF EXISTS revocation_consequences,
    DROP COLUMN IF EXISTS revoked_by_type,
    DROP COLUMN IF EXISTS revoked_by_id;

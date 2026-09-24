ALTER TABLE issue_workflow_exception
    ADD COLUMN revocation_reason text,
    ADD COLUMN revocation_consequences text,
    ADD COLUMN revoked_by_type text CHECK (revoked_by_type IN ('member', 'agent')),
    ADD COLUMN revoked_by_id uuid;

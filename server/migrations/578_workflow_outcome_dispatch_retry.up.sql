-- A provider merge is irreversible. Keep its verified delivery row even when
-- the separate outcome-task enqueue fails, then retry from the durable ledger.
ALTER TABLE issue_workflow_acceptance
    ADD COLUMN outcome_dispatch_attempt_count integer NOT NULL DEFAULT 0,
    ADD COLUMN outcome_next_attempt_at timestamptz;

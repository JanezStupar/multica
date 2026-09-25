-- Format-2 completion state is issue-owned and additive. Existing accepted
-- format-1 rows retain their original done-on-acceptance behavior.
ALTER TABLE issue_workflow_acceptance
    ADD COLUMN completion_version smallint NOT NULL DEFAULT 1 CHECK (completion_version IN (1, 2)),
    ADD COLUMN accepted_status_key text,
    ADD COLUMN outcome_agent_id uuid,
    ADD COLUMN hold_delivery boolean NOT NULL DEFAULT false,
    ADD COLUMN held_at timestamptz,
    ADD COLUMN released_at timestamptz,
    ADD COLUMN outcome_complete boolean NOT NULL DEFAULT false,
    ADD COLUMN outcome_completed_at timestamptz,
    ADD COLUMN outcome_task_id uuid,
    ADD COLUMN outcome_request_task_id uuid,
    ADD COLUMN outcome_requested_at timestamptz,
    ADD CONSTRAINT workflow_completion_v2_config CHECK (
      completion_version = 1 OR accepted_status_key IS NOT NULL AND outcome_agent_id IS NOT NULL
    );

-- A deliberate issue-agent reselection appends a new behavioral profile while
-- tasks already bound to an older row retain their immutable execution input.
ALTER TABLE issue_workflow_profile
    ADD COLUMN revision integer NOT NULL DEFAULT 1,
    ADD COLUMN previous_profile_id uuid,
    ADD COLUMN request_id uuid,
    ADD COLUMN actor_user_id uuid,
    ADD COLUMN reason text,
    ADD COLUMN consequences text,
    ADD COLUMN reconciliation text;

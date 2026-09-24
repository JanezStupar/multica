-- Provider verification may be temporarily unavailable after an autonomous
-- requester completes. Persist a bounded retry schedule so one request cannot
-- starve later terminal requests in the shared finalizer poll.
ALTER TABLE issue_workflow_acceptance
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN last_error_class text;

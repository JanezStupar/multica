-- Session selection belongs to the saved wakeup, so recurring blind reviews
-- do not accidentally resume a prior verdict's conversation.
ALTER TABLE issue_wakeup ADD COLUMN IF NOT EXISTS force_fresh_session BOOLEAN NOT NULL DEFAULT FALSE;

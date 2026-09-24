CREATE UNIQUE INDEX CONCURRENTLY issue_wakeup_handoff_request_key_idx ON issue_wakeup(issue_id,request_key) WHERE request_key IS NOT NULL;

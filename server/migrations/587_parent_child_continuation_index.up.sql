CREATE UNIQUE INDEX CONCURRENTLY issue_wakeup_child_issue_idx ON issue_wakeup(issue_id,child_issue_id) WHERE child_issue_id IS NOT NULL;

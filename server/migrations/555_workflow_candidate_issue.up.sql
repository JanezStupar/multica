CREATE INDEX CONCURRENTLY issue_workflow_candidate_issue_idx ON issue_workflow_candidate(issue_id, created_at DESC);

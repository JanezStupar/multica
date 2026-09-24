-- name: LockWorkflowClaimIssue :one
-- A task is provisionally claimed first. NOWAIT avoids inverting issue-first
-- mutation locks, and any contention rolls that provisional claim back.
-- Lock all issue tasks: a legacy issue may become frozen during cutover, and
-- the fresh post-lock predicate must see that state even if the claim
-- candidate was read before the freeze committed.
SELECT id FROM issue WHERE id = @id
FOR NO KEY UPDATE NOWAIT;

-- name: CheckWorkflowTaskClaimable :one
SELECT workflow_task_claimable(@task_id::uuid, @issue_id::uuid)::boolean;

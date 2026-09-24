-- name: GetIssueWorkflowProfile :one
SELECT id, workspace_id, issue_id, agent_id, policy_version, revision, snapshot, digest, created_at
FROM issue_workflow_profile WHERE workspace_id = $1 AND issue_id = $2 AND agent_id = $3 AND policy_version = $4
ORDER BY revision DESC LIMIT 1;

-- name: GetIssueWorkflowProfileByID :one
SELECT id, workspace_id, issue_id, agent_id, policy_version, revision, snapshot, digest, created_at
FROM issue_workflow_profile WHERE id = $1 AND workspace_id = $2 AND issue_id = $3 AND agent_id = $4;

-- name: InsertIssueWorkflowProfile :one
INSERT INTO issue_workflow_profile (id, workspace_id, issue_id, agent_id, policy_version, snapshot, digest)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, workspace_id, issue_id, agent_id, policy_version, snapshot, digest, created_at;

-- name: GetIssueWorkflowProfileByRequest :one
SELECT id, workspace_id, issue_id, agent_id, policy_version, revision, previous_profile_id,
       request_id, request_digest, actor_user_id, reason, consequences, reconciliation, snapshot, digest, created_at
FROM issue_workflow_profile
WHERE workspace_id = $1 AND issue_id = $2 AND agent_id = $3 AND request_id = $4;

-- name: InsertReselectedIssueWorkflowProfile :one
INSERT INTO issue_workflow_profile (id, workspace_id, issue_id, agent_id, policy_version,
    revision, previous_profile_id, request_id, request_digest, actor_user_id, reason, consequences,
    reconciliation, snapshot, digest)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING id, workspace_id, issue_id, agent_id, policy_version, revision,
          previous_profile_id, request_id, request_digest, actor_user_id, reason, consequences,
          reconciliation, snapshot, digest, created_at;

-- name: BindTaskWorkflowProfile :one
UPDATE agent_task_queue SET workflow_profile_id = $2, workflow_policy_version = $3
WHERE agent_task_queue.id = $1 AND agent_task_queue.issue_id = $4 AND agent_task_queue.agent_id = $5
  AND runtime_id = $6 AND dispatched_at = $7
  AND status = 'dispatched' AND started_at IS NULL
  AND EXISTS (SELECT 1 FROM issue i WHERE i.id = $4 AND i.workspace_id = $8)
  AND (workflow_profile_id IS NULL OR workflow_profile_id = $2)
  AND (workflow_policy_version IS NULL OR workflow_policy_version = $3)
RETURNING agent_task_queue.id;

-- name: HasEarlierIssueAgentTask :one
-- Queued siblings have never received instructions and may be claimed out
-- of order. Any other dispatched/started task without a profile is a cutover
-- ambiguity, regardless of creation order.
SELECT EXISTS (
    SELECT 1 FROM agent_task_queue prior
    JOIN issue i ON i.id = prior.issue_id AND i.workspace_id = $4
    WHERE prior.issue_id = $1 AND prior.agent_id = $2 AND prior.id <> $3
      AND (prior.dispatched_at IS NOT NULL OR prior.started_at IS NOT NULL
        OR prior.session_id IS NOT NULL OR prior.work_dir IS NOT NULL
        OR prior.workflow_profile_id IS NOT NULL
        OR prior.status IN ('completed', 'failed', 'cancelled'))
      AND (i.workflow_migrated_at IS NULL
        OR prior.dispatched_at >= i.workflow_migrated_at
        OR prior.started_at >= i.workflow_migrated_at
        OR prior.completed_at >= i.workflow_migrated_at
        OR (prior.created_at >= i.workflow_migrated_at AND prior.status IN ('completed', 'failed', 'cancelled')))
);

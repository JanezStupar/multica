-- name: GetIssueWorkflowPolicy :one
SELECT workflow_policy FROM issue
WHERE id = $1 AND workspace_id = $2;

-- name: GetWorkflowPolicySourceSkill :one
-- One MVCC statement captures the SKILL.md and all supporting files together.
SELECT jsonb_build_object(
    'id', s.id::text,
    'name', s.name,
    'description', s.description,
    'content', s.content,
    'files', COALESCE((
        SELECT jsonb_agg(jsonb_build_object('path', f.path, 'content', f.content) ORDER BY f.path)
        FROM skill_file f WHERE f.skill_id = s.id
    ), '[]'::jsonb)
) AS bundle
FROM skill s
WHERE s.id = $1 AND s.workspace_id = $2;

-- name: IssueHasTaskHistory :one
SELECT EXISTS(SELECT 1 FROM agent_task_queue WHERE issue_id = $1);

-- name: PinIssueWorkflowPolicy :one
UPDATE issue SET workflow_policy = $3
WHERE id = $1 AND workspace_id = $2 AND workflow_policy IS NULL
RETURNING workflow_policy;

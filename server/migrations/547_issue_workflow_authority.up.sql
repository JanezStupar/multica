-- Authority and delivery are issue-owned records without foreign keys. Issue
-- and workspace deletion must remove their rows explicitly in one transaction.
-- Every unique/lookup index is built concurrently in its own later migration.
ALTER TABLE issue ADD COLUMN workflow_candidate_id uuid;

CREATE TABLE issue_workflow_candidate (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    issue_id uuid NOT NULL,
    policy_version text NOT NULL,
    digest text NOT NULL,
    scope_digest text NOT NULL,
    source_handoff_id uuid NOT NULL,
    source_task_id uuid NOT NULL,
    writer_task_id uuid NOT NULL,
    pr_set jsonb NOT NULL CHECK (jsonb_typeof(pr_set) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE issue_workflow_review (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    issue_id uuid NOT NULL,
    candidate_id uuid NOT NULL,
    reviewer_task_id uuid NOT NULL,
    verdict text NOT NULL CHECK (verdict IN ('pass', 'changes_requested')),
    pr_review_urls jsonb NOT NULL CHECK (jsonb_typeof(pr_review_urls) = 'array'),
    submitted_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE issue_workflow_exception (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    issue_id uuid NOT NULL,
    candidate_id uuid NOT NULL,
    base_policy_version text NOT NULL,
    scope text NOT NULL CHECK (scope IN ('review', 'acceptance', 'delivery')),
    grant_details jsonb NOT NULL CHECK (jsonb_typeof(grant_details) = 'object'),
    actor_type text NOT NULL CHECK (actor_type IN ('member', 'agent')),
    actor_id uuid NOT NULL,
    source_task_id uuid,
    reason text NOT NULL,
    consequences text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz
);

CREATE TABLE issue_workflow_acceptance (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    issue_id uuid NOT NULL,
    candidate_id uuid NOT NULL,
    mode text NOT NULL CHECK (mode IN ('human', 'trivial')),
    actor_type text NOT NULL CHECK (actor_type IN ('member', 'agent')),
    actor_id uuid NOT NULL,
    source_task_id uuid,
    -- Autonomous acceptance is requested by a still-running task and only
    -- becomes accepted after that exact task completes successfully.
    state text NOT NULL CHECK (state IN ('requested', 'accepted', 'blocked', 'revoked')),
    issue_revision bigint,
    policy_version text NOT NULL,
    authority_snapshot jsonb NOT NULL CHECK (jsonb_typeof(authority_snapshot) = 'object'),
    classification_reason text,
    requested_at timestamptz NOT NULL DEFAULT now(),
    accepted_at timestamptz,
    revoked_at timestamptz
);

CREATE TABLE issue_workflow_rejection (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    issue_id uuid NOT NULL,
    candidate_id uuid NOT NULL,
    acceptance_id uuid,
    actor_type text NOT NULL CHECK (actor_type IN ('member', 'agent')),
    actor_id uuid NOT NULL,
    source_task_id uuid,
    kind text NOT NULL CHECK (kind IN ('in_scope_defect', 'scope_change')),
    reason text NOT NULL,
    resume_task_id uuid,
    resume_agent_id uuid,
    issue_revision bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- One row per accepted PR. The worker never stores provider credentials here.
-- It reads the current workspace binding by the pinned binding id on each try.
CREATE TABLE issue_workflow_delivery (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    issue_id uuid NOT NULL,
    acceptance_id uuid NOT NULL,
    candidate_id uuid NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    provider text NOT NULL CHECK (provider IN ('github', 'forgejo', 'gitea')),
    provider_binding_id uuid NOT NULL,
    repository_url text NOT NULL,
    pr_url text NOT NULL,
    repo_owner text NOT NULL,
    repo_name text NOT NULL,
    pr_number bigint NOT NULL CHECK (pr_number > 0),
    expected_head_sha text NOT NULL,
    action text NOT NULL CHECK (action IN ('ready', 'merge')),
    merge_method text CHECK (merge_method IN ('merge', 'squash', 'rebase')),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'retry', 'delivered', 'stale', 'blocked', 'cancelled')),
    attempt_count integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    readiness_done_at timestamptz,
    merged_at timestamptz,
    merge_commit_sha text,
    last_error_class text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((action = 'merge') = (merge_method IS NOT NULL))
);

CREATE TABLE issue_workflow_delivery_attempt (
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    issue_id uuid NOT NULL,
    delivery_id uuid NOT NULL,
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    operation text NOT NULL CHECK (operation IN ('prepare', 'merge', 'reconcile')),
    outcome text NOT NULL CHECK (outcome IN ('delivered', 'retry', 'stale', 'blocked', 'ambiguous')),
    error_class text,
    observed_head_sha text,
    created_at timestamptz NOT NULL DEFAULT now()
);

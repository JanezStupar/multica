-- Authenticated provider inputs are durable work requests, not approval records.
-- No foreign keys; owning entity deletion explicitly removes these rows.
CREATE TABLE vcs_workflow_input (
 id uuid NOT NULL,
 workspace_id uuid NOT NULL,
 issue_id uuid NOT NULL,
 connection_id uuid NOT NULL,
 pull_request_id uuid NOT NULL,
 event_key text NOT NULL,
 kind text NOT NULL,
 content text NOT NULL,
 html_url text NOT NULL,
 head_sha text NOT NULL DEFAULT '',
 source_task_id uuid,
 task_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 last_error text NOT NULL DEFAULT '',
 processed_at timestamptz
);

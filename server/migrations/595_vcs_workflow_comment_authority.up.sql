-- Provider-to-member approval bindings are explicit and scoped to a connection.
ALTER TABLE vcs_connection
 ADD COLUMN workflow_approvers jsonb NOT NULL DEFAULT '[]'::jsonb
 CHECK (jsonb_typeof(workflow_approvers) = 'array');

-- Preserve signed provider evidence separately from the agent-facing prompt.
ALTER TABLE vcs_workflow_input
 ADD COLUMN object_id text NOT NULL DEFAULT '',
 ADD COLUMN object_revision text NOT NULL DEFAULT '',
 ADD COLUMN object_revision_at timestamptz,
 ADD COLUMN object_action text NOT NULL DEFAULT '',
 ADD COLUMN provider_author_id text NOT NULL DEFAULT '',
 ADD COLUMN provider_author_login text NOT NULL DEFAULT '',
 ADD COLUMN body text NOT NULL DEFAULT '',
 ADD COLUMN candidate_id uuid;

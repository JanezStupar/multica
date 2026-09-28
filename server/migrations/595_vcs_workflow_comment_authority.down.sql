ALTER TABLE vcs_workflow_input
 DROP COLUMN IF EXISTS candidate_id,
 DROP COLUMN IF EXISTS body,
 DROP COLUMN IF EXISTS provider_author_login,
 DROP COLUMN IF EXISTS provider_author_id,
 DROP COLUMN IF EXISTS object_action,
 DROP COLUMN IF EXISTS object_revision_at,
 DROP COLUMN IF EXISTS object_revision,
 DROP COLUMN IF EXISTS object_id;

ALTER TABLE vcs_connection DROP COLUMN IF EXISTS workflow_approvers;

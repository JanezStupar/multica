-- Older personal-fork databases already have this column under migration
-- 327 or 451. The runner keys by filename, so preserve their exact lists.
ALTER TABLE agent ADD COLUMN IF NOT EXISTS enabled_builtin_skill_ids TEXT[];

-- Map stable built-in IDs to workspace skill UUIDs. An empty object means no
-- replacement; selection is still controlled by enabled_builtin_skill_ids.
ALTER TABLE agent ADD COLUMN IF NOT EXISTS builtin_skill_replacements JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Pin the effective platform bundle used by an issue task. A later turn must
-- not resume a provider session whose instructions came from another bundle.
ALTER TABLE agent_task_queue ADD COLUMN IF NOT EXISTS skill_bundle_fingerprint TEXT;

-- Keep enabled_builtin_skill_ids: it may predate this migration in a fork DB.
ALTER TABLE agent DROP COLUMN IF EXISTS builtin_skill_replacements;
ALTER TABLE agent_task_queue DROP COLUMN IF EXISTS skill_bundle_fingerprint;

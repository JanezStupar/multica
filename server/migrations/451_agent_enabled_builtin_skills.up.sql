-- This column shipped in the personal fork as migration
-- 327_agent_enabled_builtin_skills before the branch was rebased onto a main
-- that already used migration number 327. The migration runner keys applied
-- migrations by the full filename stem, so those databases replay this DDL
-- under the new 451 stem. Keep the replay idempotent so existing exact
-- allow-lists survive the upgrade.
ALTER TABLE agent
ADD COLUMN IF NOT EXISTS enabled_builtin_skill_ids TEXT[];

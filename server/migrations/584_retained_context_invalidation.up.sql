-- A same-task provider fallback must not retain authority borrowed from the
-- abandoned context. Only the daemon's authenticated reset transition sets it.
ALTER TABLE agent_task_queue ADD COLUMN retained_context_invalidated boolean NOT NULL DEFAULT false;

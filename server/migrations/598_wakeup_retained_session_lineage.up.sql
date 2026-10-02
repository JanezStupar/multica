-- Claim delivery records the exact retained source of an authenticated wakeup.
ALTER TABLE agent_task_queue ADD COLUMN wakeup_resume_from_task_id uuid;

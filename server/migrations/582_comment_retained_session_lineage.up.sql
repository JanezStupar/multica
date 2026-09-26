-- Written only by the server's claim-delivery gate after selecting an exact
-- retained session. Client task context is not continuation authority.
ALTER TABLE agent_task_queue ADD COLUMN comment_resume_from_task_id uuid;

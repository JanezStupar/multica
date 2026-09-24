-- Status updates can race a cutover after a queued-task claim read its snapshot.
-- Take a parent issue lock in the writer transaction. Cutover first obtains a
-- NOWAIT task-table lock, so a crossing update either finishes before freezing
-- or fails immediately; no frozen task can become dispatched or fail by expiry.
CREATE FUNCTION guard_frozen_issue_task_status() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    frozen boolean;
BEGIN
    IF NEW.status = OLD.status OR NEW.issue_id IS NULL THEN
        RETURN NEW;
    END IF;
    -- NOWAIT avoids a task-row -> issue-row deadlock with deletion and handoff,
    -- which lock the issue before touching tasks. Callers retry lock contention.
    SELECT workflow_frozen INTO frozen FROM issue WHERE id=NEW.issue_id FOR SHARE NOWAIT;
    IF frozen AND NEW.status <> 'cancelled' THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_frozen',
            MESSAGE='issue is frozen until explicit workflow migration';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER agent_task_workflow_frozen_status
BEFORE UPDATE OF status ON agent_task_queue
FOR EACH ROW EXECUTE FUNCTION guard_frozen_issue_task_status();

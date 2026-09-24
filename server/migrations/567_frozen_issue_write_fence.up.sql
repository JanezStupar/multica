-- A frozen issue has no task-driving edits until explicit migration. Database
-- row locks close the race between an API precheck and workspace cutover.
CREATE FUNCTION guard_frozen_issue_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.workflow_frozen THEN
	    -- Owner-authorized teardown clears only links within its locked workspace.
	    IF current_setting('multica.workflow_teardown_workspace_id', true) = OLD.workspace_id::text
	       AND NEW.parent_issue_id IS NULL
	       AND (to_jsonb(NEW) - 'parent_issue_id') = (to_jsonb(OLD) - 'parent_issue_id') THEN
	        RETURN NEW;
	    END IF;
	    -- Issue deletion detaches direct children after locking the parent.
	    IF current_setting('multica.issue_delete_parent_id', true) = OLD.parent_issue_id::text
	       AND NEW.parent_issue_id IS NULL AND NEW.stage IS NULL
	       AND NEW.revision = OLD.revision + 1
	       AND (to_jsonb(NEW) - ARRAY['parent_issue_id','stage','revision','updated_at','last_activity_at']) =
	           (to_jsonb(OLD) - ARRAY['parent_issue_id','stage','revision','updated_at','last_activity_at']) THEN
	        RETURN NEW;
	    END IF;
	    -- Reaction counters and activity timestamps cannot change the objective.
	    IF (to_jsonb(NEW) - ARRAY['revision','updated_at','last_activity_at']) =
	       (to_jsonb(OLD) - ARRAY['revision','updated_at','last_activity_at']) THEN
	        RETURN NEW;
	    END IF;
        IF NEW.workflow_frozen = false
           AND current_setting('multica.workflow_migration', true) = 'on'
           AND NEW.workflow_policy IS NOT NULL
           AND NEW.workflow_migrated_at IS NOT NULL
           AND NEW.id = OLD.id AND NEW.workspace_id = OLD.workspace_id THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_frozen',
            MESSAGE='issue is frozen until explicit workflow migration';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER issue_workflow_frozen_update
BEFORE UPDATE ON issue
FOR EACH ROW EXECUTE FUNCTION guard_frozen_issue_update();

-- Comment content and replies are part of a frozen objective. Lock the issue
-- in the writer's transaction so an insertion racing cutover lands before the
-- freeze or waits and is rejected after it. Migration may only settle the
-- old delegated-failure outbox marker; its comment evidence stays intact.
CREATE FUNCTION guard_frozen_issue_comment() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    parent_id uuid;
    frozen boolean;
BEGIN
    IF TG_OP = 'DELETE'
       AND current_setting('multica.workflow_teardown_workspace_id', true) = OLD.workspace_id::text THEN
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE'
       AND current_setting('multica.workflow_teardown_workspace_id', true) = OLD.workspace_id::text
       AND NEW.parent_id IS NULL
       AND (to_jsonb(NEW) - 'parent_id') = (to_jsonb(OLD) - 'parent_id') THEN
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN parent_id := OLD.issue_id;
    ELSE parent_id := NEW.issue_id; END IF;
    -- Comment writes otherwise hold a comment row while waiting for an issue
    -- row, opposite to issue deletion's lock order. Fail for a bounded retry.
    SELECT workflow_frozen INTO frozen FROM issue WHERE id=parent_id FOR SHARE NOWAIT;
    IF frozen THEN
	    IF TG_OP = 'UPDATE'
	       AND (to_jsonb(NEW) - ARRAY['revision','updated_at']) =
	           (to_jsonb(OLD) - ARRAY['revision','updated_at']) THEN
	        RETURN NEW;
	    END IF;
        IF TG_OP = 'UPDATE'
           AND NEW.recovery_settled_at IS NOT NULL
           AND OLD.recovery_settled_at IS NULL
           AND (to_jsonb(NEW) - 'recovery_settled_at') =
               (to_jsonb(OLD) - 'recovery_settled_at') THEN
            RETURN NEW;
        END IF;
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_frozen',
            MESSAGE='issue is frozen until explicit workflow migration';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER issue_workflow_frozen_comment
BEFORE INSERT OR UPDATE OR DELETE ON comment
FOR EACH ROW EXECUTE FUNCTION guard_frozen_issue_comment();

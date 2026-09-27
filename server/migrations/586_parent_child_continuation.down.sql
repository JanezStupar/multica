DROP TRIGGER capture_parent_child_completion ON issue;
DROP FUNCTION capture_parent_child_completion_trigger();
DROP FUNCTION capture_parent_child_completion(uuid);
DELETE FROM issue_wakeup_receipt WHERE wakeup_id IN (SELECT id FROM issue_wakeup WHERE child_issue_id IS NOT NULL);
UPDATE agent_task_queue SET status='cancelled',completed_at=now(),error='Parent continuation disabled by migration rollback'
 WHERE status IN ('queued','deferred') AND context->>'wakeup_id' IN (SELECT id::text FROM issue_wakeup WHERE child_issue_id IS NOT NULL);
DELETE FROM issue_wakeup WHERE child_issue_id IS NOT NULL;
CREATE OR REPLACE FUNCTION guard_issue_wakeup_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT NEW.enabled THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND OLD.enabled AND OLD.issue_id=NEW.issue_id AND OLD.workspace_id=NEW.workspace_id THEN RETURN NEW; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('issue-wakeup-capacity:'||NEW.workspace_id::text,0));
 IF (SELECT count(*) FROM (SELECT 1 FROM issue_wakeup WHERE workspace_id=NEW.workspace_id AND issue_id=NEW.issue_id AND enabled AND id<>NEW.id LIMIT 32) slots)>=32 THEN
  RAISE EXCEPTION 'An issue can have at most 32 enabled wakeups' USING ERRCODE='23514',CONSTRAINT='issue_wakeup_active_limit';
 END IF;
 IF (SELECT count(*) FROM (SELECT 1 FROM issue_wakeup WHERE workspace_id=NEW.workspace_id AND enabled AND id<>NEW.id LIMIT 1000) slots)>=1000 THEN
  RAISE EXCEPTION 'A workspace can have at most 1000 enabled wakeups' USING ERRCODE='23514',CONSTRAINT='issue_wakeup_active_limit';
 END IF;
 RETURN NEW;
END $$;

ALTER TABLE issue_wakeup DROP COLUMN child_completion_revision;
ALTER TABLE issue_wakeup DROP COLUMN child_issue_id;

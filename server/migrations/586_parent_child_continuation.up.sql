-- Child completion is a durable input to the retained parent, not an implicit
-- authorization for installation, live qualification or other privileged work.
ALTER TABLE issue_wakeup ADD COLUMN child_issue_id uuid;
ALTER TABLE issue_wakeup ADD COLUMN child_completion_revision bigint NOT NULL DEFAULT 0;

-- Internal completion inputs do not consume user subscription slots or make
-- closing a child fail merely because the parent has configured 32 rules.
CREATE OR REPLACE FUNCTION guard_issue_wakeup_capacity() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT NEW.enabled OR NEW.child_issue_id IS NOT NULL THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND OLD.enabled AND OLD.issue_id=NEW.issue_id AND OLD.workspace_id=NEW.workspace_id THEN RETURN NEW; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('issue-wakeup-capacity:'||NEW.workspace_id::text,0));
 IF (SELECT count(*) FROM (SELECT 1 FROM issue_wakeup WHERE child_issue_id IS NULL AND workspace_id=NEW.workspace_id AND issue_id=NEW.issue_id AND enabled AND id<>NEW.id LIMIT 32) slots)>=32 THEN
  RAISE EXCEPTION 'An issue can have at most 32 enabled wakeups' USING ERRCODE='23514',CONSTRAINT='issue_wakeup_active_limit';
 END IF;
 IF (SELECT count(*) FROM (SELECT 1 FROM issue_wakeup WHERE child_issue_id IS NULL AND workspace_id=NEW.workspace_id AND enabled AND id<>NEW.id LIMIT 1000) slots)>=1000 THEN
  RAISE EXCEPTION 'A workspace can have at most 1000 enabled wakeups' USING ERRCODE='23514',CONSTRAINT='issue_wakeup_active_limit';
 END IF;
 RETURN NEW;
END $$;

CREATE FUNCTION capture_parent_child_completion(p_child uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE child issue; parent issue; source agent_task_queue; existing issue_wakeup;
 wake_id uuid; completion_key text;
BEGIN
 SELECT * INTO child FROM issue WHERE id=p_child;
 IF child.parent_issue_id IS NULL OR (child.status NOT IN ('done','cancelled') AND NOT EXISTS (
  SELECT 1 FROM issue_status s WHERE s.workspace_id=child.workspace_id AND s.key=child.status AND s.category IN ('done','closed')
 )) THEN RETURN; END IF;
 SELECT * INTO parent FROM issue WHERE id=child.parent_issue_id AND workspace_id=child.workspace_id;
 IF parent.id IS NULL OR parent.workflow_frozen OR parent.assignee_type IS DISTINCT FROM 'agent'
  OR parent.assignee_id IS NULL OR NOT EXISTS (
   SELECT 1 FROM jsonb_array_elements(COALESCE(parent.workflow_policy->'bundle'->'files','[]'::jsonb)) f
   WHERE CASE WHEN f->>'path'='runtime/policy.json' THEN (f->>'content')::jsonb->>'format_version'='2' ELSE false END
  )
  OR parent.status IN ('done','cancelled') OR EXISTS (SELECT 1 FROM issue_status s WHERE s.workspace_id=parent.workspace_id AND s.key=parent.status AND s.category IN ('done','closed'))
 THEN RETURN; END IF;
 -- Retain the invoking human, never substitute the accountable user for
 -- permission. Runtime and current membership are checked again on dispatch.
 SELECT * INTO source FROM agent_task_queue t WHERE t.issue_id=parent.id
  AND t.agent_id=parent.assignee_id AND t.originator_user_id IS NOT NULL
  ORDER BY t.created_at DESC,t.id DESC LIMIT 1;
 IF source.id IS NULL THEN RETURN; END IF;
 wake_id := md5('multica-parent-child-completion:'||parent.id::text||':'||child.id::text)::uuid;
 SELECT * INTO existing FROM issue_wakeup WHERE id=wake_id FOR UPDATE;
 IF existing.id IS NOT NULL AND existing.child_completion_revision>0 THEN RETURN; END IF;
 IF existing.id IS NOT NULL AND existing.agent_id IS DISTINCT FROM parent.assignee_id THEN
  UPDATE agent_task_queue SET status='cancelled',completed_at=now(),error='Parent reassigned before child continuation'
   WHERE context->>'wakeup_id'=wake_id::text AND status IN ('queued','deferred');
 END IF;
 INSERT INTO issue_wakeup(id,workspace_id,issue_id,agent_id,created_by,source_task_id,instruction,
  kind,mode,event_types,child_issue_id,child_completion_revision)
 VALUES(wake_id,parent.workspace_id,parent.id,parent.assignee_id,source.originator_user_id,source.id,
  'A delegated child reached a terminal state. Read its result and linked evidence, reconcile intervening parent changes, and continue the existing parent scope in retained context. Completion is a fact, not permission for privileged work or live qualification; report any remaining prerequisite without inventing another approval.',
  'event','continuous',ARRAY['child.terminated'],child.id,child.revision)
 ON CONFLICT(id) DO UPDATE SET agent_id=EXCLUDED.agent_id,created_by=EXCLUDED.created_by,
  source_task_id=EXCLUDED.source_task_id,child_completion_revision=EXCLUDED.child_completion_revision,
  enabled=true,disabled_at=NULL,updated_at=clock_timestamp();
 completion_key := child.id::text||':'||child.revision::text;
 INSERT INTO issue_wakeup_receipt(id,wakeup_id,revision,event_key,event_type,payload)
 SELECT gen_random_uuid(),w.id,w.revision,completion_key,'child.terminated',jsonb_build_object(
  'event_id',completion_key,'event_type','child.terminated','version',1,'occurred_at',clock_timestamp(),
  'workspace_id',parent.workspace_id,'issue_id',parent.id,'child_issue_id',child.id,
  'child_number',child.number,'child_status',child.status,'child_revision',child.revision)
 FROM issue_wakeup w WHERE w.id=wake_id
 ON CONFLICT(wakeup_id,revision,event_key) DO NOTHING;
END $$;

CREATE FUNCTION capture_parent_child_completion_trigger() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' OR OLD.status IS DISTINCT FROM NEW.status THEN
  IF NEW.status NOT IN ('done','cancelled') AND NOT EXISTS (
   SELECT 1 FROM issue_status s WHERE s.workspace_id=NEW.workspace_id AND s.key=NEW.status AND s.category IN ('done','closed')
  ) THEN
   -- A later genuine terminal transition may be new input. Editorial saves
   -- on a still-terminal child never rearm an already handled completion.
   UPDATE issue_wakeup SET child_completion_revision=0 WHERE child_issue_id=NEW.id;
  ELSE
   PERFORM capture_parent_child_completion(NEW.id);
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER capture_parent_child_completion AFTER INSERT OR UPDATE OF status ON issue
 FOR EACH ROW EXECUTE FUNCTION capture_parent_child_completion_trigger();

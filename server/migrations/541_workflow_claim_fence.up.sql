-- Handoffs use the native queue. This predicate is an inexpensive queue filter;
-- the claim transaction also locks the enrolled issue and checks again in a
-- new statement before publishing a claim, to exclude cross-agent races.
-- Migration 545 may replace this helper to freeze claims during cutover.
CREATE OR REPLACE FUNCTION workflow_issue_executable(candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$;

-- Acceptance tables arrive in migration 547. A later migration replaces this
-- no-op helper without making fresh installs depend on a future table.
CREATE OR REPLACE FUNCTION workflow_acceptance_claimable(candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$;

-- Migration 566 adds an explicit completion marker for member handoffs.
CREATE OR REPLACE FUNCTION workflow_handoff_claimable(candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT true $$;

CREATE OR REPLACE FUNCTION workflow_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 WITH RECURSIVE
 enrolled AS (
   SELECT id FROM issue WHERE id=candidate_issue AND workflow_policy IS NOT NULL
 ),
 handoff AS (
   SELECT w.id,w.agent_id,w.filter_task_id,w.last_task_id,w.handoff FROM issue_wakeup w
   WHERE w.issue_id=candidate_issue AND w.handoff IS NOT NULL
     AND w.disabled_at IS NULL
   ORDER BY w.id DESC LIMIT 1
 ),
 lineage AS (
   SELECT t.id,t.status,t.agent_id,t.created_at FROM agent_task_queue t, handoff h
   WHERE t.issue_id=candidate_issue AND t.id=COALESCE(h.last_task_id,h.filter_task_id)
   UNION
   SELECT child.id,child.status,child.agent_id,child.created_at FROM agent_task_queue child JOIN lineage parent
     ON (child.retry_of_task_id=parent.id OR child.rerun_of_task_id=parent.id)
       AND child.agent_id=parent.agent_id
   WHERE child.issue_id=candidate_issue
 )
 SELECT workflow_issue_executable(candidate_issue) AND workflow_acceptance_claimable(candidate_issue)
   AND workflow_handoff_claimable(candidate_issue)
   AND (NOT EXISTS(SELECT 1 FROM enrolled) OR (
   NOT EXISTS (
     SELECT 1 FROM agent_task_queue active WHERE active.issue_id=candidate_issue
       AND active.id<>candidate_id
       AND active.status IN ('dispatched','running','waiting_local_directory')
   ) AND (
     NOT EXISTS(SELECT 1 FROM handoff)
     OR EXISTS(SELECT 1 FROM lineage WHERE id=candidate_id)
     -- A final failed handoff recipient may wake only its server-created
     -- delegated-failure recovery task. Its source is the exact outgoing
     -- coordinator task, not an arbitrary queued task on the same issue.
     OR EXISTS (
       SELECT 1 FROM handoff h
       JOIN agent_task_queue recovery ON recovery.id=candidate_id
         AND recovery.issue_id=candidate_issue
       JOIN agent_task_queue failed ON failed.id=recovery.delegated_from_task_id
         AND failed.id=recovery.trigger_evidence_ref_id
       JOIN lineage recipient ON recipient.id=failed.id
       JOIN agent_task_queue source ON source.id=failed.delegated_from_task_id
         AND source.id=h.filter_task_id
       JOIN comment signal ON signal.source_task_id=failed.id
         AND signal.issue_id=candidate_issue AND signal.author_type='system'
         AND signal.type='progress_update' AND signal.deleted_at IS NULL
         AND (signal.id=recovery.trigger_comment_id OR signal.id=ANY(recovery.coalesced_comment_ids))
       JOIN issue current_issue ON current_issue.id=candidate_issue
       WHERE h.last_task_id IS NOT NULL
         AND recovery.trigger_evidence_kind='delegated_failure'
         AND recovery.context->'workflow_recovery'->>'handoff_id'=h.id::text
         AND recovery.context->'workflow_recovery'->>'failed_task_id'=failed.id::text
         AND recovery.context->'workflow_recovery'->>'resume_task_id'=source.id::text
         AND recovery.agent_id=source.agent_id
         AND source.issue_id=candidate_issue AND source.status='completed'
         AND failed.issue_id=candidate_issue AND failed.agent_id=h.agent_id
         AND failed.status='failed'
         AND current_issue.assignee_type='agent' AND current_issue.assignee_id=failed.agent_id
         AND current_issue.status=h.handoff->>'status'
         AND NOT EXISTS (SELECT 1 FROM agent_task_queue retry
           WHERE retry.parent_task_id=failed.id AND retry.status<>'cancelled')
     )
     OR (EXISTS(SELECT 1 FROM handoff WHERE last_task_id IS NOT NULL)
         AND (SELECT status='completed' FROM lineage ORDER BY created_at DESC,id DESC LIMIT 1)
         AND NOT EXISTS(SELECT 1 FROM lineage WHERE status IN
           ('queued','deferred','dispatched','running','waiting_local_directory')))
   ))
 );
$$;

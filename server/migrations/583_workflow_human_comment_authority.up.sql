-- Conversation obligations are mutable queue bookkeeping, separate from the
-- immutable authority snapshot that records the acceptance decision.
ALTER TABLE issue_workflow_acceptance ADD COLUMN human_comment_obligations jsonb NOT NULL DEFAULT '[]'::jsonb;

-- Completed member handoffs remain conversation evidence while an accepted
-- candidate still has delivery or outcome work. Disabled or superseded handoffs
-- never regain authority; acceptance preserves the exact current completed one.
CREATE FUNCTION workflow_human_comment_handoff_current(candidate_issue uuid, handoff_id uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM issue i
   JOIN issue_workflow_candidate candidate ON candidate.id=i.workflow_candidate_id
     AND candidate.issue_id=i.id AND candidate.workspace_id=i.workspace_id
   JOIN issue_wakeup w ON w.id=handoff_id AND w.issue_id=i.id AND w.workspace_id=i.workspace_id
   JOIN agent_task_queue source ON source.id=w.filter_task_id AND source.issue_id=i.id
     AND source.agent_id=w.agent_id
   WHERE i.id=candidate_issue AND i.workflow_policy IS NOT NULL AND NOT i.workflow_frozen
     AND EXISTS (SELECT 1 FROM issue_status status WHERE status.workspace_id=i.workspace_id
       AND status.key=i.status AND (status.category IN ('unstarted','started')
         OR (status.category='done' AND EXISTS (SELECT 1 FROM issue_workflow_acceptance accepted
           WHERE accepted.issue_id=i.id AND accepted.workspace_id=i.workspace_id AND accepted.candidate_id=candidate.id
             AND accepted.state='accepted' AND accepted.revoked_at IS NULL AND accepted.policy_version=candidate.policy_version
             AND accepted.authority_snapshot->>'scope_digest'=candidate.scope_digest))))
     AND candidate.policy_version=i.workflow_policy->>'version'
     AND i.assignee_type='member' AND i.assignee_id IS NOT NULL
     AND w.handoff IS NOT NULL AND w.disabled_at IS NULL
     AND w.id=(SELECT latest.id FROM issue_wakeup latest WHERE latest.issue_id=i.id
       AND latest.workspace_id=i.workspace_id AND latest.handoff IS NOT NULL ORDER BY latest.id DESC LIMIT 1)
     AND w.last_task_id IS NULL AND w.handoff_completed_at IS NOT NULL
     AND w.source_task_id=source.id AND w.filter_agent_id=source.agent_id
     AND w.handoff->>'assignee_type'='member' AND w.handoff->>'assignee_id'=i.assignee_id::text
     AND w.handoff->>'outgoing_task_id'=source.id::text AND source.status='completed'

 );
$$;

-- New implicit conversations require an unfinished candidate. Existing exact
-- tasks use the current-lineage helper instead, so external completion cannot
-- erase an already-promised answer.
CREATE FUNCTION workflow_human_comment_handoff_eligible(candidate_issue uuid, handoff_id uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM issue i JOIN issue_workflow_candidate candidate ON candidate.id=i.workflow_candidate_id
   WHERE i.id=candidate_issue AND workflow_human_comment_handoff_current(i.id,handoff_id)
     AND EXISTS (SELECT 1 FROM issue_status status WHERE status.workspace_id=i.workspace_id
       AND status.key=i.status AND status.category IN ('unstarted','started'))
     AND NOT EXISTS (SELECT 1 FROM issue_workflow_acceptance a
       WHERE a.workspace_id=i.workspace_id AND a.issue_id=i.id AND a.candidate_id=candidate.id
         AND a.state='accepted' AND a.revoked_at IS NULL
         AND NOT (a.completion_version=2 AND a.policy_version=candidate.policy_version
           AND a.accepted_status_key=i.status AND a.authority_snapshot->>'scope_digest'=candidate.scope_digest
           AND (NOT a.outcome_complete OR EXISTS (SELECT 1 FROM issue_workflow_delivery d
             WHERE d.acceptance_id=a.id AND (d.status<>'delivered' OR d.merged_at IS NULL)))))
 );
$$;

-- The comment writer records these exact assigned-agent recipients under the
-- issue lock. A prior promise may still be answered after external completion.
CREATE FUNCTION workflow_accepted_comment_input_recorded(candidate_issue uuid, recipient_agent uuid, input_comment uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM issue i
   JOIN issue_workflow_candidate candidate ON candidate.id=i.workflow_candidate_id
     AND candidate.issue_id=i.id AND candidate.workspace_id=i.workspace_id
   JOIN issue_workflow_acceptance a ON a.issue_id=i.id AND a.workspace_id=i.workspace_id
     AND a.candidate_id=candidate.id
   JOIN comment c ON c.id=input_comment AND c.issue_id=i.id AND c.workspace_id=i.workspace_id
   JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=c.author_id
   WHERE i.id=candidate_issue AND NOT i.workflow_frozen AND i.assignee_type='agent' AND i.assignee_id=recipient_agent
     AND a.state='accepted' AND a.revoked_at IS NULL AND a.policy_version=i.workflow_policy->>'version'
     AND candidate.policy_version=a.policy_version AND a.authority_snapshot->>'scope_digest'=candidate.scope_digest
     AND EXISTS (SELECT 1 FROM issue_status status WHERE status.workspace_id=i.workspace_id
       AND status.key=i.status AND status.category IN ('unstarted','started','done'))
     AND a.human_comment_obligations @> jsonb_build_array(jsonb_build_object('comment_id',c.id::text,'agent_id',recipient_agent::text))
     AND c.author_type='member' AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
     AND c.created_at>candidate.created_at AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
 );
$$;

-- Recorded recipient intent is shared by crash recovery and all conversation
-- task claims. Only the exact planned input remains pending, and a completed
-- receipt covers the input version that was actually dispatched.
CREATE FUNCTION workflow_recorded_comment_input_current(candidate_issue uuid, recipient_agent uuid, input_comment uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM issue i JOIN comment c ON c.id=input_comment AND c.issue_id=i.id AND c.workspace_id=i.workspace_id
  JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=c.author_id
  JOIN issue_workflow_candidate candidate ON candidate.id=i.workflow_candidate_id AND candidate.issue_id=i.id AND candidate.workspace_id=i.workspace_id
  JOIN agent active ON active.id=recipient_agent AND active.workspace_id=i.workspace_id
  WHERE i.id=candidate_issue AND NOT i.workflow_frozen AND active.archived_at IS NULL AND active.runtime_id IS NOT NULL
   AND candidate.policy_version=i.workflow_policy->>'version'
   AND c.author_type='member' AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
   AND c.created_at>candidate.created_at AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
   AND NOT EXISTS(SELECT 1 FROM agent_task_queue covered WHERE covered.issue_id=i.id AND covered.agent_id=recipient_agent
     AND covered.status='completed' AND c.id=ANY(covered.delivered_comment_ids) AND covered.dispatched_at>=c.updated_at)
   AND (workflow_accepted_comment_input_recorded(i.id,recipient_agent,c.id)
    OR EXISTS(SELECT 1 FROM issue_wakeup handoff JOIN agent_task_queue source ON source.id=handoff.filter_task_id
      WHERE handoff.issue_id=i.id AND handoff.workspace_id=i.workspace_id AND source.agent_id=recipient_agent
       -- A completed handoff identifies the coordinator across runtime moves.
       -- Each new task must use the current runtime; session reuse is separate.
       AND workflow_human_comment_handoff_current(i.id,handoff.id)
       AND c.id=ANY(source.coalesced_comment_ids)
       AND NOT (c.id=ANY(source.delivered_comment_ids) AND source.dispatched_at>=c.updated_at)
       AND (c.author_id=i.assignee_id OR m.role IN ('owner','admin')))
    OR EXISTS(SELECT 1 FROM issue_workflow_acceptance a JOIN agent_task_queue source ON source.id=a.source_task_id
      JOIN issue_workflow_profile profile ON profile.id=source.workflow_profile_id
       AND profile.issue_id=i.id AND profile.workspace_id=i.workspace_id AND profile.agent_id=recipient_agent
      WHERE a.issue_id=i.id AND a.workspace_id=i.workspace_id AND a.candidate_id=candidate.id
       AND a.state='requested' AND a.revoked_at IS NULL AND a.actor_type='agent' AND a.actor_id=recipient_agent
       AND i.assignee_type='agent' AND i.assignee_id=recipient_agent
       AND source.issue_id=i.id AND source.agent_id=recipient_agent AND source.status IN ('dispatched','running','waiting_local_directory','completed')
       AND a.policy_version=candidate.policy_version AND a.authority_snapshot->>'scope_digest'=candidate.scope_digest
       AND source.workflow_policy_version=a.policy_version AND profile.policy_version=a.policy_version
       AND a.authority_snapshot->>'source_task_workflow_profile_id'=source.workflow_profile_id::text
       AND a.authority_snapshot->>'source_task_workflow_policy_version'=source.workflow_policy_version
       AND a.authority_snapshot ? 'selected_workflow_profile_id'
       AND a.authority_snapshot->>'selected_workflow_profile_id' IS NOT DISTINCT FROM
        (SELECT selection.id::text FROM issue_workflow_profile selection WHERE selection.issue_id=i.id
          AND selection.agent_id=recipient_agent AND selection.workspace_id=i.workspace_id
          AND selection.policy_version=a.policy_version ORDER BY selection.revision DESC LIMIT 1)
       AND EXISTS(SELECT 1 FROM issue_status status WHERE status.workspace_id=i.workspace_id AND status.key=i.status AND status.category IN ('unstarted','started'))
       AND c.id=ANY(source.coalesced_comment_ids)
       AND NOT (c.id=ANY(source.delivered_comment_ids) AND source.dispatched_at>=c.updated_at)))
 );
$$;

CREATE FUNCTION workflow_human_comment_task_current(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM agent_task_queue t
   JOIN issue i ON i.id=t.issue_id
   JOIN issue_workflow_candidate candidate ON candidate.id=i.workflow_candidate_id AND candidate.issue_id=i.id
   JOIN issue_wakeup w ON w.id=t.trigger_evidence_ref_id
   JOIN agent_task_queue source ON source.id=w.filter_task_id AND source.issue_id=i.id
     AND source.agent_id=w.agent_id
   JOIN comment c ON (c.id=t.trigger_comment_id OR c.id=ANY(t.coalesced_comment_ids))
     AND c.issue_id=i.id AND c.workspace_id=i.workspace_id
   JOIN member m ON m.workspace_id=i.workspace_id AND m.user_id=c.author_id
   WHERE t.id=$1 AND i.id=$2
     AND workflow_human_comment_handoff_current(i.id,w.id)
     AND workflow_recorded_comment_input_current(i.id,t.agent_id,c.id)
     AND t.agent_id=source.agent_id AND t.delegated_from_task_id=source.id
     AND t.trigger_evidence_kind='workflow_human_comment'
     AND t.context->'workflow_feedback'->>'candidate_id'=candidate.id::text
     AND t.context->'workflow_feedback'->>'handoff_id'=w.id::text
     AND t.context->'workflow_feedback'->>'coordinator_task_id'=source.id::text
     AND (t.context->'workflow_feedback'->>'comment_id'=t.trigger_comment_id::text
       OR (t.trigger_comment_id IS NULL AND t.context->'workflow_feedback'->>'comment_id'=
         t.context->'workflow_comment_obligation'->>'comment_id'))
     AND c.author_type='member' AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
     AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
     AND c.created_at>candidate.created_at
     AND t.originator_user_id=c.author_id AND t.accountable_user_id=c.author_id
     AND (i.assignee_id=c.author_id OR m.role IN ('owner','admin'))
 );
$$;

CREATE OR REPLACE FUNCTION workflow_human_comment_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT workflow_human_comment_task_current(candidate_id,candidate_issue)
   AND NOT EXISTS (SELECT 1 FROM agent_task_queue active WHERE active.issue_id=candidate_issue
     AND active.id<>candidate_id AND active.status IN ('dispatched','running','waiting_local_directory'));
$$;

CREATE FUNCTION workflow_accepted_comment_task_current(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS (
   SELECT 1 FROM agent_task_queue t
   JOIN comment c ON (c.id=t.trigger_comment_id OR c.id=ANY(t.coalesced_comment_ids)) AND c.issue_id=t.issue_id
   WHERE t.id=$1 AND t.issue_id=$2
     AND t.originator_user_id=c.author_id AND t.accountable_user_id=c.author_id
     AND workflow_accepted_comment_input_recorded(t.issue_id,t.agent_id,c.id)
     AND workflow_recorded_comment_input_current(t.issue_id,t.agent_id,c.id)
 );
$$;

-- Queue admission repeats current intent under the owner/issue lock. A
-- recorded old candidate or withdrawn route cannot win a recovery race.
CREATE FUNCTION workflow_comment_enqueue_allowed(candidate_issue uuid, recipient_agent uuid, input_comment uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$
DECLARE queue_runtime uuid;
BEGIN
 IF input_comment IS NULL OR NOT EXISTS(SELECT 1 FROM issue WHERE id=candidate_issue AND workflow_policy IS NOT NULL) THEN RETURN true; END IF;
 SELECT runtime_id INTO queue_runtime FROM agent WHERE id=recipient_agent;
 IF queue_runtime IS NULL OR NOT lock_task_owner_rows(recipient_agent,candidate_issue,queue_runtime) THEN RETURN false; END IF;
 -- This statement receives a fresh snapshot after the owner/issue lock wait.
 -- A stable admission predicate in the outer INSERT/UPDATE snapshot could
 -- otherwise read a recipient proof that was withdrawn while it waited.
 RETURN workflow_recorded_comment_input_current(candidate_issue,recipient_agent,input_comment)
 OR (NOT EXISTS(SELECT 1 FROM issue_workflow_acceptance a JOIN issue i ON i.id=a.issue_id AND i.workspace_id=a.workspace_id
      WHERE i.id=candidate_issue AND a.candidate_id=i.workflow_candidate_id AND a.state IN ('requested','accepted') AND a.revoked_at IS NULL)
  AND NOT EXISTS(SELECT 1 FROM issue_workflow_acceptance a WHERE a.issue_id=candidate_issue
    AND (a.human_comment_obligations @> jsonb_build_array(jsonb_build_object('comment_id',input_comment::text))
      OR EXISTS(SELECT 1 FROM agent_task_queue source WHERE source.id=a.source_task_id AND input_comment=ANY(source.coalesced_comment_ids))))
  AND NOT EXISTS(SELECT 1 FROM issue_wakeup handoff JOIN agent_task_queue source ON source.id=handoff.filter_task_id
    WHERE handoff.issue_id=candidate_issue AND handoff.handoff IS NOT NULL AND input_comment=ANY(source.coalesced_comment_ids)));
END;
$$;

-- Immutable task birth evidence survives explicit withdrawal/revocation only
-- to recognize a promised-conversation rerun and refuse stale authority.
CREATE FUNCTION workflow_comment_obligation_context(candidate_issue uuid, recipient_agent uuid, input_comment uuid)
RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_strip_nulls(jsonb_build_object('candidate_id',i.workflow_candidate_id::text,
  'recipient_agent_id',recipient_agent::text,'comment_id',input_comment::text,
  'acceptance_id',(SELECT a.id::text FROM issue_workflow_acceptance a WHERE a.issue_id=i.id AND a.workspace_id=i.workspace_id
    AND a.candidate_id=i.workflow_candidate_id AND a.state IN ('requested','accepted') AND a.revoked_at IS NULL ORDER BY a.id DESC LIMIT 1),
  'source_task_id',COALESCE((SELECT a.source_task_id::text FROM issue_workflow_acceptance a WHERE a.issue_id=i.id
    AND a.workspace_id=i.workspace_id AND a.candidate_id=i.workflow_candidate_id AND a.state='requested' AND a.revoked_at IS NULL),
    (SELECT handoff.filter_task_id::text FROM issue_wakeup handoff WHERE handoff.issue_id=i.id
      AND workflow_human_comment_handoff_current(i.id,handoff.id)))))
 FROM issue i WHERE i.id=candidate_issue AND workflow_recorded_comment_input_current(i.id,recipient_agent,input_comment);
$$;

-- A requested approval pauses ordinary mutations, but not the exact human
-- input its completed source promised to answer before that approval finalizes.
CREATE FUNCTION workflow_requested_comment_task_current(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM agent_task_queue t JOIN issue i ON i.id=t.issue_id
  JOIN issue_workflow_acceptance a ON a.issue_id=i.id AND a.workspace_id=i.workspace_id
   AND a.candidate_id=i.workflow_candidate_id AND a.state='requested' AND a.revoked_at IS NULL
  JOIN agent_task_queue source ON source.id=a.source_task_id AND source.issue_id=i.id AND source.agent_id=t.agent_id
  JOIN comment c ON (c.id=t.trigger_comment_id OR c.id=ANY(t.coalesced_comment_ids)) AND c.id=ANY(source.coalesced_comment_ids)
  WHERE t.id=$1 AND i.id=$2 AND source.id<>t.id
   AND a.actor_type='agent' AND a.actor_id=t.agent_id AND source.status='completed'
   AND workflow_recorded_comment_input_current(i.id,t.agent_id,c.id)
   AND t.originator_user_id=c.author_id AND t.accountable_user_id=c.author_id
   AND (t.workflow_profile_id IS NULL OR EXISTS(SELECT 1 FROM issue_workflow_profile retained
     WHERE retained.id=t.workflow_profile_id AND retained.issue_id=i.id AND retained.workspace_id=i.workspace_id
      AND retained.agent_id=t.agent_id AND retained.policy_version=a.policy_version))
 );
$$;

CREATE OR REPLACE FUNCTION workflow_task_claimable(candidate_id uuid, candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT workflow_human_comment_task_claimable(candidate_id,candidate_issue)
     OR (workflow_requested_comment_task_current(candidate_id,candidate_issue)
       AND NOT EXISTS (SELECT 1 FROM agent_task_queue active WHERE active.issue_id=candidate_issue
         AND active.id<>candidate_id AND active.status IN ('dispatched','running','waiting_local_directory')))
     OR (workflow_accepted_comment_task_current(candidate_id,candidate_issue)
       AND NOT EXISTS (SELECT 1 FROM agent_task_queue active WHERE active.issue_id=candidate_issue
         AND active.id<>candidate_id AND active.status IN ('dispatched','running','waiting_local_directory')))
     OR workflow_outcome_task_claimable(candidate_id,candidate_issue)
     OR workflow_regular_task_claimable(candidate_id,candidate_issue);
$$;

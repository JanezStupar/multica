CREATE OR REPLACE FUNCTION guard_issue_workflow_completion() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    old_done boolean := false;
    new_done boolean := false;
    old_terminal boolean := false;
    new_terminal boolean := false;
    explicit_legacy_reopen boolean := false;
    accepted_id uuid;
    accepted_version smallint;
    accepted_status text;
    accepted_outcome boolean;
    accepted_revision bigint;
BEGIN
    IF NEW.workflow_policy IS NULL THEN
        IF TG_OP = 'UPDATE' AND OLD.workflow_policy IS NOT NULL THEN
            RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
                MESSAGE='enrolled issue workflow policy cannot be removed';
        END IF;
        RETURN NEW;
    END IF;
    new_done := NEW.status = 'done' OR EXISTS (
        SELECT 1 FROM issue_status s WHERE s.workspace_id=NEW.workspace_id
          AND s.key=NEW.status AND s.category='done');
    new_terminal := new_done OR NEW.status = 'cancelled' OR EXISTS (
        SELECT 1 FROM issue_status s WHERE s.workspace_id=NEW.workspace_id
          AND s.key=NEW.status AND s.category='closed');
    IF TG_OP = 'INSERT' THEN
        IF new_terminal THEN
            RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
                MESSAGE='enrolled issue creation cannot start completed';
        END IF;
        RETURN NEW;
    END IF;
    IF OLD.workflow_policy IS NULL AND NEW.workflow_policy IS NOT NULL AND new_terminal THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
            MESSAGE='completed legacy issue requires explicit workflow migration';
    END IF;
    old_done := OLD.status = 'done' OR EXISTS (
        SELECT 1 FROM issue_status s WHERE s.workspace_id=OLD.workspace_id
          AND s.key=OLD.status AND s.category='done');
    old_terminal := old_done OR OLD.status = 'cancelled' OR EXISTS (
        SELECT 1 FROM issue_status s WHERE s.workspace_id=OLD.workspace_id
          AND s.key=OLD.status AND s.category='closed');
    explicit_legacy_reopen := OLD.workflow_policy IS NULL AND NEW.workflow_policy IS NOT NULL
        AND old_terminal AND NOT new_terminal AND NEW.workflow_migrated_at IS NOT NULL
        AND current_setting('multica.workflow_migration', true) = 'on';
    IF old_terminal AND NEW.workflow_policy IS DISTINCT FROM OLD.workflow_policy
       AND NOT explicit_legacy_reopen THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
            MESSAGE='accepted issue policy cannot change without explicit migration';
    END IF;
    IF NEW.workflow_candidate_id IS NOT NULL THEN
        SELECT a.completion_version,a.accepted_status_key,a.outcome_complete,a.issue_revision,a.id
          INTO accepted_version,accepted_status,accepted_outcome,accepted_revision,accepted_id
          FROM issue_workflow_acceptance a
         WHERE a.issue_id=NEW.id AND a.workspace_id=NEW.workspace_id
           AND a.candidate_id=NEW.workflow_candidate_id AND a.state='accepted'
           AND a.revoked_at IS NULL ORDER BY a.accepted_at DESC,a.id DESC LIMIT 1;
    END IF;
    IF accepted_id IS NULL AND EXISTS (
        SELECT 1 FROM jsonb_array_elements(COALESCE(NEW.workflow_policy->'bundle'->'files','[]'::jsonb)) f
        WHERE CASE WHEN f->>'path'='runtime/policy.json' THEN
          (f->>'content')::jsonb->>'accepted_status_key'=NEW.status
          ELSE false END
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
            MESSAGE='accepted status requires recorded candidate acceptance';
    END IF;
    IF NOT old_done AND new_done THEN
        IF NEW.status <> 'done' OR accepted_id IS NULL OR
           (accepted_version = 1 AND accepted_revision <> NEW.revision) OR
           (accepted_version = 2 AND (
               NOT accepted_outcome OR EXISTS (
                   SELECT 1 FROM issue_workflow_delivery d
                    WHERE d.acceptance_id=accepted_id
                      AND (d.status <> 'delivered' OR d.merged_at IS NULL)
               ) OR (SELECT count(*) FROM issue_workflow_delivery d
                      WHERE d.acceptance_id=accepted_id) <>
                      (SELECT jsonb_array_length(c.pr_set) FROM issue_workflow_candidate c
                       WHERE c.id=NEW.workflow_candidate_id AND c.issue_id=NEW.id)
           )) THEN
            RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
                MESSAGE='enrolled issue completion requires delivered candidate and outcome';
        END IF;
    ELSIF old_done AND NOT new_done AND NOT explicit_legacy_reopen THEN
        IF NOT EXISTS (
            SELECT 1 FROM issue_workflow_rejection r
            WHERE r.issue_id=NEW.id AND r.workspace_id=NEW.workspace_id
              AND r.candidate_id=OLD.workflow_candidate_id
              AND r.issue_revision=NEW.revision
        ) OR NEW.workflow_candidate_id IS NOT NULL OR EXISTS (
            SELECT 1 FROM issue_workflow_acceptance a
            WHERE a.issue_id=NEW.id AND a.workspace_id=NEW.workspace_id
              AND a.candidate_id=OLD.workflow_candidate_id
              AND a.state='accepted' AND a.revoked_at IS NULL
        ) THEN
            RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
                MESSAGE='accepted issue reopening requires recorded rejection';
        END IF;
    ELSIF old_done AND new_done AND (
        NEW.status IS DISTINCT FROM OLD.status OR NEW.title IS DISTINCT FROM OLD.title OR
        NEW.description IS DISTINCT FROM OLD.description OR
        NEW.workflow_candidate_id IS DISTINCT FROM OLD.workflow_candidate_id
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
            MESSAGE='accepted issue scope or candidate cannot change without rejection';
    END IF;
    IF accepted_version = 2 AND NOT new_done AND (
        NEW.status <> accepted_status OR
        NEW.title IS DISTINCT FROM OLD.title OR NEW.description IS DISTINCT FROM OLD.description OR
        NEW.workflow_candidate_id IS DISTINCT FROM OLD.workflow_candidate_id
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
            MESSAGE='accepted issue scope or status cannot change without invalidation';
    END IF;
    IF accepted_version = 2 AND NOT old_done AND NEW.status=accepted_status
       AND accepted_revision <> NEW.revision AND OLD.status <> accepted_status THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
            MESSAGE='accepted status requires exact revision';
    END IF;
    RETURN NEW;
END;
$$;

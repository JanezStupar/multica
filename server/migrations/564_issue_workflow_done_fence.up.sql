-- Every issue writer (single/batch HTTP, provider mirroring, task recovery,
-- creation, and internal SQL) crosses this enrolled-issue authority fence.
-- AFTER INSERT observes the workspace cutover default copied by its BEFORE
-- INSERT trigger. A rejection and its reopening share the same transaction.
CREATE FUNCTION guard_issue_workflow_completion() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    old_done boolean := false;
    new_done boolean := false;
    old_terminal boolean := false;
    new_terminal boolean := false;
    explicit_legacy_reopen boolean := false;
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
          AND s.key=NEW.status AND s.category='done'
    );
    new_terminal := new_done OR NEW.status = 'cancelled' OR EXISTS (
        SELECT 1 FROM issue_status s WHERE s.workspace_id=NEW.workspace_id
          AND s.key=NEW.status AND s.category='closed'
    );
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
          AND s.key=OLD.status AND s.category='done'
    );
    old_terminal := old_done OR OLD.status = 'cancelled' OR EXISTS (
        SELECT 1 FROM issue_status s WHERE s.workspace_id=OLD.workspace_id
          AND s.key=OLD.status AND s.category='closed'
    );
    explicit_legacy_reopen := OLD.workflow_policy IS NULL AND NEW.workflow_policy IS NOT NULL
        AND old_terminal AND NOT new_terminal AND NEW.workflow_migrated_at IS NOT NULL
        AND current_setting('multica.workflow_migration', true) = 'on';
    IF old_terminal AND NEW.workflow_policy IS DISTINCT FROM OLD.workflow_policy
       AND NOT explicit_legacy_reopen THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
            MESSAGE='accepted issue policy cannot change without explicit migration';
    END IF;
    IF NOT old_done AND new_done THEN
        IF NEW.status <> 'done' OR NEW.workflow_candidate_id IS NULL OR NOT EXISTS (
            SELECT 1 FROM issue_workflow_acceptance a
            WHERE a.issue_id=NEW.id AND a.workspace_id=NEW.workspace_id
              AND a.candidate_id=NEW.workflow_candidate_id
              AND a.issue_revision=NEW.revision AND a.state='accepted'
              AND a.revoked_at IS NULL
        ) THEN
            RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
                MESSAGE='enrolled issue completion requires exact candidate acceptance';
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
        NEW.status IS DISTINCT FROM OLD.status OR
        NEW.title IS DISTINCT FROM OLD.title OR
        NEW.description IS DISTINCT FROM OLD.description OR
        NEW.workflow_candidate_id IS DISTINCT FROM OLD.workflow_candidate_id
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='issue_workflow_authority_fence',
            MESSAGE='accepted issue scope or candidate cannot change without rejection';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER issue_workflow_authority_fence
AFTER INSERT OR UPDATE ON issue
FOR EACH ROW EXECUTE FUNCTION guard_issue_workflow_completion();

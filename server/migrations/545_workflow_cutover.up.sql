-- A workspace default is inert until the explicit cutover operation sets its
-- marker. Existing issue policy and task history are never backfilled here.
ALTER TABLE workspace ADD COLUMN workflow_default_policy jsonb;
ALTER TABLE workspace ADD COLUMN workflow_cutover_at timestamptz;
ALTER TABLE issue ADD COLUMN workflow_frozen boolean NOT NULL DEFAULT false;
ALTER TABLE issue ADD COLUMN workflow_migrated_at timestamptz;

-- All issue creation paths, including Autopilot, cross this statement-level
-- boundary. The workspace lock serializes issue creation with cutover/default
-- changes and makes each new issue inherit exactly one immutable snapshot.
CREATE FUNCTION copy_workspace_workflow_default() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    selected_policy jsonb;
    activated_at timestamptz;
BEGIN
    SELECT workflow_default_policy, workflow_cutover_at
      INTO selected_policy, activated_at
      FROM workspace WHERE id = NEW.workspace_id FOR KEY SHARE;
    IF activated_at IS NOT NULL THEN
        IF selected_policy IS NULL THEN
            RAISE EXCEPTION 'active workflow default is missing';
        END IF;
        NEW.workflow_policy := selected_policy;
        NEW.workflow_frozen := false;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER issue_workspace_workflow_default
BEFORE INSERT ON issue
FOR EACH ROW EXECUTE FUNCTION copy_workspace_workflow_default();

-- Migration 541 routes every queued claim through this helper. Reclaim and
-- StartTask have their own guards for already-dispatched task generations.
CREATE OR REPLACE FUNCTION workflow_issue_executable(candidate_issue uuid)
RETURNS boolean LANGUAGE sql STABLE AS $$
    SELECT candidate_issue IS NULL OR COALESCE((
        SELECT NOT i.workflow_frozen FROM issue i WHERE i.id = candidate_issue
    ), false);
$$;

DROP TRIGGER IF EXISTS issue_workflow_frozen_comment ON comment;
DROP FUNCTION IF EXISTS guard_frozen_issue_comment();
DROP TRIGGER IF EXISTS issue_workflow_frozen_update ON issue;
DROP FUNCTION IF EXISTS guard_frozen_issue_update();

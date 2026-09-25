ALTER TABLE issue_workflow_exception
    DROP CONSTRAINT IF EXISTS issue_workflow_exception_scope_check;

-- Rollback requires removal of external_merge grants before the old constraint
-- can be restored; keeping their audit rows makes that conflict explicit.
ALTER TABLE issue_workflow_exception
    ADD CONSTRAINT issue_workflow_exception_scope_check
    CHECK (scope IN ('review', 'acceptance', 'delivery'));

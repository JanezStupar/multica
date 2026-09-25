ALTER TABLE issue_workflow_exception
    DROP CONSTRAINT IF EXISTS issue_workflow_exception_scope_check;

ALTER TABLE issue_workflow_exception
    ADD CONSTRAINT issue_workflow_exception_scope_check
    CHECK (scope IN ('review', 'acceptance', 'delivery', 'external_merge'));

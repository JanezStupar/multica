-- Autonomous reviewed acceptance is a distinct authority route. It must not
-- be stored as trivial, because the latter is a policy classification rather
-- than a generic agent acceptance flag.
ALTER TABLE issue_workflow_acceptance
    DROP CONSTRAINT issue_workflow_acceptance_mode_check;

ALTER TABLE issue_workflow_acceptance
    ADD CONSTRAINT issue_workflow_acceptance_mode_check
    CHECK (mode IN ('human', 'trivial', 'reviewed'));

DROP TABLE IF EXISTS issue_workflow_delivery_attempt;
DROP TABLE IF EXISTS issue_workflow_delivery;
DROP TABLE IF EXISTS issue_workflow_rejection;
DROP TABLE IF EXISTS issue_workflow_acceptance;
DROP TABLE IF EXISTS issue_workflow_exception;
DROP TABLE IF EXISTS issue_workflow_review;
DROP TABLE IF EXISTS issue_workflow_candidate;
ALTER TABLE issue DROP COLUMN IF EXISTS workflow_candidate_id;

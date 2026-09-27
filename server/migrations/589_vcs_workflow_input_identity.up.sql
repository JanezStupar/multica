CREATE UNIQUE INDEX CONCURRENTLY vcs_workflow_input_identity ON vcs_workflow_input(connection_id,issue_id,event_key);

package handler

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

var workflowLedgerTables = []string{
	"issue_workflow_candidate", "issue_workflow_review", "issue_workflow_exception",
	"issue_workflow_acceptance", "issue_workflow_rejection", "issue_workflow_delivery",
	"issue_workflow_delivery_attempt",
}

func populateWorkflowLedgerForDelete(t *testing.T, workspace, issue string) {
	t.Helper()
	candidate := dbfx.Insert(t, "issue_workflow_candidate", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
		"policy_version": "delete-test", "digest": "delete-test", "scope_digest": "delete-test",
		"source_handoff_id": dbid.NewV7(), "source_task_id": dbid.NewV7(),
		"writer_task_id": dbid.NewV7(), "pr_set": testutil.Raw("'[]'::jsonb"),
	})
	dbfx.Exec(t, `UPDATE issue SET workflow_candidate_id=$2 WHERE id=$1`, issue, candidate)
	dbfx.Insert(t, "issue_workflow_review", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
		"candidate_id": candidate, "reviewer_task_id": dbid.NewV7(),
		"verdict": "pass", "pr_review_urls": testutil.Raw("'[]'::jsonb"),
	})
	dbfx.Insert(t, "issue_workflow_exception", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
		"candidate_id": candidate, "base_policy_version": "delete-test",
		"scope": "review", "grant_details": testutil.Raw("'{}'::jsonb"),
		"actor_type": "member", "actor_id": testUserID,
		"reason": "Fixture", "consequences": "Fixture",
	})
	acceptance := dbfx.Insert(t, "issue_workflow_acceptance", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
		"candidate_id": candidate, "mode": "human", "actor_type": "member",
		"actor_id": testUserID, "state": "accepted", "issue_revision": int64(1),
		"policy_version": "delete-test", "authority_snapshot": testutil.Raw("'{}'::jsonb"),
		"accepted_at": testutil.Raw("now()"),
	})
	dbfx.Insert(t, "issue_workflow_rejection", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
		"candidate_id": candidate, "acceptance_id": acceptance,
		"actor_type": "member", "actor_id": testUserID,
		"kind": "in_scope_defect", "reason": "Fixture", "issue_revision": int64(1),
	})
	delivery := dbfx.Insert(t, "issue_workflow_delivery", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
		"acceptance_id": acceptance, "candidate_id": candidate,
		"ordinal": 0, "provider": "forgejo", "provider_binding_id": dbid.NewV7(),
		"repository_url": "https://example.test/team/project",
		"pr_url":         "https://example.test/team/project/pulls/1",
		"repo_owner":     "team", "repo_name": "project", "pr_number": 1,
		"expected_head_sha": workflowDeliveryHead, "action": "ready", "status": "pending",
	})
	dbfx.Insert(t, "issue_workflow_delivery_attempt", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": workspace, "issue_id": issue,
		"delivery_id": delivery, "attempt_number": 1, "operation": "prepare",
		"outcome": "retry", "error_class": "provider_unavailable",
	})
}

func assertWorkflowLedgerDeleted(t *testing.T, issue string) {
	t.Helper()
	for _, table := range workflowLedgerTables {
		if count := dbfx.Count(t, fmt.Sprintf(`SELECT count(*) FROM %s WHERE issue_id=$1`, table), issue); count != 0 {
			t.Errorf("%s retained %d rows for deleted issue", table, count)
		}
	}
}

func TestDeleteIssueCleansWorkflowLedgerWithPendingDelivery(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler DB fixture unavailable")
	}
	workspace := dbfx.Workspace(t, "Workflow delete", fmt.Sprintf("workflow-ledger-issue-%d", time.Now().UnixNano()))
	dbfx.Member(t, workspace, testUserID, "owner")
	issue := dbfx.Issue(t, "Delete pending delivery", testutil.Cols{"workspace_id": workspace, "status": "done"})
	populateWorkflowLedgerForDelete(t, workspace, issue)
	if err := testHandler.Queries.DeleteIssue(context.Background(), db.DeleteIssueParams{
		ID: parseUUID(issue), WorkspaceID: parseUUID(testWorkspaceID),
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range workflowLedgerTables {
		if count := dbfx.Count(t, fmt.Sprintf(`SELECT count(*) FROM %s WHERE issue_id=$1`, table), issue); count != 1 {
			t.Fatalf("mismatched workspace deleted %s rows: %d", table, count)
		}
	}
	req := withURLParam(newRequest(http.MethodDelete, "/api/issues/"+issue, nil), "id", issue)
	req.Header.Set("X-Workspace-ID", workspace)
	testutil.Call(t, testHandler.DeleteIssue, req).Want(http.StatusNoContent)
	if count := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1`, issue); count != 0 {
		t.Fatal("issue survived deletion")
	}
	assertWorkflowLedgerDeleted(t, issue)
}

func TestDeleteWorkspaceCleansWorkflowLedgerWithPendingDelivery(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler DB fixture unavailable")
	}
	workspace := dbfx.Workspace(t, "Workflow workspace delete", fmt.Sprintf("workflow-ledger-ws-%d", time.Now().UnixNano()))
	dbfx.Member(t, workspace, testUserID, "owner")
	issue := dbfx.Issue(t, "Workspace pending delivery", testutil.Cols{"workspace_id": workspace, "status": "done"})
	populateWorkflowLedgerForDelete(t, workspace, issue)
	req := withURLParam(newRequest(http.MethodDelete, "/api/workspaces/"+workspace, nil), "id", workspace)
	req.Header.Set("X-Workspace-ID", workspace)
	testutil.Call(t, testHandler.DeleteWorkspace, req).Want(http.StatusNoContent)
	if count := dbfx.Count(t, `SELECT count(*) FROM workspace WHERE id=$1`, workspace); count != 0 {
		t.Fatal("workspace survived deletion")
	}
	assertWorkflowLedgerDeleted(t, issue)
}

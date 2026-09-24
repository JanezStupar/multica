package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueWorkflowMigrateCLI(t *testing.T) {
	const issueID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const skillID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	t.Chdir(t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/api/issues/"+issueID+"/workflow-migrate" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["skill_id"] != skillID || body["reason"] != "historical work" || body["reconciliation"] != "reviewed remaining work" || len(body) != 3 {
			t.Errorf("unexpected migration body: %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_policy": "migrated"})
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")
	cmd := newIssueWorkflowPolicyCommand()
	cmd.SetArgs([]string{
		"migrate", issueID,
		"--skill-id", skillID,
		"--reason", " historical work ",
		"--reconciliation", " reviewed remaining work ",
	})
	if err := cmd.Execute(); err != nil || calls != 1 {
		t.Fatalf("calls=%d, error=%v", calls, err)
	}
}

func TestIssueWorkflowMigrateRejectsMissingReconciliationBeforeRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")
	cmd := newIssueWorkflowPolicyCommand()
	cmd.SetArgs([]string{
		"migrate", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"--skill-id", "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"--reason", "reason", "--reconciliation", "  ",
	})
	if err := cmd.Execute(); err == nil || calls != 0 {
		t.Fatalf("missing reconciliation must fail before request: calls=%d, error=%v", calls, err)
	}
}

func TestIssueWorkflowMigrateCLIIncludesExplicitReopenTarget(t *testing.T) {
	const issueID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const skillID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	t.Chdir(t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["reopen_to"] != "in_progress" || len(body) != 4 {
			t.Errorf("reopen target request = %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_policy": "migrated"})
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")
	cmd := newIssueWorkflowPolicyCommand()
	cmd.SetArgs([]string{
		"migrate", issueID,
		"--skill-id", skillID,
		"--reason", "historical work",
		"--reconciliation", "reviewed historical work",
		"--reopen-to", " in_progress ",
	})
	if err := cmd.Execute(); err != nil || calls != 1 {
		t.Fatalf("calls=%d, error=%v", calls, err)
	}
}

func TestIssueWorkflowMigrateRejectsEmptyReopenTargetBeforeRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")
	cmd := newIssueWorkflowPolicyCommand()
	cmd.SetArgs([]string{
		"migrate", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		"--skill-id", "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		"--reason", "reason", "--reconciliation", "reconciled", "--reopen-to", "  ",
	})
	if err := cmd.Execute(); err == nil || calls != 0 {
		t.Fatalf("empty reopen target must fail before request: calls=%d, error=%v", calls, err)
	}
}

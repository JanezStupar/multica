package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIssueWorkflowPolicyCLI(t *testing.T) {
	const issueID = "a57c0511-1ebc-471d-a314-438ca16cc75d"
	const skillID = "356c8712-6100-4fb2-ac42-513770588468"
	for _, action := range []string{"get", "pin"} {
		t.Run(action, func(t *testing.T) {
			t.Chdir(t.TempDir())
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				method := http.MethodGet
				if action == "pin" {
					method = http.MethodPost
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["skill_id"] != skillID {
						t.Errorf("unexpected pin body: %v, error: %v", body, err)
					}
				}
				if r.Method != method || r.URL.Path != "/api/issues/"+issueID+"/workflow-policy" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"scope": "issue_workflow_bundle", "version": "sha256:example", "source_skill_id": skillID})
			}))
			defer srv.Close()
			t.Setenv("MULTICA_SERVER_URL", srv.URL)
			t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
			t.Setenv("MULTICA_TOKEN", "test-token")
			cmd := newIssueWorkflowPolicyCommand()
			args := []string{action, issueID}
			if action == "pin" {
				args = append(args, "--skill-id", skillID)
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil || calls != 1 {
				t.Fatalf("calls=%d, error=%v", calls, err)
			}
		})
	}
}

func TestIssueWorkflowPolicyCLIRejectsInvalidSkillBeforeRequest(t *testing.T) {
	cmd := newIssueWorkflowPolicyCommand()
	cmd.SetArgs([]string{"pin", "a57c0511-1ebc-471d-a314-438ca16cc75d", "--skill-id", "invalid"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("invalid skill UUID accepted")
	}
}

func TestIssueWorkflowPolicyCLIDoesNotRetryConflict(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"issue already has task history"}`))
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")
	cmd := newIssueWorkflowPolicyCommand()
	cmd.SetArgs([]string{"pin", "a57c0511-1ebc-471d-a314-438ca16cc75d", "--skill-id", "356c8712-6100-4fb2-ac42-513770588468"})
	if err := cmd.Execute(); err == nil || calls != 1 {
		t.Fatalf("conflict must be surfaced once: calls=%d, error=%v", calls, err)
	}
}

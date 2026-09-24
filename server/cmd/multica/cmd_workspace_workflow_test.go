package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkspaceWorkflowCLI(t *testing.T) {
	const workspaceID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const skillID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	for _, action := range []string{"get", "cutover", "set-default"} {
		t.Run(action, func(t *testing.T) {
			t.Chdir(t.TempDir())
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				method, suffix := http.MethodGet, "/workflow-default"
				if action == "cutover" {
					method, suffix = http.MethodPost, "/workflow-cutover"
				} else if action == "set-default" {
					method = http.MethodPut
				}
				if r.Method != method || r.URL.Path != "/api/workspaces/"+workspaceID+suffix {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if action != "get" {
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["skill_id"] != skillID {
						t.Errorf("unexpected workflow request body: %v (error: %v)", body, err)
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"policy": nil, "cutover_at": nil})
			}))
			defer srv.Close()
			t.Setenv("MULTICA_SERVER_URL", srv.URL)
			t.Setenv("MULTICA_WORKSPACE_ID", workspaceID)
			t.Setenv("MULTICA_TOKEN", "test-token")
			cmd := newWorkspaceWorkflowCommand()
			args := []string{action, workspaceID}
			if action != "get" {
				args = append(args, "--skill-id", skillID)
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil || calls != 1 {
				t.Fatalf("calls=%d, error=%v", calls, err)
			}
		})
	}
}

func TestWorkspaceWorkflowCLIDoesNotSendInvalidSkill(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	t.Setenv("MULTICA_TOKEN", "test-token")
	cmd := newWorkspaceWorkflowCommand()
	cmd.SetArgs([]string{"cutover", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "--skill-id", "bad"})
	if err := cmd.Execute(); err == nil || calls != 0 {
		t.Fatalf("invalid skill must fail before request: calls=%d, error=%v", calls, err)
	}
}

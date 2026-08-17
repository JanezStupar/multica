package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const (
	testBuiltinAgentID = "22222222-2222-2222-2222-222222222222"
	testBuiltinSkillID = "builtin:multica-working-on-issues"
)

func newAgentBuiltinsTestCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "builtins"}
	cmd.Flags().String("workspace-id", "", "")
	cmd.Flags().String("profile", "", "")
	cmd.Flags().String("server-url", "", "")
	cmd.Flags().String("output", "table", "")
	return cmd
}

func builtinPolicyTestServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MULTICA_TOKEN", "test-token")
	t.Setenv("MULTICA_WORKSPACE_ID", "workspace-123")
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
}

func writeBuiltinAgentResponse(w http.ResponseWriter, enabledIDs any, targetEnabled bool) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":                        testBuiltinAgentID,
		"enabled_builtin_skill_ids": enabledIDs,
		"builtin_skills": []map[string]any{
			{
				"id":          testBuiltinSkillID,
				"name":        "multica-working-on-issues",
				"description": "Work on Multica issues",
				"enabled":     targetEnabled,
			},
		},
	})
}

func TestAgentBuiltinsCommandsAreRegistered(t *testing.T) {
	for _, name := range []string{"list", "enable", "disable", "reset"} {
		cmd, _, err := agentCmd.Find([]string{"builtins", name})
		if err != nil {
			t.Fatalf("find agent builtins %s: %v", name, err)
		}
		if cmd == nil || cmd.Name() != name {
			t.Fatalf("agent builtins %s is not registered", name)
		}
	}
}

func TestRunAgentBuiltinsListShowsInheritedPolicy(t *testing.T) {
	var gotMethod, gotPath string
	builtinPolicyTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		writeBuiltinAgentResponse(w, nil, true)
	})

	cmd := newAgentBuiltinsTestCmd()
	out, err := captureStdout(t, func() error {
		return runAgentBuiltinsList(cmd, []string{testBuiltinAgentID})
	})
	if err != nil {
		t.Fatalf("runAgentBuiltinsList: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/agents/"+testBuiltinAgentID {
		t.Fatalf("request = %s %s, want GET agent detail", gotMethod, gotPath)
	}
	for _, want := range []string{"Policy: inherit_all", testBuiltinSkillID, "enabled"} {
		if !strings.Contains(out, want) {
			t.Fatalf("table output missing %q: %q", want, out)
		}
	}
}

func TestRunAgentBuiltinsListJSONPreservesExactEmptyPolicy(t *testing.T) {
	builtinPolicyTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		writeBuiltinAgentResponse(w, []string{}, false)
	})

	cmd := newAgentBuiltinsTestCmd()
	_ = cmd.Flags().Set("output", "json")
	out, err := captureStdout(t, func() error {
		return runAgentBuiltinsList(cmd, []string{testBuiltinAgentID})
	})
	if err != nil {
		t.Fatalf("runAgentBuiltinsList: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, out)
	}
	if got["mode"] != "exact" {
		t.Fatalf("mode = %v, want exact", got["mode"])
	}
	ids, ok := got["enabled_builtin_skill_ids"].([]any)
	if !ok || len(ids) != 0 {
		t.Fatalf("enabled_builtin_skill_ids = %#v, want explicit []", got["enabled_builtin_skill_ids"])
	}
}

func TestRunAgentBuiltinsDisableAndEnableUseDedicatedEndpoint(t *testing.T) {
	var gotMethods, gotPaths []string
	var gotEnabled []bool
	builtinPolicyTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethods = append(gotMethods, r.Method)
		gotPaths = append(gotPaths, r.URL.Path)
		if r.Method == http.MethodPut {
			var body struct {
				SkillID string `json:"skill_id"`
				Enabled bool   `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if body.SkillID != testBuiltinSkillID {
				t.Fatalf("skill_id = %q, want %q", body.SkillID, testBuiltinSkillID)
			}
			gotEnabled = append(gotEnabled, body.Enabled)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeBuiltinAgentResponse(w, []string{}, false)
	})

	cmd := newAgentBuiltinsTestCmd()
	out, err := captureStdout(t, func() error {
		return runAgentBuiltinsDisable(cmd, []string{testBuiltinAgentID, testBuiltinSkillID})
	})
	if err != nil {
		t.Fatalf("runAgentBuiltinsDisable: %v", err)
	}
	if !strings.Contains(out, "Policy: exact") || !strings.Contains(out, "disabled") {
		t.Fatalf("disable output did not read back exact policy: %q", out)
	}
	if len(gotMethods) != 2 || gotMethods[0] != http.MethodPut || gotMethods[1] != http.MethodGet {
		t.Fatalf("methods = %v, want PUT then GET", gotMethods)
	}
	wantMutationPath := "/api/agents/" + testBuiltinAgentID + "/builtin-skills/enabled"
	if gotPaths[0] != wantMutationPath || gotPaths[1] != "/api/agents/"+testBuiltinAgentID {
		t.Fatalf("paths = %v", gotPaths)
	}
	if len(gotEnabled) != 1 || gotEnabled[0] {
		t.Fatalf("disable enabled values = %v, want [false]", gotEnabled)
	}

	gotMethods, gotPaths = nil, nil
	if _, err := captureStdout(t, func() error {
		return runAgentBuiltinsEnable(cmd, []string{testBuiltinAgentID, testBuiltinSkillID})
	}); err != nil {
		t.Fatalf("runAgentBuiltinsEnable: %v", err)
	}
	if len(gotEnabled) != 2 || !gotEnabled[1] {
		t.Fatalf("enable enabled values = %v, want final true", gotEnabled)
	}
}

func TestRunAgentBuiltinsResetRestoresInheritance(t *testing.T) {
	var gotMethods []string
	builtinPolicyTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethods = append(gotMethods, r.Method)
		if r.Method == http.MethodDelete {
			if r.URL.Path != "/api/agents/"+testBuiltinAgentID+"/builtin-skills" {
				t.Fatalf("reset path = %q", r.URL.Path)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeBuiltinAgentResponse(w, nil, true)
	})

	cmd := newAgentBuiltinsTestCmd()
	out, err := captureStdout(t, func() error {
		return runAgentBuiltinsReset(cmd, []string{testBuiltinAgentID})
	})
	if err != nil {
		t.Fatalf("runAgentBuiltinsReset: %v", err)
	}
	if len(gotMethods) != 2 || gotMethods[0] != http.MethodDelete || gotMethods[1] != http.MethodGet {
		t.Fatalf("methods = %v, want DELETE then GET", gotMethods)
	}
	if !strings.Contains(out, "Policy: inherit_all") {
		t.Fatalf("reset output did not show inherited policy: %q", out)
	}
}

func TestRunAgentBuiltinsMutationSurfacesServerErrorWithoutReadback(t *testing.T) {
	requests := 0
	builtinPolicyTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, `{"error":"unknown built-in skill"}`, http.StatusBadRequest)
	})

	cmd := newAgentBuiltinsTestCmd()
	_, err := captureStdout(t, func() error {
		return runAgentBuiltinsDisable(cmd, []string{testBuiltinAgentID, "builtin:not-real"})
	})
	if err == nil || !strings.Contains(err.Error(), "unknown built-in skill") {
		t.Fatalf("error = %v, want unknown built-in skill", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want no read-back after failed mutation", requests)
	}
}

func TestRunAgentBuiltinsListRejectsUnsupportedServer(t *testing.T) {
	builtinPolicyTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": testBuiltinAgentID})
	})

	cmd := newAgentBuiltinsTestCmd()
	_, err := captureStdout(t, func() error {
		return runAgentBuiltinsList(cmd, []string{testBuiltinAgentID})
	})
	if err == nil || !strings.Contains(err.Error(), "not available from this server") {
		t.Fatalf("error = %v, want unsupported-server message", err)
	}
}

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

const (
	handoffIssueID        = "11111111-1111-4111-8111-111111111111"
	handoffRequestKey     = "22222222-2222-4222-8222-222222222222"
	handoffOutgoingTaskID = "33333333-3333-4333-8333-333333333333"
	handoffAgentID        = "44444444-4444-4444-8444-444444444444"
)

func TestIssueHandoffCreateUsesJSONFileAndKeepsEmptyArtifactLists(t *testing.T) {
	t.Chdir(t.TempDir())
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/issues/"+handoffIssueID+"/handoffs" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "55555555-5555-4555-8555-555555555555", "issue_id": handoffIssueID,
			"request_key": handoffRequestKey, "agent_id": handoffAgentID, "enabled": true,
		})
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)
	if err := os.WriteFile("handoff.json", []byte(`{
  "request_key": "`+handoffRequestKey+`",
  "outgoing_task_id": "`+handoffOutgoingTaskID+`",
  "agent_id": "`+handoffAgentID+`",
  "status": "in_review",
  "context_mode": "fresh",
  "instruction": "Review the exact candidate; start a fresh independent context."
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newIssueHandoffCommand()
	cmd.SetArgs([]string{"create", handoffIssueID, "--file", "handoff.json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if received["request_key"] != handoffRequestKey || received["outgoing_task_id"] != handoffOutgoingTaskID ||
		received["agent_id"] != handoffAgentID || received["status"] != "in_review" || received["context_mode"] != "fresh" {
		t.Fatalf("handoff request lost contract fields: %+v", received)
	}
	if candidates, ok := received["candidates"].([]any); !ok || len(candidates) != 0 {
		t.Fatalf("non-code handoff must send candidates: [], got %#v", received["candidates"])
	}
	if evidence, ok := received["evidence_urls"].([]any); !ok || len(evidence) != 0 {
		t.Fatalf("empty evidence must send evidence_urls: [], got %#v", received["evidence_urls"])
	}
}

func TestIssueHandoffCreateSupportsCanonicalHumanRecipient(t *testing.T) {
	t.Chdir(t.TempDir())
	var received map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/issues/"+handoffIssueID+"/handoffs" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "55555555-5555-4555-8555-555555555555", "issue_id": handoffIssueID,
			"request_key": handoffRequestKey, "agent_id": handoffAgentID, "enabled": false,
			"handoff_completed_at": "2026-09-24T00:00:00Z", "last_task_id": nil,
			"handoff": map[string]any{"assignee_type": "member", "assignee_id": "77777777-7777-4777-8777-777777777777"},
		})
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)
	if err := os.WriteFile("handoff.json", []byte(`{
  "request_key": "`+handoffRequestKey+`",
  "outgoing_task_id": "`+handoffOutgoingTaskID+`",
  "assignee_type": "member",
  "assignee_id": "77777777-7777-4777-8777-777777777777",
  "status": "in_review",
  "context_mode": "fresh",
  "instruction": "Please review the completed work."
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newIssueHandoffCommand()
	cmd.SetArgs([]string{"create", handoffIssueID, "--file", "handoff.json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if received["assignee_type"] != "member" || received["assignee_id"] != "77777777-7777-4777-8777-777777777777" {
		t.Fatalf("canonical member target missing: %+v", received)
	}
	if _, ok := received["agent_id"]; ok {
		t.Fatalf("member handoff must omit agent_id: %+v", received)
	}
	if received["status"] != "in_review" || received["context_mode"] != "fresh" {
		t.Fatalf("member handoff lifecycle mismatch: %+v", received)
	}
}

func TestIssueHandoffListAndCancelUseHandoffAndNativeWakeupRoutes(t *testing.T) {
	t.Chdir(t.TempDir())
	var listCalls, cancelCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/issues/"+handoffIssueID+"/handoffs":
			listCalls++
			_, _ = w.Write([]byte("[]"))
		case r.Method == http.MethodPost && r.URL.Path == "/api/issues/"+handoffIssueID+"/wakeups/66666666-6666-4666-8666-666666666666/disable":
			cancelCalls++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body) != 0 {
				t.Errorf("cancel body = %#v, want empty object", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "66666666-6666-4666-8666-666666666666", "enabled": false})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)

	listCmd := newIssueHandoffCommand()
	listCmd.SetArgs([]string{"list", handoffIssueID})
	if err := listCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	cancelCmd := newIssueHandoffCommand()
	cancelCmd.SetArgs([]string{"cancel", handoffIssueID, "--handoff-id", "66666666-6666-4666-8666-666666666666"})
	if err := cancelCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if listCalls != 1 || cancelCalls != 1 {
		t.Fatalf("list calls=%d cancel calls=%d, want one each", listCalls, cancelCalls)
	}
}

func TestIssueHandoffRejectsUnsafeOrMalformedRequestBeforeNetwork(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)
	if err := os.WriteFile("handoff.json", []byte(`{"request_key":"`+handoffRequestKey+`","outgoing_task_id":"`+handoffOutgoingTaskID+`","agent_id":"`+handoffAgentID+`","status":"in_review","context_mode":"resume","instruction":"resume","candidates":[],"evidence_urls":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newIssueHandoffCommand()
	cmd.SetArgs([]string{"create", handoffIssueID, "--file", "handoff.json"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("resume handoff without exact resume_task_id should fail")
	}
	if calls != 0 {
		t.Fatalf("malformed request reached server %d times", calls)
	}
}

func TestIssueHandoffCancelRequiresUUIDBeforeNetwork(t *testing.T) {
	t.Chdir(t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)
	cmd := newIssueHandoffCommand()
	cmd.SetArgs([]string{"cancel", handoffIssueID, "--handoff-id", "not-a-uuid"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected invalid handoff UUID to be rejected")
	}
	if calls != 0 {
		t.Fatalf("invalid handoff ID reached server %d times", calls)
	}
}

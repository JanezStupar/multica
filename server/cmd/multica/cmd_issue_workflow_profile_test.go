package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestIssueWorkflowProfileReselectCLIUsesExactRequestFile(t *testing.T) {
	const issueID = "11111111-1111-4111-8111-111111111111"
	const agentID = "22222222-2222-4222-8222-222222222222"
	const profileID = "33333333-3333-4333-8333-333333333333"
	const requestKey = "44444444-4444-4444-8444-444444444444"
	t.Chdir(t.TempDir())
	if err := os.WriteFile("profile.json", []byte(`{
		"agent_id":"`+agentID+`","expected_profile_id":"`+profileID+`",
		"request_key":"`+requestKey+`","reason":"Use edited behavior",
		"consequences":"Start a fresh session","reconciliation":"Prior attempts retain old bytes",
		"supplemental_instructions":"Report this ticket concisely"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/api/issues/"+issueID+"/workflow-profile/reselect" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["agent_id"] != agentID || body["expected_profile_id"] != profileID ||
			body["request_key"] != requestKey || body["supplemental_instructions"] != "Report this ticket concisely" || len(body) != 7 {
			t.Errorf("profile request changed: %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"workflow_profile_id": requestKey, "revision": 2})
	}))
	defer srv.Close()
	setCLITestServerEnv(t, srv.URL)
	command := newIssueWorkflowProfileCommand()
	command.SetArgs([]string{"reselect", issueID, "--file", "profile.json"})
	if err := command.Execute(); err != nil || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestIssueWorkflowProfileReselectCLIRejectsMissingRequestKey(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("profile.json", []byte(`{
		"agent_id":"22222222-2222-4222-8222-222222222222",
		"expected_profile_id":"33333333-3333-4333-8333-333333333333",
		"reason":"change","consequences":"fresh session","reconciliation":"settled"
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newIssueWorkflowProfileCommand()
	command.SetArgs([]string{"reselect", "11111111-1111-4111-8111-111111111111", "--file", "profile.json"})
	if err := command.Execute(); err == nil {
		t.Fatal("missing request key reached API")
	}
}

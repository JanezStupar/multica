package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssueWorkflowAcceptHelpShowsBothRequestShapes(t *testing.T) {
	cmd := newIssueWorkflowCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"accept", "--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`{"candidate_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","expected_revision":4,"outcome_complete":false}`,
		`"classification_reason":"Scoped trivial change with completed independent review","outcome_complete":false`,
		`"outcome_complete":false,"hold_delivery":true`,
		`"merge_order_pr_urls"`,
		"delivery_preview.requires_order",
		"pending until the source task completes successfully",
		"Format-2 policies require outcome_complete",
		"required PR merges remain server-gated",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("accept help lacks %q: %s", want, out.String())
		}
	}
}

func TestIssueWorkflowDispositionHelpExplainsStateBoundaries(t *testing.T) {
	for _, tc := range []struct {
		action string
		want   []string
	}{
		{action: "hold", want: []string{"blocks merging", "remains PR Ready", "readiness checks may still proceed", "Retries and restarts preserve the hold"}},
		{action: "release", want: []string{"exact acceptance and candidate", "does not authorize a later candidate"}},
		{action: "complete", want: []string{"actual outcome is complete", "server still gates completion on all required PR merges"}},
		{action: "retry-outcome", want: []string{"failed or was cancelled", "exactly one new task", "preserves the earlier task history", "server enforces the recovery boundary"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			cmd := newIssueWorkflowCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{tc.action, "--help"})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("%s help lacks %q: %s", tc.action, want, out.String())
				}
			}
			if !strings.Contains(out.String(), "--acceptance-id") || !strings.Contains(out.String(), "--file") {
				t.Fatalf("%s help must show acceptance and request flags: %s", tc.action, out.String())
			}
		})
	}
}

func TestIssueWorkflowAcceptValidationKeepsHumanShapeAndRejectsOversizedReason(t *testing.T) {
	const candidateID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	if _, err := decodeIssueWorkflowInput([]byte(`{"candidate_id":"`+candidateID+`","expected_revision":4}`), "accept"); err != nil {
		t.Fatalf("human request should not require classification_reason: %v", err)
	}
	input, err := decodeIssueWorkflowInput([]byte(`{"candidate_id":"`+candidateID+`","expected_revision":4,"classification_reason":"  Narrow change  "}`), "accept")
	if err != nil || input.(*issueWorkflowAcceptanceInput).ClassificationReason != "Narrow change" {
		t.Fatalf("autonomous reason was not normalized: %#v, %v", input, err)
	}
	format2, err := decodeIssueWorkflowInput([]byte(`{"candidate_id":"`+candidateID+`","expected_revision":4,"outcome_complete":false,"hold_delivery":true}`), "accept")
	if err != nil {
		t.Fatalf("format-2 acceptance fields should be accepted: %v", err)
	}
	format2Body, err := json.Marshal(format2)
	if err != nil || !strings.Contains(string(format2Body), `"outcome_complete":false`) || !strings.Contains(string(format2Body), `"hold_delivery":true`) {
		t.Fatalf("format-2 acceptance fields were not preserved: %s, %v", format2Body, err)
	}
	if _, err := decodeIssueWorkflowInput([]byte(`{"candidate_id":"`+candidateID+`","expected_revision":4,"classification_reason":"`+strings.Repeat("x", 2001)+`"}`), "accept"); err == nil || !strings.Contains(err.Error(), "classification_reason") {
		t.Fatalf("oversized reason should identify the field: %v", err)
	}
}

func TestIssueWorkflowAcceptanceDispositionRequiresCandidateRevisionAndReason(t *testing.T) {
	const candidateID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	valid := `{"candidate_id":"` + candidateID + `","expected_revision":4,"reason":" Complete the outcome "}`
	for _, action := range []string{"hold", "release", "complete", "retry-outcome"} {
		t.Run(action, func(t *testing.T) {
			input, err := decodeIssueWorkflowInput([]byte(valid), action)
			if err != nil || input.(*issueWorkflowAcceptanceDispositionInput).Reason != "Complete the outcome" {
				t.Fatalf("valid disposition was not normalized: %#v, %v", input, err)
			}
			for _, invalid := range []string{
				`{"candidate_id":"` + candidateID + `","expected_revision":4}`,
				`{"candidate_id":"` + candidateID + `","expected_revision":4,"reason":" "}`,
				`{"candidate_id":"` + candidateID + `","expected_revision":4,"reason":"ok","unexpected":true}`,
			} {
				if _, err := decodeIssueWorkflowInput([]byte(invalid), action); err == nil {
					t.Fatalf("invalid %s disposition unexpectedly accepted: %s", action, invalid)
				}
			}
			longCJKReason := strings.Repeat("界", 500)
			validBoundary := `{"candidate_id":"` + candidateID + `","expected_revision":4,"reason":"` + longCJKReason + `"}`
			if _, err := decodeIssueWorkflowInput([]byte(validBoundary), action); err != nil {
				t.Fatalf("500-code-point CJK reason should be accepted for %s: %v", action, err)
			}
			tooLongCJKReason := strings.Repeat("界", 501)
			invalidBoundary := `{"candidate_id":"` + candidateID + `","expected_revision":4,"reason":"` + tooLongCJKReason + `"}`
			if _, err := decodeIssueWorkflowInput([]byte(invalidBoundary), action); err == nil || !strings.Contains(err.Error(), "500 Unicode code points") {
				t.Fatalf("501-code-point CJK reason should be rejected for %s: %v", action, err)
			}
		})
	}
}

func TestIssueWorkflowCLIActions(t *testing.T) {
	const issueID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const candidateID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	const exceptionID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	const deliveryID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	const acceptanceID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	workflowResponse := `{"issue_id":"` + issueID + `","issue_revision":4,"frozen":false,"policy_version":"sha256:policy","candidate":null,"reviews":[],"acceptance":null,"delivery":[],"retained_context_options":[],"available_actions":{"accept_human":true,"reject":true,"request_trivial_acceptance":false,"waive_review":false}}`
	cases := []struct {
		name, action, path, input string
		acceptanceID              string
		wantMethod                string
		wantBody                  map[string]any
	}{
		{name: "get", path: "/api/issues/" + issueID + "/workflow", wantMethod: http.MethodGet},
		{name: "review", path: "/api/issues/" + issueID + "/workflow/reviews", wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","verdict":"pass","pr_review_urls":["https://git.thn.janezstupar.com/Janez/test/pulls/5#issuecomment-340","https://github.com/acme/app/pull/7#pullrequestreview-80"]}`,
			wantBody: map[string]any{"candidate_id": candidateID, "verdict": "pass", "pr_review_urls": []any{"https://git.thn.janezstupar.com/Janez/test/pulls/5#issuecomment-340", "https://github.com/acme/app/pull/7#pullrequestreview-80"}}},
		{name: "review without PRs", action: "review", path: "/api/issues/" + issueID + "/workflow/reviews", wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","verdict":"pass","pr_review_urls":[]}`,
			wantBody: map[string]any{"candidate_id": candidateID, "verdict": "pass", "pr_review_urls": []any{}}},
		{name: "accept", path: "/api/issues/" + issueID + "/workflow/acceptances", wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","expected_revision":4}`,
			wantBody: map[string]any{"candidate_id": candidateID, "expected_revision": float64(4)}},
		{name: "accept format 2", action: "accept", path: "/api/issues/" + issueID + "/workflow/acceptances", wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","expected_revision":4,"outcome_complete":false,"hold_delivery":true}`,
			wantBody: map[string]any{"candidate_id": candidateID, "expected_revision": float64(4), "outcome_complete": false, "hold_delivery": true}},
		{name: "hold", path: "/api/issues/" + issueID + "/workflow/acceptances/" + acceptanceID + "/hold", acceptanceID: acceptanceID, wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","expected_revision":4,"reason":" Hold delivery "}`,
			wantBody: map[string]any{"candidate_id": candidateID, "expected_revision": float64(4), "reason": "Hold delivery"}},
		{name: "release", path: "/api/issues/" + issueID + "/workflow/acceptances/" + acceptanceID + "/release", acceptanceID: acceptanceID, wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","expected_revision":4,"reason":" Release hold "}`,
			wantBody: map[string]any{"candidate_id": candidateID, "expected_revision": float64(4), "reason": "Release hold"}},
		{name: "complete", path: "/api/issues/" + issueID + "/workflow/acceptances/" + acceptanceID + "/complete", acceptanceID: acceptanceID, wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","expected_revision":4,"reason":" Outcome complete "}`,
			wantBody: map[string]any{"candidate_id": candidateID, "expected_revision": float64(4), "reason": "Outcome complete"}},
		{name: "retry-outcome", path: "/api/issues/" + issueID + "/workflow/acceptances/" + acceptanceID + "/retry-outcome", acceptanceID: acceptanceID, wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","expected_revision":4,"reason":" Retry outcome "}`,
			wantBody: map[string]any{"candidate_id": candidateID, "expected_revision": float64(4), "reason": "Retry outcome"}},
		{name: "reject", path: "/api/issues/" + issueID + "/workflow/rejections", wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","expected_revision":4,"kind":"scope_change","reason":" New work "}`,
			wantBody: map[string]any{"candidate_id": candidateID, "expected_revision": float64(4), "kind": "scope_change", "reason": "New work"}},
		{name: "exception", path: "/api/issues/" + issueID + "/workflow/exceptions", wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","expected_revision":4,"scope":"review","grant_details":{"waive":true},"reason":" Justified exception ","consequences":"One final review is waived"}`,
			wantBody: map[string]any{"candidate_id": candidateID, "expected_revision": float64(4), "scope": "review", "grant_details": map[string]any{"waive": true}, "reason": "Justified exception", "consequences": "One final review is waived"}},
		{name: "exception revoke", path: "/api/issues/" + issueID + "/workflow/exceptions/" + exceptionID + "/revoke", wantMethod: http.MethodPost,
			input:    `{"expected_revision":4,"reason":"Reason","consequences":"Grant no longer applies"}`,
			wantBody: map[string]any{"expected_revision": float64(4), "reason": "Reason", "consequences": "Grant no longer applies"}},
		{name: "delivery retry", action: "delivery-retry", path: "/api/issues/" + issueID + "/workflow/delivery/" + deliveryID + "/retry", wantMethod: http.MethodPost,
			input:    `{"candidate_id":"` + candidateID + `","expected_revision":4,"reason":" Binding restored "}`,
			wantBody: map[string]any{"candidate_id": candidateID, "expected_revision": float64(4), "reason": "Binding restored"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.wantMethod || r.URL.Path != tc.path {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if tc.wantBody != nil {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode request: %v", err)
					} else if !equalWorkflowJSON(body, tc.wantBody) {
						t.Errorf("request body = %#v, want %#v", body, tc.wantBody)
					}
				}
				_, _ = w.Write([]byte(workflowResponse))
			}))
			defer srv.Close()
			t.Setenv("MULTICA_SERVER_URL", srv.URL)
			t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
			t.Setenv("MULTICA_TOKEN", "test-token")

			action := tc.name
			if tc.action != "" {
				action = tc.action
			}
			args := []string{action, issueID}
			if tc.name == "exception revoke" {
				args = []string{"exception", "revoke", issueID, exceptionID, "--file", writeWorkflowFixture(t, tc.input)}
			} else if tc.name == "delivery retry" {
				args = []string{"delivery-retry", issueID, deliveryID, "--file", writeWorkflowFixture(t, tc.input)}
			} else if tc.acceptanceID != "" {
				args = append(args, "--acceptance-id", tc.acceptanceID, "--file", writeWorkflowFixture(t, tc.input))
			} else if tc.name != "get" {
				args = append(args, "--file", writeWorkflowFixture(t, tc.input))
			}
			cmd := newIssueWorkflowCommand()
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil || calls != 1 {
				t.Fatalf("calls=%d, error=%v", calls, err)
			}
		})
	}
}

func TestIssueWorkflowReviewValidationRequiresDistinctHTTPSAnchoredURLs(t *testing.T) {
	const candidateID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	valid := []string{
		"https://git.thn.janezstupar.com/Janez/test/pulls/5#issuecomment-340",
		"https://github.com/acme/app/pull/7#pullrequestreview-80",
	}
	if err := validateIssueWorkflowReview(&issueWorkflowReviewInput{CandidateID: candidateID, Verdict: "pass", PRReviewURLs: valid}); err != nil {
		t.Fatalf("canonical Forgejo and GitHub review anchors rejected: %v", err)
	}

	tooLong := "https://example.com/" + strings.Repeat("a", 2049-len("https://example.com/")-len("#review")) + "#review"
	tooMany := make([]string, 33)
	for i := range tooMany {
		tooMany[i] = "https://example.com/pull#review-" + strings.Repeat("a", i+1)
	}
	cases := []struct {
		name string
		urls []string
	}{
		{name: "missing anchor", urls: []string{"https://example.com/pull"}},
		{name: "non-HTTPS", urls: []string{"http://example.com/pull#review"}},
		{name: "userinfo", urls: []string{"https://user@example.com/pull#review"}},
		{name: "control character", urls: []string{"https://example.com/pull#review\x7f"}},
		{name: "duplicate", urls: []string{"https://example.com/pull#review", "https://example.com/pull#review"}},
		{name: "URL too long", urls: []string{tooLong}},
		{name: "too many URLs", urls: tooMany},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateIssueWorkflowReview(&issueWorkflowReviewInput{CandidateID: candidateID, Verdict: "pass", PRReviewURLs: tc.urls}); err == nil {
				t.Fatalf("invalid review URLs unexpectedly accepted: %#v", tc.urls)
			}
		})
	}
}

func TestIssueWorkflowCLIRejectsMalformedOrOversizedJSONBeforeRequest(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, input := range []string{
		`{"candidate_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","expected_revision":1,"kind":"scope_change","reason":"ok","unexpected":true}`,
		`{"candidate_id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","expected_revision":1,"kind":"scope_change","reason":"ok"} {}`,
		strings.Repeat("x", maxIssueWorkflowInputBytes+1),
	} {
		path := writeWorkflowFixture(t, input)
		cmd := newIssueWorkflowCommand()
		cmd.SetArgs([]string{"reject", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "--file", path})
		if err := cmd.Execute(); err == nil {
			t.Fatalf("malformed workflow JSON unexpectedly accepted")
		}
	}
}

func TestIssueWorkflowCLIReadsStrictRequestFromStdin(t *testing.T) {
	const issueID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const candidateID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	t.Chdir(t.TempDir())
	serverCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalls++
		if r.Method != http.MethodPost || r.URL.Path != "/api/issues/"+issueID+"/workflow/acceptances" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"issue_id":"` + issueID + `","issue_revision":2,"frozen":false,"policy_version":"p","candidate":null,"reviews":[],"acceptance":null,"delivery":[],"retained_context_options":[],"available_actions":{"accept_human":false,"reject":false,"request_trivial_acceptance":false,"waive_review":false}}`))
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")
	oldStdin := os.Stdin
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = writeEnd.WriteString(`{"candidate_id":"` + candidateID + `","expected_revision":2}`)
	_ = writeEnd.Close()
	os.Stdin = readEnd
	t.Cleanup(func() { os.Stdin = oldStdin; _ = readEnd.Close() })
	cmd := newIssueWorkflowCommand()
	cmd.SetArgs([]string{"accept", issueID, "--file", "-"})
	if err := cmd.Execute(); err != nil || serverCalls != 1 {
		t.Fatalf("calls=%d, error=%v", serverCalls, err)
	}
}

func TestIssueWorkflowExceptionRevokeRejectsMalformedIDBeforeNetwork(t *testing.T) {
	const issueID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	t.Chdir(t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("MULTICA_SERVER_URL", srv.URL)
	t.Setenv("MULTICA_WORKSPACE_ID", "ws-1")
	t.Setenv("MULTICA_TOKEN", "test-token")
	path := writeWorkflowFixture(t, `{"expected_revision":1,"reason":"reason","consequences":"consequence"}`)
	cmd := newIssueWorkflowCommand()
	cmd.SetArgs([]string{"exception", "revoke", issueID, "not-a-uuid", "--file", path})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected invalid exception ID to be rejected")
	}
	if calls != 0 {
		t.Fatalf("invalid exception ID reached server %d times", calls)
	}
}

func writeWorkflowFixture(t *testing.T, input string) string {
	t.Helper()
	path := filepath.Join(".", "workflow.json")
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	return "workflow.json"
}

func equalWorkflowJSON(left, right any) bool {
	leftBytes, _ := json.Marshal(left)
	rightBytes, _ := json.Marshal(right)
	return string(leftBytes) == string(rightBytes)
}

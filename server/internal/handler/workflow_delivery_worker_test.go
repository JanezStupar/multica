package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

const workflowDeliveryHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const workflowDeliveryChangedHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func workflowDeliveryFixture(t *testing.T, server *httptest.Server, count int, action string) (string, []string) {
	t.Helper()
	withVCSBox(t)
	token, err := testHandler.sealVCSSecret("delivery-token")
	if err != nil {
		t.Fatal(err)
	}
	binding := dbfx.Insert(t, "vcs_connection", testutil.Cols{
		"workspace_id": testWorkspaceID, "provider": "forgejo", "instance_url": server.URL,
		"account_login": "delivery fixture", "access_token_encrypted": token, "webhook_secret_encrypted": token,
	})
	issue := dbfx.Issue(t, "Deliver accepted work", testutil.Cols{"status": "done"})
	candidate := dbfx.Insert(t, "issue_workflow_candidate", testutil.Cols{
		"id":           dbid.NewV7(),
		"workspace_id": testWorkspaceID, "issue_id": issue, "policy_version": "test-v1",
		"digest": "fixture", "scope_digest": "fixture", "source_handoff_id": dbid.NewV7(),
		"source_task_id": dbid.NewV7(), "writer_task_id": dbid.NewV7(), "pr_set": testutil.Raw("'[]'::jsonb"),
	})
	dbfx.Exec(t, "UPDATE issue SET workflow_candidate_id=$2 WHERE id=$1", issue, candidate)
	acceptance := dbfx.Insert(t, "issue_workflow_acceptance", testutil.Cols{
		"id":           dbid.NewV7(),
		"workspace_id": testWorkspaceID, "issue_id": issue, "candidate_id": candidate,
		"mode": "human", "actor_type": "member", "actor_id": testUserID, "state": "accepted",
		"issue_revision": int64(1), "policy_version": "test-v1", "authority_snapshot": testutil.Raw("'{}'::jsonb"),
		"accepted_at": testutil.Raw("now()"),
	})
	ids := make([]string, count)
	for ordinal := range count {
		prURL := server.URL + "/team/project/pulls/" + strconv.Itoa(ordinal+1)
		ids[ordinal] = dbfx.Insert(t, "issue_workflow_delivery", testutil.Cols{
			"id":           dbid.NewV7(),
			"workspace_id": testWorkspaceID, "issue_id": issue, "acceptance_id": acceptance, "candidate_id": candidate,
			"ordinal": ordinal, "provider": "forgejo", "provider_binding_id": binding,
			"repository_url": server.URL + "/team/project", "pr_url": prURL, "repo_owner": "team", "repo_name": "project",
			"pr_number": ordinal + 1, "expected_head_sha": workflowDeliveryHead, "action": action,
			"merge_method": func() any {
				if action == "merge" {
					return "merge"
				}
				return nil
			}(),
			"status": "pending",
		})
	}
	return issue, ids
}

func deliveryStatus(t *testing.T, id string) (status string, attempts int) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `SELECT status,attempt_count FROM issue_workflow_delivery WHERE id=$1`, id).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	return
}

func TestWorkflowDeliveryWorkerTerminalReadyPRDoesNotRetryForever(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	for _, tc := range []struct {
		name, head, state, wantStatus, wantClass string
		merged                                   bool
	}{
		{name: "already merged exact head", head: workflowDeliveryHead, state: "closed", merged: true, wantStatus: "delivered"},
		{name: "closed unmerged", head: workflowDeliveryHead, state: "closed", wantStatus: "blocked", wantClass: "closed_unmerged"},
		{name: "merged newer head", head: workflowDeliveryChangedHead, state: "closed", merged: true, wantStatus: "stale", wantClass: "stale_head"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != http.MethodGet {
					t.Errorf("terminal PR triggered mutation %s", r.Method)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"title": "Ready work",
					"head": map[string]string{"sha": tc.head}, "draft": false,
					"merged": tc.merged, "state": tc.state})
			}))
			defer server.Close()
			_, ids := workflowDeliveryFixture(t, server, 1, "ready")
			worker := NewWorkflowDeliveryWorker(testHandler)
			worker.client = server.Client()
			worked, err := worker.ProcessNext(context.Background())
			if err != nil || !worked {
				t.Fatalf("terminal ready process worked=%v err=%v", worked, err)
			}
			if status, attempts := deliveryStatus(t, ids[0]); status != tc.wantStatus || attempts != 1 {
				t.Fatalf("terminal ready status=%s attempts=%d", status, attempts)
			}
			var class string
			if err := testPool.QueryRow(context.Background(), `SELECT COALESCE(last_error_class,'') FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&class); err != nil {
				t.Fatal(err)
			}
			if class != tc.wantClass || reads != 1 {
				t.Fatalf("terminal ready class=%q reads=%d", class, reads)
			}
		})
	}
}

func TestWorkflowDeliveryWorkerRetriesOrderedMultiPRWithoutRepeatingMerge(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	merged := false
	mergeCalls := 0
	secondUnavailable := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token delivery-token" {
			t.Errorf("wrong provider credential")
		}
		if r.URL.Path == "/api/v1/repos/team/project/pulls/2" && r.Method == http.MethodGet && secondUnavailable {
			secondUnavailable = false
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/api/v1/repos/team/project/pulls/1/merge" && r.Method == http.MethodPost {
			mergeCalls++
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["head_commit_id"] != workflowDeliveryHead {
				t.Errorf("merge did not pin accepted SHA: %v, %v", input, err)
			}
			merged = true
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/repos/team/project/pulls/") {
			isFirst := strings.HasSuffix(r.URL.Path, "/1")
			state := "open"
			if isFirst && merged {
				state = "closed"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"title": "Ready work", "head": map[string]string{"sha": workflowDeliveryHead}, "draft": false, "merged": isFirst && merged, "state": state, "merge_commit_sha": workflowDeliveryChangedHead})
			return
		}
		t.Errorf("unexpected provider call %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", 500)
	}))
	defer server.Close()
	_, ids := workflowDeliveryFixture(t, server, 2, "ready")
	// The first PR is configured to merge; the second stays ready. This is an
	// explicitly ordered delivery record, so a failure on PR 2 cannot undo PR 1.
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET action='merge',merge_method='merge' WHERE id=$1`, ids[0])
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	for step := 0; step < 3; step++ {
		worked, err := worker.ProcessNext(context.Background())
		if err != nil || !worked {
			t.Fatalf("step %d worked=%v err=%v", step, worked, err)
		}
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "delivered" {
		t.Fatalf("first PR status=%q", status)
	}
	if status, _ := deliveryStatus(t, ids[1]); status != "retry" {
		t.Fatalf("second PR status=%q", status)
	}
	if mergeCalls != 1 {
		t.Fatalf("merge calls=%d, want 1", mergeCalls)
	}
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET next_attempt_at=now() WHERE id=$1`, ids[1])
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("retry worked=%v err=%v", worked, err)
	}
	if status, attempts := deliveryStatus(t, ids[1]); status != "delivered" || attempts != 2 {
		t.Fatalf("second PR status=%q attempts=%d", status, attempts)
	}
	if mergeCalls != 1 {
		t.Fatalf("completed merge repeated: %d calls", mergeCalls)
	}
}

func TestWorkflowDeliveryWorkerStaleHeadStopsLaterPRs(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	mutations := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "WIP: Changed", "head": map[string]string{"sha": workflowDeliveryChangedHead}, "draft": false, "merged": false, "state": "open"})
	}))
	defer server.Close()
	_, ids := workflowDeliveryFixture(t, server, 2, "ready")
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("stale worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "stale" {
		t.Fatalf("first PR status=%q", status)
	}
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || worked {
		t.Fatalf("later PR advanced after stale predecessor: worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[1]); status != "pending" {
		t.Fatalf("later PR status=%q", status)
	}
	if mutations != 0 {
		t.Fatalf("changed head triggered %d mutations", mutations)
	}
}

func TestWorkflowDeliveryWorkerReconcilesAmbiguousMergeWithoutSecondMutation(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	merged := false
	mergeCalls := 0
	failVerify := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/merge") {
			mergeCalls++
			merged, failVerify = true, true
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		if failVerify {
			failVerify = false
			http.Error(w, "provider unavailable after merge", http.StatusServiceUnavailable)
			return
		}
		state := "open"
		if merged {
			state = "closed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Ready work", "head": map[string]string{"sha": workflowDeliveryHead}, "draft": false, "merged": merged, "state": state, "merge_commit_sha": workflowDeliveryChangedHead})
	}))
	defer server.Close()
	_, ids := workflowDeliveryFixture(t, server, 1, "merge")
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET readiness_done_at=now() WHERE id=$1`, ids[0])
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("ambiguous attempt worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "retry" {
		t.Fatalf("ambiguous merge status=%q", status)
	}
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET next_attempt_at=now() WHERE id=$1`, ids[0])
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("reconcile worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "delivered" {
		t.Fatalf("reconciled status=%q", status)
	}
	if mergeCalls != 1 {
		t.Fatalf("ambiguous result repeated merge mutation: %d calls", mergeCalls)
	}
}

func TestWorkflowDeliveryWorkerWaitsForIssueAuthorityLock(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Error(w, "unexpected", 500) }))
	defer server.Close()
	issue, _ := workflowDeliveryFixture(t, server, 1, "ready")
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), `SELECT 1 FROM issue WHERE id=$1 FOR UPDATE`, issue); err != nil {
		t.Fatal(err)
	}
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || worked {
		t.Fatalf("locked issue worked=%v err=%v", worked, err)
	}
	if calls != 0 {
		t.Fatalf("provider called while rejection can hold issue lock")
	}
}

func TestWorkflowDeliveryWorkerRejectsPRURLOutsideTrustedBinding(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "unexpected", 500)
	}))
	defer server.Close()
	_, ids := workflowDeliveryFixture(t, server, 1, "ready")
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET repository_url='https://unbound.example/team/project',pr_url='https://unbound.example/team/project/pulls/1' WHERE id=$1`, ids[0])
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("unbound PR worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "blocked" {
		t.Fatalf("unbound PR status=%q", status)
	}
	if calls != 0 {
		t.Fatalf("unbound PR caused %d provider calls", calls)
	}
}

func TestWorkflowDeliveryWorkerUsesGitHubInstallationBinding(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	pemBytes, _ := generateTestRSAKeyPEM(t)
	t.Setenv("GITHUB_APP_ID", "424242")
	t.Setenv("GITHUB_APP_PRIVATE_KEY", string(pemBytes))
	const installationID int64 = 99665701
	tokenRequests, prReads := 0, 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/99665701/access_tokens":
			tokenRequests++
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				t.Error("installation token request lacked App JWT")
			}
			var body struct {
				Permissions map[string]string `json:"permissions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Permissions["pull_requests"] != "write" {
				t.Errorf("installation token request permissions=%v err=%v", body.Permissions, err)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"token":"installation-delivery-token"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/team/project/pulls/1":
			prReads++
			if r.Header.Get("Authorization") != "Bearer installation-delivery-token" {
				t.Error("PR read lacked installation token")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"title": "Ready work", "head": map[string]string{"sha": workflowDeliveryHead}, "draft": false, "merged": false, "state": "open"})
		default:
			t.Errorf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	previousBase := githubAPIBase
	githubAPIBase = server.URL
	t.Cleanup(func() { githubAPIBase = previousBase })
	_, ids := workflowDeliveryFixture(t, server, 1, "ready")
	installation := dbfx.Insert(t, "github_installation", testutil.Cols{
		"workspace_id": testWorkspaceID, "installation_id": installationID,
		"account_login": "team", "account_type": "Organization",
	})
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET provider='github',provider_binding_id=$2,
		pr_url=$3 WHERE id=$1`, ids[0], installation, server.URL+"/team/project/pull/1")
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("GitHub delivery worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "delivered" {
		t.Fatalf("GitHub status=%q", status)
	}
	if tokenRequests != 1 || prReads != 1 {
		t.Fatalf("token requests=%d PR reads=%d", tokenRequests, prReads)
	}
}

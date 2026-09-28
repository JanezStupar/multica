package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func workflowApproversRequest(method, workspaceID, connectionID, userID, body string) *http.Request {
	req := httptest.NewRequest(method, "/api/workspaces/"+workspaceID+"/vcs/connections/"+connectionID+"/workflow-approvers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("X-Workspace-ID", workspaceID)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	rctx.URLParams.Add("connectionId", connectionID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func workflowApproversCall(method, workspaceID, connectionID, userID, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := workflowApproversRequest(method, workspaceID, connectionID, userID, body)
	if method == http.MethodGet {
		testHandler.ListVCSWorkflowApprovers(w, req)
	} else {
		testHandler.ReplaceVCSWorkflowApprovers(w, req)
	}
	return w
}

func workflowApproversStored(t *testing.T, connectionID string) []VCSWorkflowApprover {
	t.Helper()
	var raw []byte
	if err := testPool.QueryRow(context.Background(), `SELECT workflow_approvers FROM vcs_connection WHERE id=$1`, connectionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var entries []VCSWorkflowApprover
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestVCSWorkflowApproversReplaceAndRead(t *testing.T) {
	box := withVCSBox(t)
	connectionID := seedVCSConnection(t, context.Background(), box, "forgejo", "https://approvers-replace.test")
	t.Cleanup(func() { cleanupVCS(context.Background(), "") })
	otherUser := dbfx.User(t, "Workflow Approver", "approver-replace@test.local")
	dbfx.Member(t, testWorkspaceID, otherUser, "member")
	adminUser := dbfx.User(t, "Workflow Admin", "approver-admin@test.local")
	dbfx.Member(t, testWorkspaceID, adminUser, "admin")

	initial := workflowApproversCall(http.MethodGet, testWorkspaceID, connectionID, testUserID, "")
	if initial.Code != http.StatusOK || strings.TrimSpace(initial.Body.String()) != `{"approvers":[]}` {
		t.Fatalf("initial GET: %d %s", initial.Code, initial.Body.String())
	}
	body := `{"approvers":[{"provider_user_id":"4815","member_id":"` + otherUser + `"}]}`
	put := workflowApproversCall(http.MethodPut, testWorkspaceID, connectionID, testUserID, body)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", put.Code, put.Body.String())
	}
	if got := workflowApproversStored(t, connectionID); len(got) != 1 || got[0].MemberID != otherUser || got[0].ProviderUserID != "4815" {
		t.Fatalf("stored mappings = %+v", got)
	}
	// One human can own several provider accounts; a replacement can bind both.
	second := `{"approvers":[{"provider_user_id":"4815","member_id":"` + otherUser + `"},{"provider_user_id":"4816","member_id":"` + otherUser + `"}]}`
	put = workflowApproversCall(http.MethodPut, testWorkspaceID, connectionID, adminUser, second)
	if put.Code != http.StatusOK || len(workflowApproversStored(t, connectionID)) != 2 {
		t.Fatalf("admin PUT with two provider identities: %d %s", put.Code, put.Body.String())
	}
	get := workflowApproversCall(http.MethodGet, testWorkspaceID, connectionID, adminUser, "")
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"provider_user_id":"4815"`) {
		t.Fatalf("GET after PUT: %d %s", get.Code, get.Body.String())
	}
	if strings.Contains(get.Body.String(), "access_token") || strings.Contains(get.Body.String(), "webhook_secret") {
		t.Fatalf("GET exposed a connection secret: %s", get.Body.String())
	}
	clear := workflowApproversCall(http.MethodPut, testWorkspaceID, connectionID, adminUser, `{"approvers":[]}`)
	if clear.Code != http.StatusOK || len(workflowApproversStored(t, connectionID)) != 0 {
		t.Fatalf("clear: %d %s", clear.Code, clear.Body.String())
	}
}

func TestVCSWorkflowApproversRejectInvalidAndOutOfScope(t *testing.T) {
	box := withVCSBox(t)
	connectionID := seedVCSConnection(t, context.Background(), box, "forgejo", "https://approvers-validation.test")
	gitlabID := seedVCSConnection(t, context.Background(), box, "gitlab", "https://approvers-gitlab.test")
	t.Cleanup(func() { cleanupVCS(context.Background(), "") })
	memberUser := dbfx.User(t, "Workflow Member", "approver-member@test.local")
	dbfx.Member(t, testWorkspaceID, memberUser, "member")
	foreignUser := dbfx.User(t, "Foreign Workflow Member", "approver-foreign@test.local")
	foreignWorkspace := dbfx.Workspace(t, "Foreign Approver Workspace", "foreign-approver-workspace")
	dbfx.Member(t, foreignWorkspace, foreignUser, "member")
	dbfx.Member(t, foreignWorkspace, testUserID, "owner")
	entry := `{"provider_user_id":"2","member_id":"` + testUserID + `"}`
	foreignEntry := `{"provider_user_id":"4","member_id":"` + foreignUser + `"}`
	for _, tc := range []struct {
		name string
		body string
	}{
		{"omitted", `{}`},
		{"null", `{"approvers":null}`},
		{"unknown field", `{"approvers":[],"ignore":true}`},
		{"entry unknown field", `{"approvers":[{"provider_user_id":"2","member_id":"` + testUserID + `","login":"mutable"}]}`},
		{"trailing json", `{"approvers":[]} {}`},
		{"empty provider id", `{"approvers":[{"provider_user_id":"","member_id":"` + testUserID + `"}]}`},
		{"leading zero", `{"approvers":[{"provider_user_id":"02","member_id":"` + testUserID + `"}]}`},
		{"provider login", `{"approvers":[{"provider_user_id":"alice","member_id":"` + testUserID + `"}]}`},
		{"duplicate provider id", `{"approvers":[` + entry + `,{"provider_user_id":"2","member_id":"` + memberUser + `"}]}`},
		{"foreign member", `{"approvers":[` + foreignEntry + `]}`},
		{"not a member", `{"approvers":[{"provider_user_id":"7","member_id":"11111111-1111-1111-1111-111111111111"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := workflowApproversCall(http.MethodPut, testWorkspaceID, connectionID, testUserID, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
			}
			if len(workflowApproversStored(t, connectionID)) != 0 {
				t.Fatal("invalid PUT changed the mapping")
			}
		})
	}
	plainMember := workflowApproversCall(http.MethodGet, testWorkspaceID, connectionID, memberUser, "")
	if plainMember.Code != http.StatusForbidden {
		t.Fatalf("ordinary member GET: %d %s", plainMember.Code, plainMember.Body.String())
	}
	plainMember = workflowApproversCall(http.MethodPut, testWorkspaceID, connectionID, memberUser, `{"approvers":[]}`)
	if plainMember.Code != http.StatusForbidden {
		t.Fatalf("ordinary member PUT: %d %s", plainMember.Code, plainMember.Body.String())
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		body := `{"approvers":[]}`
		if method == http.MethodGet {
			body = ""
		}
		if w := workflowApproversCall(method, foreignWorkspace, connectionID, testUserID, body); w.Code != http.StatusNotFound {
			t.Fatalf("cross-workspace %s: %d %s", method, w.Code, w.Body.String())
		}
		if w := workflowApproversCall(method, testWorkspaceID, gitlabID, testUserID, body); w.Code != http.StatusNotFound {
			t.Fatalf("non-Forgejo %s: %d %s", method, w.Code, w.Body.String())
		}
	}
}

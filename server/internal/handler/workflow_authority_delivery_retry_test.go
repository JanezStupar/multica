package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func TestIssueWorkflowDeliveryRetryPreservesAcceptanceAndAttempts(t *testing.T) {
	issueID, candidateID, revision := workflowExceptionTestCandidate(t)
	withVCSBox(t)
	providerCalls := 0
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/team/project/pulls/1" {
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Ready work", "head": map[string]string{"sha": workflowDeliveryHead},
			"draft": false, "merged": false, "state": "open"})
	}))
	defer provider.Close()
	token, err := testHandler.sealVCSSecret("delivery-token")
	if err != nil {
		t.Fatal(err)
	}
	bindingID := dbfx.Insert(t, "vcs_connection", testutil.Cols{
		"workspace_id": testWorkspaceID, "provider": "gitea", "instance_url": provider.URL,
		"account_login": "retry fixture", "access_token_encrypted": token, "webhook_secret_encrypted": token,
	})
	policy, err := testHandler.Queries.GetIssueInWorkspace(context.Background(), db.GetIssueInWorkspaceParams{
		ID: parseUUID(issueID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := testHandler.TaskService.DecodeIssueWorkflowPolicy(policy.WorkflowPolicy)
	if err != nil || pinned == nil {
		t.Fatalf("decode workflow policy: %v", err)
	}
	snapshot, _ := json.Marshal(map[string]any{"scope_digest": service.WorkflowScopeDigest(policy, pinned.Version),
		"policy_version": pinned.Version})
	acceptanceID := dbfx.Insert(t, "issue_workflow_acceptance", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": issueID, "candidate_id": candidateID,
		"mode": "human", "actor_type": "member", "actor_id": testUserID, "state": "accepted",
		"issue_revision": revision, "policy_version": pinned.Version, "authority_snapshot": snapshot,
		"accepted_at": testutil.Raw("now()"),
	})
	dbfx.Exec(t, `UPDATE issue SET status='done' WHERE id=$1`, issueID)
	deliveryID := dbfx.Insert(t, "issue_workflow_delivery", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": issueID,
		"acceptance_id": acceptanceID, "candidate_id": candidateID, "ordinal": 0,
		"provider": "forgejo", "provider_binding_id": bindingID,
		"repository_url": provider.URL + "/team/project", "pr_url": provider.URL + "/team/project/pulls/1",
		"repo_owner": "team", "repo_name": "project", "pr_number": 1,
		"expected_head_sha": workflowDeliveryHead, "action": "ready", "status": "blocked",
		"attempt_count": 1, "last_error_class": "provider_configuration_invalid",
	})
	dbfx.Insert(t, "issue_workflow_delivery_attempt", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": issueID,
		"delivery_id": deliveryID, "attempt_number": 1, "operation": "prepare",
		"outcome": "blocked", "error_class": "provider_configuration_invalid",
	})
	svc := testHandler.workflowAuthorityService()
	initial, err := svc.ReadState(context.Background(), parseUUID(testWorkspaceID), parseUUID(issueID),
		service.WorkflowActor{Type: "member", ID: testUserID})
	if err != nil || len(initial.Delivery) != 1 || !initial.Delivery[0].Retryable {
		t.Fatalf("blocked delivery retry availability: state=%+v err=%v", initial.Delivery, err)
	}
	request := func(candidate string, expected int64, id string) *http.Request {
		req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow/delivery/"+id+"/retry",
			map[string]any{"candidate_id": candidate, "expected_revision": expected, "reason": "Provider binding repaired"}), "id", issueID)
		chi.RouteContext(req.Context()).URLParams.Add("deliveryID", id)
		return req
	}
	testutil.Call(t, testHandler.RetryIssueWorkflowDelivery, request(candidateID, revision+1, deliveryID)).Want(http.StatusConflict)
	testutil.Call(t, testHandler.RetryIssueWorkflowDelivery, request(uuidToString(dbid.NewV7()), revision, deliveryID)).Want(http.StatusConflict)
	testutil.Call(t, testHandler.RetryIssueWorkflowDelivery, request(candidateID, revision, uuidToString(dbid.NewV7()))).Want(http.StatusConflict)
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET state='requested' WHERE id=$1`, acceptanceID)
	testutil.Call(t, testHandler.RetryIssueWorkflowDelivery, request(candidateID, revision, deliveryID)).Want(http.StatusConflict)
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET state='accepted' WHERE id=$1`, acceptanceID)
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET status='stale' WHERE id=$1`, deliveryID)
	testutil.Call(t, testHandler.RetryIssueWorkflowDelivery, request(candidateID, revision, deliveryID)).Want(http.StatusConflict)
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET status='blocked' WHERE id=$1`, deliveryID)
	changedHeadAttempt := dbfx.Insert(t, "issue_workflow_delivery_attempt", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": issueID,
		"delivery_id": deliveryID, "attempt_number": 2, "operation": "prepare",
		"outcome": "stale", "observed_head_sha": workflowDeliveryChangedHead,
	})
	testutil.Call(t, testHandler.RetryIssueWorkflowDelivery, request(candidateID, revision, deliveryID)).Want(http.StatusConflict)
	staleHeadState, err := svc.ReadState(context.Background(), parseUUID(testWorkspaceID), parseUUID(issueID),
		service.WorkflowActor{Type: "member", ID: testUserID})
	if err != nil || len(staleHeadState.Delivery) != 1 || staleHeadState.Delivery[0].Retryable {
		t.Fatalf("known different head appeared retryable: state=%+v err=%v", staleHeadState.Delivery, err)
	}
	dbfx.Exec(t, `DELETE FROM issue_workflow_delivery_attempt WHERE id=$1`, changedHeadAttempt)
	otherMember := createTestUserAndMember(t, "member")
	memberRequest := request(candidateID, revision, deliveryID)
	memberRequest.Header.Set("X-User-ID", otherMember)
	testutil.Call(t, testHandler.RetryIssueWorkflowDelivery, memberRequest).Want(http.StatusForbidden)
	memberState, err := svc.ReadState(context.Background(), parseUUID(testWorkspaceID), parseUUID(issueID),
		service.WorkflowActor{Type: "member", ID: otherMember})
	if err != nil || len(memberState.Delivery) != 1 || memberState.Delivery[0].Retryable {
		t.Fatalf("non-recipient appeared retryable: state=%+v err=%v", memberState.Delivery, err)
	}
	dbfx.Exec(t, `UPDATE vcs_connection SET provider='forgejo' WHERE id=$1`, bindingID)
	var state service.WorkflowState
	testutil.Call(t, testHandler.RetryIssueWorkflowDelivery, request(candidateID, revision, deliveryID)).Want(http.StatusOK).JSON(&state)
	if len(state.Delivery) != 1 || state.Delivery[0].Status != "retry" || state.Delivery[0].AttemptCount != 1 ||
		state.Delivery[0].LastErrorClass != "" {
		t.Fatalf("delivery retry lost original intent or attempts: %+v", state.Delivery)
	}
	testutil.Call(t, testHandler.RetryIssueWorkflowDelivery, request(candidateID, revision, deliveryID)).Want(http.StatusOK)
	if count := dbfx.Count(t, `SELECT count(*) FROM activity_log WHERE issue_id=$1 AND action='workflow_delivery_retried'`, issueID); count != 1 {
		t.Fatalf("idempotent retry wrote %d activity entries", count)
	}
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = provider.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("restored binding was not eligible: worked=%v err=%v", worked, err)
	}
	if status, attempts := deliveryStatus(t, deliveryID); status != "delivered" || attempts != 2 {
		t.Fatalf("delivery status=%s attempts=%d after retry", status, attempts)
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls=%d, want one after repair", providerCalls)
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery_attempt WHERE delivery_id=$1`, deliveryID); count != 2 {
		t.Fatalf("delivery attempt history count=%d", count)
	}
}

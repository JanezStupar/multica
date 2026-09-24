package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/workflowdelivery"
	"github.com/multica-ai/multica/server/internal/service"
)

func TestWorkflowReviewEvidenceUsesCurrentBindingAndExactCandidateHead(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "token delivery-token" {
			t.Error("review API request lacked trusted binding token")
		}
		switch r.URL.Path {
		case "/api/v1/repos/team/project/pulls/1":
			_ = json.NewEncoder(w).Encode(map[string]any{"title": "Ready work", "head": map[string]string{"sha": workflowDeliveryHead}, "draft": false, "merged": false, "state": "open"})
		case "/api/v1/repos/team/project/pulls/1/reviews":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 80, "html_url": "https://" + r.Host + "/team/project/pulls/1#issuecomment-80", "commit_id": workflowDeliveryHead, "state": "APPROVED", "user": map[string]any{"id": 22}}})
		default:
			t.Errorf("unexpected review API request %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	_, ids := workflowDeliveryFixture(t, server, 1, "ready")
	var binding pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `SELECT provider_binding_id FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	input := service.WorkflowReviewEvidenceInput{WorkspaceID: parseUUID(testWorkspaceID),
		PRs: []service.WorkflowReviewPR{{Provider: "forgejo", BindingID: binding,
			RepositoryURL: server.URL + "/team/project", PRURL: server.URL + "/team/project/pulls/1",
			Owner: "team", Repo: "project", Number: 1, ExpectedHeadSHA: workflowDeliveryHead}},
		ReviewURLs: []string{server.URL + "/team/project/pulls/1#issuecomment-80"}}
	previous := testHandler.WorkflowDeliveryWorker.client
	testHandler.WorkflowDeliveryWorker.client = server.Client()
	t.Cleanup(func() { testHandler.WorkflowDeliveryWorker.client = previous })
	if err := testHandler.verifyWorkflowReviewEvidence(context.Background(), input); err != nil {
		t.Fatalf("verified review: %v", err)
	}
	if calls != 2 {
		t.Fatalf("provider calls=%d; want PR and review only", calls)
	}
	input.ReviewURLs = []string{"https://unbound.example/team/project/pulls/1#issuecomment-80"}
	if err := testHandler.verifyWorkflowReviewEvidence(context.Background(), input); !errors.Is(err, workflowdelivery.ErrInvalid) {
		t.Fatalf("foreign review URL error=%v", err)
	}
	if calls != 2 {
		t.Fatalf("foreign review URL triggered provider request: %d", calls)
	}
	input.ReviewURLs = []string{server.URL + "/team/project/pulls/1#issuecomment-80", server.URL + "/team/project/pulls/2#issuecomment-81"}
	if err := testHandler.verifyWorkflowReviewEvidence(context.Background(), input); !errors.Is(err, workflowdelivery.ErrInvalid) {
		t.Fatalf("different PR URL error=%v", err)
	}
	if calls != 2 {
		t.Fatalf("different PR URL triggered provider request: %d", calls)
	}
}

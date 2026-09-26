package handler

import (
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestIssueWorkflowAuthorityEndpointsFailClosedForMalformedAndUnenrolled(t *testing.T) {
	issueID := dbfx.Issue(t, "Workflow authority admission")
	call := func(method, path string, body any, handle http.HandlerFunc) *testutil.Response {
		t.Helper()
		return testutil.Call(t, handle, withURLParam(newRequest(method, path, body), "id", issueID))
	}
	call(http.MethodGet, "/api/issues/"+issueID+"/workflow", nil, testHandler.GetIssueWorkflow).Want(http.StatusNotFound)
	call(http.MethodPost, "/api/issues/"+issueID+"/workflow/reviews",
		map[string]any{"candidate_id": "not-a-uuid", "verdict": "pass", "pr_review_urls": []string{}},
		testHandler.RegisterIssueWorkflowReview).Want(http.StatusBadRequest)
	call(http.MethodPost, "/api/issues/"+issueID+"/workflow/acceptances",
		map[string]any{"candidate_id": "not-a-uuid", "expected_revision": 1},
		testHandler.AcceptIssueWorkflow).Want(http.StatusBadRequest)
	call(http.MethodPost, "/api/issues/"+issueID+"/workflow/rejections",
		map[string]any{"candidate_id": "not-a-uuid", "expected_revision": 1, "kind": "scope_change", "reason": "changed"},
		testHandler.RejectIssueWorkflow).Want(http.StatusBadRequest)
	call(http.MethodPost, "/api/issues/"+issueID+"/workflow/feedback-continuations",
		map[string]any{"candidate_id": "not-a-uuid", "expected_revision": 1, "comment_id": "not-a-uuid", "kind": "in_scope_defect"},
		testHandler.ContinueIssueWorkflowFeedback).Want(http.StatusBadRequest)
	call(http.MethodPost, "/api/issues/"+issueID+"/workflow/acceptances",
		map[string]any{"candidate_id": "00000000-0000-0000-0000-000000000001", "expected_revision": 1, "unexpected": true},
		testHandler.AcceptIssueWorkflow).Want(http.StatusBadRequest)
}

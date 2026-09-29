package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func humanDoneIssue(t *testing.T, frozen bool) string {
	t.Helper()
	id := dbfx.Issue(t, "Human Done decision", testutil.Cols{
		"status": "in_review", "workflow_policy": testutil.Raw(`'{"version":"pinned-v1"}'::jsonb`),
	})
	if frozen {
		dbfx.Exec(t, `UPDATE issue SET workflow_frozen=true WHERE id=$1`, id)
	}
	return id
}

func humanStatusRequest(t *testing.T, issueID string, body map[string]any) *testutil.Response {
	t.Helper()
	return testutil.Call(t, testHandler.UpdateIssue,
		withURLParam(newRequest(http.MethodPut, "/api/issues/"+issueID, body), "id", issueID))
}

func assertHumanStatusDecision(t *testing.T, issueID, status string, count int) {
	t.Helper()
	var got string
	dbfx.QueryRow(t, `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&got)
	if got != status {
		t.Fatalf("issue status = %s, want %s", got, status)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM activity_log WHERE issue_id=$1 AND action='workflow_human_status_decision'
		AND actor_type='member' AND actor_id=$2 AND details->>'transaction_id'<>''`, issueID, testUserID); n != count {
		t.Fatalf("human decision receipts = %d, want %d", n, count)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, issueID); n != 0 {
		t.Fatalf("human status decision fabricated %d acceptances", n)
	}
	if n := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_rejection WHERE issue_id=$1`, issueID); n != 0 {
		t.Fatalf("human status decision fabricated %d rejections", n)
	}
}

func TestHumanDoneNormalStatusPaths(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		id := humanDoneIssue(t, false)
		humanStatusRequest(t, id, map[string]any{"status": "done"}).Want(http.StatusOK)
		assertHumanStatusDecision(t, id, "done", 1)
	})
	t.Run("atomic text and status", func(t *testing.T) {
		id := humanDoneIssue(t, false)
		humanStatusRequest(t, id, map[string]any{"status": "done", "title": "QA complete", "description": "Verified"}).Want(http.StatusOK)
		assertHumanStatusDecision(t, id, "done", 1)
		var title string
		dbfx.QueryRow(t, `SELECT title FROM issue WHERE id=$1`, id).Scan(&title)
		if title != "QA complete" {
			t.Fatalf("atomic title = %q", title)
		}
	})
	t.Run("batch", func(t *testing.T) {
		first, second := humanDoneIssue(t, false), humanDoneIssue(t, false)
		testutil.Call(t, testHandler.BatchUpdateIssues, newRequest(http.MethodPost, "/api/issues/batch-update",
			map[string]any{"issue_ids": []string{first, second}, "updates": map[string]any{"status": "done"}})).Want(http.StatusOK)
		assertHumanStatusDecision(t, first, "done", 1)
		assertHumanStatusDecision(t, second, "done", 1)
	})
	t.Run("custom done category", func(t *testing.T) {
		id := humanDoneIssue(t, false)
		key := "qa_done_" + id[:8]
		dbfx.Insert(t, "issue_status", testutil.Cols{"workspace_id": testWorkspaceID,
			"key": key, "name": "QA Done", "category": "done", "color": "#123456"})
		humanStatusRequest(t, id, map[string]any{"status": key}).Want(http.StatusOK)
		assertHumanStatusDecision(t, id, key, 1)
	})
	t.Run("frozen status only", func(t *testing.T) {
		id := humanDoneIssue(t, true)
		humanStatusRequest(t, id, map[string]any{"status": "done", "title": "scope change"}).Want(http.StatusConflict)
		assertHumanStatusDecision(t, id, "in_review", 0)
		humanStatusRequest(t, id, map[string]any{"status": "done"}).Want(http.StatusOK)
		assertHumanStatusDecision(t, id, "done", 1)
	})
	t.Run("frozen batch preflight", func(t *testing.T) {
		ordinary, frozen := humanDoneIssue(t, false), humanDoneIssue(t, true)
		testutil.Call(t, testHandler.BatchUpdateIssues, newRequest(http.MethodPost, "/api/issues/batch-update",
			map[string]any{"issue_ids": []string{ordinary, frozen}, "updates": map[string]any{"status": "done", "title": "scope change"}})).Want(http.StatusConflict)
		assertHumanStatusDecision(t, ordinary, "in_review", 0)
		assertHumanStatusDecision(t, frozen, "in_review", 0)
		testutil.Call(t, testHandler.BatchUpdateIssues, newRequest(http.MethodPost, "/api/issues/batch-update",
			map[string]any{"issue_ids": []string{ordinary, frozen}, "updates": map[string]any{"status": "done"}})).Want(http.StatusOK)
		assertHumanStatusDecision(t, ordinary, "done", 1)
		assertHumanStatusDecision(t, frozen, "done", 1)
	})
}

func TestHumanDoneMachineAndStaleRequestsDoNotGainAuthority(t *testing.T) {
	for _, source := range []string{"task_token", "cloud_pat"} {
		t.Run(source, func(t *testing.T) {
			id := humanDoneIssue(t, false)
			req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+id, map[string]any{"status": "done"}), "id", id)
			req.Header.Set("X-Actor-Source", source)
			testutil.Call(t, testHandler.UpdateIssue, req).Want(http.StatusConflict)
			assertHumanStatusDecision(t, id, "in_review", 0)
		})
	}
	id := humanDoneIssue(t, false)
	humanStatusRequest(t, id, map[string]any{"status": "done", "expected_revision": 999999}).Want(http.StatusConflict)
	assertHumanStatusDecision(t, id, "in_review", 0)
}

func TestHumanDoneReopenNeedsCurrentHumanDecision(t *testing.T) {
	id := humanDoneIssue(t, false)
	humanStatusRequest(t, id, map[string]any{"status": "done"}).Want(http.StatusOK)
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET status='in_review',revision=revision+1 WHERE id=$1`, id); err == nil {
		t.Fatal("background SQL reopened a human-completed issue")
	}
	assertHumanStatusDecision(t, id, "done", 1)
	for _, source := range []string{"task_token", "cloud_pat"} {
		req := withURLParam(newRequest(http.MethodPut, "/api/issues/"+id, map[string]any{"status": "in_review"}), "id", id)
		req.Header.Set("X-Actor-Source", source)
		testutil.Call(t, testHandler.UpdateIssue, req).Want(http.StatusConflict)
	}
	assertHumanStatusDecision(t, id, "done", 1)
	humanStatusRequest(t, id, map[string]any{"status": "in_review"}).Want(http.StatusOK)
	assertHumanStatusDecision(t, id, "in_review", 2)
}

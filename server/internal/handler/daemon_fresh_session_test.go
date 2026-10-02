package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestBeginFreshTaskSessionClearsRetainedLineageBeforeLatePin(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	f := createCommentDeliveryFixture(t, "fresh fallback lineage")
	source := dbfx.Task(t, f.agentID, testutil.Cols{
		"issue_id": f.issueID, "runtime_id": f.runtimeID, "status": "completed",
		"session_id": "abandoned-session", "completed_at": testutil.Raw("now()-interval '1 minute'"),
	})
	claimCommentDeliveryFixture(t, f, protocol.DaemonCapabilityCoalescedCommentsV1+","+protocol.DaemonCapabilityRetainedContextResetV1)
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='running',session_id='abandoned-session',wakeup_resume_from_task_id=$2 WHERE id=$1", f.taskID, source)
	before, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if before.CommentResumeFromTaskID != parseUUID(source) {
		t.Fatalf("claim did not establish retained source: %v", before.CommentResumeFromTaskID)
	}
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+f.taskID+"/session/fresh", protocol.FreshTaskSessionRequest{
		RuntimeID: f.runtimeID, DispatchedAt: before.DispatchedAt.Time.Format(time.RFC3339Nano),
	}, testWorkspaceID, "fresh-fallback")
	testHandler.BeginFreshTaskSession(w, withURLParam(req, "taskId", f.taskID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("fresh reset: %d: %s", w.Code, w.Body.String())
	}
	cleared, err := testHandler.Queries.GetAgentTask(ctx, before.ID)
	if err != nil || !cleared.RetainedContextInvalidated || cleared.CommentResumeFromTaskID.Valid || cleared.WakeupResumeFromTaskID.Valid || cleared.SessionID.Valid || cleared.Status != "running" {
		t.Fatalf("fresh reset did not synchronously revoke ancestry: task=%+v err=%v", cleared, err)
	}
	// An old-phase straggler must not occupy the fresh session slot.
	pinTaskSessionViaAPI(t, f.taskID, "fresh-fallback", "abandoned-session", "")
	after, err := testHandler.Queries.GetAgentTask(ctx, before.ID)
	if err != nil || !after.RetainedContextInvalidated || after.CommentResumeFromTaskID.Valid || after.WakeupResumeFromTaskID.Valid || after.SessionID.Valid || after.Status != "running" {
		t.Fatalf("late old pin occupied the fresh context slot: task=%+v err=%v", after, err)
	}
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+f.taskID+"/session", PinTaskSessionRequest{
		SessionID: "fresh-session", WorkDir: "/tmp/fresh-session-workdir", AfterFreshReset: true,
	}, testWorkspaceID, "fresh-fallback")
	testHandler.PinTaskSession(w, withURLParam(req, "taskId", f.taskID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("fresh phase pin: %d: %s", w.Code, w.Body.String())
	}
	// The reversed delivery order is equally safe: old context cannot replace
	// the new crash-recovery pointer once it is pinned.
	pinTaskSessionViaAPI(t, f.taskID, "fresh-fallback", "abandoned-session", "")
	after, err = testHandler.Queries.GetAgentTask(ctx, before.ID)
	if err != nil || after.SessionID.String != "fresh-session" || !after.RetainedContextInvalidated {
		t.Fatalf("old phase overwrote fresh pin: task=%+v err=%v", after, err)
	}
	// A repeated reset cannot reuse the phase transition and clear that pin.
	w = httptest.NewRecorder()
	req = newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+f.taskID+"/session/fresh", protocol.FreshTaskSessionRequest{
		RuntimeID: f.runtimeID, DispatchedAt: before.DispatchedAt.Time.Format(time.RFC3339Nano),
	}, testWorkspaceID, "fresh-fallback")
	testHandler.BeginFreshTaskSession(w, withURLParam(req, "taskId", f.taskID))
	if w.Code != http.StatusConflict {
		t.Fatalf("repeated reset: %d: %s", w.Code, w.Body.String())
	}
	if _, err := testHandler.Queries.RecoverOrphanedTasksForRuntime(ctx, before.RuntimeID); err != nil {
		t.Fatal(err)
	}
	resume, err := testHandler.Queries.GetLastTaskSession(ctx, db.GetLastTaskSessionParams{AgentID: before.AgentID, IssueID: before.IssueID})
	if err != nil || resume.ID != before.ID || resume.SessionID.String != "fresh-session" {
		t.Fatalf("crash recovery selected abandoned provider context: resume=%+v err=%v", resume, err)
	}
}

func TestBeginFreshTaskSessionRejectsStaleOrForeignAttempt(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, tc := range []struct {
		name string
		want int
	}{
		{"stale generation", http.StatusConflict},
		{"different runtime", http.StatusConflict},
		{"terminal task", http.StatusConflict},
		{"malformed generation", http.StatusBadRequest},
		{"foreign workspace", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f := createCommentDeliveryFixture(t, tc.name)
			claimCommentDeliveryFixture(t, f, protocol.DaemonCapabilityCoalescedCommentsV1)
			dbfx.Exec(t, "UPDATE agent_task_queue SET status='running',session_id='must-remain' WHERE id=$1", f.taskID)
			before, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(f.taskID))
			if err != nil {
				t.Fatal(err)
			}
			body := protocol.FreshTaskSessionRequest{RuntimeID: f.runtimeID, DispatchedAt: before.DispatchedAt.Time.Format(time.RFC3339Nano)}
			workspace := testWorkspaceID
			switch tc.name {
			case "stale generation":
				body.DispatchedAt = before.DispatchedAt.Time.Add(-time.Second).Format(time.RFC3339Nano)
			case "different runtime":
				body.RuntimeID = createClaimReclaimRuntime(t, ctx, "different fresh runtime")
			case "terminal task":
				dbfx.Exec(t, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", f.taskID)
			case "malformed generation":
				body.DispatchedAt = "not-a-timestamp"
			case "foreign workspace":
				workspace = "00000000-0000-0000-0000-000000000001"
			}
			w := httptest.NewRecorder()
			req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+f.taskID+"/session/fresh", body, workspace, "fresh-fallback")
			testHandler.BeginFreshTaskSession(w, withURLParam(req, "taskId", f.taskID))
			if w.Code != tc.want {
				t.Fatalf("fresh reset: got %d want %d: %s", w.Code, tc.want, w.Body.String())
			}
			after, err := testHandler.Queries.GetAgentTask(ctx, before.ID)
			if err != nil || after.RetainedContextInvalidated || after.SessionID.String != "must-remain" {
				t.Fatalf("rejected reset mutated another attempt: task=%+v err=%v", after, err)
			}
		})
	}
}

func TestPinTaskSessionRejectsUnacknowledgedFreshPhase(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	f := createCommentDeliveryFixture(t, "unacknowledged fresh phase")
	claimCommentDeliveryFixture(t, f, protocol.DaemonCapabilityCoalescedCommentsV1)
	dbfx.Exec(t, "UPDATE agent_task_queue SET status='running',session_id='original-session' WHERE id=$1", f.taskID)
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/tasks/"+f.taskID+"/session", PinTaskSessionRequest{
		SessionID: "unacknowledged-fresh-session", AfterFreshReset: true,
	}, testWorkspaceID, "fresh-fallback")
	testHandler.PinTaskSession(w, withURLParam(req, "taskId", f.taskID))
	if w.Code != http.StatusNoContent {
		t.Fatalf("discarded phase pin: %d: %s", w.Code, w.Body.String())
	}
	stored, err := testHandler.Queries.GetAgentTask(context.Background(), parseUUID(f.taskID))
	if err != nil || stored.RetainedContextInvalidated || stored.SessionID.String != "original-session" {
		t.Fatalf("unacknowledged phase replaced original session: task=%+v err=%v", stored, err)
	}
}

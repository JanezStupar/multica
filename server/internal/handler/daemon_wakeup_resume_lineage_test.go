package handler

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgtype"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestClaimDeferredWakeupRetainsExactHandoffAncestryForLaterComment(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	f := createCommentDeliveryFixture(t, "deferred wakeup ancestry")
	recipient := dbfx.Task(t, f.agentID, testutil.Cols{
		"issue_id": f.issueID, "runtime_id": f.runtimeID, "status": "completed", "force_fresh_session": true,
		"session_id": "scheduled-recipient-session", "created_at": testutil.Raw("now()-interval '10 minutes'"),
		"started_at": testutil.Raw("now()-interval '9 minutes'"), "completed_at": testutil.Raw("now()-interval '8 minutes'"),
	})
	dbfx.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(),
		"workspace_id": testWorkspaceID, "issue_id": f.issueID, "agent_id": f.agentID, "created_by": testUserID,
		"instruction": "handoff", "kind": "event", "mode": "once", "handoff": map[string]any{}, "last_task_id": recipient,
	})
	wakeup := dbfx.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(),
		"workspace_id": testWorkspaceID, "issue_id": f.issueID, "agent_id": f.agentID, "created_by": testUserID,
		"instruction": "continue when available", "kind": "at", "mode": "once", "enabled": false, "last_task_id": f.taskID, "parent_comment_id": f.commentID[0],
	})
	dbfx.Insert(t, "issue_wakeup_receipt", testutil.Cols{"id": dbid.NewV7(),
		"wakeup_id": wakeup, "revision": int64(1), "event_key": "scheduled", "event_type": "time.due", "payload": map[string]any{},
		"task_id": f.taskID, "processed_at": testutil.Raw("now()-interval '8 days'"), "created_at": testutil.Raw("now()-interval '8 days'"),
	})
	dbfx.Exec(t, `UPDATE agent_task_queue SET created_at=now()-interval '20 minutes',trigger_comment_id=$4::uuid,coalesced_comment_ids='{}',
  context=jsonb_build_object('wakeup_id',$2::text,'wakeup_revision',1),originator_user_id=$3,accountable_user_id=$3,trigger_evidence_kind='issue_wakeup',trigger_evidence_ref_id=$2::uuid
  WHERE id=$1`, f.taskID, wakeup, testUserID, f.commentID[0])
	cutoff := pgtype.Timestamptz{Time: time.Now().Add(-7 * 24 * time.Hour), Valid: true}
	if _, err := testHandler.Queries.DeleteExpiredWakeupReceipts(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	var receiptCount int
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM issue_wakeup_receipt WHERE wakeup_id=$1", wakeup).Scan(&receiptCount); err != nil || receiptCount != 1 {
		t.Fatalf("offline queued wakeup lost claim evidence: count=%d err=%v", receiptCount, err)
	}
	caps := protocol.DaemonCapabilityCoalescedCommentsV1 + "," + protocol.DaemonCapabilityRetainedContextResetV1
	wakeResp := claimCommentDeliveryFixture(t, f, caps)
	stored, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.WakeupResumeFromTaskID != parseUUID(recipient) || stored.CommentResumeFromTaskID.Valid || wakeResp.PriorSessionID != "scheduled-recipient-session" {
		t.Fatalf("older-created scheduled task lost actual retained recipient: wakeup=%v comment=%v prior=%q", stored.WakeupResumeFromTaskID, stored.CommentResumeFromTaskID, wakeResp.PriorSessionID)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',started_at=now()-interval '1 minute',completed_at=now(),session_id=$2 WHERE id=$1`, f.taskID, wakeResp.PriorSessionID)
	if _, err := testHandler.Queries.DeleteExpiredWakeupReceipts(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, "SELECT count(*) FROM issue_wakeup_receipt WHERE wakeup_id=$1", wakeup).Scan(&receiptCount); err != nil || receiptCount != 0 {
		t.Fatalf("completed wakeup retained expired operational evidence: count=%d err=%v", receiptCount, err)
	}
	commentTask := dbfx.Task(t, f.agentID, testutil.Cols{"issue_id": f.issueID, "runtime_id": f.runtimeID, "trigger_comment_id": f.commentID[2]})
	f.taskID = commentTask
	commentResp := claimCommentDeliveryFixture(t, f, caps)
	comment, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(commentTask))
	if err != nil {
		t.Fatal(err)
	}
	if comment.CommentResumeFromTaskID != stored.ID || commentResp.PriorSessionID != wakeResp.PriorSessionID {
		t.Fatalf("later comment did not retain actual scheduled run: source=%v want=%v", comment.CommentResumeFromTaskID, stored.ID)
	}
}

func TestClaimInternalChildCompletionKeepsIndependentAuthorityUnderActiveHandoff(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	f := createCommentDeliveryFixture(t, "independent child completion")
	dbfx.Exec(t, "UPDATE issue SET assignee_type='agent',assignee_id=$2 WHERE id=$1", f.issueID, f.agentID)
	recipient := dbfx.Task(t, f.agentID, testutil.Cols{
		"issue_id": f.issueID, "runtime_id": f.runtimeID, "status": "completed", "force_fresh_session": true,
		"session_id": "current-handoff-recipient-session", "completed_at": testutil.Raw("now()-interval '10 minutes'"),
	})
	source := dbfx.Task(t, f.agentID, testutil.Cols{
		"issue_id": f.issueID, "runtime_id": f.runtimeID, "status": "completed",
		"session_id": "independent-parent-session", "originator_user_id": testUserID, "accountable_user_id": testUserID,
		"completed_at": testutil.Raw("now()-interval '1 minute'"),
	})
	dbfx.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(),
		"workspace_id": testWorkspaceID, "issue_id": f.issueID, "agent_id": f.agentID, "created_by": testUserID,
		"instruction": "current unrelated handoff", "kind": "event", "mode": "once", "handoff": map[string]any{}, "last_task_id": recipient,
	})
	child := dbfx.Issue(t, "completed child input", testutil.Cols{"parent_issue_id": f.issueID})
	wakeup := dbfx.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(),
		"workspace_id": testWorkspaceID, "issue_id": f.issueID, "agent_id": f.agentID, "created_by": testUserID,
		"instruction": "child input", "kind": "event", "mode": "once", "enabled": false, "last_task_id": f.taskID,
		"source_task_id": source, "child_issue_id": child, "child_completion_revision": int64(1),
	})
	dbfx.Insert(t, "issue_wakeup_receipt", testutil.Cols{"id": dbid.NewV7(),
		"wakeup_id": wakeup, "revision": int64(1), "event_key": "child completed", "event_type": "issue.completed", "payload": map[string]any{},
		"task_id": f.taskID, "processed_at": testutil.Raw("now()"),
	})
	dbfx.Exec(t, `UPDATE agent_task_queue SET trigger_comment_id=NULL,coalesced_comment_ids='{}',
  context=jsonb_build_object('wakeup_id',$2::text,'wakeup_revision',1),originator_user_id=$3,accountable_user_id=$3,
  trigger_evidence_kind='issue_wakeup',trigger_evidence_ref_id=$2::uuid,delegated_from_task_id=$4
  WHERE id=$1`, f.taskID, wakeup, testUserID, source)
	caps := protocol.DaemonCapabilityCoalescedCommentsV1 + "," + protocol.DaemonCapabilityRetainedContextResetV1
	resp := claimCommentDeliveryFixture(t, f, caps)
	stored, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if resp.PriorSessionID != "independent-parent-session" || stored.WakeupResumeFromTaskID.Valid || stored.CommentResumeFromTaskID.Valid || stored.Status != "dispatched" {
		t.Fatalf("independent child input lost claim authority or borrowed generic ancestry: prior=%q wakeup=%v comment=%v status=%s", resp.PriorSessionID, stored.WakeupResumeFromTaskID, stored.CommentResumeFromTaskID, stored.Status)
	}
}

func TestClaimOrdinaryWakeupMissingConsumedReceiptFailsBeforeLaunch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	f := createCommentDeliveryFixture(t, "missing scheduled input proof")
	recipient := dbfx.Task(t, f.agentID, testutil.Cols{"issue_id": f.issueID, "runtime_id": f.runtimeID, "status": "completed", "session_id": "valid-recipient-session", "completed_at": testutil.Raw("now()-interval '1 minute'")})
	dbfx.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": f.issueID, "agent_id": f.agentID, "created_by": testUserID, "instruction": "current handoff", "kind": "event", "mode": "once", "handoff": map[string]any{}, "last_task_id": recipient})
	wakeup := dbfx.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": f.issueID, "agent_id": f.agentID, "created_by": testUserID, "instruction": "missing evidence", "kind": "at", "mode": "once", "enabled": false, "last_task_id": f.taskID})
	dbfx.Exec(t, `UPDATE agent_task_queue SET trigger_comment_id=NULL,coalesced_comment_ids='{}',
  context=jsonb_build_object('wakeup_id',$2::text,'wakeup_revision',1),originator_user_id=$3,accountable_user_id=$3,
  trigger_evidence_kind='issue_wakeup',trigger_evidence_ref_id=$2::uuid WHERE id=$1`, f.taskID, wakeup, testUserID)
	recorder := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+f.runtimeID+"/tasks/claim", nil, testWorkspaceID, "missing-wakeup-input")
	req.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilityCoalescedCommentsV1+","+protocol.DaemonCapabilityRetainedContextResetV1)
	testHandler.ClaimTaskByRuntime(recorder, withURLParam(req, "runtimeId", f.runtimeID))
	if recorder.Code != http.StatusOK {
		t.Fatalf("claim failed unexpectedly: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Task *AgentTaskResponse `json:"task"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Task != nil {
		t.Fatalf("unproven wakeup was delivered: %s", recorder.Body.String())
	}
	stored, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(f.taskID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.FailureReason.String != "invalid_task_identity" || stored.Error.String != "The wakeup cannot continue the current workflow because its retained run has no verified handoff ancestry." {
		t.Fatalf("missing receipt failed outside the retained-source gate: reason=%q error=%q", stored.FailureReason.String, stored.Error.String)
	}
	if stored.Status != "failed" || stored.StartedAt.Valid || stored.WakeupResumeFromTaskID.Valid {
		t.Fatalf("unproven wakeup was not settled before launch: status=%s started=%v ancestry=%v", stored.Status, stored.StartedAt, stored.WakeupResumeFromTaskID)
	}
}

func TestClaimExplicitWakeupRetryOrRerunKeepsOriginalSourceAncestry(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	for _, edge := range []string{"retry_of_task_id", "rerun_of_task_id"} {
		t.Run(edge, func(t *testing.T) {
			ctx := context.Background()
			f := createCommentDeliveryFixture(t, "explicit wakeup "+edge)
			recipient := dbfx.Task(t, f.agentID, testutil.Cols{"issue_id": f.issueID, "runtime_id": f.runtimeID, "status": "completed", "session_id": "explicit-wakeup-session", "completed_at": testutil.Raw("now()-interval '2 minutes'")})
			dbfx.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": f.issueID, "agent_id": f.agentID, "created_by": testUserID, "instruction": "current handoff", "kind": "event", "mode": "once", "handoff": map[string]any{}, "last_task_id": recipient})
			wakeup := dbfx.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": f.issueID, "agent_id": f.agentID, "created_by": testUserID, "instruction": "original wakeup", "kind": "at", "mode": "once", "enabled": false})
			source := dbfx.Task(t, f.agentID, testutil.Cols{"issue_id": f.issueID, "runtime_id": f.runtimeID, "status": "completed", "session_id": "explicit-wakeup-session", "completed_at": testutil.Raw("now()-interval '1 minute'"), "originator_user_id": testUserID, "accountable_user_id": testUserID, "trigger_evidence_kind": "issue_wakeup", "trigger_evidence_ref_id": wakeup, "wakeup_resume_from_task_id": recipient, "context": map[string]any{"wakeup_id": wakeup, "wakeup_revision": 1}})
			dbfx.Insert(t, "issue_wakeup_receipt", testutil.Cols{"id": dbid.NewV7(), "wakeup_id": wakeup, "revision": int64(1), "event_key": "due", "event_type": "time.due", "payload": map[string]any{}, "task_id": source, "processed_at": testutil.Raw("now()")})
			dbfx.Exec(t, `UPDATE agent_task_queue SET trigger_comment_id=NULL,coalesced_comment_ids='{}',context=jsonb_build_object('wakeup_id',$2::text,'wakeup_revision',1),originator_user_id=$3,accountable_user_id=$3,trigger_evidence_kind='issue_wakeup',trigger_evidence_ref_id=$2::uuid WHERE id=$1`, f.taskID, wakeup, testUserID)
			dbfx.Exec(t, "UPDATE agent_task_queue SET "+edge+"=$2,force_fresh_session=$3 WHERE id=$1", f.taskID, source, edge == "rerun_of_task_id")
			caps := protocol.DaemonCapabilityCoalescedCommentsV1 + "," + protocol.DaemonCapabilityRetainedContextResetV1
			resp := claimCommentDeliveryFixture(t, f, caps)
			stored, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(f.taskID))
			if err != nil {
				t.Fatal(err)
			}
			if resp.PriorSessionID != "explicit-wakeup-session" || stored.WakeupResumeFromTaskID.Valid || stored.CommentResumeFromTaskID.Valid || (stored.RetryOfTaskID != parseUUID(source) && stored.RerunOfTaskID != parseUUID(source)) {
				t.Fatalf("explicit source ancestry was replaced or rejected: prior=%q retry=%v rerun=%v newWakeup=%v", resp.PriorSessionID, stored.RetryOfTaskID, stored.RerunOfTaskID, stored.WakeupResumeFromTaskID)
			}
		})
	}
}

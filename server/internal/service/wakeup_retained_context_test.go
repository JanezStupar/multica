package service

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func recordedWakeupContinuation(t *testing.T, f principalFixture, s *IssueWakeupService, source db.AgentTaskQueue) (db.AgentTaskQueue, string) {
	t.Helper()
	ctx := context.Background()
	wakeup := f.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(), "workspace_id": f.WorkspaceID, "issue_id": source.IssueID, "agent_id": source.AgentID, "created_by": f.UserID, "instruction": "scheduled continuation", "kind": "at", "mode": "once", "enabled": false})
	taskID := parseTestUUID(t, f.Task(t, util.UUIDToString(source.AgentID), testutil.Cols{"issue_id": source.IssueID, "runtime_id": source.RuntimeID, "status": "dispatched", "dispatched_at": testutil.Raw("now()"), "originator_user_id": f.UserID, "accountable_user_id": f.UserID, "trigger_evidence_kind": "issue_wakeup", "trigger_evidence_ref_id": wakeup, "context": map[string]any{"wakeup_id": wakeup, "wakeup_revision": 1}}))
	f.Insert(t, "issue_wakeup_receipt", testutil.Cols{"id": dbid.NewV7(), "wakeup_id": wakeup, "revision": int64(1), "event_key": "due", "event_type": "time.due", "payload": map[string]any{}, "task_id": taskID, "processed_at": testutil.Raw("now()")})
	task, err := f.q.GetAgentTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := s.Tasks.ValidateWakeupRetainedSource(ctx, f.q, task, source.ID, source.SessionID.String)
	if err != nil || !valid {
		t.Fatalf("valid consumed wakeup rejected: %v, %v", valid, err)
	}
	if _, err = f.q.SetTaskWakeupResumeSource(ctx, db.SetTaskWakeupResumeSourceParams{TaskID: task.ID, RuntimeID: task.RuntimeID, DispatchedAt: task.DispatchedAt, SourceTaskID: source.ID, SessionID: source.SessionID.String}); err != nil {
		t.Fatal(err)
	}
	f.Exec(t, "UPDATE agent_task_queue SET status='completed',session_id=$2,started_at=now(),completed_at=now() WHERE id=$1", task.ID, source.SessionID)
	task, err = f.q.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return task, wakeup
}

func TestIssueHandoffRetainedWakeupThenCommentSurvivesReceiptRetention(t *testing.T) {
	f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
	wakeupTask, wakeup := recordedWakeupContinuation(t, f, s, recipient)
	f.Exec(t, "DELETE FROM issue_wakeup_receipt WHERE wakeup_id=$1", wakeup)
	comment := recordRetainedCommentTask(t, f, wakeupTask, false)
	// Reproduce the historical gap: an older queued wakeup ran after the
	// recipient in the same provider session, but recorded no exact ancestry.
	f.Exec(t, "UPDATE agent_task_queue SET wakeup_resume_from_task_id=NULL WHERE id=$1", wakeupTask.ID)
	if _, err := s.CreateHandoff(context.Background(), w.IssueID, parseTestUUID(t, f.UserID), comment.ID, handoffInput(comment.ID, parseTestUUID(t, nextAgent))); !errors.Is(err, ErrWakeupConflict) {
		t.Fatalf("unconnected scheduled run borrowed same-session authority: %v", err)
	}
	f.Exec(t, "UPDATE agent_task_queue SET wakeup_resume_from_task_id=$2 WHERE id=$1", wakeupTask.ID, recipient.ID)
	if _, err := s.CreateHandoff(context.Background(), w.IssueID, parseTestUUID(t, f.UserID), comment.ID, handoffInput(comment.ID, parseTestUUID(t, nextAgent))); err != nil {
		t.Fatalf("verified wakeup ancestry expired with operational receipt: %v", err)
	}
}

func TestWakeupRetainedSourceRejectsInvalidProvenanceAndAncestry(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, principalFixture, db.AgentTaskQueue, string)
	}{
		{"disabled", func(t *testing.T, f principalFixture, task db.AgentTaskQueue, w string) {
			f.Exec(t, "UPDATE issue_wakeup SET disabled_at=now() WHERE id=$1", w)
		}},
		{"stale revision", func(t *testing.T, f principalFixture, task db.AgentTaskQueue, w string) {
			f.Exec(t, "UPDATE issue_wakeup SET revision=revision+1 WHERE id=$1", w)
		}},
		{"missing receipt", func(t *testing.T, f principalFixture, task db.AgentTaskQueue, w string) {
			f.Exec(t, "DELETE FROM issue_wakeup_receipt WHERE wakeup_id=$1", w)
		}},
		{"forged evidence", func(t *testing.T, f principalFixture, task db.AgentTaskQueue, w string) {
			f.Exec(t, "UPDATE agent_task_queue SET trigger_evidence_ref_id=NULL WHERE id=$1", task.ID)
		}},
		{"fresh", func(t *testing.T, f principalFixture, task db.AgentTaskQueue, w string) {
			f.Exec(t, "UPDATE agent_task_queue SET force_fresh_session=true WHERE id=$1", task.ID)
		}},
		{"invalidated", func(t *testing.T, f principalFixture, task db.AgentTaskQueue, w string) {
			f.Exec(t, "UPDATE agent_task_queue SET retained_context_invalidated=true WHERE id=$1", task.ID)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, s, _, recipient, _ := retainedCommentHandoffFixture(t)
			task, w := recordedWakeupContinuation(t, f, s, recipient)
			tc.change(t, f, task, w)
			task, err := f.q.GetAgentTask(context.Background(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			valid, err := s.Tasks.ValidateWakeupRetainedSource(context.Background(), f.q, task, recipient.ID, recipient.SessionID.String)
			if valid {
				t.Fatalf("invalid wakeup acquired retained authority: %v", err)
			}
		})
	}
	t.Run("unrelated latest run cannot borrow same session", func(t *testing.T) {
		f, s, _, recipient, _ := retainedCommentHandoffFixture(t)
		task, _ := recordedWakeupContinuation(t, f, s, recipient)
		unrelated := parseTestUUID(t, f.Task(t, util.UUIDToString(recipient.AgentID), testutil.Cols{"issue_id": recipient.IssueID, "runtime_id": recipient.RuntimeID, "status": "completed", "session_id": recipient.SessionID}))
		valid, err := s.Tasks.ValidateWakeupRetainedSource(context.Background(), f.q, task, unrelated, recipient.SessionID.String)
		if valid || !errors.Is(err, ErrWakeupConflict) {
			t.Fatalf("unrelated provider run borrowed ancestry: valid=%v err=%v", valid, err)
		}
	})
}

func TestWakeupParentCommentCannotBypassConfigurationRevocation(t *testing.T) {
	for _, change := range []string{"disabled", "revised", "missing wakeup edge"} {
		t.Run(change, func(t *testing.T) {
			f, s, w, recipient, nextAgent := retainedCommentHandoffFixture(t)
			task, wakeup := recordedWakeupContinuation(t, f, s, recipient)
			comment := parseTestUUID(t, f.Comment(t, util.UUIDToString(recipient.IssueID), "Wakeup delivery thread"))
			// Both edges can occur on rows written before the priority correction.
			// Even a live member parent must not bypass the wakeup's own authority.
			f.Exec(t, "UPDATE issue_wakeup SET parent_comment_id=$2 WHERE id=$1", wakeup, comment)
			f.Exec(t, "UPDATE agent_task_queue SET trigger_comment_id=$2,delivered_comment_ids=ARRAY[$2::uuid],comment_resume_from_task_id=$3 WHERE id=$1", task.ID, comment, recipient.ID)
			switch change {
			case "disabled":
				f.Exec(t, "UPDATE issue_wakeup SET disabled_at=now() WHERE id=$1", wakeup)
			case "revised":
				f.Exec(t, "UPDATE issue_wakeup SET revision=revision+1 WHERE id=$1", wakeup)
			case "missing wakeup edge":
				f.Exec(t, "UPDATE agent_task_queue SET wakeup_resume_from_task_id=NULL WHERE id=$1", task.ID)
			}
			followup := recordRetainedCommentTask(t, f, task, false)
			if _, err := s.CreateHandoff(context.Background(), w.IssueID, parseTestUUID(t, f.UserID), followup.ID, handoffInput(followup.ID, parseTestUUID(t, nextAgent))); !errors.Is(err, ErrWakeupConflict) {
				t.Fatalf("member delivery-thread parent bypassed wakeup %s: %v", change, err)
			}
		})
	}
}

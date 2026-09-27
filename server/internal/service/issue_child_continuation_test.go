package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func childContinuationFixture(t *testing.T) (principalFixture, *IssueWakeupService, pgtype.UUID, string, pgtype.UUID) {
	t.Helper()
	f, s, parent, agent, _ := handoffFixture(t)
	current, err := f.q.GetIssue(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := s.Tasks.DecodeIssueWorkflowPolicy(current.WorkflowPolicy)
	if err != nil {
		t.Fatal(err)
	}
	source := pinned.Bundle
	source.ID = util.UUIDToString(dbid.NewV7())
	source.Files = append(source.Files, AgentSkillFileData{Path: "runtime/policy.json", Content: `{"format_version":2,"accepted_status_key":"in_progress"}`})
	policy, err := s.Tasks.NewIssueWorkflowPolicy(source)
	if err != nil {
		t.Fatal(err)
	}
	archive, _ := json.Marshal(policy)
	f.Exec(t, `UPDATE issue SET workflow_policy=$2,assignee_type='agent',assignee_id=$3 WHERE id=$1`, parent, archive, agent)
	task := f.Task(t, agent, testutil.Cols{"issue_id": parent, "status": "completed", "session_id": "retained-parent-session", "originator_user_id": f.UserID, "accountable_user_id": f.UserID, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + agent + "')")})
	return f, s, parent, agent, parseTestUUID(t, task)
}

func completedChild(t *testing.T, f principalFixture, parent pgtype.UUID, status string) string {
	t.Helper()
	child := f.Issue(t, "Delegated child", testutil.Cols{"parent_issue_id": parent})
	f.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, child, status)
	return child
}

func TestChildContinuationCapturesEveryChildTransactionallyAndReconcilesOnce(t *testing.T) {
	f, s, parent, _, source := childContinuationFixture(t)
	_ = f.Issue(t, "Other child is still running", testutil.Cols{"parent_issue_id": parent, "status": "in_progress"})
	child := f.Issue(t, "Completed child", testutil.Cols{"parent_issue_id": parent})
	tx, err := f.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), `UPDATE issue SET status='done',revision=revision+1 WHERE id=$1`, child); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(context.Background())
	if n := f.Count(t, `SELECT count(*) FROM issue_wakeup WHERE child_issue_id=$1`, child); n != 0 {
		t.Fatalf("rollback left %d inputs", n)
	}
	f.Exec(t, `UPDATE issue SET status='done',revision=revision+1 WHERE id=$1`, child)
	var wakeID string
	f.QueryRow(t, `SELECT id FROM issue_wakeup WHERE child_issue_id=$1`, child).Scan(&wakeID)
	w, err := f.q.GetIssueWakeup(context.Background(), db.GetIssueWakeupParams{ID: parseTestUUID(t, wakeID), WorkspaceID: parseTestUUID(t, f.WorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	if w.SourceTaskID != source || w.ForceFreshSession {
		t.Fatal("parent continuation did not retain its source context")
	}
	wakeDispatch(t, s, w)
	if err = s.ReconcileChildCompletions(context.Background(), parent, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, w)
	var taskID string
	f.QueryRow(t, `SELECT id FROM agent_task_queue WHERE context->>'wakeup_id'=$1`, wakeID).Scan(&taskID)
	task, err := f.q.GetAgentTask(context.Background(), parseTestUUID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if task.ForceFreshSession || task.DelegatedFromTaskID != source || !strings.Contains(task.HandoffNote.String, child) || !strings.Contains(task.HandoffNote.String, "privileged") {
		t.Fatal("missing bounded child facts or retained context")
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE context->>'wakeup_id'=$1`, wakeID); n != 1 {
		t.Fatalf("duplicated continuation: %d", n)
	}
	// Editorial saves and operational receipt cleanup cannot replay a
	// completion that this parent already reconciled.
	f.Exec(t, `UPDATE issue SET title='Editorial child title',revision=revision+1 WHERE id=$1`, child)
	// Deleting operational receipts must not erase durable reconciliation dedup.
	f.Exec(t, `DELETE FROM issue_wakeup_receipt WHERE wakeup_id=$1`, wakeID)
	if err = s.ReconcileChildCompletions(context.Background(), parent, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM issue_wakeup_receipt WHERE wakeup_id=$1`, wakeID); n != 0 {
		t.Fatal("replayed an already consumed child")
	}
}

func TestChildContinuationHonorsParentBoundariesAndCurrentAuthority(t *testing.T) {
	for _, guard := range []string{"frozen", "cancelled", "missing_policy", "reassigned", "archived_agent", "missing_runtime", "missing_member"} {
		t.Run(guard, func(t *testing.T) {
			f, s, parent, agent, _ := childContinuationFixture(t)
			if guard == "frozen" {
				f.Exec(t, `UPDATE issue SET workflow_frozen=true WHERE id=$1`, parent)
			}
			if guard == "cancelled" {
				f.Exec(t, `UPDATE issue SET status='cancelled' WHERE id=$1`, parent)
			}
			if guard == "missing_policy" {
				// Preserve the selected version rule: use a native parent instead of
				// attempting to remove an already-pinned policy.
				parent = parseTestUUID(t, f.Issue(t, "Native parent", testutil.Cols{"assignee_type": "agent", "assignee_id": agent}))
			}
			child := completedChild(t, f, parent, "cancelled")
			if guard == "frozen" || guard == "cancelled" || guard == "missing_policy" {
				if n := f.Count(t, `SELECT count(*) FROM issue_wakeup WHERE child_issue_id=$1`, child); n != 0 {
					t.Fatal("ineligible parent captured continuation")
				}
				return
			}
			var wakeID string
			f.QueryRow(t, `SELECT id FROM issue_wakeup WHERE child_issue_id=$1`, child).Scan(&wakeID)
			if guard == "reassigned" {
				f.Exec(t, `UPDATE issue SET assignee_type='member',assignee_id=$2 WHERE id=$1`, parent, f.UserID)
			}
			if guard == "archived_agent" {
				f.Exec(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, agent)
			}
			if guard == "missing_runtime" {
				f.Exec(t, `UPDATE agent SET runtime_id=NULL WHERE id=$1`, agent)
			}
			if guard == "missing_member" {
				f.Exec(t, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, f.WorkspaceID, f.UserID)
			}
			w, err := f.q.GetIssueWakeup(context.Background(), db.GetIssueWakeupParams{ID: parseTestUUID(t, wakeID), WorkspaceID: parseTestUUID(t, f.WorkspaceID)})
			if err != nil {
				t.Fatal(err)
			}
			wakeDispatch(t, s, w)
			if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE context->>'wakeup_id'=$1`, wakeID); n != 0 {
				t.Fatal("ineligible parent resumed")
			}
		})
	}
}

func TestChildContinuationReconcilesTargetedMissingInputAndPreservesFreeze(t *testing.T) {
	f, s, parent, _, _ := childContinuationFixture(t)
	f.Exec(t, `UPDATE issue SET workflow_frozen=true WHERE id=$1`, parent)
	child := completedChild(t, f, parent, "done")
	if err := s.ReconcileChildCompletions(context.Background(), parent, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM issue_wakeup WHERE child_issue_id=$1`, child); n != 0 {
		t.Fatal("repair crossed freeze")
	}
	tx, err := f.Pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), "SET LOCAL multica.workflow_migration='on'"); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `UPDATE issue SET workflow_frozen=false,workflow_migrated_at=now() WHERE id=$1`, parent); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileChildCompletions(context.Background(), parent, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if n := f.Count(t, `SELECT count(*) FROM issue_wakeup_receipt r JOIN issue_wakeup w ON w.id=r.wakeup_id WHERE w.child_issue_id=$1`, child); n != 1 {
		t.Fatalf("repair receipts=%d", n)
	}
}

func TestChildContinuationCoalescesPendingChildrenAndCanHandOffToReview(t *testing.T) {
	f, s, parent, agent, _ := childContinuationFixture(t)
	child := completedChild(t, f, parent, "done")
	var firstID string
	f.QueryRow(t, `SELECT id FROM issue_wakeup WHERE child_issue_id=$1`, child).Scan(&firstID)
	first, err := f.q.GetIssueWakeup(context.Background(), db.GetIssueWakeupParams{ID: parseTestUUID(t, firstID), WorkspaceID: parseTestUUID(t, f.WorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, first)
	secondChild := completedChild(t, f, parent, "done")
	var secondID string
	f.QueryRow(t, `SELECT id FROM issue_wakeup WHERE child_issue_id=$1`, secondChild).Scan(&secondID)
	second, err := f.q.GetIssueWakeup(context.Background(), db.GetIssueWakeupParams{ID: parseTestUUID(t, secondID), WorkspaceID: parseTestUUID(t, f.WorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, second)
	var taskID string
	f.QueryRow(t, `SELECT id FROM agent_task_queue WHERE issue_id=$1 AND status='queued'`, parent).Scan(&taskID)
	task, err := f.q.GetAgentTask(context.Background(), parseTestUUID(t, taskID))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.HandoffNote.String, child) || !strings.Contains(task.HandoffNote.String, secondChild) {
		t.Fatal("coalesced run lost child facts")
	}
	// A resumed parent can publish the next review handoff using its recorded
	// child continuation authority, including after an earlier handoff recipient.
	reviewer := f.privateAgentOwnedBy(t, f.UserID, "child-reviewer")
	f.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now(),session_id='retained-parent-session' WHERE id=$1`, task.ID)
	bindWorkflowTestTask(t, f, parent, task.ID)
	// Operational evidence retention must not revoke this run's authority.
	f.Exec(t, `DELETE FROM issue_wakeup_receipt WHERE task_id=$1`, task.ID)
	allowed, err := childCompletionContinuation(context.Background(), f.Pool, db.Issue{ID: parent, WorkspaceID: parseTestUUID(t, f.WorkspaceID), AssigneeType: pgtype.Text{String: "agent", Valid: true}, AssigneeID: parseTestUUID(t, agent)}, task)
	if err != nil || !allowed {
		t.Fatalf("server-recorded child continuation rejected: %v", err)
	}
	fake := task
	fake.ID = dbid.NewV7()
	allowed, err = childCompletionContinuation(context.Background(), f.Pool, db.Issue{ID: parent, WorkspaceID: parseTestUUID(t, f.WorkspaceID), AssigneeType: pgtype.Text{String: "agent", Valid: true}, AssigneeID: parseTestUUID(t, agent)}, fake)
	if err != nil || allowed {
		t.Fatal("caller context alone granted continuation authority")
	}
	input := handoffInput(task.ID, parseTestUUID(t, reviewer))
	input.Candidates = []HandoffCandidate{}
	_, err = s.CreateHandoff(context.Background(), parent, parseTestUUID(t, f.UserID), task.ID, input)
	if err != nil {
		t.Fatalf("child-woken writer %s could not hand off to review: %v", agent, err)
	}
}

func TestChildContinuationReparentingKeepsFactsOnTheirOwningParent(t *testing.T) {
	f, s, formerParent, agent, _ := childContinuationFixture(t)
	current, err := f.q.GetIssue(context.Background(), formerParent)
	if err != nil {
		t.Fatal(err)
	}
	nextParent := parseTestUUID(t, f.Issue(t, "New child owner", testutil.Cols{"assignee_type": "agent", "assignee_id": agent}))
	f.Exec(t, `UPDATE issue SET workflow_policy=$2 WHERE id=$1`, nextParent, current.WorkflowPolicy)
	f.Cleanup(t, `DELETE FROM issue_wakeup WHERE issue_id=$1`, nextParent)
	f.Cleanup(t, `DELETE FROM issue_wakeup_receipt WHERE wakeup_id IN (SELECT id FROM issue_wakeup WHERE issue_id=$1)`, nextParent)
	_ = f.Task(t, agent, testutil.Cols{"issue_id": nextParent, "status": "completed", "originator_user_id": f.UserID, "accountable_user_id": f.UserID, "runtime_id": testutil.Raw("(SELECT runtime_id FROM agent WHERE id='" + agent + "')")})
	child := completedChild(t, f, formerParent, "done")
	var formerWakeID string
	f.QueryRow(t, `SELECT id FROM issue_wakeup WHERE issue_id=$1 AND child_issue_id=$2`, formerParent, child).Scan(&formerWakeID)
	former, err := f.q.GetIssueWakeup(context.Background(), db.GetIssueWakeupParams{ID: parseTestUUID(t, formerWakeID), WorkspaceID: parseTestUUID(t, f.WorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	// Reattaching completed evidence explicitly is reconciled on the new
	// parent without dispatching the former parent's still-pending receipt.
	f.Exec(t, `UPDATE issue SET parent_issue_id=$2,revision=revision+1 WHERE id=$1`, child, nextParent)
	if err = s.ReconcileChildCompletions(context.Background(), nextParent, parseTestUUID(t, f.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, former)
	var nextWakeID string
	f.QueryRow(t, `SELECT id FROM issue_wakeup WHERE issue_id=$1 AND child_issue_id=$2`, nextParent, child).Scan(&nextWakeID)
	next, err := f.q.GetIssueWakeup(context.Background(), db.GetIssueWakeupParams{ID: parseTestUUID(t, nextWakeID), WorkspaceID: parseTestUUID(t, f.WorkspaceID)})
	if err != nil {
		t.Fatal(err)
	}
	wakeDispatch(t, s, next)
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND status='queued'`, formerParent); n != 0 {
		t.Fatal("child reparenting resumed former parent")
	}
	if n := f.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND status='queued'`, nextParent); n != 1 {
		t.Fatalf("new parent's continuations=%d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM issue_wakeup WHERE child_issue_id=$1`, child); n != 2 {
		t.Fatal("reparenting erased previous parent provenance")
	}
	var queuedID string
	f.QueryRow(t, `SELECT id FROM agent_task_queue WHERE issue_id=$1 AND status='queued'`, nextParent).Scan(&queuedID)
	queued, err := f.q.GetAgentTask(context.Background(), parseTestUUID(t, queuedID))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CheckClaim(context.Background(), queued); err != nil {
		t.Fatalf("current parent owner cannot claim: %v", err)
	}
	f.Exec(t, `UPDATE issue SET assignee_type='member',assignee_id=$2 WHERE id=$1`, nextParent, f.UserID)
	if err = s.CheckClaim(context.Background(), queued); err != ErrWakeupForbidden {
		t.Fatalf("former parent owner could claim: %v", err)
	}
}

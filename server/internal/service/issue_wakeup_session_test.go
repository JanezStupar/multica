package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestIssueWakeupFreshSessionSurvivesEditEnableAndDispatch(t *testing.T) {
	f, s, issue, agent := wakeFixture(t)
	ctx := context.Background()
	owner := parseTestUUID(t, f.UserID)
	in := WakeupInput{AgentID: agent, Kind: "event", Mode: "continuous", EventTypes: []string{"comment.created"}, Instruction: "Review independently", ForceFreshSession: true}
	w := wakeCreate(t, f, s, issue, in)
	if !w.ForceFreshSession {
		t.Fatal("creation lost fresh-session selection")
	}
	if err := s.EditInstruction(ctx, issue, w.ID, owner, WakeupInstructionInput{Instruction: "Review current revision", ExpectedInstruction: w.Instruction, Revision: w.Revision}); err != nil {
		t.Fatal(err)
	}
	disabled, err := s.Disable(ctx, issue, w.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	w, err = s.Enable(ctx, issue, owner, pgtype.UUID{}, w.ID, WakeupEnableInput{Revision: disabled.Revision})
	if err != nil || !w.ForceFreshSession || w.Instruction != "Review current revision" {
		t.Fatalf("enable lost configuration: %+v, %v", w, err)
	}
	workspaceID := parseTestUUID(t, f.WorkspaceID)
	rows, err := f.q.ListIssueWakeups(ctx, db.ListIssueWakeupsParams{IssueID: issue, WorkspaceID: workspaceID, AgentIds: []pgtype.UUID{parseTestUUID(t, agent)}})
	if err != nil || len(rows) != 1 || !rows[0].ForceFreshSession {
		t.Fatalf("issue readback lost fresh-session selection: %+v, %v", rows, err)
	}
	pageJSON, err := f.q.ListWorkspaceWakeups(ctx, db.ListWorkspaceWakeupsParams{WorkspaceID: workspaceID, MemberID: owner, AgentIds: []pgtype.UUID{parseTestUUID(t, agent)}, Scope: "all", Kind: "all", PageLimit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Items []struct {
			ForceFreshSession bool `json:"force_fresh_session"`
		} `json:"items"`
	}
	if err := json.Unmarshal(pageJSON, &page); err != nil || len(page.Items) != 1 || !page.Items[0].ForceFreshSession {
		t.Fatalf("workspace readback lost fresh-session selection: %s, %v", pageJSON, err)
	}
	for pass := 0; pass < 2; pass++ {
		f.Comment(t, util.UUIDToString(issue), "A new review input")
		wakeDispatch(t, s, w)
		task, err := f.q.FindPendingWakeupTask(ctx, util.UUIDToString(w.ID))
		if err != nil || !task.ForceFreshSession {
			t.Fatalf("review pass %d could resume a previous verdict: %+v, %v", pass, task, err)
		}
		if _, err := f.Pool.Exec(ctx, "UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1", task.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Replacing the full configuration can deliberately restore continuation.
	in.ForceFreshSession = false
	w, err = s.Save(ctx, issue, owner, pgtype.UUID{}, w.ID, in)
	if err != nil || w.ForceFreshSession {
		t.Fatalf("replacement did not restore continuation: %+v, %v", w, err)
	}
	f.Comment(t, util.UUIDToString(issue), "Continue implementation")
	wakeDispatch(t, s, w)
	task, err := f.q.FindPendingWakeupTask(ctx, util.UUIDToString(w.ID))
	if err != nil || task.ForceFreshSession {
		t.Fatalf("continuation forced a context wipe: %+v, %v", task, err)
	}
	rows, err = f.q.ListIssueWakeups(ctx, db.ListIssueWakeupsParams{IssueID: issue, WorkspaceID: workspaceID, AgentIds: []pgtype.UUID{parseTestUUID(t, agent)}})
	if err != nil || len(rows) != 1 || rows[0].ForceFreshSession {
		t.Fatalf("readback lost session choice: %+v, %v", rows, err)
	}
}

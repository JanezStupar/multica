package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkflowLedgerChangesRefreshClientsWithoutChangingIssueRevision(t *testing.T) {
	f, wakeups, issueID, agentID := wakeFixture(t)
	ctx := context.Background()
	before, err := f.q.GetIssue(ctx, issueID)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.New()
	wakeups.Tasks.Bus = bus
	var received []events.Event
	bus.Subscribe(protocol.EventIssueUpdated, func(event events.Event) { received = append(received, event) })
	svc := WorkflowAuthorityService{Tasks: wakeups.Tasks}
	actor := WorkflowActor{Type: "member", ID: f.UserID}
	// A review, exception, or retry-state change may leave the issue row
	// untouched. Other clients still need the workflow-view invalidation.
	svc.PublishWorkflowIssueChange(ctx, before, actor)
	if len(received) != 1 {
		t.Fatalf("ledger-only change emitted %d refresh events", len(received))
	}
	payload := received[0].Payload.(map[string]any)
	issue := payload["issue"].(map[string]any)
	if payload["status_changed"] != false || payload["assignee_changed"] != false || issue["revision"] != before.Revision {
		t.Fatalf("ledger refresh changed issue identity or membership: %#v", payload)
	}
	if received[0].WorkspaceID != util.UUIDToString(before.WorkspaceID) || received[0].ActorID != f.UserID {
		t.Fatalf("refresh has wrong workspace or actor: %+v", received[0])
	}
	// Rejection/acceptance must also move the ordinary issue view to the
	// committed owner and phase, rather than publishing its old snapshot.
	f.Exec(t, "UPDATE issue SET status='in_review',assignee_type='agent',assignee_id=$2 WHERE id=$1", issueID, agentID)
	svc.PublishWorkflowIssueChange(ctx, before, actor)
	if len(received) != 2 {
		t.Fatalf("owner/phase change emitted %d refresh events", len(received))
	}
	wire, err := json.Marshal(received[1].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatal(err)
	}
	issue = payload["issue"].(map[string]any)
	if payload["status_changed"] != true || payload["assignee_changed"] != true || issue["status"] != "in_review" || issue["assignee_id"] != agentID {
		t.Fatalf("refresh did not publish committed owner and phase: %#v", payload)
	}
}

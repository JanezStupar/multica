package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestWillEnqueueRunCombinedAssigneeAndTerminalStatus(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, _, agentID, issueID := seedAttributionFixture(t, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO issue_status(workspace_id,key,name,category,color)
		VALUES ($1,'finished_custom','Finished','done','#334455')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM issue_status WHERE workspace_id=$1 AND key='finished_custom'`, workspaceID)
	})

	svc := &IssueService{Queries: db.New(pool)}
	for _, tc := range []struct {
		name    string
		status  string
		wantRun bool
	}{
		{name: "built-in Done", status: "done"},
		{name: "custom Done", status: "finished_custom"},
		{name: "active assignment", status: "in_progress", wantRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issue := db.Issue{
				ID:           util.MustParseUUID(issueID),
				WorkspaceID:  util.MustParseUUID(workspaceID),
				AssigneeType: pgtype.Text{String: "agent", Valid: true},
				AssigneeID:   util.MustParseUUID(agentID),
				Status:       tc.status,
			}
			trigger, enqueue := svc.WillEnqueueRun(ctx, IssueTriggerInput{
				Issue: issue, PrevStatus: "backlog", AssigneeChanged: true, StatusChanged: true,
			}, IssueTriggerProbe{})
			if enqueue != tc.wantRun {
				t.Fatalf("status %q enqueue=%t, want %t", tc.status, enqueue, tc.wantRun)
			}
			if enqueue && (trigger.AgentID != issue.AssigneeID || trigger.Source != RunSourceAssign) {
				t.Fatalf("active assignment trigger=%+v, want agent assignee and assign source", trigger)
			}
			if !enqueue && trigger != (IssueRunTrigger{}) {
				t.Fatalf("terminal assignment returned trigger %+v", trigger)
			}
		})
	}
}

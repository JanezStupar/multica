package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// PublishWorkflowIssueChange runs only after the owning transaction commits.
// Ledger-only changes still emit issue:updated so clients can invalidate their
// workflow view without putting acceptance or delivery state into issue caches.
func (s WorkflowAuthorityService) PublishWorkflowIssueChange(ctx context.Context, before db.Issue, actor WorkflowActor) {
	if s.Tasks == nil || s.Tasks.Queries == nil || s.Tasks.Bus == nil {
		return
	}
	after, err := s.Tasks.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID: before.ID, WorkspaceID: before.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return // An explicit deletion can follow the committed change.
	}
	if err != nil {
		slog.Warn("workflow change committed but realtime refresh failed", "issue_id", util.UUIDToString(before.ID), "error", err)
		return
	}
	actorType := actor.Type
	if actorType != "member" && actorType != "agent" {
		actorType = "system"
	}
	s.Tasks.Bus.Publish(events.Event{
		Type: protocol.EventIssueUpdated, WorkspaceID: util.UUIDToString(after.WorkspaceID),
		ActorType: actorType, ActorID: actor.ID,
		Payload: map[string]any{
			"issue":            IssueToMapResolved(ctx, s.Tasks.Queries, after, s.Tasks.getIssuePrefix(after.WorkspaceID)),
			"status_changed":   before.Status != after.Status,
			"assignee_changed": before.AssigneeType != after.AssigneeType || before.AssigneeID != after.AssigneeID,
			"prev_status":      before.Status,
		},
	})
}

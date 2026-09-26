package handler

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Naming the exact coordinator from a completed member handoff has the same
// conversation authority as the implicit route. Other mentions retain their
// own invocation and execution rules.
func (h *Handler) routeMentionedWorkflowCoordinator(ctx context.Context, issue db.Issue, memberID string,
	triggers []commentAgentTrigger, targets []commentMentionTarget,
) ([]commentAgentTrigger, []commentMentionTarget) {
	route, blocked, handled := h.routeWorkflowHumanComment(ctx, issue, memberID)
	if !handled {
		return triggers, targets
	}
	coordinatorID := uuidToString(route.Agent.ID)
	if blocked != nil {
		coordinatorID = blocked.TargetID
	}
	resolved := make([]commentAgentTrigger, 0, len(triggers))
	for _, trigger := range triggers {
		if trigger.Source != commentTriggerSourceMentionAgent || uuidToString(trigger.Agent.ID) != coordinatorID {
			resolved = append(resolved, trigger)
			continue
		}
		if blocked != nil {
			for i := range targets {
				if targets[i].TargetType == "agent" && targets[i].TargetID == coordinatorID {
					targets[i].Status, targets[i].ReasonCode = blocked.Status, blocked.ReasonCode
					targets[i].ExecAgentID = ""
				}
			}
			continue
		}
		resolved = append(resolved, route)
	}
	return resolved, targets
}

// recordWorkflowCommentRecipients runs after the comment statement has locked
// its owning issue. Routing, invocation authority and the recipient proof use
// that current transaction state, never the pre-save composer snapshot.
func (h *Handler) recordWorkflowCommentRecipients(ctx context.Context, tx pgx.Tx, issue db.Issue, comment db.Comment,
	actorType, actorID, originator string, suppressed []pgtype.UUID, editing bool,
) (db.Issue, []commentAgentTrigger, []CommentTriggerOutcome, []pgtype.UUID, error) {
	q := h.Queries.WithTx(tx)
	current, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issue.ID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		return issue, nil, nil, nil, err
	}
	locked := *h
	locked.Queries, locked.DB = q, tx
	tasks := *h.TaskService
	tasks.Queries = q
	locked.TaskService = &tasks
	var parent *db.Comment
	if comment.ParentID.Valid {
		p, e := q.GetComment(ctx, comment.ParentID)
		if e != nil {
			return current, nil, nil, nil, e
		}
		parent = &p
	}
	triggers, targets := locked.computeCommentAgentTriggers(ctx, current, comment.Content, parent, actorType, actorID, commentTriggerComputeOptions{
		ExcludeTriggerCommentID: comment.ID, AuthoringTaskID: comment.SourceTaskID, OriginatorUserID: originator,
	})
	triggers = filterSuppressedCommentAgentTriggers(triggers, suppressed)
	var liveAuthority bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_workflow_acceptance WHERE issue_id=$1 AND workspace_id=$2
  AND candidate_id=$3 AND state IN ('requested','accepted') AND revoked_at IS NULL)`, current.ID, current.WorkspaceID, current.WorkflowCandidateID).Scan(&liveAuthority)
	if err != nil {
		return current, nil, nil, nil, err
	}
	blocked := []CommentTriggerOutcome{}
	// An accepted assigned recipient becoming unavailable before this locked
	// decision cannot leave a success-shaped dispatch or a durable orphan plan.
	if liveAuthority && actorType == "member" && current.AssigneeType.String == "agent" && len(triggers) == 0 && len(targets) == 0 &&
		!isNoteComment(comment.Content) && len(suppressed) == 0 && len(util.ParseMentions(comment.Content)) == 0 {
		blocked = append(blocked, CommentTriggerOutcome{TargetType: "agent", TargetID: uuidToString(current.AssigneeID), Status: DispatchBlocked, ReasonCode: ReasonTargetUnavailable})
	}
	var recipients []commentAgentTrigger
	denied := append([]pgtype.UUID(nil), suppressed...)
	for _, trigger := range triggers {
		recorded := false
		if actorType == "member" && comment.AuthorType == "member" && actorID == uuidToString(comment.AuthorID) {
			if route := trigger.WorkflowFeedback; trigger.Source == commentTriggerSourceWorkflowFeedback && route != nil {
				recorded, err = service.RecordWorkflowHumanComment(ctx, tx, current, trigger.Agent.ID, route.HandoffID, route.CoordinatorTask, route.CandidateID, comment.ID)
			} else {
				var accepted, requested bool
				accepted, err = service.RecordAcceptedAssignedComment(ctx, tx, current, trigger.Agent.ID, comment.ID)
				if err == nil {
					requested, err = service.RecordRequestedAcceptanceComment(ctx, tx, current, trigger.Agent.ID, comment.ID)
				}
				recorded = accepted || requested
			}
			if err != nil {
				return current, nil, nil, nil, err
			}
		}
		if actorType != comment.AuthorType || actorID != uuidToString(comment.AuthorID) ||
			(liveAuthority || trigger.Source == commentTriggerSourceWorkflowFeedback) && !recorded {
			denied = append(denied, trigger.Agent.ID)
			blocked = append(blocked, CommentTriggerOutcome{TargetType: "agent", TargetID: uuidToString(trigger.Agent.ID), Status: DispatchBlocked, ReasonCode: ReasonTargetUnavailable})
			continue
		}
		recipients = append(recipients, trigger)
	}
	if editing {
		ids := make([]pgtype.UUID, 0, len(recipients))
		for _, recipient := range recipients {
			ids = append(ids, recipient.Agent.ID)
		}
		if err = service.ReconcileWorkflowCommentRecipients(ctx, tx, current, comment.ID, ids); err != nil {
			return current, nil, nil, nil, err
		}
	}
	return current, recipients, blocked, denied, nil
}

// finishWorkflowCommentDispatch retires only an attempted route that never
// acquired a task/replay promise, and makes a disappearing recipient visible.
func (h *Handler) finishWorkflowCommentDispatch(ctx context.Context, issue db.Issue, commentID pgtype.UUID, recipients []commentAgentTrigger,
	outcomes []CommentTriggerOutcome, enqueued map[string]commentEnqueueResult,
) []CommentTriggerOutcome {
	for _, original := range recipients {
		result, found := enqueued[uuidToString(original.Agent.ID)]
		if found && result.status != DispatchBlocked {
			continue
		}
		// Acceptance or external completion may close new-birth routing after
		// this save recorded an exact conversation. Honor that recorded route,
		// rechecking the current human permission and canonical queue fence.
		if h.DB != nil && (original.Source == commentTriggerSourceWorkflowFeedback || original.Source == commentTriggerSourceIssueAssignee) {
			var recorded bool
			if err := h.DB.QueryRow(ctx, `SELECT workflow_recorded_comment_input_current($1,$2,$3)`, issue.ID, original.Agent.ID, commentID).Scan(&recorded); err == nil && recorded {
				comment, commentErr := h.Queries.GetComment(ctx, commentID)
				agent, agentErr := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: original.Agent.ID, WorkspaceID: issue.WorkspaceID})
				if commentErr == nil && agentErr == nil && h.canInvokeAgent(ctx, agent, "member", uuidToString(comment.AuthorID), uuidToString(comment.AuthorID), uuidToString(issue.WorkspaceID)) {
					if ready, readyErr := service.AgentReadiness(ctx, h.runtimeLookup(obsmetrics.RuntimeLookupSourceComment), agent); readyErr == nil && !ready.Blocked() {
						original.Agent = agent
						status, reason := h.resolveCommentTriggerEnqueue(ctx, issue, original, commentID)
						if status != DispatchBlocked {
							filtered := outcomes[:0]
							for _, outcome := range outcomes {
								if outcome.TargetType != "agent" || outcome.TargetID != uuidToString(agent.ID) {
									filtered = append(filtered, outcome)
								}
							}
							outcomes = append(filtered, CommentTriggerOutcome{TargetType: "agent", TargetID: uuidToString(agent.ID), Status: status, ReasonCode: reason})
							continue
						}
					}
				}
			}
		}
		if err := h.TaskService.RetireUnpromisedCommentObligation(ctx, issue, original.Agent.ID, commentID); err != nil {
			// Retaining an unresolved proof is safer than falsely discharging a promise.
			continue
		}
		if !found {
			outcomes = append(outcomes, CommentTriggerOutcome{TargetType: "agent", TargetID: uuidToString(original.Agent.ID), Status: DispatchBlocked, ReasonCode: ReasonTargetUnavailable})
		}
	}
	return outcomes
}

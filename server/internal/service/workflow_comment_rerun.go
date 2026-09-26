package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var ErrWorkflowCommentRerunAuthor = errors.New("only the original comment author may retry this promised reply")

var errWorkflowCommentRerunUnavailable = errors.New("the promised comment reply is no longer current or retryable")

var errWorkflowCommentRerunAnswered = errors.New("this promised reply was already answered; add a new comment to request another answer")

var errWorkflowCommentRerunNotTerminal = errors.New("only failed or cancelled promised replies can be retried")

// A promised reply derives its claim authority from the exact human input,
// rather than the member who happens to click rerun. Validate that proof before
// any queue mutation and retain it atomically on the new attempt.
func (s *TaskService) rerunPromisedWorkflowComment(ctx context.Context, issue db.Issue, source db.AgentTaskQueue, overrideCommentID, actorID pgtype.UUID, canInvoke func(db.Agent) bool) (bool, *db.AgentTaskQueue, error) {
	memberHandoff := source.TriggerEvidenceKind.String == "workflow_human_comment"
	var birthContext map[string]json.RawMessage
	_ = json.Unmarshal(source.Context, &birthContext)
	_, recordedPromise := birthContext["workflow_comment_obligation"]
	// Birth evidence is used only to refuse a stale promised rerun. Current
	// authority still comes from the SQL proofs below, never from this marker.
	if !memberHandoff && !recordedPromise && (len(issue.WorkflowPolicy) == 0 || (!source.TriggerCommentID.Valid && len(source.CoalescedCommentIds) == 0)) {
		return false, nil, nil
	}
	if s.TxStarter == nil {
		return true, nil, errWorkflowCommentRerunUnavailable
	}
	agent, err := s.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: source.AgentID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		return true, nil, err
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return true, nil, err
	}
	defer tx.Rollback(ctx)
	q := s.Queries.WithTx(tx)
	var currentMember, currentAssigned, currentRequested, recordedAssigned bool
	readProof := func() error {
		return tx.QueryRow(ctx, `SELECT workflow_human_comment_task_current($1,$2),
			workflow_accepted_comment_task_current($1,$2),
			workflow_requested_comment_task_current($1,$2),
			EXISTS(SELECT 1 FROM issue_workflow_acceptance a CROSS JOIN LATERAL jsonb_array_elements(a.human_comment_obligations) input
			 WHERE a.issue_id=$2 AND a.workspace_id=$3
			 AND input->>'agent_id'=$4::uuid::text
			 AND (input->>'comment_id'=$5::uuid::text OR input->>'comment_id'=ANY(SELECT id::text FROM unnest($6::uuid[]) id)))`,
			source.ID, issue.ID, issue.WorkspaceID, source.AgentID, source.TriggerCommentID, source.CoalescedCommentIds).Scan(&currentMember, &currentAssigned, &currentRequested, &recordedAssigned)
	}
	validate := func() error {
		if !actorID.Valid || actorID != source.OriginatorUserID || actorID != source.AccountableUserID {
			return ErrWorkflowCommentRerunAuthor
		}
		if source.Status == "completed" {
			return errWorkflowCommentRerunAnswered
		}
		if (!currentMember && !currentAssigned && !currentRequested) ||
			(overrideCommentID.Valid && overrideCommentID != source.TriggerCommentID) || source.IssueID != issue.ID || source.AgentID != agent.ID || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid {
			return errWorkflowCommentRerunUnavailable
		}
		if source.Status != "failed" && source.Status != "cancelled" {
			return errWorkflowCommentRerunNotTerminal
		}
		if canInvoke != nil && !canInvoke(agent) {
			return ErrRerunInvokeNotAllowed
		}
		return nil
	}
	if err = readProof(); err != nil {
		return true, nil, err
	}
	if !memberHandoff && !recordedPromise && !currentAssigned && !currentRequested && !recordedAssigned {
		return false, nil, nil
	}
	if err = validate(); err != nil {
		return true, nil, err
	}
	// Connected-app resolution can call a provider. Resolve it before taking
	// row locks, then revalidate the exact proof and actor before any insert.
	overlay := s.buildRuntimeMCPOverlay(ctx, actorID, agent)
	// Keep the normal owner fence's order before taking stronger issue/source
	// locks; the source-agent NOWAIT fence prevents runtime rebinding mid-enqueue.
	var ownersPresent bool
	if err = tx.QueryRow(ctx, `SELECT lock_task_owner_rows($1,$2,$3)`, source.AgentID, issue.ID, agent.RuntimeID).Scan(&ownersPresent); err != nil {
		return true, nil, err
	}
	if !ownersPresent {
		return true, nil, errWorkflowCommentRerunUnavailable
	}
	expectedRuntime := agent.RuntimeID
	agent, err = q.LockHandoffSourceAgent(ctx, db.LockHandoffSourceAgentParams{AgentID: source.AgentID, WorkspaceID: issue.WorkspaceID})
	if err != nil {
		return true, nil, err
	}
	if agent.RuntimeID != expectedRuntime {
		return true, nil, errWorkflowCommentRerunUnavailable
	}
	issue, err = lockWorkflowAuthorityIssue(ctx, tx, q, issue.WorkspaceID, issue.ID)
	if err != nil {
		return true, nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM agent_task_queue WHERE id=$1 FOR UPDATE`, source.ID); err != nil {
		return true, nil, err
	}
	source, err = q.GetAgentTask(ctx, source.ID)
	if err != nil {
		return true, nil, err
	}
	if err = readProof(); err != nil {
		return true, nil, err
	}
	if err = validate(); err != nil {
		return true, nil, err
	}
	if err := guardIssueNotInTriage(ctx, q, issue.ID, OriginNamed); err != nil {
		return true, nil, err
	}
	// A deleted primary may leave an exact live coalesced promise. Promote only
	// this task's original author's input, retaining the historical parent.
	var trigger pgtype.UUID
	plan := append(append([]pgtype.UUID{}, source.CoalescedCommentIds...), source.TriggerCommentID)
	err = tx.QueryRow(ctx, `SELECT c.id FROM comment c
		WHERE c.id=ANY($1::uuid[]) AND c.issue_id=$2 AND c.workspace_id=$3
		AND c.author_type='member' AND c.author_id=$4 AND c.deleted_at IS NULL
		AND c.type IN ('comment','progress_update') AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
		AND workflow_recorded_comment_input_current($2,$6,c.id)
		ORDER BY (c.id=$5) DESC,c.created_at DESC,c.id DESC LIMIT 1 FOR SHARE`,
		plan, issue.ID, issue.WorkspaceID, actorID, source.TriggerCommentID, agent.ID).Scan(&trigger)
	if err != nil {
		return true, nil, errWorkflowCommentRerunUnavailable
	}
	coalesced := make([]pgtype.UUID, 0, len(source.CoalescedCommentIds))
	for _, id := range source.CoalescedCommentIds {
		if id != trigger {
			coalesced = append(coalesced, id)
		}
	}
	// Existing promises from this or another human remain intact if the thread
	// already has a pending attempt. The unique queue fence returns that conflict
	// without cancelling another person's unanswered input.
	task, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		ID: dbid.NewV7(), AgentID: agent.ID, RuntimeID: agent.RuntimeID, IssueID: issue.ID,
		Priority: source.Priority, TriggerCommentID: trigger, CoalescedCommentIds: coalesced,
		TriggerSummary: s.buildCommentTriggerSummary(ctx, issue.WorkspaceID, trigger), ForceFreshSession: pgtype.Bool{Bool: true, Valid: true},
		IsLeaderTask: pgtype.Bool{Bool: source.IsLeaderTask, Valid: true}, SquadID: source.SquadID, HandoffNote: source.HandoffNote,
		OriginatorUserID: source.OriginatorUserID, AccountableUserID: source.AccountableUserID, OriginatorSource: source.OriginatorSource,
		DelegatedFromTaskID: source.DelegatedFromTaskID, RuleVersionID: source.RuleVersionID, RerunOfTaskID: source.ID,
		TriggerEvidenceKind: source.TriggerEvidenceKind, TriggerEvidenceRefID: source.TriggerEvidenceRefID,
		RuntimeMcpOverlay: overlay.Overlay, RuntimeConnectedApps: overlay.ConnectedApps,
	})
	if err != nil {
		if isDuplicatePendingTaskErr(err) {
			return true, nil, ErrDuplicatePendingTask
		}
		return true, nil, fmt.Errorf("enqueue promised comment rerun: %w", err)
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_task_queue SET context=CASE WHEN $4::boolean
		THEN jsonb_set(COALESCE($2::jsonb,'{}')||COALESCE(context,'{}'),'{workflow_feedback,comment_id}',to_jsonb($3::uuid::text))
		ELSE COALESCE($2::jsonb,'{}')||COALESCE(context,'{}') END
		WHERE id=$1`, task.ID, source.Context, trigger, currentMember); err != nil {
		return true, nil, err
	}
	var newProof bool
	if err = tx.QueryRow(ctx, `SELECT workflow_human_comment_task_current($1,$2)
		OR workflow_accepted_comment_task_current($1,$2)
		OR workflow_requested_comment_task_current($1,$2)`, task.ID, issue.ID).Scan(&newProof); err != nil {
		return true, nil, err
	}
	if !newProof {
		return true, nil, errWorkflowCommentRerunUnavailable
	}
	// Neither the abandoned-context authority marker nor its comment-resume
	// pointer transfers to a new task; the exact rerun parent remains evidence.
	task, err = q.GetAgentTask(ctx, task.ID)
	if err != nil {
		return true, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return true, nil, err
	}
	s.broadcastTaskEvent(ctx, protocol.EventTaskQueued, task)
	s.NotifyTaskEnqueued(ctx, task)
	return true, &task, nil
}

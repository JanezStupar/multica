package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/attribution"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type workflowRecoveryContext struct {
	HandoffID    string `json:"handoff_id"`
	FailedTaskID string `json:"failed_task_id"`
	ResumeTaskID string `json:"resume_task_id"`
}

// nativeHandoffRecovery recognizes the precise failed recipient of the latest
// live handoff. It deliberately does not require the old phase/assignee here:
// the failure signal remains durable after human intervention, while dispatch
// and claim decide whether a coordinator may still execute it.
func nativeHandoffRecovery(ctx context.Context, q *db.Queries, failed, source db.AgentTaskQueue, issue db.Issue) (*db.IssueWakeup, error) {
	if !failed.IssueID.Valid || failed.IssueID != issue.ID || source.IssueID != issue.ID ||
		failed.DelegatedFromTaskID != source.ID || source.Status != "completed" {
		return nil, nil
	}
	w, err := q.LatestActiveIssueHandoff(ctx, issue.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !w.LastTaskID.Valid || w.FilterTaskID != source.ID || w.AgentID != failed.AgentID ||
		!handoffRecipientMatches(ctx, q, w, failed) {
		return nil, nil
	}
	if _, err = handoffIntent(w); err != nil {
		return nil, err
	}
	return &w, nil
}

func workflowRecoveryOwnerMatches(issue db.Issue, failed db.AgentTaskQueue, w db.IssueWakeup) bool {
	intent, err := handoffIntent(w)
	return err == nil && issue.Status == intent.Status && issue.AssigneeType.String == "agent" && issue.AssigneeID == failed.AgentID
}

// ValidateWorkflowRecoverySource checks a server-authored delegated-failure
// recovery task, its durable comment and the latest handoff before returning
// the exact outgoing coordinator task for retained-session selection. A task
// context pointer alone is never authority to resume another provider session.
func (s *TaskService) ValidateWorkflowRecoverySource(ctx context.Context, task db.AgentTaskQueue) (db.AgentTaskQueue, error) {
	return s.validateWorkflowRecoverySourceWithQueries(ctx, s.Queries, task)
}

func (s *TaskService) validateWorkflowRecoverySourceWithQueries(ctx context.Context, q *db.Queries, task db.AgentTaskQueue) (db.AgentTaskQueue, error) {
	bad := func() (db.AgentTaskQueue, error) {
		return db.AgentTaskQueue{}, fmt.Errorf("%w: workflow recovery provenance unavailable", ErrWakeupForbidden)
	}
	var envelope struct {
		Recovery workflowRecoveryContext `json:"workflow_recovery"`
	}
	if len(task.Context) == 0 || json.Unmarshal(task.Context, &envelope) != nil {
		return bad()
	}
	recovery := envelope.Recovery
	handoffID, e1 := wakeupUUID(recovery.HandoffID)
	failedID, e2 := wakeupUUID(recovery.FailedTaskID)
	sourceID, e3 := wakeupUUID(recovery.ResumeTaskID)
	if e1 != nil || e2 != nil || e3 != nil || !handoffID.Valid || !failedID.Valid || !sourceID.Valid ||
		!task.IssueID.Valid || !task.TriggerCommentID.Valid ||
		!task.TriggerEvidenceKind.Valid || task.TriggerEvidenceKind.String != string(attribution.EvidenceDelegatedFailure) ||
		task.TriggerEvidenceRefID != failedID || task.DelegatedFromTaskID != failedID {
		return bad()
	}
	failed, err := q.GetAgentTask(ctx, failedID)
	if err != nil || failed.Status != "failed" || failed.IssueID != task.IssueID || failed.DelegatedFromTaskID != sourceID {
		return bad()
	}
	hasRetry, err := q.HasRetryTaskForParent(ctx, failed.ID)
	if err != nil || hasRetry {
		return bad()
	}
	source, err := q.GetAgentTask(ctx, sourceID)
	if err != nil || source.AgentID != task.AgentID || source.IssueID != task.IssueID || source.Status != "completed" {
		return bad()
	}
	issue, err := q.GetIssue(ctx, task.IssueID)
	if err != nil {
		return bad()
	}
	w, err := nativeHandoffRecovery(ctx, q, failed, source, issue)
	if err != nil || w == nil || w.ID != handoffID || !workflowRecoveryOwnerMatches(issue, failed, *w) {
		return bad()
	}
	comment, err := q.GetDelegatedFailureRecoveryComment(ctx, db.GetDelegatedFailureRecoveryCommentParams{
		IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, SourceTaskID: failed.ID,
	})
	if err != nil || comment.DeletedAt.Valid || (task.TriggerCommentID != comment.ID && !slices.Contains(task.CoalescedCommentIds, comment.ID)) {
		return bad()
	}
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: source.AgentID, WorkspaceID: issue.WorkspaceID})
	if err != nil || (&IssueWakeupService{Tasks: s}).authorize(ctx, q, issue.WorkspaceID, w.CreatedBy, agent) != nil {
		return bad()
	}
	return source, nil
}

func workflowRecoveryPayload(w db.IssueWakeup, failed, source pgtype.UUID) []byte {
	encoded, _ := json.Marshal(workflowRecoveryContext{
		HandoffID: util.UUIDToString(w.ID), FailedTaskID: util.UUIDToString(failed), ResumeTaskID: util.UUIDToString(source),
	})
	return encoded
}

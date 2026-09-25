package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// ValidateWorkflowCompletionConfig resolves configured identities in the issue's
// workspace. Status display names are not API keys, and terminal lifecycle
// categories cannot represent accepted work that still has delivery pending.
func ValidateWorkflowCompletionConfig(ctx context.Context, tx pgx.Tx, issue db.Issue, policy WorkflowAuthorityPolicy) error {
	if policy.FormatVersion != 2 {
		return nil
	}
	// Archive takes the exclusive side before its policy-reference census.
	// Acquiring the shared side before resolving the key makes a successful pin
	// and an archive mutually exclusive, even when validation follows an issue
	// or workspace row lock. Archive's census is read-only and takes no row lock.
	if err := db.New(tx).LockIssueStatusCatalogShared(ctx, issue.WorkspaceID); err != nil {
		return err
	}
	var category string
	var system bool
	err := tx.QueryRow(ctx, `SELECT category,is_system FROM issue_status
		WHERE workspace_id=$1 AND key=$2 AND archived_at IS NULL`, issue.WorkspaceID, policy.AcceptedStatusKey).Scan(&category, &system)
	if errors.Is(err, pgx.ErrNoRows) || category != "started" || system {
		return fmt.Errorf("%w: accepted_status_key must resolve to an active custom started status", ErrWorkflowAuthorityConflict)
	}
	if err != nil {
		return err
	}
	var runtimeID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE workspace_id=$1 AND id=$2`,
		issue.WorkspaceID, mustAuthorityUUID(policy.OutcomeAgentID)).Scan(&runtimeID)
	if errors.Is(err, pgx.ErrNoRows) || !runtimeID.Valid {
		return fmt.Errorf("%w: outcome_agent_id must resolve to a workspace agent with a runtime", ErrWorkflowAuthorityConflict)
	}
	return err
}

func validateWorkflowActionReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 500 {
		return "", fmt.Errorf("%w: reason must be 1-500 characters", ErrWorkflowAuthorityInput)
	}
	return reason, nil
}

// A provider-observed changed head invalidates the whole accepted candidate.
// Its row stays current until explicit rejection can resume the retained
// writer, but no review waiver or old review can reaccept that candidate.
func workflowCandidateStale(ctx context.Context, tx pgx.Tx, issue db.Issue, candidateID pgtype.UUID) (bool, error) {
	var stale bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_workflow_delivery d
		WHERE d.workspace_id=$1 AND d.issue_id=$2 AND d.candidate_id=$3
		AND d.merged_at IS NULL
		AND (d.status='stale' OR EXISTS (
			SELECT 1 FROM issue_workflow_delivery_attempt attempt
			WHERE attempt.workspace_id=d.workspace_id AND attempt.issue_id=d.issue_id
			AND attempt.delivery_id=d.id AND attempt.observed_head_sha IS NOT NULL
			AND attempt.observed_head_sha<>d.expected_head_sha)))`,
		issue.WorkspaceID, issue.ID, candidateID).Scan(&stale)
	return stale, err
}

func validateWorkflowOutcomeTask(ctx context.Context, tx pgx.Tx, task db.AgentTaskQueue) error {
	if !task.IssueID.Valid || len(task.Context) == 0 {
		return ErrWorkflowAuthorityConflict
	}
	var envelope struct {
		Outcome struct {
			AcceptanceID string `json:"acceptance_id"`
			CandidateID  string `json:"candidate_id"`
		} `json:"workflow_outcome"`
	}
	if err := json.Unmarshal(task.Context, &envelope); err != nil {
		return ErrWorkflowAuthorityConflict
	}
	acceptanceID, err := workflowAuthorityUUID(envelope.Outcome.AcceptanceID)
	if err != nil {
		return ErrWorkflowAuthorityConflict
	}
	candidateID, err := workflowAuthorityUUID(envelope.Outcome.CandidateID)
	if err != nil {
		return ErrWorkflowAuthorityConflict
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_workflow_acceptance a
		JOIN issue i ON i.id=a.issue_id AND i.workspace_id=a.workspace_id
		WHERE a.id=$1 AND a.issue_id=$2 AND a.candidate_id=$3 AND a.outcome_task_id=$4
		AND a.outcome_agent_id=$5 AND a.state='accepted' AND a.revoked_at IS NULL
		AND i.workflow_candidate_id=a.candidate_id AND i.workflow_policy->>'version'=a.policy_version
		AND workflow_outcome_task_claimable($4,$2))`,
		acceptanceID, task.IssueID, candidateID, task.ID, task.AgentID).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrWorkflowAuthorityConflict
	}
	return nil
}

func (s *TaskService) ValidateWorkflowOutcomeTask(ctx context.Context, task db.AgentTaskQueue) error {
	if s == nil || s.TxStarter == nil {
		return ErrWorkflowAuthorityUnavailable
	}
	tx, err := s.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	return validateWorkflowOutcomeTask(ctx, tx, task)
}

// ReconcileWorkflowCompletion runs with the issue row locked. Every caller
// first records the provider result or outcome acknowledgment in the same
// transaction, so the status transition cannot outrun either fact.
func ReconcileWorkflowCompletion(ctx context.Context, tx pgx.Tx, q *db.Queries, issue db.Issue,
	acceptanceID pgtype.UUID,
) (*db.AgentTaskQueue, bool, error) {
	var candidateID, outcomeAgentID, outcomeTaskID pgtype.UUID
	var state, acceptedStatus, policyVersion string
	var version int16
	var outcomeComplete bool
	err := tx.QueryRow(ctx, `SELECT candidate_id,state,completion_version,accepted_status_key,
		outcome_agent_id,outcome_task_id,outcome_complete,policy_version
		FROM issue_workflow_acceptance WHERE id=$1 AND issue_id=$2 AND workspace_id=$3 AND revoked_at IS NULL`,
		acceptanceID, issue.ID, issue.WorkspaceID).Scan(&candidateID, &state, &version, &acceptedStatus,
		&outcomeAgentID, &outcomeTaskID, &outcomeComplete, &policyVersion)
	if err != nil {
		return nil, false, err
	}
	if version != 2 || state != "accepted" || issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID ||
		issue.Status != acceptedStatus {
		return nil, false, nil
	}
	var pinnedVersion string
	if err := tx.QueryRow(ctx, `SELECT workflow_policy->>'version' FROM issue WHERE id=$1`, issue.ID).Scan(&pinnedVersion); err != nil {
		return nil, false, err
	}
	if pinnedVersion != policyVersion {
		return nil, false, ErrWorkflowAuthorityConflict
	}
	var expected, merged int
	err = tx.QueryRow(ctx, `SELECT jsonb_array_length(c.pr_set),
		(SELECT count(*) FROM issue_workflow_delivery d WHERE d.acceptance_id=$2
		 AND d.status='delivered' AND d.merged_at IS NOT NULL)
		FROM issue_workflow_candidate c WHERE c.id=$1 AND c.issue_id=$3`,
		candidateID, acceptanceID, issue.ID).Scan(&expected, &merged)
	if err != nil {
		return nil, false, err
	}
	if merged != expected {
		return nil, false, nil
	}
	if outcomeComplete {
		var revision int64
		err = tx.QueryRow(ctx, `UPDATE issue SET status='done',revision=revision+1,updated_at=now(),last_activity_at=now()
			WHERE id=$1 AND workspace_id=$2 AND revision=$3 AND status=$4 RETURNING revision`,
			issue.ID, issue.WorkspaceID, issue.Revision, acceptedStatus).Scan(&revision)
		if err != nil || revision != issue.Revision+1 {
			return nil, false, fmt.Errorf("%w: final completion changed: %v", ErrWorkflowAuthorityConflict, err)
		}
		return nil, true, nil
	}
	if outcomeTaskID.Valid {
		return nil, false, nil
	}
	agent, err := q.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{ID: outcomeAgentID, WorkspaceID: issue.WorkspaceID})
	if err != nil || !agent.RuntimeID.Valid {
		return nil, false, fmt.Errorf("%w: outcome agent unavailable: %v", ErrWorkflowAuthorityUnavailable, err)
	}
	var writerTaskID pgtype.UUID
	var writerAgentID, writerRuntimeID pgtype.UUID
	var writerProfileID, selectedProfileID pgtype.UUID
	var writerSession string
	err = tx.QueryRow(ctx, `SELECT c.writer_task_id,t.agent_id,t.runtime_id,COALESCE(t.session_id,''),t.workflow_profile_id
		FROM issue_workflow_candidate c JOIN agent_task_queue t ON t.id=c.writer_task_id
		WHERE c.id=$1 AND c.issue_id=$2`, candidateID, issue.ID).Scan(
		&writerTaskID, &writerAgentID, &writerRuntimeID, &writerSession, &writerProfileID)
	if err != nil {
		return nil, false, err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM issue_workflow_profile
		WHERE workspace_id=$1 AND issue_id=$2 AND agent_id=$3 AND policy_version=$4
		ORDER BY revision DESC LIMIT 1`, issue.WorkspaceID, issue.ID, outcomeAgentID, policyVersion).Scan(&selectedProfileID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	var resume pgtype.UUID
	continuity := "A fresh outcome context is required; reconcile the ticket, candidate, PR evidence and current repository state."
	if writerAgentID == outcomeAgentID && writerRuntimeID == agent.RuntimeID && writerSession != "" &&
		writerProfileID == selectedProfileID {
		resume = writerTaskID
		continuity = "Resume the retained candidate writer context after reconciling intervening delivery and repository changes."
	} else if writerAgentID == outcomeAgentID && writerProfileID != selectedProfileID {
		continuity = "The selected agent profile changed after the writer run; use a fresh outcome context and reconcile the ticket, candidate and delivery evidence."
	}
	note := "Accepted candidate " + util.UUIDToString(candidateID) + " has merged all required PRs. " +
		"Complete remaining deployment, QA or other outcome work, then acknowledge the actual outcome on this exact acceptance. " + continuity
	context, _ := json.Marshal(map[string]string{"kind": "workflow_outcome", "acceptance_id": util.UUIDToString(acceptanceID),
		"candidate_id": util.UUIDToString(candidateID)})
	task, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		ID: dbid.NewV7(), AgentID: outcomeAgentID, RuntimeID: agent.RuntimeID, IssueID: issue.ID,
		Priority: priorityToInt(issue.Priority), ForceFreshSession: pgtype.Bool{Bool: true, Valid: true},
		RerunOfTaskID: resume, HandoffNote: pgtype.Text{String: note, Valid: true},
	})
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_task_queue SET context=jsonb_build_object('workflow_outcome',$2::jsonb)
		WHERE id=$1 AND issue_id=$3`, task.ID, context, issue.ID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET outcome_task_id=$2 WHERE id=$1 AND outcome_task_id IS NULL`,
		acceptanceID, task.ID); err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE issue SET assignee_type='agent',assignee_id=$2,
		revision=revision+1,updated_at=now(),last_activity_at=now()
		WHERE id=$1 AND workspace_id=$3 AND revision=$4`, issue.ID, outcomeAgentID, issue.WorkspaceID, issue.Revision); err != nil {
		return nil, false, err
	}
	return &task, false, nil
}

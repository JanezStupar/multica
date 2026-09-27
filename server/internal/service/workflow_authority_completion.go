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
	err := tx.QueryRow(ctx, `SELECT category FROM issue_status
		WHERE workspace_id=$1 AND key=$2 AND archived_at IS NULL`, issue.WorkspaceID, policy.AcceptedStatusKey).Scan(&category)
	if errors.Is(err, pgx.ErrNoRows) || category != "started" && category != "unstarted" {
		return fmt.Errorf("%w: accepted_status_key must resolve to an active nonterminal status", ErrWorkflowAuthorityConflict)
	}
	if err != nil {
		return err
	}
	return nil
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

// ReconcileWorkflowCompletion runs with the issue row locked. Recorded provider
// merges complete the bound work; an old outcome flag is not a remaining-work
// instruction. No-PR acceptance already records an explicit completion decision.
// The returned task, if any, is a cancelled legacy outcome run to publish after
// the owning transaction commits.
func ReconcileWorkflowCompletion(ctx context.Context, tx pgx.Tx, q *db.Queries, issue db.Issue,
	acceptanceID pgtype.UUID,
) (*db.AgentTaskQueue, bool, error) {
	var candidateID, outcomeTaskID pgtype.UUID
	var state, policyVersion string
	var version int16
	err := tx.QueryRow(ctx, `SELECT candidate_id,state,completion_version,outcome_task_id,policy_version
		FROM issue_workflow_acceptance WHERE id=$1 AND issue_id=$2 AND workspace_id=$3 AND revoked_at IS NULL`,
		acceptanceID, issue.ID, issue.WorkspaceID).Scan(&candidateID, &state, &version, &outcomeTaskID, &policyVersion)
	if err != nil {
		return nil, false, err
	}
	if version != 2 || state != "accepted" || issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID {
		return nil, false, nil
	}
	if issue.Status != "done" {
		eligible, err := WorkflowNonterminalStatus(ctx, tx, issue)
		if err != nil {
			return nil, false, err
		}
		if !eligible {
			return nil, false, nil
		}
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
	cancelled, err := cancelWorkflowOutcomeTask(ctx, q, outcomeTaskID, "Bound work completed; legacy outcome run retired")
	if err != nil {
		return nil, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET outcome_complete=true,
		outcome_completed_at=COALESCE(outcome_completed_at,now()),outcome_request_task_id=NULL,
		last_error_class=NULL,outcome_next_attempt_at=NULL WHERE id=$1`, acceptanceID); err != nil {
		return nil, false, err
	}
	if issue.Status == "done" {
		return cancelled, false, nil
	}
	var revision int64
	err = tx.QueryRow(ctx, `UPDATE issue SET status='done',revision=revision+1,updated_at=now(),last_activity_at=now()
		WHERE id=$1 AND workspace_id=$2 AND revision=$3 RETURNING revision`,
		issue.ID, issue.WorkspaceID, issue.Revision).Scan(&revision)
	if err != nil || revision != issue.Revision+1 {
		return nil, false, fmt.Errorf("%w: final completion changed: %v", ErrWorkflowAuthorityConflict, err)
	}
	return cancelled, true, nil
}

func cancelWorkflowOutcomeTask(ctx context.Context, q *db.Queries, taskID pgtype.UUID, reason string) (*db.AgentTaskQueue, error) {
	if !taskID.Valid {
		return nil, nil
	}
	task, err := q.CancelAgentTaskWithReason(ctx, db.CancelAgentTaskWithReasonParams{
		ID: taskID, Error: pgtype.Text{String: reason, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := SettleDeliveredDelegatedFailureRecoveries(ctx, q, task); err != nil {
		return nil, err
	}
	return &task, nil
}

// NotifyWorkflowCompletionTask publishes the legacy outcome cancellation only
// after its completion transaction is durable.
func (s *TaskService) NotifyWorkflowCompletionTask(ctx context.Context, workspaceID pgtype.UUID, task *db.AgentTaskQueue) {
	if task != nil {
		s.BroadcastCancelledTasks(ctx, util.UUIDToString(workspaceID), []db.AgentTaskQueue{*task})
	}
}

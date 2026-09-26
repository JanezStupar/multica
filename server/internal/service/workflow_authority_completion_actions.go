package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type WorkflowCompletionActionInput struct {
	CandidateID      string `json:"candidate_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

// ChangeCompletion records a postacceptance hold, release or outcome
// acknowledgment. The issue lock serializes each decision with an in-flight
// provider mutation; a hold cannot claim success while an earlier merge still
// owns that lock.
func (s WorkflowAuthorityService) ChangeCompletion(ctx context.Context, workspaceID, issueID,
	acceptanceID pgtype.UUID, actor WorkflowActor, action string, in WorkflowCompletionActionInput,
) (bool, error) {
	if action != "hold" && action != "release" && action != "complete" && action != "retry-outcome" {
		return false, ErrWorkflowAuthorityInput
	}
	candidateID, err := workflowAuthorityUUID(in.CandidateID)
	if err != nil || in.ExpectedRevision < 1 {
		return false, fmt.Errorf("%w: candidate_id and positive expected_revision are required", ErrWorkflowAuthorityInput)
	}
	in.Reason, err = validateWorkflowActionReason(in.Reason)
	if err != nil {
		return false, err
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	issue, err := lockWorkflowAuthorityIssue(ctx, tx, q, workspaceID, issueID)
	if err != nil {
		return false, err
	}
	if issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID || issue.Revision != in.ExpectedRevision {
		return false, ErrWorkflowAuthorityConflict
	}
	pinned, authority, err := workflowAuthorityPolicy(ctx, s, issue)
	if err != nil {
		return false, err
	}
	if authority.FormatVersion != 2 || issue.Status != authority.AcceptedStatusKey {
		return false, ErrWorkflowAuthorityConflict
	}
	var state, acceptedStatus, acceptedPolicy, actorType string
	var lastErrorClass pgtype.Text
	var actorID, outcomeAgentID, outcomeTaskID, pendingTaskID pgtype.UUID
	var version int16
	var held, outcomeComplete bool
	err = tx.QueryRow(ctx, `SELECT state,completion_version,accepted_status_key,policy_version,actor_type,actor_id,
		outcome_agent_id,outcome_task_id,outcome_request_task_id,hold_delivery,outcome_complete,last_error_class
		FROM issue_workflow_acceptance WHERE id=$1 AND workspace_id=$2 AND issue_id=$3 AND candidate_id=$4
		AND revoked_at IS NULL FOR UPDATE`, acceptanceID, workspaceID, issueID, candidateID).Scan(
		&state, &version, &acceptedStatus, &acceptedPolicy, &actorType, &actorID,
		&outcomeAgentID, &outcomeTaskID, &pendingTaskID, &held, &outcomeComplete, &lastErrorClass)
	if errors.Is(err, pgx.ErrNoRows) || state != "accepted" || version != 2 ||
		acceptedStatus != issue.Status || acceptedPolicy != pinned.Version {
		return false, ErrWorkflowAuthorityConflict
	}
	if err != nil {
		return false, err
	}
	if actor.Type == "member" {
		role, memberID, err := workflowMemberRole(ctx, tx, workspaceID, actor)
		if err != nil {
			return false, err
		}
		if role != "owner" && role != "admin" && !(actorType == "member" && actorID == memberID) {
			return false, ErrWorkflowAuthorityForbidden
		}
	} else {
		if action != "complete" || actor.Type != "agent" || !outcomeTaskID.Valid ||
			actor.ID != util.UUIDToString(outcomeAgentID) || actor.SourceTaskID != util.UUIDToString(outcomeTaskID) {
			return false, ErrWorkflowAuthorityForbidden
		}
		var taskStatus string
		var taskPolicy pgtype.Text
		var taskProfile pgtype.UUID
		err := tx.QueryRow(ctx, `SELECT status,workflow_policy_version,workflow_profile_id FROM agent_task_queue
			WHERE id=$1 AND issue_id=$2 AND agent_id=$3`, outcomeTaskID, issueID, outcomeAgentID).Scan(
			&taskStatus, &taskPolicy, &taskProfile)
		if err != nil || taskStatus != "running" || !taskPolicy.Valid || taskPolicy.String != pinned.Version || !taskProfile.Valid {
			return false, ErrWorkflowAuthorityForbidden
		}
	}
	var changed bool
	var queuedOutcome *db.AgentTaskQueue
	switch action {
	case "hold":
		if actor.Type != "member" || held {
			return false, ErrWorkflowAuthorityConflict
		}
		var pendingMerge bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_workflow_delivery
			WHERE acceptance_id=$1 AND action='merge' AND status IN ('pending','retry','blocked'))`,
			acceptanceID).Scan(&pendingMerge); err != nil {
			return false, err
		}
		if !pendingMerge {
			return false, ErrWorkflowAuthorityConflict
		}
		_, err = tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET hold_delivery=true,held_at=now() WHERE id=$1`, acceptanceID)
		changed = true
	case "release":
		if actor.Type != "member" || !held {
			return false, ErrWorkflowAuthorityConflict
		}
		_, err = tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET hold_delivery=false,released_at=now() WHERE id=$1`, acceptanceID)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE issue_workflow_delivery SET next_attempt_at=now(),updated_at=now()
				WHERE acceptance_id=$1 AND action='merge' AND status IN ('pending','retry')
				AND readiness_done_at IS NOT NULL`, acceptanceID)
		}
		changed = true
	case "complete":
		if outcomeComplete {
			return false, ErrWorkflowAuthorityConflict
		}
		if actor.Type == "agent" {
			if pendingTaskID.Valid && pendingTaskID != outcomeTaskID {
				return false, ErrWorkflowAuthorityConflict
			}
			_, err = tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET outcome_request_task_id=$2,
				outcome_requested_at=now() WHERE id=$1 AND outcome_complete=false`, acceptanceID, outcomeTaskID)
			changed = true
		} else {
			if outcomeTaskID.Valid {
				var taskStatus string
				if err := tx.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id=$1`, outcomeTaskID).Scan(&taskStatus); err != nil {
					return false, err
				}
				if taskStatus == "running" || taskStatus == "dispatched" || taskStatus == "waiting_local_directory" {
					return false, ErrWorkflowAuthorityConflict
				}
				if taskStatus == "queued" || taskStatus == "deferred" {
					cancelled, err := tx.Exec(ctx, `UPDATE agent_task_queue SET status='cancelled',completed_at=now(),
						error='Outcome acknowledged by human',cancelled_by_type='system'
						WHERE id=$1 AND status IN ('queued','deferred') AND started_at IS NULL`, outcomeTaskID)
					if err != nil {
						return false, err
					}
					if cancelled.RowsAffected() != 1 {
						return false, ErrWorkflowAuthorityConflict
					}
				}
			}
			_, err = tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET outcome_complete=true,
				outcome_completed_at=now(),outcome_request_task_id=NULL WHERE id=$1`, acceptanceID)
			changed = true
		}
	case "retry-outcome":
		if actor.Type != "member" || outcomeComplete {
			return false, ErrWorkflowAuthorityConflict
		}
		if !outcomeTaskID.Valid {
			if lastErrorClass.String != "outcome_dispatch_failed" {
				return false, ErrWorkflowAuthorityConflict
			}
			if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET outcome_next_attempt_at=now()
				WHERE id=$1 AND outcome_task_id IS NULL AND last_error_class='outcome_dispatch_failed'`,
				acceptanceID); err != nil {
				return false, err
			}
			changed = true
			break
		}
		var taskStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id=$1 AND issue_id=$2`,
			outcomeTaskID, issueID).Scan(&taskStatus); err != nil {
			return false, err
		}
		if taskStatus != "failed" && taskStatus != "cancelled" {
			return false, ErrWorkflowAuthorityConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET outcome_task_id=NULL,
			outcome_request_task_id=NULL,outcome_requested_at=NULL,last_error_class=NULL
			WHERE id=$1 AND outcome_task_id=$2`, acceptanceID, outcomeTaskID); err != nil {
			return false, err
		}
		queuedOutcome, _, err = ReconcileWorkflowCompletion(ctx, tx, q, issue, acceptanceID)
		if err != nil {
			return false, err
		}
		changed = true
	}
	if err != nil {
		return false, err
	}
	if action == "complete" && actor.Type == "member" {
		if _, _, err := ReconcileWorkflowCompletion(ctx, tx, q, issue, acceptanceID); err != nil {
			return false, err
		}
	}
	var currentRevision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM issue WHERE id=$1 AND workspace_id=$2`,
		issueID, workspaceID).Scan(&currentRevision); err != nil {
		return false, err
	}
	if currentRevision == issue.Revision {
		updated, err := tx.Exec(ctx, `UPDATE issue SET revision=revision+1,updated_at=now(),last_activity_at=now()
			WHERE id=$1 AND workspace_id=$2 AND revision=$3`, issueID, workspaceID, issue.Revision)
		if err != nil {
			return false, err
		}
		if updated.RowsAffected() != 1 {
			return false, ErrWorkflowAuthorityConflict
		}
	}
	details, _ := json.Marshal(map[string]string{"acceptance_id": util.UUIDToString(acceptanceID),
		"candidate_id": in.CandidateID, "reason": in.Reason})
	actorUUID, _ := workflowAuthorityUUID(actor.ID)
	if _, err := tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
		VALUES($1,$2,$3,$4,$5,$6)`, workspaceID, issueID, actor.Type, actorUUID,
		"workflow_"+action, details); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	if queuedOutcome != nil {
		s.Tasks.NotifyTaskEnqueued(ctx, *queuedOutcome)
	}
	if changed {
		s.PublishWorkflowIssueChange(ctx, issue, actor)
	}
	return changed, nil
}

// FinalizeNextOutcomeAcknowledgment turns an agent's pending acknowledgment
// into completion only after that exact outcome task succeeds. A failed task
// remains visible and cannot accidentally finish the accepted issue.
func (s WorkflowAuthorityService) FinalizeNextOutcomeAcknowledgment(ctx context.Context) (bool, error) {
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var issueID, acceptanceID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT a.issue_id,a.id FROM issue_workflow_acceptance a
		JOIN agent_task_queue t ON t.id=a.outcome_request_task_id
		WHERE a.completion_version=2 AND a.state='accepted' AND a.revoked_at IS NULL
		AND NOT a.outcome_complete AND t.status IN ('completed','failed','cancelled')
		AND (a.last_error_class IS DISTINCT FROM 'human_feedback_pending' OR a.outcome_next_attempt_at<=now())
		ORDER BY a.outcome_requested_at,a.id LIMIT 1`).Scan(&issueID, &acceptanceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var workspaceID pgtype.UUID
	err = tx.QueryRow(ctx, `SELECT workspace_id FROM issue WHERE id=$1 FOR UPDATE SKIP LOCKED`, issueID).Scan(&workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	q := s.Tasks.Queries.WithTx(tx)
	issue, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issueID, WorkspaceID: workspaceID})
	if err != nil {
		return false, err
	}
	var candidateID, taskID pgtype.UUID
	var state, taskStatus, policyVersion, acceptedStatus string
	var version int16
	err = tx.QueryRow(ctx, `SELECT a.candidate_id,a.outcome_request_task_id,a.state,a.completion_version,
		a.policy_version,a.accepted_status_key,t.status
		FROM issue_workflow_acceptance a JOIN agent_task_queue t ON t.id=a.outcome_request_task_id
		WHERE a.id=$1 AND a.issue_id=$2 AND a.revoked_at IS NULL FOR UPDATE OF a`, acceptanceID, issueID).Scan(
		&candidateID, &taskID, &state, &version, &policyVersion, &acceptedStatus, &taskStatus)
	if errors.Is(err, pgx.ErrNoRows) || state != "accepted" || version != 2 {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if taskStatus != "completed" || issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID ||
		issue.Status != acceptedStatus || issue.WorkflowPolicy == nil {
		if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET outcome_request_task_id=NULL,
			last_error_class='outcome_task_failed' WHERE id=$1 AND outcome_request_task_id=$2`, acceptanceID, taskID); err != nil {
			return true, err
		}
		if err := tx.Commit(ctx); err != nil {
			return true, err
		}
		s.PublishWorkflowIssueChange(ctx, issue, WorkflowActor{Type: "system"})
		return true, nil
	}
	pinned, err := s.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || pinned == nil || pinned.Version != policyVersion {
		return false, ErrWorkflowAuthorityConflict
	}
	// Agent completion cannot consume a still-unclassified human correction.
	// Keep the exact successful acknowledgment pending for the normal retry;
	// explicit human completion retains its separate authority path.
	pendingFeedback, err := WorkflowHasPendingHumanFeedback(ctx, tx, issue)
	if err != nil {
		return true, err
	}
	if pendingFeedback {
		if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET last_error_class='human_feedback_pending',
			outcome_next_attempt_at=now()+interval '5 seconds' WHERE id=$1 AND outcome_request_task_id=$2`, acceptanceID, taskID); err != nil {
			return true, err
		}
		if err := tx.Commit(ctx); err != nil {
			return true, err
		}
		return true, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET outcome_complete=true,
		outcome_completed_at=now(),outcome_request_task_id=NULL,last_error_class=NULL
		WHERE id=$1 AND outcome_request_task_id=$2 AND outcome_complete=false`, acceptanceID, taskID); err != nil {
		return true, err
	}
	if _, _, err := ReconcileWorkflowCompletion(ctx, tx, q, issue, acceptanceID); err != nil {
		return true, err
	}
	if err := tx.Commit(ctx); err != nil {
		return true, err
	}
	s.PublishWorkflowIssueChange(ctx, issue, WorkflowActor{Type: "agent"})
	return true, nil
}

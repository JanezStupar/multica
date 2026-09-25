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

// FinalizeNextRequestedAcceptance is safe to run on every server replica. The
// requested row is durable while an agent is running; only that task's
// successful terminal outcome can turn it into accepted authority.
func (s WorkflowAuthorityService) FinalizeNextRequestedAcceptance(ctx context.Context) (bool, error) {
	if s.Tasks == nil || s.Tasks.TxStarter == nil {
		return false, ErrWorkflowAuthorityUnavailable
	}
	// Terminal-source filtering avoids one long-running request starving other
	// completed requests in the bounded poller.
	var issueID, acceptanceID pgtype.UUID
	tx, beginErr := s.Tasks.TxStarter.Begin(ctx)
	if beginErr != nil {
		return false, beginErr
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout='2s'`); err != nil {
		return false, err
	}
	queryErr := tx.QueryRow(ctx, `SELECT a.issue_id,a.id FROM issue_workflow_acceptance a
		JOIN agent_task_queue t ON t.id=a.source_task_id AND t.issue_id=a.issue_id
		WHERE a.state='requested' AND a.revoked_at IS NULL AND a.next_attempt_at<=now()
		AND t.status IN ('completed','failed','cancelled')
		ORDER BY a.requested_at,a.id LIMIT 1`).Scan(&issueID, &acceptanceID)
	if errors.Is(queryErr, pgx.ErrNoRows) {
		return false, nil
	}
	if queryErr != nil {
		return false, queryErr
	}
	q := s.Tasks.Queries.WithTx(tx)
	var workspaceID pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT workspace_id FROM issue WHERE id=$1 FOR UPDATE SKIP LOCKED`, issueID).Scan(&workspaceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	issue, err := q.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: issueID, WorkspaceID: workspaceID})
	if err != nil {
		return false, err
	}
	var candidateID, sourceTaskID, actorID pgtype.UUID
	var snapshot []byte
	var sourceStatus, acceptanceState, acceptedPolicyVersion string
	err = tx.QueryRow(ctx, `SELECT a.candidate_id,a.source_task_id,a.actor_id,a.authority_snapshot,
		a.state,a.policy_version,t.status FROM issue_workflow_acceptance a
		JOIN agent_task_queue t ON t.id=a.source_task_id AND t.issue_id=a.issue_id
		WHERE a.id=$1 AND a.issue_id=$2 FOR UPDATE OF a`, acceptanceID, issue.ID).Scan(
		&candidateID, &sourceTaskID, &actorID, &snapshot, &acceptanceState, &acceptedPolicyVersion, &sourceStatus)
	if errors.Is(err, pgx.ErrNoRows) || acceptanceState != "requested" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	block := func(reason string) (bool, error) {
		_, updateErr := tx.Exec(ctx, `UPDATE issue_workflow_acceptance
			SET state='blocked',last_error_class=$2,
			    authority_snapshot=jsonb_set(authority_snapshot,'{blocker}',to_jsonb($2::text),true)
			WHERE id=$1 AND state='requested'`, acceptanceID, reason)
		if updateErr != nil {
			return true, updateErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return true, commitErr
		}
		s.PublishWorkflowIssueChange(ctx, issue, WorkflowActor{Type: "agent", ID: util.UUIDToString(actorID)})
		return true, nil
	}
	if sourceStatus != "completed" {
		return block("requester_task_failed")
	}
	var stored struct {
		Request               workflowAcceptanceRequest `json:"request"`
		DeliveryAction        string                    `json:"delivery_action"`
		MergeMethod           string                    `json:"merge_method"`
		ReviewExceptionID     string                    `json:"review_exception_id"`
		DeliveryExceptionID   string                    `json:"delivery_exception_id"`
		AcceptanceExceptionID string                    `json:"acceptance_exception_id"`
		ScopeDigest           string                    `json:"scope_digest"`
	}
	if json.Unmarshal(snapshot, &stored) != nil {
		return block("acceptance_snapshot_invalid")
	}
	if issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID || issue.Revision != stored.Request.ExpectedRevision ||
		issue.Status != "in_review" || issue.AssigneeType.String != "agent" || issue.AssigneeID != actorID {
		return block("candidate_or_owner_changed")
	}
	pinned, authority, err := workflowAuthorityPolicy(ctx, s, issue)
	if err != nil || pinned.Version != acceptedPolicyVersion {
		return block("pinned_authority_changed")
	}
	if err := ValidateWorkflowCompletionConfig(ctx, tx, issue, authority); err != nil {
		return block("completion_config_changed")
	}
	allowed := false
	for _, id := range authority.AutonomousAgentIDs {
		allowed = allowed || id == util.UUIDToString(actorID)
	}
	acceptanceExceptionID, acceptanceGrant, err := workflowExceptionGrant(ctx, tx, issue, candidateID, pinned.Version, "acceptance")
	if err != nil {
		return true, err
	}
	if stored.AcceptanceExceptionID != util.UUIDToString(acceptanceExceptionID) {
		return block("acceptance_authority_changed")
	}
	allowed = authority.AutonomousEnabled && allowed || acceptanceGrant["agent_actor_id"] == util.UUIDToString(actorID)
	if !allowed || stored.Request.ClassificationReason == "" {
		return block("autonomous_authority_missing")
	}
	candidate, err := loadCurrentWorkflowCandidate(ctx, tx, issue, pinned.Version)
	if err != nil {
		return block("candidate_scope_changed")
	}
	if stale, err := workflowCandidateStale(ctx, tx, issue, candidate.ID); err != nil {
		return true, err
	} else if stale {
		return block("candidate_head_changed")
	}
	if stored.ScopeDigest != candidate.ScopeDigest {
		return block("candidate_scope_changed")
	}
	active, err := workflowHasMutableRuns(ctx, tx, issue.ID, pgtype.UUID{})
	if err != nil {
		return true, err
	}
	pending, err := workflowHasPendingHandoff(ctx, tx, issue.ID)
	if err != nil {
		return true, err
	}
	if active || pending {
		return block("concurrent_work_or_handoff")
	}
	bindings, err := workflowDeliveryBindings(ctx, tx, issue, candidate.PRs)
	if err != nil {
		return block("provider_binding_changed")
	}
	reviewExceptionID, reviewGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, pinned.Version, "review")
	if err != nil {
		return true, err
	}
	if stored.ReviewExceptionID != util.UUIDToString(reviewExceptionID) {
		return block("review_authority_changed")
	}
	_, err = workflowReviewEvidence(ctx, s, tx, issue, candidate, bindings,
		authority.ReviewRequired && reviewGrant["waive"] != true, true, pgtype.UUID{})
	if errors.Is(err, ErrWorkflowAuthorityUnavailable) {
		if _, updateErr := tx.Exec(ctx, `UPDATE issue_workflow_acceptance
			SET next_attempt_at=now()+interval '5 seconds',last_error_class='provider_unavailable'
			WHERE id=$1 AND state='requested'`, acceptanceID); updateErr != nil {
			return true, updateErr
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return true, commitErr
		}
		s.PublishWorkflowIssueChange(ctx, issue, WorkflowActor{Type: "agent", ID: util.UUIDToString(actorID)})
		return true, nil
	}
	if err != nil {
		return block("review_evidence_changed")
	}
	deliveryExceptionID, deliveryGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, pinned.Version, "delivery")
	if err != nil {
		return true, err
	}
	if stored.DeliveryExceptionID != util.UUIDToString(deliveryExceptionID) {
		return block("delivery_authority_changed")
	}
	action, method, ordered, err := workflowDeliveryPlan(candidate.PRs, authority, "trivial", stored.Request.MergeOrderPRURLs, deliveryGrant)
	if err != nil {
		return block("delivery_authority_changed")
	}
	if action != stored.DeliveryAction || method != stored.MergeMethod {
		return block("delivery_authority_changed")
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET state='accepted',issue_revision=$2,accepted_at=now(),
		outcome_completed_at=CASE WHEN outcome_complete THEN now() ELSE NULL END
		WHERE id=$1 AND state='requested'`, acceptanceID, issue.Revision+1); err != nil {
		return true, err
	}
	outcomeTask, err := finalizeWorkflowAcceptance(ctx, tx, q, issue, acceptanceID,
		WorkflowActor{Type: "agent", ID: util.UUIDToString(actorID), SourceTaskID: util.UUIDToString(sourceTaskID)},
		ordered, bindings, action, method, authority, stored.Request)
	if err != nil {
		return true, fmt.Errorf("finalize autonomous acceptance: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return true, err
	}
	if outcomeTask != nil {
		s.Tasks.NotifyTaskEnqueued(ctx, *outcomeTask)
	}
	s.PublishWorkflowIssueChange(ctx, issue, WorkflowActor{Type: "agent", ID: util.UUIDToString(actorID)})
	return true, nil
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
)

type WorkflowDeliveryRetryInput struct {
	CandidateID      string `json:"candidate_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason,omitempty"`
}

func workflowDeliveryRetryActorAllowed(ctx context.Context, tx pgx.Tx, workspaceID, issueID, candidateID pgtype.UUID,
	policyVersion string, authority WorkflowAuthorityPolicy, actor WorkflowActor,
	acceptedByType string, acceptedByID pgtype.UUID, exceptionID string) (bool, error) {
	role, actorID, err := workflowMemberRole(ctx, tx, workspaceID, actor)
	if err != nil {
		return false, err
	}
	allowed := role == "owner" || role == "admin"
	if acceptedByType != "member" || acceptedByID != actorID {
		return allowed, nil
	}
	for _, acceptedRole := range authority.HumanAcceptRoles {
		allowed = allowed || role == acceptedRole
	}
	if !allowed && exceptionID != "" {
		parsedExceptionID, err := workflowAuthorityUUID(exceptionID)
		if err != nil {
			return false, ErrWorkflowAuthorityConflict
		}
		var namedActor string
		err = tx.QueryRow(ctx, `SELECT grant_details->>'human_actor_id' FROM issue_workflow_exception
			WHERE id=$1 AND workspace_id=$2 AND issue_id=$3 AND candidate_id=$4
			AND scope='acceptance' AND base_policy_version=$5 AND revoked_at IS NULL`,
			parsedExceptionID, workspaceID, issueID, candidateID, policyVersion).Scan(&namedActor)
		allowed = err == nil && namedActor == actor.ID
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
	}
	return allowed, nil
}

func workflowDeliveryNewerHeadKnown(ctx context.Context, tx pgx.Tx, workspaceID, issueID, deliveryID pgtype.UUID,
	expectedHead string) (bool, error) {
	var known bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_workflow_delivery_attempt
		WHERE workspace_id=$1 AND issue_id=$2 AND delivery_id=$3
		AND observed_head_sha IS NOT NULL AND observed_head_sha<>'' AND observed_head_sha<>$4)`,
		workspaceID, issueID, deliveryID, expectedHead).Scan(&known)
	return known, err
}

// RetryDelivery requeues an existing blocked delivery under its original
// acceptance. It does not change the approved action, PR head, or provider
// binding, and the worker revalidates all three before touching the provider.
func (s WorkflowAuthorityService) RetryDelivery(ctx context.Context, workspaceID, issueID, deliveryID pgtype.UUID,
	actor WorkflowActor, in WorkflowDeliveryRetryInput) (bool, error) {
	if s.Tasks == nil || s.Tasks.TxStarter == nil {
		return false, ErrWorkflowAuthorityUnavailable
	}
	candidateID, err := workflowAuthorityUUID(in.CandidateID)
	if err != nil || in.ExpectedRevision < 1 {
		return false, fmt.Errorf("%w: candidate_id and positive expected_revision are required", ErrWorkflowAuthorityInput)
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if len(in.Reason) > 500 {
		return false, fmt.Errorf("%w: reason must be at most 500 bytes", ErrWorkflowAuthorityInput)
	}
	if actor.Type != "member" {
		return false, ErrWorkflowAuthorityForbidden
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
	if authority.FormatVersion == 2 && issue.Status != authority.AcceptedStatusKey ||
		authority.FormatVersion == 1 && issue.Status != "done" {
		return false, ErrWorkflowAuthorityConflict
	}
	candidate, err := loadCurrentWorkflowCandidate(ctx, tx, issue, pinned.Version)
	if err != nil {
		return false, err
	}
	var row struct {
		acceptanceID    pgtype.UUID
		candidateID     pgtype.UUID
		status          string
		expectedHead    string
		lastError       pgtype.Text
		acceptanceState string
		acceptedByType  string
		acceptedByID    pgtype.UUID
		policyVersion   string
		scopeDigest     string
		exceptionID     string
	}
	err = tx.QueryRow(ctx, `SELECT d.acceptance_id,d.candidate_id,d.status,d.expected_head_sha,d.last_error_class,
		a.state,a.actor_type,a.actor_id,a.policy_version,
		COALESCE(a.authority_snapshot->>'scope_digest',''),
		COALESCE(a.authority_snapshot->>'acceptance_exception_id','')
		FROM issue_workflow_delivery d
		JOIN issue_workflow_acceptance a ON a.id=d.acceptance_id AND a.workspace_id=d.workspace_id
			AND a.issue_id=d.issue_id AND a.candidate_id=d.candidate_id
		WHERE d.id=$1 AND d.workspace_id=$2 AND d.issue_id=$3 AND a.revoked_at IS NULL
		FOR UPDATE OF d`, deliveryID, workspaceID, issueID).Scan(
		&row.acceptanceID, &row.candidateID, &row.status, &row.expectedHead, &row.lastError,
		&row.acceptanceState, &row.acceptedByType, &row.acceptedByID, &row.policyVersion,
		&row.scopeDigest, &row.exceptionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrWorkflowAuthorityConflict
		}
		return false, err
	}
	if row.candidateID != candidate.ID || row.acceptanceState != "accepted" || row.policyVersion != pinned.Version ||
		row.scopeDigest != candidate.ScopeDigest {
		return false, ErrWorkflowAuthorityConflict
	}
	allowed, err := workflowDeliveryRetryActorAllowed(ctx, tx, workspaceID, issueID, candidateID, pinned.Version,
		authority, actor, row.acceptedByType, row.acceptedByID, row.exceptionID)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, ErrWorkflowAuthorityForbidden
	}
	if row.status != "blocked" && row.status != "pending" && row.status != "retry" {
		return false, ErrWorkflowAuthorityConflict
	}
	newerHeadKnown, err := workflowDeliveryNewerHeadKnown(ctx, tx, workspaceID, issueID, deliveryID, row.expectedHead)
	if err != nil {
		return false, err
	}
	if newerHeadKnown {
		return false, fmt.Errorf("%w: a different PR head was observed; evaluate a new candidate", ErrWorkflowAuthorityConflict)
	}
	if row.status == "pending" || row.status == "retry" {
		return false, tx.Commit(ctx)
	}
	_, actorID, err := workflowMemberRole(ctx, tx, workspaceID, actor)
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE issue_workflow_delivery SET status='retry',next_attempt_at=now(),
		last_error_class=NULL,updated_at=now() WHERE id=$1 AND workspace_id=$2 AND issue_id=$3 AND status='blocked'`,
		deliveryID, workspaceID, issueID); err != nil {
		return false, err
	}
	details, _ := json.Marshal(map[string]any{"delivery_id": util.UUIDToString(deliveryID),
		"acceptance_id": util.UUIDToString(row.acceptanceID), "candidate_id": in.CandidateID,
		"previous_error_class": row.lastError.String, "reason": in.Reason})
	if _, err = tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
		VALUES($1,$2,'member',$3,'workflow_delivery_retried',$4)`, workspaceID, issueID, actorID, details); err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	s.PublishWorkflowIssueChange(ctx, issue, actor)
	return true, nil
}

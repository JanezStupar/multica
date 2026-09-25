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
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func normalizedWorkflowExceptionGrant(scope string, details map[string]any) (map[string]any, error) {
	bad := func() (map[string]any, error) {
		return nil, fmt.Errorf("%w: unsupported %s exception grant", ErrWorkflowAuthorityInput, scope)
	}
	switch scope {
	case "review":
		if len(details) != 1 || details["waive"] != true {
			return bad()
		}
		return map[string]any{"waive": true}, nil
	case "acceptance":
		if len(details) != 1 {
			return bad()
		}
		for _, key := range []string{"human_actor_id", "agent_actor_id"} {
			if raw, ok := details[key]; ok {
				actorText, ok := raw.(string)
				if !ok {
					return bad()
				}
				actorID, err := workflowAuthorityUUID(actorText)
				if err != nil {
					return bad()
				}
				return map[string]any{key: util.UUIDToString(actorID)}, nil
			}
		}
		return bad()
	case "delivery":
		action, ok := details["action"].(string)
		if !ok {
			return bad()
		}
		if action == "ready" && len(details) == 1 {
			return map[string]any{"action": "ready"}, nil
		}
		if action == "merge" && len(details) == 2 {
			method, ok := details["merge_method"].(string)
			if ok && (method == "merge" || method == "squash" || method == "rebase") {
				return map[string]any{"action": "merge", "merge_method": method}, nil
			}
		}
		return bad()
	case "external_merge":
		if len(details) != 1 || details["accept_merged_head"] != true {
			return bad()
		}
		return map[string]any{"accept_merged_head": true}, nil
	default:
		return bad()
	}
}

func workflowExceptionReason(reason, consequences string) (string, string, error) {
	reason, consequences = strings.TrimSpace(reason), strings.TrimSpace(consequences)
	if reason == "" || consequences == "" || len(reason) > 4096 || len(consequences) > 4096 {
		return "", "", fmt.Errorf("%w: reason and consequences are required (4096 bytes maximum each)", ErrWorkflowAuthorityInput)
	}
	return reason, consequences, nil
}

func authorizeWorkflowExceptionActor(ctx context.Context, tx pgx.Tx, workspaceID pgtype.UUID,
	issue db.Issue, policy WorkflowAuthorityPolicy, policyVersion, scope string, actor WorkflowActor) (pgtype.UUID, pgtype.UUID, error) {
	if actor.Type == "member" {
		role, actorID, err := workflowMemberRole(ctx, tx, workspaceID, actor)
		if err != nil || role != "owner" && role != "admin" {
			return pgtype.UUID{}, pgtype.UUID{}, ErrWorkflowAuthorityForbidden
		}
		return actorID, pgtype.UUID{}, nil
	}
	delegatedScope := scope
	if scope == "external_merge" {
		delegatedScope = "delivery"
	}
	if actor.Type != "agent" || !policy.SupervisorAgentScopes[actor.ID][delegatedScope] {
		return pgtype.UUID{}, pgtype.UUID{}, ErrWorkflowAuthorityForbidden
	}
	agentID, taskID, err := workflowAgentTask(ctx, tx, issue, actor)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	var taskPolicy pgtype.Text
	var taskProfile pgtype.UUID
	if err = tx.QueryRow(ctx, `SELECT workflow_policy_version,workflow_profile_id FROM agent_task_queue
		WHERE id=$1 AND issue_id=$2 AND agent_id=$3`, taskID, issue.ID, agentID).Scan(&taskPolicy, &taskProfile); err != nil ||
		!taskPolicy.Valid || !taskProfile.Valid || taskPolicy.String != policyVersion {
		return pgtype.UUID{}, pgtype.UUID{}, ErrWorkflowAuthorityForbidden
	}
	return agentID, taskID, nil
}

func workflowExceptionAcceptanceState(ctx context.Context, tx pgx.Tx, issue db.Issue, candidateID pgtype.UUID) (string, error) {
	var state string
	err := tx.QueryRow(ctx, `SELECT state FROM issue_workflow_acceptance
		WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3 AND revoked_at IS NULL
		AND state IN ('requested','accepted') ORDER BY requested_at DESC,id DESC LIMIT 1`,
		issue.WorkspaceID, issue.ID, candidateID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return state, err
}

func blockRequestedWorkflowAcceptance(ctx context.Context, tx pgx.Tx, issue db.Issue, candidateID pgtype.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET state='blocked',revoked_at=now()
		WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3 AND state='requested' AND revoked_at IS NULL`,
		issue.WorkspaceID, issue.ID, candidateID)
	return err
}

// GrantException records only the listed machine-readable authority for one
// current candidate. Instructions and profile reselection cannot write here.
func (s WorkflowAuthorityService) GrantException(ctx context.Context, workspaceID, issueID pgtype.UUID,
	actor WorkflowActor, in WorkflowExceptionInput) (string, error) {
	if s.Tasks == nil || s.Tasks.TxStarter == nil {
		return "", ErrWorkflowAuthorityUnavailable
	}
	candidateID, err := workflowAuthorityUUID(in.CandidateID)
	if err != nil || in.ExpectedRevision < 1 {
		return "", fmt.Errorf("%w: candidate_id and expected_revision are required", ErrWorkflowAuthorityInput)
	}
	grant, err := normalizedWorkflowExceptionGrant(in.Scope, in.GrantDetails)
	if err != nil {
		return "", err
	}
	reason, consequences, err := workflowExceptionReason(in.Reason, in.Consequences)
	if err != nil {
		return "", err
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	issue, err := lockWorkflowAuthorityIssue(ctx, tx, q, workspaceID, issueID)
	if err != nil {
		return "", err
	}
	if issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID || issue.Revision != in.ExpectedRevision {
		return "", ErrWorkflowAuthorityConflict
	}
	pinned, authority, err := workflowAuthorityPolicy(ctx, s, issue)
	if err != nil {
		return "", err
	}
	if in.Scope == "external_merge" {
		if authority.FormatVersion != 2 || issue.Status != "in_review" && issue.Status != authority.AcceptedStatusKey {
			return "", ErrWorkflowAuthorityConflict
		}
	} else if issue.Status != "in_review" {
		return "", ErrWorkflowAuthorityConflict
	}
	if _, err = loadCurrentWorkflowCandidate(ctx, tx, issue, pinned.Version); err != nil {
		return "", err
	}
	actorID, sourceTaskID, err := authorizeWorkflowExceptionActor(ctx, tx, workspaceID, issue, authority, pinned.Version, in.Scope, actor)
	if err != nil {
		return "", err
	}
	state, err := workflowExceptionAcceptanceState(ctx, tx, issue, candidateID)
	if err != nil {
		return "", err
	}
	if state == "accepted" && in.Scope != "external_merge" {
		return "", fmt.Errorf("%w: reject the accepted candidate before changing authority", ErrWorkflowAuthorityConflict)
	}
	if state == "requested" && in.Scope == "external_merge" {
		return "", fmt.Errorf("%w: a newer acceptance request must resolve before external merge reconciliation", ErrWorkflowAuthorityConflict)
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_workflow_exception
		WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3 AND base_policy_version=$4
		AND scope=$5 AND revoked_at IS NULL)`, workspaceID, issueID, candidateID, pinned.Version, in.Scope).Scan(&active)
	if err != nil {
		return "", err
	}
	if active {
		return "", fmt.Errorf("%w: active exception already exists for candidate and scope", ErrWorkflowAuthorityConflict)
	}
	if state == "requested" {
		if err = blockRequestedWorkflowAcceptance(ctx, tx, issue, candidateID); err != nil {
			return "", err
		}
	}
	grantJSON, _ := json.Marshal(grant)
	id := dbid.NewV7()
	_, err = tx.Exec(ctx, `INSERT INTO issue_workflow_exception
		(id,workspace_id,issue_id,candidate_id,base_policy_version,scope,grant_details,actor_type,actor_id,
		source_task_id,reason,consequences) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		id, workspaceID, issueID, candidateID, pinned.Version, in.Scope, grantJSON, actor.Type, actorID, sourceTaskID, reason, consequences)
	if err != nil {
		return "", err
	}
	if in.Scope == "external_merge" {
		if _, err = tx.Exec(ctx, `UPDATE issue_workflow_delivery SET next_attempt_at=now(),updated_at=now()
			WHERE issue_id=$1 AND candidate_id=$2 AND status='stale' AND merged_at IS NULL`, issueID, candidateID); err != nil {
			return "", err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE issue SET revision=revision+1,updated_at=now() WHERE id=$1 AND workspace_id=$2`, issueID, workspaceID); err != nil {
		return "", err
	}
	details, _ := json.Marshal(map[string]any{"exception_id": util.UUIDToString(id), "candidate_id": in.CandidateID,
		"scope": in.Scope, "base_policy_version": pinned.Version, "reason": reason, "consequences": consequences})
	if _, err = tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
		VALUES($1,$2,$3,$4,'workflow_exception_granted',$5)`, workspaceID, issueID, actor.Type, actorID, details); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	s.PublishWorkflowIssueChange(ctx, issue, actor)
	return util.UUIDToString(id), nil
}

// RevokeException retains the original grant and records an explicit reason
// and actor. A requested acceptance is blocked; an accepted decision needs a
// rejection, preserving its already-issued delivery intent.
func (s WorkflowAuthorityService) RevokeException(ctx context.Context, workspaceID, issueID, exceptionID pgtype.UUID,
	actor WorkflowActor, in WorkflowExceptionRevokeInput) error {
	if s.Tasks == nil || s.Tasks.TxStarter == nil {
		return ErrWorkflowAuthorityUnavailable
	}
	if in.ExpectedRevision < 1 {
		return fmt.Errorf("%w: expected_revision is required", ErrWorkflowAuthorityInput)
	}
	reason, consequences, err := workflowExceptionReason(in.Reason, in.Consequences)
	if err != nil {
		return err
	}
	tx, err := s.Tasks.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := s.Tasks.Queries.WithTx(tx)
	issue, err := lockWorkflowAuthorityIssue(ctx, tx, q, workspaceID, issueID)
	if err != nil {
		return err
	}
	if issue.WorkflowFrozen || issue.Revision != in.ExpectedRevision {
		return ErrWorkflowAuthorityConflict
	}
	pinned, authority, err := workflowAuthorityPolicy(ctx, s, issue)
	if err != nil {
		return err
	}
	if _, err = loadCurrentWorkflowCandidate(ctx, tx, issue, pinned.Version); err != nil {
		return err
	}
	var candidateID pgtype.UUID
	var scope, basePolicy, grantorType string
	var grantorID pgtype.UUID
	var revokedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx, `SELECT candidate_id,scope,base_policy_version,actor_type,actor_id,revoked_at
		FROM issue_workflow_exception WHERE id=$1 AND workspace_id=$2 AND issue_id=$3 FOR UPDATE`,
		exceptionID, workspaceID, issueID).Scan(&candidateID, &scope, &basePolicy, &grantorType, &grantorID, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWorkflowAuthorityConflict
	}
	if err != nil {
		return err
	}
	if revokedAt.Valid || candidateID != issue.WorkflowCandidateID || basePolicy != pinned.Version {
		return ErrWorkflowAuthorityConflict
	}
	if scope == "external_merge" {
		if authority.FormatVersion != 2 || issue.Status != "in_review" && issue.Status != authority.AcceptedStatusKey {
			return ErrWorkflowAuthorityConflict
		}
	} else if issue.Status != "in_review" {
		return ErrWorkflowAuthorityConflict
	}
	actorID, sourceTaskID, err := authorizeWorkflowExceptionActor(ctx, tx, workspaceID, issue, authority, pinned.Version, scope, actor)
	if err != nil {
		return err
	}
	if actor.Type == "agent" && (grantorType != "agent" || grantorID != actorID) {
		return ErrWorkflowAuthorityForbidden
	}
	state, err := workflowExceptionAcceptanceState(ctx, tx, issue, candidateID)
	if err != nil {
		return err
	}
	if state == "accepted" && scope != "external_merge" {
		return fmt.Errorf("%w: reject the accepted candidate before revoking authority", ErrWorkflowAuthorityConflict)
	}
	if state == "requested" && scope == "external_merge" {
		return fmt.Errorf("%w: a newer acceptance request must resolve before external merge revocation", ErrWorkflowAuthorityConflict)
	}
	if state == "requested" {
		if err = blockRequestedWorkflowAcceptance(ctx, tx, issue, candidateID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE issue_workflow_exception SET revoked_at=now(),revocation_reason=$2,
		revocation_consequences=$3,revoked_by_type=$4,revoked_by_id=$5 WHERE id=$1 AND revoked_at IS NULL`,
		exceptionID, reason, consequences, actor.Type, actorID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE issue SET revision=revision+1,updated_at=now() WHERE id=$1 AND workspace_id=$2`, issueID, workspaceID); err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]any{"exception_id": util.UUIDToString(exceptionID), "candidate_id": util.UUIDToString(candidateID),
		"scope": scope, "base_policy_version": pinned.Version, "reason": reason, "consequences": consequences,
		"source_task_id": util.UUIDToString(sourceTaskID)})
	if _, err = tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
		VALUES($1,$2,$3,$4,'workflow_exception_revoked',$5)`, workspaceID, issueID, actor.Type, actorID, details); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	s.PublishWorkflowIssueChange(ctx, issue, actor)
	return nil
}

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// WorkflowCommentAuthority is resolved from stored source evidence while the
// issue is locked. MemberID is the human decision maker, while SourceTaskID is
// the authenticated agent run that presented the decision to the server.
type WorkflowCommentAuthority struct {
	MemberID     pgtype.UUID
	SourceTaskID pgtype.UUID
	Snapshot     map[string]any
}

type workflowCommentMergeUpgrade struct {
	Request         WorkflowCommentAcceptanceInput `json:"request"`
	MemberID        string                         `json:"member_id"`
	ExecutorAgentID string                         `json:"executor_agent_id"`
	ExecutorTaskID  string                         `json:"executor_task_id"`
	SourceSnapshot  map[string]any                 `json:"source_snapshot"`
	ReviewID        string                         `json:"review_id"`
}

func workflowCommentReplayTaskBound(ctx context.Context, tx pgx.Tx, issue db.Issue, actor WorkflowActor) (bool, error) {
	if actor.Type != "agent" {
		return false, nil
	}
	agentID, err := workflowAuthorityUUID(actor.ID)
	if err != nil {
		return false, nil
	}
	taskID, err := workflowAuthorityUUID(actor.SourceTaskID)
	if err != nil {
		return false, nil
	}
	var bound bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent_task_queue
		WHERE id=$1 AND issue_id=$2 AND agent_id=$3)`, taskID, issue.ID, agentID).Scan(&bound)
	return bound, err
}

func workflowCommentSourceCreatedAt(snapshot map[string]any) (time.Time, bool) {
	switch value := snapshot["source_created_at"].(type) {
	case time.Time:
		return value, !value.IsZero()
	case string:
		created, err := time.Parse(time.RFC3339Nano, value)
		return created, err == nil
	default:
		return time.Time{}, false
	}
}

func normalizeWorkflowCommentAcceptanceInput(in WorkflowCommentAcceptanceInput) (WorkflowCommentAcceptanceInput, workflowAcceptanceRequest, error) {
	in.Source = strings.TrimSpace(in.Source)
	in.SourceID = strings.TrimSpace(in.SourceID)
	in.Action = strings.TrimSpace(in.Action)
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Action == "" {
		in.Action = "ready"
	}
	if in.Source != "multica" && in.Source != "forgejo" || in.SourceID == "" ||
		(in.Action != "ready" && in.Action != "merge") || in.Reason == "" || len(in.Reason) > 2000 ||
		in.ReleaseHold && in.Action != "merge" {
		return in, workflowAcceptanceRequest{}, fmt.Errorf("%w: source, source_id, ready or merge action, and reason are required", ErrWorkflowAuthorityInput)
	}
	if in.Source == "multica" {
		if _, err := workflowAuthorityUUID(in.SourceID); err != nil {
			return in, workflowAcceptanceRequest{}, fmt.Errorf("%w: invalid Multica comment id", ErrWorkflowAuthorityInput)
		}
	}
	base, err := normalizeWorkflowAcceptanceInput(WorkflowAcceptanceInput{
		CandidateID: in.CandidateID, ExpectedRevision: in.ExpectedRevision,
		MergeOrderPRURLs: in.MergeOrderPRURLs,
	})
	if err != nil {
		return in, workflowAcceptanceRequest{}, err
	}
	if len(base.MergeOrderPRURLs) == 0 {
		in.MergeOrderPRURLs = nil
	} else {
		in.MergeOrderPRURLs = base.MergeOrderPRURLs
	}
	base.CommentDecision = &in
	return in, base, nil
}

// AcceptWorkflowComment records a human decision communicated through one
// exact source comment. The authenticated agent only interprets the words;
// source provenance, member authority and the current candidate are checked
// transactionally by the server.
func (s WorkflowAuthorityService) AcceptWorkflowComment(ctx context.Context, workspaceID, issueID pgtype.UUID,
	actor WorkflowActor, in WorkflowCommentAcceptanceInput) (string, error) {
	if actor.Type != "agent" {
		return "", ErrWorkflowAuthorityForbidden
	}
	_, request, err := normalizeWorkflowCommentAcceptanceInput(in)
	if err != nil {
		return "", err
	}
	return s.acceptWorkflow(ctx, workspaceID, issueID, actor, request, request.CommentDecision)
}

func workflowMulticaCommentAuthority(ctx context.Context, tx pgx.Tx, issue db.Issue,
	actor WorkflowActor, in WorkflowCommentAcceptanceInput) (WorkflowCommentAuthority, error) {
	var proof WorkflowCommentAuthority
	agentID, sourceTaskID, err := workflowAgentTask(ctx, tx, issue, actor)
	if err != nil {
		return proof, err
	}
	commentID, err := workflowAuthorityUUID(in.SourceID)
	if err != nil {
		return proof, ErrWorkflowAuthorityInput
	}
	var content, role string
	var revision int64
	var authorID pgtype.UUID
	var createdAt, updatedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx, `SELECT c.author_id,c.content,c.revision,c.created_at,c.updated_at,m.role
		FROM comment c
		JOIN member m ON m.workspace_id=c.workspace_id AND m.user_id=c.author_id
		JOIN agent_task_queue task ON task.id=$4 AND task.issue_id=c.issue_id AND task.agent_id=$5
		JOIN issue_workflow_candidate candidate ON candidate.id=$6 AND candidate.issue_id=c.issue_id
			AND candidate.workspace_id=c.workspace_id
		WHERE c.id=$1 AND c.workspace_id=$2 AND c.issue_id=$3
		AND c.author_type='member' AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
		AND c.created_at>candidate.created_at
		AND (task.trigger_comment_id=c.id OR c.id=ANY(task.coalesced_comment_ids))
		AND c.id=ANY(task.delivered_comment_ids)
		AND task.dispatched_at IS NOT NULL AND c.updated_at<=task.dispatched_at
		AND task.originator_user_id=c.author_id AND task.accountable_user_id=c.author_id
		FOR SHARE OF c`, commentID, issue.WorkspaceID, issue.ID, sourceTaskID, agentID,
		issue.WorkflowCandidateID).Scan(&authorID, &content, &revision, &createdAt, &updatedAt, &role)
	if err == pgx.ErrNoRows {
		return proof, fmt.Errorf("%w: source comment was not delivered to this task for the current candidate", ErrWorkflowAuthorityForbidden)
	}
	if err != nil {
		return proof, err
	}
	if strings.TrimSpace(content) == "" || strings.EqualFold(strings.Fields(content)[0], "/note") {
		return proof, ErrWorkflowAuthorityForbidden
	}
	proof.MemberID = authorID
	proof.SourceTaskID = sourceTaskID
	proof.Snapshot = map[string]any{
		"source": "multica", "source_id": in.SourceID, "source_revision": revision,
		"source_content": content, "source_created_at": createdAt.Time,
		"source_updated_at": updatedAt.Time, "member_id": util.UUIDToString(authorID),
		"member_role": role, "executor_agent_id": actor.ID,
		"executor_task_id": actor.SourceTaskID,
	}
	return proof, nil
}

func workflowCommentAuthority(ctx context.Context, tx pgx.Tx, issue db.Issue,
	actor WorkflowActor, in WorkflowCommentAcceptanceInput) (WorkflowCommentAuthority, error) {
	switch in.Source {
	case "multica":
		return workflowMulticaCommentAuthority(ctx, tx, issue, actor, in)
	case "forgejo":
		return workflowForgejoCommentAuthority(ctx, tx, issue, actor, in)
	default:
		return WorkflowCommentAuthority{}, ErrWorkflowAuthorityInput
	}
}

// workflowCommentAcceptanceAvailable is an affordance check. AcceptWorkflowComment
// repeats all proofs under the issue lock before writing a decision.
func workflowCommentAcceptanceAvailable(ctx context.Context, tx pgx.Tx, issue db.Issue,
	actor WorkflowActor, authority WorkflowAuthorityPolicy, policyVersion string) (bool, error) {
	if actor.Type != "agent" || !issue.WorkflowCandidateID.Valid {
		return false, nil
	}
	agentID, taskID, err := workflowAgentTask(ctx, tx, issue, actor)
	if err != nil {
		if errors.Is(err, ErrWorkflowAuthorityForbidden) {
			return false, nil
		}
		return false, err
	}
	_, grant, err := workflowExceptionGrant(ctx, tx, issue, issue.WorkflowCandidateID, policyVersion, "acceptance")
	if err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT c.author_id::text,m.role FROM comment c
		JOIN member m ON m.workspace_id=c.workspace_id AND m.user_id=c.author_id
		JOIN agent_task_queue task ON task.id=$3 AND task.issue_id=c.issue_id AND task.agent_id=$4
		JOIN issue_workflow_candidate candidate ON candidate.id=$5 AND candidate.issue_id=c.issue_id
			AND candidate.workspace_id=c.workspace_id
		WHERE c.workspace_id=$1 AND c.issue_id=$2 AND c.author_type='member'
		AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
		AND c.created_at>candidate.created_at
		AND (task.trigger_comment_id=c.id OR c.id=ANY(task.coalesced_comment_ids))
		AND c.id=ANY(task.delivered_comment_ids)
		AND task.dispatched_at IS NOT NULL AND c.updated_at<=task.dispatched_at
		AND task.originator_user_id=c.author_id AND task.accountable_user_id=c.author_id
		AND btrim(c.content)<>'' AND c.content !~* '^\\s*/note(\\s|$)'`,
		issue.WorkspaceID, issue.ID, taskID, agentID, issue.WorkflowCandidateID)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var memberID, role string
		if err := rows.Scan(&memberID, &role); err != nil {
			rows.Close()
			return false, err
		}
		if workflowHumanAcceptanceAllowed(authority, role, memberID, grant) {
			rows.Close()
			return true, nil
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	return workflowForgejoCommentAvailable(ctx, tx, issue, actor, authority, policyVersion)
}

// A later, explicit human merge instruction upgrades only the already
// accepted exact candidate. The original ready decision remains in the ledger
// snapshot; the new source and executor are recorded as a second decision.
func (s WorkflowAuthorityService) upgradeWorkflowCommentMerge(ctx context.Context, tx pgx.Tx, issue db.Issue,
	acceptanceID pgtype.UUID, acceptedAt pgtype.Timestamptz, actor WorkflowActor,
	in WorkflowCommentAcceptanceInput) (WorkflowActor, error) {
	if issue.WorkflowFrozen || !acceptedAt.Valid || issue.WorkflowCandidateID != mustAuthorityUUID(in.CandidateID) ||
		issue.Revision != in.ExpectedRevision {
		return WorkflowActor{}, ErrWorkflowAuthorityConflict
	}
	pinned, authority, err := workflowAuthorityPolicy(ctx, s, issue)
	if err != nil {
		return WorkflowActor{}, err
	}
	candidate, err := loadCurrentWorkflowCandidate(ctx, tx, issue, pinned.Version)
	if err != nil {
		return WorkflowActor{}, err
	}
	if len(candidate.PRs) == 0 {
		return WorkflowActor{}, fmt.Errorf("%w: accepted candidate has no PR to merge", ErrWorkflowAuthorityConflict)
	}
	var acceptedPolicy, acceptedScope, previousAction string
	var originalWaiver, held bool
	var acceptedBy pgtype.UUID
	var previousUpgrade []byte
	err = tx.QueryRow(ctx, `SELECT policy_version,
		COALESCE(authority_snapshot->>'scope_digest',''),
		COALESCE(authority_snapshot->>'delivery_action',''),
		COALESCE((authority_snapshot->'comment_decision'->>'waive_review')::boolean,false),
		hold_delivery,actor_id,authority_snapshot->'merge_upgrade'
		FROM issue_workflow_acceptance WHERE id=$1 AND workspace_id=$2 AND issue_id=$3
		AND candidate_id=$4 AND state='accepted' AND revoked_at IS NULL`,
		acceptanceID, issue.WorkspaceID, issue.ID, candidate.ID).Scan(
		&acceptedPolicy, &acceptedScope, &previousAction, &originalWaiver, &held, &acceptedBy, &previousUpgrade)
	if err != nil || acceptedPolicy != pinned.Version || acceptedScope != candidate.ScopeDigest ||
		(previousAction != "ready" && previousAction != "merge") ||
		(previousAction == "merge" && (!held || !in.ReleaseHold)) || in.ReleaseHold && !held {
		return WorkflowActor{}, ErrWorkflowAuthorityConflict
	}
	if previousAction == "ready" {
		if stale, err := workflowCandidateStale(ctx, tx, issue, candidate.ID); err != nil {
			return WorkflowActor{}, err
		} else if stale {
			return WorkflowActor{}, fmt.Errorf("%w: candidate PR head changed", ErrWorkflowAuthorityConflict)
		}
	}
	proof, err := workflowCommentAuthority(ctx, tx, issue, actor, in)
	if err != nil {
		return WorkflowActor{}, err
	}
	createdAt, ok := workflowCommentSourceCreatedAt(proof.Snapshot)
	if !ok || !createdAt.After(acceptedAt.Time) {
		return WorkflowActor{}, fmt.Errorf("%w: merge requires a later human comment", ErrWorkflowAuthorityConflict)
	}
	if len(previousUpgrade) > 0 {
		var earlier workflowCommentMergeUpgrade
		if json.Unmarshal(previousUpgrade, &earlier) != nil {
			return WorkflowActor{}, ErrWorkflowAuthorityConflict
		}
		previousCreated, present := workflowCommentSourceCreatedAt(earlier.SourceSnapshot)
		if !present || !createdAt.After(previousCreated) {
			return WorkflowActor{}, fmt.Errorf("%w: hold release requires a new later human comment", ErrWorkflowAuthorityConflict)
		}
	}
	decisionActor := WorkflowActor{Type: "member", ID: util.UUIDToString(proof.MemberID)}
	role, _, err := workflowMemberRole(ctx, tx, issue.WorkspaceID, decisionActor)
	if err != nil {
		return WorkflowActor{}, err
	}
	_, acceptanceGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, pinned.Version, "acceptance")
	if err != nil {
		return WorkflowActor{}, err
	}
	if !workflowHumanAcceptanceAllowed(authority, role, decisionActor.ID, acceptanceGrant) ||
		in.WaiveReview && role != "owner" && role != "admin" {
		return WorkflowActor{}, ErrWorkflowAuthorityForbidden
	}
	if in.ReleaseHold && role != "owner" && role != "admin" && proof.MemberID != acceptedBy {
		return WorkflowActor{}, ErrWorkflowAuthorityForbidden
	}
	if previousAction == "merge" {
		if in.WaiveReview || len(in.MergeOrderPRURLs) > 0 || authority.FormatVersion != 2 {
			return WorkflowActor{}, ErrWorkflowAuthorityInput
		}
		eligible, err := WorkflowNonterminalStatus(ctx, tx, issue)
		if err != nil {
			return WorkflowActor{}, err
		}
		if !eligible {
			return WorkflowActor{}, ErrWorkflowAuthorityConflict
		}
		release := workflowCommentMergeUpgrade{Request: in, MemberID: decisionActor.ID,
			ExecutorAgentID: actor.ID, ExecutorTaskID: actor.SourceTaskID,
			SourceSnapshot: proof.Snapshot}
		releaseJSON, err := json.Marshal(release)
		if err != nil {
			return WorkflowActor{}, err
		}
		result, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET authority_snapshot=
			authority_snapshot||jsonb_build_object('hold_release',$2::jsonb),
			hold_delivery=false,released_at=now()
			WHERE id=$1 AND state='accepted' AND revoked_at IS NULL AND hold_delivery`, acceptanceID, releaseJSON)
		if err != nil {
			return WorkflowActor{}, err
		}
		if result.RowsAffected() != 1 {
			return WorkflowActor{}, ErrWorkflowAuthorityConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE issue_workflow_delivery SET next_attempt_at=now(),updated_at=now()
			WHERE acceptance_id=$1 AND action='merge' AND status IN ('pending','retry')
			AND readiness_done_at IS NOT NULL`, acceptanceID); err != nil {
			return WorkflowActor{}, err
		}
		details, _ := json.Marshal(map[string]any{"acceptance_id": util.UUIDToString(acceptanceID),
			"candidate_id": in.CandidateID, "comment_decision": release})
		if _, err := tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
			VALUES($1,$2,'member',$3,'workflow_delivery_hold_released',$4)`,
			issue.WorkspaceID, issue.ID, proof.MemberID, details); err != nil {
			return WorkflowActor{}, err
		}
		return decisionActor, nil
	}
	active, err := workflowHasMutableRuns(ctx, tx, issue.ID, proof.SourceTaskID)
	if err != nil {
		return WorkflowActor{}, err
	}
	pending, err := workflowHasPendingHandoff(ctx, tx, issue.ID)
	if err != nil {
		return WorkflowActor{}, err
	}
	if active || pending {
		return WorkflowActor{}, fmt.Errorf("%w: issue has active work or handoff", ErrWorkflowAuthorityConflict)
	}
	bindings, err := workflowDeliveryBindings(ctx, tx, issue, candidate.PRs)
	if err != nil {
		return WorkflowActor{}, err
	}
	_, reviewGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, pinned.Version, "review")
	if err != nil {
		return WorkflowActor{}, err
	}
	reviewRequired := authority.ReviewRequired && reviewGrant["waive"] != true && !originalWaiver && !in.WaiveReview
	reviewID, err := workflowReviewEvidence(ctx, s, tx, issue, candidate, bindings, reviewRequired, true, pgtype.UUID{})
	if err != nil {
		return WorkflowActor{}, err
	}
	_, deliveryGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, pinned.Version, "delivery")
	if err != nil {
		return WorkflowActor{}, err
	}
	planGrant := map[string]any{"action": "merge"}
	if override, ok := deliveryGrant["merge_method"].(string); ok {
		planGrant["merge_method"] = override
	}
	deliveryAuthority := authority
	deliveryAuthority.HumanDelivery = "merge"
	action, method, ordered, err := workflowDeliveryPlan(candidate.PRs, deliveryAuthority, "human", in.MergeOrderPRURLs, planGrant)
	if err != nil {
		return WorkflowActor{}, err
	}
	if action != "merge" {
		return WorkflowActor{}, ErrWorkflowAuthorityConflict
	}
	type deliveryRow struct {
		id, url string
	}
	rows, err := tx.Query(ctx, `SELECT id::text,pr_url,action,status,merged_at FROM issue_workflow_delivery
		WHERE workspace_id=$1 AND issue_id=$2 AND acceptance_id=$3 AND candidate_id=$4
		ORDER BY ordinal FOR UPDATE`, issue.WorkspaceID, issue.ID, acceptanceID, candidate.ID)
	if err != nil {
		return WorkflowActor{}, err
	}
	byURL := make(map[string]deliveryRow)
	for rows.Next() {
		var row deliveryRow
		var rowAction, rowStatus string
		var merged pgtype.Timestamptz
		if err := rows.Scan(&row.id, &row.url, &rowAction, &rowStatus, &merged); err != nil {
			rows.Close()
			return WorkflowActor{}, err
		}
		if rowAction != "ready" || merged.Valid ||
			rowStatus != "pending" && rowStatus != "retry" && rowStatus != "delivered" {
			rows.Close()
			return WorkflowActor{}, ErrWorkflowAuthorityConflict
		}
		byURL[row.url] = row
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return WorkflowActor{}, err
	}
	if len(byURL) != len(ordered) {
		return WorkflowActor{}, ErrWorkflowAuthorityConflict
	}
	// Ordinals are unique within an acceptance. Move them out of the
	// final range before applying an explicit new merge order.
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_delivery SET ordinal=ordinal+$2
		WHERE acceptance_id=$1 AND issue_id=$3`, acceptanceID, len(ordered), issue.ID); err != nil {
		return WorkflowActor{}, err
	}
	for ordinal, pr := range ordered {
		row, ok := byURL[pr.PRURL]
		if !ok {
			return WorkflowActor{}, ErrWorkflowAuthorityConflict
		}
		if _, err := tx.Exec(ctx, `UPDATE issue_workflow_delivery SET action='merge',merge_method=$2,
			ordinal=$3,status='pending',next_attempt_at=now(),last_error_class=NULL,updated_at=now()
			WHERE id=$1 AND acceptance_id=$4`, mustAuthorityUUID(row.id), method, ordinal, acceptanceID); err != nil {
			return WorkflowActor{}, err
		}
	}
	upgrade := workflowCommentMergeUpgrade{Request: in, MemberID: decisionActor.ID,
		ExecutorAgentID: actor.ID, ExecutorTaskID: actor.SourceTaskID,
		SourceSnapshot: proof.Snapshot, ReviewID: reviewID}
	upgradeJSON, err := json.Marshal(upgrade)
	if err != nil {
		return WorkflowActor{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance SET authority_snapshot=
		authority_snapshot||jsonb_build_object('delivery_action','merge','merge_method',$2::text,
		'merge_upgrade',$3::jsonb),
		hold_delivery=CASE WHEN $4 THEN false ELSE hold_delivery END,
		released_at=CASE WHEN $4 THEN now() ELSE released_at END
		WHERE id=$1 AND state='accepted' AND revoked_at IS NULL`,
		acceptanceID, method, upgradeJSON, in.ReleaseHold); err != nil {
		return WorkflowActor{}, err
	}
	details, _ := json.Marshal(map[string]any{"acceptance_id": util.UUIDToString(acceptanceID),
		"candidate_id": in.CandidateID, "comment_decision": upgrade})
	if _, err := tx.Exec(ctx, `INSERT INTO activity_log(workspace_id,issue_id,actor_type,actor_id,action,details)
		VALUES($1,$2,'member',$3,'workflow_delivery_merge_approved',$4)`,
		issue.WorkspaceID, issue.ID, proof.MemberID, details); err != nil {
		return WorkflowActor{}, err
	}
	return decisionActor, nil
}

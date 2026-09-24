package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/workflowdelivery"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

type workflowAcceptanceRequest struct {
	CandidateID          string   `json:"candidate_id"`
	ExpectedRevision     int64    `json:"expected_revision"`
	ClassificationReason string   `json:"classification_reason,omitempty"`
	MergeOrderPRURLs     []string `json:"merge_order_pr_urls"`
}

func normalizeWorkflowAcceptanceInput(in WorkflowAcceptanceInput) (workflowAcceptanceRequest, error) {
	if _, err := workflowAuthorityUUID(in.CandidateID); err != nil || in.ExpectedRevision < 1 {
		return workflowAcceptanceRequest{}, fmt.Errorf("%w: candidate_id and positive expected_revision are required", ErrWorkflowAuthorityInput)
	}
	in.ClassificationReason = strings.TrimSpace(in.ClassificationReason)
	if len(in.ClassificationReason) > 2000 {
		return workflowAcceptanceRequest{}, fmt.Errorf("%w: classification reason is too long", ErrWorkflowAuthorityInput)
	}
	if len(in.MergeOrderPRURLs) > 20 {
		return workflowAcceptanceRequest{}, fmt.Errorf("%w: too many merge order entries", ErrWorkflowAuthorityInput)
	}
	if in.MergeOrderPRURLs == nil {
		in.MergeOrderPRURLs = []string{}
	}
	return workflowAcceptanceRequest(in), nil
}

func validateAutonomousAcceptanceAuthority(allowed, assigned bool, classificationReason string) error {
	if !allowed || !assigned {
		return ErrWorkflowAuthorityForbidden
	}
	if classificationReason == "" {
		return fmt.Errorf("%w: classification_reason is required for autonomous acceptance", ErrWorkflowAuthorityInput)
	}
	return nil
}

func workflowAuthorityPolicy(ctx context.Context, s WorkflowAuthorityService, issue db.Issue) (*IssueWorkflowPolicy, WorkflowAuthorityPolicy, error) {
	pinned, err := s.Tasks.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || pinned == nil {
		return nil, WorkflowAuthorityPolicy{}, ErrWorkflowAuthorityUnavailable
	}
	authority, err := ParseWorkflowAuthorityPolicy(pinned.Bundle)
	if err != nil {
		return nil, WorkflowAuthorityPolicy{}, fmt.Errorf("%w: invalid pinned authority: %v", ErrWorkflowAuthorityUnavailable, err)
	}
	return pinned, authority, nil
}

func workflowMemberRole(ctx context.Context, tx pgx.Tx, workspaceID pgtype.UUID, actor WorkflowActor) (string, pgtype.UUID, error) {
	if actor.Type != "member" {
		return "", pgtype.UUID{}, ErrWorkflowAuthorityForbidden
	}
	actorID, err := workflowAuthorityUUID(actor.ID)
	if err != nil {
		return "", pgtype.UUID{}, ErrWorkflowAuthorityForbidden
	}
	var role string
	if err := tx.QueryRow(ctx, `SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2`, workspaceID, actorID).Scan(&role); err != nil {
		return "", pgtype.UUID{}, ErrWorkflowAuthorityForbidden
	}
	return role, actorID, nil
}

func workflowAgentTask(ctx context.Context, tx pgx.Tx, issue db.Issue, actor WorkflowActor) (pgtype.UUID, pgtype.UUID, error) {
	if actor.Type != "agent" {
		return pgtype.UUID{}, pgtype.UUID{}, ErrWorkflowAuthorityForbidden
	}
	agentID, err := workflowAuthorityUUID(actor.ID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, ErrWorkflowAuthorityForbidden
	}
	taskID, err := workflowAuthorityUUID(actor.SourceTaskID)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, ErrWorkflowAuthorityForbidden
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM agent_task_queue WHERE id=$1 AND issue_id=$2 AND agent_id=$3`, taskID, issue.ID, agentID).Scan(&status); err != nil || status != "running" {
		return pgtype.UUID{}, pgtype.UUID{}, ErrWorkflowAuthorityForbidden
	}
	return agentID, taskID, nil
}

func workflowExceptionGrant(ctx context.Context, tx pgx.Tx, issue db.Issue, candidateID pgtype.UUID, version, scope string) (pgtype.UUID, map[string]any, error) {
	var id pgtype.UUID
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT id,grant_details FROM issue_workflow_exception
		WHERE workspace_id=$1 AND issue_id=$2 AND candidate_id=$3 AND base_policy_version=$4
		AND scope=$5 AND revoked_at IS NULL ORDER BY created_at DESC,id DESC LIMIT 1`,
		issue.WorkspaceID, issue.ID, candidateID, version, scope).Scan(&id, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, nil, nil
	}
	if err != nil {
		return pgtype.UUID{}, nil, err
	}
	var grant map[string]any
	if err := json.Unmarshal(raw, &grant); err != nil {
		return pgtype.UUID{}, nil, err
	}
	return id, grant, nil
}

func workflowDeliveryPlan(prs []HandoffCandidate, policy WorkflowAuthorityPolicy, mode string, order []string, grant map[string]any) (string, string, []HandoffCandidate, error) {
	action := policy.HumanDelivery
	if mode == "trivial" {
		action = policy.AutonomousDelivery
	}
	method := policy.MergeMethod
	if grant != nil {
		if override, ok := grant["action"].(string); ok {
			action = override
		}
		if override, ok := grant["merge_method"].(string); ok {
			method = override
		}
	}
	if action != "ready" && action != "merge" || action == "merge" && method != "merge" && method != "squash" && method != "rebase" {
		return "", "", nil, ErrWorkflowAuthorityConflict
	}
	ordered := append([]HandoffCandidate{}, prs...)
	if action == "ready" {
		if len(order) != 0 {
			return "", "", nil, fmt.Errorf("%w: ready-only delivery cannot specify merge order", ErrWorkflowAuthorityInput)
		}
		return action, "", ordered, nil
	}
	multiOrderGranted := policy.MultiPRMergeOrder == "explicit" || grant["action"] == "merge"
	if len(prs) > 1 && !multiOrderGranted {
		return "", "", nil, fmt.Errorf("%w: multi-PR merge is not granted", ErrWorkflowAuthorityConflict)
	}
	if len(prs) > 1 || len(order) > 0 {
		if len(order) != len(prs) {
			return "", "", nil, fmt.Errorf("%w: complete explicit merge order is required", ErrWorkflowAuthorityInput)
		}
		byURL := make(map[string]HandoffCandidate, len(prs))
		for _, pr := range prs {
			byURL[pr.PRURL] = pr
		}
		for i, raw := range order {
			pr, ok := byURL[raw]
			if !ok {
				return "", "", nil, fmt.Errorf("%w: merge order differs from candidate PR set", ErrWorkflowAuthorityInput)
			}
			ordered[i] = pr
			delete(byURL, raw)
		}
	}
	return action, method, ordered, nil
}

func workflowReviewEvidence(ctx context.Context, s WorkflowAuthorityService, tx pgx.Tx, issue db.Issue, candidate workflowCandidateRecord, bindings []workflowDeliveryBinding, required, verifyProvider bool, pendingReviewerTaskID pgtype.UUID) (string, error) {
	if !required {
		return "", nil
	}
	reviewID, err := workflowReviewSatisfiedForRequest(ctx, tx, issue, candidate, true, pendingReviewerTaskID)
	if err != nil {
		return "", err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT pr_review_urls FROM issue_workflow_review WHERE id=$1 AND candidate_id=$2`,
		mustAuthorityUUID(reviewID), candidate.ID).Scan(&raw); err != nil {
		return "", err
	}
	var urls []string
	if err := json.Unmarshal(raw, &urls); err != nil {
		return "", err
	}
	if len(candidate.PRs) == 0 {
		return reviewID, nil
	}
	if len(urls) == 0 {
		return "", fmt.Errorf("%w: PR review links are required", ErrWorkflowAuthorityConflict)
	}
	if !verifyProvider {
		return reviewID, nil
	}
	if s.ReviewVerifier == nil {
		return "", fmt.Errorf("%w: PR review verification is not configured", ErrWorkflowAuthorityUnavailable)
	}
	input := WorkflowReviewEvidenceInput{WorkspaceID: issue.WorkspaceID, ReviewURLs: urls}
	for _, b := range bindings {
		input.PRs = append(input.PRs, WorkflowReviewPR{
			Provider: b.Provider, BindingID: b.BindingID, RepositoryURL: b.PR.RepositoryURL,
			PRURL: b.PR.PRURL, Owner: b.Owner, Repo: b.Repo, Number: b.Number, ExpectedHeadSHA: b.PR.CommitSHA,
		})
	}
	if err := s.ReviewVerifier(ctx, input); err != nil {
		if errors.Is(err, workflowdelivery.ErrTransient) || errors.Is(err, workflowdelivery.ErrAmbiguous) ||
			errors.Is(err, workflowdelivery.ErrUnauthorized) {
			return "", fmt.Errorf("%w: provider review verification unavailable: %v", ErrWorkflowAuthorityUnavailable, err)
		}
		return "", fmt.Errorf("%w: PR review evidence could not be verified: %v", ErrWorkflowAuthorityConflict, err)
	}
	return reviewID, nil
}

func mustAuthorityUUID(raw string) pgtype.UUID {
	id, _ := workflowAuthorityUUID(raw)
	return id
}

// AcceptWorkflow either records a human decision immediately or records an
// autonomous request that only a later successful source task may finalize.
// The issue lock serializes it with handoff, rejection and delivery.
func (s WorkflowAuthorityService) AcceptWorkflow(ctx context.Context, workspaceID, issueID pgtype.UUID, actor WorkflowActor, in WorkflowAcceptanceInput) (string, error) {
	if s.Tasks == nil || s.Tasks.TxStarter == nil {
		return "", ErrWorkflowAuthorityUnavailable
	}
	request, err := normalizeWorkflowAcceptanceInput(in)
	if err != nil {
		return "", err
	}
	candidateID, _ := workflowAuthorityUUID(request.CandidateID)
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
	mode := "human"
	if actor.Type == "agent" {
		mode = "trivial"
	}
	// A repeated response after an ambiguous network failure returns the exact
	// existing request, including when the issue revision already advanced.
	var existingID pgtype.UUID
	var existingActor, existingMode, existingState string
	var existingSource pgtype.UUID
	var existingRequest []byte
	err = tx.QueryRow(ctx, `SELECT id,actor_id::text,mode,state,source_task_id,authority_snapshot->'request'
		FROM issue_workflow_acceptance WHERE issue_id=$1 AND candidate_id=$2 AND revoked_at IS NULL
		AND state IN ('requested','accepted') ORDER BY requested_at DESC,id DESC LIMIT 1`, issue.ID, candidateID).Scan(
		&existingID, &existingActor, &existingMode, &existingState, &existingSource, &existingRequest)
	if err == nil {
		var prior workflowAcceptanceRequest
		if json.Unmarshal(existingRequest, &prior) == nil &&
			existingActor == actor.ID && existingMode == mode && reflect.DeepEqual(prior, request) &&
			(mode != "trivial" || existingSource == mustAuthorityUUID(actor.SourceTaskID)) {
			return existingState, tx.Commit(ctx)
		}
		return "", fmt.Errorf("%w: candidate has a different acceptance request", ErrWorkflowAuthorityConflict)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID || issue.Revision != request.ExpectedRevision || issue.Status != "in_review" {
		return "", ErrWorkflowAuthorityConflict
	}
	pinned, authority, err := workflowAuthorityPolicy(ctx, s, issue)
	if err != nil {
		return "", err
	}
	candidate, err := loadCurrentWorkflowCandidate(ctx, tx, issue, pinned.Version)
	if err != nil {
		return "", err
	}
	actorID, err := workflowAuthorityUUID(actor.ID)
	if err != nil {
		return "", ErrWorkflowAuthorityForbidden
	}
	var sourceTaskID pgtype.UUID
	acceptanceExceptionID, acceptanceGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, pinned.Version, "acceptance")
	if err != nil {
		return "", err
	}
	if mode == "human" {
		role, _, err := workflowMemberRole(ctx, tx, workspaceID, actor)
		if err != nil {
			return "", err
		}
		allowed := false
		for _, candidateRole := range authority.HumanAcceptRoles {
			allowed = allowed || role == candidateRole
		}
		allowed = allowed || acceptanceGrant["human_actor_id"] == actor.ID
		if !allowed || issue.AssigneeType.String != "member" ||
			(issue.AssigneeID != actorID && acceptanceGrant["human_actor_id"] != actor.ID) || request.ClassificationReason != "" {
			return "", ErrWorkflowAuthorityForbidden
		}
	} else {
		agentID, taskID, err := workflowAgentTask(ctx, tx, issue, actor)
		if err != nil {
			return "", err
		}
		var taskPolicy pgtype.Text
		var taskProfile pgtype.UUID
		if err := tx.QueryRow(ctx, `SELECT workflow_policy_version,workflow_profile_id
			FROM agent_task_queue WHERE id=$1 AND issue_id=$2 AND agent_id=$3`,
			taskID, issue.ID, agentID).Scan(&taskPolicy, &taskProfile); err != nil ||
			!taskPolicy.Valid || taskPolicy.String != pinned.Version || !taskProfile.Valid {
			return "", ErrWorkflowAuthorityForbidden
		}
		allowed := false
		for _, id := range authority.AutonomousAgentIDs {
			allowed = allowed || id == actor.ID
		}
		allowed = authority.AutonomousEnabled && allowed || acceptanceGrant["agent_actor_id"] == actor.ID
		if err := validateAutonomousAcceptanceAuthority(allowed,
			issue.AssigneeType.String == "agent" && issue.AssigneeID == agentID,
			request.ClassificationReason); err != nil {
			return "", err
		}
		sourceTaskID = taskID
	}
	active, err := workflowHasMutableRuns(ctx, tx, issue.ID, sourceTaskID)
	if err != nil {
		return "", err
	}
	pending, err := workflowHasPendingHandoff(ctx, tx, issue.ID)
	if err != nil {
		return "", err
	}
	if active || pending {
		return "", fmt.Errorf("%w: issue has active work or handoff", ErrWorkflowAuthorityConflict)
	}
	bindings, err := workflowDeliveryBindings(ctx, tx, issue, candidate.PRs)
	if err != nil {
		return "", err
	}
	reviewExceptionID, reviewGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, pinned.Version, "review")
	if err != nil {
		return "", err
	}
	reviewRequired := authority.ReviewRequired && reviewGrant["waive"] != true
	reviewID, err := workflowReviewEvidence(ctx, s, tx, issue, candidate, bindings, reviewRequired, true, sourceTaskID)
	if err != nil {
		return "", err
	}
	deliveryExceptionID, deliveryGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, pinned.Version, "delivery")
	if err != nil {
		return "", err
	}
	action, method, ordered, err := workflowDeliveryPlan(candidate.PRs, authority, mode, request.MergeOrderPRURLs, deliveryGrant)
	if err != nil {
		return "", err
	}
	authorityJSON, _ := json.Marshal(map[string]any{"request": request, "review_id": reviewID,
		"acceptance_exception_id": util.UUIDToString(acceptanceExceptionID),
		"review_exception_id":     util.UUIDToString(reviewExceptionID), "delivery_action": action,
		"delivery_exception_id": util.UUIDToString(deliveryExceptionID),
		"merge_method":          method, "policy_version": pinned.Version, "scope_digest": candidate.ScopeDigest})
	acceptanceID := dbid.NewV7()
	state := "requested"
	var acceptedRevision any
	if mode == "human" {
		state = "accepted"
		acceptedRevision = issue.Revision + 1
	}
	_, err = tx.Exec(ctx, `INSERT INTO issue_workflow_acceptance
		(id,workspace_id,issue_id,candidate_id,mode,actor_type,actor_id,source_task_id,state,issue_revision,
		policy_version,authority_snapshot,classification_reason,accepted_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,
		CASE WHEN $9='accepted' THEN now() ELSE NULL END)`, acceptanceID, workspaceID, issueID, candidateID,
		mode, actor.Type, actorID, sourceTaskID, state, acceptedRevision, pinned.Version, authorityJSON,
		pgtype.Text{String: request.ClassificationReason, Valid: request.ClassificationReason != ""})
	if err != nil {
		return "", err
	}
	if mode == "human" {
		if err := finalizeWorkflowAcceptance(ctx, tx, q, issue, acceptanceID, actor, ordered, bindings, action, method); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	s.PublishWorkflowIssueChange(ctx, issue, actor)
	return state, nil
}

func finalizeWorkflowAcceptance(ctx context.Context, tx pgx.Tx, q *db.Queries, issue db.Issue,
	acceptanceID pgtype.UUID, actor WorkflowActor, ordered []HandoffCandidate, bindings []workflowDeliveryBinding,
	action, method string,
) error {
	// The issue row lock also fences native enqueue paths. Retire unstarted
	// plans before the done transition; started runs are rejected by caller.
	if _, err := tx.Exec(ctx, `UPDATE agent_task_queue SET status='cancelled',completed_at=now(),
		error='Issue accepted; queued work retired',prepare_lease_expires_at=NULL,
		cancelled_by_type='system' WHERE issue_id=$1 AND status IN ('queued','deferred')
		AND started_at IS NULL`, issue.ID); err != nil {
		return err
	}
	updated, err := q.UpdateIssue(ctx, db.UpdateIssueParams{
		ID: issue.ID, ExpectedRevision: pgtype.Int8{Int64: issue.Revision, Valid: true},
		Title: pgtype.Text{String: issue.Title, Valid: true}, Description: issue.Description,
		Status: pgtype.Text{String: "done", Valid: true}, Priority: pgtype.Text{String: issue.Priority, Valid: true},
		AssigneeType: issue.AssigneeType, AssigneeID: issue.AssigneeID,
		StartDate: issue.StartDate, DueDate: issue.DueDate, ParentIssueID: issue.ParentIssueID,
		ProjectID: issue.ProjectID, Stage: issue.Stage,
	})
	if err != nil || updated.Revision != issue.Revision+1 {
		return fmt.Errorf("%w: issue completion failed: %v", ErrWorkflowAuthorityConflict, err)
	}
	if err := q.DisableIssueWakeups(ctx, issue.ID); err != nil {
		return err
	}
	byURL := make(map[string]workflowDeliveryBinding, len(bindings))
	for _, b := range bindings {
		byURL[b.PR.PRURL] = b
	}
	for ordinal, pr := range ordered {
		b, ok := byURL[pr.PRURL]
		if !ok {
			return fmt.Errorf("%w: provider binding changed", ErrWorkflowAuthorityConflict)
		}
		var mergeMethod any
		if action == "merge" {
			mergeMethod = method
		}
		_, err := tx.Exec(ctx, `INSERT INTO issue_workflow_delivery
			(id,workspace_id,issue_id,acceptance_id,candidate_id,ordinal,provider,provider_binding_id,
			repository_url,pr_url,repo_owner,repo_name,pr_number,expected_head_sha,action,merge_method)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
			dbid.NewV7(), issue.WorkspaceID, issue.ID, acceptanceID, issue.WorkflowCandidateID, ordinal,
			b.Provider, b.BindingID, pr.RepositoryURL, pr.PRURL, b.Owner, b.Repo, b.Number, pr.CommitSHA, action, mergeMethod)
		if err != nil {
			return err
		}
	}
	actorID, _ := workflowAuthorityUUID(actor.ID)
	activityDetails, _ := json.Marshal(map[string]string{"candidate_id": util.UUIDToString(issue.WorkflowCandidateID),
		"acceptance_id": util.UUIDToString(acceptanceID), "from": issue.Status, "to": "done"})
	_, err = q.CreateActivity(ctx, db.CreateActivityParams{ID: dbid.NewV7(), WorkspaceID: issue.WorkspaceID,
		IssueID: issue.ID, ActorType: pgtype.Text{String: actor.Type, Valid: true}, ActorID: actorID,
		Action: "status_changed", Details: activityDetails})
	return err
}

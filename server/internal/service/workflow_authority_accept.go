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
	AcceptanceMode       string   `json:"acceptance_mode,omitempty"`
	ClassificationReason string   `json:"classification_reason,omitempty"`
	MergeOrderPRURLs     []string `json:"merge_order_pr_urls"`
	OutcomeComplete      *bool    `json:"outcome_complete,omitempty"`
	HoldDelivery         bool     `json:"hold_delivery,omitempty"`
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
	request := workflowAcceptanceRequest{
		CandidateID:          in.CandidateID,
		ExpectedRevision:     in.ExpectedRevision,
		ClassificationReason: in.ClassificationReason,
		MergeOrderPRURLs:     in.MergeOrderPRURLs,
		OutcomeComplete:      in.OutcomeComplete,
		HoldDelivery:         in.HoldDelivery,
	}
	request.AcceptanceMode = strings.TrimSpace(in.AcceptanceMode)
	if request.AcceptanceMode != "" && request.AcceptanceMode != "trivial" && request.AcceptanceMode != "reviewed" {
		return workflowAcceptanceRequest{}, fmt.Errorf("%w: acceptance_mode must be trivial or reviewed", ErrWorkflowAuthorityInput)
	}
	return request, nil
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

func workflowAutonomousAcceptanceAllowed(authority WorkflowAuthorityPolicy, mode, actorID string, grant map[string]any) bool {
	if grant != nil && grant["agent_actor_id"] == actorID {
		return true
	}
	var enabled bool
	var acceptors []string
	switch mode {
	case "trivial":
		enabled, acceptors = authority.AutonomousEnabled, authority.AutonomousAgentIDs
	case "reviewed":
		enabled, acceptors = authority.AutonomousReviewedEnabled, authority.AutonomousReviewedAgentIDs
	default:
		return false
	}
	if !enabled {
		return false
	}
	for _, id := range acceptors {
		if id == actorID {
			return true
		}
	}
	return false
}

func workflowHumanAcceptanceAllowed(authority WorkflowAuthorityPolicy, role, actorID string, grant map[string]any) bool {
	if grant["human_actor_id"] == actorID {
		return true
	}
	for _, acceptedRole := range authority.HumanAcceptRoles {
		if role == acceptedRole {
			return true
		}
	}
	return false
}

// WorkflowNonterminalStatus resolves eligibility for active workflow authority
// from the workspace's lifecycle category, rather than a presentation key.
func WorkflowNonterminalStatus(ctx context.Context, tx pgx.Tx, issue db.Issue) (bool, error) {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_status
		WHERE workspace_id=$1 AND key=$2 AND category IN ('unstarted','started'))`,
		issue.WorkspaceID, issue.Status).Scan(&allowed)
	return allowed, err
}

func workflowSelectedProfileID(ctx context.Context, tx pgx.Tx, issue db.Issue, agentID pgtype.UUID, policyVersion string) (pgtype.UUID, error) {
	var profileID pgtype.UUID
	err := tx.QueryRow(ctx, `SELECT (SELECT id FROM issue_workflow_profile
		WHERE workspace_id=$1 AND issue_id=$2 AND agent_id=$3 AND policy_version=$4
		ORDER BY revision DESC LIMIT 1)`, issue.WorkspaceID, issue.ID, agentID, policyVersion).Scan(&profileID)
	return profileID, err
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
	} else if mode == "reviewed" {
		action = policy.AutonomousReviewedDelivery
	} else if mode != "human" {
		return "", "", nil, ErrWorkflowAuthorityConflict
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
			Provider: b.Provider, BindingID: b.BindingID, RepositoryURL: b.ProviderRepositoryURL,
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
		mode = request.AcceptanceMode
		if mode == "" {
			mode = "trivial"
		}
		request.AcceptanceMode = mode
	} else if request.AcceptanceMode != "" {
		return "", fmt.Errorf("%w: acceptance_mode is only valid for agent acceptance", ErrWorkflowAuthorityInput)
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
		priorOK := json.Unmarshal(existingRequest, &prior) == nil
		if priorOK {
			if prior.AcceptanceMode == "" && existingMode == "trivial" {
				// Requests written before the distinct reviewed route used an
				// omitted mode for autonomous trivial acceptance.
				prior.AcceptanceMode = "trivial"
			}
		}
		if priorOK &&
			existingActor == actor.ID && existingMode == mode && reflect.DeepEqual(prior, request) &&
			(mode == "human" || existingSource == mustAuthorityUUID(actor.SourceTaskID)) {
			return existingState, tx.Commit(ctx)
		}
		return "", fmt.Errorf("%w: candidate has a different acceptance request", ErrWorkflowAuthorityConflict)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if issue.WorkflowFrozen || issue.WorkflowCandidateID != candidateID || issue.Revision != request.ExpectedRevision {
		return "", ErrWorkflowAuthorityConflict
	}
	allowedStatus, err := WorkflowNonterminalStatus(ctx, tx, issue)
	if err != nil {
		return "", err
	}
	if !allowedStatus {
		return "", fmt.Errorf("%w: terminal issue cannot receive new acceptance", ErrWorkflowAuthorityConflict)
	}
	pinned, authority, err := workflowAuthorityPolicy(ctx, s, issue)
	if err != nil {
		return "", err
	}
	if authority.FormatVersion == 2 {
		if err := ValidateWorkflowCompletionConfig(ctx, tx, issue, authority); err != nil {
			return "", err
		}
	} else if request.OutcomeComplete != nil || request.HoldDelivery {
		return "", fmt.Errorf("%w: completion controls require format 2", ErrWorkflowAuthorityInput)
	}
	candidate, err := loadCurrentWorkflowCandidate(ctx, tx, issue, pinned.Version)
	if err != nil {
		return "", err
	}
	if stale, err := workflowCandidateStale(ctx, tx, issue, candidate.ID); err != nil {
		return "", err
	} else if stale {
		return "", fmt.Errorf("%w: candidate PR head changed; reject and evaluate a new candidate", ErrWorkflowAuthorityConflict)
	}
	actorID, err := workflowAuthorityUUID(actor.ID)
	if err != nil {
		return "", ErrWorkflowAuthorityForbidden
	}
	var sourceTaskID pgtype.UUID
	var sourceTaskPolicy pgtype.Text
	var sourceTaskProfile, selectedProfile pgtype.UUID
	acceptanceExceptionID, acceptanceGrant, err := workflowExceptionGrant(ctx, tx, issue, candidate.ID, pinned.Version, "acceptance")
	if err != nil {
		return "", err
	}
	if mode == "human" {
		role, _, err := workflowMemberRole(ctx, tx, workspaceID, actor)
		if err != nil {
			return "", err
		}
		if !workflowHumanAcceptanceAllowed(authority, role, actor.ID, acceptanceGrant) || request.ClassificationReason != "" {
			return "", ErrWorkflowAuthorityForbidden
		}
	} else {
		agentID, taskID, err := workflowAgentTask(ctx, tx, issue, actor)
		if err != nil {
			return "", err
		}
		if err := tx.QueryRow(ctx, `SELECT workflow_policy_version,workflow_profile_id
			FROM agent_task_queue WHERE id=$1 AND issue_id=$2 AND agent_id=$3`,
			taskID, issue.ID, agentID).Scan(&sourceTaskPolicy, &sourceTaskProfile); err != nil ||
			!sourceTaskPolicy.Valid || sourceTaskPolicy.String != pinned.Version || !sourceTaskProfile.Valid {
			return "", ErrWorkflowAuthorityForbidden
		}
		selectedProfile, err = workflowSelectedProfileID(ctx, tx, issue, agentID, pinned.Version)
		if err != nil {
			return "", err
		}
		allowed := workflowAutonomousAcceptanceAllowed(authority, mode, actor.ID, acceptanceGrant)
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
	// The reviewed route is explicitly for nontrivial work that already has an
	// independent final review. A review exception must not turn it into an
	// unreviewed autonomous acceptance.
	if mode == "reviewed" {
		reviewRequired = true
	}
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
	authoritySnapshot := map[string]any{"request": request, "review_id": reviewID,
		"acceptance_exception_id": util.UUIDToString(acceptanceExceptionID),
		"review_exception_id":     util.UUIDToString(reviewExceptionID), "delivery_action": action,
		"delivery_exception_id": util.UUIDToString(deliveryExceptionID),
		"merge_method":          method, "policy_version": pinned.Version, "scope_digest": candidate.ScopeDigest}
	if mode == "trivial" || mode == "reviewed" {
		authoritySnapshot["source_task_workflow_policy_version"] = sourceTaskPolicy.String
		authoritySnapshot["source_task_workflow_profile_id"] = util.UUIDToString(sourceTaskProfile)
		// Retained tasks can use an older immutable profile. Capture the
		// current selection independently so deliberate reselection requires
		// reevaluation without comparing global agent defaults.
		var selectedID any
		if selectedProfile.Valid {
			selectedID = util.UUIDToString(selectedProfile)
		}
		authoritySnapshot["selected_workflow_profile_id"] = selectedID
	}
	authorityJSON, _ := json.Marshal(authoritySnapshot)
	acceptanceID := dbid.NewV7()
	state := "requested"
	var acceptedRevision any
	if mode == "human" {
		state = "accepted"
		acceptedRevision = issue.Revision + 1
	}
	completionVersion := authority.FormatVersion
	var acceptedStatus, outcomeAgent any
	if completionVersion == 2 {
		acceptedStatus = authority.AcceptedStatusKey
		if authority.OutcomeAgentID != "" {
			outcomeAgent = mustAuthorityUUID(authority.OutcomeAgentID)
		}
	}
	outcomeComplete := len(ordered) == 0 || request.OutcomeComplete != nil && *request.OutcomeComplete
	_, err = tx.Exec(ctx, `INSERT INTO issue_workflow_acceptance
		(id,workspace_id,issue_id,candidate_id,mode,actor_type,actor_id,source_task_id,state,issue_revision,
		policy_version,authority_snapshot,classification_reason,accepted_at,completion_version,accepted_status_key,
		outcome_agent_id,hold_delivery,held_at,outcome_complete,outcome_completed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,
		CASE WHEN $9='accepted' THEN now() ELSE NULL END,$14,$15,$16,$17,
		CASE WHEN $17 THEN now() ELSE NULL END,$18,CASE WHEN $9='accepted' AND $18 THEN now() ELSE NULL END)`, acceptanceID, workspaceID, issueID, candidateID,
		mode, actor.Type, actorID, sourceTaskID, state, acceptedRevision, pinned.Version, authorityJSON,
		pgtype.Text{String: request.ClassificationReason, Valid: request.ClassificationReason != ""},
		completionVersion, acceptedStatus, outcomeAgent, request.HoldDelivery, outcomeComplete)
	if err != nil {
		return "", err
	}
	var outcomeTask *db.AgentTaskQueue
	if mode == "human" {
		outcomeTask, err = finalizeWorkflowAcceptance(ctx, tx, q, issue, acceptanceID, actor, ordered, bindings, action, method, authority, request)
		if err != nil {
			return "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if outcomeTask != nil {
		s.Tasks.NotifyWorkflowCompletionTask(ctx, issue.WorkspaceID, outcomeTask)
	}
	s.PublishWorkflowIssueChange(ctx, issue, actor)
	return state, nil
}

func finalizeWorkflowAcceptance(ctx context.Context, tx pgx.Tx, q *db.Queries, issue db.Issue,
	acceptanceID pgtype.UUID, actor WorkflowActor, ordered []HandoffCandidate, bindings []workflowDeliveryBinding,
	action, method string, authority WorkflowAuthorityPolicy, request workflowAcceptanceRequest,
) (*db.AgentTaskQueue, error) {
	targetStatus := "done"
	if authority.FormatVersion == 2 && len(ordered) > 0 {
		targetStatus = authority.AcceptedStatusKey
	}
	// A human may approve while a permission-checked ordinary question is
	// already queued for the assigned agent. Preserve that exact input as
	// conversation authority, independent of the acceptance decision evidence.
	if _, err := tx.Exec(ctx, `UPDATE issue_workflow_acceptance a SET human_comment_obligations=
		(SELECT COALESCE(jsonb_agg(DISTINCT entry),'[]'::jsonb) FROM (
		  SELECT entry FROM jsonb_array_elements(a.human_comment_obligations) entry
		  UNION ALL SELECT jsonb_build_object('comment_id',c.id::text,'agent_id',conversation.agent_id::text)
		  FROM agent_task_queue conversation JOIN comment c
		    ON (c.id=conversation.trigger_comment_id OR c.id=ANY(conversation.coalesced_comment_ids))
		  JOIN member m ON m.workspace_id=$2 AND m.user_id=c.author_id
		  WHERE conversation.issue_id=$1 AND conversation.agent_id=$3
		  AND conversation.status IN ('queued','deferred') AND conversation.started_at IS NULL
		  AND c.issue_id=$1 AND c.workspace_id=$2 AND c.author_type='member'
		  AND c.type IN ('comment','progress_update') AND c.deleted_at IS NULL
		  AND c.created_at>(SELECT created_at FROM issue_workflow_candidate WHERE id=$5)
		  AND btrim(c.content)<>'' AND c.content !~* '^\s*/note(\s|$)'
		  AND conversation.originator_user_id=c.author_id AND conversation.accountable_user_id=c.author_id
		) input) WHERE a.id=$4 AND a.issue_id=$1 AND $6`, issue.ID, issue.WorkspaceID,
		issue.AssigneeID, acceptanceID, issue.WorkflowCandidateID, issue.AssigneeType.String == "agent"); err != nil {
		return nil, err
	}
	// Previously queued ordinary conversations gain server evidence when their
	// exact input is preserved by this approval. It restricts stale reruns even
	// after explicit withdrawal removes mutable recipient bookkeeping.
	if _, err := tx.Exec(ctx, `UPDATE agent_task_queue conversation SET context=COALESCE(context,'{}'::jsonb)
		||jsonb_build_object('workflow_comment_obligation',workflow_comment_obligation_context(issue_id,agent_id,trigger_comment_id))
		WHERE issue_id=$1 AND status IN ('queued','deferred') AND trigger_comment_id IS NOT NULL
		AND workflow_comment_obligation_context(issue_id,agent_id,trigger_comment_id) IS NOT NULL`, issue.ID); err != nil {
		return nil, err
	}

	// Retire unstarted work, preserving only exact already-promised human
	// conversations. Acceptance does not cancel an answer; those tasks retain
	// conversation authority while completion/rework guards remain separate.
	if _, err := tx.Exec(ctx, `UPDATE agent_task_queue SET status='cancelled',completed_at=now(),
		error='Issue accepted; queued work retired',prepare_lease_expires_at=NULL,
		cancelled_by_type='system' WHERE issue_id=$1 AND status IN ('queued','deferred')
		AND started_at IS NULL AND NOT workflow_human_comment_task_current(id,issue_id)
		AND NOT workflow_accepted_comment_task_current(id,issue_id)`, issue.ID); err != nil {
		return nil, err
	}
	updated, err := q.UpdateIssue(ctx, db.UpdateIssueParams{
		ID: issue.ID, ExpectedRevision: pgtype.Int8{Int64: issue.Revision, Valid: true},
		Title: pgtype.Text{String: issue.Title, Valid: true}, Description: issue.Description,
		Status: pgtype.Text{String: targetStatus, Valid: true}, Priority: pgtype.Text{String: issue.Priority, Valid: true},
		AssigneeType: issue.AssigneeType, AssigneeID: issue.AssigneeID,
		StartDate: issue.StartDate, DueDate: issue.DueDate, ParentIssueID: issue.ParentIssueID,
		ProjectID: issue.ProjectID, Stage: issue.Stage,
	})
	if err != nil || updated.Revision != issue.Revision+1 {
		return nil, fmt.Errorf("%w: issue completion failed: %v", ErrWorkflowAuthorityConflict, err)
	}
	byURL := make(map[string]workflowDeliveryBinding, len(bindings))
	for _, b := range bindings {
		byURL[b.PR.PRURL] = b
	}
	for ordinal, pr := range ordered {
		b, ok := byURL[pr.PRURL]
		if !ok {
			return nil, fmt.Errorf("%w: provider binding changed", ErrWorkflowAuthorityConflict)
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
			b.Provider, b.BindingID, b.ProviderRepositoryURL, pr.PRURL, b.Owner, b.Repo, b.Number, pr.CommitSHA, action, mergeMethod)
		if err != nil {
			return nil, err
		}
	}
	// A completed member handoff cannot dispatch again. Keep only that exact
	// current conversation evidence while delivery or outcome remains pending;
	// ordinary and incomplete wakeups are still retired by acceptance.
	if _, err := tx.Exec(ctx, `UPDATE issue_wakeup SET enabled=false,disabled_at=clock_timestamp(),updated_at=clock_timestamp()
		WHERE issue_id=$1 AND disabled_at IS NULL
		AND NOT (workflow_human_comment_handoff_current($1,id) AND
		  ($2 OR EXISTS(SELECT 1 FROM agent_task_queue conversation WHERE conversation.issue_id=$1
		    AND conversation.status IN ('queued','deferred','dispatched','running','waiting_local_directory')
		    AND conversation.trigger_evidence_ref_id=issue_wakeup.id
		    AND workflow_human_comment_task_current(conversation.id,$1))))`,
		issue.ID, targetStatus != "done"); err != nil {
		return nil, err
	}
	var outcomeTask *db.AgentTaskQueue
	if authority.FormatVersion == 2 && len(ordered) == 0 && targetStatus == authority.AcceptedStatusKey {
		outcomeTask, _, _, err = TryReconcileWorkflowCompletion(ctx, tx, q, updated, acceptanceID)
		if err != nil {
			return nil, err
		}
	}
	actorID, _ := workflowAuthorityUUID(actor.ID)
	activityDetails, _ := json.Marshal(map[string]string{"candidate_id": util.UUIDToString(issue.WorkflowCandidateID),
		"acceptance_id": util.UUIDToString(acceptanceID), "from": issue.Status, "to": targetStatus})
	_, err = q.CreateActivity(ctx, db.CreateActivityParams{ID: dbid.NewV7(), WorkspaceID: issue.WorkspaceID,
		IssueID: issue.ID, ActorType: pgtype.Text{String: actor.Type, Valid: true}, ActorID: actorID,
		Action: "status_changed", Details: activityDetails})
	return outcomeTask, err
}

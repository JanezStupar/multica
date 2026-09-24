package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// bindClaimIssueWorkflowProfile captures the behavioral configuration of the
// selected agent on its first enrolled-issue claim. A later claim reuses the
// saved bytes, while live runtime access and credentials remain independently
// checked by the ordinary claim path.
func (h *Handler) bindClaimIssueWorkflowProfile(ctx context.Context, task db.AgentTaskQueue, runtime db.AgentRuntime) (*service.IssueWorkflowProfile, pgtype.UUID, error) {
	returnProfile, profileID, err := h.TaskService.BindIssueWorkflowProfile(ctx, runtime.WorkspaceID, task,
		func(qtx *db.Queries, policy *service.IssueWorkflowPolicy) (service.IssueWorkflowProfile, error) {
			return h.captureIssueWorkflowProfile(ctx, qtx, policy, task.AgentID, runtime.WorkspaceID, task.RuntimeID)
		})
	return returnProfile, profileID, err
}

func (h *Handler) captureIssueWorkflowProfile(ctx context.Context, qtx *db.Queries, policy *service.IssueWorkflowPolicy,
	agentID, workspaceID, expectedRuntimeID pgtype.UUID) (service.IssueWorkflowProfile, error) {
	agent, err := qtx.GetAgent(ctx, agentID)
	if err != nil {
		return service.IssueWorkflowProfile{}, fmt.Errorf("load selected agent: %w", err)
	}
	if agent.WorkspaceID != workspaceID || (expectedRuntimeID.Valid && agent.RuntimeID != expectedRuntimeID) {
		return service.IssueWorkflowProfile{}, errors.New("selected agent changed workspace or runtime before profile capture")
	}
	runtime, err := qtx.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{ID: agent.RuntimeID, WorkspaceID: workspaceID})
	if err != nil {
		return service.IssueWorkflowProfile{}, fmt.Errorf("load selected runtime: %w", err)
	}
	workspace, err := qtx.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return service.IssueWorkflowProfile{}, fmt.Errorf("load workspace context: %w", err)
	}
	replacements, err := service.DecodeBuiltinSkillReplacements(agent.BuiltinSkillReplacements)
	if err != nil {
		return service.IssueWorkflowProfile{}, err
	}
	policySelection := service.AgentBuiltinPolicy{
		SystemKey: agent.SystemKey.String, EnabledIDs: agent.EnabledBuiltinSkillIds,
		WorkspaceID: agent.WorkspaceID, Replacements: replacements,
		PinnedPlatform: &policy.Bundle,
	}
	transactional := *h.TaskService
	transactional.Queries = qtx
	skills, _, err := transactional.LoadAgentSkillBundles(ctx, agent.ID, agent.SystemKey.String, false, policySelection)
	if err != nil {
		return service.IssueWorkflowProfile{}, fmt.Errorf("load selected agent skills: %w", err)
	}
	argsDigest := service.IssueWorkflowCustomArgsDigest(agent.CustomArgs)
	if argsDigest == "" {
		return service.IssueWorkflowProfile{}, errors.New("selected agent has invalid custom arguments")
	}
	profile := service.IssueWorkflowProfile{
		AgentName: agent.Name, AgentInstructions: agent.Instructions,
		ExpectedProvider: runtime.Provider, Model: agent.Model.String,
		ThinkingLevel: agent.ThinkingLevel.String, ServiceTier: agent.ServiceTier.String,
		CustomArgsDigest: argsDigest, Skills: skills,
	}
	if workspace.Context.Valid {
		profile.WorkspaceContext = workspace.Context.String
	}
	return profile, nil
}

func validateClaimIssueWorkflowProfile(profile *service.IssueWorkflowProfile, runtime db.AgentRuntime, agent db.Agent) error {
	if profile == nil || profile.ExpectedProvider != runtime.Provider {
		return errors.New("selected runtime provider no longer matches the ticket profile")
	}
	if digest := service.IssueWorkflowCustomArgsDigest(agent.CustomArgs); digest == "" || digest != profile.CustomArgsDigest {
		return errors.New("selected agent custom arguments changed; explicit profile reselection required")
	}
	return nil
}

func profileSkillRefs(skills []service.AgentSkillData) ([]service.AgentSkillData, []service.AgentSkillRefData) {
	// Preserve the frozen order, source and hash for both full and slim claims.
	return service.BuildAgentSkillBundles(skills)
}

func profileSkillMap(skills []service.AgentSkillData) map[string]service.AgentSkillData {
	allowed := make(map[string]service.AgentSkillData, len(skills))
	for _, skill := range skills {
		allowed[service.AgentSkillBundleKey(skill.Source, skill.ID)] = skill
	}
	return allowed
}

type reselectIssueWorkflowProfileRequest struct {
	AgentID                  string  `json:"agent_id"`
	ExpectedProfileID        string  `json:"expected_profile_id"`
	RequestID                string  `json:"request_key"`
	Reason                   string  `json:"reason"`
	Consequences             string  `json:"consequences"`
	Reconciliation           string  `json:"reconciliation"`
	SupplementalInstructions *string `json:"supplemental_instructions"`
}

// ReselectIssueWorkflowProfile is a ticket-and-agent scoped operator change.
// It never changes the pinned platform policy or grants review, acceptance or
// delivery authority; those use the separate structured exception ledger.
func (h *Handler) ReselectIssueWorkflowProfile(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "this endpoint is only available to human actors")
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, uuidToString(issue.WorkspaceID), "issue not found", "owner", "admin")
	if !ok {
		return
	}
	var input reselectIssueWorkflowProfileRequest
	if !decodeWorkflowRequest(w, r, &input) {
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	input.Consequences = strings.TrimSpace(input.Consequences)
	input.Reconciliation = strings.TrimSpace(input.Reconciliation)
	if input.Reason == "" || input.Consequences == "" || input.Reconciliation == "" ||
		len(input.Reason) > 4096 || len(input.Consequences) > 4096 || len(input.Reconciliation) > 4096 {
		writeError(w, http.StatusBadRequest, "reason, consequences and reconciliation are required (4096 characters maximum each)")
		return
	}
	if input.SupplementalInstructions != nil {
		*input.SupplementalInstructions = strings.TrimSpace(*input.SupplementalInstructions)
		if len(*input.SupplementalInstructions) > 16000 {
			writeError(w, http.StatusBadRequest, "supplemental instructions exceed 16000 characters")
			return
		}
	}
	agentID, ok := parseUUIDOrBadRequest(w, input.AgentID, "agent_id")
	if !ok {
		return
	}
	expectedID, ok := parseUUIDOrBadRequest(w, input.ExpectedProfileID, "expected_profile_id")
	if !ok {
		return
	}
	requestID, ok := parseUUIDOrBadRequest(w, input.RequestID, "request_key")
	if !ok {
		return
	}
	selected, err := h.TaskService.ReselectIssueWorkflowProfile(r.Context(), service.IssueWorkflowProfileReselection{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, AgentID: agentID,
		ExpectedProfileID: expectedID, RequestID: requestID, ActorUserID: member.UserID,
		Reason: input.Reason, Consequences: input.Consequences,
		Reconciliation: input.Reconciliation, SupplementalInstructions: input.SupplementalInstructions,
	}, func(qtx *db.Queries, policy *service.IssueWorkflowPolicy) (service.IssueWorkflowProfile, error) {
		// A workspace role alone cannot retune another member's private agent
		// or private machine. Recheck live access in the selection transaction.
		activeMember, memberErr := qtx.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
			UserID: member.UserID, WorkspaceID: issue.WorkspaceID,
		})
		if memberErr != nil {
			return service.IssueWorkflowProfile{}, service.ErrIssueWorkflowProfileForbidden
		}
		agent, agentErr := qtx.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
			ID: agentID, WorkspaceID: issue.WorkspaceID,
		})
		if agentErr != nil || agent.ArchivedAt.Valid || !agent.RuntimeID.Valid ||
			!service.CanMemberInvokeAgent(r.Context(), qtx, agent, member.UserID, issue.WorkspaceID) {
			return service.IssueWorkflowProfile{}, service.ErrIssueWorkflowProfileForbidden
		}
		runtime, runtimeErr := qtx.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
			ID: agent.RuntimeID, WorkspaceID: issue.WorkspaceID,
		})
		if runtimeErr != nil || !canUseRuntimeForAgent(activeMember, runtime) {
			return service.IssueWorkflowProfile{}, service.ErrIssueWorkflowProfileForbidden
		}
		return h.captureIssueWorkflowProfile(r.Context(), qtx, policy, agentID, issue.WorkspaceID, pgtype.UUID{})
	})
	if errors.Is(err, service.ErrIssueWorkflowProfileForbidden) {
		writeError(w, http.StatusForbidden, "you do not have permission to reselect this agent profile")
		return
	}
	if errors.Is(err, service.ErrIssueWorkflowProfileConflict) {
		writeError(w, http.StatusConflict, "profile selection changed or issue is unavailable; refresh and reconcile")
		return
	}
	if errors.Is(err, service.ErrIssueWorkflowProfileBusy) {
		writeError(w, http.StatusConflict, "issue-agent task activity is busy; reconcile or retry reselection")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reselect issue workflow profile")
		return
	}
	writeJSON(w, http.StatusCreated, selected)
}

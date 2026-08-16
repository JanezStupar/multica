package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (h *Handler) builtinSkillSummaries(enabledIDs []string) []AgentSkillSummary {
	if h.TaskService == nil {
		return []AgentSkillSummary{}
	}
	enabled := make(map[string]struct{}, len(enabledIDs))
	if enabledIDs != nil {
		for _, id := range enabledIDs {
			enabled[id] = struct{}{}
		}
	}
	skills := h.TaskService.BuiltinSkills()
	result := make([]AgentSkillSummary, 0, len(skills))
	for _, skill := range skills {
		id := service.BuiltinSkillID(skill.Name)
		_, explicitlyEnabled := enabled[id]
		result = append(result, AgentSkillSummary{
			ID:          id,
			Name:        skill.Name,
			Description: skill.Description,
			Enabled:     enabledIDs == nil || explicitlyEnabled,
		})
	}
	return result
}

// SetAgentBuiltinSkillEnabled turns the inherited-all default into an exact
// allow-list on first edit, then toggles one stable built-in skill ID. Unknown
// existing IDs are retained so an older server in a rolling deployment cannot
// erase configuration written by a newer one.
func (h *Handler) SetAgentBuiltinSkillEnabled(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}

	var req struct {
		SkillID string `json:"skill_id"`
		Enabled *bool  `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil {
		writeError(w, http.StatusBadRequest, "skill_id and enabled are required")
		return
	}
	req.SkillID = strings.TrimSpace(req.SkillID)
	known := make(map[string]struct{})
	for _, skill := range h.TaskService.BuiltinSkills() {
		known[service.BuiltinSkillID(skill.Name)] = struct{}{}
	}
	if _, exists := known[req.SkillID]; !exists {
		writeError(w, http.StatusBadRequest, "unknown built-in skill")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.GetAgentForUpdate(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}

	current := locked.EnabledBuiltinSkillIds
	if current == nil {
		current = make([]string, 0, len(known))
		for id := range known {
			current = append(current, id)
		}
	}
	nextSet := make(map[string]struct{}, len(current)+1)
	for _, id := range current {
		if id != req.SkillID {
			nextSet[id] = struct{}{}
		}
	}
	if *req.Enabled {
		nextSet[req.SkillID] = struct{}{}
	}
	next := make([]string, 0, len(nextSet))
	for id := range nextSet {
		next = append(next, id)
	}
	sort.Strings(next)

	updated, err := qtx.UpdateAgentEnabledBuiltinSkillIDs(r.Context(), db.UpdateAgentEnabledBuiltinSkillIDsParams{
		ID:                     locked.ID,
		EnabledBuiltinSkillIds: next,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update built-in skill")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit")
		return
	}

	h.publishBuiltinSkillAgentUpdate(r, updated)
	w.WriteHeader(http.StatusNoContent)
}

// ResetAgentBuiltinSkills restores inherit-all behavior. Future built-ins will
// again be enabled automatically, unlike an explicit list containing every
// built-in known to the current server.
func (h *Handler) ResetAgentBuiltinSkills(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, agentID)
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin transaction")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	locked, err := qtx.GetAgentForUpdate(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent")
		return
	}
	updated, err := qtx.UpdateAgentEnabledBuiltinSkillIDs(r.Context(), db.UpdateAgentEnabledBuiltinSkillIDsParams{
		ID:                     locked.ID,
		EnabledBuiltinSkillIds: nil,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reset built-in skills")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit")
		return
	}

	h.publishBuiltinSkillAgentUpdate(r, updated)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) publishBuiltinSkillAgentUpdate(r *http.Request, updated db.Agent) {
	resp := h.agentToResponse(updated)
	if err := h.enrichAgentResponseWithTargets(r.Context(), &resp, updated.ID); err != nil {
		slog.Warn("built-in skill toggle: load invocation targets for broadcast failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(updated.ID))...)
	}
	if err := h.attachAgentSkills(r.Context(), &resp, updated.ID); err != nil {
		slog.Warn("built-in skill toggle: load agent skills for broadcast failed",
			append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(updated.ID))...)
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(updated.WorkspaceID))
	h.publish(protocol.EventAgentStatus, uuidToString(updated.WorkspaceID), actorType, actorID,
		map[string]any{"agent": broadcastAgentResponse(resp)})
}

package handler

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type issueHandoffResponse struct {
	db.IssueWakeup
	Handoff json.RawMessage `json:"handoff"`
}

type issueHandoffRowResponse struct {
	db.ListIssueWakeupsRow
	Handoff json.RawMessage `json:"handoff"`
}

// CreateIssueHandoff registers one immutable continuation. Status and ownership
// move together with the recipient enqueue after the named source completes.
func (h *Handler) CreateIssueHandoff(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var in service.HandoffInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid handoff body")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid handoff body")
		return
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), uuidToString(issue.WorkspaceID))
	originator := h.invokeOriginatorFromRequest(r, actorType, actorID)
	if originator == "" {
		writeError(w, http.StatusForbidden, "a human originator is required")
		return
	}
	svc := service.IssueWakeupService{Tasks: h.TaskService}
	result, err := svc.CreateHandoff(r.Context(), issue.ID, parseUUID(originator), h.wakeupSourceTaskID(r), in)
	if err != nil {
		wakeupError(w, err)
		return
	}
	// Idempotent registration has the same response for creation and replay.
	writeJSON(w, http.StatusOK, issueHandoffResponse{IssueWakeup: result, Handoff: json.RawMessage(result.Handoff)})
}

func (h *Handler) ListIssueHandoffs(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	workspaceID := uuidToString(issue.WorkspaceID)
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	allowed, ok := h.accessibleAgentIDs(r.Context(), workspaceID, actorType, actorID, member.Role)
	if !ok {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent access")
		return
	}
	ids := make([]pgtype.UUID, 0, len(allowed))
	for id := range allowed {
		ids = append(ids, parseUUID(id))
	}
	rows, err := h.Queries.ListIssueWakeups(r.Context(), db.ListIssueWakeupsParams{IssueID: issue.ID, WorkspaceID: issue.WorkspaceID, AgentIds: ids})
	if err != nil {
		wakeupError(w, err)
		return
	}
	result := make([]issueHandoffRowResponse, 0)
	for _, row := range rows {
		// The structured record names both agents, so apply the same visibility
		// filter to the recipient and the outgoing source before returning it.
		_, recipientVisible := allowed[uuidToString(row.AgentID)]
		if len(row.Handoff) != 0 && string(row.Handoff) != "null" && recipientVisible && row.FilterTaskID.Valid {
			result = append(result, issueHandoffRowResponse{ListIssueWakeupsRow: row, Handoff: json.RawMessage(row.Handoff)})
		}
	}
	writeJSON(w, http.StatusOK, result)
}

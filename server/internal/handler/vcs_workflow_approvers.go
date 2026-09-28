package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// VCSWorkflowApprover binds an immutable Forgejo numeric user ID to a human
// workspace user ID (member.user_id, rather than the member row's ID).
type VCSWorkflowApprover struct {
	ProviderUserID string `json:"provider_user_id"`
	MemberID       string `json:"member_id"`
}

type vcsWorkflowApproversResponse struct {
	Approvers []VCSWorkflowApprover `json:"approvers"`
}

const maxVCSWorkflowApprovers = 100

// ListVCSWorkflowApprovers returns the explicit, connection-scoped Forgejo
// approval identity bindings. Only workspace owners and admins may read them.
func (h *Handler) ListVCSWorkflowApprovers(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin"); !ok {
		return
	}
	connUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "connectionId"), "connection id")
	if !ok {
		return
	}
	if !h.isVCSAvailable() {
		writeError(w, http.StatusNotFound, "vcs integration is not available on this deployment")
		return
	}

	var raw []byte
	err := h.DB.QueryRow(r.Context(), `SELECT workflow_approvers FROM vcs_connection
		WHERE id=$1 AND workspace_id=$2 AND provider='forgejo'`, connUUID, wsUUID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "vcs connection not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow approvers")
		return
	}
	var approvers []VCSWorkflowApprover
	if err := json.Unmarshal(raw, &approvers); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid stored workflow approvers")
		return
	}
	if approvers == nil {
		approvers = []VCSWorkflowApprover{}
	}
	writeJSON(w, http.StatusOK, vcsWorkflowApproversResponse{Approvers: approvers})
}

// ReplaceVCSWorkflowApprovers atomically replaces the complete identity map.
// Empty approvers explicitly clears it; omitted or null approvers are invalid.
func (h *Handler) ReplaceVCSWorkflowApprovers(w http.ResponseWriter, r *http.Request) {
	workspaceID := chi.URLParam(r, "id")
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireWorkspaceRole(w, r, workspaceID, "workspace not found", "owner", "admin")
	if !ok {
		return
	}
	connUUID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "connectionId"), "connection id")
	if !ok {
		return
	}
	if !h.isVCSAvailable() {
		writeError(w, http.StatusNotFound, "vcs integration is not available on this deployment")
		return
	}

	var request struct {
		Approvers *[]VCSWorkflowApprover `json:"approvers"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || request.Approvers == nil || len(*request.Approvers) > maxVCSWorkflowApprovers {
		writeError(w, http.StatusBadRequest, "invalid workflow approvers")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid workflow approvers")
		return
	}

	// Provider identities are numeric and immutable. A canonical decimal form
	// avoids treating 01 and 1 as different identities. A person may link more
	// than one provider account, but each provider account has one target.
	providerIDs := make(map[string]bool, len(*request.Approvers))
	for _, entry := range *request.Approvers {
		id, err := strconv.ParseInt(entry.ProviderUserID, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != entry.ProviderUserID {
			writeError(w, http.StatusBadRequest, "provider_user_id must be a positive decimal Forgejo user ID")
			return
		}
		memberUUID, ok := parseUUIDOrBadRequest(w, entry.MemberID, "member_id")
		if !ok {
			return
		}
		canonicalMemberID := uuidToString(memberUUID)
		if entry.MemberID != canonicalMemberID || providerIDs[entry.ProviderUserID] {
			writeError(w, http.StatusBadRequest, "workflow approvers must have canonical member IDs and unique provider IDs")
			return
		}
		providerIDs[entry.ProviderUserID] = true
	}

	ctx := r.Context()
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow approvers")
		return
	}
	defer tx.Rollback(ctx)

	// Recheck and lock the acting member after the router and handler guard, so
	// revocation cannot race the replacement. The target locks similarly keep
	// every mapping valid through commit.
	var role string
	err = tx.QueryRow(ctx, `SELECT role FROM member WHERE workspace_id=$1 AND user_id=$2 FOR SHARE`,
		wsUUID, member.UserID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !roleAllowed(role, "owner", "admin")) {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow approvers")
		return
	}
	var provider string
	err = tx.QueryRow(ctx, `SELECT provider FROM vcs_connection WHERE id=$1 AND workspace_id=$2 FOR UPDATE`,
		connUUID, wsUUID).Scan(&provider)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && provider != "forgejo") {
		writeError(w, http.StatusNotFound, "vcs connection not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow approvers")
		return
	}
	for _, entry := range *request.Approvers {
		var found string
		err = tx.QueryRow(ctx, `SELECT user_id FROM member
			WHERE workspace_id=$1 AND user_id=$2 FOR SHARE`, wsUUID, entry.MemberID).Scan(&found)
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusBadRequest, "member_id must identify a current workspace member")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to validate workflow approvers")
			return
		}
	}
	raw, err := json.Marshal(*request.Approvers)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow approvers")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE vcs_connection SET workflow_approvers=$1::jsonb WHERE id=$2 AND workspace_id=$3`,
		raw, connUUID, wsUUID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow approvers")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save workflow approvers")
		return
	}
	writeJSON(w, http.StatusOK, vcsWorkflowApproversResponse{Approvers: *request.Approvers})
}

package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type enrollIssueWorkflowPolicyRequest struct {
	SkillID string `json:"skill_id"`
}

// GetIssueWorkflowPolicy reads the version and complete content an issue will
// receive on every future claim. An unpinned issue keeps its legacy behavior.
func (h *Handler) GetIssueWorkflowPolicy(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	raw, err := h.Queries.GetIssueWorkflowPolicy(r.Context(), db.GetIssueWorkflowPolicyParams{
		ID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load issue workflow policy")
		return
	}
	policy, err := h.TaskService.DecodeIssueWorkflowPolicy(raw)
	if err != nil {
		slog.Error("invalid stored issue workflow policy", "issue_id", uuidToString(issue.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "invalid issue workflow policy")
		return
	}
	if policy == nil {
		writeError(w, http.StatusNotFound, "issue workflow policy not enrolled")
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

// EnrollIssueWorkflowPolicy freezes a complete workspace platform skill before
// an issue has any task history. A later change needs explicit work/session
// reconciliation and is intentionally unavailable through this endpoint.
func (h *Handler) EnrollIssueWorkflowPolicy(w http.ResponseWriter, r *http.Request) {
	if isMachineCredentialActor(r) {
		writeError(w, http.StatusForbidden, "this endpoint is only available to human actors")
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceRole(w, r, uuidToString(issue.WorkspaceID), "issue not found", "owner", "admin"); !ok {
		return
	}
	var input enrollIssueWorkflowPolicyRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	skillID, ok := parseUUIDOrBadRequest(w, input.SkillID, "skill_id")
	if !ok {
		return
	}
	// The source is read in one MVCC statement. The frozen bundle remains
	// valid even if a creator edits or removes the source immediately after.
	sourceRaw, err := h.Queries.GetWorkflowPolicySourceSkill(r.Context(), db.GetWorkflowPolicySourceSkillParams{
		ID: skillID, WorkspaceID: issue.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "skill not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load workflow skill")
		return
	}
	var source service.AgentSkillData
	if err := json.Unmarshal(sourceRaw, &source); err != nil {
		writeError(w, http.StatusInternalServerError, "invalid workflow skill")
		return
	}
	for _, file := range source.Files {
		if !validateFilePath(file.Path) {
			writeError(w, http.StatusBadRequest, "workflow skill has unsafe file path")
			return
		}
	}
	policy, err := h.TaskService.NewIssueWorkflowPolicy(source)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to encode workflow policy")
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enroll workflow policy")
		return
	}
	defer tx.Rollback(r.Context())
	// This brief admin operation blocks task INSERT/UPDATE before checking
	// history. An issue row lock alone would not exclude every enqueue path.
	// A bounded wait turns contention into a visible retry instead of tying up
	// the scheduler indefinitely.
	if _, err := tx.Exec(r.Context(), "SET LOCAL lock_timeout = '2s'"); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enroll workflow policy")
		return
	}
	if _, err := tx.Exec(r.Context(), "LOCK TABLE agent_task_queue IN SHARE ROW EXCLUSIVE MODE"); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			writeError(w, http.StatusConflict, "task activity is busy; retry enrollment")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to enroll workflow policy")
		return
	}
	// Some enqueue paths lock the issue before inserting a task. Never wait
	// on that row while holding the table exclusion: that would invert their
	// lock order. A contended issue is a retryable enrollment conflict.
	var lockedIssueID string
	var frozen bool
	err = tx.QueryRow(r.Context(), `SELECT id, workflow_frozen FROM issue WHERE id = $1 AND workspace_id = $2 FOR UPDATE NOWAIT`,
		issue.ID, issue.WorkspaceID).Scan(&lockedIssueID, &frozen)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			writeError(w, http.StatusConflict, "issue activity is busy; retry enrollment")
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "issue not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to enroll workflow policy")
		return
	}
	if frozen {
		writeError(w, http.StatusConflict, "issue is frozen; use explicit workflow migration")
		return
	}
	qtx := h.Queries.WithTx(tx)
	hasTasks, err := qtx.IssueHasTaskHistory(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check issue tasks")
		return
	}
	if hasTasks {
		writeError(w, http.StatusConflict, "issue has task history; policy migration is not available")
		return
	}
	existingRaw, err := qtx.GetIssueWorkflowPolicy(r.Context(), db.GetIssueWorkflowPolicyParams{
		ID: issue.ID, WorkspaceID: issue.WorkspaceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load issue workflow policy")
		return
	}
	existing, err := h.TaskService.DecodeIssueWorkflowPolicy(existingRaw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid issue workflow policy")
		return
	}
	if existing != nil {
		if existing.SourceSkillID != policy.SourceSkillID || existing.Version != policy.Version {
			writeError(w, http.StatusConflict, "issue is already pinned to another policy version")
			return
		}
		if err := tx.Commit(r.Context()); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to enroll workflow policy")
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	}
	if _, err := qtx.PinIssueWorkflowPolicy(r.Context(), db.PinIssueWorkflowPolicyParams{
		ID: issue.ID, WorkspaceID: issue.WorkspaceID, WorkflowPolicy: encoded,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusConflict, "issue policy changed during enrollment")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to enroll workflow policy")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enroll workflow policy")
		return
	}
	writeJSON(w, http.StatusCreated, policy)
}

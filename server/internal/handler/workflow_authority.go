package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

func decodeWorkflowBody(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		writeError(w, http.StatusBadRequest, "invalid workflow request body")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid workflow request body")
		return false
	}
	return true
}

func workflowAuthorityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrWorkflowAuthorityInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrWorkflowAuthorityForbidden):
		writeError(w, http.StatusForbidden, "workflow action is not authorized")
	case errors.Is(err, service.ErrWorkflowAuthorityConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrWorkflowAuthorityUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		slog.Error("issue workflow action", "error", err)
		writeError(w, http.StatusInternalServerError, "issue workflow action failed")
	}
}

func (h *Handler) workflowActorForIssue(r *http.Request, workspaceID string) service.WorkflowActor {
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	return service.WorkflowActor{Type: actorType, ID: actorID, SourceTaskID: uuidToString(h.wakeupSourceTaskID(r))}
}

func (h *Handler) workflowAuthorityService() service.WorkflowAuthorityService {
	return service.WorkflowAuthorityService{Tasks: h.TaskService, ReviewVerifier: h.verifyWorkflowReviewEvidence}
}

func (h *Handler) RegisterIssueWorkflowReview(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var in service.WorkflowReviewInput
	if !decodeWorkflowBody(w, r, &in) {
		return
	}
	actor := h.workflowActorForIssue(r, uuidToString(issue.WorkspaceID))
	svc := h.workflowAuthorityService()
	if err := svc.RegisterReview(r.Context(), issue.WorkspaceID, issue.ID, actor, in); err != nil {
		workflowAuthorityError(w, err)
		return
	}
	state, err := svc.ReadState(r.Context(), issue.WorkspaceID, issue.ID, actor)
	if err != nil {
		workflowAuthorityError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, state)
}

func (h *Handler) AcceptIssueWorkflow(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var in service.WorkflowAcceptanceInput
	if !decodeWorkflowBody(w, r, &in) {
		return
	}
	actor := h.workflowActorForIssue(r, uuidToString(issue.WorkspaceID))
	svc := h.workflowAuthorityService()
	stateName, err := svc.AcceptWorkflow(r.Context(), issue.WorkspaceID, issue.ID, actor, in)
	if err != nil {
		workflowAuthorityError(w, err)
		return
	}
	state, err := svc.ReadState(r.Context(), issue.WorkspaceID, issue.ID, actor)
	if err != nil {
		workflowAuthorityError(w, err)
		return
	}
	code := http.StatusOK
	if stateName == "requested" {
		code = http.StatusAccepted
	}
	writeJSON(w, code, state)
}

func (h *Handler) RejectIssueWorkflow(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var in service.WorkflowRejectionInput
	if !decodeWorkflowBody(w, r, &in) {
		return
	}
	actor := h.workflowActorForIssue(r, uuidToString(issue.WorkspaceID))
	svc := h.workflowAuthorityService()
	if err := svc.RejectWorkflow(r.Context(), issue.WorkspaceID, issue.ID, actor, in); err != nil {
		workflowAuthorityError(w, err)
		return
	}
	state, err := svc.ReadState(r.Context(), issue.WorkspaceID, issue.ID, actor)
	if err != nil {
		workflowAuthorityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// GetIssueWorkflow reads a consistent, credential-free view of the current
// candidate, review attestations, acceptance and per-PR delivery progress.
func (h *Handler) GetIssueWorkflow(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	actor := h.workflowActorForIssue(r, uuidToString(issue.WorkspaceID))
	svc := h.workflowAuthorityService()
	state, err := svc.ReadState(r.Context(), issue.WorkspaceID, issue.ID, actor)
	if errors.Is(err, service.ErrWorkflowAuthorityUnavailable) {
		writeError(w, http.StatusNotFound, "issue workflow is not enrolled")
		return
	}
	if err != nil {
		slog.Error("read issue workflow", "issue_id", uuidToString(issue.ID), "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read issue workflow")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

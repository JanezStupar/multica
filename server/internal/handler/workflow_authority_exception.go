package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

func (h *Handler) GrantIssueWorkflowException(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var input service.WorkflowExceptionInput
	if !decodeWorkflowBody(w, r, &input) {
		return
	}
	actor := h.workflowActorForIssue(r, uuidToString(issue.WorkspaceID))
	svc := h.workflowAuthorityService()
	if _, err := svc.GrantException(r.Context(), issue.WorkspaceID, issue.ID, actor, input); err != nil {
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

func (h *Handler) RevokeIssueWorkflowException(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	exceptionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "exceptionID"), "exception_id")
	if !ok {
		return
	}
	var input service.WorkflowExceptionRevokeInput
	if !decodeWorkflowBody(w, r, &input) {
		return
	}
	actor := h.workflowActorForIssue(r, uuidToString(issue.WorkspaceID))
	svc := h.workflowAuthorityService()
	if err := svc.RevokeException(r.Context(), issue.WorkspaceID, issue.ID, exceptionID, actor, input); err != nil {
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

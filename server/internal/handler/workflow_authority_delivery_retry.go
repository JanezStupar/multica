package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

func (h *Handler) RetryIssueWorkflowDelivery(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	deliveryID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "deliveryID"), "delivery_id")
	if !ok {
		return
	}
	var in service.WorkflowDeliveryRetryInput
	if !decodeWorkflowBody(w, r, &in) {
		return
	}
	actor := h.workflowActorForIssue(r, uuidToString(issue.WorkspaceID))
	svc := h.workflowAuthorityService()
	changed, err := svc.RetryDelivery(r.Context(), issue.WorkspaceID, issue.ID, deliveryID, actor, in)
	if err != nil {
		workflowAuthorityError(w, err)
		return
	}
	if changed && h.WorkflowDeliveryWorker != nil {
		h.WorkflowDeliveryWorker.Notify()
	}
	state, err := svc.ReadState(r.Context(), issue.WorkspaceID, issue.ID, actor)
	if err != nil {
		workflowAuthorityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

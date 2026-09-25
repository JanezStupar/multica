package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

func (h *Handler) changeIssueWorkflowCompletion(w http.ResponseWriter, r *http.Request, action string) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	acceptanceID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "acceptanceID"), "acceptance_id")
	if !ok {
		return
	}
	var in service.WorkflowCompletionActionInput
	if !decodeWorkflowBody(w, r, &in) {
		return
	}
	actor := h.workflowActorForIssue(r, uuidToString(issue.WorkspaceID))
	svc := h.workflowAuthorityService()
	changed, err := svc.ChangeCompletion(r.Context(), issue.WorkspaceID, issue.ID, acceptanceID, actor, action, in)
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

func (h *Handler) HoldIssueWorkflowDelivery(w http.ResponseWriter, r *http.Request) {
	h.changeIssueWorkflowCompletion(w, r, "hold")
}

func (h *Handler) ReleaseIssueWorkflowDelivery(w http.ResponseWriter, r *http.Request) {
	h.changeIssueWorkflowCompletion(w, r, "release")
}

func (h *Handler) CompleteIssueWorkflowOutcome(w http.ResponseWriter, r *http.Request) {
	h.changeIssueWorkflowCompletion(w, r, "complete")
}

func (h *Handler) RetryIssueWorkflowOutcome(w http.ResponseWriter, r *http.Request) {
	h.changeIssueWorkflowCompletion(w, r, "retry-outcome")
}

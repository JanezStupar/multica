package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/integrations/workflowdelivery"
	"github.com/multica-ai/multica/server/internal/service"
)

const workflowReviewVerificationTimeout = 30 * time.Second

// verifyWorkflowReviewEvidence is the authority service's credential boundary.
// The candidate names exact PRs and commits; current workspace integration
// bindings supply both provider API bases and tokens. Review links are matched
// to these PRs before any request, and are never contacted as HTTP targets.
func (h *Handler) verifyWorkflowReviewEvidence(ctx context.Context, input service.WorkflowReviewEvidenceInput) error {
	if h == nil || h.DB == nil || !input.WorkspaceID.Valid {
		return workflowdelivery.ErrTransient
	}
	if len(input.PRs) == 0 {
		if len(input.ReviewURLs) != 0 {
			return workflowdelivery.ErrInvalid
		}
		return nil
	}
	if len(input.PRs) > 20 || len(input.ReviewURLs) == 0 || len(input.ReviewURLs) > 32 {
		return workflowdelivery.ErrInvalid
	}
	byPR := make([][]string, len(input.PRs))
	seen := make(map[string]bool, len(input.ReviewURLs))
	for _, raw := range input.ReviewURLs {
		if seen[raw] {
			return workflowdelivery.ErrInvalid
		}
		seen[raw] = true
		match := -1
		for i, pr := range input.PRs {
			if strings.HasPrefix(raw, pr.PRURL+"#") {
				if match >= 0 {
					return workflowdelivery.ErrInvalid
				}
				match = i
			}
		}
		if match < 0 {
			return workflowdelivery.ErrInvalid
		}
		byPR[match] = append(byPR[match], raw)
	}
	for _, links := range byPR {
		if len(links) == 0 {
			return workflowdelivery.ErrUnverified
		}
	}
	verifyCtx, cancel := context.WithTimeout(ctx, workflowReviewVerificationTimeout)
	defer cancel()
	worker := h.WorkflowDeliveryWorker
	if worker == nil {
		worker = NewWorkflowDeliveryWorker(h)
	}
	for i, pr := range input.PRs {
		d := workflowDeliveryIntent{workspaceID: input.WorkspaceID, bindingID: pr.BindingID, provider: pr.Provider,
			repositoryURL: pr.RepositoryURL, prURL: pr.PRURL, owner: pr.Owner, repo: pr.Repo,
			prNumber: pr.Number, expectedSHA: pr.ExpectedHeadSHA}
		provider, token, err := worker.providerForIntent(verifyCtx, h.DB, d)
		if err != nil {
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return fmt.Errorf("%w: provider binding missing", workflowdelivery.ErrUnsupported)
			case errors.Is(err, workflowdelivery.ErrInvalid), errors.Is(err, workflowdelivery.ErrUnsupported):
				return err
			default:
				return fmt.Errorf("%w: provider credentials unavailable", workflowdelivery.ErrTransient)
			}
		}
		ref := workflowdelivery.Ref{Owner: pr.Owner, Repo: pr.Repo, Number: pr.Number,
			RepositoryURL: pr.RepositoryURL, PullURL: pr.PRURL}
		if _, err := provider.VerifyReviewEvidence(verifyCtx, ref, token, pr.ExpectedHeadSHA, byPR[i]); err != nil {
			return err
		}
	}
	return nil
}

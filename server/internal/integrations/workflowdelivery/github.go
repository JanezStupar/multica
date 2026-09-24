package workflowdelivery

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

func (a *adapter) githubMarkReady(ctx context.Context, token, nodeID string) error {
	if nodeID == "" {
		return deliveryError(ErrAmbiguous, "identify draft PR", 0)
	}
	const mutation = `mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { id isDraft } } }`
	status, body, err := a.request(ctx, "make PR ready", http.MethodPost, a.apiBase+"/graphql", token,
		map[string]any{"query": mutation, "variables": map[string]string{"id": nodeID}})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return mutationStatusError("make PR ready", status)
	}
	var payload struct {
		Errors []struct {
			Type string `json:"type"`
		} `json:"errors"`
		Data struct {
			Mark struct {
				PullRequest *struct {
					ID      string `json:"id"`
					IsDraft bool   `json:"isDraft"`
				} `json:"pullRequest"`
			} `json:"markPullRequestReadyForReview"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return deliveryError(ErrAmbiguous, "decode ready result", status)
	}
	for _, issue := range payload.Errors {
		switch strings.ToUpper(issue.Type) {
		case "FORBIDDEN", "UNAUTHORIZED":
			return deliveryError(ErrUnauthorized, "make PR ready", status)
		case "RATE_LIMITED":
			return deliveryError(ErrTransient, "make PR ready", status)
		}
	}
	ready := payload.Data.Mark.PullRequest
	if len(payload.Errors) != 0 || ready == nil || ready.ID != nodeID || ready.IsDraft {
		return deliveryError(ErrAmbiguous, "verify ready result", status)
	}
	return nil
}

func (a *adapter) githubMerge(ctx context.Context, ref Ref, token, expectedSHA string, method MergeMethod) (int, []byte, error) {
	return a.request(ctx, "merge PR", http.MethodPut, a.pullEndpoint(ref)+"/merge", token,
		map[string]string{"sha": strings.ToLower(expectedSHA), "merge_method": string(method)})
}

package workflowdelivery

import (
	"context"
	"net/http"
	"strings"
)

// Gitea and Forgejo expose the same expected-head merge field. A caller must
// supply the accepted source revision; force_merge is never sent.
func (a *adapter) forgejoMerge(ctx context.Context, ref Ref, token, expectedSHA string, method MergeMethod) (int, []byte, error) {
	return a.request(ctx, "merge PR", http.MethodPost, a.pullEndpoint(ref)+"/merge", token,
		map[string]string{"do": string(method), "head_commit_id": strings.ToLower(expectedSHA)})
}

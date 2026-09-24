package workflowdelivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func reviewURL(kind Kind, base string) string {
	if kind == GitHub {
		return base + "/team/project/pull/7#pullrequestreview-80"
	}
	return base + "/team/project/pulls/7#issuecomment-80"
}

func TestVerifyReviewEvidenceExactProviderReview(t *testing.T) {
	for _, kind := range []Kind{GitHub, Forgejo, Gitea} {
		t.Run(string(kind), func(t *testing.T) {
			reads := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected mutation %s", r.Method)
				}
				if (kind == GitHub && r.Header.Get("Authorization") != "Bearer short-lived") || (kind != GitHub && r.Header.Get("Authorization") != "token short-lived") {
					t.Error("provider token missing")
				}
				if strings.HasSuffix(r.URL.Path, "/reviews") {
					reads++
					_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 80, "html_url": reviewURL(kind, "https://"+r.Host), "commit_id": acceptedHead, "state": "APPROVED", "user": map[string]any{"id": 22, "login": "reviewer"}}})
					return
				}
				writePR(w, "Ready work", acceptedHead, false, false, "open")
			}))
			defer server.Close()
			provider, ref := testProvider(t, kind, server)
			evidence, err := provider.VerifyReviewEvidence(context.Background(), ref, "short-lived", acceptedHead, []string{reviewURL(kind, server.URL)})
			if err != nil || evidence.PullURL != ref.PullURL || evidence.HeadSHA != acceptedHead || evidence.PRState != "open" || evidence.ReviewerIDs[reviewURL(kind, server.URL)] != string(kind)+":22" || reads != 1 {
				t.Fatalf("evidence=%+v err=%v reads=%d", evidence, err, reads)
			}
		})
	}
}

func TestVerifyReviewEvidenceFailsClosedForMissingForeignAndStaleReviews(t *testing.T) {
	for _, tc := range []struct {
		name         string
		url          func(string) string
		head, commit string
		state        string
		omitCommit   bool
		want         error
		wantCalls    int
	}{
		{name: "foreign origin", url: func(string) string { return "https://unbound.example/team/project/pull/7#pullrequestreview-80" }, head: acceptedHead, commit: acceptedHead, want: ErrInvalid, wantCalls: 0},
		{name: "different PR", url: func(base string) string { return base + "/team/project/pull/8#pullrequestreview-80" }, head: acceptedHead, commit: acceptedHead, want: ErrInvalid, wantCalls: 0},
		{name: "missing review", url: func(base string) string { return base + "/team/project/pull/7#pullrequestreview-81" }, head: acceptedHead, commit: acceptedHead, want: ErrUnverified, wantCalls: 3},
		{name: "reviewed older commit", url: func(base string) string { return base + "/team/project/pull/7#pullrequestreview-80" }, head: acceptedHead, commit: changedHead, want: ErrUnverified, wantCalls: 2},
		{name: "pending review", url: func(base string) string { return base + "/team/project/pull/7#pullrequestreview-80" }, head: acceptedHead, commit: acceptedHead, state: "PENDING", want: ErrUnverified, wantCalls: 2},
		{name: "provider lacks commit binding", url: func(base string) string { return base + "/team/project/pull/7#pullrequestreview-80" }, head: acceptedHead, omitCommit: true, want: ErrUnsupported, wantCalls: 2},
		{name: "PR head moved", url: func(base string) string { return base + "/team/project/pull/7#pullrequestreview-80" }, head: changedHead, commit: acceptedHead, want: ErrStaleHead, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if strings.HasSuffix(r.URL.Path, "/reviews") {
					if r.URL.Query().Get("page") == "2" {
						_ = json.NewEncoder(w).Encode([]any{})
						return
					}
					state := tc.state
					if state == "" {
						state = "APPROVED"
					}
					review := map[string]any{"id": 80, "html_url": "https://" + r.Host + "/team/project/pull/7#pullrequestreview-80", "state": state}
					if !tc.omitCommit {
						review["commit_id"] = tc.commit
					}
					_ = json.NewEncoder(w).Encode([]any{review})
					return
				}
				writePR(w, "Ready work", tc.head, false, false, "open")
			}))
			defer server.Close()
			provider, ref := testProvider(t, GitHub, server)
			_, err := provider.VerifyReviewEvidence(context.Background(), ref, "short-lived", acceptedHead, []string{tc.url(server.URL)})
			if !errors.Is(err, tc.want) || calls != tc.wantCalls {
				t.Fatalf("err=%v calls=%d; want %v and %d", err, calls, tc.want, tc.wantCalls)
			}
		})
	}
}

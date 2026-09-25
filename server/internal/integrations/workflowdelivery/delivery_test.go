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

const acceptedHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const changedHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func testProvider(t *testing.T, kind Kind, server *httptest.Server) (Provider, Ref) {
	t.Helper()
	api := server.URL
	if kind != GitHub {
		api += "/api/v1"
	}
	provider, err := New(Config{Kind: kind, APIBaseURL: api, RepositoryBaseURL: server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ref := Ref{Owner: "team", Repo: "project", Number: 7, RepositoryURL: server.URL + "/team/project"}
	if kind == GitHub {
		ref.PullURL = ref.RepositoryURL + "/pull/7"
	} else {
		ref.PullURL = ref.RepositoryURL + "/pulls/7"
	}
	return provider, ref
}

func writePR(w http.ResponseWriter, title, head string, draft, merged bool, state string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"title": title, "head": map[string]string{"sha": head}, "draft": draft,
		"merged": merged, "state": state, "node_id": "PR_node_7",
		"content_version": 3, "merge_commit_sha": changedHead,
	})
}

func decodeRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestReadPRPreservesProviderMergerAuditIdentity(t *testing.T) {
	for _, kind := range []Kind{GitHub, Forgejo, Gitea} {
		t.Run(string(kind), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Fatalf("read PR attempted %s", r.Method)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"title": "Accepted work", "head": map[string]string{"sha": changedHead},
					"draft": false, "merged": true, "state": "closed", "merge_commit_sha": acceptedHead,
					"merged_by": map[string]any{"id": 5, "login": "Multica", "type": "User"},
				})
			}))
			defer server.Close()
			provider, ref := testProvider(t, kind, server)
			pr, err := provider.ReadPR(context.Background(), ref, "token")
			if err != nil || !pr.Merged || pr.State != "closed" || pr.MergedBy.ID != 5 ||
				pr.MergedBy.Login != "Multica" || pr.MergedBy.Type != "User" {
				t.Fatalf("merger identity lost: PR=%+v err=%v", pr, err)
			}
		})
	}
}

func TestPrepareReadyReconcilesMergedAndStopsClosedPR(t *testing.T) {
	for _, kind := range []Kind{GitHub, Forgejo, Gitea} {
		for _, state := range []string{"merged", "closed", "changed_head"} {
			t.Run(string(kind)+"/"+state, func(t *testing.T) {
				calls := 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodGet {
						t.Errorf("terminal PR triggered mutation %s", r.Method)
					}
					head := acceptedHead
					if state == "changed_head" {
						head = changedHead
					}
					writePR(w, "Ready work", head, false, state == "merged", "closed")
				}))
				defer server.Close()
				provider, ref := testProvider(t, kind, server)
				pr, err := provider.PrepareReady(context.Background(), ref, "token", acceptedHead)
				switch state {
				case "merged":
					if err != nil || !pr.Merged || pr.HeadSHA != acceptedHead {
						t.Fatalf("merged exact head was not reconciled: %+v, %v", pr, err)
					}
				case "closed":
					if !errors.Is(err, ErrClosed) {
						t.Fatalf("closed unmerged error=%v", err)
					}
				default:
					if !errors.Is(err, ErrStaleHead) {
						t.Fatalf("changed head error=%v", err)
					}
				}
				if calls != 1 {
					t.Fatalf("terminal PR calls=%d", calls)
				}
			})
		}
	}
}

func TestGitHubPrepareReadyChecksHeadAroundBothMutations(t *testing.T) {
	title, draft, reads, titleEdits, readyCalls := "WIP: implement", true, 0, 0, 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer short-lived" {
			t.Errorf("missing scoped token on %s", r.URL.Path)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/team/project/pulls/7":
			reads++
			writePR(w, title, acceptedHead, draft, false, "open")
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/team/project/pulls/7":
			titleEdits++
			payload := decodeRequest(t, r)
			if payload["title"] != "implement" {
				t.Errorf("unexpected title edit: %#v", payload)
			}
			title = "implement"
			writePR(w, title, acceptedHead, draft, false, "open")
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			readyCalls++
			payload := decodeRequest(t, r)
			variables, ok := payload["variables"].(map[string]any)
			if !ok || variables["id"] != "PR_node_7" || !strings.Contains(payload["query"].(string), "markPullRequestReadyForReview") {
				t.Errorf("unexpected ready mutation: %#v", payload)
			}
			draft = false
			_, _ = w.Write([]byte(`{"data":{"markPullRequestReadyForReview":{"pullRequest":{"id":"PR_node_7","isDraft":false}}}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	provider, ref := testProvider(t, GitHub, server)
	pr, err := provider.PrepareReady(context.Background(), ref, "short-lived", acceptedHead)
	if err != nil || pr.Title != "implement" || pr.Draft || reads != 3 || titleEdits != 1 || readyCalls != 1 {
		t.Fatalf("ready PR=%+v err=%v reads=%d title=%d ready=%d", pr, err, reads, titleEdits, readyCalls)
	}
}

func TestForgejoTitleReadinessDoesNotClaimSeparateDraftReady(t *testing.T) {
	for _, afterTitleDraft := range []bool{false, true} {
		t.Run(map[bool]string{false: "title_only", true: "separate_draft"}[afterTitleDraft], func(t *testing.T) {
			title, draft, patches := "WIP: implement", true, 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "token short-lived" {
					t.Errorf("unexpected auth header")
				}
				switch r.Method {
				case http.MethodGet:
					writePR(w, title, acceptedHead, draft, false, "open")
				case http.MethodPatch:
					patches++
					payload := decodeRequest(t, r)
					if payload["title"] != "implement" || payload["content_version"] != float64(3) {
						t.Errorf("unexpected title edit: %#v", payload)
					}
					title, draft = "implement", afterTitleDraft
					writePR(w, title, acceptedHead, draft, false, "open")
				default:
					t.Errorf("unexpected request %s", r.Method)
				}
			}))
			defer server.Close()
			provider, ref := testProvider(t, Forgejo, server)
			pr, err := provider.PrepareReady(context.Background(), ref, "short-lived", acceptedHead)
			if patches != 1 {
				t.Fatalf("title patches=%d", patches)
			}
			if afterTitleDraft {
				if !errors.Is(err, ErrUnsupported) {
					t.Fatalf("separate draft must fail closed: PR=%+v err=%v", pr, err)
				}
			} else if err != nil || pr.Draft || pr.Title != "implement" {
				t.Fatalf("title ready PR=%+v err=%v", pr, err)
			}
		})
	}
}

func TestMergeSendsProviderEnforcedExpectedHeadAndConfiguredMethod(t *testing.T) {
	for _, kind := range []Kind{GitHub, Forgejo, Gitea} {
		t.Run(string(kind), func(t *testing.T) {
			merged, mergeCalls := false, 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/merge") {
					mergeCalls++
					payload := decodeRequest(t, r)
					if kind == GitHub {
						if r.Method != http.MethodPut || payload["sha"] != acceptedHead || payload["merge_method"] != "squash" {
							t.Errorf("unsafe GitHub merge: %#v", payload)
						}
					} else if r.Method != http.MethodPost || payload["head_commit_id"] != acceptedHead || payload["do"] != "squash" {
						t.Errorf("unsafe Forgejo/Gitea merge: %#v", payload)
					}
					if len(payload) != 2 {
						t.Errorf("unexpected merge option: %#v", payload)
					}
					merged = true
					w.WriteHeader(http.StatusOK)
					return
				}
				if r.Method != http.MethodGet {
					t.Errorf("unexpected request %s", r.Method)
				}
				state := "open"
				if merged {
					state = "closed"
				}
				writePR(w, "implement", acceptedHead, false, merged, state)
			}))
			defer server.Close()
			provider, ref := testProvider(t, kind, server)
			result, err := provider.Merge(context.Background(), ref, "short-lived", acceptedHead, Squash)
			if err != nil || result.HeadSHA != acceptedHead || result.MergeCommitSHA != changedHead || mergeCalls != 1 {
				t.Fatalf("merge result=%+v err=%v calls=%d", result, err, mergeCalls)
			}
			result, err = provider.Merge(context.Background(), ref, "short-lived", acceptedHead, Squash)
			if err != nil || result.HeadSHA != acceptedHead || mergeCalls != 1 {
				t.Fatalf("idempotent reconcile=%+v err=%v calls=%d", result, err, mergeCalls)
			}
		})
	}
}

func TestMergeConflictDistinguishesStaleHeadFromBlockedChecks(t *testing.T) {
	for _, kind := range []Kind{GitHub, Forgejo, Gitea} {
		for _, current := range []string{acceptedHead, changedHead} {
			t.Run(string(kind)+"/"+current, func(t *testing.T) {
				reads := 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/merge") {
						w.WriteHeader(http.StatusConflict)
						return
					}
					reads++
					head := acceptedHead
					if reads > 1 {
						head = current
					}
					writePR(w, "implement", head, false, false, "open")
				}))
				defer server.Close()
				provider, ref := testProvider(t, kind, server)
				_, err := provider.Merge(context.Background(), ref, "token", acceptedHead, Merge)
				want := ErrBlocked
				if current == changedHead {
					want = ErrStaleHead
				}
				if !errors.Is(err, want) || reads != 2 {
					t.Fatalf("conflict err=%v reads=%d want=%v", err, reads, want)
				}
			})
		}
	}
}

func TestPrepareReadyHeadMovesAfterTitleUpdate(t *testing.T) {
	title, reads, graphqlCalls := "WIP: implement", 0, 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			reads++
			head := acceptedHead
			if reads > 1 {
				head = changedHead
			}
			writePR(w, title, head, true, false, "open")
		case http.MethodPatch:
			title = "implement"
			writePR(w, title, acceptedHead, true, false, "open")
		default:
			graphqlCalls++
		}
	}))
	defer server.Close()
	provider, ref := testProvider(t, GitHub, server)
	_, err := provider.PrepareReady(context.Background(), ref, "token", acceptedHead)
	if !errors.Is(err, ErrStaleHead) || graphqlCalls != 0 {
		t.Fatalf("moving head err=%v ready calls=%d", err, graphqlCalls)
	}
}

func TestRejectsUntrustedIdentityAndRedirectWithoutForwardingToken(t *testing.T) {
	forwarded := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded++
		if r.Header.Get("Authorization") != "" {
			t.Errorf("token leaked to redirected host")
		}
	}))
	defer target.Close()
	requests := 0
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	provider, ref := testProvider(t, GitHub, source)
	bad := ref
	bad.PullURL = target.URL + "/team/project/pull/7"
	if _, err := provider.ReadPR(context.Background(), bad, "token"); !errors.Is(err, ErrInvalid) || requests != 0 {
		t.Fatalf("untrusted PR URL err=%v requests=%d", err, requests)
	}
	if _, err := provider.ReadPR(context.Background(), ref, "token"); !errors.Is(err, ErrAmbiguous) || requests != 1 || forwarded != 0 {
		t.Fatalf("redirect err=%v source=%d target=%d", err, requests, forwarded)
	}
	if _, err := New(Config{Kind: GitHub, APIBaseURL: "http://example.com", RepositoryBaseURL: "https://example.com"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("non-HTTPS base err=%v", err)
	}
}

func TestReadAndMergeFailuresRemainClassifiedWithoutProviderBody(t *testing.T) {
	for _, tc := range []struct {
		name       string
		readStatus int
		mergeCode  int
		want       error
	}{
		{name: "read unauthorized", readStatus: http.StatusUnauthorized, want: ErrUnauthorized},
		{name: "read transient", readStatus: http.StatusServiceUnavailable, want: ErrTransient},
		{name: "read malformed", readStatus: http.StatusOK, want: ErrAmbiguous},
		{name: "merge checks", mergeCode: http.StatusMethodNotAllowed, want: ErrBlocked},
		{name: "merge unauthorized", mergeCode: http.StatusUnauthorized, want: ErrUnauthorized},
		{name: "merge forbidden ambiguity", mergeCode: http.StatusForbidden, want: ErrAmbiguous},
		{name: "merge server ambiguity", mergeCode: http.StatusServiceUnavailable, want: ErrAmbiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					if tc.readStatus != 0 {
						w.WriteHeader(tc.readStatus)
						_, _ = w.Write([]byte(`{"secret":"must not leak"}`))
						return
					}
					writePR(w, "implement", acceptedHead, false, false, "open")
					return
				}
				w.WriteHeader(tc.mergeCode)
				_, _ = w.Write([]byte(`{"secret":"must not leak"}`))
			}))
			defer server.Close()
			provider, ref := testProvider(t, GitHub, server)
			var err error
			if tc.readStatus != 0 {
				_, err = provider.ReadPR(context.Background(), ref, "token")
			} else {
				_, err = provider.Merge(context.Background(), ref, "token", acceptedHead, Merge)
			}
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "must not leak") {
				t.Fatalf("classification err=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestReconcileMergedRequiresSameAcceptedRevision(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePR(w, "implement", changedHead, false, true, "closed")
	}))
	defer server.Close()
	provider, ref := testProvider(t, Gitea, server)
	if _, err := provider.ReconcileMerged(context.Background(), ref, "token", acceptedHead); !errors.Is(err, ErrStaleHead) {
		t.Fatalf("changed merged revision err=%v", err)
	}
}

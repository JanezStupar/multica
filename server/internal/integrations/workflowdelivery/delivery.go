// Package workflowdelivery provides provider API primitives for delivering an
// accepted, exact PR revision. It does not decide acceptance, mint credentials,
// schedule retries, or call providers until a caller invokes a method.
package workflowdelivery

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Kind string

const (
	GitHub  Kind = "github"
	Forgejo Kind = "forgejo"
	Gitea   Kind = "gitea"
)

var (
	ErrInvalid      = errors.New("invalid delivery input")
	ErrUnsupported  = errors.New("provider operation unsupported")
	ErrUnauthorized = errors.New("provider authorization failed")
	ErrNotFound     = errors.New("provider pull request not found")
	ErrStaleHead    = errors.New("pull request head differs from accepted revision")
	ErrBlocked      = errors.New("provider checks or protections block delivery")
	ErrClosed       = errors.New("pull request closed without merge")
	ErrTransient    = errors.New("temporary provider failure")
	ErrAmbiguous    = errors.New("provider delivery outcome is ambiguous")
	ErrUnverified   = errors.New("review evidence did not establish the accepted revision")
)

// Error carries a safe class and HTTP status. Provider bodies and credentials
// are deliberately absent: API errors may echo request data or tokens.
type Error struct {
	Kind   error
	Op     string
	Status int
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("workflow delivery %s: %v (HTTP %d)", e.Op, e.Kind, e.Status)
	}
	return fmt.Sprintf("workflow delivery %s: %v", e.Op, e.Kind)
}
func (e *Error) Unwrap() error { return e.Kind }

func deliveryError(kind error, op string, status int) error {
	return &Error{Kind: kind, Op: op, Status: status}
}

// Config is derived from trusted workspace provider configuration. GitHub's
// public API uses api.github.com with github.com as the repository base;
// self-hosted providers use the same origin for both. No API base is inferred
// from a PR URL supplied by an agent.
type Config struct {
	Kind              Kind
	APIBaseURL        string
	RepositoryBaseURL string
}

// Ref identifies one PR without carrying authentication. URLs supplied with
// a handoff must agree with the workspace-configured origin and identity.
type Ref struct {
	Owner         string
	Repo          string
	Number        int64
	RepositoryURL string
	PullURL       string
}

type PullRequest struct {
	Title          string
	HeadSHA        string
	State          string
	Draft          bool
	Merged         bool
	MergeCommitSHA string
	NodeID         string // GitHub GraphQL identity, never a credential.
	ContentVersion int64  // Forgejo/Gitea edit concurrency token when returned.
}

type MergeMethod string

const (
	Merge  MergeMethod = "merge"
	Squash MergeMethod = "squash"
	Rebase MergeMethod = "rebase"
)

type MergeResult struct {
	HeadSHA        string
	MergeCommitSHA string
}

// ReviewEvidence proves that the linked, published provider reviews belong to
// this PR and its exact accepted head. ReviewerIDs are audit metadata only;
// they do not establish independence from a writer task without a trusted
// mapping from that task to a provider account.
type ReviewEvidence struct {
	PullURL     string
	HeadSHA     string
	PRState     string
	ReviewerIDs map[string]string // asserted review URL -> provider account ID, if returned
}

type Provider interface {
	ReadPR(ctx context.Context, ref Ref, token string) (PullRequest, error)
	PrepareReady(ctx context.Context, ref Ref, token, expectedSHA string) (PullRequest, error)
	Merge(ctx context.Context, ref Ref, token, expectedSHA string, method MergeMethod) (MergeResult, error)
	ReconcileMerged(ctx context.Context, ref Ref, token, expectedSHA string) (MergeResult, error)
	VerifyReviewEvidence(ctx context.Context, ref Ref, token, expectedHeadSHA string, reviewURLs []string) (ReviewEvidence, error)
}

type adapter struct {
	kind     Kind
	apiBase  string
	repoBase string
	client   *http.Client
}

// New rejects credential-bearing or non-HTTPS bases and unsupported providers.
// Its private HTTP client refuses all redirects, so an upstream 3xx cannot
// forward a short-lived token to another host.
func New(config Config, client *http.Client) (Provider, error) {
	if config.Kind != GitHub && config.Kind != Forgejo && config.Kind != Gitea {
		return nil, deliveryError(ErrUnsupported, "configure provider", 0)
	}
	api, err := secureBase(config.APIBaseURL)
	if err != nil {
		return nil, err
	}
	repo, err := secureBase(config.RepositoryBaseURL)
	if err != nil {
		return nil, err
	}
	if config.Kind == GitHub {
		public := api.Hostname() == "api.github.com" && repo.Hostname() == "github.com"
		if !public && api.Host != repo.Host {
			return nil, deliveryError(ErrInvalid, "configure GitHub origins", 0)
		}
	} else if api.Host != repo.Host || strings.TrimRight(api.Path, "/") != strings.TrimRight(repo.Path, "/")+"/api/v1" {
		return nil, deliveryError(ErrInvalid, "configure Forgejo origins", 0)
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	copyClient := *client
	if copyClient.Timeout <= 0 {
		copyClient.Timeout = 15 * time.Second
	}
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	copyClient.Jar = nil
	return &adapter{kind: config.Kind, apiBase: strings.TrimRight(api.String(), "/"), repoBase: strings.TrimRight(repo.String(), "/"), client: &copyClient}, nil
}

func secureBase(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, deliveryError(ErrInvalid, "configure HTTPS base", 0)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

func validSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func validSegment(s string) bool {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\?#@") {
		return false
	}
	return len(s) <= 255
}

func (a *adapter) validate(ref Ref, token string) error {
	if token == "" || strings.ContainsAny(token, "\r\n") || !validSegment(ref.Owner) || !validSegment(ref.Repo) || ref.Number <= 0 {
		return deliveryError(ErrInvalid, "validate PR identity", 0)
	}
	base := a.repoBase + "/" + url.PathEscape(ref.Owner) + "/" + url.PathEscape(ref.Repo)
	wantPull := base + "/pulls/" + strconv.FormatInt(ref.Number, 10)
	if a.kind == GitHub {
		wantPull = base + "/pull/" + strconv.FormatInt(ref.Number, 10)
	}
	if ref.RepositoryURL != base || ref.PullURL != wantPull {
		return deliveryError(ErrInvalid, "validate PR URL", 0)
	}
	return nil
}

func (a *adapter) pullEndpoint(ref Ref) string {
	return a.apiBase + "/repos/" + url.PathEscape(ref.Owner) + "/" + url.PathEscape(ref.Repo) + "/pulls/" + strconv.FormatInt(ref.Number, 10)
}

func (a *adapter) request(ctx context.Context, op, method, endpoint, token string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, deliveryError(ErrInvalid, op, 0)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, nil, deliveryError(ErrInvalid, op, 0)
	}
	if a.kind == GitHub {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	} else {
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Accept", "application/json")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		// A mutation might have succeeded before the connection failed.
		if method != http.MethodGet {
			return 0, nil, deliveryError(ErrAmbiguous, op, 0)
		}
		return 0, nil, deliveryError(ErrTransient, op, 0)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return resp.StatusCode, nil, deliveryError(ErrAmbiguous, op, resp.StatusCode)
	}
	return resp.StatusCode, data, nil
}

func readStatusError(op string, status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return deliveryError(ErrUnauthorized, op, status)
	case http.StatusNotFound:
		return deliveryError(ErrNotFound, op, status)
	case http.StatusTooManyRequests:
		return deliveryError(ErrTransient, op, status)
	default:
		if status >= 500 {
			return deliveryError(ErrTransient, op, status)
		}
		return deliveryError(ErrAmbiguous, op, status)
	}
}

func mutationStatusError(op string, status int) error {
	switch status {
	case http.StatusUnauthorized:
		return deliveryError(ErrUnauthorized, op, status)
	case http.StatusForbidden:
		// 403 is also used for branch protection and provider policy; the
		// status alone cannot prove whether credentials or checks blocked it.
		return deliveryError(ErrAmbiguous, op, status)
	case http.StatusTooManyRequests:
		return deliveryError(ErrTransient, op, status)
	case http.StatusMethodNotAllowed, http.StatusLocked, http.StatusUnprocessableEntity:
		return deliveryError(ErrBlocked, op, status)
	default:
		return deliveryError(ErrAmbiguous, op, status)
	}
}

func (a *adapter) ReadPR(ctx context.Context, ref Ref, token string) (PullRequest, error) {
	if err := a.validate(ref, token); err != nil {
		return PullRequest{}, err
	}
	status, body, err := a.request(ctx, "read PR", http.MethodGet, a.pullEndpoint(ref), token, nil)
	if err != nil {
		return PullRequest{}, err
	}
	if status != http.StatusOK {
		return PullRequest{}, readStatusError("read PR", status)
	}
	var payload struct {
		Title          *string `json:"title"`
		State          *string `json:"state"`
		Draft          *bool   `json:"draft"`
		Merged         *bool   `json:"merged"`
		MergeCommitSHA string  `json:"merge_commit_sha"`
		NodeID         string  `json:"node_id"`
		ContentVersion int64   `json:"content_version"`
		Head           *struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Title == nil || payload.State == nil || payload.Draft == nil || payload.Merged == nil || payload.Head == nil || !validSHA(payload.Head.SHA) {
		return PullRequest{}, deliveryError(ErrAmbiguous, "decode PR", status)
	}
	return PullRequest{Title: *payload.Title, State: *payload.State, Draft: *payload.Draft, Merged: *payload.Merged,
		HeadSHA: strings.ToLower(payload.Head.SHA), MergeCommitSHA: payload.MergeCommitSHA,
		NodeID: payload.NodeID, ContentVersion: payload.ContentVersion}, nil
}

func currentHead(pr PullRequest, expectedSHA string) error {
	if !validSHA(expectedSHA) {
		return deliveryError(ErrInvalid, "expected head", 0)
	}
	if !strings.EqualFold(pr.HeadSHA, expectedSHA) {
		return deliveryError(ErrStaleHead, "expected head", 0)
	}
	return nil
}

func strippedWIP(title string) (string, bool) {
	if len(title) < 4 || !strings.EqualFold(title[:4], "WIP:") {
		return title, false
	}
	stripped := strings.TrimSpace(title[4:])
	return stripped, true
}

func (a *adapter) PrepareReady(ctx context.Context, ref Ref, token, expectedSHA string) (PullRequest, error) {
	pr, err := a.ReadPR(ctx, ref, token)
	if err != nil {
		return PullRequest{}, err
	}
	if err := currentHead(pr, expectedSHA); err != nil {
		return PullRequest{}, err
	}
	if pr.Merged && pr.State == "closed" {
		// A prior mutation may have succeeded before its response was lost.
		// The pinned head still proves this is the accepted revision.
		return pr, nil
	}
	if pr.State != "open" {
		return PullRequest{}, deliveryError(ErrClosed, "prepare PR", 0)
	}
	if title, wip := strippedWIP(pr.Title); wip {
		if title == "" {
			return PullRequest{}, deliveryError(ErrInvalid, "strip WIP title", 0)
		}
		body := map[string]any{"title": title}
		if a.kind != GitHub && pr.ContentVersion > 0 {
			body["content_version"] = pr.ContentVersion
		}
		status, _, requestErr := a.request(ctx, "update PR title", http.MethodPatch, a.pullEndpoint(ref), token, body)
		if requestErr != nil {
			return PullRequest{}, requestErr
		}
		if status != http.StatusOK && status != http.StatusCreated {
			return PullRequest{}, mutationStatusError("update PR title", status)
		}
		pr, err = a.ReadPR(ctx, ref, token)
		if err != nil {
			return PullRequest{}, deliveryError(ErrAmbiguous, "verify PR title", 0)
		}
		if err := currentHead(pr, expectedSHA); err != nil {
			return PullRequest{}, err
		}
		if pr.Merged && pr.State == "closed" {
			return pr, nil
		}
		if pr.State != "open" {
			return PullRequest{}, deliveryError(ErrClosed, "verify PR title", 0)
		}
		if _, wip := strippedWIP(pr.Title); wip {
			return PullRequest{}, deliveryError(ErrAmbiguous, "verify PR title", 0)
		}
	}
	if a.kind == GitHub && pr.Draft {
		if err := a.githubMarkReady(ctx, token, pr.NodeID); err != nil {
			return PullRequest{}, err
		}
		pr, err = a.ReadPR(ctx, ref, token)
		if err != nil {
			return PullRequest{}, deliveryError(ErrAmbiguous, "verify PR ready", 0)
		}
		if err := currentHead(pr, expectedSHA); err != nil {
			return PullRequest{}, err
		}
		if pr.Merged && pr.State == "closed" {
			return pr, nil
		}
		if pr.State != "open" {
			return PullRequest{}, deliveryError(ErrClosed, "verify PR ready", 0)
		}
	}
	// Forgejo/Gitea have a WIP-title mechanism but their documented edit API
	// has no separate draft-to-ready field. A remaining draft is unsupported.
	if pr.Draft && a.kind != GitHub {
		return PullRequest{}, deliveryError(ErrUnsupported, "make draft PR ready", 0)
	}
	if title, wip := strippedWIP(pr.Title); wip || title == "" || pr.Draft || pr.Merged || pr.State != "open" {
		return PullRequest{}, deliveryError(ErrAmbiguous, "verify PR ready", 0)
	}
	return pr, nil
}

func (a *adapter) ReconcileMerged(ctx context.Context, ref Ref, token, expectedSHA string) (MergeResult, error) {
	pr, err := a.ReadPR(ctx, ref, token)
	if err != nil {
		return MergeResult{}, err
	}
	if err := currentHead(pr, expectedSHA); err != nil {
		return MergeResult{}, err
	}
	if !pr.Merged || pr.State != "closed" {
		return MergeResult{}, deliveryError(ErrAmbiguous, "reconcile merge", 0)
	}
	return MergeResult{HeadSHA: pr.HeadSHA, MergeCommitSHA: pr.MergeCommitSHA}, nil
}

func (a *adapter) Merge(ctx context.Context, ref Ref, token, expectedSHA string, method MergeMethod) (MergeResult, error) {
	if method != Merge && method != Squash && method != Rebase {
		return MergeResult{}, deliveryError(ErrInvalid, "merge method", 0)
	}
	pr, err := a.ReadPR(ctx, ref, token)
	if err != nil {
		return MergeResult{}, err
	}
	if err := currentHead(pr, expectedSHA); err != nil {
		return MergeResult{}, err
	}
	if pr.Merged {
		return a.ReconcileMerged(ctx, ref, token, expectedSHA)
	}
	_, wip := strippedWIP(pr.Title)
	if pr.State != "open" || pr.Draft || wip {
		return MergeResult{}, deliveryError(ErrBlocked, "merge PR", 0)
	}
	var status int
	if a.kind == GitHub {
		status, _, err = a.githubMerge(ctx, ref, token, expectedSHA, method)
	} else {
		status, _, err = a.forgejoMerge(ctx, ref, token, expectedSHA, method)
	}
	if err != nil {
		return MergeResult{}, err
	}
	if status == http.StatusConflict {
		latest, readErr := a.ReadPR(ctx, ref, token)
		if readErr != nil {
			return MergeResult{}, deliveryError(ErrAmbiguous, "classify merge conflict", status)
		}
		if !strings.EqualFold(latest.HeadSHA, expectedSHA) {
			return MergeResult{}, deliveryError(ErrStaleHead, "merge PR", status)
		}
		return MergeResult{}, deliveryError(ErrBlocked, "merge PR", status)
	}
	if status != http.StatusOK {
		return MergeResult{}, mutationStatusError("merge PR", status)
	}
	result, err := a.ReconcileMerged(ctx, ref, token, expectedSHA)
	if err != nil {
		if errors.Is(err, ErrStaleHead) {
			return MergeResult{}, err
		}
		return MergeResult{}, deliveryError(ErrAmbiguous, "verify merged PR", 0)
	}
	return result, nil
}

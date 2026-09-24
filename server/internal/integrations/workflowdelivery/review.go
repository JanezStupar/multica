package workflowdelivery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	reviewPageSize = 100
	maxReviewPages = 20
)

var reviewFragment = regexp.MustCompile(`^(pullrequestreview|pullreview|issuecomment)-[1-9][0-9]*$`)

// reviewURLValid checks only the configured PR page and canonical numeric
// review anchors. It never causes a request to the asserted URL: the only
// network target below is the provider API derived from trusted configuration.
func (a *adapter) reviewURLValid(ref Ref, raw string) bool {
	if len(raw) == 0 || len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Opaque != "" || u.Fragment == "" || !reviewFragment.MatchString(u.Fragment) {
		return false
	}
	if a.kind == GitHub && !strings.HasPrefix(u.Fragment, "pullrequestreview-") {
		return false
	}
	return raw == ref.PullURL+"#"+u.Fragment
}

type providerReview struct {
	ID        *int64  `json:"id"`
	HTMLURL   *string `json:"html_url"`
	CommitID  *string `json:"commit_id"`
	State     *string `json:"state"`
	Dismissed *bool   `json:"dismissed"`
	User      *struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"user"`
}

func publishedReview(state string, dismissed *bool) bool {
	if dismissed != nil && *dismissed {
		return false
	}
	switch strings.ToUpper(state) {
	case "APPROVED", "COMMENTED", "COMMENT":
		return true
	default:
		return false
	}
}

// VerifyReviewEvidence establishes only provider facts: the current PR head,
// and each asserted published review's PR and commit. Independent task/session
// attribution belongs to the workflow authority service, not to a URL.
func (a *adapter) VerifyReviewEvidence(ctx context.Context, ref Ref, token, expectedHeadSHA string, reviewURLs []string) (ReviewEvidence, error) {
	if err := a.validate(ref, token); err != nil {
		return ReviewEvidence{}, err
	}
	if !validSHA(expectedHeadSHA) || len(reviewURLs) == 0 || len(reviewURLs) > 32 {
		return ReviewEvidence{}, deliveryError(ErrInvalid, "validate review evidence", 0)
	}
	wanted := make(map[string]bool, len(reviewURLs))
	for _, raw := range reviewURLs {
		if !a.reviewURLValid(ref, raw) || wanted[raw] {
			return ReviewEvidence{}, deliveryError(ErrInvalid, "validate review URL", 0)
		}
		wanted[raw] = false
	}
	pr, err := a.ReadPR(ctx, ref, token)
	if err != nil {
		return ReviewEvidence{}, err
	}
	if err := currentHead(pr, expectedHeadSHA); err != nil {
		return ReviewEvidence{}, err
	}
	result := ReviewEvidence{PullURL: ref.PullURL, HeadSHA: pr.HeadSHA, PRState: pr.State, ReviewerIDs: map[string]string{}}
	endpoint := a.pullEndpoint(ref) + "/reviews"
	for page := 1; page <= maxReviewPages; page++ {
		param := "per_page"
		if a.kind != GitHub {
			param = "limit"
		}
		status, body, err := a.request(ctx, "list PR reviews", http.MethodGet,
			fmt.Sprintf("%s?%s=%d&page=%d", endpoint, param, reviewPageSize, page), token, nil)
		if err != nil {
			return ReviewEvidence{}, err
		}
		if status != http.StatusOK {
			return ReviewEvidence{}, readStatusError("list PR reviews", status)
		}
		var reviews []providerReview
		if len(body) == 0 || json.Unmarshal(body, &reviews) != nil || reviews == nil {
			return ReviewEvidence{}, deliveryError(ErrAmbiguous, "decode PR reviews", status)
		}
		if len(reviews) == 0 {
			return ReviewEvidence{}, deliveryError(ErrUnverified, "find linked reviews", 0)
		}
		for _, review := range reviews {
			if review.HTMLURL == nil || review.ID == nil || *review.ID <= 0 {
				return ReviewEvidence{}, deliveryError(ErrUnsupported, "identify provider reviews", 0)
			}
			if _, listed := wanted[*review.HTMLURL]; !listed {
				continue
			}
			if review.CommitID == nil || !validSHA(*review.CommitID) || review.State == nil {
				return ReviewEvidence{}, deliveryError(ErrUnsupported, "bind review to commit", 0)
			}
			if !strings.EqualFold(*review.CommitID, expectedHeadSHA) || !publishedReview(*review.State, review.Dismissed) {
				return ReviewEvidence{}, deliveryError(ErrUnverified, "check reviewed revision", 0)
			}
			if a.kind == GitHub && *review.HTMLURL != ref.PullURL+"#pullrequestreview-"+strconv.FormatInt(*review.ID, 10) {
				return ReviewEvidence{}, deliveryError(ErrAmbiguous, "check review identity", 0)
			}
			wanted[*review.HTMLURL] = true
			if review.User != nil && review.User.ID > 0 {
				result.ReviewerIDs[*review.HTMLURL] = string(a.kind) + ":" + strconv.FormatInt(review.User.ID, 10)
			}
		}
		allFound := true
		for _, found := range wanted {
			if !found {
				allFound = false
				break
			}
		}
		if allFound {
			return result, nil
		}
	}
	// A long or provider-truncated listing cannot prove a missing review. The
	// caller must retry or grant an authorized exception; it must not infer one.
	return ReviewEvidence{}, deliveryError(ErrAmbiguous, "review listing incomplete", 0)
}

var _ Provider = (*adapter)(nil)

package vcs

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// AgentOutputMarker labels output posted by agents using a shared integration
// account. The account itself cannot distinguish agent output from human input.
const AgentOutputMarker = "<!-- multica-agent-output -->"

func (p forgejoProvider) ParsePullRequestFeedback(body []byte) (PullRequestFeedbackEvent, error) {
	var d struct {
		Action     string `json:"action"`
		Repository struct {
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Owner    struct {
				Login    string `json:"login"`
				Username string `json:"username"`
			} `json:"owner"`
		} `json:"repository"`
		Issue struct {
			Number      int32           `json:"number"`
			PullRequest json.RawMessage `json:"pull_request"`
			IsPull      bool            `json:"is_pull"`
		} `json:"issue"`
		PullRequest struct {
			Number int32 `json:"number"`
			Head   struct {
				SHA string `json:"sha"`
			} `json:"head"`
		} `json:"pull_request"`
		Comment feedbackObject `json:"comment"`
		Review  feedbackObject `json:"review"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return PullRequestFeedbackEvent{}, err
	}
	object, kind := d.Comment, "comment"
	if object.ID == 0 {
		object, kind = d.Review, "review"
	}
	if kind == "review" && strings.TrimSpace(object.Body) == "" && object.State != "" {
		object.Body = "PR review verdict: " + object.State
	}
	number := d.PullRequest.Number
	if number == 0 && (d.Issue.IsPull || len(d.Issue.PullRequest) > 0 && string(d.Issue.PullRequest) != "null") {
		number = d.Issue.Number
	}
	owner := coalesce(d.Repository.Owner.Username, d.Repository.Owner.Login)
	if owner == "" {
		owner, _, _ = strings.Cut(d.Repository.FullName, "/")
	}
	if number == 0 || owner == "" || d.Repository.Name == "" || object.ID == 0 {
		return PullRequestFeedbackEvent{}, errors.New("feedback is not an identified pull request discussion")
	}
	authorID := ""
	if object.User.ID > 0 {
		authorID = strconv.FormatInt(object.User.ID, 10)
	}
	return PullRequestFeedbackEvent{RepoOwner: owner, RepoName: d.Repository.Name, Number: number, Kind: kind,
		ObjectID: strconv.FormatInt(object.ID, 10), Action: d.Action, Body: object.Body, HTMLURL: object.HTMLURL,
		UpdatedAt: coalesce(object.UpdatedAt, coalesce(object.SubmittedAt, object.CreatedAt)), HeadSHA: d.PullRequest.Head.SHA,
		AuthorLogin: coalesce(object.User.Username, object.User.Login), AuthorID: authorID}, nil
}

type feedbackObject struct {
	State       string `json:"state"`
	ID          int64  `json:"id"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	UpdatedAt   string `json:"updated_at"`
	CreatedAt   string `json:"created_at"`
	SubmittedAt string `json:"submitted_at"`
	User        struct {
		ID       int64  `json:"id"`
		Login    string `json:"login"`
		Username string `json:"username"`
	} `json:"user"`
}

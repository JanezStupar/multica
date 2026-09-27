package vcs

import (
	"net/http"
	"testing"
)

func TestForgejoPullRequestFeedback(t *testing.T) {
	p := forgejoProvider{kind: KindForgejo}
	for _, event := range []string{"issue_comment", "pull_request_review_comment", "pull_request_review", "pull_request_approved", "pull_request_rejected"} {
		h := http.Header{}
		h.Set("X-Gitea-Event", event)
		if p.EventKind(h) != EventPullRequestFeedback {
			t.Fatalf("%s ignored", event)
		}
	}
	ev, err := p.ParsePullRequestFeedback([]byte(`{"action":"created","repository":{"name":"desktopapp","owner":{"username":"trackself"}},"issue":{"number":29,"pull_request":{}},"comment":{"id":435,"body":"I fixed the first issue; please inspect the other one","html_url":"https://forge.example/trackself/desktopapp/pulls/29#issuecomment-435","updated_at":"2026-09-27T10:00:00Z","user":{"login":"Multica"}}}`))
	if err != nil || ev.Number != 29 || ev.ObjectID != "435" || ev.AuthorLogin != "Multica" || ev.Body == "" {
		t.Fatalf("shared-account human input lost: %+v %v", ev, err)
	}
	_, err = p.ParsePullRequestFeedback([]byte(`{"repository":{"name":"desktopapp","owner":{"username":"trackself"}},"issue":{"number":29},"comment":{"id":435,"body":"ordinary issue"}}`))
	if err == nil {
		t.Fatal("ordinary Forgejo issue was treated as PR discussion")
	}
	review, err := p.ParsePullRequestFeedback([]byte(`{"repository":{"name":"desktopapp","owner":{"login":"trackself"}},"pull_request":{"number":29,"head":{"sha":"abc"}},"review":{"id":87,"body":"Please fix this","html_url":"https://forge.example/pulls/29#review-87","submitted_at":"2026-09-27T10:00:00Z"}}`))
	if err != nil || review.Kind != "review" || review.HeadSHA != "abc" {
		t.Fatalf("review input lost: %+v %v", review, err)
	}
}

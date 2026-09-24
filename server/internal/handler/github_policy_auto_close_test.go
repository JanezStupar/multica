package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestWebhookMergedPRLinksButDoesNotAcceptEnrolledIssue(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized")
	}
	ctx := context.Background()
	secret := "enrolled-merge-secret"
	t.Setenv("GITHUB_WEBHOOK_SECRET", secret)
	issueID := dbfx.Issue(t, "Enrolled PR merge", testutil.Cols{
		"status": "in_progress", "number": nextWorkspaceIssueNumber(t),
	})
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	identifier := issueToResponse(issue, testHandler.getIssuePrefix(ctx, issue.WorkspaceID)).Identifier
	sourceID := insertCompleteWorkflowSkill(t, "---\nname: enrolled-pr-workflow\n---\n\nHuman acceptance is required.")
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusCreated)

	const installationID int64 = 99661502
	const repo = "enrolled-workflow-merge"
	const prNumber int32 = 61502
	installation, err := testHandler.Queries.CreateGitHubInstallation(ctx, db.CreateGitHubInstallationParams{
		WorkspaceID: parseUUID(testWorkspaceID), InstallationID: installationID,
		AccountLogin: "workflow-merge-test", AccountType: "User",
	})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Cleanup(t, "DELETE FROM github_installation WHERE id = $1", installation.ID)
	dbfx.Cleanup(t, "DELETE FROM github_pull_request WHERE workspace_id = $1 AND repo_owner = 'acme' AND repo_name = $2 AND pr_number = $3", testWorkspaceID, repo, prNumber)
	dbfx.Cleanup(t, "DELETE FROM issue_pull_request WHERE issue_id = $1", issueID)

	firePullRequestWebhook(t, secret, identifier, installationID, repo, prNumber, "open")
	firePullRequestWebhook(t, secret, identifier, installationID, repo, prNumber, "merged")

	pr, err := testHandler.Queries.GetGitHubPullRequest(ctx, db.GetGitHubPullRequestParams{
		WorkspaceID: parseUUID(testWorkspaceID), RepoOwner: "acme", RepoName: repo, PrNumber: prNumber,
	})
	if err != nil || pr.State != "merged" {
		t.Fatalf("merged PR mirror = %+v, %v", pr, err)
	}
	links, err := testHandler.Queries.ListPullRequestsByIssue(ctx, parseUUID(issueID))
	if err != nil || len(links) != 1 {
		t.Fatalf("PR links = %d, %v; want one", len(links), err)
	}
	updated, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil || updated.Status != "in_progress" || len(updated.WorkflowPolicy) == 0 {
		t.Fatalf("enrolled issue after PR merge = status %q, policy bytes %d, err %v", updated.Status, len(updated.WorkflowPolicy), err)
	}
}

func TestAdvanceIssueToDoneRechecksConcurrentEnrollment(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized")
	}
	ctx := context.Background()
	issueID := dbfx.Issue(t, "Enrolled while webhook row is stale", testutil.Cols{
		"status": "in_progress", "number": nextWorkspaceIssueNumber(t),
	})
	stale, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	sourceID := insertCompleteWorkflowSkill(t, "---\nname: stale-row-workflow\n---\n\nHuman acceptance is required.")
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusCreated)
	testHandler.advanceIssueToDone(ctx, stale, testWorkspaceID)
	if status := issueStatusOf(t, issueID); status != "in_progress" {
		t.Fatalf("stale webhook row auto-accepted enrolled issue: %q", status)
	}
}

func TestAdvanceIssueToDoneRechecksConcurrentLegacyFreeze(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture not initialized")
	}
	ctx := context.Background()
	issueID := dbfx.Issue(t, "Legacy issue frozen after webhook lookup", testutil.Cols{
		"status": "in_progress", "number": nextWorkspaceIssueNumber(t),
	})
	stale, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE issue SET workflow_frozen=true WHERE id=$1`, issueID)
	testHandler.advanceIssueToDone(ctx, stale, testWorkspaceID)
	if status := issueStatusOf(t, issueID); status != "in_progress" {
		t.Fatalf("stale webhook row auto-closed frozen legacy issue: %q", status)
	}
	fresh, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	testHandler.advanceIssueToDone(ctx, fresh, testWorkspaceID)
	if status := issueStatusOf(t, issueID); status != "in_progress" {
		t.Fatalf("webhook auto-closed frozen legacy issue: %q", status)
	}
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestForgejoStoredCommentResolvesMappedHumanForCurrentCandidate(t *testing.T) {
	ctx := context.Background()
	f := setupWorkflowHumanCommentFixtureWithPolicy(t, func(_ string) string {
		return `{"format_version":2,"accepted_status_key":"in_progress","review":{"required":false},"human":{"accept_roles":[],"delivery":"ready"}}`
	}, false)
	box := withVCSBox(t)
	connID := seedVCSConnection(t, ctx, box, "forgejo", "https://forge.example")
	conn, err := testHandler.Queries.GetVCSConnectionByID(ctx, parseUUID(connID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupVCS(ctx, "") })
	dbfx.Cleanup(t, `DELETE FROM vcs_workflow_input WHERE issue_id=$1`, f.issueID)
	pr, err := testHandler.Queries.UpsertVCSPullRequest(ctx, db.UpsertVCSPullRequestParams{
		WorkspaceID: parseUUID(testWorkspaceID), ConnectionID: conn.ID, Provider: "forgejo",
		RepoOwner: "team", RepoName: "repo", PrNumber: 42, Title: "WIP: Feature", State: "draft",
		HtmlUrl: "https://forge.example/team/repo/pulls/42", HeadSha: strings.Repeat("a", 40),
		PrCreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		PrUpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `INSERT INTO issue_vcs_pull_request(issue_id,pull_request_id) VALUES($1,$2)`, f.issueID, pr.ID)
	dbfx.Cleanup(t, `DELETE FROM issue_vcs_pull_request WHERE issue_id=$1`, f.issueID)
	// This webhook is delivered late: the original comment predates the
	// candidate even though its receipt and mirrored head are current.
	oldRevision := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	oldRaw := []byte(fmt.Sprintf(`{"action":"created","repository":{"name":"repo","owner":{"login":"team"}},"issue":{"number":42,"pull_request":{}},"comment":{"id":434,"body":"I approve making this PR ready.","html_url":"https://forge.example/team/repo/pulls/42#issuecomment-434","updated_at":%q,"user":{"id":91,"login":"janez"}}}`, oldRevision))
	oldWebhook := httptest.NewRecorder()
	testHandler.HandleVCSWebhook(oldWebhook, vcsWebhookReq(connID, map[string]string{
		"X-Gitea-Event": "issue_comment", "X-Gitea-Signature": giteaSig(oldRaw),
	}, oldRaw))
	if oldWebhook.Code != http.StatusAccepted {
		t.Fatalf("old webhook status %d: %s", oldWebhook.Code, oldWebhook.Body.String())
	}
	providerRevision := time.Now().UTC().Format(time.RFC3339Nano)
	raw := []byte(fmt.Sprintf(`{"action":"created","repository":{"name":"repo","owner":{"login":"team"}},"issue":{"number":42,"pull_request":{}},"comment":{"id":435,"body":"I approve making this PR ready for review.","html_url":"https://forge.example/team/repo/pulls/42#issuecomment-435","updated_at":%q,"user":{"id":91,"login":"janez"}}}`, providerRevision))
	w := httptest.NewRecorder()
	testHandler.HandleVCSWebhook(w, vcsWebhookReq(connID, map[string]string{
		"X-Gitea-Event": "issue_comment", "X-Gitea-Signature": giteaSig(raw),
	}, raw))
	if w.Code != http.StatusAccepted {
		t.Fatalf("webhook status %d: %s", w.Code, w.Body.String())
	}
	var inputID pgtype.UUID
	var authorID, authorLogin, objectID, revision, body, head string
	var candidateID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT id,provider_author_id,provider_author_login,object_id,
		object_revision,body,head_sha,candidate_id FROM vcs_workflow_input WHERE issue_id=$1 AND object_id='435'`, f.issueID).
		Scan(&inputID, &authorID, &authorLogin, &objectID, &revision, &body, &head, &candidateID); err != nil {
		t.Fatal(err)
	}
	if authorID != "91" || authorLogin != "janez" || objectID != "435" ||
		revision != providerRevision || body != "I approve making this PR ready for review." ||
		head != strings.Repeat("a", 40) || candidateID != f.before.WorkflowCandidateID {
		t.Fatalf("signed provider evidence was not scoped: author=%q/%q object=%q revision=%q head=%q candidate=%v", authorID, authorLogin, objectID, revision, head, candidateID)
	}
	worker := NewWorkflowDeliveryWorker(testHandler)
	if worked, err := worker.RecoverNextVCSWorkflowInput(ctx); err != nil || !worked {
		t.Fatalf("continuation %v %v", worked, err)
	}
	claim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claim.AgentID != uuidToString(f.writer) {
		t.Fatalf("wrong retained writer: %s", claim.AgentID)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(claim.ID)); err != nil {
		t.Fatal(err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	actor := service.WorkflowActor{Type: "agent", ID: uuidToString(f.writer), SourceTaskID: claim.ID}
	in := service.WorkflowCommentAcceptanceInput{CandidateID: uuidToString(candidateID), ExpectedRevision: issue.Revision,
		Source: "forgejo", SourceID: uuidToString(inputID), Action: "ready", Reason: "The mapped human approved PR readiness."}
	svc := testHandler.workflowAuthorityService()
	if _, err := svc.AcceptWorkflowComment(ctx, issue.WorkspaceID, issue.ID, actor, in); !errors.Is(err, service.ErrWorkflowAuthorityForbidden) {
		t.Fatalf("unmapped provider actor gained human authority: %v", err)
	}
	mappings, _ := json.Marshal([]map[string]string{{"provider_user_id": "91", "member_id": testUserID}})
	dbfx.Exec(t, `UPDATE vcs_connection SET workflow_approvers=$2::jsonb WHERE id=$1`, conn.ID, string(mappings))
	withoutGrant, err := svc.ReadState(ctx, issue.WorkspaceID, issue.ID, actor)
	if err != nil || withoutGrant.AvailableActions.AcceptComment {
		t.Fatalf("ungranted provider comment appeared actionable: %+v %v", withoutGrant.AvailableActions, err)
	}
	if _, err := svc.GrantException(ctx, issue.WorkspaceID, issue.ID,
		service.WorkflowActor{Type: "member", ID: testUserID}, service.WorkflowExceptionInput{
			CandidateID: uuidToString(candidateID), ExpectedRevision: issue.Revision,
			Scope: "acceptance", GrantDetails: map[string]any{"human_actor_id": testUserID},
			Reason:       "Allow this mapped human to approve the current candidate.",
			Consequences: "Approval may make the current PR ready.",
		}); err != nil {
		t.Fatal(err)
	}
	issue, err = testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedRevision = issue.Revision
	withGrant, err := svc.ReadState(ctx, issue.WorkspaceID, issue.ID, actor)
	if err != nil || !withGrant.AvailableActions.AcceptComment {
		t.Fatalf("candidate acceptance grant absent from provider affordance: %+v %v", withGrant.AvailableActions, err)
	}
	var oldInputID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM vcs_workflow_input WHERE issue_id=$1 AND object_id='434'`, f.issueID).Scan(&oldInputID); err != nil {
		t.Fatal(err)
	}
	oldInput := in
	oldInput.SourceID = uuidToString(oldInputID)
	if _, err := svc.AcceptWorkflowComment(ctx, issue.WorkspaceID, issue.ID, actor, oldInput); !errors.Is(err, service.ErrWorkflowAuthorityForbidden) {
		t.Fatalf("late pre-candidate comment gained authority: %v", err)
	}
	state, err := svc.AcceptWorkflowComment(ctx, issue.WorkspaceID, issue.ID, actor, in)
	if err != nil || state != "accepted" {
		t.Fatalf("mapped comment did not record human decision: %s %v", state, err)
	}
	var decisionActorType, decisionActorID, action string
	var snapshot []byte
	if err := testPool.QueryRow(ctx, `SELECT actor_type,actor_id::text,authority_snapshot->>'delivery_action',
		authority_snapshot->'comment_authority' FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`,
		f.issueID).Scan(&decisionActorType, &decisionActorID, &action, &snapshot); err != nil {
		t.Fatal(err)
	}
	if decisionActorType != "member" || decisionActorID != testUserID || action != "ready" ||
		!strings.Contains(string(snapshot), uuidToString(inputID)) || !strings.Contains(string(snapshot), "I approve making this PR ready") {
		t.Fatalf("wrong human decision attribution or source snapshot: %s %s %s %s", decisionActorType, decisionActorID, action, snapshot)
	}
	// A subsequent provider deletion is durable feedback, even though its body
	// is empty. It must remain pending so delivery can pause for reconciliation.
	deleteRevision := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	deleteRaw := []byte(fmt.Sprintf(`{"action":"deleted","repository":{"name":"repo","owner":{"login":"team"}},"issue":{"number":42,"pull_request":{}},"comment":{"id":435,"body":"","html_url":"https://forge.example/team/repo/pulls/42#issuecomment-435","updated_at":%q,"user":{"id":91,"login":"janez"}}}`, deleteRevision))
	deletedWebhook := httptest.NewRecorder()
	testHandler.HandleVCSWebhook(deletedWebhook, vcsWebhookReq(connID, map[string]string{
		"X-Gitea-Event": "issue_comment", "X-Gitea-Signature": giteaSig(deleteRaw),
	}, deleteRaw))
	if deletedWebhook.Code != http.StatusAccepted {
		t.Fatalf("delete webhook status %d: %s", deletedWebhook.Code, deletedWebhook.Body.String())
	}
	var pending bool
	if err := testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vcs_workflow_input
		WHERE issue_id=$1 AND object_id='435' AND object_action='deleted' AND processed_at IS NULL)`, f.issueID).Scan(&pending); err != nil || !pending {
		t.Fatalf("deleted approval did not remain pending for delivery guard: pending=%v error=%v", pending, err)
	}
}

func TestForgejoClearedEditWithdrawsStoredApproval(t *testing.T) {
	ctx := context.Background()
	f := setupWorkflowHumanCommentFixtureWithPolicy(t, func(_ string) string {
		return `{"format_version":2,"accepted_status_key":"in_progress","review":{"required":false}}`
	}, false)
	box := withVCSBox(t)
	connID := seedVCSConnection(t, ctx, box, "forgejo", "https://forge.example")
	conn, err := testHandler.Queries.GetVCSConnectionByID(ctx, parseUUID(connID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupVCS(ctx, "") })
	dbfx.Cleanup(t, `DELETE FROM vcs_workflow_input WHERE issue_id=$1`, f.issueID)
	pr, err := testHandler.Queries.UpsertVCSPullRequest(ctx, db.UpsertVCSPullRequestParams{
		WorkspaceID: parseUUID(testWorkspaceID), ConnectionID: conn.ID, Provider: "forgejo",
		RepoOwner: "team", RepoName: "repo", PrNumber: 42, Title: "WIP: Feature", State: "draft",
		HtmlUrl: "https://forge.example/team/repo/pulls/42", HeadSha: strings.Repeat("a", 40),
		PrCreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		PrUpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `INSERT INTO issue_vcs_pull_request(issue_id,pull_request_id) VALUES($1,$2)`, f.issueID, pr.ID)
	dbfx.Cleanup(t, `DELETE FROM issue_vcs_pull_request WHERE issue_id=$1`, f.issueID)
	mappings, _ := json.Marshal([]map[string]string{{"provider_user_id": "91", "member_id": testUserID}})
	dbfx.Exec(t, `UPDATE vcs_connection SET workflow_approvers=$2::jsonb WHERE id=$1`, conn.ID, string(mappings))
	send := func(action, body, revision string) {
		t.Helper()
		raw := []byte(fmt.Sprintf(`{"action":%q,"repository":{"name":"repo","owner":{"login":"team"}},"issue":{"number":42,"pull_request":{}},"comment":{"id":435,"body":%q,"html_url":"https://forge.example/team/repo/pulls/42#issuecomment-435","updated_at":%q,"user":{"id":91,"login":"janez"}}}`, action, body, revision))
		w := httptest.NewRecorder()
		testHandler.HandleVCSWebhook(w, vcsWebhookReq(connID, map[string]string{
			"X-Gitea-Event": "issue_comment", "X-Gitea-Signature": giteaSig(raw),
		}, raw))
		if w.Code != http.StatusAccepted {
			t.Fatalf("%s webhook status %d: %s", action, w.Code, w.Body.String())
		}
	}
	send("created", "I approve making this PR ready.", time.Now().UTC().Format(time.RFC3339Nano))
	var inputID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM vcs_workflow_input WHERE issue_id=$1 AND object_id='435'`, f.issueID).Scan(&inputID); err != nil {
		t.Fatal(err)
	}
	worker := NewWorkflowDeliveryWorker(testHandler)
	if worked, err := worker.RecoverNextVCSWorkflowInput(ctx); err != nil || !worked {
		t.Fatalf("continuation %v %v", worked, err)
	}
	claim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(claim.ID)); err != nil {
		t.Fatal(err)
	}
	send("edited", "", time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano))
	var clearedPending bool
	if err := testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vcs_workflow_input
		WHERE issue_id=$1 AND object_id='435' AND object_action='edited' AND body='' AND processed_at IS NULL)`,
		f.issueID).Scan(&clearedPending); err != nil || !clearedPending {
		t.Fatalf("cleared edit did not become pending withdrawal evidence: pending=%v error=%v", clearedPending, err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	actor := service.WorkflowActor{Type: "agent", ID: uuidToString(f.writer), SourceTaskID: claim.ID}
	_, err = testHandler.workflowAuthorityService().AcceptWorkflowComment(ctx, issue.WorkspaceID, issue.ID, actor,
		service.WorkflowCommentAcceptanceInput{CandidateID: uuidToString(issue.WorkflowCandidateID),
			ExpectedRevision: issue.Revision, Source: "forgejo", SourceID: uuidToString(inputID),
			Action: "ready", Reason: "This is the now-cleared approval."})
	if !errors.Is(err, service.ErrWorkflowAuthorityForbidden) {
		t.Fatalf("cleared approval retained authority: %v", err)
	}
}

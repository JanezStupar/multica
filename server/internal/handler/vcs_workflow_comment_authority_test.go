package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/vcs"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestForgejoHeadWebhookRecordsProviderRevisionForApprovalProof(t *testing.T) {
	ctx := context.Background()
	f, conn, pr, _ := setupKnownSupersededVCSHead(t)
	headRevision := time.Now().UTC().Add(time.Second).Truncate(time.Second)
	newHead := strings.Repeat("c", 40)
	if err := testHandler.mirrorVCSPullRequest(ctx, conn, vcs.PullRequestEvent{
		RepoOwner: "team", RepoName: "repo", Number: 42, Title: "Feature", State: "draft",
		HTMLURL: pr.HtmlUrl, HeadSHA: newHead, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		UpdatedAt: headRevision.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	var revision string
	var revisionAt time.Time
	if err := testPool.QueryRow(ctx, `SELECT object_revision,object_revision_at FROM vcs_workflow_input
		WHERE issue_id=$1 AND pull_request_id=$2 AND kind='head' AND head_sha=$3`, f.issueID, pr.ID, newHead).
		Scan(&revision, &revisionAt); err != nil {
		t.Fatal(err)
	}
	if revision != headRevision.Format(time.RFC3339Nano) || !revisionAt.Equal(headRevision) {
		t.Fatalf("head provider revision was lost: revision=%q at=%s", revision, revisionAt)
	}
}

func setForgejoCandidatePRSet(t *testing.T, candidateID pgtype.UUID, update func([]service.HandoffCandidate) []service.HandoffCandidate) {
	t.Helper()
	ctx := context.Background()
	var version, scope string
	var raw []byte
	if err := testPool.QueryRow(ctx, `SELECT policy_version,scope_digest,pr_set FROM issue_workflow_candidate WHERE id=$1`,
		candidateID).Scan(&version, &scope, &raw); err != nil {
		t.Fatal(err)
	}
	var prs []service.HandoffCandidate
	if err := json.Unmarshal(raw, &prs); err != nil {
		t.Fatal(err)
	}
	prSet, err := json.Marshal(update(prs))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(struct {
		PolicyVersion string          `json:"policy_version"`
		ScopeDigest   string          `json:"scope_digest"`
		PRSet         json.RawMessage `json:"pr_set"`
	}{version, scope, prSet})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	dbfx.Exec(t, `UPDATE issue_workflow_candidate SET pr_set=$2::jsonb,digest=$3 WHERE id=$1`, candidateID,
		string(prSet), hex.EncodeToString(sum[:]))
}

// The human can approve a concrete PR head while the writer is still preparing
// its handoff. The webhook is stored without a candidate, then the same head
// becomes the reviewed candidate before the retained writer receives it.
func setupForgejoPreCandidateApproval(t *testing.T) (workflowHumanCommentFixture, db.VcsConnection, db.VcsPullRequest, pgtype.UUID, pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	f := setupWorkflowHumanCommentFixtureWithPolicy(t, func(_ string) string {
		return `{"format_version":2,"accepted_status_key":"in_progress","review":{"required":true},"human":{"accept_roles":["owner"],"delivery":"ready"}}`
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
	dbfx.Exec(t, `UPDATE issue SET workflow_candidate_id=NULL WHERE id=$1`, f.issueID)
	headRevision := time.Now().UTC().Add(-time.Minute)
	if err := testHandler.recordVCSInput(ctx, conn, pr.ID, "head", "pre-approval-head", "PR head changed to "+pr.HeadSha,
		pr.HtmlUrl, pr.HeadSha, vcsFeedbackMeta{revision: headRevision.Format(time.RFC3339Nano), revisionAt: &headRevision}); err != nil {
		t.Fatal(err)
	}
	providerRevision := time.Now().UTC().Format(time.RFC3339Nano)
	raw := []byte(fmt.Sprintf(`{"action":"created","repository":{"name":"repo","owner":{"login":"team"}},"issue":{"number":42,"pull_request":{}},"comment":{"id":495,"body":"I approve making this PR ready.","html_url":"https://forge.example/team/repo/pulls/42#issuecomment-495","updated_at":%q,"user":{"id":91,"login":"janez"}}}`, providerRevision))
	w := httptest.NewRecorder()
	testHandler.HandleVCSWebhook(w, vcsWebhookReq(connID, map[string]string{
		"X-Gitea-Event": "issue_comment", "X-Gitea-Signature": giteaSig(raw),
	}, raw))
	if w.Code != http.StatusAccepted {
		t.Fatalf("pre-candidate webhook status %d: %s", w.Code, w.Body.String())
	}
	var inputID, recordedCandidate pgtype.UUID
	var recordedAt, revisionAt time.Time
	if err := testPool.QueryRow(ctx, `SELECT id,candidate_id,created_at,object_revision_at FROM vcs_workflow_input
		WHERE issue_id=$1 AND object_id='495'`, f.issueID).Scan(&inputID, &recordedCandidate, &recordedAt, &revisionAt); err != nil {
		t.Fatal(err)
	}
	if recordedCandidate.Valid {
		t.Fatal("pre-candidate input was bound to a candidate")
	}
	candidateID := dbid.NewV7()
	dbfx.Exec(t, `INSERT INTO issue_workflow_candidate
		(id,workspace_id,issue_id,policy_version,digest,scope_digest,source_handoff_id,source_task_id,writer_task_id,pr_set)
		SELECT $2,workspace_id,issue_id,policy_version,digest,scope_digest,source_handoff_id,source_task_id,writer_task_id,pr_set
		FROM issue_workflow_candidate WHERE id=$1`, f.before.WorkflowCandidateID, candidateID)
	dbfx.Exec(t, `UPDATE issue SET workflow_candidate_id=$2 WHERE id=$1`, f.issueID, candidateID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET context=jsonb_set(context,'{workflow_handoff,candidate_id}',to_jsonb($2::text))
		WHERE id=$1`, f.coordinatorTaskID, uuidToString(candidateID))
	var candidateAt time.Time
	if err := testPool.QueryRow(ctx, `SELECT created_at FROM issue_workflow_candidate WHERE id=$1`, candidateID).Scan(&candidateAt); err != nil {
		t.Fatal(err)
	}
	if !recordedAt.Before(candidateAt) || !revisionAt.Before(candidateAt) {
		t.Fatalf("fixture did not record approval before candidate: input=%s revision=%s candidate=%s", recordedAt, revisionAt, candidateAt)
	}
	dbfx.Insert(t, "issue_workflow_review", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": f.issueID, "candidate_id": candidateID,
		"reviewer_task_id": f.coordinatorTaskID, "verdict": "pass",
		"pr_review_urls": testutil.Raw(`'["https://forge.example/team/repo/pulls/42#issuecomment-review"]'::jsonb`),
	})
	worker := NewWorkflowDeliveryWorker(testHandler)
	if worked, err := worker.RecoverNextVCSWorkflowInput(ctx); err != nil || !worked {
		t.Fatalf("reconcile already-candidate head: %v %v", worked, err)
	}
	if worked, err := worker.RecoverNextVCSWorkflowInput(ctx); err != nil || !worked {
		t.Fatalf("pre-candidate approval continuation: %v %v", worked, err)
	}
	claim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claim.AgentID != uuidToString(f.writer) {
		t.Fatalf("wrong retained writer: %s", claim.AgentID)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(claim.ID)); err != nil {
		t.Fatal(err)
	}
	return f, conn, pr, candidateID, inputID
}

func TestForgejoApprovalBeforeCandidateRegistration(t *testing.T) {
	ctx := context.Background()
	f, _, _, candidateID, inputID := setupForgejoPreCandidateApproval(t)
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	var taskID pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT task_id FROM vcs_workflow_input WHERE id=$1`, inputID).Scan(&taskID); err != nil || !taskID.Valid {
		t.Fatalf("approval was not delivered: task=%v err=%v", taskID, err)
	}
	actor := service.WorkflowActor{Type: "agent", ID: uuidToString(f.writer), SourceTaskID: uuidToString(taskID)}
	svc := testHandler.workflowAuthorityService()
	svc.ReviewVerifier = func(_ context.Context, evidence service.WorkflowReviewEvidenceInput) error {
		if len(evidence.PRs) != 1 || evidence.PRs[0].ExpectedHeadSHA != strings.Repeat("a", 40) ||
			len(evidence.ReviewURLs) != 1 {
			t.Fatalf("wrong independent review evidence: %+v", evidence)
		}
		return nil
	}
	view, err := svc.ReadState(ctx, issue.WorkspaceID, issue.ID, actor)
	if err != nil || !view.AvailableActions.AcceptComment {
		t.Fatalf("pre-candidate approval hidden from available actions: %+v %v", view.AvailableActions, err)
	}
	state, err := svc.AcceptWorkflowComment(ctx, issue.WorkspaceID, issue.ID, actor,
		service.WorkflowCommentAcceptanceInput{CandidateID: uuidToString(candidateID), ExpectedRevision: issue.Revision,
			Source: "forgejo", SourceID: uuidToString(inputID), Action: "ready", Reason: "The mapped human approved this exact PR head."})
	if err != nil || state != "accepted" {
		t.Fatalf("pre-candidate approval rejected: state=%q err=%v", state, err)
	}
	var actorType, deliveryAction, sourceCandidate string
	if err := testPool.QueryRow(ctx, `SELECT actor_type,authority_snapshot->>'delivery_action',
		authority_snapshot->'comment_authority'->>'candidate_id'
		FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, f.issueID).Scan(&actorType, &deliveryAction, &sourceCandidate); err != nil {
		t.Fatal(err)
	}
	if actorType != "member" || deliveryAction != "ready" || sourceCandidate != uuidToString(candidateID) {
		t.Fatalf("approval escaped mapped-human ready-only scope: actor=%q action=%q candidate=%q", actorType, deliveryAction, sourceCandidate)
	}
}

func TestForgejoPreCandidateApprovalRejectsChangedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, workflowHumanCommentFixture, db.VcsConnection, db.VcsPullRequest, pgtype.UUID, pgtype.UUID)
	}{
		{name: "different head", change: func(t *testing.T, _ workflowHumanCommentFixture, _ db.VcsConnection, _ db.VcsPullRequest, _ pgtype.UUID, inputID pgtype.UUID) {
			dbfx.Exec(t, `UPDATE vcs_workflow_input SET head_sha=$2 WHERE id=$1`, inputID, strings.Repeat("b", 40))
		}},
		{name: "different PR", change: func(t *testing.T, _ workflowHumanCommentFixture, conn db.VcsConnection, _ db.VcsPullRequest, _ pgtype.UUID, inputID pgtype.UUID) {
			pr, err := testHandler.Queries.UpsertVCSPullRequest(context.Background(), db.UpsertVCSPullRequestParams{
				WorkspaceID: parseUUID(testWorkspaceID), ConnectionID: conn.ID, Provider: "forgejo",
				RepoOwner: "team", RepoName: "repo", PrNumber: 43, Title: "Other PR", State: "draft",
				HtmlUrl: "https://forge.example/team/repo/pulls/43", HeadSha: strings.Repeat("a", 40),
				PrCreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
				PrUpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			dbfx.Exec(t, `UPDATE vcs_workflow_input SET pull_request_id=$2 WHERE id=$1`, inputID, pr.ID)
		}},
		{name: "different bound candidate", change: func(t *testing.T, f workflowHumanCommentFixture, _ db.VcsConnection, _ db.VcsPullRequest, _ pgtype.UUID, inputID pgtype.UUID) {
			dbfx.Exec(t, `UPDATE vcs_workflow_input SET candidate_id=$2 WHERE id=$1`, inputID, f.before.WorkflowCandidateID)
		}},
		{name: "intervening candidate at equal timestamp", change: func(t *testing.T, _ workflowHumanCommentFixture, _ db.VcsConnection, _ db.VcsPullRequest, candidateID, _ pgtype.UUID) {
			dbfx.Exec(t, `INSERT INTO issue_workflow_candidate
				(id,workspace_id,issue_id,policy_version,digest,scope_digest,source_handoff_id,source_task_id,writer_task_id,pr_set,created_at)
				SELECT $2,workspace_id,issue_id,policy_version,digest,scope_digest,source_handoff_id,source_task_id,writer_task_id,pr_set,created_at
				FROM issue_workflow_candidate WHERE id=$1`, candidateID, dbid.NewV7())
		}},
		{name: "candidate added another PR", change: func(t *testing.T, f workflowHumanCommentFixture, conn db.VcsConnection, _ db.VcsPullRequest, candidateID, _ pgtype.UUID) {
			pr, err := testHandler.Queries.UpsertVCSPullRequest(context.Background(), db.UpsertVCSPullRequestParams{
				WorkspaceID: parseUUID(testWorkspaceID), ConnectionID: conn.ID, Provider: "forgejo",
				RepoOwner: "team", RepoName: "repo", PrNumber: 43, Title: "Additional PR", State: "draft",
				HtmlUrl: "https://forge.example/team/repo/pulls/43", HeadSha: strings.Repeat("b", 40),
				PrCreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
				PrUpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
			if err != nil {
				t.Fatal(err)
			}
			dbfx.Exec(t, `INSERT INTO issue_vcs_pull_request(issue_id,pull_request_id) VALUES($1,$2)`, f.issueID, pr.ID)
			dbfx.Cleanup(t, `DELETE FROM issue_vcs_pull_request WHERE issue_id=$1 AND pull_request_id=$2`, f.issueID, pr.ID)
			setForgejoCandidatePRSet(t, candidateID, func(prs []service.HandoffCandidate) []service.HandoffCandidate {
				return append(prs, service.HandoffCandidate{RepositoryURL: "https://forge.example/team/repo",
					PRURL: pr.HtmlUrl, Branch: "feature/other", CommitSHA: pr.HeadSha, Draft: true})
			})
		}},
		{name: "no recorded head", change: func(t *testing.T, f workflowHumanCommentFixture, _ db.VcsConnection, _ db.VcsPullRequest, _ pgtype.UUID, _ pgtype.UUID) {
			dbfx.Exec(t, `DELETE FROM vcs_workflow_input WHERE issue_id=$1 AND kind='head'`, f.issueID)
		}},
		{name: "head and approval same provider time", change: func(t *testing.T, f workflowHumanCommentFixture, _ db.VcsConnection, _ db.VcsPullRequest, _ pgtype.UUID, inputID pgtype.UUID) {
			dbfx.Exec(t, `UPDATE vcs_workflow_input SET object_revision_at=(SELECT object_revision_at FROM vcs_workflow_input WHERE id=$2)
				WHERE issue_id=$1 AND kind='head'`, f.issueID, inputID)
		}},
		{name: "new head after old approval", change: func(t *testing.T, f workflowHumanCommentFixture, conn db.VcsConnection, pr db.VcsPullRequest, candidateID, inputID pgtype.UUID) {
			newHead := strings.Repeat("b", 40)
			setForgejoCandidatePRSet(t, candidateID, func(prs []service.HandoffCandidate) []service.HandoffCandidate {
				prs[0].CommitSHA = newHead
				return prs
			})
			dbfx.Exec(t, `UPDATE vcs_pull_request SET head_sha=$2 WHERE id=$1`, pr.ID, newHead)
			dbfx.Exec(t, `UPDATE vcs_workflow_input SET head_sha=$2 WHERE id=$1`, inputID, newHead)
			var commentRevision time.Time
			if err := testPool.QueryRow(context.Background(), `SELECT object_revision_at FROM vcs_workflow_input WHERE id=$1`, inputID).Scan(&commentRevision); err != nil {
				t.Fatal(err)
			}
			newRevision := commentRevision.Add(time.Second)
			if err := testHandler.recordVCSInput(context.Background(), conn, pr.ID, "head", "post-approval-head",
				"PR head changed to "+newHead, pr.HtmlUrl, newHead,
				vcsFeedbackMeta{revision: newRevision.Format(time.RFC3339Nano), revisionAt: &newRevision}); err != nil {
				t.Fatal(err)
			}
			// The old webhook was delayed until after this newer head was mirrored.
			dbfx.Exec(t, `UPDATE vcs_workflow_input SET created_at=(SELECT created_at FROM vcs_workflow_input WHERE id=$2)
				WHERE issue_id=$1 AND event_key='post-approval-head'`, f.issueID, inputID)
		}},
		{name: "independent review missing", change: func(t *testing.T, f workflowHumanCommentFixture, _ db.VcsConnection, _ db.VcsPullRequest, _ pgtype.UUID, _ pgtype.UUID) {
			dbfx.Exec(t, `DELETE FROM issue_workflow_review WHERE issue_id=$1`, f.issueID)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f, conn, pr, candidateID, inputID := setupForgejoPreCandidateApproval(t)
			tc.change(t, f, conn, pr, candidateID, inputID)
			issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
			if err != nil {
				t.Fatal(err)
			}
			var taskID pgtype.UUID
			if err := testPool.QueryRow(ctx, `SELECT task_id FROM vcs_workflow_input WHERE id=$1`, inputID).Scan(&taskID); err != nil {
				t.Fatal(err)
			}
			actor := service.WorkflowActor{Type: "agent", ID: uuidToString(f.writer), SourceTaskID: uuidToString(taskID)}
			svc := testHandler.workflowAuthorityService()
			svc.ReviewVerifier = func(context.Context, service.WorkflowReviewEvidenceInput) error { return nil }
			state, err := svc.AcceptWorkflowComment(ctx, issue.WorkspaceID, issue.ID, actor,
				service.WorkflowCommentAcceptanceInput{CandidateID: uuidToString(candidateID), ExpectedRevision: issue.Revision,
					Source: "forgejo", SourceID: uuidToString(inputID), Action: "ready", Reason: "Old approval cannot authorize changed evidence."})
			if err == nil {
				t.Fatalf("changed evidence accepted: state=%q", state)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, f.issueID); got != 0 {
				t.Fatalf("changed evidence recorded %d acceptances", got)
			}
		})
	}
}

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

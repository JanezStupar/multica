package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/integrations/vcs"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVCSWorkflowFeedbackResumesWriterAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	f := setupWorkflowHumanCommentFixtureWithPolicy(t, func(_ string) string { return `{"format_version":2,"accepted_status_key":"in_progress"}` }, false)
	box := withVCSBox(t)
	connID := seedVCSConnection(t, ctx, box, "forgejo", "https://forge.example")
	conn, err := testHandler.Queries.GetVCSConnectionByID(ctx, parseUUID(connID))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupVCS(ctx, "") })
	dbfx.Cleanup(t, `DELETE FROM vcs_workflow_input WHERE issue_id=$1`, f.issueID)
	pr, err := testHandler.Queries.UpsertVCSPullRequest(ctx, db.UpsertVCSPullRequestParams{WorkspaceID: parseUUID(testWorkspaceID), ConnectionID: conn.ID, Provider: "forgejo", RepoOwner: "team", RepoName: "repo", PrNumber: 42, Title: "Feature", State: "draft", HtmlUrl: "https://forge.example/team/repo/pulls/42", HeadSha: strings.Repeat("a", 40), PrCreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}, PrUpdatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `INSERT INTO issue_vcs_pull_request(issue_id,pull_request_id) VALUES($1,$2)`, f.issueID, pr.ID)
	dbfx.Cleanup(t, `DELETE FROM issue_vcs_pull_request WHERE issue_id=$1`, f.issueID)
	raw := []byte(`{"action":"created","repository":{"name":"repo","owner":{"login":"team"}},"issue":{"number":42,"pull_request":{}},"comment":{"id":435,"body":"I pushed a fix. Please inspect the remaining concern.","html_url":"https://forge.example/team/repo/pulls/42#issuecomment-435","updated_at":"2026-09-27T12:00:00Z","user":{"login":"Multica"}}}`)
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		testHandler.HandleVCSWebhook(w, vcsWebhookReq(connID, map[string]string{"X-Gitea-Event": "issue_comment", "X-Gitea-Signature": giteaSig(raw)}, raw))
		if w.Code != http.StatusAccepted {
			t.Fatalf("webhook %d: %s", w.Code, w.Body.String())
		}
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM vcs_workflow_input WHERE issue_id=$1`, f.issueID); got != 1 {
		t.Fatalf("delivery not deduplicated: %d", got)
	}
	if err = testHandler.recordVCSDiscussion(ctx, conn, vcs.PullRequestFeedbackEvent{RepoOwner: "team", RepoName: "repo", Number: 42, Kind: "comment", ObjectID: "437", Body: "Another concern", HTMLURL: "https://forge.example/team/repo/pulls/42#issuecomment-437", UpdatedAt: "2026-09-27T12:01:00Z", AuthorLogin: "Multica"}); err != nil {
		t.Fatal(err)
	}
	if err = testHandler.recordVCSInput(ctx, conn, pr.ID, "head", "batch-head", "PR head changed to "+strings.Repeat("b", 40), pr.HtmlUrl, strings.Repeat("b", 40)); err != nil {
		t.Fatal(err)
	}
	worker := NewWorkflowDeliveryWorker(testHandler)
	dbfx.Exec(t, `UPDATE issue SET workflow_frozen=true WHERE id=$1`, f.issueID)
	if err = testHandler.recordVCSDiscussion(ctx, conn, vcs.PullRequestFeedbackEvent{RepoOwner: "team", RepoName: "repo", Number: 42, Kind: "comment", ObjectID: "438", Body: "Feedback while temporarily frozen", HTMLURL: "https://forge.example/team/repo/pulls/42#issuecomment-438", UpdatedAt: "2026-09-27T12:02:00Z", AuthorLogin: "Multica"}); err != nil {
		t.Fatal(err)
	}
	if worked, err := worker.RecoverNextVCSWorkflowInput(ctx); err != nil || worked {
		t.Fatalf("frozen work dispatched: %v %v", worked, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM vcs_workflow_input WHERE issue_id=$1 AND processed_at IS NULL`, f.issueID); got != 4 {
		t.Fatalf("frozen feedback lost: %d", got)
	}
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('multica.workflow_migration','on',true)`); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE issue SET workflow_frozen=false,workflow_migrated_at=now() WHERE id=$1`, f.issueID); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	worked, err := worker.RecoverNextVCSWorkflowInput(ctx)
	if err != nil || !worked {
		t.Fatalf("continuation: %v %v", worked, err)
	}
	var taskID, writer, rerun, originator pgtype.UUID
	var note string
	if err = testPool.QueryRow(ctx, `SELECT task.id,task.agent_id,task.rerun_of_task_id,task.originator_user_id,task.handoff_note FROM agent_task_queue task JOIN vcs_workflow_input input ON input.task_id=task.id WHERE input.issue_id=$1`, f.issueID).Scan(&taskID, &writer, &rerun, &originator, &note); err != nil {
		t.Fatal(err)
	}
	if writer != f.writer || rerun != parseUUID(f.writerTaskID) || originator != parseUUID(testUserID) || !strings.Contains(note, "remaining concern") || !strings.Contains(note, vcs.AgentOutputMarker) {
		t.Fatalf("wrong retained writer input: writer=%v rerun=%v note=%s", writer, rerun, note)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND status='queued'`, f.issueID); got != 1 {
		t.Fatalf("coalesced inputs created %d runs", got)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM vcs_workflow_input WHERE issue_id=$1 AND task_id=$2`, f.issueID, taskID); got != 4 {
		t.Fatalf("input evidence not preserved: %d", got)
	}
	if !strings.Contains(note, "Feedback while temporarily frozen") || !strings.Contains(note, "Another concern") || !strings.Contains(note, strings.Repeat("b", 40)) {
		t.Fatal("coalesced comment/head omitted")
	}
	var assignee string
	var candidate pgtype.UUID
	if err = testPool.QueryRow(ctx, `SELECT assignee_type,workflow_candidate_id FROM issue WHERE id=$1`, f.issueID).Scan(&assignee, &candidate); err != nil {
		t.Fatal(err)
	}
	if assignee != "member" || candidate != f.before.WorkflowCandidateID {
		t.Fatal("provider input altered candidate/acceptance or human owner")
	}
	if worked, err = worker.RecoverNextVCSWorkflowInput(ctx); err != nil || worked {
		t.Fatalf("duplicate continuation %v %v", worked, err)
	}
	claim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claim.ID != uuidToString(taskID) || claim.PriorSessionID != "candidate-writer-session" {
		t.Fatalf("writer session not retained: id=%s session=%s", claim.ID, claim.PriorSessionID)
	}
	if _, err = testHandler.TaskService.StartTask(ctx, taskID); err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='failed',completed_at=now(),session_id='failed-feedback-turn-session',work_dir='/tmp/workflow-candidate-writer',error='Transient interruption' WHERE id=$1`, taskID)
	retry, err := testHandler.Queries.CreateRetryTask(ctx, db.CreateRetryTaskParams{ID: taskID, NewTaskID: dbid.NewV7()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(retry.HandoffNote.String, "remaining concern") || !strings.Contains(retry.HandoffNote.String, "Another concern") {
		t.Fatal("retry discarded provider feedback input")
	}
	retryClaim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if retryClaim.ID != uuidToString(retry.ID) || retryClaim.PriorSessionID != "failed-feedback-turn-session" {
		t.Fatalf("retry did not retain actual interrupted turn: id=%s session=%s", retryClaim.ID, retryClaim.PriorSessionID)
	}
	if _, err = testHandler.TaskService.StartTask(ctx, retry.ID); err != nil {
		t.Fatal(err)
	}
	taskID = retry.ID
	// Human ownership is not a reason to refuse the resumed writer's next
	// independent review handoff.
	wakeups := service.IssueWakeupService{Tasks: testHandler.TaskService}
	_, err = wakeups.CreateHandoff(ctx, parseUUID(f.issueID), parseUUID(testUserID), taskID, service.HandoffInput{
		RequestKey: uuidToString(dbid.NewV7()), OutgoingTaskID: uuidToString(taskID), AssigneeType: "agent", AssigneeID: f.coordinatorID,
		Status: "in_review", ContextMode: "fresh", Instruction: "Independently review the reconciled candidate.",
		Candidates: []service.HandoffCandidate{{RepositoryURL: "https://forge.example/team/repo", PRURL: pr.HtmlUrl, Branch: "feature/feedback", CommitSHA: strings.Repeat("a", 40), Draft: true}}, EvidenceURLs: []string{},
	})
	if err != nil {
		t.Fatalf("provider writer continuation cannot hand off to review: %v", err)
	}
	// Agent output is marked, not filtered by the shared integration account.
	payload := vcs.PullRequestFeedbackEvent{RepoOwner: "team", RepoName: "repo", Number: 42, Kind: "comment", ObjectID: "436", Body: "fixed " + vcs.AgentOutputMarker, HTMLURL: "https://forge.example/team/repo/pulls/42#issuecomment-436"}
	if err = testHandler.recordVCSDiscussion(ctx, conn, payload); err != nil {
		t.Fatal(err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM vcs_workflow_input WHERE issue_id=$1`, f.issueID); got != 4 {
		t.Fatal("agent output produced a feedback loop")
	}
	// A pushed head is queued as reconciliation, never accepted by the webhook.
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
	if err = testHandler.recordVCSInput(ctx, conn, pr.ID, "head", "new-head", "PR head changed", pr.HtmlUrl, strings.Repeat("b", 40)); err != nil {
		t.Fatal(err)
	}
	if worked, err = worker.RecoverNextVCSWorkflowInput(ctx); err != nil || worked {
		t.Fatalf("head feedback raced pending review transfer %v %v", worked, err)
	}
	var heads []byte
	if err = testPool.QueryRow(ctx, `SELECT pr_set FROM issue_workflow_candidate WHERE id=$1`, candidate).Scan(&heads); err != nil {
		t.Fatal(err)
	}
	var prs []map[string]any
	if err = json.Unmarshal(heads, &prs); err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0]["commit_sha"] != strings.Repeat("a", 40) {
		t.Fatal("new head inherited old review")
	}
	// A newer due input must be included even when more than a batch of older
	// rows were deferred. Its task evidence must refer to a delivered input.
	dbfx.Exec(t, `UPDATE issue_wakeup SET enabled=false,disabled_at=now() WHERE issue_id=$1 AND source_task_id=$2`, f.issueID, taskID)
	for n := 0; n < 20; n++ {
		if err = testHandler.recordVCSInput(ctx, conn, pr.ID, "comment", fmt.Sprintf("older-deferred-%d", n), fmt.Sprintf("Older deferred concern %d", n), pr.HtmlUrl, ""); err != nil {
			t.Fatal(err)
		}
	}
	dbfx.Exec(t, `UPDATE vcs_workflow_input SET next_attempt_at=now()+interval '1 hour',created_at=now()-interval '1 hour' WHERE issue_id=$1 AND event_key LIKE 'older-deferred-%'`, f.issueID)
	if err = testHandler.recordVCSInput(ctx, conn, pr.ID, "comment", "newer-due", "Newly due concern", pr.HtmlUrl, ""); err != nil {
		t.Fatal(err)
	}
	if worked, err = worker.RecoverNextVCSWorkflowInput(ctx); err != nil || !worked {
		t.Fatalf("deferred batch continuation %v %v", worked, err)
	}
	var actualEvidence, claimable bool
	if err = testPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vcs_workflow_input input WHERE input.id=task.trigger_evidence_ref_id AND input.task_id=task.id),workflow_task_claimable(task.id,task.issue_id)
      FROM agent_task_queue task WHERE task.issue_id=$1 AND task.status='queued'`, f.issueID).Scan(&actualEvidence, &claimable); err != nil {
		t.Fatal(err)
	}
	if !actualEvidence || !claimable {
		t.Fatal("batch excluded its primary evidence and stranded the queued continuation")
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM vcs_workflow_input WHERE issue_id=$1 AND processed_at IS NULL`, f.issueID); got != 2 {
		t.Fatalf("bounded batch silently discarded deferred inputs: %d", got)
	}

}

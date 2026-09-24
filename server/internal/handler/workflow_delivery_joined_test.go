package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

// This crosses the authority, trusted integration, provider evidence, durable
// acceptance and outbound poller boundaries without contacting a live host.
func TestWorkflowAutonomousReviewedPRFinalizesAndDeliversThroughPoller(t *testing.T) {
	for _, action := range []string{"ready", "merge"} {
		t.Run(action, func(t *testing.T) { runWorkflowAutonomousReviewedPRFinalizesAndDeliversThroughPoller(t, action) })
	}
}

func runWorkflowAutonomousReviewedPRFinalizesAndDeliversThroughPoller(t *testing.T, action string) {
	if testHandler == nil {
		t.Skip("handler DB fixture unavailable")
	}
	ready, merged := false, false
	patches, reviewReads, mergeCalls := 0, 0, 0
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token joined-workflow-token" {
			t.Errorf("provider request did not use trusted binding credential")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/team/project/pulls/1":
			title := "WIP: Reviewed change"
			if ready {
				title = "Reviewed change"
			}
			state := "open"
			if merged {
				state = "closed"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"title": title,
				"head": map[string]string{"sha": workflowDeliveryHead}, "draft": false, "merged": merged,
				"state": state, "merge_commit_sha": workflowDeliveryChangedHead})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/team/project/pulls/1/reviews":
			reviewReads++
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": 80,
				"html_url":  "https://" + r.Host + "/team/project/pulls/1#issuecomment-80",
				"commit_id": workflowDeliveryHead, "state": "APPROVED", "user": map[string]any{"id": 22}}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/team/project/pulls/1":
			patches++
			var edit struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&edit); err != nil || edit.Title != "Reviewed change" {
				t.Errorf("unexpected ready mutation %+v: %v", edit, err)
			}
			ready = true
			_ = json.NewEncoder(w).Encode(map[string]any{"title": edit.Title})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/team/project/pulls/1/merge":
			mergeCalls++
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil ||
				request["head_commit_id"] != workflowDeliveryHead || request["do"] != "merge" {
				t.Errorf("merge lacked accepted exact SHA and policy method: %+v, %v", request, err)
			}
			merged = true
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	defer provider.Close()
	withVCSBox(t)
	sealed, err := testHandler.sealVCSSecret("joined-workflow-token")
	if err != nil {
		t.Fatal(err)
	}
	binding := dbfx.Insert(t, "vcs_connection", testutil.Cols{
		"workspace_id": testWorkspaceID, "provider": "forgejo", "instance_url": provider.URL,
		"account_login": "joined fixture", "access_token_encrypted": sealed,
		"webhook_secret_encrypted": sealed,
	})
	runtime := dbfx.Runtime(t, "joined workflow runtime")
	writer := dbfx.Agent(t, "joined workflow writer", runtime)
	reviewer := dbfx.Agent(t, "joined workflow reviewer", runtime)
	acceptor := dbfx.Agent(t, "joined workflow acceptor", runtime)
	issue := dbfx.Issue(t, "Exact reviewed PR delivery")
	for _, table := range workflowLedgerTables {
		dbfx.Cleanup(t, fmt.Sprintf(`DELETE FROM %s WHERE issue_id=$1`, table), issue)
	}
	skill := insertCompleteWorkflowSkill(t, "---\nname: joined-workflow\n---\n\nReview exact PR before autonomous acceptance")
	policyJSON := fmt.Sprintf(`{"format_version":1,"autonomous_trivial":{"enabled":true,"acceptor_agent_ids":[%q],"delivery":%q}}`, acceptor, action)
	if action == "merge" {
		policyJSON = fmt.Sprintf(`{"format_version":1,"autonomous_trivial":{"enabled":true,"acceptor_agent_ids":[%q],"delivery":"merge"},"delivery":{"merge_method":"merge"}}`, acceptor)
	}
	dbfx.Insert(t, "skill_file", testutil.Cols{"skill_id": skill, "path": "runtime/policy.json", "content": policyJSON})
	var pinned service.IssueWorkflowPolicy
	enrollWorkflowPolicy(t, issue, skill).Want(http.StatusCreated).JSON(&pinned)
	prURL := provider.URL + "/team/project/pulls/1"
	pr := dbfx.Insert(t, "vcs_pull_request", testutil.Cols{
		"workspace_id": testWorkspaceID, "connection_id": binding, "provider": "forgejo",
		"repo_owner": "team", "repo_name": "project", "pr_number": 1,
		"title": "WIP: Reviewed change", "state": "open", "html_url": prURL,
		"branch": "reviewed-change", "head_sha": workflowDeliveryHead,
		"pr_created_at": testutil.Raw("now()"), "pr_updated_at": testutil.Raw("now()"),
	})
	dbfx.InsertNoID(t, "issue_vcs_pull_request", testutil.Cols{"issue_id": issue, "pull_request_id": pr},
		"issue_id=$1 AND pull_request_id=$2", issue, pr)
	writerTask := dbfx.Task(t, writer, testutil.Cols{"issue_id": issue, "runtime_id": runtime,
		"status": "completed", "session_id": "joined-writer-session",
		"started_at": testutil.Raw("now()"), "completed_at": testutil.Raw("now()")})
	dbfx.Exec(t, `UPDATE issue SET status='in_progress',assignee_type='agent',assignee_id=$2 WHERE id=$1`, issue, writer)
	handoffService := service.IssueWakeupService{Tasks: testHandler.TaskService}
	handoff, err := handoffService.CreateHandoff(context.Background(), parseUUID(issue), parseUUID(testUserID), parseUUID(writerTask),
		service.HandoffInput{RequestKey: uuidToString(dbid.NewV7()), OutgoingTaskID: writerTask,
			AgentID: reviewer, Status: "in_review", ContextMode: "fresh",
			Instruction: "Independently review the exact candidate PR revision.",
			Candidates: []service.HandoffCandidate{{RepositoryURL: provider.URL + "/team/project",
				PRURL: prURL, Branch: "reviewed-change", CommitSHA: workflowDeliveryHead, Draft: true}},
			EvidenceURLs: []string{},
		})
	if err != nil {
		t.Fatalf("create fresh reviewer handoff: %v", err)
	}
	dbfx.Cleanup(t, `DELETE FROM issue_wakeup_receipt WHERE wakeup_id=$1`, handoff.ID)
	dbfx.Cleanup(t, `DELETE FROM issue_wakeup WHERE id=$1`, handoff.ID)
	if err := handoffService.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := handoffService.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	var reviewerTask string
	if err := testPool.QueryRow(context.Background(), `SELECT last_task_id::text FROM issue_wakeup WHERE id=$1`, handoff.ID).Scan(&reviewerTask); err != nil {
		t.Fatalf("fresh reviewer task not dispatched: %v", err)
	}
	dbfx.Cleanup(t, `DELETE FROM agent_task_queue WHERE id=$1`, reviewerTask)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='running',started_at=now(),
		session_id='joined-reviewer-session',workflow_policy_version=$2,workflow_profile_id=$3
		WHERE id=$1`, reviewerTask, pinned.Version, dbid.NewV7())
	current, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(issue))
	if err != nil || !current.WorkflowCandidateID.Valid || current.Status != "in_review" || uuidToString(current.AssigneeID) != reviewer {
		t.Fatalf("handoff did not register current exact candidate: %+v, %v", current, err)
	}
	candidate := uuidToString(current.WorkflowCandidateID)
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = provider.Client()
	previousWorker := testHandler.WorkflowDeliveryWorker
	testHandler.WorkflowDeliveryWorker = worker
	t.Cleanup(func() { testHandler.WorkflowDeliveryWorker = previousWorker })
	svc := testHandler.workflowAuthorityService()
	reviewLink := prURL + "#issuecomment-80"
	if err := svc.RegisterReview(context.Background(), parseUUID(testWorkspaceID), parseUUID(issue),
		service.WorkflowActor{Type: "agent", ID: reviewer, SourceTaskID: reviewerTask},
		service.WorkflowReviewInput{CandidateID: candidate, Verdict: "pass", PRReviewURLs: []string{reviewLink}}); err != nil {
		t.Fatalf("register independent review: %v", err)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, reviewerTask)
	acceptorTask := dbfx.Task(t, acceptor, testutil.Cols{"issue_id": issue, "runtime_id": runtime,
		"status": "running", "session_id": "joined-acceptor-session",
		"workflow_policy_version": pinned.Version, "workflow_profile_id": dbid.NewV7(),
		"started_at": testutil.Raw("now()")})
	dbfx.Exec(t, `UPDATE issue SET assignee_id=$2 WHERE id=$1`, issue, acceptor)
	current, err = testHandler.Queries.GetIssue(context.Background(), parseUUID(issue))
	if err != nil {
		t.Fatal(err)
	}
	state, err := svc.AcceptWorkflow(context.Background(), parseUUID(testWorkspaceID), parseUUID(issue),
		service.WorkflowActor{Type: "agent", ID: acceptor, SourceTaskID: acceptorTask},
		service.WorkflowAcceptanceInput{CandidateID: candidate, ExpectedRevision: current.Revision,
			ClassificationReason: "Routine scoped change with a completed exact-head review."})
	if err != nil || state != "requested" {
		t.Fatalf("autonomous acceptance request state=%q err=%v", state, err)
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE issue_id=$1`, issue); count != 0 {
		t.Fatalf("running acceptor released %d delivery intents", count)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, acceptorTask)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	go worker.Run(ctx)
	for {
		if dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE issue_id=$1 AND status='delivered'`, issue) == 1 {
			break
		}
		if ctx.Err() != nil {
			var gotState, errClass string
			_ = testPool.QueryRow(context.Background(), `SELECT state,COALESCE(last_error_class,'') FROM issue_workflow_acceptance
				WHERE issue_id=$1 ORDER BY requested_at DESC LIMIT 1`, issue).Scan(&gotState, &errClass)
			t.Fatalf("poller failed to deliver exact reviewed PR: acceptance=%s class=%s", gotState, errClass)
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	if !worker.WaitWithTimeout(time.Second) {
		t.Fatal("workflow worker did not stop")
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, issue); got != 1 {
		t.Fatalf("accepted authority records=%d", got)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue WHERE id=$1 AND status='done'`, issue); got != 1 {
		t.Fatal("issue was not completed before delivery")
	}
	wantAttempts := 1
	if action == "merge" {
		wantAttempts = 2 // readiness and pinned merge have separate durable results.
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery_attempt WHERE issue_id=$1 AND outcome='delivered'`, issue); got != wantAttempts {
		t.Fatalf("durable delivery attempts=%d", got)
	}
	wantMergeCalls := 0
	if action == "merge" {
		wantMergeCalls = 1
	}
	if reviewReads < 2 || patches != 1 || !ready || mergeCalls != wantMergeCalls || merged != (action == "merge") {
		t.Fatalf("provider review reads=%d ready mutations=%d ready=%v merge calls=%d merged=%v",
			reviewReads, patches, ready, mergeCalls, merged)
	}
}

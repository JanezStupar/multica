package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func workflowFormat2DeliveryFixture(t *testing.T, server *httptest.Server, count int, action string, held, outcomeComplete bool) (issueID, candidateID, acceptanceID string, deliveryIDs []string) {
	t.Helper()
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	var statusCategory string
	err := testPool.QueryRow(context.Background(), `SELECT category FROM issue_status WHERE workspace_id=$1 AND key='pr_ready'`, testWorkspaceID).Scan(&statusCategory)
	if errors.Is(err, pgx.ErrNoRows) {
		// Insert before the issue fixture so cleanup removes issues first.
		dbfx.Insert(t, "issue_status", testutil.Cols{
			"workspace_id": testWorkspaceID, "key": "pr_ready", "name": "PR Ready",
			"category": "started", "color": "#22c55e", "position": 1000,
		})
	} else if err != nil {
		t.Fatal(err)
	} else if statusCategory != "started" {
		t.Fatalf("pr_ready status category=%q, want started", statusCategory)
	}
	issueID, deliveryIDs = workflowDeliveryFixture(t, server, count, action)
	runtimeID := dbfx.Runtime(t, "completion outcome runtime")
	outcomeAgentID := dbfx.Agent(t, "completion outcome agent", runtimeID)

	var source service.AgentSkillData
	for _, builtin := range testHandler.TaskService.BuiltinSkills("", false) {
		if builtin.Name != service.PlatformSkillName {
			continue
		}
		source = builtin
		source.ID = uuidToString(dbid.NewV7())
		files := make([]service.AgentSkillFileData, 0, len(builtin.Files)+2)
		for _, file := range builtin.Files {
			if file.Path != "runtime/policy.json" {
				files = append(files, file)
			}
		}
		source.Files = files
		break
	}
	if source.ID == "" || source.Content == "" {
		t.Fatal("platform workflow skill unavailable for format-2 test policy")
	}
	source.Files = append(source.Files, service.AgentSkillFileData{
		Path: "runtime/issue-workflow.md", Content: "Follow the pinned workflow for this test issue.",
	}, service.AgentSkillFileData{
		Path:    "runtime/policy.json",
		Content: fmt.Sprintf(`{"format_version":2,"accepted_status_key":"pr_ready","outcome_agent_id":%q,"human":{"accept_roles":["owner","admin"],"delivery":%q},"review":{"required":true},"delivery":{"merge_method":"merge","multi_pr_merge_order":"explicit"}}`, outcomeAgentID, action),
	})
	pinned, err := testHandler.TaskService.NewIssueWorkflowPolicy(source)
	if err != nil {
		t.Fatalf("pin format-2 workflow policy: %v", err)
	}
	policyJSON, err := json.Marshal(pinned)
	if err != nil {
		t.Fatal(err)
	}
	prSet, err := json.Marshal(make([]struct{}, count))
	if err != nil {
		t.Fatal(err)
	}
	var candidateIDBytes, acceptanceIDBytes string
	if err := testPool.QueryRow(context.Background(), `SELECT d.candidate_id::text,d.acceptance_id::text
		FROM issue_workflow_delivery d WHERE d.id=$1`, deliveryIDs[0]).Scan(&candidateIDBytes, &acceptanceIDBytes); err != nil {
		t.Fatal(err)
	}
	candidateID, acceptanceID = candidateIDBytes, acceptanceIDBytes
	// The legacy worker fixture starts at done with no enrolled policy. Move it
	// to the v2 accepted status before pinning the new policy and acceptance.
	dbfx.Exec(t, `UPDATE issue SET status='pr_ready' WHERE id=$1`, issueID)
	issue, err := testHandler.Queries.GetIssueInWorkspace(context.Background(), db.GetIssueInWorkspaceParams{
		ID: parseUUID(issueID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatal(err)
	}
	scopeDigest := service.WorkflowScopeDigest(issue, pinned.Version)
	dbfx.Exec(t, `UPDATE issue_workflow_candidate SET policy_version=$2,scope_digest=$3,pr_set=$4 WHERE id=$1`,
		candidateID, pinned.Version, scopeDigest, prSet)
	dbfx.Exec(t, `UPDATE issue SET workflow_policy=$2 WHERE id=$1`, issueID, policyJSON)
	var revision int64
	if err := testPool.QueryRow(context.Background(), `SELECT revision FROM issue WHERE id=$1`, issueID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET issue_revision=$2,policy_version=$3,
		completion_version=2,accepted_status_key='pr_ready',outcome_agent_id=$4,
		hold_delivery=$5,held_at=CASE WHEN $5 THEN now() ELSE NULL END,
		outcome_complete=$6,outcome_completed_at=CASE WHEN $6 THEN now() ELSE NULL END
		WHERE id=$1`, acceptanceID, revision, pinned.Version, outcomeAgentID, held, outcomeComplete)
	return issueID, candidateID, acceptanceID, deliveryIDs
}

func workflowCompletionActionRequest(action, issueID, acceptanceID, candidateID string, revision int64, reason string) *http.Request {
	req := newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow/acceptances/"+acceptanceID+"/"+action,
		map[string]any{"candidate_id": candidateID, "expected_revision": revision, "reason": reason})
	req = withURLParam(req, "id", issueID)
	chi.RouteContext(req.Context()).URLParams.Add("acceptanceID", acceptanceID)
	return req
}

func workflowIssueRevision(t *testing.T, issueID string) int64 {
	t.Helper()
	var revision int64
	if err := testPool.QueryRow(context.Background(), `SELECT revision FROM issue WHERE id=$1`, issueID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func workflowOutcomeAgent(t *testing.T, acceptanceID string) (agentID, runtimeID string) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `SELECT a.outcome_agent_id::text,g.runtime_id::text
		FROM issue_workflow_acceptance a JOIN agent g ON g.id=a.outcome_agent_id
		WHERE a.id=$1`, acceptanceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatal(err)
	}
	return agentID, runtimeID
}

func TestWorkflowFormat2MergedFactSurvivesOutcomeDispatchFailureAndRetries(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	var externallyMerged atomic.Bool
	var mergeCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token delivery-token" {
			t.Errorf("wrong provider credential")
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/team/project/pulls/1/merge" {
			mergeCalls.Add(1)
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["head_commit_id"] != workflowDeliveryHead {
				t.Errorf("merge did not use accepted head: body=%v err=%v", input, err)
			}
			externallyMerged.Store(true)
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/team/project/pulls/1" {
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected provider request", http.StatusInternalServerError)
			return
		}
		merged := externallyMerged.Load()
		state := "open"
		if merged {
			state = "closed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Accepted work", "head": map[string]string{"sha": workflowDeliveryHead},
			"draft": false, "merged": merged, "state": state, "merge_commit_sha": workflowDeliveryChangedHead})
	}))
	defer server.Close()
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, false)
	outcomeAgentID, runtimeID := workflowOutcomeAgent(t, acceptanceID)
	writerTaskID := dbfx.Task(t, outcomeAgentID, testutil.Cols{
		"issue_id": issueID, "runtime_id": runtimeID, "status": "completed",
		"started_at": testutil.Raw("now()-interval '1 minute'"), "completed_at": testutil.Raw("now()"),
		"session_id": "completion-dispatch-writer-session",
	})
	dbfx.Exec(t, `UPDATE issue_workflow_candidate SET writer_task_id=$2 WHERE id=$1`, candidateID, writerTaskID)
	dbfx.Exec(t, `UPDATE agent SET runtime_id=NULL WHERE id=$1`, outcomeAgentID)
	defer func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE agent SET runtime_id=$2 WHERE id=$1`, outcomeAgentID, runtimeID)
	}()
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("readiness preparation worked=%v err=%v", worked, err)
	}
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("provider merge with unavailable outcome agent worked=%v err=%v", worked, err)
	}
	if mergeCalls.Load() != 1 {
		t.Fatalf("provider merge calls=%d, want one", mergeCalls.Load())
	}
	var deliveryStatusValue string
	var mergedAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `SELECT status,merged_at FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&deliveryStatusValue, &mergedAt); err != nil || deliveryStatusValue != "delivered" || !mergedAt.Valid {
		t.Fatalf("verified merge fact status=%q merged_at=%v err=%v", deliveryStatusValue, mergedAt, err)
	}
	view, err := testHandler.workflowAuthorityService().ReadState(context.Background(), parseUUID(testWorkspaceID), parseUUID(issueID),
		service.WorkflowActor{Type: "member", ID: testUserID})
	if err != nil || view.Acceptance == nil || view.Acceptance.Blocker != "outcome_dispatch_failed" || view.Acceptance.OutcomeTaskID != "" {
		t.Fatalf("durable outcome dispatch blocker state=%+v err=%v", view.Acceptance, err)
	}
	var errorClass string
	var attempts int
	var retryScheduled bool
	if err := testPool.QueryRow(context.Background(), `SELECT last_error_class,outcome_dispatch_attempt_count,
		outcome_next_attempt_at>now() FROM issue_workflow_acceptance WHERE id=$1`, acceptanceID).
		Scan(&errorClass, &attempts, &retryScheduled); err != nil || errorClass != "outcome_dispatch_failed" || attempts != 1 || !retryScheduled {
		t.Fatalf("durable retry marker class=%q attempts=%d scheduled=%t err=%v", errorClass, attempts, retryScheduled, err)
	}
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || worked || mergeCalls.Load() != 1 {
		t.Fatalf("delivered PR repeated after dispatch failure: worked=%v merge_calls=%d err=%v", worked, mergeCalls.Load(), err)
	}
	dbfx.Exec(t, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, outcomeAgentID, runtimeID)
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET outcome_next_attempt_at=now() WHERE id=$1`, acceptanceID)
	worked, err = testHandler.workflowAuthorityService().RetryNextWorkflowCompletionDispatch(context.Background())
	if err != nil || !worked {
		t.Fatalf("automatic outcome dispatch retry worked=%v err=%v", worked, err)
	}
	view, err = testHandler.workflowAuthorityService().ReadState(context.Background(), parseUUID(testWorkspaceID), parseUUID(issueID),
		service.WorkflowActor{Type: "member", ID: testUserID})
	if err != nil || view.Acceptance == nil || view.Acceptance.Blocker != "" || view.Acceptance.OutcomeTaskID == "" {
		t.Fatalf("outcome dispatch retry state=%+v err=%v", view.Acceptance, err)
	}
	var taskCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM agent_task_queue
		WHERE issue_id=$1 AND context->'workflow_outcome'->>'acceptance_id'=$2`, issueID, acceptanceID).Scan(&taskCount); err != nil || taskCount != 1 {
		t.Fatalf("outcome task count=%d err=%v, want exactly one", taskCount, err)
	}
	if mergeCalls.Load() != 1 {
		t.Fatalf("outcome retry repeated provider merge: calls=%d", mergeCalls.Load())
	}
}

func TestWorkflowFormat2HeldPreparedMergeObservesExternalMergeWithoutReleasingHold(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	var externallyMerged atomic.Bool
	var mergeCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mergeCalls.Add(1)
			t.Errorf("held merge was mutated by the worker: %s", r.URL.Path)
			http.Error(w, "held merge must not be issued", http.StatusInternalServerError)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/team/project/pulls/1" {
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected provider request", http.StatusInternalServerError)
			return
		}
		merged := externallyMerged.Load()
		state := "open"
		if merged {
			state = "closed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Accepted work", "head": map[string]string{"sha": workflowDeliveryHead},
			"draft": false, "merged": merged, "state": state, "merge_commit_sha": workflowDeliveryChangedHead})
	}))
	defer server.Close()
	issueID, _, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", true, true)
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("held readiness preparation worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "pending" {
		t.Fatalf("held prepared merge status=%q, want pending", status)
	}
	var readinessAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `SELECT readiness_done_at FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&readinessAt); err != nil || !readinessAt.Valid {
		t.Fatalf("held merge was not prepared: readiness_at=%v err=%v", readinessAt, err)
	}
	externallyMerged.Store(true)
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("held external merge observation worked=%v err=%v", worked, err)
	}
	var finalStatus string
	var mergedAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `SELECT status,merged_at FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&finalStatus, &mergedAt); err != nil || finalStatus != "delivered" || !mergedAt.Valid {
		t.Fatalf("held external merge delivery status=%q merged_at=%v err=%v", finalStatus, mergedAt, err)
	}
	var held bool
	var issueStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT a.hold_delivery,i.status FROM issue_workflow_acceptance a
		JOIN issue i ON i.id=a.issue_id WHERE a.id=$1 AND i.id=$2`, acceptanceID, issueID).Scan(&held, &issueStatus); err != nil || !held || issueStatus != "done" {
		t.Fatalf("external merge changed hold or outcome status: held=%t issue=%q err=%v", held, issueStatus, err)
	}
	if mergeCalls.Load() != 0 {
		t.Fatalf("held external observer sent %d merge POSTs", mergeCalls.Load())
	}
}

func TestWorkflowFormat2HoldAllowsReadinessAndSurvivesWorkerReconstruction(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	title := "WIP: Accepted work"
	readinessCalls, mergeCalls := 0, 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token delivery-token" {
			t.Errorf("wrong provider credential")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/team/project/pulls/1":
			_ = json.NewEncoder(w).Encode(map[string]any{"title": title, "head": map[string]string{"sha": workflowDeliveryHead},
				"draft": false, "merged": false, "state": "open"})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/repos/team/project/pulls/1":
			readinessCalls++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["title"] != "Accepted work" {
				t.Errorf("readiness update body=%v err=%v", body, err)
			}
			title = "Accepted work"
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/team/project/pulls/1/merge":
			mergeCalls++
			http.Error(w, "held delivery must not merge", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, true)
	revision := workflowIssueRevision(t, issueID)
	var held service.WorkflowState
	testutil.Call(t, testHandler.HoldIssueWorkflowDelivery,
		workflowCompletionActionRequest("hold", issueID, acceptanceID, candidateID, revision, "Keep the merge held")).
		Want(http.StatusOK).JSON(&held)
	if held.Acceptance == nil || !held.Acceptance.HoldDelivery || !held.Acceptance.OutcomeComplete ||
		len(held.Delivery) != 1 || held.Delivery[0].Status != "pending" {
		t.Fatalf("hold state=%+v", held)
	}
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("held readiness worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "pending" {
		t.Fatalf("readiness status=%q; want pending merge", status)
	}
	var readinessAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `SELECT readiness_done_at FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&readinessAt); err != nil || !readinessAt.Valid {
		t.Fatalf("readiness was not persisted: %v %v", readinessAt, err)
	}
	if readinessCalls != 1 || mergeCalls != 0 {
		t.Fatalf("readiness calls=%d merge calls=%d", readinessCalls, mergeCalls)
	}
	restarted := NewWorkflowDeliveryWorker(testHandler)
	restarted.client = server.Client()
	_, err = restarted.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("reconstructed held worker read-only observation failed: %v", err)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&status); err != nil || status != "pr_ready" {
		t.Fatalf("held issue status=%q err=%v", status, err)
	}
	var hold bool
	if err := testPool.QueryRow(context.Background(), `SELECT a.hold_delivery,d.readiness_done_at
		FROM issue_workflow_acceptance a JOIN issue_workflow_delivery d ON d.acceptance_id=a.id
		WHERE a.id=$1 AND d.id=$2`, acceptanceID, ids[0]).Scan(&hold, &readinessAt); err != nil || !hold || !readinessAt.Valid {
		t.Fatalf("reconstructed worker lost durable hold or readiness: hold=%t readiness=%v err=%v", hold, readinessAt, err)
	}
	if readinessCalls != 1 || mergeCalls != 0 {
		t.Fatalf("reconstructed worker repeated work: readiness=%d merge=%d", readinessCalls, mergeCalls)
	}
}

func TestWorkflowFormat2OutcomeCompleteWaitsForRequiredMerge(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	mergeCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mergeCalls++
			t.Errorf("completion acknowledgment bypassed pending merge: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Accepted work", "head": map[string]string{"sha": workflowDeliveryHead},
			"draft": false, "merged": false, "state": "open"})
	}))
	defer server.Close()
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, false)
	revision := workflowIssueRevision(t, issueID)
	var acknowledged service.WorkflowState
	testutil.Call(t, testHandler.CompleteIssueWorkflowOutcome,
		workflowCompletionActionRequest("complete", issueID, acceptanceID, candidateID, revision, "Deployment and QA are complete")).
		Want(http.StatusOK).JSON(&acknowledged)
	if acknowledged.Acceptance == nil || !acknowledged.Acceptance.OutcomeComplete ||
		len(acknowledged.Delivery) != 1 || acknowledged.Delivery[0].MergedAt != nil {
		t.Fatalf("outcome acknowledgment state=%+v", acknowledged)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&status); err != nil || status != "pr_ready" {
		t.Fatalf("issue with pending merge status=%q err=%v", status, err)
	}
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("ready preparation worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "pending" || mergeCalls != 0 {
		t.Fatalf("pending merge status=%q merge calls=%d", status, mergeCalls)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&status); err != nil || status != "pr_ready" {
		t.Fatalf("preparation completed issue: status=%q err=%v", status, err)
	}
}

func TestWorkflowFormat2ReadyOnlyObservesExternalMerge(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	var providerMerged, providerClosed atomic.Bool
	mergeCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/team/project/pulls/1" {
			if r.Method == http.MethodPost {
				mergeCalls++
			}
			t.Errorf("ready-only delivery made unexpected provider request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected provider request", http.StatusInternalServerError)
			return
		}
		merged := providerMerged.Load()
		state := "open"
		if providerClosed.Load() {
			state = "closed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Accepted work", "head": map[string]string{"sha": workflowDeliveryHead},
			"draft": false, "merged": merged, "state": state, "merge_commit_sha": workflowDeliveryChangedHead})
	}))
	defer server.Close()
	issueID, _, _, ids := workflowFormat2DeliveryFixture(t, server, 1, "ready", false, true)
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("ready preparation worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "delivered" {
		t.Fatalf("ready-only preparation status=%q", status)
	}
	var mergedAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `SELECT merged_at FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&mergedAt); err != nil || mergedAt.Valid {
		t.Fatalf("preparation claimed a merge: merged_at=%v err=%v", mergedAt, err)
	}
	var issueStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&issueStatus); err != nil || issueStatus != "pr_ready" {
		t.Fatalf("ready-only issue status=%q err=%v", issueStatus, err)
	}
	providerMerged.Store(true)
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("inconsistent merge observation worked=%v err=%v", worked, err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT merged_at FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&mergedAt); err != nil || mergedAt.Valid {
		t.Fatalf("inconsistent provider state recorded a merge: merged_at=%v err=%v", mergedAt, err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&issueStatus); err != nil || issueStatus != "pr_ready" {
		t.Fatalf("inconsistent provider state completed issue: status=%q err=%v", issueStatus, err)
	}
	var errorClass string
	if err := testPool.QueryRow(context.Background(), `SELECT COALESCE(last_error_class,'') FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&errorClass); err != nil || errorClass != "provider_state_inconsistent" {
		t.Fatalf("inconsistent provider state error class=%q err=%v", errorClass, err)
	}
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET next_attempt_at=now() WHERE id=$1`, ids[0])
	providerClosed.Store(true)
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("external merge observation worked=%v err=%v", worked, err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT merged_at FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&mergedAt); err != nil || !mergedAt.Valid {
		t.Fatalf("external merge fact was not recorded: merged_at=%v err=%v", mergedAt, err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&issueStatus); err != nil || issueStatus != "done" {
		t.Fatalf("externally merged complete outcome status=%q err=%v", issueStatus, err)
	}
	if mergeCalls != 0 {
		t.Fatalf("ready-only observer issued %d merge mutations", mergeCalls)
	}
}

func TestWorkflowFormat2HoldWaitsForInflightMergeAndCannotClaimSuccess(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	mergeStarted := make(chan struct{})
	allowMerge := make(chan struct{})
	merged := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/team/project/pulls/1" && r.URL.Path != "/api/v1/repos/team/project/pulls/1/merge" {
			t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodPost {
			close(mergeStarted)
			<-allowMerge
			merged = true
			w.WriteHeader(http.StatusOK)
			return
		}
		state := "open"
		if merged {
			state = "closed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Accepted work", "head": map[string]string{"sha": workflowDeliveryHead},
			"draft": false, "merged": merged, "state": state, "merge_commit_sha": workflowDeliveryChangedHead})
	}))
	defer server.Close()
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, true)
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET readiness_done_at=now() WHERE id=$1`, ids[0])
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	workerResult := make(chan error, 1)
	go func() {
		worked, err := worker.ProcessNext(context.Background())
		if err == nil && !worked {
			err = fmt.Errorf("worker found no due merge")
		}
		workerResult <- err
	}()
	<-mergeStarted
	holdRequest := workflowCompletionActionRequest("hold", issueID, acceptanceID, candidateID,
		workflowIssueRevision(t, issueID), "Hold during provider merge")
	holdResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		testHandler.HoldIssueWorkflowDelivery(recorder, holdRequest)
		holdResult <- recorder
	}()
	select {
	case response := <-holdResult:
		t.Fatalf("hold returned while provider merge was in flight: %d %s", response.Code, response.Body.String())
	case <-time.After(100 * time.Millisecond):
	}
	close(allowMerge)
	if err := <-workerResult; err != nil {
		t.Fatalf("in-flight merge failed: %v", err)
	}
	response := <-holdResult
	if response.Code != http.StatusConflict {
		t.Fatalf("hold response after successful merge=%d %s, want conflict", response.Code, response.Body.String())
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "delivered" {
		t.Fatalf("in-flight merge status=%q", status)
	}
	var hold bool
	var issueStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT a.hold_delivery,i.status FROM issue_workflow_acceptance a
		JOIN issue i ON i.id=a.issue_id WHERE a.id=$1`, acceptanceID).Scan(&hold, &issueStatus); err != nil {
		t.Fatal(err)
	}
	if hold || issueStatus != "done" {
		t.Fatalf("hold=%t issue status=%q after serialized merge", hold, issueStatus)
	}
}

func TestWorkflowFormat2OrderedPartialMergeRetriesExactHeadWithoutRepeatingDeliveredPR(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	merged := map[int]bool{}
	mergeAttempts := map[int]int{}
	mergeHeads := map[int][]string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, suffix, _ := strings.Cut(r.URL.Path, "/pulls/")
			prNumber, _ := strconv.Atoi(strings.TrimSuffix(suffix, "/merge"))
			mergeAttempts[prNumber]++
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Errorf("decode merge request: %v", err)
			}
			mergeHeads[prNumber] = append(mergeHeads[prNumber], input["head_commit_id"])
			if prNumber == 2 && mergeAttempts[2] == 1 {
				http.Error(w, "temporary provider failure", http.StatusServiceUnavailable)
				return
			}
			merged[prNumber] = true
			w.WriteHeader(http.StatusOK)
			return
		}
		var prNumber int
		_, _ = fmt.Sscanf(r.URL.Path, "/api/v1/repos/team/project/pulls/%d", &prNumber)
		state := "open"
		if merged[prNumber] {
			state = "closed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Accepted work", "head": map[string]string{"sha": workflowDeliveryHead},
			"draft": false, "merged": merged[prNumber], "state": state, "merge_commit_sha": workflowDeliveryChangedHead})
	}))
	defer server.Close()
	issueID, _, _, ids := workflowFormat2DeliveryFixture(t, server, 2, "merge", false, true)
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET readiness_done_at=now() WHERE id=ANY($1::uuid[])`, ids)
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	for attempt := 1; attempt <= 2; attempt++ {
		worked, err := worker.ProcessNext(context.Background())
		if err != nil || !worked {
			t.Fatalf("initial ordered attempt %d worked=%v err=%v", attempt, worked, err)
		}
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "delivered" {
		t.Fatalf("first PR status=%q", status)
	}
	if status, _ := deliveryStatus(t, ids[1]); status != "retry" {
		t.Fatalf("second PR status=%q", status)
	}
	var issueStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&issueStatus); err != nil || issueStatus != "pr_ready" {
		t.Fatalf("partial delivery issue status=%q err=%v", issueStatus, err)
	}
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET next_attempt_at=now() WHERE id=$1`, ids[1])
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("ordered retry worked=%v err=%v", worked, err)
	}
	if mergeAttempts[1] != 1 || mergeAttempts[2] != 2 || len(mergeHeads[1]) != 1 || len(mergeHeads[2]) != 2 {
		t.Fatalf("merge attempts=%v heads=%v", mergeAttempts, mergeHeads)
	}
	for _, heads := range mergeHeads {
		for _, head := range heads {
			if head != workflowDeliveryHead {
				t.Fatalf("merge used head %q, want exact accepted %q", head, workflowDeliveryHead)
			}
		}
	}
	if status, _ := deliveryStatus(t, ids[1]); status != "delivered" {
		t.Fatalf("retried PR status=%q", status)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&issueStatus); err != nil || issueStatus != "done" {
		t.Fatalf("fully merged complete outcome status=%q err=%v", issueStatus, err)
	}
}

func TestWorkflowFormat2ChangedHeadRevokesAcceptanceAndRequiresExplicitRejection(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	mergeCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mergeCalls++
			t.Errorf("changed head was merged: %s", r.URL.Path)
			http.Error(w, "unexpected merge", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"title": "Changed accepted work", "head": map[string]string{"sha": workflowDeliveryChangedHead},
			"draft": false, "merged": false, "state": "open"})
	}))
	defer server.Close()
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 2, "merge", false, true)
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET status='delivered',merged_at=now(),merge_commit_sha=$2
		WHERE id=$1`, ids[0], workflowDeliveryChangedHead)
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET readiness_done_at=now() WHERE id=$1`, ids[1])
	runtimeID := dbfx.Runtime(t, "changed-head writer runtime")
	writerAgentID := dbfx.Agent(t, "changed-head writer", runtimeID)
	writerTaskID := dbfx.Task(t, writerAgentID, testutil.Cols{
		"runtime_id": runtimeID, "issue_id": issueID, "status": "completed", "started_at": testutil.Raw("now()-interval '1 minute'"),
		"completed_at": testutil.Raw("now()"), "session_id": "retained-writer-session",
	})
	dbfx.Exec(t, `UPDATE issue_workflow_candidate SET writer_task_id=$2 WHERE id=$1`, candidateID, writerTaskID)
	dbfx.Insert(t, "issue_workflow_review", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": issueID, "candidate_id": candidateID,
		"reviewer_task_id": dbid.NewV7(), "verdict": "pass", "pr_review_urls": testutil.Raw("'[]'::jsonb"),
	})
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("stale delivery worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[1]); status != "stale" {
		t.Fatalf("changed-head delivery status=%q", status)
	}
	staleWorker := NewWorkflowDeliveryWorker(testHandler)
	staleWorker.client = server.Client()
	worked, err = staleWorker.ProcessNext(context.Background())
	if err != nil || worked {
		t.Fatalf("revoked stale candidate was automatically redispatched: worked=%v err=%v", worked, err)
	}
	var issueStatus string
	var currentCandidate, acceptanceState, acceptanceError string
	if err := testPool.QueryRow(context.Background(), `SELECT i.status,i.workflow_candidate_id::text,a.state,COALESCE(a.last_error_class,'')
		FROM issue i JOIN issue_workflow_acceptance a ON a.issue_id=i.id WHERE a.id=$1`, acceptanceID).
		Scan(&issueStatus, &currentCandidate, &acceptanceState, &acceptanceError); err != nil {
		t.Fatal(err)
	}
	if issueStatus != "in_review" || currentCandidate != candidateID || acceptanceState != "revoked" || acceptanceError != "stale_head" {
		t.Fatalf("stale candidate issue=%s candidate=%s acceptance=%s error=%s", issueStatus, currentCandidate, acceptanceState, acceptanceError)
	}
	var mergedAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `SELECT merged_at FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&mergedAt); err != nil || !mergedAt.Valid {
		t.Fatalf("already merged PR fact was lost: %v %v", mergedAt, err)
	}
	if mergeCalls != 0 {
		t.Fatalf("stale candidate triggered %d merge calls", mergeCalls)
	}
	newRevision := workflowIssueRevision(t, issueID)
	request := newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow/acceptances", map[string]any{
		"candidate_id": candidateID, "expected_revision": newRevision, "outcome_complete": false,
	})
	testutil.Call(t, testHandler.AcceptIssueWorkflow, withURLParam(request, "id", issueID)).Want(http.StatusConflict)
	if count := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_review WHERE issue_id=$1 AND candidate_id=$2`, issueID, candidateID); count != 1 {
		t.Fatalf("historical review record count=%d, want retained evidence", count)
	}
	request = newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow/rejections", map[string]any{
		"candidate_id": candidateID, "expected_revision": newRevision, "kind": "in_scope_defect",
		"reason": "Re-evaluate the changed PR head", "resume_task_id": writerTaskID,
	})
	testutil.Call(t, testHandler.RejectIssueWorkflow, withURLParam(request, "id", issueID)).Want(http.StatusOK)
	var resumedStatus string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&resumedStatus); err != nil || resumedStatus != "in_progress" {
		t.Fatalf("explicit rejection resume status=%q err=%v", resumedStatus, err)
	}
	var queued, resumedFrom int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*),count(*) FILTER (WHERE rerun_of_task_id=$2)
		FROM agent_task_queue WHERE issue_id=$1 AND status='queued'`, issueID, writerTaskID).Scan(&queued, &resumedFrom); err != nil {
		t.Fatal(err)
	}
	if queued != 1 || resumedFrom != 1 {
		t.Fatalf("explicit rejection queued=%d resumes=%d", queued, resumedFrom)
	}
}

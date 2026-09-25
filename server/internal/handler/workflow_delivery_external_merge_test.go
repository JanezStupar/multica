package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func externalMergeProvider(t *testing.T, head string, mergeCalls *int) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/team/project/pulls/1" {
			if r.Method == http.MethodPost {
				*mergeCalls++
			}
			t.Errorf("external merge made provider mutation %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected provider request", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"title": "Accepted work", "head": map[string]string{"sha": head},
			"draft": false, "merged": true, "state": "closed", "merge_commit_sha": workflowDeliveryHead,
			"merged_by": map[string]any{"id": 5, "login": "Multica"},
		})
	}))
}

func assertExternalMergedDelivery(t *testing.T, issueID, acceptanceID, deliveryID, authoritySource string) {
	t.Helper()
	var issueStatus, acceptanceState, deliveryStatus, source, mergedLogin string
	var mergedAt pgtype.Timestamptz
	if err := testPool.QueryRow(context.Background(), `SELECT i.status,a.state,d.status,d.merged_at
		FROM issue i JOIN issue_workflow_acceptance a ON a.issue_id=i.id
		JOIN issue_workflow_delivery d ON d.acceptance_id=a.id
		WHERE i.id=$1 AND a.id=$2 AND d.id=$3`, issueID, acceptanceID, deliveryID).
		Scan(&issueStatus, &acceptanceState, &deliveryStatus, &mergedAt); err != nil {
		t.Fatal(err)
	}
	if issueStatus != "done" || acceptanceState != "accepted" || deliveryStatus != "delivered" || !mergedAt.Valid {
		t.Fatalf("external merge did not finish: issue=%s acceptance=%s delivery=%s merged=%v",
			issueStatus, acceptanceState, deliveryStatus, mergedAt.Valid)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT details->>'authority_source',details->'merged_by'->>'login'
		FROM activity_log WHERE issue_id=$1 AND action='workflow_external_merge_observed'
		ORDER BY created_at DESC LIMIT 1`, issueID).Scan(&source, &mergedLogin); err != nil {
		t.Fatal(err)
	}
	if source != authoritySource || mergedLogin != "Multica" {
		t.Fatalf("external merge audit source=%q provider account=%q", source, mergedLogin)
	}
}

func TestWorkflowFormat2ExternalMergedHeadPolicyClosesWithoutMergePost(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	mergeCalls := 0
	server := externalMergeProvider(t, workflowDeliveryChangedHead, &mergeCalls)
	defer server.Close()
	issueID, _, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, true, "accepted")
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET readiness_done_at=now() WHERE id=$1`, ids[0])
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("observe off-head provider merge: worked=%v err=%v", worked, err)
	}
	assertExternalMergedDelivery(t, issueID, acceptanceID, ids[0], "policy")
	if mergeCalls != 0 {
		t.Fatalf("provider merge authority was expanded: %d POSTs", mergeCalls)
	}
}

func TestWorkflowFormat2OldPinNeedsCandidateScopedExternalMergeGrant(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	mergeCalls := 0
	server := externalMergeProvider(t, workflowDeliveryChangedHead, &mergeCalls)
	defer server.Close()
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, true)
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET readiness_done_at=now() WHERE id=$1`, ids[0])
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("old pin observation: worked=%v err=%v", worked, err)
	}
	if status, _ := deliveryStatus(t, ids[0]); status != "stale" {
		t.Fatalf("old pin silently trusted changed head: %s", status)
	}
	var issueStatus, acceptanceState string
	if err := testPool.QueryRow(context.Background(), `SELECT i.status,a.state FROM issue i
		JOIN issue_workflow_acceptance a ON a.issue_id=i.id WHERE a.id=$1`, acceptanceID).
		Scan(&issueStatus, &acceptanceState); err != nil || issueStatus != "pr_ready" || acceptanceState != "accepted" {
		t.Fatalf("old pin state issue=%s acceptance=%s err=%v", issueStatus, acceptanceState, err)
	}
	grant := map[string]any{"candidate_id": candidateID, "expected_revision": workflowIssueRevision(t, issueID),
		"scope": "external_merge", "grant_details": map[string]any{"accept_merged_head": true},
		"reason":       "The bound PR was merged by the provider after branch integration",
		"consequences": "Treat only its verified merge as delivery; do not authorize a Multica merge POST"}
	request := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow/exceptions", grant), "id", issueID)
	testutil.Call(t, testHandler.GrantIssueWorkflowException, request).Want(http.StatusCreated)
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("candidate-scoped external merge observation: worked=%v err=%v", worked, err)
	}
	assertExternalMergedDelivery(t, issueID, acceptanceID, ids[0], "exception")
	if mergeCalls != 0 {
		t.Fatalf("old pin exception caused %d merge POSTs", mergeCalls)
	}
}

func TestWorkflowFormat2PreviouslyRevokedStaleAcceptanceRecoversOnlyAfterGrant(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	mergeCalls := 0
	server := externalMergeProvider(t, workflowDeliveryChangedHead, &mergeCalls)
	defer server.Close()
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", true, true)
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET state='revoked',revoked_at=now(),last_error_class='stale_head' WHERE id=$1`, acceptanceID)
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET status='stale',last_error_class='stale_head',next_attempt_at=now() WHERE id=$1`, ids[0])
	dbfx.Exec(t, `UPDATE issue SET status='in_review',revision=revision+1 WHERE id=$1`, issueID)
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("revoked old pin probe: worked=%v err=%v", worked, err)
	}
	var state string
	if err := testPool.QueryRow(context.Background(), `SELECT state FROM issue_workflow_acceptance WHERE id=$1`, acceptanceID).Scan(&state); err != nil || state != "revoked" {
		t.Fatalf("revoked approval silently restored: state=%s err=%v", state, err)
	}
	grant := map[string]any{"candidate_id": candidateID, "expected_revision": workflowIssueRevision(t, issueID),
		"scope": "external_merge", "grant_details": map[string]any{"accept_merged_head": true},
		"reason":       "The already accepted bound PR was externally merged after branch integration",
		"consequences": "Restore only this stale-head approval and record the provider merge; no merge POST"}
	request := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow/exceptions", grant), "id", issueID)
	testutil.Call(t, testHandler.GrantIssueWorkflowException, request).Want(http.StatusCreated)
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("recover old stale acceptance: worked=%v err=%v", worked, err)
	}
	assertExternalMergedDelivery(t, issueID, acceptanceID, ids[0], "exception")
	var held bool
	if err := testPool.QueryRow(context.Background(), `SELECT hold_delivery FROM issue_workflow_acceptance WHERE id=$1`, acceptanceID).Scan(&held); err != nil || !held {
		t.Fatalf("recovery lost delivery hold: held=%v err=%v", held, err)
	}
	if mergeCalls != 0 {
		t.Fatalf("recovery caused %d merge POSTs", mergeCalls)
	}
}

func TestWorkflowFormat2ExternalMergeRespectsAllPRAndOutcomeGates(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	merged := map[int]bool{1: true, 2: false}
	mergeCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mergeCalls++
			t.Errorf("worker mutated external merge: %s %s", r.Method, r.URL.Path)
			return
		}
		part := strings.TrimPrefix(r.URL.Path, "/api/v1/repos/team/project/pulls/")
		number, err := strconv.Atoi(part)
		if err != nil || number < 1 || number > 2 {
			t.Errorf("unexpected provider path %s", r.URL.Path)
			return
		}
		state := "open"
		if merged[number] {
			state = "closed"
		}
		head := workflowDeliveryHead
		if number == 1 {
			head = workflowDeliveryChangedHead
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"title": "Accepted work", "head": map[string]string{"sha": head},
			"draft": false, "merged": merged[number], "state": state,
			"merge_commit_sha": workflowDeliveryHead, "merged_by": map[string]any{"id": 5, "login": "Multica"},
		})
	}))
	defer server.Close()
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 2, "merge", false, false, "accepted")
	outcomeAgentID, runtimeID := workflowOutcomeAgent(t, acceptanceID)
	writerTaskID := dbfx.Task(t, outcomeAgentID, testutil.Cols{
		"issue_id": issueID, "runtime_id": runtimeID, "status": "completed",
		"started_at": testutil.Raw("now()-interval '1 minute'"), "completed_at": testutil.Raw("now()"),
		"session_id": "external-merge-writer-session",
	})
	dbfx.Exec(t, `UPDATE issue_workflow_candidate SET writer_task_id=$2 WHERE id=$1`, candidateID, writerTaskID)
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("first external merge: worked=%v err=%v", worked, err)
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&status); err != nil || status != "pr_ready" {
		t.Fatalf("partial multi-PR merge completed issue: status=%q err=%v", status, err)
	}
	if first, _ := deliveryStatus(t, ids[0]); first != "delivered" {
		t.Fatalf("first delivery=%s", first)
	}
	if second, _ := deliveryStatus(t, ids[1]); second != "pending" {
		t.Fatalf("second delivery=%s", second)
	}
	merged[2] = true
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("second external merge: worked=%v err=%v", worked, err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM issue WHERE id=$1`, issueID).Scan(&status); err != nil || status != "pr_ready" {
		t.Fatalf("merged PRs bypassed pending outcome: status=%q err=%v", status, err)
	}
	var outcomeTask pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `SELECT outcome_task_id FROM issue_workflow_acceptance WHERE id=$1`, acceptanceID).Scan(&outcomeTask); err != nil || !outcomeTask.Valid {
		t.Fatalf("pending outcome task=%v err=%v", outcomeTask, err)
	}
	worked, err = worker.ProcessNext(context.Background())
	if err != nil || worked || mergeCalls != 0 {
		t.Fatalf("repeated poll duplicated delivery: worked=%v err=%v merge calls=%d", worked, err, mergeCalls)
	}
}

func TestWorkflowFormat2StaleRecoveryDoesNotOverrideLaterRejection(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	mergeCalls := 0
	server := externalMergeProvider(t, workflowDeliveryChangedHead, &mergeCalls)
	defer server.Close()
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, true, "accepted")
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET state='revoked',revoked_at=now(),last_error_class='stale_head' WHERE id=$1`, acceptanceID)
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET status='stale',last_error_class='stale_head',next_attempt_at=now() WHERE id=$1`, ids[0])
	dbfx.Exec(t, `UPDATE issue SET status='in_review',revision=revision+1 WHERE id=$1`, issueID)
	dbfx.Insert(t, "issue_workflow_rejection", testutil.Cols{
		"id":           dbid.NewV7(),
		"workspace_id": testWorkspaceID, "issue_id": issueID, "candidate_id": candidateID,
		"actor_type": "member", "actor_id": testUserID, "kind": "in_scope_defect",
		"reason": "Later explicit rejection", "issue_revision": workflowIssueRevision(t, issueID),
	})
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	worked, err := worker.ProcessNext(context.Background())
	if err != nil || !worked {
		t.Fatalf("conflicting recovery probe: worked=%v err=%v", worked, err)
	}
	var issueStatus, acceptanceState, deliveryStatus, errorClass string
	if err := testPool.QueryRow(context.Background(), `SELECT i.status,a.state,d.status,COALESCE(d.last_error_class,'')
		FROM issue i JOIN issue_workflow_acceptance a ON a.issue_id=i.id
		JOIN issue_workflow_delivery d ON d.acceptance_id=a.id WHERE a.id=$1`, acceptanceID).
		Scan(&issueStatus, &acceptanceState, &deliveryStatus, &errorClass); err != nil {
		t.Fatal(err)
	}
	if issueStatus != "in_review" || acceptanceState != "revoked" || deliveryStatus != "stale" || errorClass != "recovery_conflict" {
		t.Fatalf("later rejection was overridden: issue=%s acceptance=%s delivery=%s error=%s",
			issueStatus, acceptanceState, deliveryStatus, errorClass)
	}
	if mergeCalls != 0 {
		t.Fatalf("conflicting recovery made %d merge POSTs", mergeCalls)
	}
}

func TestWorkflowFormat2ExternalMergeGrantAndRevokePreserveNewerRequest(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	mergeCalls := 0
	server := externalMergeProvider(t, workflowDeliveryChangedHead, &mergeCalls)
	defer server.Close()
	actor := service.WorkflowActor{Type: "member", ID: testUserID}
	grantInput := func(candidateID string, revision int64) service.WorkflowExceptionInput {
		return service.WorkflowExceptionInput{CandidateID: candidateID, ExpectedRevision: revision,
			Scope: "external_merge", GrantDetails: map[string]any{"accept_merged_head": true},
			Reason:       "Use provider merge closure for this accepted PR",
			Consequences: "No Multica merge POST is authorized by this grant"}
	}
	seedRequested := func(issueID, candidateID, acceptanceID string) string {
		t.Helper()
		dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET state='revoked',revoked_at=now(),last_error_class='stale_head' WHERE id=$1`, acceptanceID)
		dbfx.Exec(t, `UPDATE issue SET status='in_review',revision=revision+1 WHERE id=$1`, issueID)
		var policyVersion string
		if err := testPool.QueryRow(context.Background(), `SELECT policy_version FROM issue_workflow_acceptance WHERE id=$1`, acceptanceID).Scan(&policyVersion); err != nil {
			t.Fatal(err)
		}
		return dbfx.Insert(t, "issue_workflow_acceptance", testutil.Cols{
			"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": issueID,
			"candidate_id": candidateID, "mode": "trivial", "actor_type": "agent",
			"actor_id": dbid.NewV7(), "state": "requested", "policy_version": policyVersion,
			"authority_snapshot": testutil.Raw("'{}'::jsonb"),
			"requested_at":       testutil.Raw("now()+interval '1 second'"),
		})
	}
	t.Run("grant", func(t *testing.T) {
		issueID, candidateID, acceptanceID, _ := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, true)
		requestedID := seedRequested(issueID, candidateID, acceptanceID)
		_, err := testHandler.workflowAuthorityService().GrantException(context.Background(),
			parseUUID(testWorkspaceID), parseUUID(issueID), actor,
			grantInput(candidateID, workflowIssueRevision(t, issueID)))
		if !errors.Is(err, service.ErrWorkflowAuthorityConflict) {
			t.Fatalf("grant displaced newer request: %v", err)
		}
		var state string
		if err := testPool.QueryRow(context.Background(), `SELECT state FROM issue_workflow_acceptance WHERE id=$1`, requestedID).Scan(&state); err != nil || state != "requested" {
			t.Fatalf("newer request state=%q err=%v", state, err)
		}
	})
	t.Run("revoke", func(t *testing.T) {
		issueID, candidateID, acceptanceID, _ := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, true)
		exceptionID, err := testHandler.workflowAuthorityService().GrantException(context.Background(),
			parseUUID(testWorkspaceID), parseUUID(issueID), actor,
			grantInput(candidateID, workflowIssueRevision(t, issueID)))
		if err != nil {
			t.Fatal(err)
		}
		requestedID := seedRequested(issueID, candidateID, acceptanceID)
		err = testHandler.workflowAuthorityService().RevokeException(context.Background(),
			parseUUID(testWorkspaceID), parseUUID(issueID), parseUUID(exceptionID), actor,
			service.WorkflowExceptionRevokeInput{ExpectedRevision: workflowIssueRevision(t, issueID),
				Reason: "Withdraw external merge delegation", Consequences: "No new off-head closure"})
		if !errors.Is(err, service.ErrWorkflowAuthorityConflict) {
			t.Fatalf("revoke displaced newer request: %v", err)
		}
		var state string
		if err := testPool.QueryRow(context.Background(), `SELECT state FROM issue_workflow_acceptance WHERE id=$1`, requestedID).Scan(&state); err != nil || state != "requested" {
			t.Fatalf("newer request state=%q err=%v", state, err)
		}
	})
}

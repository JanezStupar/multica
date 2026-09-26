package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/dbid"
)

func seedWorkflowStaleAcceptance(t *testing.T, issueID, acceptanceID, deliveryID, status string) {
	t.Helper()
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET state='revoked',revoked_at=now(),last_error_class='stale_head' WHERE id=$1`, acceptanceID)
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET status='stale',last_error_class='stale_head',next_attempt_at=now() WHERE id=$1`, deliveryID)
	dbfx.Exec(t, `UPDATE issue SET status=$2,revision=revision+1 WHERE id=$1`, issueID, status)
}

func grantWorkflowExternalMerge(t *testing.T, issueID, candidateID string) string {
	t.Helper()
	id, err := testHandler.workflowAuthorityService().GrantException(context.Background(),
		parseUUID(testWorkspaceID), parseUUID(issueID), service.WorkflowActor{Type: "member", ID: testUserID},
		service.WorkflowExceptionInput{CandidateID: candidateID, ExpectedRevision: workflowIssueRevision(t, issueID),
			Scope: "external_merge", GrantDetails: map[string]any{"accept_merged_head": true},
			Reason:       "Reconcile the verified provider merge for this candidate",
			Consequences: "Restore only the stale acceptance; no provider merge POST is authorized"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func assertWorkflowStaleAcceptance(t *testing.T, issueID, acceptanceID, deliveryID, expectedStatus string) {
	t.Helper()
	var status, state, delivery string
	dbfx.QueryRow(t, `SELECT i.status,a.state,d.status FROM issue i
		JOIN issue_workflow_acceptance a ON a.issue_id=i.id
		JOIN issue_workflow_delivery d ON d.acceptance_id=a.id
		WHERE i.id=$1 AND a.id=$2 AND d.id=$3`, issueID, acceptanceID, deliveryID).Scan(&status, &state, &delivery)
	if status != expectedStatus || state != "revoked" || delivery != "stale" {
		t.Fatalf("stale acceptance changed: status=%s acceptance=%s delivery=%s", status, state, delivery)
	}
}

func TestWorkflowStaleExternalMergeRecoveryAcrossNonterminalStatuses(t *testing.T) {
	for _, status := range []string{"backlog", "todo", "in_progress", "in_review", "blocked", "custom_unstarted", "custom_started"} {
		t.Run(status, func(t *testing.T) {
			if testHandler == nil {
				t.Skip("handler test fixture unavailable")
			}
			if status == "custom_unstarted" || status == "custom_started" {
				category := "started"
				if status == "custom_unstarted" {
					category = "unstarted"
				}
				dbfx.Insert(t, "issue_status", testutil.Cols{"workspace_id": testWorkspaceID,
					"key": status, "name": status, "category": category, "color": "#22c55e"})
			}
			mergeCalls := 0
			server := externalMergeProvider(t, workflowDeliveryChangedHead, &mergeCalls)
			defer server.Close()
			issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", true, true)
			seedWorkflowStaleAcceptance(t, issueID, acceptanceID, ids[0], status)
			worker := NewWorkflowDeliveryWorker(testHandler)
			worker.client = server.Client()
			if worked, err := worker.ProcessNextReadyObservation(context.Background()); err != nil || !worked {
				t.Fatalf("ungranted stale observation worked=%v error=%v", worked, err)
			}
			assertWorkflowStaleAcceptance(t, issueID, acceptanceID, ids[0], status)
			grantWorkflowExternalMerge(t, issueID, candidateID)
			if worked, err := worker.ProcessNextReadyObservation(context.Background()); err != nil || !worked {
				t.Fatalf("granted stale recovery worked=%v error=%v", worked, err)
			}
			assertExternalMergedDelivery(t, issueID, acceptanceID, ids[0], "exception")
			if mergeCalls != 0 {
				t.Fatalf("recovery made %d provider mutation requests", mergeCalls)
			}
		})
	}
}

func TestWorkflowStaleRecoveryUsesExistingAuthorityAcrossNonterminalStatuses(t *testing.T) {
	for _, tc := range []struct{ status, head, policy, source string }{
		{"blocked", workflowDeliveryChangedHead, "accepted", "policy"},
		{"custom_started", workflowDeliveryChangedHead, "accepted", "policy"},
		{"blocked", workflowDeliveryHead, "", "exact_head"},
	} {
		t.Run(tc.status+"/"+tc.source, func(t *testing.T) {
			if testHandler == nil {
				t.Skip("handler test fixture unavailable")
			}
			if tc.status == "custom_started" {
				dbfx.Insert(t, "issue_status", testutil.Cols{"workspace_id": testWorkspaceID,
					"key": tc.status, "name": tc.status, "category": "started", "color": "#22c55e"})
			}
			mergeCalls := 0
			server := externalMergeProvider(t, tc.head, &mergeCalls)
			defer server.Close()
			var policy []string
			if tc.policy != "" {
				policy = []string{tc.policy}
			}
			issueID, _, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, true, policy...)
			seedWorkflowStaleAcceptance(t, issueID, acceptanceID, ids[0], tc.status)
			worker := NewWorkflowDeliveryWorker(testHandler)
			worker.client = server.Client()
			if worked, err := worker.ProcessNextReadyObservation(context.Background()); err != nil || !worked {
				t.Fatalf("existing authority recovery worked=%v error=%v", worked, err)
			}
			assertExternalMergedDelivery(t, issueID, acceptanceID, ids[0], tc.source)
			if count := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_exception WHERE issue_id=$1`, issueID); count != 0 {
				t.Fatalf("status drift needed %d extra authority grants", count)
			}
			if mergeCalls != 0 {
				t.Fatalf("recovery made %d provider mutation requests", mergeCalls)
			}
		})
	}
}

func TestWorkflowStaleRecoveryDoesNotReviveCancelledWork(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test fixture unavailable")
	}
	mergeCalls := 0
	server := externalMergeProvider(t, workflowDeliveryChangedHead, &mergeCalls)
	defer server.Close()
	issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, true)
	seedWorkflowStaleAcceptance(t, issueID, acceptanceID, ids[0], "blocked")
	grantWorkflowExternalMerge(t, issueID, candidateID)
	dbfx.Exec(t, `UPDATE issue SET status='cancelled',revision=revision+1 WHERE id=$1`, issueID)
	worker := NewWorkflowDeliveryWorker(testHandler)
	worker.client = server.Client()
	if worked, err := worker.ProcessNextReadyObservation(context.Background()); err != nil || worked {
		t.Fatalf("cancelled stale recovery worked=%v error=%v", worked, err)
	}
	assertWorkflowStaleAcceptance(t, issueID, acceptanceID, ids[0], "cancelled")
	_, err := testHandler.workflowAuthorityService().GrantException(context.Background(),
		parseUUID(testWorkspaceID), parseUUID(issueID), service.WorkflowActor{Type: "member", ID: testUserID},
		service.WorkflowExceptionInput{CandidateID: candidateID, ExpectedRevision: workflowIssueRevision(t, issueID),
			Scope: "external_merge", GrantDetails: map[string]any{"accept_merged_head": true},
			Reason: "Try to revive cancelled work", Consequences: "Would restore acceptance"})
	if !errors.Is(err, service.ErrWorkflowAuthorityConflict) {
		t.Fatalf("cancelled candidate received new external merge authority: %v", err)
	}
}

func TestWorkflowStaleRecoveryPreservesAuthorityAndWorkGuards(t *testing.T) {
	for _, guard := range []string{"active_work", "queued_work", "deferred_work", "pending_handoff", "changed_scope", "wrong_candidate_grant", "wrong_policy_grant", "revoked_grant", "wrong_revocation", "open_changed_head"} {
		t.Run(guard, func(t *testing.T) {
			if testHandler == nil {
				t.Skip("handler test fixture unavailable")
			}
			mergeCalls := 0
			var server *httptest.Server
			if guard == "open_changed_head" {
				server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						mergeCalls++
					}
					_, _ = w.Write([]byte(`{"title":"Pending work","head":{"sha":"` + workflowDeliveryChangedHead + `"},"draft":false,"merged":false,"state":"open"}`))
				}))
			} else {
				server = externalMergeProvider(t, workflowDeliveryChangedHead, &mergeCalls)
			}
			defer server.Close()
			issueID, candidateID, acceptanceID, ids := workflowFormat2DeliveryFixture(t, server, 1, "merge", false, true)
			seedWorkflowStaleAcceptance(t, issueID, acceptanceID, ids[0], "blocked")
			exceptionID := grantWorkflowExternalMerge(t, issueID, candidateID)
			var newTaskID string
			switch guard {
			case "active_work", "queued_work", "deferred_work":
				agentID, runtimeID := workflowOutcomeAgent(t, acceptanceID)
				status := "running"
				if guard == "queued_work" {
					status = "queued"
				} else if guard == "deferred_work" {
					status = "deferred"
				}
				newTaskID = dbfx.Task(t, agentID, testutil.Cols{"issue_id": issueID, "runtime_id": runtimeID,
					"status": status})
			case "pending_handoff":
				agentID, _ := workflowOutcomeAgent(t, acceptanceID)
				dbfx.Insert(t, "issue_wakeup", testutil.Cols{"id": dbid.NewV7(), "workspace_id": testWorkspaceID,
					"issue_id": issueID, "agent_id": agentID, "created_by": testUserID,
					"instruction": "Pending exact candidate continuation", "kind": "at", "mode": "once",
					"enabled": true, "handoff": testutil.Raw("'{}'::jsonb")})
			case "changed_scope":
				dbfx.Exec(t, `UPDATE issue SET title=title||' changed',revision=revision+1 WHERE id=$1`, issueID)
			case "wrong_candidate_grant":
				dbfx.Exec(t, `UPDATE issue_workflow_exception SET candidate_id=$2 WHERE id=$1`, exceptionID, dbid.NewV7())
			case "wrong_policy_grant":
				dbfx.Exec(t, `UPDATE issue_workflow_exception SET base_policy_version='different-policy' WHERE id=$1`, exceptionID)
			case "revoked_grant":
				dbfx.Exec(t, `UPDATE issue_workflow_exception SET revoked_at=now() WHERE id=$1`, exceptionID)
			case "wrong_revocation":
				dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET last_error_class='rejected' WHERE id=$1`, acceptanceID)
			}
			worker := NewWorkflowDeliveryWorker(testHandler)
			worker.client = server.Client()
			if _, err := worker.ProcessNextReadyObservation(context.Background()); err != nil {
				t.Fatal(err)
			}
			assertWorkflowStaleAcceptance(t, issueID, acceptanceID, ids[0], "blocked")
			if guard == "queued_work" || guard == "deferred_work" {
				var observed bool
				var mergeSHA, taskStatus string
				dbfx.QueryRow(t, `SELECT merged_at IS NOT NULL,COALESCE(merge_commit_sha,'')
					FROM issue_workflow_delivery WHERE id=$1`, ids[0]).Scan(&observed, &mergeSHA)
				dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id=$1`, newTaskID).Scan(&taskStatus)
				wantTaskStatus := "queued"
				if guard == "deferred_work" {
					wantTaskStatus = "deferred"
				}
				if !observed || mergeSHA != workflowDeliveryHead || taskStatus != wantTaskStatus {
					t.Fatalf("conflict lost provider fact or newer work: observed=%v SHA=%s task=%s", observed, mergeSHA, taskStatus)
				}
				if count := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery_attempt
					WHERE delivery_id=$1 AND operation='reconcile' AND outcome='blocked'
					AND error_class='recovery_conflict' AND observed_head_sha=$2`, ids[0], workflowDeliveryChangedHead); count != 1 {
					t.Fatalf("blocked restoration recorded %d provider attempts", count)
				}
				if count := dbfx.Count(t, `SELECT count(*) FROM activity_log WHERE issue_id=$1
					AND action='workflow_external_merge_observed' AND details->>'recovery_outcome'='conflict'`, issueID); count != 1 {
					t.Fatalf("blocked restoration recorded %d provider audits", count)
				}
				if worked, err := worker.ProcessNextReadyObservation(context.Background()); err != nil || worked {
					t.Fatalf("observed conflict kept polling: worked=%v error=%v", worked, err)
				}
			}
			if mergeCalls != 0 {
				t.Fatalf("guard %s made %d provider mutation requests", guard, mergeCalls)
			}
		})
	}
}

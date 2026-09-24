package handler

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func workflowExceptionTestCandidate(t *testing.T) (issueID, candidateID string, revision int64) {
	t.Helper()
	issueID = dbfx.Issue(t, "Candidate authority exception", testutil.Cols{"status": "in_review"})
	enrollWorkflowPolicy(t, issueID, insertCompleteWorkflowSkill(t, "---\nname: exception-policy\n---\n\nPinned policy")).Want(http.StatusCreated)
	issue, err := testHandler.Queries.GetIssueInWorkspace(context.Background(), db.GetIssueInWorkspaceParams{
		ID: parseUUID(issueID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := testHandler.TaskService.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || policy == nil {
		t.Fatalf("decode pinned policy: %v", err)
	}
	candidateID = dbfx.Insert(t, "issue_workflow_candidate", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": issueID,
		"policy_version": policy.Version, "digest": "candidate-digest",
		"scope_digest":      service.WorkflowScopeDigest(issue, policy.Version),
		"source_handoff_id": dbid.NewV7(), "source_task_id": dbid.NewV7(),
		"writer_task_id": dbid.NewV7(), "pr_set": testutil.Raw("'[]'::jsonb"),
	})
	dbfx.Exec(t, `UPDATE issue SET workflow_candidate_id=$2 WHERE id=$1`, issueID, candidateID)
	return issueID, candidateID, issue.Revision
}

func TestIssueWorkflowExceptionGrantAndRevokePreserveHistory(t *testing.T) {
	issueID, candidateID, revision := workflowExceptionTestCandidate(t)
	grantBody := map[string]any{"candidate_id": candidateID, "expected_revision": revision,
		"scope": "review", "grant_details": map[string]any{"waive": true},
		"reason":       "Independent review unavailable for this candidate",
		"consequences": "Accepting this candidate will not require a passing review"}
	request := func(body any) *http.Request {
		return withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow/exceptions", body), "id", issueID)
	}
	var granted service.WorkflowState
	testutil.Call(t, testHandler.GrantIssueWorkflowException, request(grantBody)).Want(http.StatusCreated).JSON(&granted)
	if granted.IssueRevision != revision+1 || len(granted.Exceptions) != 1 ||
		granted.Exceptions[0].CandidateID != candidateID || granted.Exceptions[0].Scope != "review" ||
		granted.Exceptions[0].GrantDetails["waive"] != true || granted.Exceptions[0].RevokedAt != nil {
		t.Fatalf("grant state lost candidate-scoped authority: %+v", granted)
	}
	exceptionID := granted.Exceptions[0].ID
	duplicate := map[string]any{}
	for key, value := range grantBody {
		duplicate[key] = value
	}
	duplicate["expected_revision"] = revision + 1
	testutil.Call(t, testHandler.GrantIssueWorkflowException, request(duplicate)).Want(http.StatusConflict)
	invalid := map[string]any{}
	for key, value := range duplicate {
		invalid[key] = value
	}
	invalid["grant_details"] = map[string]any{"waive": "yes"}
	testutil.Call(t, testHandler.GrantIssueWorkflowException, request(invalid)).Want(http.StatusBadRequest)

	acceptanceID := dbfx.Insert(t, "issue_workflow_acceptance", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": issueID, "candidate_id": candidateID,
		"mode": "trivial", "actor_type": "agent", "actor_id": dbid.NewV7(),
		"state": "accepted", "issue_revision": revision + 1, "policy_version": granted.PolicyVersion,
		"authority_snapshot": testutil.Raw("'{}'::jsonb"), "accepted_at": testutil.Raw("now()"),
	})
	revoke := func(expected int64) *http.Request {
		req := newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow/exceptions/"+exceptionID+"/revoke",
			map[string]any{"expected_revision": expected, "reason": "Restore independent review",
				"consequences": "A new passing review is required"})
		req = withURLParam(req, "id", issueID)
		chi.RouteContext(req.Context()).URLParams.Add("exceptionID", exceptionID)
		return req
	}
	testutil.Call(t, testHandler.RevokeIssueWorkflowException, revoke(revision+1)).Want(http.StatusConflict)
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET state='requested',accepted_at=NULL WHERE id=$1`, acceptanceID)
	var revoked service.WorkflowState
	testutil.Call(t, testHandler.RevokeIssueWorkflowException, revoke(revision+1)).Want(http.StatusOK).JSON(&revoked)
	if revoked.IssueRevision != revision+2 || len(revoked.Exceptions) != 1 ||
		revoked.Exceptions[0].RevokedAt == nil || revoked.Exceptions[0].RevocationReason != "Restore independent review" ||
		revoked.Exceptions[0].RevokedByType != "member" || revoked.Acceptance == nil || revoked.Acceptance.State != "blocked" {
		t.Fatalf("revoke failed to preserve history and block pending acceptance: %+v", revoked)
	}
	var count int
	dbfx.QueryRow(t, `SELECT count(*) FROM issue_workflow_exception WHERE id=$1 AND base_policy_version=$2`, exceptionID, granted.PolicyVersion).Scan(&count)
	if count != 1 {
		t.Fatalf("revocation removed historical grant: %d", count)
	}
	var actorID string
	dbfx.QueryRow(t, `SELECT revoked_by_id::text FROM issue_workflow_exception WHERE id=$1`, exceptionID).Scan(&actorID)
	if actorID != testUserID {
		t.Fatalf("revocation actor=%s, want current member", actorID)
	}
}

func TestIssueWorkflowExceptionRejectsUnconfiguredSupervisorAndMember(t *testing.T) {
	issueID, candidateID, revision := workflowExceptionTestCandidate(t)
	body := map[string]any{"candidate_id": candidateID, "expected_revision": revision, "scope": "delivery",
		"grant_details": map[string]any{"action": "merge", "merge_method": "squash"},
		"reason":        "Approve merge delivery", "consequences": "The delivery worker may squash merge"}
	memberID := createTestUserAndMember(t, "member")
	memberReq := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow/exceptions", body), "id", issueID)
	memberReq.Header.Set("X-User-ID", memberID)
	testutil.Call(t, testHandler.GrantIssueWorkflowException, memberReq).Want(http.StatusForbidden)
	svc := service.WorkflowAuthorityService{Tasks: testHandler.TaskService}
	_, err := svc.GrantException(context.Background(), parseUUID(testWorkspaceID), parseUUID(issueID),
		service.WorkflowActor{Type: "agent", ID: uuidToString(dbid.NewV7()), SourceTaskID: uuidToString(dbid.NewV7())},
		service.WorkflowExceptionInput{CandidateID: candidateID, ExpectedRevision: revision, Scope: "delivery",
			GrantDetails: map[string]any{"action": "merge", "merge_method": "squash"},
			Reason:       "Approve merge delivery", Consequences: "The delivery worker may squash merge"})
	if err != service.ErrWorkflowAuthorityForbidden {
		t.Fatalf("unconfigured agent grant error=%v", err)
	}
	if count := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_exception WHERE issue_id=$1`, issueID); count != 0 {
		t.Fatalf("unauthorized grant inserted %d exceptions", count)
	}
}

func TestIssueWorkflowExceptionConfiguredSupervisorNeedsBoundRunningTaskAndScope(t *testing.T) {
	issueID := dbfx.Issue(t, "Supervisor exception", testutil.Cols{"status": "in_review"})
	runtimeID := createClaimReclaimRuntime(t, nil, "supervisor exception runtime")
	agentID := dbfx.Agent(t, "supervisor agent", runtimeID)
	sourceID := insertCompleteWorkflowSkill(t, "---\nname: supervisor-policy\n---\n\nPinned policy")
	dbfx.Insert(t, "skill_file", testutil.Cols{"skill_id": sourceID, "path": "runtime/policy.json",
		"content": fmt.Sprintf(`{"format_version":1,"supervisors":[{"agent_id":%q,"scopes":["review"]}]}`, agentID)})
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusCreated)
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	claimed := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claimed.ID != taskID || claimed.WorkflowProfileID == "" {
		t.Fatalf("supervisor task lacks bound profile: %+v", claimed)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='running',started_at=now() WHERE id=$1`, taskID)
	issue, err := testHandler.Queries.GetIssueInWorkspace(context.Background(), db.GetIssueInWorkspaceParams{
		ID: parseUUID(issueID), WorkspaceID: parseUUID(testWorkspaceID),
	})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := testHandler.TaskService.DecodeIssueWorkflowPolicy(issue.WorkflowPolicy)
	if err != nil || policy == nil {
		t.Fatalf("decode supervisor policy: %v", err)
	}
	candidateID := dbfx.Insert(t, "issue_workflow_candidate", testutil.Cols{
		"id": dbid.NewV7(), "workspace_id": testWorkspaceID, "issue_id": issueID,
		"policy_version": policy.Version, "digest": "supervisor candidate",
		"scope_digest":      service.WorkflowScopeDigest(issue, policy.Version),
		"source_handoff_id": dbid.NewV7(), "source_task_id": dbid.NewV7(),
		"writer_task_id": dbid.NewV7(), "pr_set": testutil.Raw("'[]'::jsonb"),
	})
	dbfx.Exec(t, `UPDATE issue SET workflow_candidate_id=$2 WHERE id=$1`, issueID, candidateID)
	svc := service.WorkflowAuthorityService{Tasks: testHandler.TaskService}
	actor := service.WorkflowActor{Type: "agent", ID: agentID, SourceTaskID: taskID}
	_, err = svc.GrantException(context.Background(), parseUUID(testWorkspaceID), parseUUID(issueID),
		service.WorkflowActor{Type: "agent", ID: agentID, SourceTaskID: uuidToString(dbid.NewV7())},
		service.WorkflowExceptionInput{CandidateID: candidateID, ExpectedRevision: issue.Revision, Scope: "review",
			GrantDetails: map[string]any{"waive": true}, Reason: "Supervisor review decision", Consequences: "Waives review"})
	if err != service.ErrWorkflowAuthorityForbidden {
		t.Fatalf("unbound task accepted: %v", err)
	}
	id, err := svc.GrantException(context.Background(), parseUUID(testWorkspaceID), parseUUID(issueID), actor,
		service.WorkflowExceptionInput{CandidateID: candidateID, ExpectedRevision: issue.Revision, Scope: "review",
			GrantDetails: map[string]any{"waive": true}, Reason: "Supervisor review decision", Consequences: "Waives review"})
	if err != nil || id == "" {
		t.Fatalf("configured supervisor grant=%q error=%v", id, err)
	}
	_, err = svc.GrantException(context.Background(), parseUUID(testWorkspaceID), parseUUID(issueID), actor,
		service.WorkflowExceptionInput{CandidateID: candidateID, ExpectedRevision: issue.Revision + 1, Scope: "delivery",
			GrantDetails: map[string]any{"action": "merge", "merge_method": "squash"},
			Reason:       "Try delivery", Consequences: "Would merge"})
	if err != service.ErrWorkflowAuthorityForbidden {
		t.Fatalf("supervisor crossed delegated scope: %v", err)
	}
	if err := svc.RevokeException(context.Background(), parseUUID(testWorkspaceID), parseUUID(issueID), parseUUID(id), actor,
		service.WorkflowExceptionRevokeInput{ExpectedRevision: issue.Revision + 1, Reason: "Review required again", Consequences: "No waiver"}); err != nil {
		t.Fatalf("grantor supervisor revoke: %v", err)
	}
}

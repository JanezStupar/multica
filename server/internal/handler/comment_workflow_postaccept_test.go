package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func workflowPostacceptFixture(t *testing.T, noPR bool) workflowHumanCommentFixture {
	t.Helper()
	return workflowPostacceptFixtureWithOutcome(t, noPR, false)
}

func workflowPostacceptFixtureWithOutcome(t *testing.T, noPR, distinctOutcome bool) workflowHumanCommentFixture {
	t.Helper()
	if dbfx.Count(t, `SELECT count(*) FROM issue_status WHERE workspace_id=$1 AND key='pr_ready'`, testWorkspaceID) == 0 {
		dbfx.Insert(t, "issue_status", testutil.Cols{"workspace_id": testWorkspaceID,
			"key": "pr_ready", "name": "PR Ready", "category": "started", "color": "#22c55e", "position": 1000})
	}
	f := setupWorkflowHumanCommentFixtureWithPolicy(t, func(coordinatorID string) string {
		outcomeID := coordinatorID
		if distinctOutcome {
			agent, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(coordinatorID))
			if err != nil {
				t.Fatal(err)
			}
			outcomeID = dbfx.Agent(t, "Distinct configured outcome agent", uuidToString(agent.RuntimeID))
		}
		return fmt.Sprintf(`{"format_version":2,"accepted_status_key":"pr_ready","outcome_agent_id":%q,
		"review":{"required":false},"human":{"accept_roles":["owner","admin"],"delivery":"ready"},
		"autonomous_trivial":{"enabled":true,"acceptor_agent_ids":[%q],"delivery":"ready"}}`, outcomeID, coordinatorID)
	}, noPR)
	if !noPR {
		binding := dbfx.Insert(t, "vcs_connection", testutil.Cols{"workspace_id": testWorkspaceID,
			"provider": "forgejo", "instance_url": "https://forge.example", "account_login": "feedback fixture",
			"access_token_encrypted": []byte("unused test credential"), "webhook_secret_encrypted": []byte("unused test secret")})
		pr := dbfx.Insert(t, "vcs_pull_request", testutil.Cols{"workspace_id": testWorkspaceID,
			"connection_id": binding, "provider": "forgejo", "repo_owner": "team", "repo_name": "repo", "pr_number": 42,
			"title": "WIP: feedback candidate", "state": "open", "html_url": "https://forge.example/team/repo/pulls/42",
			"branch": "feature/feedback", "head_sha": strings.Repeat("a", 40),
			"pr_created_at": testutil.Raw("now()"), "pr_updated_at": testutil.Raw("now()")})
		dbfx.InsertNoID(t, "issue_vcs_pull_request", testutil.Cols{"issue_id": f.issueID, "pull_request_id": pr},
			"issue_id=$1 AND pull_request_id=$2", f.issueID, pr)
	}
	return f
}

func workflowPostacceptComment(t *testing.T, f workflowHumanCommentFixture, content string) CommentResponse {
	t.Helper()
	var response CommentResponse
	testutil.Call(t, testHandler.CreateComment, withURLParam(newRequest(http.MethodPost,
		"/api/issues/"+f.issueID+"/comments", map[string]any{"content": content}), "id", f.issueID)).
		Want(http.StatusCreated).JSON(&response)
	return response
}

func workflowPostacceptHumanAccept(t *testing.T, f workflowHumanCommentFixture, outcomeComplete bool) string {
	t.Helper()
	current, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	testutil.Call(t, testHandler.AcceptIssueWorkflow, withURLParam(newRequest(http.MethodPost,
		"/api/issues/"+f.issueID+"/workflow/acceptances", map[string]any{
			"candidate_id": uuidToString(current.WorkflowCandidateID), "expected_revision": current.Revision,
			"outcome_complete": outcomeComplete,
		}), "id", f.issueID)).Want(http.StatusOK)
	var acceptanceID string
	if err := testPool.QueryRow(context.Background(), `SELECT id::text FROM issue_workflow_acceptance
		WHERE issue_id=$1 AND state='accepted'`, f.issueID).Scan(&acceptanceID); err != nil {
		t.Fatal(err)
	}
	return acceptanceID
}

func workflowPostacceptTaskID(t *testing.T, f workflowHumanCommentFixture, commentID string) string {
	t.Helper()
	var taskID string
	if err := testPool.QueryRow(context.Background(), `SELECT id::text FROM agent_task_queue
		WHERE issue_id=$1 AND trigger_comment_id=$2 AND status IN ('queued','deferred','dispatched','running')`,
		f.issueID, commentID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	return taskID
}

func workflowPostacceptStart(t *testing.T, f workflowHumanCommentFixture, taskID, commentID string) {
	t.Helper()
	var diagnostic []byte
	if err := testPool.QueryRow(context.Background(), `SELECT jsonb_build_object('claimable',workflow_task_claimable(t.id,t.issue_id),
	 'requested',workflow_requested_comment_task_current(t.id,t.issue_id),'recorded',workflow_recorded_comment_input_current(t.issue_id,t.agent_id,$2),
	 'task_originator',t.originator_user_id,'task_accountable',t.accountable_user_id,'task_profile',t.workflow_profile_id,
	 'source_profile',source.workflow_profile_id,'source_policy',source.workflow_policy_version,'source_status',source.status,
	 'source_receipt',source.delivered_comment_ids,'source_plan',source.coalesced_comment_ids,'snapshot',a.authority_snapshot,
	 'selected',(SELECT id FROM issue_workflow_profile WHERE issue_id=t.issue_id AND agent_id=t.agent_id ORDER BY revision DESC LIMIT 1))
	 FROM agent_task_queue t JOIN issue_workflow_acceptance a ON a.issue_id=t.issue_id AND a.state='requested'
	 JOIN agent_task_queue source ON source.id=a.source_task_id WHERE t.id=$1`, taskID, commentID).Scan(&diagnostic); err == nil {
		t.Logf("requested claim evidence: %s", diagnostic)
	}
	claimed := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claimed.ID != taskID || claimed.AgentID != f.coordinatorID {
		t.Fatalf("wrong exact coordinator claim: task=%s agent=%s want_task=%s want_agent=%s", claimed.ID, claimed.AgentID, taskID, f.coordinatorID)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(delivered_comment_ids)`,
		taskID, commentID); got != 1 {
		t.Fatalf("human input %s was not actually delivered to %s", commentID, taskID)
	}
	if _, err := testHandler.TaskService.StartTask(context.Background(), parseUUID(taskID)); err != nil {
		t.Fatal(err)
	}
}

func workflowPostacceptCorrectionRequest(t *testing.T, f workflowHumanCommentFixture, taskID, commentID string) *http.Request {
	t.Helper()
	current, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/workflow/feedback-continuations",
		map[string]any{"candidate_id": uuidToString(current.WorkflowCandidateID), "expected_revision": current.Revision,
			"comment_id": commentID, "kind": "in_scope_defect"}), "id", f.issueID)
	req.Header.Set("X-Agent-ID", f.coordinatorID)
	req.Header.Set("X-Task-ID", taskID)
	req.Header.Set("X-Actor-Source", "task_token")
	return req
}

func TestWorkflowPostAcceptanceQuestionThenCorrectionUsesExactCoordinator(t *testing.T) {
	f := workflowPostacceptFixture(t, false)
	acceptanceID := workflowPostacceptHumanAccept(t, f, false)
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_wakeup WHERE id=$1 AND disabled_at IS NULL
		AND handoff_completed_at IS NOT NULL AND last_task_id IS NULL`, f.second.ID); got != 1 {
		t.Fatal("acceptance retired the exact completed member conversation evidence")
	}
	question := workflowPostacceptComment(t, f, "Can you explain how this candidate handles retries?")
	if len(question.TriggerOutcomes) != 1 || question.TriggerOutcomes[0].Status != DispatchQueued ||
		question.TriggerOutcomes[0].TargetID != f.coordinatorID {
		t.Fatalf("accepted question lost coordinator dispatch: %+v", question.TriggerOutcomes)
	}
	questionTask := workflowPostacceptTaskID(t, f, question.ID)
	workflowPostacceptStart(t, f, questionTask, question.ID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, questionTask)
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND state='accepted' AND revoked_at IS NULL`, acceptanceID); got != 1 {
		t.Fatal("discussion revoked accepted authority")
	}
	correction := workflowPostacceptComment(t, f, "The retry behavior is wrong; please correct the missing regression.")
	correctionTask := workflowPostacceptTaskID(t, f, correction.ID)
	workflowPostacceptStart(t, f, correctionTask, correction.ID)
	testutil.Call(t, testHandler.ContinueIssueWorkflowFeedback,
		workflowPostacceptCorrectionRequest(t, f, correctionTask, correction.ID)).Want(http.StatusOK)
	current, err := testHandler.Queries.GetIssue(context.Background(), parseUUID(f.issueID))
	if err != nil || current.WorkflowCandidateID.Valid || current.AssigneeType.String != "agent" || current.AssigneeID != f.writer {
		t.Fatalf("classified accepted correction failed to return to retained writer: %+v %v", current, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND state='revoked'`, acceptanceID); got != 1 {
		t.Fatal("classified correction did not revoke accepted authority")
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE acceptance_id=$1 AND status='cancelled'`, acceptanceID); got != 1 {
		t.Fatal("classified correction did not cancel pending delivery")
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, correctionTask)
	claimed := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claimed.AgentID != uuidToString(f.writer) || claimed.PriorSessionID != "candidate-writer-session" {
		t.Fatalf("correction did not enqueue retained writer: %+v", claimed)
	}
}

func TestWorkflowAcceptancePreservesAlreadyQueuedHumanQuestion(t *testing.T) {
	for _, noPR := range []bool{false, true} {
		t.Run(fmt.Sprintf("noPR=%v", noPR), func(t *testing.T) {
			f := workflowPostacceptFixture(t, noPR)
			question := workflowPostacceptComment(t, f, "Can you explain the result before I proceed?")
			taskID := workflowPostacceptTaskID(t, f, question.ID)
			acceptanceID := workflowPostacceptHumanAccept(t, f, noPR)
			workflowPostacceptStart(t, f, taskID, question.ID)
			if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND state='accepted'`, acceptanceID); got != 1 {
				t.Fatal("queued question altered acceptance")
			}
		})
	}
}

func TestWorkflowPromisedQuestionSurvivesExternalCompletionWithoutReworkAuthority(t *testing.T) {
	f := workflowPostacceptFixture(t, false)
	acceptanceID := workflowPostacceptHumanAccept(t, f, true)
	question := workflowPostacceptComment(t, f, "Can you explain the accepted behavior?")
	taskID := workflowPostacceptTaskID(t, f, question.ID)
	// External delivery is authoritative even while the reply is still queued.
	dbfx.Exec(t, `UPDATE issue_workflow_delivery SET status='delivered',merged_at=now() WHERE acceptance_id=$1`, acceptanceID)
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	q := testHandler.Queries.WithTx(tx)
	current, err := q.GetIssue(context.Background(), parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	_, completed, err := service.ReconcileWorkflowCompletion(context.Background(), tx, q, current, parseUUID(acceptanceID))
	if err != nil || completed {
		t.Fatalf("external completion discarded promised question: completed=%v err=%v", completed, err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	workflowPostacceptStart(t, f, taskID, question.ID)
	testutil.Call(t, testHandler.ContinueIssueWorkflowFeedback,
		workflowPostacceptCorrectionRequest(t, f, taskID, question.ID)).Want(http.StatusConflict)
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE id=$1 AND state='accepted' AND revoked_at IS NULL`, acceptanceID); got != 1 {
		t.Fatal("postdelivery discussion revoked completed authority")
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET outcome_next_attempt_at=now()-interval '1 second' WHERE id=$1`, acceptanceID)
	svc := testHandler.workflowAuthorityService()
	if processed, err := svc.RetryNextWorkflowCompletionDispatch(context.Background()); err != nil || !processed {
		t.Fatalf("final close after question: %v %v", processed, err)
	}
	newQuestion := workflowPostacceptComment(t, f, "Another new question after final completion.")
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND trigger_comment_id=$2`, f.issueID, newQuestion.ID); got != 0 {
		t.Fatal("completed candidate created a new implicit coordinator task")
	}
}

func workflowPostacceptRequest(t *testing.T, noPR bool) (workflowHumanCommentFixture, string, service.WorkflowAuthorityService) {
	t.Helper()
	return workflowPostacceptRequestWithOutcome(t, noPR, false)
}

func workflowPostacceptRequestWithOutcome(t *testing.T, noPR, distinctOutcome bool) (workflowHumanCommentFixture, string, service.WorkflowAuthorityService) {
	t.Helper()
	f := workflowPostacceptFixtureWithOutcome(t, noPR, distinctOutcome)
	ctx := context.Background()
	// Explicitly supersede the completed human handoff with a real new agent
	// handoff; an unrelated synthetic task must not bypass retained lineage.
	dbfx.Exec(t, `UPDATE issue_wakeup SET disabled_at=now(),enabled=false WHERE id=$1`, f.second.ID)
	prs := []service.HandoffCandidate{}
	if !noPR {
		prs = append(prs, service.HandoffCandidate{RepositoryURL: "https://forge.example/team/repo", PRURL: "https://forge.example/team/repo/pulls/42", Branch: "feature/feedback", CommitSHA: strings.Repeat("a", 40), Draft: true})
	}
	wakeups := service.IssueWakeupService{Tasks: testHandler.TaskService}
	handoff, err := wakeups.CreateHandoff(ctx, parseUUID(f.issueID), parseUUID(testUserID), parseUUID(f.coordinatorTaskID), service.HandoffInput{
		RequestKey: uuidToString(dbid.NewV7()), OutgoingTaskID: f.coordinatorTaskID,
		AssigneeType: "agent", AssigneeID: f.coordinatorID, AgentID: f.coordinatorID, Status: "in_review", ContextMode: "fresh",
		Instruction: "Evaluate the unchanged candidate under the pinned autonomous acceptance policy.", Candidates: prs, EvidenceURLs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = wakeups.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	handoff, err = testHandler.Queries.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: handoff.ID, WorkspaceID: handoff.WorkspaceID})
	if err != nil || !handoff.LastTaskID.Valid {
		t.Fatalf("new autonomous source handoff: %+v %v", handoff, err)
	}
	sourceID := uuidToString(handoff.LastTaskID)
	claimed := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claimed.ID != sourceID {
		t.Fatalf("wrong acceptance source: %+v", claimed)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(sourceID)); err != nil {
		t.Fatal(err)
	}
	current, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	svc := testHandler.workflowAuthorityService()
	outcomeComplete := noPR
	state, err := svc.AcceptWorkflow(ctx, parseUUID(testWorkspaceID), current.ID,
		service.WorkflowActor{Type: "agent", ID: f.coordinatorID, SourceTaskID: sourceID}, service.WorkflowAcceptanceInput{
			CandidateID: uuidToString(current.WorkflowCandidateID), ExpectedRevision: current.Revision, OutcomeComplete: &outcomeComplete,
			ClassificationReason: "The unchanged candidate satisfies the pinned scoped policy."})
	if err != nil || state != "requested" {
		t.Fatalf("request acceptance: state=%s err=%v", state, err)
	}
	return f, sourceID, svc
}

func TestWorkflowRequestedAcceptanceWaitsForActualHumanCommentDelivery(t *testing.T) {
	f, sourceID, svc := workflowPostacceptRequest(t, true)
	ctx := context.Background()
	question := workflowPostacceptComment(t, f, "Can you explain the tradeoff before delivery?")
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(coalesced_comment_ids)
		AND NOT $2::uuid=ANY(delivered_comment_ids)`, sourceID, question.ID); got != 1 {
		t.Fatal("comment transaction did not pin the exact undelivered source obligation")
	}
	result, _ := json.Marshal(protocol.TaskCompletedPayload{TaskID: sourceID, Output: "Candidate review finished."})
	completedSource, transitioned, err := testHandler.TaskService.CompleteTaskWithTransition(ctx, parseUUID(sourceID), result,
		"acceptance-source-session", "", "", false, "", "")
	if err != nil || !transitioned {
		t.Fatalf("complete acceptance source: %v %v", transitioned, err)
	}
	finalize := func(want string) {
		t.Helper()
		dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET next_attempt_at=now() WHERE issue_id=$1 AND state='requested'`, f.issueID)
		processed, err := svc.FinalizeNextRequestedAcceptance(ctx)
		if err != nil || !processed {
			t.Fatalf("finalize pending authority: processed=%v err=%v", processed, err)
		}
		var got string
		if err := testPool.QueryRow(ctx, `SELECT state FROM issue_workflow_acceptance WHERE issue_id=$1`, f.issueID).Scan(&got); err != nil || got != want {
			t.Fatalf("acceptance state=%s want=%s err=%v", got, want, err)
		}
	}
	// Run finalization before completion replay to exercise the race directly.
	finalize("requested")
	testHandler.reconcileCommentsOnCompletion(ctx, completedSource)
	followupID := workflowPostacceptTaskID(t, f, question.ID)
	workflowPostacceptStart(t, f, followupID, question.ID)
	finalize("requested")
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, followupID)
	finalize("accepted")
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1`, f.issueID); got != 1 {
		t.Fatal("question required a second approval request")
	}
}

func TestWorkflowRequestedRunningSourceKeepsHumanQuestionAcrossDefaultRuntimeRebind(t *testing.T) {
	f, sourceID, svc := workflowPostacceptRequest(t, false)
	ctx := context.Background()
	source, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(sourceID))
	if err != nil || source.Status != "running" || !source.WorkflowProfileID.Valid || !source.DispatchedAt.Valid {
		t.Fatalf("requested source lacks its authenticated running claim: err=%v status=%s", err, source.Status)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"candidate_id": uuidToString(issue.WorkflowCandidateID), "expected_revision": issue.Revision,
		"outcome_complete": false, "classification_reason": "The unchanged candidate satisfies the pinned scoped policy."}
	replayRequest := func() {
		t.Helper()
		req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/workflow/acceptances", request), "id", f.issueID)
		req.Header.Set("X-Agent-ID", f.coordinatorID)
		req.Header.Set("X-Task-ID", sourceID)
		req.Header.Set("X-Actor-Source", "task_token")
		testutil.Call(t, testHandler.AcceptIssueWorkflow, req).Want(http.StatusAccepted)
	}
	replayRequest()
	question := workflowPostacceptComment(t, f, "Please explain this candidate before the requested approval finalizes.")
	pendingQuestionID := workflowPostacceptTaskID(t, f, question.ID)
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='queued'
		AND runtime_id=$2 AND dispatched_at IS NULL AND started_at IS NULL`, pendingQuestionID, source.RuntimeID); got != 1 {
		t.Fatal("running source did not retain its question as an unclaimed plan")
	}
	oldRuntime, err := testHandler.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID: source.RuntimeID, WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	newRuntimeID := dbfx.Runtime(t, "New default for retained acceptance source", testutil.Cols{
		"provider": oldRuntime.Provider, "runtime_mode": oldRuntime.RuntimeMode,
	})
	testutil.Call(t, testHandler.UpdateAgent, withURLParam(newRequest(http.MethodPut,
		"/api/agents/"+f.coordinatorID, map[string]any{"runtime_id": newRuntimeID}), "id", f.coordinatorID)).Want(http.StatusOK)
	replayRequest()
	retained, err := testHandler.Queries.GetAgentTask(ctx, source.ID)
	if err != nil || retained.Status != "running" || retained.RuntimeID != source.RuntimeID ||
		retained.DispatchedAt != source.DispatchedAt || retained.WorkflowProfileID != source.WorkflowProfileID ||
		retained.WorkflowPolicyVersion != source.WorkflowPolicyVersion {
		t.Fatalf("default runtime move rewrote the source claim or policy: err=%v status=%s", err, retained.Status)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(coalesced_comment_ids)
		AND NOT $2::uuid=ANY(delivered_comment_ids) AND workflow_recorded_comment_input_current(issue_id,agent_id,$2)`, sourceID, question.ID); got != 1 {
		t.Fatal("default runtime move invalidated the exact recorded question on the retained source lease")
	}
	var body struct {
		Task *AgentTaskResponse `json:"task"`
	}
	claim := withURLParam(newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+newRuntimeID+"/tasks/claim",
		nil, testWorkspaceID, "workflow-policy-daemon"), "runtimeId", newRuntimeID)
	claim.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilityPlatformSkillV1)
	testutil.Call(t, testHandler.ClaimTaskByRuntime, claim).Want(http.StatusOK).JSON(&body)
	if body.Task != nil {
		t.Fatalf("new runtime crossed the still-running source lease: task=%s", body.Task.ID)
	}
	result, _ := json.Marshal(protocol.TaskCompletedPayload{TaskID: sourceID, Output: "The leased source has finished its candidate review."})
	completed, transitioned, err := testHandler.TaskService.CompleteTaskWithTransition(ctx, source.ID, result,
		"retained-old-runtime-source-session", "", "", false, "", "")
	if err != nil || !transitioned {
		t.Fatalf("complete retained source lease: transitioned=%v err=%v", transitioned, err)
	}
	if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
		t.Fatalf("defer existing approval for recorded input: processed=%v err=%v", processed, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='requested'
		AND last_error_class='human_comment_pending'`, f.issueID); got != 1 {
		t.Fatal("pending question did not keep the unchanged requested approval deferred")
	}
	worker := NewWorkflowDeliveryWorker(testHandler)
	if recovered, err := worker.RecoverNextRecordedWorkflowComment(ctx); err != nil || !recovered {
		t.Fatalf("recover recorded question onto new default runtime: recovered=%v err=%v", recovered, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='cancelled'
		AND runtime_id=$2 AND dispatched_at IS NULL AND started_at IS NULL`, pendingQuestionID, source.RuntimeID); got != 1 {
		t.Fatal("recovery changed a claimed lease or left the stale unclaimed plan current")
	}
	testHandler.reconcileCommentsOnCompletion(ctx, completed)
	followupID := workflowPostacceptTaskID(t, f, question.ID)
	f.runtimeID = newRuntimeID
	followup := claimWorkflowTask(t, newRuntimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if followup.ID != followupID || followup.RuntimeID != newRuntimeID || followup.AgentID != f.coordinatorID ||
		followup.WorkflowProfileID != uuidToString(source.WorkflowProfileID) || followup.WorkflowPolicyVersion != source.WorkflowPolicyVersion.String ||
		followup.PriorSessionID != "" {
		t.Fatalf("new-runtime reply lost retained policy or borrowed old session: task=%s runtime=%s profile=%s prior_session=%q",
			followup.ID, followup.RuntimeID, followup.WorkflowProfileID, followup.PriorSessionID)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(followupID)); err != nil {
		t.Fatalf("start new-runtime exact question: %v", err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='running'
		AND $2::uuid=ANY(delivered_comment_ids)`, followupID, question.ID); got != 1 {
		t.Fatal("new-runtime reply did not actually receive the promised input")
	}
	answer, _ := json.Marshal(protocol.TaskCompletedPayload{TaskID: followupID, Output: "The human question has been answered."})
	if _, transitioned, err := testHandler.TaskService.CompleteTaskWithTransition(ctx, parseUUID(followupID), answer,
		"current-runtime-question-session", "", "", false, "", ""); err != nil || !transitioned {
		t.Fatalf("complete new-runtime answer: transitioned=%v err=%v", transitioned, err)
	}
	dbfx.Exec(t, `UPDATE issue_workflow_acceptance SET next_attempt_at=now() WHERE issue_id=$1 AND state='requested'`, f.issueID)
	if processed, err := svc.FinalizeNextRequestedAcceptance(ctx); err != nil || !processed {
		t.Fatalf("finalize unchanged approval after answer: processed=%v err=%v", processed, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, f.issueID); got != 1 {
		t.Fatal("runtime move required a new approval after the exact question was answered")
	}
}

func TestWorkflowAcceptedAssignedAgentQuestionAndCorrectionNeedNoHumanReassignment(t *testing.T) {
	f, sourceID, svc := workflowPostacceptRequest(t, false)
	ctx := context.Background()
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, sourceID)
	processed, err := svc.FinalizeNextRequestedAcceptance(ctx)
	if err != nil || !processed {
		t.Fatalf("finalizer before human comment: processed=%v err=%v", processed, err)
	}
	question := workflowPostacceptComment(t, f, "Can you explain the accepted tradeoff?")
	if len(question.TriggerOutcomes) != 1 || question.TriggerOutcomes[0].Status != DispatchQueued || question.TriggerOutcomes[0].TargetID != f.coordinatorID {
		t.Fatalf("accepted assigned-agent question did not report truthful dispatch: %+v", question.TriggerOutcomes)
	}
	taskID := workflowPostacceptTaskID(t, f, question.ID)
	workflowPostacceptStart(t, f, taskID, question.ID)
	current, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil || current.AssigneeType.String != "agent" || uuidToString(current.AssigneeID) != f.coordinatorID || current.Status != "pr_ready" {
		t.Fatalf("discussion required assignment/status ceremony: %+v %v", current, err)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1
		AND human_comment_obligations @> jsonb_build_array(jsonb_build_object('comment_id',$2::uuid::text,'agent_id',$3::uuid::text))`,
		f.issueID, question.ID, f.coordinatorID); got != 1 {
		t.Fatal("accepted assigned recipient was not recorded atomically")
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
	correction := workflowPostacceptComment(t, f, "The accepted retry behavior is wrong; please correct it.")
	correctionID := workflowPostacceptTaskID(t, f, correction.ID)
	workflowPostacceptStart(t, f, correctionID, correction.ID)
	testutil.Call(t, testHandler.ContinueIssueWorkflowFeedback,
		workflowPostacceptCorrectionRequest(t, f, correctionID, correction.ID)).Want(http.StatusOK)
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='revoked'`, f.issueID); got != 1 {
		t.Fatal("accepted assigned-agent correction did not revoke acceptance")
	}
}

func TestWorkflowRequestedAcceptanceIgnoresWithdrawnAndNoteInputPlans(t *testing.T) {
	for _, withdrawal := range []string{"deleted", "note"} {
		t.Run(withdrawal, func(t *testing.T) {
			f, sourceID, svc := workflowPostacceptRequest(t, true)
			comment := workflowPostacceptComment(t, f, "A question which I am withdrawing.")
			if withdrawal == "deleted" {
				dbfx.Exec(t, `UPDATE comment SET deleted_at=now(),content='',updated_at=now() WHERE id=$1`, comment.ID)
			} else {
				dbfx.Exec(t, `UPDATE comment SET content='/note withdrawn from agent input',updated_at=now() WHERE id=$1`, comment.ID)
			}
			dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, sourceID)
			processed, err := svc.FinalizeNextRequestedAcceptance(context.Background())
			if err != nil || !processed {
				t.Fatalf("finalize after %s withdrawal: processed=%v err=%v", withdrawal, processed, err)
			}
			if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, f.issueID); got != 1 {
				t.Fatalf("%s input plan created a false conversation barrier", withdrawal)
			}
		})
	}
}

func TestWorkflowPendingHumanQuestionDoesNotStarveOtherAcceptanceRequests(t *testing.T) {
	pending, pendingSource, svc := workflowPostacceptRequest(t, true)
	workflowPostacceptComment(t, pending, "Please explain the pending candidate.")
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, pendingSource)
	ready, readySource, _ := workflowPostacceptRequest(t, true)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, readySource)
	for turn := 0; turn < 2; turn++ {
		processed, err := svc.FinalizeNextRequestedAcceptance(context.Background())
		if err != nil || !processed {
			t.Fatalf("finalize turn %d: processed=%v err=%v", turn, processed, err)
		}
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='requested'
		AND last_error_class='human_comment_pending' AND next_attempt_at>now()`, pending.issueID); got != 1 {
		t.Fatal("pending question lost its durable request/retry deferral")
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='accepted'`, ready.issueID); got != 1 {
		t.Fatal("pending conversation starved unrelated ready authority")
	}
}

func TestWorkflowHumanConversationClaimStillRejectsCancelledSupersededAndFrozenLineage(t *testing.T) {
	for _, change := range []string{"cancelled", "custom_closed", "superseded", "candidate_cleared", "frozen"} {
		t.Run(change, func(t *testing.T) {
			f := setupWorkflowHumanCommentFixture(t)
			comment := workflowPostacceptComment(t, f, "Please explain the current candidate.")
			taskID := workflowPostacceptTaskID(t, f, comment.ID)
			switch change {
			case "cancelled":
				dbfx.Exec(t, `UPDATE issue SET status='cancelled',revision=revision+1 WHERE id=$1`, f.issueID)
			case "custom_closed":
				dbfx.Insert(t, "issue_status", testutil.Cols{"workspace_id": testWorkspaceID,
					"key": "closed_feedback_test", "name": "Closed", "category": "closed", "color": "#8899aa", "position": 2000})
				dbfx.Exec(t, `UPDATE issue SET status='closed_feedback_test',revision=revision+1 WHERE id=$1`, f.issueID)
			case "superseded":
				// A newer disabled handoff is still newer evidence. The old
				// completed one cannot regain authority through active-only lookup.
				dbfx.Exec(t, `INSERT INTO issue_wakeup(id,workspace_id,issue_id,agent_id,created_by,kind,mode,instruction,filter_agent_id,filter_task_id,
					source_task_id,handoff,handoff_completed_at,disabled_at,enabled)
					SELECT $2,workspace_id,issue_id,agent_id,created_by,kind,mode,instruction,filter_agent_id,filter_task_id,
					source_task_id,handoff,now(),now(),false FROM issue_wakeup WHERE id=$1`, f.second.ID, dbid.NewV7())
			case "candidate_cleared":
				dbfx.Exec(t, `UPDATE issue SET workflow_candidate_id=NULL,revision=revision+1 WHERE id=$1`, f.issueID)
			case "frozen":
				dbfx.Exec(t, `UPDATE issue SET workflow_frozen=true WHERE id=$1`, f.issueID)
			}
			allowed, err := testHandler.Queries.CheckWorkflowTaskClaimable(context.Background(), db.CheckWorkflowTaskClaimableParams{
				TaskID: parseUUID(taskID), IssueID: parseUUID(f.issueID)})
			if err != nil || allowed {
				t.Fatalf("%s lineage remained claimable: allowed=%v err=%v", change, allowed, err)
			}
			if _, err := testHandler.TaskService.StartTask(context.Background(), parseUUID(taskID)); err == nil {
				t.Fatalf("%s lineage crossed the start gate", change)
			}
		})
	}
}

func TestWorkflowRequestedAcceptanceCorrectionInvalidatesBeforeFinalization(t *testing.T) {
	f, sourceID, svc := workflowPostacceptRequest(t, true)
	ctx := context.Background()
	correction := workflowPostacceptComment(t, f, "The reviewed behavior is wrong; please correct the defect.")
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, sourceID)
	processed, err := svc.FinalizeNextRequestedAcceptance(ctx)
	if err != nil || !processed {
		t.Fatalf("defer requested authority: processed=%v err=%v", processed, err)
	}
	completed, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(sourceID))
	if err != nil {
		t.Fatal(err)
	}
	testHandler.reconcileCommentsOnCompletion(ctx, &completed)
	taskID := workflowPostacceptTaskID(t, f, correction.ID)
	workflowPostacceptStart(t, f, taskID, correction.ID)
	testutil.Call(t, testHandler.ContinueIssueWorkflowFeedback,
		workflowPostacceptCorrectionRequest(t, f, taskID, correction.ID)).Want(http.StatusOK)
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='revoked'`, f.issueID); got != 1 {
		t.Fatal("classified correction left requested authority live")
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_delivery WHERE issue_id=$1`, f.issueID); got != 0 {
		t.Fatal("correction released provider delivery before classification")
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, taskID)
	writer := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if writer.AgentID != uuidToString(f.writer) || writer.PriorSessionID != "candidate-writer-session" {
		t.Fatalf("requested correction did not resume retained writer: %+v", writer)
	}
}

func TestWorkflowRunningHumanFeedbackUsesRecordedSurvivorAfterPrimaryDeletion(t *testing.T) {
	f := workflowPostacceptFixture(t, false)
	workflowPostacceptHumanAccept(t, f, false)
	correction := workflowPostacceptComment(t, f, "Please correct this candidate's missing regression.")
	var primary CommentResponse
	testutil.Call(t, testHandler.CreateComment, withURLParam(newRequest(http.MethodPost,
		"/api/issues/"+f.issueID+"/comments", map[string]any{
			"content": "A later question which is being removed.", "parent_id": correction.ID,
		}), "id", f.issueID)).Want(http.StatusCreated).JSON(&primary)
	taskID := workflowPostacceptTaskID(t, f, primary.ID)
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(coalesced_comment_ids)`,
		f.coordinatorTaskID, correction.ID); got != 1 {
		t.Fatal("surviving input lacks exact completed coordinator source-plan proof")
	}
	workflowPostacceptStart(t, f, taskID, correction.ID)
	// Exercise the FK-cleared primary boundary without cancelling the running
	// task: the already-delivered, recorded coalesced input keeps its own proof.
	dbfx.Exec(t, `DELETE FROM comment WHERE id=$1`, primary.ID)
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND status='running'
		AND trigger_comment_id IS NULL AND $2::uuid=ANY(delivered_comment_ids)
		AND workflow_human_comment_task_current(id,issue_id)`, taskID, correction.ID); got != 1 {
		t.Fatal("current running task lost its exact recorded coalesced input")
	}
	testutil.Call(t, testHandler.ContinueIssueWorkflowFeedback,
		workflowPostacceptCorrectionRequest(t, f, taskID, correction.ID)).Want(http.StatusOK)
	if got := dbfx.Count(t, `SELECT count(*) FROM issue_workflow_acceptance WHERE issue_id=$1 AND state='revoked'`, f.issueID); got != 1 {
		t.Fatal("recorded survivor did not invalidate the accepted candidate")
	}
}

func TestWorkflowCorrectionPreservesOtherHumanLiveCoalescedQuestionAfterPrimaryWithdrawal(t *testing.T) {
	for _, ordinary := range []bool{false, true} {
		for _, withdrawal := range []string{"deleted", "note"} {
			t.Run(fmt.Sprintf("ordinary=%v/%s", ordinary, withdrawal), func(t *testing.T) {
				f := setupWorkflowHumanCommentFixture(t)
				ctx := context.Background()
				correction := workflowPostacceptComment(t, f, "Please correct this candidate's missing regression.")
				classifierID := workflowPostacceptTaskID(t, f, correction.ID)
				workflowPostacceptStart(t, f, classifierID, correction.ID)
				adminID := dbfx.User(t, "Coalesced question admin", "coalesced-question@multica.test")
				dbfx.Member(t, testWorkspaceID, adminID, "admin")
				f.allowCoordinatorInvocation(t, adminID)
				if ordinary {
					dbfx.Exec(t, `UPDATE issue SET assignee_type='agent',assignee_id=$2,revision=revision+1 WHERE id=$1`, f.issueID, f.coordinatorID)
				}
				postAdmin := func(content, parentID string) CommentResponse {
					t.Helper()
					var response CommentResponse
					testutil.Call(t, testHandler.CreateComment, withURLParam(newRequestAs(adminID, http.MethodPost,
						"/api/issues/"+f.issueID+"/comments", map[string]any{"content": content, "parent_id": parentID}),
						"id", f.issueID)).Want(http.StatusCreated).JSON(&response)
					return response
				}
				live := postAdmin("Can you explain this candidate's tradeoff to me?", correction.ID)
				withdrawn := postAdmin("A second question I am withdrawing.", live.ID)
				var otherTaskID string
				var beforeOverlay, beforeApps []byte
				if err := testPool.QueryRow(ctx, `SELECT id::text,runtime_mcp_overlay,runtime_connected_apps
					FROM agent_task_queue WHERE issue_id=$1 AND trigger_comment_id=$2 AND $3::uuid=ANY(coalesced_comment_ids)`,
					f.issueID, withdrawn.ID, live.ID).Scan(&otherTaskID, &beforeOverlay, &beforeApps); err != nil {
					t.Fatalf("real enqueue did not coalesce the same human's questions: %v", err)
				}
				if withdrawal == "deleted" {
					dbfx.Exec(t, `UPDATE comment SET deleted_at=now(),content='',updated_at=now() WHERE id=$1`, withdrawn.ID)
				} else {
					dbfx.Exec(t, `UPDATE comment SET content='/note no longer agent input',updated_at=now() WHERE id=$1`, withdrawn.ID)
				}
				testutil.Call(t, testHandler.ContinueIssueWorkflowFeedback,
					workflowPostacceptCorrectionRequest(t, f, classifierID, correction.ID)).Want(http.StatusOK)
				var status, originator, accountable, repairedPrimary string
				var afterOverlay, afterApps []byte
				if err := testPool.QueryRow(ctx, `SELECT status,originator_user_id::text,accountable_user_id::text,
					trigger_comment_id::text,runtime_mcp_overlay,runtime_connected_apps FROM agent_task_queue WHERE id=$1`,
					otherTaskID).Scan(&status, &originator, &accountable, &repairedPrimary, &afterOverlay, &afterApps); err != nil {
					t.Fatal(err)
				}
				if status != "deferred" || originator != adminID || accountable != adminID || repairedPrimary != live.ID ||
					string(beforeOverlay) != string(afterOverlay) || string(beforeApps) != string(afterApps) {
					t.Fatalf("remaining human input lost attribution/overlay: status=%s originator=%s accountable=%s primary=%s", status, originator, accountable, repairedPrimary)
				}
				dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, classifierID)
				writer := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
				if writer.AgentID != uuidToString(f.writer) {
					t.Fatalf("correction did not run writer first: %+v", writer)
				}
				dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, writer.ID)
				preserved := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
				if preserved.ID != otherTaskID || preserved.AgentID != f.coordinatorID {
					t.Fatalf("remaining question was stranded: %+v", preserved)
				}
				if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE id=$1 AND $2::uuid=ANY(delivered_comment_ids)
					AND NOT $3::uuid=ANY(delivered_comment_ids)`, otherTaskID, live.ID, withdrawn.ID); got != 1 {
					t.Fatal("repaired remaining question did not receive exact delivery receipt")
				}
			})
		}
	}
}

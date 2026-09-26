package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// The writer and final coordinator are deliberately different: the comment
// wakes the person who handed over the candidate, while a classified correction
// resumes the candidate's retained writer.
type workflowHumanCommentFixture struct {
	issueID, runtimeID, coordinatorID, coordinatorTaskID, writerTaskID string
	before                                                             db.Issue
	second                                                             db.IssueWakeup
	writer                                                             pgtype.UUID
}

func (f workflowHumanCommentFixture) allowCoordinatorInvocation(t *testing.T, userID string) {
	t.Helper()
	// A workspace admin cannot invoke another human's private agent. Share the
	// coordinator explicitly with this commenter, keeping the writer private.
	dbfx.Exec(t, `UPDATE agent SET permission_mode='public_to' WHERE id=$1`, f.coordinatorID)
	dbfx.Insert(t, "agent_invocation_target", testutil.Cols{
		"agent_id": f.coordinatorID, "target_type": "member", "target_id": userID,
		"created_by": testUserID,
	})
}

func setupWorkflowHumanCommentFixture(t *testing.T) workflowHumanCommentFixture {
	t.Helper()
	return setupWorkflowHumanCommentFixtureWithPolicy(t, nil, false)
}

func setupWorkflowHumanCommentFixtureWithPolicy(t *testing.T, policyConfig func(string) string, noPR bool) workflowHumanCommentFixture {
	t.Helper()
	ctx := context.Background()
	issueID := dbfx.Issue(t, "Human comment after workflow handoff")
	for _, table := range []string{"issue_workflow_rejection", "issue_workflow_delivery", "issue_workflow_acceptance", "issue_workflow_review", "issue_workflow_candidate", "issue_wakeup", "agent_task_queue", "comment"} {
		dbfx.Cleanup(t, "DELETE FROM "+table+" WHERE issue_id=$1", issueID)
	}
	dbfx.Cleanup(t, `DELETE FROM issue_wakeup_receipt WHERE wakeup_id IN
		(SELECT id FROM issue_wakeup WHERE issue_id=$1)`, issueID)
	runtimeID := createClaimReclaimRuntime(t, nil, "Human feedback runtime")
	writerID := dbfx.Agent(t, "Candidate writer "+issueID, runtimeID)
	coordinatorID := dbfx.Agent(t, "Final candidate coordinator "+issueID, runtimeID)
	skillID := insertCompleteWorkflowSkill(t, "workflow feedback test")
	dbfx.Exec(t, `UPDATE skill SET name=name||$2 WHERE id=$1`, skillID, " "+issueID)
	if policyConfig != nil {
		dbfx.Insert(t, "skill_file", testutil.Cols{"skill_id": skillID, "path": "runtime/policy.json", "content": policyConfig(coordinatorID)})
	}
	enrollWorkflowPolicy(t, issueID, skillID).Want(http.StatusCreated)
	dbfx.Exec(t, `UPDATE issue SET status='in_progress',assignee_type='agent',assignee_id=$2 WHERE id=$1`, issueID, writerID)
	writerTaskID := dbfx.Task(t, writerID, testutil.Cols{
		"issue_id": issueID, "runtime_id": runtimeID, "status": "queued",
		"originator_user_id": testUserID, "accountable_user_id": testUserID,
	})
	writerClaim := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if writerClaim.ID != writerTaskID || writerClaim.WorkflowProfileID == "" {
		t.Fatalf("candidate writer did not bind pinned workflow profile: %+v", writerClaim)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(writerTaskID)); err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET session_id='candidate-writer-session',
		work_dir='/tmp/workflow-candidate-writer' WHERE id=$1`, writerTaskID)
	pr := service.HandoffCandidate{RepositoryURL: "https://forge.example/team/repo", PRURL: "https://forge.example/team/repo/pulls/42",
		Branch: "feature/feedback", CommitSHA: strings.Repeat("a", 40), Draft: true}
	prs := []service.HandoffCandidate{pr}
	if noPR {
		prs = []service.HandoffCandidate{}
	}
	wakeups := service.IssueWakeupService{Tasks: testHandler.TaskService}
	first, err := wakeups.CreateHandoff(ctx, parseUUID(issueID), parseUUID(testUserID), parseUUID(writerTaskID), service.HandoffInput{
		RequestKey: uuidToString(dbid.NewV7()), OutgoingTaskID: writerTaskID,
		AssigneeType: "agent", AssigneeID: coordinatorID, AgentID: coordinatorID,
		Status: "in_review", ContextMode: "fresh", Instruction: "Review the candidate and coordinate with the human.",
		Candidates: prs, EvidenceURLs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, writerTaskID)
	if err := wakeups.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	first, err = testHandler.Queries.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: first.ID, WorkspaceID: first.WorkspaceID})
	if err != nil || !first.LastTaskID.Valid {
		t.Fatalf("first handoff did not dispatch: %+v %v", first, err)
	}
	coordinatorTaskID := uuidToString(first.LastTaskID)
	coordinatorClaim := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if coordinatorClaim.ID != coordinatorTaskID || coordinatorClaim.WorkflowProfileID == "" {
		t.Fatalf("candidate coordinator did not bind pinned workflow profile: %+v", coordinatorClaim)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(coordinatorTaskID)); err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET session_id='coordinator-session' WHERE id=$1`, coordinatorTaskID)
	second, err := wakeups.CreateHandoff(ctx, parseUUID(issueID), parseUUID(testUserID), first.LastTaskID, service.HandoffInput{
		RequestKey: uuidToString(dbid.NewV7()), OutgoingTaskID: coordinatorTaskID,
		AssigneeType: "member", AssigneeID: testUserID, Status: "in_review", ContextMode: "fresh",
		Instruction: "Present the candidate to the human for feedback.",
		Candidates:  prs, EvidenceURLs: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, coordinatorTaskID)
	if err := wakeups.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	second, err = testHandler.Queries.GetIssueWakeup(ctx, db.GetIssueWakeupParams{ID: second.ID, WorkspaceID: second.WorkspaceID})
	if err != nil || !second.HandoffCompletedAt.Valid || second.LastTaskID.Valid {
		t.Fatalf("member handoff not complete: %+v %v", second, err)
	}
	dbfx.Exec(t, `UPDATE issue SET status='blocked',revision=revision+1 WHERE id=$1`, issueID)
	before, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil || before.AssigneeType.String != "member" || !before.WorkflowCandidateID.Valid {
		t.Fatalf("member candidate unavailable: %+v %v", before, err)
	}
	var writer pgtype.UUID
	if err := testPool.QueryRow(ctx, `SELECT writer_task_id FROM issue_workflow_candidate WHERE id=$1`, before.WorkflowCandidateID).Scan(&writer); err != nil || writer != parseUUID(writerTaskID) {
		t.Fatalf("candidate writer drifted: %v %v", writer, err)
	}
	return workflowHumanCommentFixture{issueID: issueID, runtimeID: runtimeID, coordinatorID: coordinatorID,
		coordinatorTaskID: coordinatorTaskID, writerTaskID: writerTaskID, before: before, second: second, writer: parseUUID(writerID)}
}

func TestPlainHumanCommentOnMemberHandoffStartsWorkflowConversation(t *testing.T) {
	f := setupWorkflowHumanCommentFixture(t)
	ctx := context.Background()
	issueID, runtimeID, coordinatorID, coordinatorTaskID := f.issueID, f.runtimeID, f.coordinatorID, f.coordinatorTaskID
	before, second, writer := f.before, f.second, f.writer
	post := func(content string) CommentResponse {
		t.Helper()
		var response CommentResponse
		testutil.Call(t, testHandler.CreateComment,
			withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/comments", map[string]any{"content": content}), "id", issueID)).
			Want(http.StatusCreated).JSON(&response)
		return response
	}
	note := post("/note Keep this private note human-only")
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND trigger_comment_id=$2`, issueID, note.ID); got != 0 {
		t.Fatalf("/note queued %d tasks", got)
	}
	feedback := post("Please correct the missing regression and update the draft candidate.")
	if len(feedback.TriggerOutcomes) != 1 || feedback.TriggerOutcomes[0].Status != DispatchQueued ||
		feedback.TriggerOutcomes[0].TargetID != coordinatorID {
		t.Fatalf("workflow dispatch outcome missing from saved comment: %+v", feedback.TriggerOutcomes)
	}
	issueAfterComment, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil || issueAfterComment.WorkflowCandidateID != before.WorkflowCandidateID ||
		issueAfterComment.Status != "blocked" || issueAfterComment.AssigneeType.String != "member" {
		t.Fatalf("posting feedback changed decision or owner: %+v %v", issueAfterComment, err)
	}
	var queuedID string
	var marker []byte
	var evidenceKind, evidenceRef, delegatedFrom string
	if err := testPool.QueryRow(ctx, `SELECT id::text,context->'workflow_feedback',trigger_evidence_kind,
		trigger_evidence_ref_id::text,delegated_from_task_id::text FROM agent_task_queue
		WHERE issue_id=$1 AND trigger_comment_id=$2`, issueID, feedback.ID).Scan(
		&queuedID, &marker, &evidenceKind, &evidenceRef, &delegatedFrom); err != nil {
		t.Fatal(err)
	}
	var fields map[string]string
	if err := json.Unmarshal(marker, &fields); err != nil || fields["candidate_id"] != uuidToString(before.WorkflowCandidateID) ||
		fields["handoff_id"] != uuidToString(second.ID) || fields["coordinator_task_id"] != coordinatorTaskID ||
		fields["comment_id"] != feedback.ID || evidenceKind != "workflow_human_comment" ||
		evidenceRef != uuidToString(second.ID) || delegatedFrom != coordinatorTaskID {
		t.Fatalf("workflow comment marker incomplete: %s %v %s %s %s", marker, err, evidenceKind, evidenceRef, delegatedFrom)
	}
	forged := dbfx.Task(t, coordinatorID, testutil.Cols{
		"issue_id": issueID, "runtime_id": runtimeID, "status": "queued", "trigger_comment_id": note.ID,
	})
	allowed, err := testHandler.Queries.CheckWorkflowTaskClaimable(ctx, db.CheckWorkflowTaskClaimableParams{TaskID: parseUUID(forged), IssueID: parseUUID(issueID)})
	if err != nil || allowed {
		t.Fatalf("unmarked task crossed member handoff fence: %v %v", allowed, err)
	}
	// The forged task has served its claim-fence assertion; leaving it pending
	// would correctly block correction on its missing human-input provenance.
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='cancelled',completed_at=now() WHERE id=$1`, forged)
	claimed := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claimed.ID != queuedID || claimed.AgentID != coordinatorID {
		t.Fatalf("wrong conversation task claimed: %+v", claimed)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(queuedID)); err != nil {
		t.Fatal(err)
	}
	current, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil {
		t.Fatal(err)
	}
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow/feedback-continuations", map[string]any{
		"candidate_id": uuidToString(current.WorkflowCandidateID), "expected_revision": current.Revision,
		"comment_id": feedback.ID, "kind": "in_scope_defect",
	}), "id", issueID)
	req.Header.Set("X-Agent-ID", coordinatorID)
	req.Header.Set("X-Task-ID", queuedID)
	req.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.ContinueIssueWorkflowFeedback, req).Want(http.StatusOK)
	after, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
	if err != nil || after.WorkflowCandidateID.Valid || after.AssigneeType.String != "agent" || after.AssigneeID != writer {
		t.Fatalf("correction did not return to retained writer: %+v %v", after, err)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, queuedID)
	resumed := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if resumed.AgentID != uuidToString(writer) || resumed.PriorSessionID != "candidate-writer-session" ||
		resumed.PriorWorkDir != "/tmp/workflow-candidate-writer" {
		t.Fatalf("writer continuation used wrong retained session: %+v", resumed)
	}
}

func TestWorkflowHumanCommentUsesCurrentRuntimeWithPinnedPolicy(t *testing.T) {
	f := setupWorkflowHumanCommentFixture(t)
	ctx := context.Background()
	newRuntimeID := createClaimReclaimRuntime(t, nil, "Replacement feedback runtime")
	dbfx.Exec(t, `UPDATE agent SET runtime_id=$2 WHERE id=$1`, f.coordinatorID, newRuntimeID)
	var feedback CommentResponse
	testutil.Call(t, testHandler.CreateComment,
		withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/comments",
			map[string]any{"content": "Can you explain this candidate?"}), "id", f.issueID)).
		Want(http.StatusCreated).JSON(&feedback)
	if len(feedback.TriggerOutcomes) != 1 || feedback.TriggerOutcomes[0].Status != DispatchQueued {
		t.Fatalf("replacement runtime did not get a truthful queued outcome: %+v", feedback.TriggerOutcomes)
	}
	var taskID, taskRuntime string
	var rerun pgtype.UUID
	var fresh bool
	if err := testPool.QueryRow(ctx, `SELECT id::text,runtime_id::text,rerun_of_task_id,force_fresh_session FROM agent_task_queue
		WHERE issue_id=$1 AND trigger_comment_id=$2`, f.issueID, feedback.ID).Scan(&taskID, &taskRuntime, &rerun, &fresh); err != nil {
		t.Fatal(err)
	}
	if taskRuntime != newRuntimeID || rerun.Valid || !fresh {
		t.Fatalf("replacement runtime task inherited a stale session: runtime=%s rerun=%v fresh=%v", taskRuntime, rerun, fresh)
	}
	claimed := claimWorkflowTask(t, newRuntimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claimed.ID != taskID || claimed.PriorSessionID != "" ||
		claimed.PriorWorkDir != "" ||
		claimed.WorkflowProfileID == "" || claimed.WorkflowPolicyVersion == "" {
		t.Fatalf("fresh replacement runtime task=%s profile=%s policy=%s prior_session=%q prior_workdir=%q resume_unavailable=%v",
			claimed.ID, claimed.WorkflowProfileID, claimed.WorkflowPolicyVersion, claimed.PriorSessionID, claimed.PriorWorkDir,
			claimed.PriorSessionResumeUnavailable)
	}
	var sourceProfile string
	if err := testPool.QueryRow(ctx, `SELECT workflow_profile_id::text FROM agent_task_queue WHERE id=$1`, f.coordinatorTaskID).Scan(&sourceProfile); err != nil {
		t.Fatal(err)
	}
	if claimed.WorkflowProfileID != sourceProfile {
		t.Fatalf("replacement runtime captured a different profile: got=%s pinned=%s", claimed.WorkflowProfileID, sourceProfile)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(taskID)); err != nil {
		t.Fatalf("start fresh replacement runtime conversation: %v", err)
	}
	var pinnedVersion string
	if err := testPool.QueryRow(ctx, `SELECT workflow_policy->>'version' FROM issue WHERE id=$1`, f.issueID).Scan(&pinnedVersion); err != nil {
		t.Fatal(err)
	}
	if claimed.WorkflowPolicyVersion != pinnedVersion {
		t.Fatalf("fresh claim policy version=%q, pinned=%q", claimed.WorkflowPolicyVersion, pinnedVersion)
	}
}

func TestWorkflowHumanCommentResumesLatestSameHumanConversation(t *testing.T) {
	f := setupWorkflowHumanCommentFixture(t)
	ctx := context.Background()
	post := func(content string) CommentResponse {
		t.Helper()
		var response CommentResponse
		testutil.Call(t, testHandler.CreateComment,
			withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/comments",
				map[string]any{"content": content}), "id", f.issueID)).
			Want(http.StatusCreated).JSON(&response)
		return response
	}
	first := post("What changed in the candidate?")
	firstClaim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if firstClaim.AgentID != f.coordinatorID || firstClaim.WorkflowProfileID == "" ||
		firstClaim.TriggerCommentID == nil || *firstClaim.TriggerCommentID != first.ID {
		t.Fatalf("first conversation was not claimed with workflow profile: %+v", firstClaim)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(firstClaim.ID)); err != nil {
		t.Fatal(err)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now(),
		session_id='first-feedback-session',work_dir='/tmp/first-feedback' WHERE id=$1`, firstClaim.ID)
	second := post("Can you clarify the answer to my first question?")
	if len(second.TriggerOutcomes) != 1 || second.TriggerOutcomes[0].Status != DispatchQueued {
		t.Fatalf("second conversation did not queue: %+v", second.TriggerOutcomes)
	}
	var secondID, rerunID string
	if err := testPool.QueryRow(ctx, `SELECT id::text,rerun_of_task_id::text FROM agent_task_queue
		WHERE issue_id=$1 AND trigger_comment_id=$2`, f.issueID, second.ID).Scan(&secondID, &rerunID); err != nil {
		t.Fatal(err)
	}
	if rerunID != firstClaim.ID || rerunID == f.coordinatorTaskID {
		t.Fatalf("second conversation did not continue first answer: rerun=%s first=%s", rerunID, firstClaim.ID)
	}
	secondClaim := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if secondClaim.ID != secondID || secondClaim.PriorSessionID != "first-feedback-session" ||
		secondClaim.PriorWorkDir != "/tmp/first-feedback" || secondClaim.WorkflowProfileID != firstClaim.WorkflowProfileID {
		t.Fatalf("second question lost prior answer or pinned workflow: %+v", secondClaim)
	}
}

func TestWorkflowHumanCommentUnavailableCoordinatorBlocksOtherImplicitRoutes(t *testing.T) {
	f := setupWorkflowHumanCommentFixture(t)
	otherAgentID := dbfx.Agent(t, "Unrelated thread agent", f.runtimeID)
	parentID := dbfx.Comment(t, f.issueID, "Unrelated agent's earlier note", testutil.Cols{
		"author_type": "agent", "author_id": otherAgentID,
	})
	dbfx.Exec(t, `UPDATE agent SET archived_at=now() WHERE id=$1`, f.coordinatorID)
	var response CommentResponse
	testutil.Call(t, testHandler.CreateComment,
		withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/comments",
			map[string]any{"content": "Can you explain the current candidate?", "parent_id": parentID}), "id", f.issueID)).
		Want(http.StatusCreated).JSON(&response)
	if len(response.TriggerOutcomes) != 1 || response.TriggerOutcomes[0].TargetID != f.coordinatorID ||
		response.TriggerOutcomes[0].Status != DispatchBlocked || response.TriggerOutcomes[0].ReasonCode != ReasonTargetUnavailable {
		t.Fatalf("unavailable coordinator silently fell through to thread agent: %+v", response.TriggerOutcomes)
	}
	if got := dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND trigger_comment_id=$2`, f.issueID, response.ID); got != 0 {
		t.Fatalf("unavailable coordinator triggered %d unrelated runs", got)
	}
}

func TestWorkflowCorrectionPreservesOtherHumanCommentClaim(t *testing.T) {
	f := setupWorkflowHumanCommentFixture(t)
	ctx := context.Background()
	adminID := dbfx.User(t, "Follow-up reviewer", "follow-up-reviewer@multica.test")
	dbfx.Member(t, testWorkspaceID, adminID, "admin")
	f.allowCoordinatorInvocation(t, adminID)
	post := func(userID, content, parentID string) CommentResponse {
		t.Helper()
		body := map[string]any{"content": content}
		if parentID != "" {
			body["parent_id"] = parentID
		}
		var response CommentResponse
		testutil.Call(t, testHandler.CreateComment,
			withURLParam(newRequestAs(userID, http.MethodPost, "/api/issues/"+f.issueID+"/comments", body), "id", f.issueID)).
			Want(http.StatusCreated).JSON(&response)
		return response
	}
	correction := post(testUserID, "Please fix the missing regression in this candidate.", "")
	followup := post(adminID, "Can you explain the failing case to me?", correction.ID)
	if len(followup.TriggerOutcomes) != 1 || followup.TriggerOutcomes[0].Status != DispatchDeferred {
		t.Fatalf("second human did not receive deferred outcome: %+v", followup.TriggerOutcomes)
	}
	var followupTaskID string
	if err := testPool.QueryRow(ctx, `SELECT id::text FROM agent_task_queue
		WHERE issue_id=$1 AND trigger_comment_id=$2`, f.issueID, followup.ID).Scan(&followupTaskID); err != nil {
		t.Fatal(err)
	}
	classifier := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if classifier.AgentID != f.coordinatorID || classifier.WorkflowProfileID == "" {
		t.Fatalf("classifier did not bind pinned workflow profile: %+v", classifier)
	}
	if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(classifier.ID)); err != nil {
		t.Fatal(err)
	}
	issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(f.issueID))
	if err != nil {
		t.Fatal(err)
	}
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+f.issueID+"/workflow/feedback-continuations", map[string]any{
		"candidate_id": uuidToString(issue.WorkflowCandidateID), "expected_revision": issue.Revision,
		"comment_id": correction.ID, "kind": "in_scope_defect",
	}), "id", f.issueID)
	req.Header.Set("X-Agent-ID", f.coordinatorID)
	req.Header.Set("X-Task-ID", classifier.ID)
	req.Header.Set("X-Actor-Source", "task_token")
	testutil.Call(t, testHandler.ContinueIssueWorkflowFeedback, req).Want(http.StatusOK)
	var preservedStatus, preservedAgent, preservedOriginator string
	var marker []byte
	if err := testPool.QueryRow(ctx, `SELECT status,agent_id::text,originator_user_id::text,
		COALESCE(context->'workflow_feedback','null'::jsonb) FROM agent_task_queue WHERE id=$1`, followupTaskID).Scan(
		&preservedStatus, &preservedAgent, &preservedOriginator, &marker); err != nil {
		t.Fatal(err)
	}
	if preservedStatus != "deferred" || preservedAgent != f.coordinatorID || preservedOriginator != adminID || string(marker) != "null" {
		t.Fatalf("other human comment was discarded or borrowed classifier authority: status=%s agent=%s author=%s marker=%s",
			preservedStatus, preservedAgent, preservedOriginator, marker)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, classifier.ID)
	writer := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if writer.AgentID != uuidToString(f.writer) || writer.PriorSessionID != "candidate-writer-session" ||
		writer.WorkflowProfileID == "" || writer.WorkflowPolicyVersion == "" {
		t.Fatalf("correction did not claim retained writer with pinned profile: %+v", writer)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, writer.ID)
	preserved := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if preserved.ID != followupTaskID || preserved.AgentID != f.coordinatorID ||
		preserved.WorkflowProfileID == "" || preserved.WorkflowPolicyVersion != writer.WorkflowPolicyVersion ||
		preserved.PriorSessionID != "" {
		t.Fatalf("other human's deferred comment did not claim safely under pinned policy: %+v", preserved)
	}
	var delivered bool
	if err := testPool.QueryRow(ctx, `SELECT $2::uuid=ANY(delivered_comment_ids) FROM agent_task_queue WHERE id=$1`,
		followupTaskID, followup.ID).Scan(&delivered); err != nil || !delivered {
		t.Fatalf("preserved comment missing delivery receipt: delivered=%v err=%v", delivered, err)
	}
}

func TestWorkflowHumanCommentKeepsDifferentAuthorsInSeparateRuns(t *testing.T) {
	for _, terminal := range []string{"completed", "failed", "cancelled"} {
		t.Run(terminal, func(t *testing.T) {
			f := setupWorkflowHumanCommentFixture(t)
			ctx := context.Background()
			adminID := dbfx.User(t, "Feedback admin", "feedback-admin@multica.test")
			dbfx.Member(t, testWorkspaceID, adminID, "admin")
			f.allowCoordinatorInvocation(t, adminID)
			post := func(userID, content, parentID string) CommentResponse {
				t.Helper()
				body := map[string]any{"content": content}
				if parentID != "" {
					body["parent_id"] = parentID
				}
				var result CommentResponse
				testutil.Call(t, testHandler.CreateComment,
					withURLParam(newRequestAs(userID, http.MethodPost, "/api/issues/"+f.issueID+"/comments", body), "id", f.issueID)).
					Want(http.StatusCreated).JSON(&result)
				return result
			}
			first := post(testUserID, "The retained candidate needs a corrected regression test.", "")
			second := post(adminID, "Can you also explain this part of the candidate?", first.ID)
			if len(second.TriggerOutcomes) != 1 || second.TriggerOutcomes[0].Status != DispatchDeferred ||
				second.TriggerOutcomes[0].TargetID != f.coordinatorID {
				t.Fatalf("second author did not receive a truthful deferred outcome: %+v", second.TriggerOutcomes)
			}
			var taskID, triggerID, originator string
			var planned []string
			if err := testPool.QueryRow(ctx, `SELECT id::text,trigger_comment_id::text,originator_user_id::text,
				coalesced_comment_ids::text[] FROM agent_task_queue
				WHERE issue_id=$1 AND agent_id=$2 AND status='queued'`, f.issueID, f.coordinatorID).Scan(
				&taskID, &triggerID, &originator, &planned); err != nil {
				t.Fatal(err)
			}
			if triggerID != first.ID || originator != testUserID || len(planned) != 0 {
				t.Fatalf("cross-author comment changed first task: trigger=%s originator=%s planned=%v", triggerID, originator, planned)
			}
			var deferredID, deferredAuthor, deferredStatus string
			if err := testPool.QueryRow(ctx, `SELECT id::text,originator_user_id::text,status FROM agent_task_queue
				WHERE issue_id=$1 AND trigger_comment_id=$2`, f.issueID, second.ID).Scan(
				&deferredID, &deferredAuthor, &deferredStatus); err != nil || deferredAuthor != adminID || deferredStatus != "deferred" {
				t.Fatalf("second author lacks durable deferred task: %s %s %s %v", deferredID, deferredAuthor, deferredStatus, err)
			}
			secondComment, err := testHandler.Queries.GetCommentInWorkspace(ctx, db.GetCommentInWorkspaceParams{
				ID: parseUUID(second.ID), WorkspaceID: parseUUID(testWorkspaceID),
			})
			if err != nil {
				t.Fatal(err)
			}
			replayed := testHandler.triggerTasksForComment(ctx, f.before, secondComment, nil,
				"member", adminID, adminID, nil)
			if len(replayed) != 1 || replayed[0].Status != DispatchDeferred ||
				dbfx.Count(t, `SELECT count(*) FROM agent_task_queue WHERE issue_id=$1 AND trigger_comment_id=$2`, f.issueID, second.ID) != 1 {
				t.Fatalf("replayed deferred comment changed its obligation: %+v", replayed)
			}
			claimed := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
			if claimed.ID != taskID {
				t.Fatalf("wrong first task claimed: %+v", claimed)
			}
			if _, err := testHandler.TaskService.StartTask(ctx, parseUUID(taskID)); err != nil {
				t.Fatal(err)
			}
			dbfx.Exec(t, `UPDATE agent_task_queue SET status=$2,completed_at=now() WHERE id=$1`, taskID, terminal)
			if terminal == "completed" {
				completed, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
				if err != nil {
					t.Fatal(err)
				}
				testHandler.reconcileCommentsOnCompletion(ctx, &completed)
			}
			if err := testHandler.TaskService.PromoteDueDeferredTasksForRuntime(ctx, parseUUID(f.runtimeID)); err != nil {
				t.Fatal(err)
			}
			var followupID, followupAuthor string
			if err := testPool.QueryRow(ctx, `SELECT id::text,originator_user_id::text FROM agent_task_queue
				WHERE issue_id=$1 AND trigger_comment_id=$2 AND status='queued'`, f.issueID, second.ID).Scan(
				&followupID, &followupAuthor); err != nil || followupAuthor != adminID || followupID != deferredID {
				t.Fatalf("second author did not receive separate follow-up: task=%s author=%s err=%v", followupID, followupAuthor, err)
			}
			allowed, err := testHandler.Queries.CheckWorkflowTaskClaimable(ctx, db.CheckWorkflowTaskClaimableParams{
				TaskID: parseUUID(followupID), IssueID: parseUUID(f.issueID),
			})
			if err != nil || !allowed {
				t.Fatalf("second author's own task not claimable: %v %v", allowed, err)
			}
			claimedSecond := claimWorkflowTask(t, f.runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
			if claimedSecond.ID != deferredID || claimedSecond.AgentID != f.coordinatorID || claimedSecond.PriorSessionID != "" {
				t.Fatalf("second human inherited another's session or missed deferred task: %+v", claimedSecond)
			}
		})
	}
}

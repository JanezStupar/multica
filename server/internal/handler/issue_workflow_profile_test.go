package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/dbid"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/skillbundle"
)

func TestIssueWorkflowProfileFirstClaimFreezesBehaviorAcrossLaterQueuedTask(t *testing.T) {
	issueID := dbfx.Issue(t, "First-use execution profile")
	sourceID := insertCompleteWorkflowSkill(t, "---\nname: profile-policy\n---\n\nPinned policy")
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusCreated)
	runtimeID := createClaimReclaimRuntime(t, nil, "profile runtime")
	agentID := dbfx.Agent(t, "profile agent", runtimeID)
	skillID := dbfx.Insert(t, "skill", testutil.Cols{
		"workspace_id": testWorkspaceID, "name": t.Name() + "-skill",
		"description": "Profile skill", "content": "Original agent skill",
		"config": testutil.Raw("'{}'::jsonb"), "created_by": testUserID,
	})
	dbfx.InsertNoID(t, "agent_skill", testutil.Cols{"agent_id": agentID, "skill_id": skillID},
		"agent_id = $1 AND skill_id = $2", agentID, skillID)
	var priorContext string
	if err := testPool.QueryRow(context.Background(), `SELECT COALESCE(context, '') FROM workspace WHERE id=$1`, testWorkspaceID).Scan(&priorContext); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dbfx.Exec(t, `UPDATE workspace SET context=$2 WHERE id=$1`, testWorkspaceID, priorContext) })
	dbfx.Exec(t, `UPDATE workspace SET context='Original workspace context' WHERE id=$1`, testWorkspaceID)
	dbfx.Exec(t, `UPDATE agent SET instructions='Original identity', model='original-model' WHERE id=$1`, agentID)
	older := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "priority": 0})
	triggerID := dbfx.Comment(t, issueID, "Priority request")
	newer := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "priority": 10,
		"trigger_comment_id": triggerID})
	first := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if first.ID != newer || first.Agent.Instructions != "Original identity" || first.Agent.Model != "original-model" ||
		first.WorkspaceContext != "Original workspace context" {
		t.Fatalf("first claim did not capture high-priority profile: %+v", first)
	}
	var profileID, policyVersion string
	if err := testPool.QueryRow(context.Background(), `SELECT workflow_profile_id,workflow_policy_version FROM agent_task_queue WHERE id=$1`, newer).
		Scan(&profileID, &policyVersion); err != nil || profileID == "" || policyVersion == "" {
		t.Fatalf("first task profile binding = %q %q: %v", profileID, policyVersion, err)
	}
	if first.WorkflowProfileID != profileID || first.WorkflowPolicyVersion != policyVersion {
		t.Fatalf("first claim trace identity = %q %q, stored = %q %q", first.WorkflowProfileID, first.WorkflowPolicyVersion, profileID, policyVersion)
	}
	dbfx.Exec(t, `UPDATE agent SET instructions='Changed identity', model='changed-model' WHERE id=$1`, agentID)
	dbfx.Exec(t, `UPDATE workspace SET context='Changed workspace context' WHERE id=$1`, testWorkspaceID)
	dbfx.Exec(t, `UPDATE skill SET content='Changed agent skill' WHERE id=$1`, skillID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, newer)
	second := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if second.ID != older || second.Agent.Instructions != first.Agent.Instructions || second.Agent.Model != first.Agent.Model ||
		second.WorkspaceContext != first.WorkspaceContext {
		t.Fatalf("later task adopted edited behavior: %+v", second)
	}
	var found bool
	for _, skill := range second.Agent.Skills {
		if skill.ID == skillID {
			found = true
			if skill.Content != "Original agent skill" {
				t.Fatalf("later task adopted edited skill: %+v", skill)
			}
		}
	}
	if !found {
		t.Fatal("frozen agent skill missing from later task")
	}
	var secondProfileID, secondPolicyVersion string
	if err := testPool.QueryRow(context.Background(), `SELECT workflow_profile_id,workflow_policy_version FROM agent_task_queue WHERE id=$1`, older).
		Scan(&secondProfileID, &secondPolicyVersion); err != nil || secondProfileID != profileID || secondPolicyVersion != policyVersion {
		t.Fatalf("later task profile binding changed: %q %q vs %q %q: %v", secondProfileID, secondPolicyVersion, profileID, policyVersion, err)
	}
	if second.WorkflowProfileID != profileID || second.WorkflowPolicyVersion != policyVersion {
		t.Fatalf("later claim trace identity changed: %q %q", second.WorkflowProfileID, second.WorkflowPolicyVersion)
	}

	// Slim resolution must use the same complete saved bundles after both the
	// source skill and the agent's live replacement policy become unavailable.
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, older)
	dbfx.Exec(t, `DELETE FROM skill WHERE id=$1`, sourceID)
	dbfx.Exec(t, `UPDATE agent SET builtin_skill_replacements='{"builtin:multica-platform":"invalid-uuid"}'::jsonb WHERE id=$1`, agentID)
	dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	slim := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1+","+protocol.DaemonCapabilitySkillBundlesV1)
	var frozenRef service.AgentSkillRefData
	for _, ref := range slim.Agent.SkillRefs {
		if ref.ID == skillID {
			frozenRef = ref
		}
	}
	if frozenRef.ID != skillID || frozenRef.Hash == "" {
		t.Fatalf("slim claim lost frozen agent skill: %+v", slim.Agent.SkillRefs)
	}
	resolveFrozen := func() []service.AgentSkillData {
		t.Helper()
		req := newDaemonTokenRequest(http.MethodPost, "/", resolveSkillBundlesRequest{Skills: []resolveSkillBundleRef{{ID: frozenRef.ID, Source: skillbundle.SourceWorkspace, Hash: frozenRef.Hash}}}, testWorkspaceID, "workflow-policy-daemon")
		req.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilityPlatformSkillV1+","+protocol.DaemonCapabilitySkillBundlesV1)
		req = withURLParams(req, "runtimeId", runtimeID, "taskId", slim.ID)
		w := testutil.Call(t, testHandler.ResolveTaskSkillBundles, req).Want(http.StatusOK)
		var body struct {
			Bundles []service.AgentSkillData `json:"bundles"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Bundles
	}
	if bundles := resolveFrozen(); len(bundles) != 1 || bundles[0].Content != "Original agent skill" {
		t.Fatalf("slim resolve adopted edited skill: %+v", bundles)
	}
	// A later policy generation must not reinterpret this already bound task.
	otherIssue := dbfx.Issue(t, "Later policy generation")
	t.Run("later policy source", func(t *testing.T) {
		enrollWorkflowPolicy(t, otherIssue, insertCompleteWorkflowSkill(t, "---\nname: later-policy\n---\n\nLater policy")).Want(http.StatusCreated)
	})
	dbfx.Exec(t, `UPDATE issue SET workflow_policy=(SELECT workflow_policy FROM issue WHERE id=$2) WHERE id=$1`, issueID, otherIssue)
	if bundles := resolveFrozen(); len(bundles) != 1 || bundles[0].Content != "Original agent skill" {
		t.Fatalf("historical bound task changed after policy migration: %+v", bundles)
	}
	// The explicit migration attestation opens a new version for future tasks;
	// old task IDs remain tied to their previous profile and skill bytes.
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, slim.ID)
	dbfx.Exec(t, `UPDATE issue SET workflow_migrated_at=now() WHERE id=$1`, issueID)
	dbfx.Exec(t, `UPDATE agent SET builtin_skill_replacements='{}'::jsonb WHERE id=$1`, agentID)
	dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	migrated := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if migrated.WorkflowProfileID == profileID || migrated.WorkflowPolicyVersion == policyVersion ||
		migrated.Agent.Instructions != "Changed identity" || migrated.Agent.Model != "changed-model" {
		t.Fatalf("explicitly migrated issue did not select new profile generation: %+v", migrated)
	}
}

func TestIssueWorkflowProfileReselectionScopesOverrideAndPreservesLineage(t *testing.T) {
	issueID := dbfx.Issue(t, "Explicit profile reselection")
	enrollWorkflowPolicy(t, issueID, insertCompleteWorkflowSkill(t, "---\nname: reselect-policy\n---\n\nPinned policy")).Want(http.StatusCreated)
	runtimeID := createClaimReclaimRuntime(t, nil, "profile reselection runtime")
	agentID := dbfx.Agent(t, "profile reselection agent", runtimeID)
	dbfx.Exec(t, `UPDATE agent SET instructions='Original identity',model='old-model' WHERE id=$1`, agentID)
	dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	first := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',started_at=COALESCE(started_at,now()),
		completed_at=now(),session_id='old-profile-session' WHERE id=$1`, first.ID)
	dbfx.Exec(t, `UPDATE agent SET instructions='Selected identity',model='new-model' WHERE id=$1`, agentID)

	requestID := uuidToString(dbid.NewV7())
	body := map[string]any{
		"agent_id": agentID, "expected_profile_id": first.WorkflowProfileID,
		"request_key": requestID, "reason": "Use the updated specialist instructions",
		"consequences":              "Future deliberate turns start a fresh provider session",
		"reconciliation":            "Prior runs and retries keep the old execution profile",
		"supplemental_instructions": "Use the ticket's concise reporting format",
	}
	reselect := func(input map[string]any) *http.Request {
		return withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow-profile/reselect", input), "id", issueID)
	}
	var selected service.IssueWorkflowProfileSelection
	testutil.Call(t, testHandler.ReselectIssueWorkflowProfile, reselect(body)).Want(http.StatusCreated).JSON(&selected)
	if selected.ProfileID == first.WorkflowProfileID || selected.PreviousProfileID != first.WorkflowProfileID ||
		selected.PolicyVersion != first.WorkflowPolicyVersion || selected.Revision != 2 ||
		selected.Scope != "issue_agent_until_reselected" || !selected.SupplementalInstructionsActive {
		t.Fatalf("unexpected profile selection: %+v", selected)
	}
	var replay service.IssueWorkflowProfileSelection
	testutil.Call(t, testHandler.ReselectIssueWorkflowProfile, reselect(body)).Want(http.StatusCreated).JSON(&replay)
	if replay.ProfileID != selected.ProfileID {
		t.Fatalf("idempotent request changed profile: %+v", replay)
	}
	changedIntent := map[string]any{}
	for key, value := range body {
		changedIntent[key] = value
	}
	delete(changedIntent, "supplemental_instructions")
	testutil.Call(t, testHandler.ReselectIssueWorkflowProfile, reselect(changedIntent)).Want(http.StatusConflict)
	conflicting := map[string]any{}
	for key, value := range body {
		conflicting[key] = value
	}
	conflicting["request_key"] = uuidToString(dbid.NewV7())
	testutil.Call(t, testHandler.ReselectIssueWorkflowProfile, reselect(conflicting)).Want(http.StatusConflict)

	newID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	busy := map[string]any{}
	for key, value := range body {
		busy[key] = value
	}
	busy["request_key"] = uuidToString(dbid.NewV7())
	busy["expected_profile_id"] = selected.ProfileID
	testutil.Call(t, testHandler.ReselectIssueWorkflowProfile, reselect(busy)).Want(http.StatusConflict)
	next := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if next.ID != newID || next.WorkflowProfileID != selected.ProfileID || next.Agent.Model != "new-model" ||
		!strings.Contains(next.Agent.Instructions, "Selected identity") ||
		!strings.Contains(next.Agent.Instructions, "Use the ticket's concise reporting format") ||
		next.PriorSessionID != "" || !next.PriorSessionResumeUnavailable {
		t.Fatalf("new deliberate turn did not use selected fresh profile: %+v", next)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, next.ID)

	retryID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "retry_of_task_id": first.ID})
	retry := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if retry.ID != retryID || retry.WorkflowProfileID != first.WorkflowProfileID || retry.Agent.Model != "old-model" ||
		retry.Agent.Instructions != "Original identity" {
		t.Fatalf("retry adopted new profile instead of source binding: %+v", retry)
	}
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, retry.ID)
	rerunID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "rerun_of_task_id": first.ID})
	rerun := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if rerun.ID != rerunID || rerun.WorkflowProfileID != first.WorkflowProfileID || rerun.Agent.Instructions != "Original identity" {
		t.Fatalf("named rerun adopted new profile instead of source binding: %+v", rerun)
	}
}

func TestIssueWorkflowProfileReselectionCannotRetuneAnotherMembersPrivateAgent(t *testing.T) {
	issueID := dbfx.Issue(t, "Private profile reselection")
	enrollWorkflowPolicy(t, issueID, insertCompleteWorkflowSkill(t, "---\nname: private-policy\n---\n\nPinned policy")).Want(http.StatusCreated)
	otherOwner := createTestUserAndMember(t, "member")
	runtimeID := createClaimReclaimRuntime(t, nil, "private profile runtime")
	agentID := dbfx.Agent(t, "private profile agent", runtimeID)
	dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	first := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, first.ID)
	dbfx.Exec(t, `UPDATE agent SET owner_id=$2,permission_mode='private' WHERE id=$1`, agentID, otherOwner)
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow-profile/reselect", map[string]any{
		"agent_id": agentID, "expected_profile_id": first.WorkflowProfileID,
		"request_key": uuidToString(dbid.NewV7()), "reason": "Try to retune private agent",
		"consequences": "New instructions", "reconciliation": "No running task",
		"supplemental_instructions": "Unauthorized instruction",
	}), "id", issueID)
	testutil.Call(t, testHandler.ReselectIssueWorkflowProfile, req).Want(http.StatusForbidden)
	var count int
	dbfx.QueryRow(t, `SELECT count(*) FROM issue_workflow_profile WHERE issue_id=$1 AND agent_id=$2`, issueID, agentID).Scan(&count)
	if count != 1 {
		t.Fatalf("private agent profile count = %d, want 1", count)
	}
}

func TestIssueWorkflowProfileChangedCustomArgsRefusesLaterClaim(t *testing.T) {
	issueID := dbfx.Issue(t, "Profile custom argument guard")
	enrollWorkflowPolicy(t, issueID, insertCompleteWorkflowSkill(t, "---\nname: args-policy\n---\n\nPinned policy")).Want(http.StatusCreated)
	runtimeID := createClaimReclaimRuntime(t, nil, "profile args runtime")
	agentID := dbfx.Agent(t, "profile args agent", runtimeID)
	dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	first := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, first.ID)
	dbfx.Exec(t, `UPDATE agent SET custom_args='["--changed"]'::jsonb WHERE id=$1`, agentID)
	dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	req := newDaemonTokenRequest(http.MethodPost, "/", nil, testWorkspaceID, "workflow-policy-daemon")
	req.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilityPlatformSkillV1)
	req = withURLParam(req, "runtimeId", runtimeID)
	testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM agent_task_queue WHERE issue_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, issueID).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("changed custom args task status = %q: %v", status, err)
	}
}

func TestIssueWorkflowProfileCorruptionFailsBeforeNextLaunch(t *testing.T) {
	issueID := dbfx.Issue(t, "Corrupt profile")
	enrollWorkflowPolicy(t, issueID, insertCompleteWorkflowSkill(t, "---\nname: corrupt-policy\n---\n\nPinned policy")).Want(http.StatusCreated)
	runtimeID := createClaimReclaimRuntime(t, nil, "corrupt profile runtime")
	agentID := dbfx.Agent(t, "corrupt profile agent", runtimeID)
	dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	first := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status='completed',completed_at=now() WHERE id=$1`, first.ID)
	dbfx.Exec(t, `UPDATE issue_workflow_profile SET snapshot=jsonb_set(snapshot,'{agent_instructions}','"tampered"'::jsonb) WHERE id=$1`, first.WorkflowProfileID)
	nextID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	req := newDaemonTokenRequest(http.MethodPost, "/", nil, testWorkspaceID, "workflow-policy-daemon")
	req.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilityPlatformSkillV1)
	req = withURLParam(req, "runtimeId", runtimeID)
	testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)
	var status string
	if err := testPool.QueryRow(context.Background(), `SELECT status FROM agent_task_queue WHERE id=$1`, nextID).Scan(&status); err != nil || status != "failed" {
		t.Fatalf("corrupt profile task status = %q: %v", status, err)
	}
}

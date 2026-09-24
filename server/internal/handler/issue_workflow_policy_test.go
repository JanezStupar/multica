package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/testutil"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/skillbundle"
)

func insertCompleteWorkflowSkill(t *testing.T, content string) string {
	t.Helper()
	id := dbfx.Insert(t, "skill", testutil.Cols{
		"workspace_id": testWorkspaceID, "name": t.Name() + "-workflow",
		"description": "Pinned issue policy", "content": content,
		"config": testutil.Raw("'{}'::jsonb"), "created_by": testUserID,
	})
	for _, builtin := range testHandler.TaskService.BuiltinSkills("", false) {
		if builtin.Name != service.PlatformSkillName {
			continue
		}
		for _, file := range builtin.Files {
			dbfx.Insert(t, "skill_file", testutil.Cols{
				"skill_id": id, "path": file.Path, "content": file.Content,
			})
		}
	}
	dbfx.Insert(t, "skill_file", testutil.Cols{
		"skill_id": id, "path": "runtime/issue-workflow.md", "content": "Frozen workflow version one",
	})
	return id
}

func enrollWorkflowPolicy(t *testing.T, issueID, skillID string) *testutil.Response {
	t.Helper()
	req := withURLParam(newRequest(http.MethodPost, "/api/issues/"+issueID+"/workflow-policy", map[string]any{"skill_id": skillID}), "id", issueID)
	return testutil.Call(t, testHandler.EnrollIssueWorkflowPolicy, req)
}

func claimWorkflowTask(t *testing.T, runtimeID, capability string) *AgentTaskResponse {
	t.Helper()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/runtimes/"+runtimeID+"/tasks/claim", nil, testWorkspaceID, "workflow-policy-daemon")
	req.Header.Set("X-Client-Capabilities", capability)
	req = withURLParam(req, "runtimeId", runtimeID)
	w := testutil.Call(t, testHandler.ClaimTaskByRuntime, req).Want(http.StatusOK)
	var body struct {
		Task *AgentTaskResponse `json:"task"`
	}
	w.JSON(&body)
	if body.Task == nil || body.Task.Agent == nil {
		t.Fatalf("claim body: %s", w.Body.String())
	}
	return body.Task
}

func TestIssueWorkflowPolicyPinSurvivesSourceAndAgentChangesAcrossClaims(t *testing.T) {
	issueID := dbfx.Issue(t, "Pinned workflow issue")
	sourceID := insertCompleteWorkflowSkill(t, "---\nname: pinned-workflow\n---\n\nOriginal policy")
	first := enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusCreated)
	var pinned service.IssueWorkflowPolicy
	first.JSON(&pinned)
	if pinned.Version == "" || pinned.Bundle.Hash != pinned.Version || pinned.SourceSkillID != sourceID {
		t.Fatalf("pin response: %+v", pinned)
	}
	get := testutil.Call(t, testHandler.GetIssueWorkflowPolicy,
		withURLParam(newRequest(http.MethodGet, "/", nil), "id", issueID)).Want(http.StatusOK)
	var readback service.IssueWorkflowPolicy
	get.JSON(&readback)
	if readback.Version != pinned.Version || readback.Bundle.Content != pinned.Bundle.Content || len(readback.Bundle.Files) != len(pinned.Bundle.Files) {
		t.Fatalf("readback changed snapshot: %+v", readback)
	}
	var replay service.IssueWorkflowPolicy
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusOK).JSON(&replay)
	if replay.Version != pinned.Version {
		t.Fatalf("same-source retry changed policy: %+v", replay)
	}

	runtime1 := createClaimReclaimRuntime(t, nil, "pinned full runtime")
	agent1 := dbfx.Agent(t, "pinned full agent", runtime1)
	// The source changes after enrollment. A new agent replacement points at
	// a different attached workspace skill; neither may reinterpret the pin.
	currentID := dbfx.Insert(t, "skill", testutil.Cols{
		"workspace_id": testWorkspaceID, "name": t.Name() + "-current",
		"description": "current", "content": "Current agent replacement",
		"config": testutil.Raw("'{}'::jsonb"), "created_by": testUserID,
	})
	dbfx.InsertNoID(t, "agent_skill", testutil.Cols{"agent_id": agent1, "skill_id": currentID}, "agent_id = $1 AND skill_id = $2", agent1, currentID)
	dbfx.Exec(t, `UPDATE agent SET builtin_skill_replacements = jsonb_build_object($2::text, $3::text) WHERE id = $1`,
		agent1, service.BuiltinSkillID(service.PlatformSkillName), currentID)
	dbfx.Exec(t, `UPDATE skill SET content = 'Changed live policy' WHERE id = $1`, sourceID)
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusConflict)
	dbfx.Task(t, agent1, testutil.Cols{"runtime_id": runtime1, "issue_id": issueID})
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusConflict)
	full := claimWorkflowTask(t, runtime1, protocol.DaemonCapabilityPlatformSkillV1)
	var found int
	for _, bundle := range full.Agent.Skills {
		if bundle.ReplacesBuiltin == service.BuiltinSkillID(service.PlatformSkillName) {
			found++
			if bundle.ID != sourceID || bundle.Content != pinned.Bundle.Content || bundle.Hash != pinned.Version {
				t.Fatalf("full claim changed pinned policy: %+v", bundle)
			}
		}
		if bundle.ID == currentID || bundle.ID == service.BuiltinSkillID(service.PlatformSkillName) {
			t.Fatalf("competing workflow reached full claim: %+v", bundle)
		}
	}
	if found != 1 {
		t.Fatalf("full claim platform bundles = %d", found)
	}

	// A second agent and later run still receive the original snapshot after
	// the source skill has been removed entirely.
	dbfx.Exec(t, `DELETE FROM skill WHERE id = $1`, sourceID)
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1`, full.ID)
	runtime2 := createClaimReclaimRuntime(t, nil, "pinned slim runtime")
	agent2 := dbfx.Agent(t, "pinned slim agent", runtime2)
	dbfx.Task(t, agent2, testutil.Cols{"runtime_id": runtime2, "issue_id": issueID})
	slim := claimWorkflowTask(t, runtime2, protocol.DaemonCapabilityPlatformSkillV1+","+protocol.DaemonCapabilitySkillBundlesV1)
	var ref service.AgentSkillRefData
	for _, candidate := range slim.Agent.SkillRefs {
		if candidate.ReplacesBuiltin == service.BuiltinSkillID(service.PlatformSkillName) {
			ref = candidate
		}
		if candidate.ID == service.BuiltinSkillID(service.PlatformSkillName) {
			t.Fatalf("old built-in reached slim claim: %+v", candidate)
		}
	}
	if ref.ID != sourceID || ref.Hash != pinned.Version {
		t.Fatalf("slim claim lost pin: %+v", ref)
	}
	resolve := func(bundleRef resolveSkillBundleRef) (int, []service.AgentSkillData) {
		t.Helper()
		req := newDaemonTokenRequest(http.MethodPost, "/", resolveSkillBundlesRequest{Skills: []resolveSkillBundleRef{bundleRef}}, testWorkspaceID, "workflow-policy-daemon")
		req.Header.Set("X-Client-Capabilities", protocol.DaemonCapabilityPlatformSkillV1+","+protocol.DaemonCapabilitySkillBundlesV1)
		req = withURLParams(req, "runtimeId", runtime2, "taskId", slim.ID)
		w := testutil.Call(t, testHandler.ResolveTaskSkillBundles, req)
		var body struct {
			Bundles []service.AgentSkillData `json:"bundles"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body.Bundles
	}
	status, bundles := resolve(resolveSkillBundleRef{ID: ref.ID, Source: skillbundle.SourceWorkspace, Hash: ref.Hash})
	if status != http.StatusOK || len(bundles) != 1 || bundles[0].Content != pinned.Bundle.Content || bundles[0].Hash != pinned.Version {
		t.Fatalf("resolved snapshot: %d %+v", status, bundles)
	}
	status, _ = resolve(resolveSkillBundleRef{ID: service.BuiltinSkillID(service.PlatformSkillName), Source: skillbundle.SourceBuiltin, Hash: "sha256:old"})
	if status != http.StatusNotFound {
		t.Fatalf("old built-in resolve status %d", status)
	}
}

func TestIssueWorkflowPolicyEnrollmentRejectsUnsafeInputAndHistory(t *testing.T) {
	issueID := dbfx.Issue(t, "Policy admission")
	sourceID := insertCompleteWorkflowSkill(t, "Initial workflow")
	if w := enrollWorkflowPolicy(t, issueID, "not-a-uuid"); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed ID: %d %s", w.Code, w.Body.String())
	}
	foreignID := insertHandlerTestSkillInForeignWorkspace(t, "foreign-policy", "foreign")
	if w := enrollWorkflowPolicy(t, issueID, foreignID); w.Code != http.StatusNotFound {
		t.Fatalf("foreign skill: %d %s", w.Code, w.Body.String())
	}
	unsafeFile := dbfx.Insert(t, "skill_file", testutil.Cols{
		"skill_id": sourceID, "path": "../../escape.md", "content": "unsafe",
	})
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusBadRequest)
	dbfx.Exec(t, `DELETE FROM skill_file WHERE id = $1`, unsafeFile)
	dbfx.Exec(t, `UPDATE skill_file SET content = '  ' WHERE skill_id = $1 AND path = 'runtime/issue-workflow.md'`, sourceID)
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusBadRequest)
	dbfx.Exec(t, `UPDATE skill_file SET content = 'Ready workflow' WHERE skill_id = $1 AND path = 'runtime/issue-workflow.md'`, sourceID)
	member := dbfx.User(t, "policy member", "policy-member-"+t.Name()+"@multica.test")
	dbfx.Member(t, testWorkspaceID, member, "member")
	memberReq := withURLParam(newRequestAs(member, http.MethodPost, "/", map[string]any{"skill_id": sourceID}), "id", issueID)
	w := testutil.Call(t, testHandler.EnrollIssueWorkflowPolicy, memberReq)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member enrolled policy: %d %s", w.Code, w.Body.String())
	}
	machineReq := withURLParam(newRequest(http.MethodPost, "/", map[string]any{"skill_id": sourceID}), "id", issueID)
	machineReq.Header.Set("X-Actor-Source", "task_token")
	w = testutil.Call(t, testHandler.EnrollIssueWorkflowPolicy, machineReq)
	if w.Code != http.StatusForbidden {
		t.Fatalf("task token enrolled policy: %d %s", w.Code, w.Body.String())
	}
	agent := dbfx.Agent(t, "policy history agent", testRuntimeID)
	dbfx.Task(t, agent, testutil.Cols{"runtime_id": testRuntimeID, "issue_id": issueID, "status": "completed", "completed_at": testutil.Raw("now()")})
	if w := enrollWorkflowPolicy(t, issueID, sourceID); w.Code != http.StatusConflict {
		t.Fatalf("issue with task history enrolled: %d %s", w.Code, w.Body.String())
	}
	get := testutil.Call(t, testHandler.GetIssueWorkflowPolicy,
		withURLParam(newRequest(http.MethodGet, "/", nil), "id", issueID))
	if get.Code != http.StatusNotFound {
		t.Fatalf("rejected pin persisted: %d %s", get.Code, get.Body.String())
	}
}

func TestIssueWorkflowPolicyEnrollmentDoesNotWaitOnIssueFirstEnqueuer(t *testing.T) {
	issueID := dbfx.Issue(t, "Policy row lock")
	sourceID := insertCompleteWorkflowSkill(t, "Initial workflow")
	tx, err := testPool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var locked string
	if err := tx.QueryRow(context.Background(), `SELECT id FROM issue WHERE id = $1 FOR UPDATE`, issueID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusConflict)
	if time.Since(started) > time.Second {
		t.Fatal("enrollment waited while holding the task table against an issue-first writer")
	}
	testutil.Call(t, testHandler.GetIssueWorkflowPolicy,
		withURLParam(newRequest(http.MethodGet, "/", nil), "id", issueID)).Want(http.StatusNotFound)
}

func TestPinnedIssueRejectsLegacyDaemonAndOmitsLiveMikaWorkflow(t *testing.T) {
	issueID := dbfx.Issue(t, "Mika policy pin")
	sourceID := insertCompleteWorkflowSkill(t, "Mika's pinned workflow")
	enrollWorkflowPolicy(t, issueID, sourceID).Want(http.StatusCreated)
	runtimeID := createClaimReclaimRuntime(t, nil, "pinned mika runtime")
	agentID := dbfx.Agent(t, "pinned mika agent", runtimeID, testutil.Cols{
		"system_key":   service.MikaSystemKey,
		"instructions": "Only workspace notes are live",
	})
	taskID := dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID})
	legacyReq := withURLParam(newDaemonTokenRequest(http.MethodPost, "/", nil, testWorkspaceID, "workflow-policy-daemon"), "runtimeId", runtimeID)
	testutil.Call(t, testHandler.ClaimTaskByRuntime, legacyReq).Want(http.StatusInternalServerError)
	var status string
	dbfx.QueryRow(t, `SELECT status FROM agent_task_queue WHERE id = $1`, taskID).Scan(&status)
	if status != "dispatched" {
		t.Fatalf("legacy daemon changed task status: %s", status)
	}
	// The claim failure preserves the task for reclaim. Return it to queued
	// in this fixture to exercise the modern daemon's eventual retry.
	dbfx.Exec(t, `UPDATE agent_task_queue SET status = 'queued', dispatched_at = NULL WHERE id = $1`, taskID)
	claimed := claimWorkflowTask(t, runtimeID, protocol.DaemonCapabilityPlatformSkillV1)
	if claimed.Agent.Instructions != "Only workspace notes are live" ||
		strings.Contains(claimed.Agent.Instructions, service.MikaSystemInstructions("pinned mika agent")) {
		t.Fatalf("compiled Mika workflow reached pinned issue: %q", claimed.Agent.Instructions)
	}
}

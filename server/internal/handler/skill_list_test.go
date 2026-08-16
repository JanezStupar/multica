package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// TestListSkills_OmitsContent guards the fix for GH multica-ai/multica#2174:
// the workspace skill list endpoint must not ship the SKILL.md `content`
// blob, which used to bloat the payload past CLI timeouts on workspaces with
// many large skills. The detail endpoint still returns content (covered by
// TestGetSkill_IncludesContent below).
func TestListSkills_OmitsContent(t *testing.T) {
	skillID := insertHandlerTestSkill(t, "list-omits-content", strings.Repeat("a", 4096))

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/skills?workspace_id="+testWorkspaceID, nil)
	testHandler.ListSkills(w, req)
	if w.Code != 200 {
		t.Fatalf("ListSkills: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Decode into a generic shape so we can prove the wire format has no
	// `content` field at all — not "content present but empty", which would
	// still leave the bytes on the wire.
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("ListSkills: failed to decode body: %v", err)
	}

	var found bool
	for _, row := range rows {
		if row["id"] != skillID {
			continue
		}
		found = true
		if _, ok := row["content"]; ok {
			t.Fatalf("ListSkills: response must not include `content` field, got: %v", row)
		}
		// Other expected list fields should still be present.
		for _, key := range []string{"id", "name", "description", "config", "created_at", "updated_at", "workspace_id"} {
			if _, ok := row[key]; !ok {
				t.Fatalf("ListSkills: missing expected field %q in response: %v", key, row)
			}
		}
	}
	if !found {
		t.Fatalf("ListSkills: inserted skill %s not in response", skillID)
	}
}

// TestGetSkill_IncludesContent confirms the detail endpoint still ships the
// full SKILL.md body — the list-summary change must not regress single-skill
// reads.
func TestGetSkill_IncludesContent(t *testing.T) {
	body := "# detail body\nstill served on /api/skills/{id}"
	skillID := insertHandlerTestSkill(t, "detail-includes-content", body)

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/skills/"+skillID, nil)
	req = withURLParam(req, "id", skillID)
	testHandler.GetSkill(w, req)
	if w.Code != 200 {
		t.Fatalf("GetSkill: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("GetSkill: failed to decode body: %v", err)
	}
	if got, _ := resp["content"].(string); got != body {
		t.Fatalf("GetSkill: expected content %q, got %q", body, got)
	}
}

// TestListAgentSkills_OmitsContent: same constraint for the agent-scoped
// listing — gpt-boy review of the original fix flagged this as a sister case
// because `multica agent skills list` follows the same shape rules.
func TestListAgentSkills_OmitsContent(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Skill Summary Test", nil)
	skillID := insertHandlerTestSkill(t, "agent-skill-omits-content", strings.Repeat("b", 1024))
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`,
		agentID, skillID,
	); err != nil {
		t.Fatalf("attach skill to agent: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/agents/"+agentID+"/skills", nil)
	req = withURLParam(req, "id", agentID)
	testHandler.ListAgentSkills(w, req)
	if w.Code != 200 {
		t.Fatalf("ListAgentSkills: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatalf("ListAgentSkills: failed to decode body: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("ListAgentSkills: expected at least 1 skill")
	}
	for _, row := range rows {
		if _, ok := row["content"]; ok {
			t.Fatalf("ListAgentSkills: response must not include `content` field, got: %v", row)
		}
	}
}

func TestSetAgentSkillEnabledControlsExecutionWithoutRemovingAssignment(t *testing.T) {
	agentID := createHandlerTestAgent(t, "Handler Skill Toggle Test", nil)
	skillID := insertHandlerTestSkill(t, "agent-skill-toggle", "# Toggle me")
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)`,
		agentID, skillID,
	); err != nil {
		t.Fatalf("attach skill to agent: %v", err)
	}

	setEnabled := func(enabled bool) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("PUT", "/api/agents/"+agentID+"/skills/"+skillID+"/enabled", map[string]any{
			"enabled": enabled,
		})
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", agentID)
		rctx.URLParams.Add("skillId", skillID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		testHandler.SetAgentSkillEnabled(w, req)
		return w
	}

	if w := setEnabled(false); w.Code != 200 {
		t.Fatalf("disable skill: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Disabled assignments remain visible to management surfaces.
	rows, err := testHandler.Queries.ListAgentSkillSummaries(context.Background(), parseUUID(agentID))
	if err != nil || len(rows) != 1 || rows[0].Enabled {
		t.Fatalf("disabled assignment not preserved: rows=%+v err=%v", rows, err)
	}
	// Execution only receives enabled skills.
	active, err := testHandler.Queries.ListAgentSkills(context.Background(), parseUUID(agentID))
	if err != nil || len(active) != 0 {
		t.Fatalf("disabled skill leaked into execution: skills=%+v err=%v", active, err)
	}

	if w := setEnabled(true); w.Code != 200 {
		t.Fatalf("enable skill: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	active, err = testHandler.Queries.ListAgentSkills(context.Background(), parseUUID(agentID))
	if err != nil || len(active) != 1 || active[0].Name == "" {
		t.Fatalf("re-enabled skill missing from execution: skills=%+v err=%v", active, err)
	}
}

func TestSetAgentRuntimeSkillEnabledPersistsScopedOverride(t *testing.T) {
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, permission_mode, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'local', '{}'::jsonb, $3, 'private', 'private', 1, $4)
		RETURNING id
	`, testWorkspaceID, "Runtime Skill Toggle "+t.Name(), runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	setEnabled := func(enabled bool) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("PUT", "/api/agents/"+agentID+"/runtime-skills/enabled", map[string]any{
			"runtime_id": runtimeID,
			"root":       "provider",
			"key":        "review",
			"name":       "Review Helper",
			"enabled":    enabled,
		})
		req = withURLParam(req, "id", agentID)
		testHandler.SetAgentRuntimeSkillEnabled(w, req)
		return w
	}

	if w := setEnabled(false); w.Code != 204 {
		t.Fatalf("disable inherited skill: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	row, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	disabled := decodeDisabledRuntimeSkills(row.DisabledRuntimeSkills)
	if len(disabled) != 1 || disabled[0].RuntimeID != runtimeID ||
		disabled[0].Provider != "claude" || disabled[0].Root != "provider" || disabled[0].Key != "review" ||
		disabled[0].Name != "Review Helper" {
		t.Fatalf("unexpected disabled runtime skills: %+v", disabled)
	}
	if w := setEnabled(false); w.Code != 204 {
		t.Fatalf("repeat disable inherited skill: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	row, err = testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil || len(decodeDisabledRuntimeSkills(row.DisabledRuntimeSkills)) != 1 {
		t.Fatalf("repeat disable was not idempotent: row=%+v err=%v", row, err)
	}

	if w := setEnabled(true); w.Code != 204 {
		t.Fatalf("enable inherited skill: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	row, err = testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("reload agent: %v", err)
	}
	if disabled = decodeDisabledRuntimeSkills(row.DisabledRuntimeSkills); len(disabled) != 0 {
		t.Fatalf("re-enabled skill still disabled: %+v", disabled)
	}
}

func TestSetAgentBuiltinSkillEnabledCreatesExactAllowlist(t *testing.T) {
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			visibility, permission_mode, max_concurrent_tasks, owner_id
		)
		VALUES ($1, $2, '', 'local', '{}'::jsonb, 'private', 'private', 1, $3)
		RETURNING id
	`, testWorkspaceID, "Built-in Skill Toggle "+t.Name(), testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	builtins := testHandler.TaskService.BuiltinSkills()
	if len(builtins) < 2 {
		t.Fatalf("need at least two built-ins, got %d", len(builtins))
	}
	targetID := service.BuiltinSkillID(builtins[0].Name)
	setEnabled := func(skillID string, enabled bool) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := newRequest("PUT", "/api/agents/"+agentID+"/builtin-skills/enabled", map[string]any{
			"skill_id": skillID,
			"enabled":  enabled,
		})
		req = withURLParam(req, "id", agentID)
		testHandler.SetAgentBuiltinSkillEnabled(w, req)
		return w
	}

	if w := setEnabled(targetID, false); w.Code != 204 {
		t.Fatalf("disable built-in: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	row, err := testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	if row.EnabledBuiltinSkillIds == nil {
		t.Fatal("first edit must replace inherit-all with an explicit allowlist")
	}
	if len(row.EnabledBuiltinSkillIds) != len(builtins)-1 {
		t.Fatalf("enabled ids = %d, want %d", len(row.EnabledBuiltinSkillIds), len(builtins)-1)
	}
	for _, id := range row.EnabledBuiltinSkillIds {
		if id == targetID {
			t.Fatalf("disabled built-in %s remained enabled", targetID)
		}
	}
	{
		w := httptest.NewRecorder()
		req := newRequest("GET", "/api/agents/"+agentID, nil)
		req = withURLParam(req, "id", agentID)
		testHandler.GetAgent(w, req)
		if w.Code != 200 {
			t.Fatalf("get customized agent: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var response AgentResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode customized agent: %v", err)
		}
		if response.EnabledBuiltinSkillIDs == nil || len(response.BuiltinSkills) != len(builtins) {
			t.Fatalf("agent detail omitted built-in policy/inventory: %+v", response)
		}
		for _, summary := range response.BuiltinSkills {
			if summary.ID == targetID && summary.Enabled {
				t.Fatalf("agent detail rendered disabled built-in %s as enabled", targetID)
			}
		}
	}
	resolved, refs := testHandler.TaskService.LoadAgentSkillBundles(
		context.Background(), parseUUID(agentID), row.EnabledBuiltinSkillIds,
	)
	if len(resolved) != len(builtins)-1 || len(refs) != len(builtins)-1 {
		t.Fatalf("execution resolved %d bundles/%d refs, want %d", len(resolved), len(refs), len(builtins)-1)
	}
	for _, ref := range refs {
		if ref.ID == targetID {
			t.Fatalf("disabled built-in %s leaked into execution refs", targetID)
		}
	}

	// Preserve an unknown ID written by a newer server during rolling deploys.
	const futureID = "builtin:future-skill"
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent SET enabled_builtin_skill_ids = array_append(enabled_builtin_skill_ids, $2)
		WHERE id = $1
	`, agentID, futureID); err != nil {
		t.Fatalf("seed future built-in id: %v", err)
	}
	if w := setEnabled(targetID, true); w.Code != 204 {
		t.Fatalf("re-enable built-in: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	row, err = testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("reload agent: %v", err)
	}
	foundTarget, foundFuture := false, false
	for _, id := range row.EnabledBuiltinSkillIds {
		foundTarget = foundTarget || id == targetID
		foundFuture = foundFuture || id == futureID
	}
	if !foundTarget || !foundFuture {
		t.Fatalf("re-enabled/forward-compatible ids missing: %v", row.EnabledBuiltinSkillIds)
	}

	before := append([]string(nil), row.EnabledBuiltinSkillIds...)
	if w := setEnabled("builtin:not-real", false); w.Code != 400 {
		t.Fatalf("unknown built-in: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	row, err = testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil || strings.Join(row.EnabledBuiltinSkillIds, "\x00") != strings.Join(before, "\x00") {
		t.Fatalf("unknown built-in changed policy: before=%v after=%v err=%v", before, row.EnabledBuiltinSkillIds, err)
	}

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/agents/"+agentID+"/builtin-skills", nil)
	req = withURLParam(req, "id", agentID)
	testHandler.ResetAgentBuiltinSkills(w, req)
	if w.Code != 204 {
		t.Fatalf("reset built-ins: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	row, err = testHandler.Queries.GetAgent(context.Background(), parseUUID(agentID))
	if err != nil {
		t.Fatalf("reload reset agent: %v", err)
	}
	if row.EnabledBuiltinSkillIds != nil {
		t.Fatalf("reset policy = %v, want nil inherit-all", row.EnabledBuiltinSkillIds)
	}
	resolved, refs = testHandler.TaskService.LoadAgentSkillBundles(
		context.Background(), parseUUID(agentID), row.EnabledBuiltinSkillIds,
	)
	if len(resolved) != len(builtins) || len(refs) != len(builtins) {
		t.Fatalf("reset execution resolved %d bundles/%d refs, want all %d", len(resolved), len(refs), len(builtins))
	}
}

// TestGetSkill_MalformedUUIDReturns400 guards the handler UUID parsing
// convention (CLAUDE.md → "Backend Handler UUID Parsing Convention"): raw
// `id` URL params on the request boundary must be validated with
// parseUUIDOrBadRequest, not the panic-prone parseUUID. Before the fix
// the malformed input panicked in MustParseUUID and was rescued by the
// chi Recoverer middleware as a 500.
func TestGetSkill_MalformedUUIDReturns400(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/skills/not-a-uuid", nil)
	req = withURLParam(req, "id", "not-a-uuid")
	testHandler.GetSkill(w, req)
	if w.Code != 400 {
		t.Fatalf("GetSkill malformed uuid: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// insertHandlerTestSkill writes a skill row directly via SQL and registers a
// cleanup hook. We bypass the create handler to keep the test focused on the
// list/detail wire shape and to make it easy to inject a large body.
func insertHandlerTestSkill(t *testing.T, namePrefix, content string) string {
	t.Helper()
	name := namePrefix + "-" + t.Name()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO skill (workspace_id, name, description, content, config, created_by)
		VALUES ($1, $2, $3, $4, '{}'::jsonb, $5)
		RETURNING id
	`, testWorkspaceID, name, "fixture", content, testUserID).Scan(&id); err != nil {
		t.Fatalf("insert skill: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, id)
	})
	return id
}

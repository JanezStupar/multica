package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/service"
)

func TestAgentBuiltinReplacementSelectsWholeBundleAndFailsOnMissingFile(t *testing.T) {
	ctx := context.Background()
	var agentID string
	if err := testPool.QueryRow(ctx, `INSERT INTO agent
		(workspace_id, name, description, runtime_mode, runtime_config,
		 visibility, permission_mode, max_concurrent_tasks, owner_id)
		VALUES ($1, $2, '', 'local', '{}'::jsonb, 'private', 'private', 1, $3)
		RETURNING id`, testWorkspaceID, t.Name(), testUserID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent WHERE id = $1`, agentID) })

	builtin := testHandler.TaskService.BuiltinSkills("", false)[0]
	skillID := insertHandlerTestSkill(t, "replacement", "---\nname: replacement\n---\n\n# Replacement")
	for _, file := range builtin.Files {
		if _, err := testPool.Exec(ctx, `INSERT INTO skill_file (skill_id, path, content) VALUES ($1, $2, $3)`, skillID, file.Path, file.Content); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO skill_file (skill_id, path, content) VALUES ($1, 'runtime/issue-workflow.md', 'Selected workflow')`, skillID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM skill_file WHERE skill_id = $1`, skillID) })

	w := httptest.NewRecorder()
	req := withURLParam(newRequest("PUT", "/api/agents/"+agentID+"/builtin-skills/replacements", map[string]any{
		"skill_id": service.BuiltinSkillID(builtin.Name), "replacement_skill_id": skillID,
	}), "id", agentID)
	testHandler.SetAgentBuiltinSkillReplacement(w, req)
	if w.Code != 204 {
		t.Fatalf("set replacement: %d %s", w.Code, w.Body.String())
	}
	row, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatal(err)
	}
	replacements, err := service.DecodeBuiltinSkillReplacements(row.BuiltinSkillReplacements)
	if err != nil {
		t.Fatal(err)
	}
	policy := service.AgentBuiltinPolicy{WorkspaceID: row.WorkspaceID, Replacements: replacements}
	bundles, refs, err := testHandler.TaskService.LoadAgentSkillBundles(ctx, row.ID, "", false, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 || len(refs) != 1 || bundles[0].ID != skillID || refs[0].ReplacesBuiltin != service.BuiltinSkillID(builtin.Name) {
		t.Fatalf("effective bundles=%+v refs=%+v", bundles, refs)
	}
	resolved, err := testHandler.TaskService.LoadRequestedAgentSkillBundles(ctx, row.ID, []service.AgentSkillBundleRef{
		{ID: skillID, Source: "workspace"}, {ID: service.BuiltinSkillID(builtin.Name), Source: "builtin"},
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[service.AgentSkillBundleKey("workspace", skillID)].ReplacesBuiltin == "" {
		t.Fatalf("resolve exposed built-in or lost replacement: %+v", resolved)
	}

	if _, err := testPool.Exec(ctx, `DELETE FROM skill_file WHERE skill_id = $1 AND path = 'references/issues.md'`, skillID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := testHandler.TaskService.LoadAgentSkillBundles(ctx, row.ID, "", false, policy); err == nil {
		t.Fatal("incomplete replacement dispatched")
	}
	var stored map[string]string
	if err := json.Unmarshal(row.BuiltinSkillReplacements, &stored); err != nil || stored[service.BuiltinSkillID(builtin.Name)] != skillID {
		t.Fatalf("stored policy = %v, %v", stored, err)
	}
}

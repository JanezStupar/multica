package execenv

import (
	"strings"
	"testing"
)

func TestPlatformReplacementControlsIssueBrief(t *testing.T) {
	ctx := TaskContextForEnv{
		IssueID: "issue-1",
		AgentSkills: []SkillContextForEnv{{
			ID: "workspace-skill-id", Source: "workspace", Name: "trackself-platform",
			ReplacesBuiltin: "builtin:multica-platform",
			Content:         "---\nname: trackself-platform\n---\nbody",
			Files: []SkillFileContextForEnv{
				{Path: "runtime/issue-workflow.md", Content: "Selected workflow owns context and status."},
				{Path: "references/issues.md", Content: "platform contract"},
			},
		}},
	}
	brief := buildMetaSkillContentSlim("codex", ctx)
	for _, want := range []string{"Selected workflow owns context and status.", "Open its `references/issues.md`", "`trackself-platform` skill"} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief missing %q", want)
		}
	}
	for _, forbidden := range []string{"Every issue turn runs the same workflow", "Post exactly ONE comment per run"} {
		if strings.Contains(brief, forbidden) {
			t.Errorf("brief retained generic instruction %q", forbidden)
		}
	}
}

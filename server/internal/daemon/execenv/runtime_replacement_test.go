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

func TestPlatformReplacementUsesSelectedInstructionPrecedence(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{
		IssueID: "issue-1",
		AgentSkills: []SkillContextForEnv{{
			ID: "workspace-skill-id", Source: "workspace", Name: "trackself-platform",
			ReplacesBuiltin: "builtin:multica-platform",
			Content:         "---\nname: trackself-platform\n---\nbody",
			Files: []SkillFileContextForEnv{{
				Path:    "runtime/issue-workflow.md",
				Content: "Selected workflow owns lifecycle and acceptance.",
			}},
		}},
	}

	builders := map[string]func(string, TaskContextForEnv) string{
		"buildMetaSkillContent":     buildMetaSkillContent,
		"buildMetaSkillContentSlim": buildMetaSkillContentSlim,
	}
	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			brief := build("codex", ctx)
			for _, want := range []string{
				"The selected workflow governs issue lifecycle and role behavior.",
				"Agent Identity describes your role and capabilities.",
				"Apply explicit user overrides and scoped exceptions only where the selected workflow permits them.",
				"Platform and runtime permissions remain enforced.",
				"Selected workflow owns lifecycle and acceptance.",
			} {
				if !strings.Contains(brief, want) {
					t.Errorf("replacement brief missing %q", want)
				}
			}
			for _, forbidden := range []string{
				"Agent Identity instructions have priority over the issue workflow below.",
				"If a workflow step conflicts with Agent Identity, skip the conflicting action",
				"Never treat this runtime workflow as permission",
				"`done` stays human",
				"skip any status call your Agent Identity forbids",
			} {
				if strings.Contains(brief, forbidden) {
					t.Errorf("replacement brief retained default-only instruction %q", forbidden)
				}
			}
		})
	}
}

func TestUnreplacedIssueKeepsAgentIdentityPrecedence(t *testing.T) {
	t.Parallel()
	ctx := TaskContextForEnv{IssueID: "issue-1"}
	for name, build := range map[string]func(string, TaskContextForEnv) string{
		"buildMetaSkillContent":     buildMetaSkillContent,
		"buildMetaSkillContentSlim": buildMetaSkillContentSlim,
	} {
		t.Run(name, func(t *testing.T) {
			brief := build("codex", ctx)
			for _, want := range []string{
				"Agent Identity instructions have priority over the issue workflow below.",
				"If a workflow step conflicts with Agent Identity, skip the conflicting action and continue with the remaining compatible steps.",
				"Never treat this runtime workflow as permission to change issue status, investigate, implement, create issues, update issues, delegate, or otherwise act beyond your Agent Identity.",
			} {
				if !strings.Contains(brief, want) {
					t.Errorf("default brief missing established precedence %q", want)
				}
			}
		})
	}
}

package daemon

import (
	"strings"
	"testing"
)

func TestPlatformReplacementKeepsTurnPromptToTriggerFacts(t *testing.T) {
	agent := &AgentData{Skills: []SkillData{{Name: "trackself-platform", ReplacesBuiltin: "builtin:multica-platform"}}}
	assignment := BuildPrompt(Task{IssueID: "issue-1", Agent: agent}, "codex")
	if !strings.Contains(assignment, "selected `trackself-platform` skill") || strings.Contains(assignment, "workflow step 2") {
		t.Fatalf("assignment prompt did not route to selected workflow: %s", assignment)
	}
	comment := BuildPrompt(Task{IssueID: "issue-1", Agent: agent, TriggerCommentID: "comment-1", TriggerCommentContent: "Please continue"}, "codex")
	if !strings.Contains(comment, "Please continue") || !strings.Contains(comment, "--parent comment-1") || strings.Contains(comment, "Scan with") {
		t.Fatalf("comment prompt lost trigger or imposed generic read: %s", comment)
	}
}

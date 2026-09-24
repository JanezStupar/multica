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

func TestBuildPromptPinnedSquadLeaderUsesSelectedWorkflowForReplies(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		coalesced []CoalescedCommentData
	}{
		{name: "one thread"},
		{name: "several threads", coalesced: []CoalescedCommentData{{ID: "earlier", ThreadID: "other-thread", Content: "Earlier request"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := Task{
				IssueID:               "issue-123",
				TriggerCommentID:      "comment-456",
				TriggerThreadID:       "trigger-thread",
				TriggerCommentContent: "Review this",
				CoalescedComments:     tc.coalesced,
				IsLeaderTask:          true,
				LeaderRoleResolved:    true,
				Agent: &AgentData{Skills: []SkillData{{
					Name: "selected-ticket-workflow", ReplacesBuiltin: "builtin:multica-platform",
				}}},
			}
			out := BuildPrompt(task, "claude")
			for _, want := range []string{"selected-ticket-workflow", "--parent comment-456"} {
				if !strings.Contains(out, want) {
					t.Errorf("pinned leader prompt lost %q:\n%s", want, out)
				}
			}
			for _, oldRule := range []string{"outcome is `no_action`", "multica squad activity", "Post your reply as a comment", "Post ONE reply per thread"} {
				if strings.Contains(out, oldRule) {
					t.Errorf("pinned leader prompt received compiled reply rule %q:\n%s", oldRule, out)
				}
			}
		})
	}
}

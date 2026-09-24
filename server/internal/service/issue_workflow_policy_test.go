package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/multica-ai/multica/server/pkg/skillbundle"
)

func TestIssueWorkflowPolicySnapshotIntegrityDoesNotDependOnCurrentInventory(t *testing.T) {
	// This is a valid v1 historical snapshot with fewer references than the
	// current embedded platform skill. Current defaults cannot rewrite it.
	old := AgentSkillData{
		ID: "11111111-1111-1111-1111-111111111111", Source: skillbundle.SourceWorkspace,
		ReplacesBuiltin: BuiltinSkillID(PlatformSkillName), Name: "old-policy",
		Content: "historical instructions",
		Files:   []AgentSkillFileData{{Path: "runtime/issue-workflow.md", Content: "historical workflow"}},
	}
	bundles, _ := BuildAgentSkillBundles([]AgentSkillData{old})
	stored := IssueWorkflowPolicy{
		FormatVersion: 1, Scope: IssueWorkflowBundleScope,
		Coverage: issueWorkflowPolicyCoverage,
		Version:  bundles[0].Hash, SourceSkillID: old.ID, Bundle: bundles[0],
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	s := &TaskService{}
	got, err := s.DecodeIssueWorkflowPolicy(raw)
	if err != nil || got.Version != stored.Version {
		t.Fatalf("historical snapshot rejected: %+v, %v", got, err)
	}
	stored.Bundle.Files[0].Content = "tampered"
	raw, _ = json.Marshal(stored)
	if _, err := s.DecodeIssueWorkflowPolicy(raw); err == nil {
		t.Fatal("tampered content retained the old version")
	}
}

func TestPinnedIssuePolicyWinsOverAgentPlatformSelection(t *testing.T) {
	s := &TaskService{}
	pinned := AgentSkillData{ID: "11111111-1111-1111-1111-111111111111", Name: "pinned", Content: "frozen"}
	current := AgentSkillData{ID: "22222222-2222-2222-2222-222222222222", Name: "new-agent-replacement", Content: "live"}
	unrelated := AgentSkillData{ID: "33333333-3333-3333-3333-333333333333", Name: "unrelated", Content: "unrelated"}
	selected, err := s.applyBuiltinPolicy(context.Background(), []AgentSkillData{pinned, current, unrelated}, AgentBuiltinPolicy{
		EnabledIDs: []string{}, PinnedPlatform: &pinned,
		Replacements: map[string]string{BuiltinSkillID(PlatformSkillName): current.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || selected[0].ID != unrelated.ID || selected[1].ID != pinned.ID ||
		selected[1].ReplacesBuiltin != BuiltinSkillID(PlatformSkillName) {
		t.Fatalf("pinned platform selection = %+v", selected)
	}
}

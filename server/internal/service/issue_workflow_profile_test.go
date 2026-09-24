package service

import (
	"bytes"
	"testing"

	"github.com/multica-ai/multica/server/pkg/skillbundle"
)

func TestIssueWorkflowProfileRejectsCorruptFrozenSkillAndHidesCustomArgs(t *testing.T) {
	platform := AgentSkillData{
		ID: "11111111-1111-1111-1111-111111111111", Source: skillbundle.SourceWorkspace,
		ReplacesBuiltin: BuiltinSkillID(PlatformSkillName), Name: "pinned platform", Content: "policy",
		Files: []AgentSkillFileData{{Path: "runtime/issue-workflow.md", Content: "do the work"}},
	}
	bundles, _ := BuildAgentSkillBundles([]AgentSkillData{platform})
	profile := IssueWorkflowProfile{
		FormatVersion: 1, IssueID: "22222222-2222-2222-2222-222222222222",
		AgentID: "33333333-3333-3333-3333-333333333333", PolicyVersion: bundles[0].Hash,
		ExpectedProvider: "codex", CustomArgsDigest: IssueWorkflowCustomArgsDigest([]byte(`["--token","secret-value"]`)),
		Skills: bundles,
	}
	raw, digest, err := profile.encodedAndDigest()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("secret-value")) {
		t.Fatal("custom argument value leaked into persisted profile")
	}
	if _, err := DecodeIssueWorkflowProfile(raw, digest, profile.IssueID, profile.AgentID, profile.PolicyVersion); err != nil {
		t.Fatalf("valid frozen profile rejected: %v", err)
	}
	profile.Skills[0].Content = "changed"
	tampered, tamperedDigest, err := profile.encodedAndDigest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeIssueWorkflowProfile(tampered, tamperedDigest, profile.IssueID, profile.AgentID, profile.PolicyVersion); err == nil {
		t.Fatal("changed skill content accepted with stale bundle hash")
	}
}

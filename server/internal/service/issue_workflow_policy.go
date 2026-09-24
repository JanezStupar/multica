package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/skillbundle"
)

// IssueWorkflowPolicy is the first, explicitly enrolled versioned issue
// policy. Its scope is the complete platform workflow skill only: agent
// instructions, model settings, and permissions remain governed separately.
type IssueWorkflowPolicy struct {
	FormatVersion int                         `json:"format_version"`
	Scope         string                      `json:"scope"`
	Coverage      IssueWorkflowPolicyCoverage `json:"coverage"`
	Version       string                      `json:"version"`
	SourceSkillID string                      `json:"source_skill_id"`
	Bundle        AgentSkillData              `json:"bundle"`
}

type IssueWorkflowPolicyCoverage struct {
	WorkflowBundlePinned    bool `json:"workflow_bundle_pinned"`
	AgentInstructionsPinned bool `json:"agent_instructions_pinned"`
	ModelSettingsPinned     bool `json:"model_settings_pinned"`
}

var issueWorkflowPolicyCoverage = IssueWorkflowPolicyCoverage{WorkflowBundlePinned: true}

const IssueWorkflowBundleScope = "issue_workflow_bundle"

// NewIssueWorkflowPolicy verifies that a workspace skill can replace the
// platform slot, then freezes the entire bundle with a content-derived version.
func (s *TaskService) NewIssueWorkflowPolicy(source AgentSkillData) (IssueWorkflowPolicy, error) {
	if _, err := util.ParseUUID(source.ID); err != nil {
		return IssueWorkflowPolicy{}, fmt.Errorf("workflow skill id must be a UUID")
	}
	if strings.TrimSpace(source.Content) == "" {
		return IssueWorkflowPolicy{}, fmt.Errorf("workflow skill must have a SKILL.md")
	}
	var platform *AgentSkillData
	for _, builtin := range s.AllBuiltinSkills() {
		if builtin.Name == PlatformSkillName {
			platform = &builtin
			break
		}
	}
	if platform == nil {
		return IssueWorkflowPolicy{}, fmt.Errorf("platform skill is unavailable")
	}
	files := make(map[string]bool, len(source.Files))
	for _, file := range source.Files {
		if file.Path == "" || files[file.Path] {
			return IssueWorkflowPolicy{}, fmt.Errorf("workflow skill has invalid or duplicate file path")
		}
		files[file.Path] = true
		if file.Path == "runtime/issue-workflow.md" && strings.TrimSpace(file.Content) == "" {
			return IssueWorkflowPolicy{}, fmt.Errorf("workflow skill has empty runtime/issue-workflow.md")
		}
	}
	for _, file := range platform.Files {
		if !files[file.Path] {
			return IssueWorkflowPolicy{}, fmt.Errorf("workflow skill is missing %s", file.Path)
		}
	}
	if !files["runtime/issue-workflow.md"] {
		return IssueWorkflowPolicy{}, fmt.Errorf("workflow skill is missing runtime/issue-workflow.md")
	}
	source.Source = skillbundle.SourceWorkspace
	source.ReplacesBuiltin = BuiltinSkillID(PlatformSkillName)
	bundles, _ := BuildAgentSkillBundles([]AgentSkillData{source})
	bundle := bundles[0]
	if _, err := ParseWorkflowAuthorityPolicy(bundle); err != nil {
		return IssueWorkflowPolicy{}, err
	}
	return IssueWorkflowPolicy{
		FormatVersion: 1,
		Scope:         IssueWorkflowBundleScope, Coverage: issueWorkflowPolicyCoverage, Version: bundle.Hash,
		SourceSkillID: bundle.ID, Bundle: bundle,
	}, nil
}

// DecodeIssueWorkflowPolicy checks both the schema and the digest before a
// claim or slim resolve may use the stored bytes. Corrupt data fails closed.
func (s *TaskService) DecodeIssueWorkflowPolicy(raw []byte) (*IssueWorkflowPolicy, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var stored IssueWorkflowPolicy
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("decode issue workflow policy: %w", err)
	}
	if stored.FormatVersion != 1 || stored.Scope != IssueWorkflowBundleScope ||
		stored.Coverage != issueWorkflowPolicyCoverage ||
		stored.SourceSkillID == "" || stored.SourceSkillID != stored.Bundle.ID {
		return nil, fmt.Errorf("invalid issue workflow policy identity")
	}
	if _, err := util.ParseUUID(stored.SourceSkillID); err != nil {
		return nil, fmt.Errorf("invalid issue workflow policy skill id")
	}
	if strings.TrimSpace(stored.Bundle.Content) == "" || stored.Bundle.Source != skillbundle.SourceWorkspace ||
		stored.Bundle.ReplacesBuiltin != BuiltinSkillID(PlatformSkillName) {
		return nil, fmt.Errorf("invalid issue workflow policy bundle")
	}
	files := make(map[string]bool, len(stored.Bundle.Files))
	for _, file := range stored.Bundle.Files {
		if file.Path == "" || files[file.Path] {
			return nil, fmt.Errorf("invalid issue workflow policy files")
		}
		files[file.Path] = true
		if file.Path == "runtime/issue-workflow.md" && strings.TrimSpace(file.Content) == "" {
			return nil, fmt.Errorf("issue workflow policy has empty workflow instructions")
		}
	}
	if !files["runtime/issue-workflow.md"] {
		return nil, fmt.Errorf("issue workflow policy lacks workflow instructions")
	}
	bundles, _ := BuildAgentSkillBundles([]AgentSkillData{stored.Bundle})
	validated := bundles[0]
	if stored.Version != validated.Hash || stored.Bundle.Hash != validated.Hash {
		return nil, fmt.Errorf("issue workflow policy digest mismatch")
	}
	if _, err := ParseWorkflowAuthorityPolicy(validated); err != nil {
		return nil, err
	}
	stored.Bundle = validated
	return &stored, nil
}

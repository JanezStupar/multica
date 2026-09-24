package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/skillbundle"
)

// AgentBuiltinPolicy is the claim-time choice of logical built-in slots and
// their complete workspace replacements. A nil EnabledIDs inherits the
// platform inventory; an empty non-nil slice selects none.
type AgentBuiltinPolicy struct {
	SystemKey       string
	LegacyRedirects bool
	EnabledIDs      []string
	WorkspaceID     pgtype.UUID
	Replacements    map[string]string // builtin:<slug> -> workspace skill UUID
	// PinnedPlatform is the issue-owned snapshot. It wins over agent-level
	// selection and replacement for the platform workflow slot.
	PinnedPlatform *AgentSkillData
}

func DecodeBuiltinSkillReplacements(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return map[string]string{}, nil
	}
	var replacements map[string]string
	if err := json.Unmarshal(raw, &replacements); err != nil || replacements == nil {
		return nil, fmt.Errorf("invalid built-in skill replacements")
	}
	return replacements, nil
}

func (s *TaskService) replacementSkill(ctx context.Context, policy AgentBuiltinPolicy, builtin AgentSkillData, id string) (AgentSkillData, error) {
	parsed, err := util.ParseUUID(id)
	if err != nil || !policy.WorkspaceID.Valid {
		return AgentSkillData{}, fmt.Errorf("invalid replacement skill for %s", builtin.Name)
	}
	row, err := s.Queries.GetSkillInWorkspace(ctx, db.GetSkillInWorkspaceParams{ID: parsed, WorkspaceID: policy.WorkspaceID})
	if err != nil {
		return AgentSkillData{}, fmt.Errorf("replacement skill for %s unavailable: %w", builtin.Name, err)
	}
	loaded, err := s.skillsWithFiles(ctx, []db.Skill{row})
	if err != nil {
		return AgentSkillData{}, err
	}
	skill := loaded[0]
	if strings.TrimSpace(skill.Content) == "" {
		return AgentSkillData{}, fmt.Errorf("replacement skill for %s has no SKILL.md", builtin.Name)
	}
	files := make(map[string]bool, len(skill.Files))
	for _, file := range skill.Files {
		files[file.Path] = true
	}
	for _, required := range builtin.Files {
		if !files[required.Path] {
			return AgentSkillData{}, fmt.Errorf("replacement skill for %s is missing %s", builtin.Name, required.Path)
		}
	}
	if builtin.Name == PlatformSkillName && !files["runtime/issue-workflow.md"] {
		return AgentSkillData{}, fmt.Errorf("replacement skill for %s is missing runtime/issue-workflow.md", builtin.Name)
	}
	skill.ReplacesBuiltin = BuiltinSkillID(builtin.Name)
	return skill, nil
}

// applyBuiltinPolicy is the single selection rule shared by full and slim
// claims. A mapped slot produces one workspace bundle, never the built-in and
// workspace bundle together. Unknown mappings fail closed.
func (s *TaskService) applyBuiltinPolicy(ctx context.Context, workspaceSkills []AgentSkillData, policy AgentBuiltinPolicy) ([]AgentSkillData, error) {
	builtins := s.EnabledBuiltinSkills(policy.SystemKey, policy.LegacyRedirects, policy.EnabledIDs)
	replacements := policy.Replacements
	if policy.PinnedPlatform != nil {
		// An issue's explicit version is independent of every agent's mutable
		// built-in allow-list and replacement map. In particular, a removed
		// source must not be reloaded through the agent mapping.
		replacements = make(map[string]string, len(policy.Replacements))
		for id, skillID := range policy.Replacements {
			if id != BuiltinSkillID(PlatformSkillName) {
				replacements[id] = skillID
			}
		}
		filtered := builtins[:0:0]
		for _, builtin := range builtins {
			if builtin.Name != PlatformSkillName {
				filtered = append(filtered, builtin)
			}
		}
		builtins = filtered
	}
	selected := make(map[string]AgentSkillData, len(builtins))
	for _, builtin := range builtins {
		selected[BuiltinSkillID(builtin.Name)] = builtin
	}
	for id := range replacements {
		if _, ok := selected[id]; !ok {
			return nil, fmt.Errorf("replacement targets disabled or unknown built-in %s", id)
		}
	}
	replacedWorkspace := make(map[string]bool, len(policy.Replacements))
	var effectiveBuiltins []AgentSkillData
	for _, builtin := range builtins {
		id := replacements[BuiltinSkillID(builtin.Name)]
		if id == "" {
			effectiveBuiltins = append(effectiveBuiltins, builtin)
			continue
		}
		if replacedWorkspace[id] {
			return nil, fmt.Errorf("workspace skill %s replaces more than one built-in", id)
		}
		replacement, err := s.replacementSkill(ctx, policy, builtin, id)
		if err != nil {
			return nil, err
		}
		replacedWorkspace[id] = true
		effectiveBuiltins = append(effectiveBuiltins, replacement)
	}
	result := make([]AgentSkillData, 0, len(workspaceSkills)+len(effectiveBuiltins))
	for _, skill := range workspaceSkills {
		pinnedConflict := policy.PinnedPlatform != nil &&
			(skill.ID == policy.PinnedPlatform.ID || skill.ID == policy.Replacements[BuiltinSkillID(PlatformSkillName)])
		if !replacedWorkspace[skill.ID] && !pinnedConflict {
			result = append(result, skill)
		}
	}
	if policy.PinnedPlatform != nil {
		pinned := *policy.PinnedPlatform
		pinned.Source = skillbundle.SourceWorkspace
		pinned.ReplacesBuiltin = BuiltinSkillID(PlatformSkillName)
		effectiveBuiltins = append(effectiveBuiltins, pinned)
	}
	return append(result, effectiveBuiltins...), nil
}

func (s *TaskService) ValidateAgentBuiltinPolicy(ctx context.Context, policy AgentBuiltinPolicy) error {
	_, err := s.applyBuiltinPolicy(ctx, nil, policy)
	return err
}

package service

import (
	"embed"
	"io/fs"
	"path"
	"strings"

	internalSkill "github.com/multica-ai/multica/server/internal/skill"
)

//go:embed builtin_skills
var builtinSkillsFS embed.FS

const builtinSkillsRoot = "builtin_skills"

const builtinSkillIDPrefix = "builtin:"

// BuiltinSkillID returns the stable public identity for a built-in skill.
// Directory names are compile-time product identifiers; display names from
// frontmatter must never change persisted agent controls.
func BuiltinSkillID(name string) string {
	return builtinSkillIDPrefix + name
}

// BuiltinSkills returns the platform's built-in skills, embedded at compile
// time. Agents inherit these on top of workspace-bound skills unless their
// exact per-agent allow-list disables one, so they teach platform-wide "how
// to" workflows (e.g. mentioning) that the runtime brief leaves to skills.
//
// Layout: builtin_skills/<name>/SKILL.md plus optional supporting files. The
// <name> directory carries a "multica-" prefix so its on-disk slug can never
// collide with a workspace skill a user authored (see writeSkillFiles, which
// derives the skill directory from AgentSkillData.Name).
func (s *TaskService) BuiltinSkills() []AgentSkillData {
	return loadBuiltinSkills()
}

// EnabledBuiltinSkills applies an agent's exact built-in allow-list. A nil
// list means the agent has never customized built-ins and inherits all of
// them. A non-nil list, including an empty list, is authoritative.
func (s *TaskService) EnabledBuiltinSkills(enabledIDs []string) []AgentSkillData {
	skills := s.BuiltinSkills()
	if enabledIDs == nil {
		return skills
	}
	enabled := make(map[string]struct{}, len(enabledIDs))
	for _, id := range enabledIDs {
		enabled[id] = struct{}{}
	}
	result := make([]AgentSkillData, 0, len(skills))
	for _, skill := range skills {
		if _, ok := enabled[BuiltinSkillID(skill.Name)]; ok {
			result = append(result, skill)
		}
	}
	return result
}

func loadBuiltinSkills() []AgentSkillData {
	entries, err := fs.ReadDir(builtinSkillsFS, builtinSkillsRoot)
	if err != nil {
		return nil
	}
	var skills []AgentSkillData
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if skill, ok := loadBuiltinSkill(entry.Name()); ok {
			skills = append(skills, skill)
		}
	}
	return skills
}

func loadBuiltinSkill(name string) (AgentSkillData, bool) {
	dir := path.Join(builtinSkillsRoot, name)
	content, err := fs.ReadFile(builtinSkillsFS, path.Join(dir, "SKILL.md"))
	if err != nil {
		// A skill directory without a SKILL.md is malformed — skip it rather
		// than ship an empty skill.
		return AgentSkillData{}, false
	}
	_, description := internalSkill.ParseSkillFrontmatter(string(content))
	skill := AgentSkillData{Name: name, Description: description, Content: string(content)}
	// Any other file in the directory becomes a supporting file, preserving
	// its relative path so subdirectories (e.g. rules/styling.md) survive.
	_ = fs.WalkDir(builtinSkillsFS, dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		rel := strings.TrimPrefix(p, dir+"/")
		if rel == "SKILL.md" {
			return nil
		}
		data, readErr := fs.ReadFile(builtinSkillsFS, p)
		if readErr != nil {
			return nil
		}
		skill.Files = append(skill.Files, AgentSkillFileData{Path: rel, Content: string(data)})
		return nil
	})
	return skill, true
}

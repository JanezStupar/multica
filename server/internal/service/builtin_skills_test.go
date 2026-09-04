package service

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	maxSkillBodyLines   = 500
	maxRoutingBodyLines = 150
	maxDescriptionChars = 300
)

var granularBuiltinReferences = map[string]string{
	"multica-working-on-issues":      "references/issues.md",
	"multica-mentioning":             "references/mentions.md",
	"multica-creating-agents":        "references/agents.md",
	"multica-squads":                 "references/squads.md",
	"multica-autopilots":             "references/autopilots.md",
	"multica-projects-and-resources": "references/projects.md",
	"multica-runtimes-and-repos":     "references/runtimes.md",
	"multica-skill-importing":        "references/skill-import.md",
}

func TestBuiltinSkillsConformToTemplate(t *testing.T) {
	for _, skill := range allBuiltinSkillsForTest() {
		t.Run(skill.Name, func(t *testing.T) {
			if !strings.HasPrefix(skill.Name, "multica-") {
				t.Errorf("skill name %q must carry the multica- prefix", skill.Name)
			}
			fm, body, ok := splitFrontmatter(skill.Content)
			if !ok {
				t.Fatal("SKILL.md must lead with YAML frontmatter")
			}
			if fm["name"] != skill.Name {
				t.Errorf("frontmatter name = %q, want %q", fm["name"], skill.Name)
			}
			if desc := strings.TrimSpace(fm["description"]); desc == "" || len(desc) > maxDescriptionChars {
				t.Errorf("description length = %d, want 1..%d", len(desc), maxDescriptionChars)
			}
			if fm["user-invocable"] != "false" {
				t.Errorf("user-invocable = %q, want false", fm["user-invocable"])
			}
			if !strings.Contains(fm["allowed-tools"], "Bash(multica *)") {
				t.Errorf("allowed-tools = %q, want Multica CLI access", fm["allowed-tools"])
			}
			budget := maxSkillBodyLines
			if len(skill.Files) > 0 {
				budget = maxRoutingBodyLines
			}
			if lines := strings.Count(body, "\n") + 1; lines > budget {
				t.Errorf("SKILL.md body has %d lines, want at most %d", lines, budget)
			}
			for _, file := range skill.Files {
				if lines := strings.Count(file.Content, "\n") + 1; lines > maxSkillBodyLines {
					t.Errorf("%s has %d lines, want at most %d", file.Path, lines, maxSkillBodyLines)
				}
			}
		})
	}
}

func TestGranularBuiltinCatalogAndReferences(t *testing.T) {
	svc := &TaskService{}
	ordinary := svc.BuiltinSkills("", false)
	if len(ordinary) != len(granularBuiltinReferences) {
		t.Fatalf("ordinary built-ins = %d, want %d", len(ordinary), len(granularBuiltinReferences))
	}
	for name, reference := range granularBuiltinReferences {
		skill, ok := findSkill(ordinary, name)
		if !ok {
			t.Errorf("missing granular built-in %q", name)
			continue
		}
		if len(skill.Files) != 1 || skill.Files[0].Path != reference {
			t.Errorf("%s files = %+v, want only %s", name, skill.Files, reference)
		}
		if !strings.Contains(skill.Content, reference) {
			t.Errorf("%s router does not name %s", name, reference)
		}
	}
	if _, ok := findSkill(ordinary, "multica-platform"); ok {
		t.Error("consolidated multica-platform must not replace independently selectable built-ins")
	}
	if _, ok := findSkill(ordinary, "multica-onboarding"); ok {
		t.Error("ordinary agent received Mika-only onboarding")
	}
	mika := svc.BuiltinSkills(MikaSystemKey, false)
	if _, ok := findSkill(mika, "multica-onboarding"); !ok {
		t.Error("Mika did not receive onboarding")
	}
}

func TestEnabledBuiltinSkillsUsesNilAsInheritAndNonNilAsExactSet(t *testing.T) {
	svc := &TaskService{}
	all := svc.BuiltinSkills("", false)
	if got := svc.EnabledBuiltinSkills("", false, nil); len(got) != len(all) {
		t.Fatalf("nil policy returned %d skills, want %d", len(got), len(all))
	}
	if got := svc.EnabledBuiltinSkills("", false, []string{}); len(got) != 0 {
		t.Fatalf("empty exact policy returned %d skills, want none", len(got))
	}
	wantID := BuiltinSkillID("multica-mentioning")
	got := svc.EnabledBuiltinSkills("", false, []string{wantID, "builtin:future-skill"})
	if len(got) != 1 || BuiltinSkillID(got[0].Name) != wantID {
		t.Fatalf("subset = %+v, want only %s", got, wantID)
	}
}

func TestBuiltinSkillsFrontmatterIsStrictYAML(t *testing.T) {
	for _, skill := range allBuiltinSkillsForTest() {
		t.Run(skill.Name, func(t *testing.T) {
			rest := strings.TrimPrefix(skill.Content, "---\n")
			end := strings.Index(rest, "\n---")
			if end < 0 {
				t.Fatal("frontmatter has no closing delimiter")
			}
			var fm map[string]any
			if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
				t.Fatalf("frontmatter is not strict YAML: %v", err)
			}
		})
	}
}

func TestBuiltinSkillPayloadCarriesNoSourceReferences(t *testing.T) {
	banned := []*regexp.Regexp{
		regexp.MustCompile(`(?:server|packages|apps|e2e)/[A-Za-z0-9_-]+/`),
		regexp.MustCompile(`[A-Za-z0-9_./-]*\.(?:go|ts|tsx|sql)\b`),
		regexp.MustCompile(`\bgo (?:test|build|vet|run)\b`),
		regexp.MustCompile(`\bmigrations?/[0-9]`),
		regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9]{3,}\b`),
	}
	for _, skill := range allBuiltinSkillsForTest() {
		files := append([]AgentSkillFileData{{Path: "SKILL.md", Content: skill.Content}}, skill.Files...)
		for _, file := range files {
			for _, re := range banned {
				if match := re.FindString(file.Content); match != "" {
					t.Errorf("%s/%s leaks repository-only source reference %q", skill.Name, file.Path, match)
				}
			}
		}
	}
}

func TestGranularBuiltinsCarryKeyContracts(t *testing.T) {
	cases := map[string][]string{
		"multica-working-on-issues":      {"multica issue pull-requests", "--status backlog", "--no-start"},
		"multica-mentioning":             {"mention://agent/", "mention://member/", "mention://project/"},
		"multica-creating-agents":        {"agent builtins disable", "enabled_builtin_skill_ids", "inherit_all"},
		"multica-squads":                 {"multica squad", "leader"},
		"multica-autopilots":             {"create_issue", "run_only"},
		"multica-projects-and-resources": {"github_repo", "local_directory"},
		"multica-runtimes-and-repos":     {"multica runtime", "repo checkout"},
		"multica-skill-importing":        {"multica skill import", ".zip"},
	}
	for name, anchors := range cases {
		skill, ok := findSkill(allBuiltinSkillsForTest(), name)
		if !ok {
			t.Fatalf("missing built-in %s", name)
		}
		content := skill.Content
		for _, file := range skill.Files {
			content += "\n" + file.Content
		}
		for _, anchor := range anchors {
			if !strings.Contains(content, anchor) {
				t.Errorf("%s missing contract anchor %q", name, anchor)
			}
		}
	}
}

func TestMentionParserContract(t *testing.T) {
	const id = "7f3a1b2c-0000-4000-8000-000000000abc"
	if got := util.ParseMentions("[@Alice](mention://member/Alice)"); len(got) != 0 {
		t.Fatalf("name parsed as mention id: %+v", got)
	}
	want := []util.Mention{{Type: "agent", ID: id}}
	if got := util.ParseMentions("[@Bot](mention://agent/" + id + ")"); !slices.Equal(got, want) {
		t.Fatalf("valid mention = %+v, want %+v", got, want)
	}
}

func allBuiltinSkillsForTest() []AgentSkillData {
	return loadBuiltinSkillDirs(func(string) bool { return true })
}

func findSkill(skills []AgentSkillData, name string) (AgentSkillData, bool) {
	for _, skill := range skills {
		if skill.Name == name {
			return skill, true
		}
	}
	return AgentSkillData{}, false
}

func splitFrontmatter(content string) (map[string]string, string, bool) {
	if !strings.HasPrefix(content, "---\n") {
		return nil, content, false
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, content, false
	}
	block := rest[:end]
	body := rest[end:]
	if nl := strings.Index(body, "\n"); nl >= 0 {
		body = body[nl+1:]
	}
	fm := make(map[string]string)
	for _, line := range strings.Split(block, "\n") {
		key, value, found := strings.Cut(line, ":")
		if found {
			fm[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return fm, body, true
}

package handler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseSkillArchive_TrackselfPolicyBuilderOutput(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not locate this test source")
	}
	if !filepath.IsAbs(source) {
		var err error
		source, err = filepath.Abs(source)
		if err != nil {
			t.Fatalf("resolve test source path: %v", err)
		}
	}
	root := filepath.Dir(source)
	for {
		if _, err := os.Stat(filepath.Join(root, "fork", "trackself", "build_skill.py")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("could not find repository root containing fork/trackself/build_skill.py")
		}
		root = parent
	}

	archivePath := filepath.Join(t.TempDir(), "mica-policy.skill")
	command := exec.Command("python3", "fork/trackself/build_skill.py", "--output", archivePath)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build tracked policy archive: %v\n%s", err, output)
	}
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read built archive: %v", err)
	}

	imported, err := parseSkillArchive(data, filepath.Base(archivePath))
	if err != nil {
		t.Fatalf("parseSkillArchive on builder output: %v", err)
	}
	if !strings.HasPrefix(imported.name, "trackself-platform-") {
		t.Fatalf("skill name = %q, want content-derived trackself-platform-* name", imported.name)
	}
	if len(strings.TrimPrefix(imported.name, "trackself-platform-")) != 16 {
		t.Fatalf("skill name = %q, want 16-character identity suffix", imported.name)
	}
	if !strings.Contains(imported.content, "references/workflow.md") || !strings.Contains(imported.content, "runtime/issue-workflow.md") {
		t.Fatalf("root SKILL.md does not route to the versioned workflow files")
	}

	wantPaths := map[string]bool{
		"runtime/issue-workflow.md":  true,
		"runtime/policy.json":        true,
		"references/workflow.md":     true,
		"scripts/forgejo_draft_pr.py": true,
		"source-manifest.json":       true,
		"references/agents.md":       true,
		"references/autopilots.md":   true,
		"references/issues.md":       true,
		"references/mentions.md":     true,
		"references/projects.md":     true,
		"references/runtimes.md":     true,
		"references/skill-import.md": true,
		"references/squads.md":       true,
	}
	if len(imported.files) != len(wantPaths) {
		t.Fatalf("supporting files = %v, want %d expected paths", filePaths(imported), len(wantPaths))
	}
	for _, path := range filePaths(imported) {
		if !wantPaths[path] {
			t.Errorf("unexpected imported supporting path %q", path)
		}
		if strings.EqualFold(filepath.Base(path), "SKILL.md") {
			t.Errorf("nested SKILL.md reached importer as supporting file: %q", path)
		}
	}
	for _, conflict := range []struct {
		path string
		text string
	}{
		{"references/issues.md", "the turn must not exit with a stale value"},
		{"references/issues.md", "a review-the-PR issue is being worked the moment reviewing starts"},
		{"references/squads.md", "changing assignee cancels existing tasks"},
		{"references/squads.md", "the leader's first assignment turn should move the parent"},
		{"references/squads.md", "parent stays `in_progress` until the leader later confirms"},
		{"references/squads.md", "that status authority is granted only when"},
		{"references/squads.md", "the comment prohibition on `no_action` only applies"},
	} {
		content, ok := fileContent(imported, conflict.path)
		if !ok {
			t.Fatalf("missing candidate platform reference %q", conflict.path)
		}
		if strings.Contains(content, conflict.text) {
			t.Errorf("candidate reference %q retains conflicting workflow passage %q", conflict.path, conflict.text)
		}
	}
	if content, _ := fileContent(imported, "references/squads.md"); !strings.Contains(content, "changing assignee does not cancel tasks already in flight") {
		t.Errorf("candidate squad reference does not state the current reassignment effect")
	}
	if content, _ := fileContent(imported, "references/squads.md"); !strings.Contains(strings.Join(strings.Fields(content), " "), "The selected workflow governs dispatch, reporting, continuation and status authority") {
		t.Errorf("candidate squad reference does not defer enrolled behavior to the selected workflow")
	}

	manifestText, ok := fileContent(imported, "source-manifest.json")
	if !ok {
		t.Fatal("source-manifest.json was not retained by the importer")
	}
	var manifest struct {
		PolicyVersion        string            `json:"policy_version"`
		SkillName            string            `json:"skill_name"`
		BundleIdentitySHA256 string            `json:"bundle_identity_sha256"`
		SourceHashes         map[string]string `json:"source_hashes_sha256"`
	}
	if err := json.Unmarshal([]byte(manifestText), &manifest); err != nil {
		t.Fatalf("decode source manifest: %v", err)
	}
	if manifest.PolicyVersion != "mica-v1" || manifest.SkillName != imported.name {
		t.Fatalf("manifest policy/name = %q/%q, want mica-v1/%q", manifest.PolicyVersion, manifest.SkillName, imported.name)
	}
	if len(manifest.BundleIdentitySHA256) != 64 || len(manifest.SourceHashes) != 13 {
		t.Fatalf("manifest does not contain full source identity and all 13 inputs: %#v", manifest)
	}
}

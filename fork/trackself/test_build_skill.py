#!/usr/bin/env python3
"""Focused tests for the versioned Mica policy archive builder."""

import json
import tempfile
import unittest
import zipfile
from pathlib import Path

import build_skill


PLATFORM_REFERENCE_NAMES = sorted(build_skill.PLATFORM_REFERENCES)
POLICY_FILES = {
    "SKILL.md": (
        "---\n"
        "name: trackself-platform\n"
        "description: Test policy bundle\n"
        "---\n\n"
        "# Test policy\n"
    ),
    "runtime/issue-workflow.md": "Follow the active ticket policy.\n",
    "references/workflow.md": "Mica coordinates the assigned outcome.\n",
}


class BuildSkillTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.policy = self.root / "mica-v1"
        self.platform = self.root / "platform"
        for name, content in POLICY_FILES.items():
            path = self.policy / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content, encoding="utf-8")
        for name in PLATFORM_REFERENCE_NAMES:
            path = self.platform / "references" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(f"Platform contract: {name}\n", encoding="utf-8")

    def tearDown(self):
        self.temp.cleanup()

    def test_archive_bytes_and_manifest_are_deterministic(self):
        first_name, first_files, first_manifest = build_skill.build_bundle(self.policy, self.platform)
        second_name, second_files, second_manifest = build_skill.build_bundle(self.policy, self.platform)
        self.assertEqual(first_name, second_name)
        self.assertEqual(first_files, second_files)
        self.assertEqual(first_manifest, second_manifest)

        first_archive = self.root / "first.skill"
        second_archive = self.root / "second.skill"
        build_skill.write_archive(first_archive, first_files)
        build_skill.write_archive(second_archive, second_files)
        self.assertEqual(first_archive.read_bytes(), second_archive.read_bytes())
        with zipfile.ZipFile(first_archive) as archive:
            root_skill = archive.read("SKILL.md").decode("utf-8")
            manifest = json.loads(archive.read("source-manifest.json"))
        self.assertIn(f"name: {first_name}\n", root_skill)
        self.assertEqual(len(manifest["bundle_identity_sha256"]), 64)
        self.assertIn("multica-platform/references/issues.md", manifest["source_hashes_sha256"])

    def test_policy_version_and_upstream_reference_content_change_identity(self):
        v1_name, _, v1_manifest = build_skill.build_bundle(self.policy, self.platform)

        v2 = self.root / "mica-v2"
        for path in self.policy.rglob("*"):
            if path.is_file():
                target = v2 / path.relative_to(self.policy)
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(path.read_bytes())
        v2_name, _, v2_manifest = build_skill.build_bundle(v2, self.platform)
        self.assertNotEqual(v1_name, v2_name)
        self.assertNotEqual(v1_manifest["bundle_identity_sha256"], v2_manifest["bundle_identity_sha256"])
        self.assertEqual(v2_manifest["policy_version"], "mica-v2")

        issue_ref = self.platform / "references/issues.md"
        issue_ref.write_text(issue_ref.read_text(encoding="utf-8") + "Changed contract.\n", encoding="utf-8")
        changed_name, _, changed_manifest = build_skill.build_bundle(self.policy, self.platform)
        self.assertNotEqual(v1_name, changed_name)
        self.assertNotEqual(v1_manifest["bundle_identity_sha256"], changed_manifest["bundle_identity_sha256"])

    def test_nested_skill_md_is_rejected(self):
        nested = self.policy / "references" / "other" / "SKILL.md"
        nested.parent.mkdir(parents=True)
        nested.write_text("not importable as a nested skill\n", encoding="utf-8")
        with self.assertRaisesRegex(build_skill.BundleError, "nested or non-canonical SKILL.md"):
            build_skill.build_bundle(self.policy, self.platform)

    def test_symlinks_and_malformed_required_inputs_are_rejected(self):
        link = self.policy / "runtime" / "linked.md"
        link.symlink_to(self.policy / "runtime" / "issue-workflow.md")
        with self.assertRaisesRegex(build_skill.BundleError, "symbolic links"):
            build_skill.build_bundle(self.policy, self.platform)
        link.unlink()

        (self.policy / "runtime" / "issue-workflow.md").unlink()
        with self.assertRaisesRegex(build_skill.BundleError, "missing required files"):
            build_skill.build_bundle(self.policy, self.platform)

    def test_noncanonical_base_skill_name_is_rejected(self):
        path = self.policy / "SKILL.md"
        path.write_text(POLICY_FILES["SKILL.md"].replace("trackself-platform", "other-skill"), encoding="utf-8")
        with self.assertRaisesRegex(build_skill.BundleError, "name must be 'trackself-platform'"):
            build_skill.build_bundle(self.policy, self.platform)

    def test_malformed_frontmatter_is_rejected(self):
        path = self.policy / "SKILL.md"
        malformed = POLICY_FILES["SKILL.md"].replace(
            "description: Test policy bundle", 'description: "unterminated'
        )
        path.write_text(malformed, encoding="utf-8")
        with self.assertRaisesRegex(build_skill.BundleError, "unterminated quoted value"):
            build_skill.build_bundle(self.policy, self.platform)


if __name__ == "__main__":
    unittest.main()

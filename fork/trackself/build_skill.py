#!/usr/bin/env python3
"""Build a versioned Mica policy bundle with Multica platform references."""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import stat
from pathlib import Path, PurePosixPath
from zipfile import ZIP_DEFLATED, ZipFile, ZipInfo

REPO = Path(__file__).resolve().parents[2]
PLATFORM = REPO / "server/internal/service/builtin_skills/multica-platform"
DEFAULT_POLICY = REPO / "fork/trackself/policies/mica-v1"
PLATFORM_REFERENCES = {
    "agents.md",
    "autopilots.md",
    "issues.md",
    "mentions.md",
    "projects.md",
    "runtimes.md",
    "skill-import.md",
    "squads.md",
}
REQUIRED_POLICY_FILES = {
    "SKILL.md",
    "runtime/issue-workflow.md",
    "references/workflow.md",
}
POLICY_VERSION_RE = re.compile(r"mica-v[0-9]+(?:\.[0-9]+)*\Z")
BASE_SKILL_NAME = "trackself-platform"
MAX_SOURCE_FILE_BYTES = 1 << 20
MAX_SUPPORTING_BYTES = 8 << 20
MAX_SUPPORTING_FILES = 256


class BundleError(ValueError):
    """A source bundle is incomplete or unsafe to package."""


def _read_source_tree(root: Path, label: str) -> dict[str, bytes]:
    if root.is_symlink() or not root.is_dir():
        raise BundleError(f"{label} must be a real directory: {root}")

    contents: dict[str, bytes] = {}
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise BundleError(f"symbolic links are not allowed in {label}: {path}")
        mode = path.lstat().st_mode
        if stat.S_ISDIR(mode):
            continue
        if not stat.S_ISREG(mode):
            raise BundleError(f"only regular files are allowed in {label}: {path}")

        relative = path.relative_to(root).as_posix()
        pure = PurePosixPath(relative)
        if pure.is_absolute() or ".." in pure.parts or "\\" in relative:
            raise BundleError(f"unsafe {label} path: {relative!r}")
        if len(relative) >= 2 and relative[0].isalpha() and relative[1] == ":":
            raise BundleError(f"Windows drive syntax is not allowed in {label} paths: {relative!r}")
        if any(part.startswith(".") or part == "__MACOSX" for part in pure.parts):
            raise BundleError(f"ignored archive paths are not allowed in {label}: {relative!r}")
        if pure.name.casefold() in {"license", "license.md", "license.txt"}:
            raise BundleError(f"license files are not packaged from {label}: {relative!r}")
        if path.name.casefold() == "skill.md" and relative != "SKILL.md":
            raise BundleError(f"nested or non-canonical SKILL.md is not allowed: {relative}")

        data = path.read_bytes()
        if len(data) > MAX_SOURCE_FILE_BYTES:
            raise BundleError(f"{label} file exceeds {MAX_SOURCE_FILE_BYTES} bytes: {relative}")
        try:
            data.decode("utf-8")
        except UnicodeDecodeError as exc:
            raise BundleError(f"{label} file must be UTF-8 text: {relative}") from exc
        if not data.strip():
            raise BundleError(f"{label} file must not be empty: {relative}")
        contents[relative] = data
    return contents


def _frontmatter_fields(skill: bytes) -> tuple[str, str]:
    try:
        text = skill.decode("utf-8")
    except UnicodeDecodeError as exc:  # Also checked by _read_source_tree.
        raise BundleError("policy SKILL.md must be UTF-8 text") from exc

    lines = text.splitlines()
    if not lines or lines[0] != "---":
        raise BundleError("policy SKILL.md must start with YAML frontmatter")
    try:
        end = lines.index("---", 1)
    except ValueError as exc:
        raise BundleError("policy SKILL.md has unterminated YAML frontmatter") from exc

    values: dict[str, str] = {}
    for line in lines[1:end]:
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        match = re.match(r"^([A-Za-z][A-Za-z0-9_-]*):[ \t]*(.*?)\s*$", line)
        if not match:
            raise BundleError(f"policy SKILL.md has malformed frontmatter line: {line!r}")
        key, value = match.groups()
        if key in values:
            raise BundleError(f"policy SKILL.md has duplicate frontmatter field {key!r}")
        if not value or value[0] in "[{|>&*!":
            raise BundleError(f"policy SKILL.md field {key!r} must be a non-empty scalar")
        if value[0] in "'\"" and (len(value) < 2 or value[-1] != value[0]):
            raise BundleError(f"policy SKILL.md field {key!r} has an unterminated quoted value")
        if value[0] not in "'\"" and re.search(r":\s", value):
            raise BundleError(f"policy SKILL.md field {key!r} must quote a value containing colon-space")
        values[key] = value
    for key in ("name", "description"):
        if key not in values:
            raise BundleError(f"policy SKILL.md needs exactly one non-empty {key!r} field")
    name = values["name"].strip("'\"")
    if name != BASE_SKILL_NAME:
        raise BundleError(f"policy SKILL.md name must be {BASE_SKILL_NAME!r}, found {name!r}")
    description = values["description"].strip()
    if description[0] in "'\"":
        description = description[1:-1].strip()
    if not description:
        raise BundleError("policy SKILL.md needs a non-empty 'description' field")
    return name, description


def _identity(version: str, inputs: dict[str, bytes]) -> str:
    digest = hashlib.sha256()
    digest.update(b"mica-policy-bundle-v1\0")
    digest.update(version.encode("utf-8"))
    digest.update(b"\0")
    for path, data in sorted(inputs.items()):
        encoded_path = path.encode("utf-8")
        digest.update(len(encoded_path).to_bytes(8, "big"))
        digest.update(encoded_path)
        digest.update(len(data).to_bytes(8, "big"))
        digest.update(data)
    return digest.hexdigest()


def build_bundle(policy_dir: Path, platform_dir: Path = PLATFORM) -> tuple[str, dict[str, bytes], dict[str, object]]:
    """Return (skill name, archive files, source manifest) for these inputs."""
    version = policy_dir.name
    if not POLICY_VERSION_RE.fullmatch(version):
        raise BundleError(f"policy directory name must be a full mica-vN version, found {version!r}")

    policy_files = _read_source_tree(policy_dir, "policy")
    missing = sorted(REQUIRED_POLICY_FILES - policy_files.keys())
    if missing:
        raise BundleError(f"policy is missing required files: {', '.join(missing)}")
    if "source-manifest.json" in policy_files:
        raise BundleError("source-manifest.json is reserved for the generated provenance manifest")
    _frontmatter_fields(policy_files["SKILL.md"])

    platform_refs_dir = platform_dir / "references"
    platform_files = _read_source_tree(platform_refs_dir, "Multica platform references")
    actual_refs = set(platform_files)
    if actual_refs != PLATFORM_REFERENCES:
        raise BundleError(
            "Multica platform references changed: expected "
            f"{sorted(PLATFORM_REFERENCES)}, found {sorted(actual_refs)}. "
            "Review the package contract before building."
        )

    inputs: dict[str, bytes] = {
        **{f"policy/{path}": data for path, data in policy_files.items()},
        **{f"multica-platform/references/{path}": data for path, data in platform_files.items()},
    }
    identity = _identity(version, inputs)
    skill_name = f"{BASE_SKILL_NAME}-{identity[:16]}"

    root_skill = policy_files["SKILL.md"].decode("utf-8")
    root_skill, replaced = re.subn(
        r"(?m)^name:[ \t]*(?:trackself-platform|'trackself-platform'|\"trackself-platform\")\r?$",
        f"name: {skill_name}",
        root_skill,
        count=1,
    )
    if replaced != 1:
        raise BundleError("could not replace the base skill name in policy SKILL.md frontmatter")

    files: dict[str, bytes] = {"SKILL.md": root_skill.encode("utf-8")}
    for path, data in policy_files.items():
        if path != "SKILL.md":
            files[path] = data
    for path, data in platform_files.items():
        output_path = f"references/{path}"
        if output_path in files:
            raise BundleError(f"policy and platform files collide at {output_path!r}")
        files[output_path] = data

    source_hashes = {path: hashlib.sha256(data).hexdigest() for path, data in sorted(inputs.items())}
    output_hashes = {path: hashlib.sha256(data).hexdigest() for path, data in sorted(files.items())}
    manifest: dict[str, object] = {
        "schema_version": 1,
        "policy_version": version,
        "skill_name": skill_name,
        "bundle_identity_sha256": identity,
        "source_hashes_sha256": source_hashes,
        "packaged_file_hashes_sha256": output_hashes,
    }
    files["source-manifest.json"] = (json.dumps(manifest, indent=2, sort_keys=True) + "\n").encode("utf-8")
    for path, data in files.items():
        if len(data) > MAX_SOURCE_FILE_BYTES:
            raise BundleError(f"packaged file exceeds Multica's 1 MiB per-file limit: {path}")
    supporting_files = {path: data for path, data in files.items() if path != "SKILL.md"}
    if len(supporting_files) > MAX_SUPPORTING_FILES:
        raise BundleError(f"archive exceeds Multica's {MAX_SUPPORTING_FILES} supporting-file limit")
    if sum(map(len, supporting_files.values())) > MAX_SUPPORTING_BYTES:
        raise BundleError(f"archive exceeds Multica's {MAX_SUPPORTING_BYTES}-byte supporting-file limit")
    return skill_name, files, manifest


def write_archive(output: Path, files: dict[str, bytes]) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(output, "w", compression=ZIP_DEFLATED, compresslevel=9) as archive:
        for name, data in sorted(files.items()):
            entry = ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            entry.compress_type = ZIP_DEFLATED
            entry.create_system = 3
            entry.external_attr = (stat.S_IFREG | 0o644) << 16
            archive.writestr(entry, data, compress_type=ZIP_DEFLATED, compresslevel=9)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--policy-dir", type=Path, default=DEFAULT_POLICY)
    parser.add_argument(
        "--output",
        type=Path,
        help="archive destination (default: fork/trackself/<content-derived-skill-name>.skill)",
    )
    args = parser.parse_args()

    try:
        skill_name, files, _ = build_bundle(args.policy_dir)
    except BundleError as exc:
        parser.error(str(exc))

    output = args.output or (REPO / "fork/trackself" / f"{skill_name}.skill")
    write_archive(output, files)
    print(output)


if __name__ == "__main__":
    main()

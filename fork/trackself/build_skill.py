#!/usr/bin/env python3
"""Build a complete Trackself replacement from current Multica and KB sources."""

import argparse
import hashlib
import json
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile

REPO = Path(__file__).resolve().parents[2]
PLATFORM = REPO / "server/internal/service/builtin_skills/multica-platform"
OVERRIDES = Path(__file__).resolve().parent / "overrides"
DEFAULT_TRACKSELF = Path.home() / "workspaces/trackself/workspace-control/config/multica-skills/trackself-working-on-issues"
REQUIRED = {
    "agents.md", "autopilots.md", "issues.md", "mentions.md",
    "projects.md", "runtimes.md", "skill-import.md", "squads.md",
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--trackself-skill", type=Path, default=DEFAULT_TRACKSELF)
    parser.add_argument("--output", type=Path, default=REPO / "fork/trackself/trackself-platform.skill")
    args = parser.parse_args()

    actual = {p.name for p in (PLATFORM / "references").glob("*.md")}
    if actual != REQUIRED:
        raise SystemExit(f"Multica platform references changed: expected {sorted(REQUIRED)}, found {sorted(actual)}. Review the new bundle contract before packaging.")
    if not (args.trackself_skill / "SKILL.md").is_file():
        raise SystemExit(f"Trackself skill missing: {args.trackself_skill / 'SKILL.md'}")

    contents = {}
    for path in PLATFORM.rglob("*"):
        if path.is_file() and path.name != "SKILL.md":
            contents[path.relative_to(PLATFORM).as_posix()] = path.read_bytes()
    for path in OVERRIDES.rglob("*"):
        if path.is_file():
            contents[path.relative_to(OVERRIDES).as_posix()] = path.read_bytes()
    trackself_body = (args.trackself_skill / "SKILL.md").read_text()
    marker = "## Prepare execution and closeout"
    if marker not in trackself_body or "multica-working-on-issues" not in trackself_body:
        raise SystemExit("Trackself skill introduction changed; review its routing before packaging")
    trackself_body = (
        "# Trackself parent issue workflow\n\n"
        "This procedure is bundled inside the complete `trackself-platform` replacement. "
        "Use its `references/issues.md` for Multica command effects and other "
        "platform references only when their domain is needed.\n\n"
        + marker + trackself_body.split(marker, 1)[1]
    )
    for path in args.trackself_skill.rglob("*"):
        if path.is_file():
            data = trackself_body.encode() if path.name == "SKILL.md" and path.parent == args.trackself_skill else path.read_bytes()
            contents[(Path("references/trackself") / path.relative_to(args.trackself_skill)).as_posix()] = data
    contents["source-manifest.json"] = json.dumps({
        "multica_platform": hashlib.sha256((PLATFORM / "SKILL.md").read_bytes()).hexdigest(),
        "trackself_skill": hashlib.sha256((args.trackself_skill / "SKILL.md").read_bytes()).hexdigest(),
        "files": {name: hashlib.sha256(data).hexdigest() for name, data in sorted(contents.items())},
    }, indent=2).encode() + b"\n"

    args.output.parent.mkdir(parents=True, exist_ok=True)
    with ZipFile(args.output, "w", compression=ZIP_DEFLATED) as archive:
        for name, data in sorted(contents.items()):
            archive.writestr(name, data)
    print(args.output)


if __name__ == "__main__":
    main()

---
name: trackself-platform
description: "Use for Trackself parent issue orchestration and Multica platform contracts. Open only the reference needed for the current operation."
user-invocable: false
allowed-tools: Bash(multica *), Bash(git *), Bash(gh *)
---

# Trackself and Multica

For a Trackself automatic parent objective, open `references/trackself/SKILL.md`
and follow its context preparation, staged dispatch, result recording, and
disposition rules. That document comes from the Trackself workspace when this
bundle is built. Its named references live under
`references/trackself/references/`.

For platform command effects, open only the relevant reference:

| Reference | Domain |
|---|---|
| `references/issues.md` | Issue and PR side effects, statuses, child stages, wakeups |
| `references/mentions.md` | Mention and resource-link routing |
| `references/agents.md` | Agent definitions and skill binding |
| `references/squads.md` | Squad routing and leader state |
| `references/autopilots.md` | Autopilot triggers and runs |
| `references/projects.md` | Projects and durable resources |
| `references/runtimes.md` | Runtimes and task CLI boundaries |
| `references/skill-import.md` | Skill import and refresh |

The reference files are copied from this fork's current Multica platform
bundle at package time. Do not load every reference to prepare an issue turn.
The selected Trackself workflow owns when to read issue or comment history,
how to deliver results, and when to change status. Read `references/issues.md`
for the exact server behavior of a command you are about to use.

---
name: multica-runtimes-and-repos
description: "Use when a Multica runtime or daemon misbehaves: agent not running, task not claimed, runtime offline, workdir or session reuse, repository checkout."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Multica runtimes and repositories

Read `references/runtimes.md` before changing or diagnosing a runtime, daemon,
repository checkout, workdir, or task session. It defines the backend, daemon,
and CLI boundaries and the relevant read-only checks.

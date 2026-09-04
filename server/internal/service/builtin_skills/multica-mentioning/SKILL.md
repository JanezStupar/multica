---
name: multica-mentioning
description: "Use when an issue comment needs to @mention someone — link to a person, trigger another agent, hand work to a squad, or broadcast with @all. Whether to mention at all is covered by the runtime brief, not here."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Mentioning and delegating

Read `references/mentions.md` before writing a `mention://` link. It explains
which mention types enqueue work, which are inert links, and why a mention may
appear valid without causing the intended side effect.

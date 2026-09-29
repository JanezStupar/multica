---
name: trackself-platform
description: "Coordinate Trackself ticket work under the versioned Mica workflow and Multica platform contracts."
allowed-tools: Bash(multica *), Bash(git *), Bash(gh *), Bash(python3 *)
---

# Trackself issue workflow

For an enrolled issue, follow its pinned policy and the detailed
[`references/workflow.md`](references/workflow.md). An issue run also receives
[`runtime/issue-workflow.md`](runtime/issue-workflow.md) in its runtime brief;
use that short route to find the applicable workflow section. The selected
workflow governs authority, review, acceptance and delivery for every role.

Open the relevant `references/*.md` platform contract only when you need a
Multica command's effects. A bundle name or current workspace default does not
establish a ticket pin. Existing tickets retain their recorded version until
an authorized migration. Building or importing this bundle does not activate
it. The capability requirements and activation boundary are in the workflow
reference.

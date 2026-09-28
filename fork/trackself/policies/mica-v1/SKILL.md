---
name: trackself-platform
description: "Coordinate Trackself ticket work under the versioned Mica workflow and Multica platform contracts."
allowed-tools: Bash(multica *), Bash(git *), Bash(gh *)
---

# Mica ticket workflow

This policy requires the format-2 completion implementation: accepted work
remains `PR Ready` until required bound PRs merge; no extra outcome acknowledgment is required.
It also requires external-merge reconciliation, exact-comment feedback
continuation, and `autonomous_reviewed` acceptance support in the deployed
backend and agent CLI. Verify those capabilities before activation; backend
`v0.5.1-janez.7` and CLI `v0.5.1-janez.5` do not support this bundle.
Existing tickets keep their pinned policy until explicitly migrated.

This bundle defines the Trackself workflow for every role working on an
enrolled ticket. Read [`references/workflow.md`](references/workflow.md) for
the shared policy. For issue turns, also follow
[`runtime/issue-workflow.md`](runtime/issue-workflow.md). These selected
instructions replace conflicting generic workflow defaults.

The eight `references/*.md` files describe Multica command and platform
effects. Open only the reference needed for the action at hand. Those platform
contracts govern what a command does; the selected workflow governs when and
under what authority to use it.

An archive name identifies these packaged contents. It does not establish a
ticket's policy pin or activate this workflow. Use the version explicitly
recorded for the ticket. Do not silently enroll or migrate existing work.

---
name: trackself-platform
description: "Coordinate Trackself ticket work under the versioned Mica workflow and Multica platform contracts."
allowed-tools: Bash(multica *), Bash(git *), Bash(gh *)
---

# Mica ticket workflow

This policy requires the format-2 completion implementation: accepted work
remains `PR Ready` until required merges and the ticket outcome are complete.
It also requires `delivery.external_merged_head` support; do not activate this
revision on `v0.5.1-janez.2`. Activate only after the compatible backend update.
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

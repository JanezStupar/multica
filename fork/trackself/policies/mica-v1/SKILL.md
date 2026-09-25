---
name: trackself-platform
description: "Coordinate Trackself ticket work under the versioned Mica workflow and Multica platform contracts."
allowed-tools: Bash(multica *), Bash(git *), Bash(gh *)
---

# Mica ticket workflow

**Draft for the next policy version; not ready for activation.** The source now
includes the agreed `PR Ready`/`Done` and durable hold requirements. The released
workflow writes `done` at acceptance and does not establish these semantics.
Complete implementation, proof and authority bindings before building/importing
this revision for cutover. Existing tickets keep their pinned policy.

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

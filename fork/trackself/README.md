# Trackself Mica policy bundle

The [workflow spec](workflow_spec.md) records the agreed target for ticket
execution, review, acceptance and delivery. The [implementation plan](implementation_plan.md)
coordinates the engineering work. The policy archive built here is a prepared
input, not an activated workflow.

The builder only reads repository sources and writes a `.skill` archive. It
does not import a workspace skill, change agent or ticket bindings, enroll or
migrate tickets, or start work. Building, importing or binding a skill does not
by itself prove the complete workflow or activate ticket policy.

## Replacement capability

Multica supports per-agent built-in skill selection and replacement:

- `enabled_builtin_skill_ids: null` inherits the built-ins available to that
  agent. An explicit list is exact, and `[]` disables every built-in.
- `builtin_skill_replacements` maps an enabled stable built-in ID, such as
  `builtin:multica-platform`, to a workspace skill UUID. The embedded bundle
  is omitted for that agent, and a skill assigned in both ways is included
  once.
- A `multica-platform` replacement needs a root `SKILL.md`, the platform
  references, and `runtime/issue-workflow.md`. A missing or incomplete bundle
  blocks a claim instead of silently restoring conflicting instructions.

The generated issue brief uses the selected replacement's runtime instructions
for its workflow and retains Multica's command and safety instructions. The
platform references describe command effects; the selected Mica policy decides
when and under what authority to use them.

## Existing configuration

Migration `536_agent_builtin_skill_policy` adds replacement storage while
preserving the nullable built-in allowlist, including its inheritance and
exact-list behavior. Existing exact allowlists with old granular built-in IDs
are not automatically changed to `builtin:multica-platform`. Keep current
bindings as they are until the coordinated cutover reconciles the active
agents, all-role policy and ticket enrollment.

## Build a versioned archive

From the repository root, build the default `mica-v1` policy:

```bash
python3 fork/trackself/build_skill.py
```

The default policy source is `fork/trackself/policies/mica-v1`. Select another
version with `--policy-dir`; choose a different artifact destination with
`--output`. Without `--output`, the builder writes the content-named archive
under `fork/trackself/` with a gitignored `.skill` extension and prints its
path.

The archive contains the selected all-role workflow and runtime instructions,
the current eight Multica platform references, and `source-manifest.json`.
The manifest records the full policy version, complete bundle SHA-256 identity,
and hashes of every policy and platform input. The skill name uses the first
16 identity characters. Any content or policy-version change creates a new
skill name. The builder rejects malformed or oversized inputs, symlinks,
nested `SKILL.md` files and platform reference-set drift. The stable ZIP output
lets reviewers compare identical source inputs byte for byte.

Under this policy, each PR carries its branch, exact commit and draft code
handoff. PR reviews hold technical findings, fixes, validation and independent
verdicts; the ticket tracks outcome, owner, status, scope, blockers and
acceptance, with links to reviews. One ticket may coordinate multiple PRs
across repositories.

## Recoverable issue handoffs

For an explicitly enrolled issue, create a durable transfer to an agent or a
human member:

```bash
multica issue handoff create <issue-id> --file handoff.json
```

Supply a `request_key` UUID and reuse the exact request and key after an
ambiguous response. Use `multica issue handoff list <issue-id>` to reconcile
saved handoffs. The request names the outgoing task, recipient, target status
(`in_progress` or `in_review`), context mode (`fresh` or `resume`), instruction
and evidence URLs. Agent requests may use legacy `agent_id` or
`assignee_type: "agent"` and `assignee_id`. A human member request uses
`assignee_type: "member"` and `assignee_id`, and must target `in_review` with
fresh context; no agent task is created for that recipient. Resume mode also
requires an exact completed `resume_task_id` for the same agent, runtime and
issue. A candidate records `repository_url`,
`pr_url`, `branch`, full 40- or 64-character `commit_sha` and `draft: true`.
Use `"candidates": []` for non-code work. These PR and commit references are
declared inputs; the handoff does not verify provider state or compare the
current PR head with the declared commit.

Creation records intent. When the named outgoing task completes successfully,
Multica changes issue status and assignee and enqueues an agent recipient
together, provided the issue still matches the saved status/assignee. Other
active issue runs defer dispatch until the issue is clear. A failed or
cancelled outgoing task, or a changed issue owner/status, prevents launch and
leaves a record to inspect or cancel. A human handoff is consumed by the
completion marker `handoff_completed_at`; its `last_task_id` remains null.
Cancel a pending handoff with:

```bash
multica issue handoff cancel <issue-id> --handoff-id <uuid>
```

This disables pending execution and cancels an unstarted recipient task when
possible. A started recipient keeps running; use
`multica issue cancel-task <run-id>` to stop it. The API exposes create/list at
`/api/issues/{id}/handoffs` and cancellation at
`/api/issues/{id}/wakeups/{handoff-id}/disable`. The endpoint requires a ticket
policy pin and does not enroll tickets or activate a workspace default.

If a coordinated cutover imports an archive, import its new content-derived
name as a new skill with conflict handling set to fail. Do not overwrite an
older policy skill in place: tickets may still be pinned to its version. The
short name identifies archive contents only; the runtime must separately
record and enforce each ticket's selected policy version.

## Explicit ticket enrollment interface

The current CLI can inspect or explicitly enroll one issue:

```bash
multica issue workflow-policy get <issue-id>
multica issue workflow-policy pin <issue-id> --skill-id <workspace-skill-uuid>
```

The matching API is `GET /api/issues/{id}/workflow-policy` and
`POST /api/issues/{id}/workflow-policy` with `{"skill_id":"<workspace-skill-uuid>"}`.
Reading is available to workspace members. Pinning is a mutation for a human
workspace owner or administrator. The source must be a complete
`multica-platform` replacement in the same workspace. A successful pin freezes
that complete workflow bundle for later claims, even if the source skill or an
agent's replacement setting later changes. `get` returns the recorded version
and bundle with `workflow_bundle_pinned: true`,
`agent_instructions_pinned: false` and `model_settings_pinned: false` coverage.

Pin before the issue has any task history. Create it without assignment or
other task triggers, pin it, read the result back, then begin execution as
part of a coordinated cutover. An issue with any task history cannot use this
endpoint for migration, and an existing pin cannot be changed to a different
version. `get` returns 404 while an issue is unpinned; `pin` returns a conflict
when task history, a different pin or concurrent issue activity blocks
enrollment. Repeating an identical pin before task history is safe and returns
the existing snapshot. This is per-issue enrollment, not a workspace default.
The workflow-policy snapshot covers only the platform workflow rules and their
authority. Each agent's first task on an enrolled issue separately captures
that agent's behavioral profile, including instructions, model/effort/tier,
workspace context and selected skills; retries retain it until an authorized
profile reselection after that agent's pending tasks settle. Run history keeps
its original profile identity. Profile snapshots exclude credentials,
permission mode, MCP configuration, environment, custom-argument values and
runtime bindings; live access and credentials remain governed by the claim
path. These are separate snapshot layers, not new ticket permissions.
Importing a skill does not pin an issue.

## Workspace cutover and legacy issue migration

Workspace owners and admins can inspect the selected default, perform the
one-time cutover, and later change the default for future issues:

```bash
multica workspace workflow get [workspace-id|slug|prefix]
multica workspace workflow cutover [workspace-id|slug|prefix] --skill-id <workspace-skill-uuid>
multica workspace workflow set-default [workspace-id|slug|prefix] --skill-id <workspace-skill-uuid>
```

Cutover freezes pre-existing unpinned issues, including historical terminal
issues, and unfinished enrolled issues. Already enrolled terminal issues retain
their recorded policy and lifecycle. Historical done or closed issues keep
their status and history. A workspace with a claimed or executing issue task must
wait until that activity finishes. After cutover, newly created issues receive
the selected default;
changing that default does not rewrite existing issue snapshots. The first
cutover reports the frozen issue count, and an exact replay returns the stored
cutover time. The API routes are `GET /api/workspaces/{id}/workflow-default`,
`POST /api/workspaces/{id}/workflow-cutover` and
`PUT /api/workspaces/{id}/workflow-default`.

While an old issue is frozen, issue updates and comment creation, editing and
deletion are blocked for human and agent actors. Reading its history and
explicit issue deletion remain available. A human workspace owner or admin can
migrate one issue after recording why and how current work was reconciled:

```bash
multica issue workflow-policy migrate <issue-id> \
  --skill-id <workspace-skill-uuid> \
  --reason "why this issue is moving" \
  --reconciliation "remaining work, evidence, context and ownership reviewed"
```

Migration snapshots the selected complete workflow bundle on that issue and
unfreezes it. For an issue currently in a terminal status (including done,
cancelled or a custom done/closed status), add `--reopen-to <active-nonterminal-status>`
to explicitly move its current status while preserving its terminal history.
The target must be an active workspace status whose category is not done or
closed. The flag is required for terminal issues and forbidden for
nonterminal issues. Migration does not dispatch work, even when it reopens an
issue; check the migrated issue before starting any next task. The API is
`POST /api/issues/{id}/workflow-migrate` with `skill_id`, `reason` and
`reconciliation`, plus optional `reopen_to` fields.
Already accepted enrolled work must use recorded rejection before reopening;
migration is not a substitute for invalidating its acceptance.

## Candidate review and acceptance

Format 2 separates acceptance, delivery and outcome completion. Its independent
code review, database regressions and guarded Forgejo delivery trial passed;
[runtime-proof.md](runtime-proof.md#2026-09-25-format-2-completion-trial) records
the evidence and limits. Deployment and coordinated cutover remain required:
`v0.5.1-janez.1` writes `done` at acceptance and cannot run this policy.
Use the workflow operations for acceptance rather than moving the status card.

The format-2 authority file adds `accepted_status_key` (an existing custom
workspace status in the `started` category) and `outcome_agent_id` (a configured
workspace agent). Acceptance explicitly supplies `outcome_complete` and may
set `hold_delivery`. Held work can become ready, but cannot merge until release.
`workflow hold|release|complete|retry-outcome` require the acceptance ID and a
JSON body with `candidate_id`, `expected_revision` and `reason`. Read current
`available_actions`; a visible acceptance is not authority to change it.

Required merges and the actual outcome must both be complete before Done.
When outcome work remains, the pinned agent receives a bound task; its outcome
acknowledgment takes effect only after that task succeeds. Failed or cancelled
outcome work preserves its history and supports an authorized explicit retry.
If scheduling fails before an outcome task exists, the verified merge remains
recorded and scheduling retries automatically with bounded backoff. Held PRs
merged externally are observed without releasing the hold or issuing a merge.
Format-1 pins retain their released semantics. The policy envelope itself
remains format 1; this version refers to `runtime/policy.json` inside the bundle.

The optional `runtime/policy.json` in a policy source directory configures
machine-enforced authority. Its contents participate in the immutable bundle
identity. Without this file, independent review is required, workspace owners,
admins and members may accept, accepted PRs become ready, and autonomous
acceptance and merging are disabled. This is the unconfigured bundle's behavior,
not the agreed final choice for Trackself's trivial-work policy.

For example, the following **template** grants one configured agent autonomous
acceptance and squash merging, while human acceptance only makes PRs ready.
Replace `<acceptor-agent-uuid>` with an explicitly selected workspace agent ID
before building; this example does not configure any live agent:

```json
{
  "format_version": 1,
  "human": {"accept_roles": ["owner", "admin", "member"], "delivery": "ready"},
  "review": {"required": true},
  "autonomous_trivial": {
    "enabled": true,
    "acceptor_agent_ids": ["<acceptor-agent-uuid>"],
    "delivery": "merge"
  },
  "delivery": {"merge_method": "squash", "multi_pr_merge_order": "explicit"},
  "supervisors": []
}
```

Merge methods are `merge`, `squash` or `rebase`; multi-PR merging requires an
explicit ordered PR list at acceptance. Supervisor entries take `agent_id`
and delegated `scopes` (`review`, `acceptance`, `delivery`). Classification
criteria, capability selection and review depth remain editable instructions
in the policy bundle. The authority file does not decide whether a task is
trivial: the authorized acceptor records that judgment and its reason.
An autonomous request becomes acceptance only after its requesting task
completes successfully and the candidate, review and authority still match.

Scoped exceptions record the candidate, policy version, reason and consequences;
they do not rewrite the bundle or other tickets. A review exception can waive
review; acceptance can name one human or agent; delivery can select readiness
or an explicit merge method. Supervisors can grant only their delegated scopes.
An exception that affects accepted work requires rejection before revocation.

An enrolled issue's workflow state is available with:

```bash
multica issue workflow get <issue-id>
```

The result shows the candidate ID and issue revision, review verdicts,
acceptance blockers, delivery preview, per-PR delivery state, retained writer
contexts and exception history. A reviewer agent records its independent
verdict and PR review links with `multica issue workflow review <issue-id>
--file <json>`. Human acceptance or rejection uses the exact candidate ID and
revision from `get` via `multica issue workflow accept|reject <issue-id>
--file <json>`. Rejection may select a retained task only from the options
returned by `get`. Use `multica issue workflow exception <issue-id> --file
<json>` to grant a scoped, explained exception; `multica issue workflow
exception revoke <issue-id> <exception-id> --file <json>` records its
revision-bound revocation and consequences. These commands return current
workflow state so the caller can reconcile each action. Request JSON is strict,
limited to 64 KiB, and may be supplied on stdin with `--file -`.

Each accepted candidate is bound to its exact commit set and issue revision.
New commits need renewed evaluation. The server reports why an action is
blocked and previews readiness or merge actions; provider secrets are not
returned in the workflow record.

## Coordinated cutover

Binding the Mica policy requires coordinated review of shared Trackself
workflow guidance, provider configuration, Multica instructions and ticket
policy snapshots. Importing or preparing a candidate bundle alone does not
freeze current tickets or activate the workspace default. At the coordinated
cutover, old unfinished tickets remain frozen until explicit migration
reconciles current work, remaining scope, evidence, context and ownership.
Historical terminal tickets retain their done or closed status and history;
they are not reclassified as unfinished. Reopening one requires an explicit
migration target and does not dispatch work. Do not bind only the former parent
orchestrators as a substitute for the agreed all-role workflow.

Before cutover, verify the generated archive with Multica's actual
`parseSkillArchive` importer and review its root skill, runtime instructions,
policy reference and source manifest. Those checks establish package
compatibility and source identity; they do not establish live bindings,
ticket-policy enforcement, provider behavior or end-to-end workflow readiness.

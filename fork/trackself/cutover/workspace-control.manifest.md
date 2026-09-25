# Workspace-control cutover candidate

**Inactive preparation only.** The active desired-state TOML, installed skills,
live agent records, ticket policies and runtime settings are unchanged. This
candidate is paired with [the KB patch](kb.patch); apply neither independently
as an active workflow change.

## Candidate identity

The [baseline](baseline.json) identifies the current owning working-tree inputs,
including pre-existing uncommitted work, and both patch hashes. The workspace
patch contains 30 changed paths. It preserves the existing runtime identities,
workspace bindings, credential groups and permission settings.

## Agent and skill preparation

The proposed `config/mica-cutover-profile.toml` selects Astra low for Mica,
Luna xhigh for bounded implementation, Sol medium for senior implementation and
independent review, and the existing native QA/release capabilities. These are
proposed cutover defaults, not active model changes.

Five Review Controller identities are excluded from new routing. Their agent
records, task history and sessions are retained. The proposal leaves 24 routed
profiles across the three Linux and two native environments. Review/fix can use
a capable writer context; independent final review uses a fresh read-only
reviewer. Native profiles remain subject to their separate host rollout.

`scripts/prepare-mica-agent-handoff` reads the proposed profile and existing
identity inventory. It requires the final bundle's `source-manifest.json` and
its actual imported workspace skill UUID, rejects the legacy skill UUID, and
prints an inactive JSON handoff or creates resolved desired-state JSON at an
explicit output path. It includes proposed updates and excluded identities;
it makes no provider calls. The full bundle replaces the platform built-in
and is not also directly assigned, avoiding duplicate policy loading. No
placeholder UUID or active binding is generated. Production acceptance and
merge authority must be selected separately in the owning policy bundle.

The old granular built-in IDs for issues, mentions, agents, squads,
autopilots, projects, runtimes and skill import are retired by the current
Multica platform bundle. The candidate enables only that platform slot, plus
Mika's separately scoped onboarding, and requires all eight platform
references in the imported replacement. This preserves those capabilities
without retaining inert old allowlist entries.

The legacy desired-state validator still checks the active 29-agent
configuration, and its unsupported CLI `apply` remains disabled. The separate
`scripts/reconcile-mica-agent-definitions` is a guarded Linux-only API path.
It loads resolved persistent desired state and checks it against the source
manifest and current role profile; verifies imported skill identity and the
embedded manifest hash; reads all 15 Linux agents before any write; and rejects
non-idle or unbound agents, unexpected direct skills or replacement maps, and
runtime, permission or credential-presence drift. It changes only model,
effort, instructions and skill/built-in bindings through supported endpoints.
`plan` is the default and read-only, `check` reports drift, and `apply` requires
exact workspace UUID confirmation. A post-write read-back checks each changed
agent. The token is supplied via an operator-owned 0600 file, never printed.
Native agent updates remain paused for their separate host rollout.
Because agent policy takes several API calls, Linux `apply` also requires an
operator assertion that all three Linux daemons have been stopped and a live
runtime-list read-back showing their exact declared UUID/daemon rows offline.
The read-back repeats before each agent's writes. Offline status alone is not
a stop guarantee; keep the daemon processes stopped until every agent reads
back in sync and the coordinated ticket/default cutover is complete.

## Removed process and preserved operations

The candidate removes superseded stage/child/verdict execution recipes and
mandatory ticket-body narration. It creates no legacy archive. The old context
projector remains only for reading frozen work during explicit migration;
it is not a Mica dispatch mechanism. Workspace/project validation, runtime
isolation, queue order, native QA/release procedures and exact-commit transport
remain owned by the existing tooling and runbook.

## Validation and limits

- KB candidate: ADR reference maintenance, lint, relative links and diff checks
  passed. Independent review covered the KB, fork documentation, workspace
  candidate and preparation instructions; its runbook finding was fixed.
- Workspace candidate: 57 existing agent-definition tests, 6 preparation tests
  and 8 guarded-reconciler tests passed. Legacy manifest validation and diff
  checks passed.
- Both complete patches pass `git apply --check` against the owning current
  working trees; the named inputs still match their preparation baselines.
- The read-only live check for all 15 Linux agents passed against the active
  legacy configuration. It reported the expected retired-built-in availability
  warnings. This is not a live check of the inactive proposed configuration.

No final skill was imported, agent changed, ticket frozen/migrated, new workflow
activated, native host upgraded, or runtime restarted by this preparation.
The [production policy choices](../workflow_spec.md#agreed-production-policy)
are agreed. Revised `PR Ready`/`Done` and durable hold mechanics passed source
review and the [format-2 trial](../runtime-proof.md#2026-09-25-format-2-completion-trial);
their release and deployment remain pending. The inactive policy encodes Linux authority
bindings and automatic merge after human acceptance unless explicitly held;
final import identity, live mutation/read-back proof and coordinated cutover
remain outstanding. Offline preparation does not prove the live cutover.

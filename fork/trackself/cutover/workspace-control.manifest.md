# Workspace-control cutover candidate

**Inactive preparation only.** The active desired-state TOML, installed skills,
live agent records, ticket policies and runtime settings are unchanged. This
candidate is paired with [the KB patch](kb.patch); apply neither independently
as an active workflow change.

## Candidate identity

The [baseline](baseline.json) identifies the current owning working-tree inputs,
including pre-existing uncommitted work, and both patch hashes. The workspace
patch contains 28 changed paths. It preserves the existing runtime identities,
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
prints an inactive JSON handoff. It includes proposed updates and the excluded
identities; it makes no provider calls. Production acceptance and merge authority
must be selected separately in the owning policy bundle.

This preparation does not make the legacy desired-state validator accept new
live bindings: that validator still checks the active 29-agent configuration.
The candidate explicitly disables its unsupported live `apply` path. Before
activation, finish the supported agent mutation/read-back path and reconcile
its persistent desired state with the generated handoff. Do not send the whole
handoff JSON to an API as though it were an update request body.

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
- Workspace candidate: 57 existing agent-definition tests and 5 preparation
  tests passed. Legacy manifest validation and diff checks passed.
- Both complete patches pass `git apply --check` against the owning current
  working trees; the named inputs still match their preparation baselines.
- The read-only live check for all 15 Linux agents passed against the active
  legacy configuration. It reported the expected retired-built-in availability
  warnings. This is not a live check of the inactive proposed configuration.

No final skill was imported, agent changed, ticket frozen/migrated, new workflow
activated, native host upgraded, or runtime restarted by this preparation.
The [production policy choices](../workflow_spec.md#agreed-production-policy)
are agreed. Revised `PR Ready`/`Done` and durable hold mechanics still need
implementation and proof; concrete authority bindings, human delivery default,
final import identity, supported mutation/read-back and coordinated cutover
remain outstanding. The prior validation above does not prove these later requirements.

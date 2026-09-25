# Prepared Mica cutover

These patches prepare the agreed Trackself workflow change. **Live cutover is
paused.** They have been reviewed in isolated copies; they are not installed
KB instructions, agent settings, imported skills, or ticket policy.

The server and Linux runtime upgrade is separate and complete. The owning
[release record](../release.md) links the deployment record; the
[runtime proof](../runtime-proof.md) identifies the successful Linux provider
trials and their source snapshots. Native macOS/Windows checks accompany their
own deployment and do not block Linux activation.

## Review surface and ownership

- [KB patch](kb.patch): provider-neutral workflow and authority rules, coherent
  ADRs, compact issue objectives, generic handoff/review skills and navigation.
  It preserves repository-owned context and useful bounded plans.
- [Workspace patch](workspace-control.patch): Trackself instructions, provider
  adapter, inactive agent configuration preparation, validation and tests.
  The [workspace manifest](workspace-control.manifest.md) describes its bounds
  and the remaining activation inputs.
- [Baseline](baseline.json): owning repository heads and SHA-256 identities of
  the exact working-tree inputs used for each changed path. `null` means a new
  path. These candidates include the owners' existing uncommitted baseline;
  applying them to bare repository HEAD is not equivalent.

The fork spec owns Multica behavior. KB owns shared policy. Workspace-control
owns the selected agent/runtime configuration. These patches are a pending
change set, not another owner of those rules. Once the coordinated changes are
applied and verified, remove the consumed patches and update these pointers;
retain lasting instructions only in their owning repositories.

## Verify before applying

Use fresh disposable copies of the current owning working trees, preserving
unrelated edits. Compare the named inputs with `baseline.json`; reconcile drift
instead of overwriting it. With `CUTOVER` pointing to this directory and
`KB_COPY` / `CONTROL_COPY` to those copies:

```sh
git -C "$KB_COPY" apply --check "$CUTOVER/kb.patch"
git -C "$CONTROL_COPY" apply --check "$CUTOVER/workspace-control.patch"
git -C "$KB_COPY" apply "$CUTOVER/kb.patch"
git -C "$CONTROL_COPY" apply "$CUTOVER/workspace-control.patch"
```

Review the complete resulting surface together. Run KB lint, link checks and
ADR reference maintenance with the workspace copy as a consumer; run workspace
agent validation and the affected tests. A patch fitting its baseline proves
neither semantic acceptance nor live workflow readiness.

Do not install the changed skills or replace active instructions piecemeal.
Reconcile the generated runtime policy, desired agent state, operational
runbook and KB together at the authorized cutover. No archive import, live
agent update, ticket freezing, default selection or migration occurs by reading
or testing these candidates.

## Remaining activation inputs

- Implement and prove `PR Ready`/`Done` and persistent hold behavior before
  activation; the release uses `done` at acceptance. Encode the
  [agreed production policy](../workflow_spec.md#agreed-production-policy), then
  resolve its [remaining bindings and defaults](../workflow_spec.md#remaining-policy-parameters).
  The draft bundle is not ready for activation; the disposable proof policy
  does not supply production settings.
- Finalize the policy archive and verify its actual imported identity. Generate
  and review the agent handoff with that identity; do not reuse the legacy skill
  UUID or invent a new one.
- Resolve the supported agent update/read-back path, inspect current tasks and
  dispatch sources, and prepare the exact workspace cutover. New tickets then
  use the selected policy; old unfinished work stays frozen until explicitly
  migrated. Preserve its agent records, contexts, evidence and remaining work.

Offline validation and live configuration observations are recorded in the
workspace manifest. Active configuration observations describe the legacy
configuration, not conformance to this inactive candidate.

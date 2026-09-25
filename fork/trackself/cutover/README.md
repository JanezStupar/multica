# Prepared Mica cutover

**Live cutover has not occurred.** The patches and baseline here are earlier
preparation inputs, superseded by the independently reviewed owner candidates:

- KB: `dfefa3c236cb2a66dcb4919db9e93f920fc36865`, based on Forgejo main
  `68afe455ea7ff9dac855787cf9b2785b449a4e04`.
- Workspace-control: `d6338efb66b7e3de663d0ed5125d5ff396740e1a`, based on main
  `6871897ce8c95126955f263127ae441bcf0276b4`.

Do not apply the old patches as the final cutover. The current candidates include
review corrections and the actual imported skill UUID. They remain local in
`/tmp/mica-kb-publication-candidate` and `/tmp/mica-control-candidate.xRKBeX`;
publication awaits scoped user approval after automatic review rejected the two
main-branch pushes. The prepared execution sequence is
`/tmp/mica-followup-deploy/cutover-commands.md`. Preserve the active owner edits
when adopting the exact published commits; verify branch task checkout before
activation. Retire these earlier preparation inputs after owner adoption.

The format-2 server and Linux runtime upgrade is complete. The owning
[release record](../release.md) links deployment evidence; the
[runtime proof](../runtime-proof.md) records provider trials. Native checks
accompany their own deployment and do not block Linux activation.

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

- Deploy the proven format-2 `PR Ready`/`Done` and persistent hold implementation
  before activation; the installed release uses `done` at acceptance. Apply the
  [agreed production policy](../workflow_spec.md#agreed-production-policy), then
  resolve its [remaining bindings and defaults](../workflow_spec.md#remaining-policy-parameters).
  The source bundle encodes the agreed production settings; its exact imported
  identity and coordinated activation still require verification.
- Finalize the policy archive and verify its actual imported identity. Generate
  and review the agent handoff with that identity; do not reuse the legacy skill
  UUID or invent a new one.
- Prove the prepared Linux agent update/read-back path against the selected
  production bundle, inspect current tasks and dispatch sources, stop the
  three Linux daemons for quiescent multi-call agent reconciliation, and prepare
  the exact workspace cutover. New tickets then
  use the selected policy; old unfinished work stays frozen until explicitly
  migrated. Preserve its agent records, contexts, evidence and remaining work.

Offline validation and live configuration observations are recorded in the
workspace manifest. Active configuration observations describe the legacy
configuration, not conformance to this inactive candidate.

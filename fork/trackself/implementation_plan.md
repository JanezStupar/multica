# Mica workflow implementation and cutover

The implementation is published and deployed as `v0.5.1-janez.2` from source
commit `0f23313676b0fcc66f197dd9dffef7c4feb10d09`. Production server, main and
branch-2 runtime health and authenticated checks passed. Branch-1 has the new
image and remains stopped; its previous fault was not repaired by this upgrade.
Publication and platform deployment did not activate the Trackself workflow. The release
record in [release.md](release.md) owns artifact identity and points to the
private-infra deployment record that owns deployment state. The [README](README.md)
owns implementation usage and limits.

## Evidence and boundaries

The Linux provider trials established same-ticket handoff and retained-writer
continuity, fresh independent review, rejection/rework, human acceptance,
autonomous trivial acceptance, exact-head PR delivery, and ordered delivery
across repositories. The final trial also verified daemon selection of the
runtime-provided CLI path. The exact trial workspaces, task/session identities,
source overlays, test PRs and cleanup evidence are recorded in
[runtime-proof.md](runtime-proof.md). Those trials used source snapshots based
on `c052b3b5cb68a4c23929ad51697657fe67b1473b` plus recorded uncommitted
overlays; they are evidence for those snapshots, not a deployed-environment
canary or a byte-for-byte test of the release source commit.

Automated and independent code review evidence is summarized in the release
handoff and detailed in the implementation history retained by Git. Current
source, tests and the importer are authoritative for implementation behavior;
this plan does not duplicate their mechanism descriptions or session diary.

The exact published policy was imported as an unbound Trackself skill; no
agent binding or workspace default changed, and no live ticket was frozen or
migrated. The proof policy was imported only in a disposable workspace. Native macOS and Windows execution remains deferred until
deployment to those hosts. Release publication and deployment are not workflow
activation.

## Remaining coordination

The 2026-09-25 working candidate extends the released acceptance contract with
format-2 PR Ready, durable hold/release and outcome completion. Focused checks,
clean migrations, independent code acceptance and the guarded Forgejo trial
passed; [runtime-proof.md](runtime-proof.md#2026-09-25-format-2-completion-trial)
records exact evidence and broader frontend baseline failures. The KB and
workspace-control consumers have independently reviewed local commits, recorded
in [the cutover preparation status](cutover/README.md). Their Forgejo main
pushes await scoped user approval after automatic review rejected publication;
active owners and bindings remain unchanged. Human acceptance defaults to automatic merge unless explicitly
held, and the inactive policy encodes the selected Linux authority bindings.
The follow-up is published as `v0.5.1-janez.2` from
`0f23313676b0fcc66f197dd9dffef7c4feb10d09`, and is deployed but not activated.
The earlier provider trials are supplemented by the new format-2 trial.

Both disposable completion-test Compose projects, volumes and networks were
removed after validation. The workstation and Linux-runtime databases were
untouched. The production
`pr_ready` status currently has terminal category `done` and no tickets;
activation must recheck that baseline and change it to `started` before using
the format-2 policy. Agent reconciliation also requires the three Linux daemons
to remain stopped through configuration read-back and coordinated cutover.

1. Reconcile the prepared KB and `workspace-control` candidates against their
   current owning repositories and review all consumer changes together. The
   candidate artifacts record their baselines and are not authority to apply
   them to active consumers. Verify the updated KB at the branch runtimes'
   actual task workspaces: refreshing the operator's `/home/janez/.codex-kb`
   does not refresh those containers' separate Codex homes or workspace copies.
2. Reconcile the deployed format-2 implementation with the consumers. Apply the
   [agreed production policy](workflow_spec.md#agreed-production-policy), then
   resolve only the [remaining bindings and defaults](workflow_spec.md#remaining-policy-parameters)
   in their owners. Classification, ordinary review, supervisor boundaries and
   squash merging are agreed; disposable test settings are not production policy.
3. Rebuild and review the policy bundle after the final consumer and delivery
   decisions; verify its content-derived identity with the actual Multica
   importer, then prepare matching all-role bindings and workspace desired
   state. Keep current bindings and defaults unchanged during preparation.
4. Before activation, reconcile the live dispatch sources and running work for
   the exact target workspace. Confirm the new-ticket default, cutover freeze,
   and explicit migration path with read-back evidence. At cutover, new tickets
   may use the selected policy; old unfinished tickets remain frozen until an
   individual migration preserves their work and ownership.
5. When native macOS or Windows agents are deployed, run their targeted dispatch,
   repository-access, and fresh/retained-context checks. These host checks do
   not block Linux activation and do not repeat server-side delivery proof.

Consumer preparation and runtime deployment remain separate from active workflow
cutover. Import, binding, default selection, live ticket freeze/migration, and
any non-disposable delivery action each require exact-scope authorization and
read-back under the owning workspace procedures.

## Completion boundary

Format 2 is released and deployed after source review, bounded completion
proof and production-copy migration rehearsal. Cutover coordination
is not complete until the consumers, bundle identity, workspace configuration,
and live freeze/migration procedure are reconciled. Do not retire this plan before
that boundary; after coordination ends, retain lasting operating guidance in
the README and requirements in the spec. Git preserves prior implementation
diagnoses and detailed proof history.

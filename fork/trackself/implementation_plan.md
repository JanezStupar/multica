# Mica workflow implementation and cutover

The implementation is published as `v0.5.1-janez.1` from source commit
`3ed16d8217503e168961f1d52ba97a2c20aae207`. The production server and all
three Linux runtimes run this release, and the recorded production migration,
configuration, health and authenticated API checks passed. Publication and
platform deployment did not activate the Trackself workflow. The release
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

No production policy was imported or bound, no workspace default changed, and
no live ticket was frozen or migrated. The proof policy was imported only in a
disposable workspace. Native macOS and Windows execution remains deferred until
deployment to those hosts. Release publication and deployment are not workflow
activation.

## Remaining coordination

1. Reconcile the prepared KB and `workspace-control` candidates against their
   current owning repositories and review all consumer changes together. The
   candidate artifacts record their baselines and are not authority to apply
   them to active consumers.
2. Implement and prove the revised `PR Ready`/`Done` and durable hold requirements;
   the release currently marks acceptance `done`. Apply the
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

The implementation is released and the named Linux trials have passed within
the evidence boundaries above. Cutover coordination is not complete until the consumers,
revised completion/hold mechanics, production policy encoding, bundle identity, workspace configuration, and
live freeze/migration procedure are reconciled. Do not retire this plan before
that boundary; after coordination ends, retain lasting operating guidance in
the README and requirements in the spec. Git preserves prior implementation
diagnoses and detailed proof history.

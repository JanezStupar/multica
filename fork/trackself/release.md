# Mica release and deployment

## Current backend: v0.5.1-janez.17

Deployed on 2026-10-02 from `27143a8bf0fd00e3b78aa3bf4f55674444378439`.
Scheduled continuations now record their exact handoff ancestry, so subsequent
human comments can advance the workflow without borrowing authority from a
shared conversation ID. Migration 598 adds the server-owned source column.
Fresh-session reset, wakeup revocation, child inputs and explicit retry/rerun
boundaries are preserved; offline tasks retain their input receipts until claim.

Independent Sol review, 49 bounded race-enabled test groups (131 unique test
events), exact binary vulnerability scanning, production readiness, migration
readback and the owning validator passed. Wider baseline-reproduced workflow
failures remain outside this fix. Only backend image/version selector changed;
existing clients and daemons retain their versions and supported reset protocol.

TRA-647's historical disconnected runs were not backfilled. Supported recovery
reran Mewina from the exact latest handoff recipient and verified that the new
Windows run retained both the explicit source and existing conversation.
Private-infra `infra/automation-server/multica/upgrade-v0.5.1-janez.17.md`
owns exact artifacts, validation limits and migration-aware rollback.

## Previous backend: v0.5.1-janez.16

Deployed on 2026-10-01 from `0dcd25cd3d56a334e1c949c3852b69913b7df880`.
Explicit member assignment now permits a fresh run after a consumed human handoff.
Migration 597 requires the actual member caller recorded in the task snapshot,
matching assignment audit evidence and current recipient/runtime. Creator-derived
human attribution cannot authorize a run, including when agent audit writes fail.
Existing acceptance, frozen/terminal, active-work, handoff and lineage fences remain.

Independent Sol review and 29 final database assignment/claim/enqueue/handler checks
passed; the exact versioned backend binary passed vulnerability scanning. The wider
suite retained baseline-reproduced workflow failures and a fixture-residue failure;
the owning receipt records them. Production readiness, migration readback, exact
binary checksum, unchanged service settings and the production validator passed.
Only backend image/version selector changed; web/agents remain `.15`.

TRA-641's older queued Mika task lacks the new actual-caller proof and remains
unclaimable. No ticket state, authority or run was changed/backfilled after the user
reported merging its PRs. Completion does not require another agent run.
Private-infra `infra/automation-server/multica/upgrade-v0.5.1-janez.16.md`
owns exact artifact identities, validation limits and migration-aware recovery.

## Previous backend; current web and agent CLIs: v0.5.1-janez.15

Agent rollout verified on 2026-09-29 from
`3d3938025f48ee8d6b6e93d100cc082a1f05070c`. All three Linux and both native
macOS/Windows Trackself daemons, plus the operator CLI, use `.15`. Codex is
`0.159.0` on all five agent runtimes. All 24 retained mappings are in sync;
Senior Implementor uses GPT-6.1-Sol medium and Acceptance Reviewer high,
with Luna settings retained. Fresh daemon catalog readback confirms medium/high.
The current-account refresh has a 15-second bound and failed catalogs retain
fallback provenance so recovery can retry. No real-agent inference was run.

Backend/web `.15` images and six CLI archives are published and checksum-verified.
Production backend/web are deployed at the exact `.15` digests recorded in
private-infra. Protected database/config backups, readiness, exact source identity,
service-setting comparisons and the production validator passed. Source is
committed and pushed. TRA-639 was restored to `in_review` at revision 44 through
an exact-revision status update without starting an agent. Its existing policy,
candidate/PASS and acceptance/delivery evidence were preserved; no PR was changed.
Private-infra `infra/automation-server/multica/upgrade-v0.5.1-janez.15.md`
owns exact artifact identities, validation, activation and recovery.
Desktop/mobile apps were not released in this round.

## TRA-639 workflow correction

The user authorized the TRA-639 correction on 2026-09-29, alongside current-account
Codex model discovery. Live rollout and ticket recovery are verified above.

### TRA-639: passing review escalated on its own unfinished run

Observed on backend `.14`, 2026-09-29. All times below are UTC. The revised
desktopapp candidate is `12652aa314e3a058a3b3b9240221f9203a92f4d4`, candidate
record `01a0ee10-c164-70c0-8d34-ac27262d3c9e`, on
[PR #33](https://git.thn.janezstupar.com/trackself/desktopapp/pulls/33).
Pinned policy: `sha256:487c761ae70d3c5c3073dc0ce9f0e9fc1d8849fc046a425ce2a3267f2ac8f500`.

- 16:47:50: implementation handoff moved the issue to `in_review` and started
  fresh reviewer task `01a0ee10-c17a-77a5-9ded-2d6901ff8832`, distinct from
  writer task `01a0ee00-111f-70ca-8307-faccdda6ee13`.
- 16:54:03: reviewer recorded PASS, review
  `01a0ee16-72ae-7e4b-8ff2-f920b2f48b34`, with
  [commit-bound provider evidence](https://git.thn.janezstupar.com/trackself/desktopapp/pulls/33#issuecomment-538).
  Its subsequent workflow read reported `review_not_independent` while that
  reviewer task was still running.
- 16:55:39: reviewer completed. At 16:55:50 its requested handoff
  `01a0ee17-8470-77d8-966e-30ea476138ec` executed, assigning Mika and explicitly
  changing the issue from `in_review` to `in_progress` to investigate the guard.
- 16:58:31: Mika reported two reads with no acceptance blockers, retained the
  valid PASS, and finished without restoring `in_review`. Diagnostic readback
  at revision 43 confirmed `in_progress`, the same candidate/PASS, and
  `acceptance_blockers: []`. No acceptance or delivery was recorded.

Code diagnosis: `workflowReviewSatisfiedForRequest` in
`server/internal/service/workflow_authority_acceptance.go` requires reviewer
completion, except for an eligible running reviewer's own autonomous acceptance
request. `workflow_authority_state.go` maps failures from this predicate to
`review_not_independent`, conflating pending completion with invalid review
independence. The observed timing explains why the blocker disappeared after
the reviewer finished. The durable handoff, rather than a failed initial status
transition, explains the later `in_progress` state.

Implemented correction: expose pending reviewer completion separately
from invalid independence while preserving the completion guard; have a reviewer
finish normally after PASS without escalating its own pending completion; keep
`in_review` during procedural coordination unless implementation changes are
required. Regressions cover running PASS, successful completion clearing the
pending condition, and failed/cancelled reviewer runs remaining blocked.
Coordination status preservation is instruction guidance, not an automatic
status mutation. Scoped ticket recovery restored
`in_review` without replacing candidate/review evidence or granting acceptance.

The original diagnosis changed no runtime or ticket state. The correction now
adds a distinct `review_pending_completion` blocker and translated waiting copy,
preserving human-only authority and reviewer completion/independence guards.
Trackself reviewer/coordinator instructions preserve `in_review` for procedural
coordination. Focused service/database race checks passed with 62 PASS events,
zero skips; the broader autonomous scope-change regression also fails against
unchanged source `5b0fb26f` and is outside this correction. Native desktop/live-provider
evidence gaps remain separately owned; they were not the cause of this handoff.

## Previous backend/web and current desktop: v0.5.1-janez.14

Deployed on 2026-09-29 from `08b0c7b747dcc306adfad73d3fddc7a169351f0a`.
Explicit human Done now
uses ordinary status updates with a durable member decision, even when legacy
candidate bookkeeping is incomplete. Agent and background workflow guards
remain enforced. Candidate/review/PR evidence is preserved, and later human
reopening, rejection and migration decisions supersede the earlier decision.
Shared web/desktop controls no longer redirect Done to candidate acceptance.

Backend digest: `sha256:13cdd453f5c55fceba682949c6286f80e2d06ce5f3e7898c141f14cfd5222d4e`.
Web digest: `sha256:217c824c3b45c1f557ce77c3b1114e409a5601901ce7ca43e3aacf87121f9a55`.
Exact source, metadata and the installed Linux AppImage are checksum-verified
in their matching Forgejo generic packages. Desktop profiles/launcher were
preserved; its daemon now uses the bundled
`.11` CLI. Other agent runtimes and policy bindings were not changed.

Independent Sol review, focused database race/UI regressions, typechecks,
vulnerability scan, image/AppImage checks and production validation passed.
The full frontend suite retains unrelated translation/contrast failures and
desktop git-fixture failures from the sandbox run. No full GUI or real-agent
canary is claimed. Runtime identity and migration 596 were verified.

TRA-583 is Done at revision 41 through the supported human status API, with
repeated stable readback and no active runs. Its existing evidence and linked
merged PRs #5/#9 were preserved; no candidate or acceptance was fabricated.
Private-infra's `infra/automation-server/multica/upgrade-v0.5.1-janez.14.md`
owns detailed validation and recovery instructions. Protected host state is
under `before-v0.5.1-janez.14`; the installed desktop predecessor is retained
beside its AppImage. Mobile was not released.

## Previous backend: v0.5.1-janez.13

Deployed on 2026-09-29 from `28bb04f64d95da53a7e3c0fed1d5647df3dea37a`,
at digest `sha256:bb62c69d210dc5187769d8ac16ac56a9cabc3f2599b6ff45d332604c0c843d6e`.
Exact source and metadata are checksum-verified in the matching Forgejo generic
package. The backend now accepts a delivered human approval received before
registration of the first single-PR candidate when provider-timed head evidence
proves the same approved commit. Independent review, mapped human authority,
current PR/head and edited/deleted-source checks remain enforced. Ambiguous
ordering or missing historical provider head-time evidence still fails closed.
Ready-only approval cannot become merge authority.

Focused database regressions with the race detector, independent Sol review,
vulnerability scan and the full production deployment validator passed. Exact
running image/commit and unchanged service settings were read back. No migration,
policy rebind or client update was required; no real-agent canary was dispatched.
TRA-637's repeated human approval had already removed WIP before deployment;
PR #31 was subsequently merged separately at 09:55:29 UTC. This rollout made no
ticket/PR mutations and does not count that recovery as proof of the new path.

Protected rollback state is at
`utility-server:/opt/multica/config-backups/before-v0.5.1-janez.13`.
Private-infra's `infra/automation-server/multica/upgrade-v0.5.1-janez.13.md`
owns deployment and recovery evidence. Agent CLIs remain `.11`; web/desktop
remain `.5`; `.12` policy activation and pending Windows state are unchanged.

## Previous backend: v0.5.1-janez.12

Deployed on 2026-09-29 from `3c8118522a6af4c64ab5228781dc4b7bd1046083`,
at backend digest `sha256:6320b2cfa493a24a358a28dfbbf1c96de516908e710834f9d230abe071cd921a`.
This release suppresses proven superseded-head wakeups and supplies compact
workflow instructions plus the packaged Forgejo draft-PR helper. Exact source,
policy archive and metadata are checksum-verified in the matching Forgejo
generic package. No migrations or client binary updates were required.

The new-ticket default is `trackself-platform-dac21bcfc12efb81`, imported skill
`9ddee7fe-d94a-4521-98c0-a0aa3cff42b3`, policy version
`sha256:34391ca0a78a0692419f04c21602f325b6e23c11333a54533fb0ea54d2c685b8`.
All 12 Linux and six Mac agent bindings passed guarded apply and readback;
their daemons restarted. Windows' six bindings remain on the previous bundle
because its host is offline and the stopped-process guard cannot be verified.
Existing ticket pins were preserved. CLI binaries remain `.11`; frontend and
desktop remain `.5`.

Focused Python/Go regressions, independent Sol acceptance, vulnerability scan,
exact imported-file readback and production deployment validation passed.
Backend service settings other than its image, frontend and PostgreSQL service
specifications were preserved. No live agent/Forgejo mutation canary was run.
Protected rollback state is at
`utility-server:/opt/multica/config-backups/before-v0.5.1-janez.12`.
Private-infra's `infra/automation-server/multica/upgrade-v0.5.1-janez.12.md`
owns deployment recovery; workspace-control owns pending Windows activation.

## Previous backend and current agent CLIs: v0.5.1-janez.11

Deployed on 2026-09-28 from reviewed source commit
`ca1d21dba1415b6ff89257ea845f85586aac5e4a`. Backend digest:
`sha256:0b417a792aefc28307760689e3559e2ca1d505b11614fb5287b1ec5f84879d88`.
The six-platform CLI bundle is published at
`git.thn.janezstupar.com/janez/multica-cli@sha256:413525037882bc3f3fc6727b877dba209dec75261aeae651ce8dc5df3e1d6326`.
Exact source and build metadata are checksum-verified in the matching Forgejo
generic package. The earlier local `.11` automatic-acceptance build was never
published or deployed; these digests identify the corrected release.

Clear human comments can authorize acceptance and PR readiness through the
agent's `comment-accept` action. Plain approval removes draft/WIP without
merging. Merge, review waiver and release of an existing hold require explicit
instructions. Forgejo account `Janez` (provider ID `1`) is mapped to the existing
human Multica identity on the Trackself connection. Shared agent-account
comments remain feedback, not mapped human approval.

The 24 Linux, Mac and Windows agent definitions and the new-ticket default use
`trackself-platform-51a78aaf74391e93` (skill
`9337e215-e140-4e35-b2a6-7d5f29a8b4df`), policy version
`sha256:e87c363a0c8255067360a0554a31285b35e671a506efbcca2dc7f4590a8e716a`.
Both autonomous acceptance defaults are disabled. Existing ticket pins and
review candidates were retained. All three Linux containers and both named
native daemons returned online on `.11`; the operator CLI also matches.
Web and desktop applications remain unchanged.

Focused service/database, provider, handler and CLI regressions passed, as did
core API tests, frontend typecheck, policy-builder tests and independent review.
Backend readiness, migration 595, exact binary identities, agent-definition
readback and frontend/service preservation passed. Broad product QA and a real
agent comment-interpretation canary were not run. The disposable database,
cluster, log and socket were removed.

TRA-636's existing human approval was reconciled by the operator through a
candidate-scoped ready-only override and human acceptance; its old policy pin
and passing review were preserved. PR #30 is open, ready and unmerged at
`366a31b80223967337b7cfb6e76ac6ce28351d20`. This is live readiness-delivery proof,
not a claim that an agent interpreted a new comment during the rollout.

The protected server backup is
`utility-server:/opt/multica/config-backups/before-v0.5.1-janez.11`.
Owning host deployment and rollback details are in private-infra's
`infra/automation-server/multica/upgrade-v0.5.1-janez.11.md`.

## Previous backend: v0.5.1-janez.9

Deployed on 2026-09-28 from source commit
`7e7cccc2997334b78bb04e584e79425001898ec8` at immutable backend digest
`sha256:e0a4fdbb02f14bf36d8b3dbf7034d0feddb5b5b3bc64f1c41ec680dae48a44d4`.
Migration 593 lets a direct owner comment reach the currently assigned agent
after a completed human handoff even if a later agent transfer exists. A newer
live pending handoff still blocks the claim. The focused database regression,
including an equal-timestamp handoff tie, and independent code review passed.
Backend readiness, migration presence, unchanged `.5` frontend, and service
invariants were verified. Exact source and metadata were published and read
back from the registry. The protected backup is
`utility-server:/opt/multica/config-backups/before-v0.5.1-janez.9`.

The 24 Linux, Mac and Windows agent definitions and the new-ticket default use
`trackself-platform-4b1306da8314f04a`, version
`sha256:487c761ae70d3c5c3073dc0ce9f0e9fc1d8849fc046a425ce2a3267f2ac8f500`.
Thirty-one unfinished tickets without a current review candidate or live
acceptance were explicitly migrated with their status and historical evidence
retained. TRA-623 keeps its older pin while its current review candidate is
live. TRA-634 resumed from the owner's comment, then reached Done after the
owner's candidate-scoped review exception and exact-head human acceptance.
Desktopapp PR #29 merged at head `9afd37119ea97487f3a02bc268f8d02453a5bc24`;
desktopapp was not deployed. Broad native/product QA remains deferred.

## Previous backend: v0.5.1-janez.8

Deployed on 2026-09-28 from `ffca3f6edd1571d003974da1d899aa54672fb0dd`
at digest `sha256:a4a0478cbbee2c70b44b3dc4672af6f57167b2e4aa5777c3e036259406803dfa`.
It added the audited active-ticket policy migration endpoint and migration 592
for direct owner-comment continuation. The live TRA-634 check exposed a later
agent transfer that migration 592 did not account for; `.9` corrects that
selection. The `.8` backup is under
`utility-server:/opt/multica/config-backups/before-v0.5.1-janez.8`.

## Previous backend: v0.5.1-janez.7

The 2026-09-27 continuation correction addresses the observed TRA-625, TRA-623
and TRA-634 failures below. Selected format-2 tickets receive durable terminal
child facts in their retained parent context, including service-written Done.
Explicit current assignments can hand work onward without stale recipient
lineage or obsolete queued transfers obstructing them. Authenticated Forgejo
PR comments, reviews and head changes reach the retained writer, with deduplication
and coalescing, shared-account human feedback, output-loop suppression and actual
claim/retry/handoff continuity while the ticket remains assigned to its human.
Provider changes do not inherit prior commit evaluation or broaden authority.

Migrations 586–591 add internal child inputs, provider input records and their
claim predicate. Native workflows retain their stage-barrier behavior; policy
pins and role identities are unchanged. Existing `.5` clients remain compatible.
The user enabled PR comment and PR review events on the existing organization
webhook, preserving its URL and secret. Focused regressions and independent
review precede rollout; broad product/native QA remains deferred.

Deployed on 2026-09-27 from exact source commit
`3fc27ac8baa86b9856506a89fc44fef537d20c8c`. Backend image digest:

`sha256:a6299b23cc0d321b11cee3bb9a2694fc4049f1422bd2bc1db1f93f61c3b8df1e`

Exact source and build metadata were published and read back from the registry.
Backend readiness passed; migrations 586–591 applied. The frontend remains on
.5 with its service specification unchanged. The protected backup is
`utility-server:/opt/multica/config-backups/before-v0.5.1-janez.7`; .6 is retained
for rollback. Focused service, handler and provider-parser regressions passed,
followed by independent code and rollout-script review. Broad QA is deferred.
The disposable database was verified absent; its profile, worktree and temporary
PostgreSQL container were removed, preserving the volume and product databases.

Targeted recovery confirmed running tasks:

- TRA-625: retained Maca parent `01a0e439-8c9d-7724-b9d7-c5fb78fb3e81`
  received TRA-633's completed-child fact.
- TRA-634: retained writer `01a0e439-4de5-7b0d-9f1a-acb63202a600`
  received comments 435/436 and PR29 head
  `2f45387d20ed45ac5c472ad93f63a300ca3d1d41` in one continuation.
- TRA-623: fresh Senior review `01a0e43a-76e9-73bb-a437-c2b926585484`
  dispatched from the completed human-assigned writer. Current PR6 head
  `cb237de45617cbabcb5660e2d178d8e315556e94` is its review candidate.

These are verified dispatches, not completed product review, human acceptance,
merge, deployment or native qualification. Comment/review webhook events are
enabled. The user also enabled PR synchronization; read-back confirmed that
the existing active organization hook selects it. Future open-PR pushes can
notify the backend. No hook secret or connection credentials changed.

## Previous backend: v0.5.1-janez.6

Deployed on 2026-09-27 from commit
`4f251cbf9e9e58e2d5b243fbde9dbcf034d002bf`. Backend image digest:

`sha256:833b810db41d1f8216e7764bee78b4c3bfec8a909f3f8bed3ced46debe08fe42`

The registry contains the exact source archive and verified release metadata.
Production readiness passed, migration 585 applied, and the frontend service
specification remained unchanged on `.5`. The protected database/configuration
backup is `/opt/multica/config-backups/before-v0.5.1-janez.6` on utility-server;
`.5` remains available for backend rollback without reversing the migration.
TRA-622 reconciled automatically to Done from its existing acceptance and PR28
merge. Focused service/handler regressions, policy-builder checks and independent
code/script review passed. The disposable database, profile and worktree were
removed; the trial PostgreSQL container was removed with its volume preserved.

The 2026-09-27 patch removes process gates that obstructed recorded user
decisions. Required bound PR merges close accepted tickets without an outcome
checkbox or automatically dispatched outcome run; explicit no-PR acceptance
finishes directly. Existing merged work is reconciled on upgrade. Completed
provider facts are recorded for blocked deliveries and changed heads, while
automated outbound merges still check the evaluated head. Cancellation,
authenticated access and independent review defaults remain.

Editorial ticket changes and PR readiness do not erase the current candidate or
review. A replacement candidate retains prior approval evidence but requires
current engineering evaluation and authorized acceptance/carry-forward. Scoped
exceptions no longer require a fictitious rejection. Explicit delivery Retry
reschedules immediately across nonterminal presentation statuses.

Migration 585 removes outcome/status/text fences and the mandatory outcome-agent
constraint. Rollback retains this schema; reversing the migration requires an
explicit reconciliation. Existing ticket policy pins and agent identities are
not migrated. Server-only publication and rollout leave `.5` clients unchanged.
The user requested focused regressions and deployment, deferring broad QA until
the workflow is usable. Closing a code ticket does not claim unperformed QA.

## Previous backend: v0.5.1-janez.5

Deployed on 2026-09-26 from independently reviewed commit
`d3ae217b9a1e616f633195f80631622e803bec10`. Backend image digest:
`sha256:94cf2a25d300e349490173adb88d306fd24d2d2b1e0b0b7a7272626003224316`.
The exact source and build metadata are published in Forgejo's generic
`Janez/multica-backend/v0.5.1-janez.5` package; the six-target CLI transport
image digest is `sha256:78f6907e369a46a82f0d9d075c62d209086c9cf6aaede8299bd8ce246e444b85`.

Production validation passed. The frontend was subsequently upgraded to `.5`
at digest `sha256:f1bbc412d59b66516c7568d49c2b48057704d9e22e94be375d3dc7ad4d0a5025`;
its update preserved the backend service specification and running task. The
operator CLI, all three Linux runtimes and the Mac and Windows CLIs and daemons
run `.5`. All 24 retained agent bindings match the newly imported bundle
`0fb2c074-f586-4a9d-89b0-c72790de9ac8`. The new-ticket default was activated on
2026-09-26 with policy version
`sha256:61b3c803e0c530ae26d145e2a49afc136ad4297029bdad05a8ef00ee4ff15823`.
Before/after read-back verified
all 634 existing ticket policy versions and frozen states unchanged, including
602 frozen tickets. Existing work still requires explicit migration or a
scoped override to change its policy.

Database-backed feedback, resumption, handoff, acceptance and delivery
regressions passed, including full handler/service race suites. The managed
trial database and profile were destroyed after migration rehearsal.
Production backup remains on utility-server at
`/opt/multica/config-backups/before-v0.5.1-janez.5`. Migrations 580–584 are
additive; an image-only downgrade is not a verified rollback.

TRA-633's existing human approval was registered without requesting another
approval. Services PR6 was made ready and merged at the accepted head on
2026-09-26 after replacing the saved Forgejo integration credential. Provider
logs confirmed the old credential's title PATCH was denied by token-scope
checks. Only the encrypted access credential changed; connection identity and
webhook secret were preserved. Delivery read-back passed. TRA-622's PR28 is
ready with its merge hold intact. TRA-625's live cleanup/crash qualification
and TRA-634's human UX acceptance remain outstanding; source delivery does
not establish those runtime outcomes.

Revision-bound deployment limitation (2026-09-26): Docker stack's legacy
interpolation rejects the owning Compose file's nested backend-version
expression. After the installer guards passed, the backend alone was updated
using its exact tag and digest; the owning validator then passed. This does
not establish that a future stack-wide installer invocation will succeed.

## Current workstation desktop: v0.5.1-janez.5

Installed on 2026-09-26 from the same exact reviewed source. AppImage SHA-256:
`223eb8d9a305218ff94f8599c74819e92dfb565378fdf87a0b3721452528e647`.
The packaged app and running bundled daemon report `.5`; its CLI matches the
published binary SHA-256
`ed41dbc81d99e55894a1eb677b406320439b2ae223fb883487c18a105dcc41bb`.
The launcher and profile were preserved; rollback AppImage remains alongside
the installed file. AppImage and metadata are published in Forgejo generic
`Janez/multica-desktop/v0.5.1-janez.5`.

Client production builds/typechecks, 113 focused web tests, isolated and live
HTTPS assets/version checks, actual desktop package inspection and startup
passed. This does not constitute a full interactive GUI acceptance pass.
The owning deployment record is private-infra
`infra/automation-server/multica/upgrade-v0.5.1-janez.5.md`.

## Previous backend: v0.5.1-janez.4

Backend-only upgrade completed on 2026-09-25 from reviewed commit
`be821aba1d5569af0cb361e67b15d8d0744b58c4`. Production uses
`git.thn.janezstupar.com/janez/multica-backend:v0.5.1-janez.4` at
`sha256:8778967785e14a52c6f4c0f728745ebfbb7e2dd7d3c0bf9aba742ae503f49002`.
The patch accepts the exact `.git` clone form of a provider-bound repository
URL while sending the provider web URL to review and delivery. No schema,
web, desktop or agent CLI changed. Database-backed ready/merge regressions,
independent review, vulnerability scan and production validation passed.
TRA-634 retains its candidate and independent review at PR #29 head
`1daeef806a5912ffc23cf9fe3492e823298a0efd`; `provider_binding_missing`
cleared, while human-recipient handoff remains pending. PR #29 was not merged
or deployed by this release. Source and metadata are published in the registry.
The owning infrastructure record is
`infra/automation-server/multica/upgrade-v0.5.1-janez.4.md` in private-infra;
backup: `utility-server:/opt/multica/config-backups/before-v0.5.1-janez.4`.

## Previous backend: v0.5.1-janez.3

Backend-only upgrade completed on 2026-09-25 from reviewed commit
`89d0c9b6629c31c5349facb88aaec302da828252`. Production uses
`git.thn.janezstupar.com/janez/multica-backend:v0.5.1-janez.3` at
`sha256:980a53493e5db9f77769859e9b3cb1b9547e0261506fb9f34da31eeeb5a3d220`.
Migration 579 and the exact running container's health/image identity passed;
the frontend task stayed unchanged. Web, desktop and agent/operator CLIs remain
on `.2` and require no upgrade for this backend correction.

The exact source archive and build metadata are published in Forgejo's generic
`Janez/multica-backend/v0.5.1-janez.3` package. Focused database regressions,
provider/CLI checks, binary builds, independent source review and vulnerability
scan passed. The disposable proof database/container/volume/network were removed.
The owning infrastructure record is
`infra/automation-server/multica/upgrade-v0.5.1-janez.3.md` in private-infra.

The new-ticket default is `trackself-platform-56ee71794967313b`, imported UUID
`890e2f62-bc21-4831-8765-24665f92e31d`, policy version
`sha256:7430ed94e22944526c01ba258884827137cb6997cc67bf9d837b6939a24f8d9b`.
All twelve Linux profiles matched the imported bundle and current instructions;
the three previously running runtimes restarted healthy. Existing ticket pins
and the original cutover boundary remain unchanged.

TRA-609's scoped `external_merge` exception recovered its stale-head-only
revocation and observed its already completed PR26 merge. Read-back confirmed
Done at revision 40, original acceptance restored, and delivery recorded at
`2026-09-25T13:07:25.976648Z`; no new review, acceptance request or merge POST.
The provider actor remains recorded as the `Multica` service account.
Open changed-head PRs retain approval but pause agent merging; authorizing an
updated open head for automatic merge remains a separate capability boundary.

At the user's request, the superseded `.2` backend registry package and
utility-server cache were removed after health and delivery read-back. The
active `.2` web/CLI artifacts and protected backups remain intact. Production
backup: `utility-server:/opt/multica/config-backups/before-v0.5.1-janez.3`.
Do not blindly downgrade migration 579 or discard its exception audit rows.

## Initial v0.5.1-janez.2 release

Published 2026-09-25 as `v0.5.1-janez.2` from reviewed source commit
`0f23313676b0fcc66f197dd9dffef7c4feb10d09` on
`feature/trackself-context-workflow`. The registry bundle contains the exact
source archive, so recovery does not depend on an unpublished source branch.
No GitHub release tag was pushed.

Publication and the production server/Linux upgrade are complete. Main and
branch-2 runtimes are healthy on `.2`; branch-1 has the new image and remains
stopped. Coordinated Linux Mica activation completed on 2026-09-25.
Native macOS/Windows activation remains deferred. The
private-infra record `infra/automation-server/multica/upgrade-v0.5.1-janez.2.md`
owns deployment state; the prior `upgrade-v0.5.1-janez.1.md` owns the
previous revision and rollback baseline.

## Registry artifacts

All repositories use `git.thn.janezstupar.com/janez/` and exact tag
`v0.5.1-janez.2`. Use immutable digests for deployment.

| Repository | Digest | Contents |
| --- | --- | --- |
| `multica-backend` | `sha256:90ba6d031ec0bce386319806294bf2c9297ec759060e1c685c18a1209fb6f025` | Linux amd64 server, CLI, migration and maintenance binaries; migrations through 578. |
| `multica-web` | `sha256:ba19c70c3776e55608f9b9b3eb123e6c5c1a7e7fa5f7824941b41be2f7f2ef86` | Matching Linux amd64 frontend. |
| `multica-cli` | `sha256:1b344007c565855d97f6a5971081ddc0b332d18ff159c32c3d5d0276ab772f30` | Extract-only `/dist` bundle: CLI archives for Linux/macOS/Windows on amd64/arm64, exact source, prepared Mica skill, metadata and checksums. |

The CLI bundle's Linux amd64 OCI metadata describes its transport container,
not the six archive targets. Extract it without starting a container:

```sh
artifact=git.thn.janezstupar.com/janez/multica-cli@sha256:1b344007c565855d97f6a5971081ddc0b332d18ff159c32c3d5d0276ab772f30
docker pull --platform linux/amd64 "$artifact"
container=$(docker create --platform linux/amd64 "$artifact")
mkdir -p ./multica-v0.5.1-janez.2
docker cp "$container":/dist/. ./multica-v0.5.1-janez.2/
docker rm "$container"
(cd ./multica-v0.5.1-janez.2 && sha256sum -c checksums.txt)
```

The prepared policy is `trackself-platform-4ea20d203a893dc2`, content identity
`4ea20d203a893dc2610d93fbd1c47cce11013e2e0a79b837ea29a465759ca6cb`.
It includes the agreed production authority and format-2 completion semantics.
The imported Trackself UUID is `3dd9bba6-81b2-425c-8361-377a9a1acbe8`;
read-back matched the root and all 12 supporting files byte-for-byte. It was
the initial default at the separately approved cutover described below; current
policy-only revisions are recorded in workspace-control `docs/multica-workflow.md`.
Import alone did not change agents, defaults or existing tickets.

Local retained artifacts are under ignored `dist/releases/v0.5.1-janez.2/`.
Registry artifacts are the durable published copies.

## Validation and build identity

- Build date `2026-09-25T07:55:44Z`; Go binaries use Go 1.26.8,
  `CGO_ENABLED=0`, exact version and full source commit. Standalone CLI builds
  use `-trimpath -buildvcs=false` and explicit identity flags.
- Backend and web images were built from a clean `git archive` context and
  carry the full source revision label. The frontend production compile,
  TypeScript check and network-isolated HTTP 200 smoke passed.
- Backend and standalone Linux CLI report the expected identity. All six
  archive targets and contents passed checks; checksums passed again after
  extraction from the published OCI image. Native runtime behavior is deferred.
- Source and built backend/CLI vulnerability scans found no vulnerabilities.
- All three registry manifests were read back; anonymous pulls by digest passed.
- Independent code acceptance, database proof, guarded Forgejo PR10 delivery
  and cleanup, and unrelated broad frontend baseline failures are recorded in
  [runtime-proof.md](runtime-proof.md#2026-09-25-format-2-completion-trial).
  They do not constitute a deployed-environment canary.

The matching local Linux runtime image is
`local/multica-trackself-desktopapp-runtime:multica-v0.5.1-janez.2-codex-0.156.1`,
image ID `sha256:6950ac009b752846498d6b50be5074f2434a15acf356556fe46974771d1361d2`.
It replaces only the Multica CLI over the exact previous runtime image.
Isolated checks confirmed Multica identity, Codex 0.156.1, Node 22.23.2 and
Git 2.55.0. All three recreated agent containers use this image with unchanged mounts.
Main and branch-2 passed health/version/authenticated API canaries; branch-1
remains stopped with the new binary.

## Deployment boundary

The production-copy rehearsal and its second no-op migration run passed;
its temporary database and dump were removed. Production now has 605 migration
stems, with protected database/uploads/configuration backups retained under
`utility-server:/opt/multica/config-backups/before-v0.5.1-janez.2`. An older image
alone is not a database rollback. Keep `DO_NOT_TRACK=1`,
original mounts, credentials and toolchain settings intact. Preserve branch-1's
pre-existing stopped state; restart only the previously running main and branch-2
runtimes after health and configuration checks.

Coordinate the KB, workspace-control, complete skill replacement, imported
bundle identity, existing `pr_ready` status category and new-ticket default at
activation. Old unfinished tickets remain frozen until explicitly migrated.
No native host upgrade or automatic legacy-ticket migration is implied.

The superseded `v0.5.1-janez.1` backend, web and CLI registry versions were
deleted through the Forgejo API after the upgrade. The stopped old web container
and cached backend/web images were removed from utility-server; the old CLI
image was already absent. Read-back confirmed all three old package versions
absent and all `.2` versions preserved. Protected database/configuration backups
remain intact. Logs are retained with the release validation evidence.

## Local Linux desktop client

On 2026-09-25, the workstation AppImage was built from the same exact source
commit and installed at `/home/janez/.local/opt/multica/Multica.AppImage`.
Desktop metadata reports `0.5.1-janez.2`; the bundled CLI is byte-identical to
published CLI SHA-256 `eb43ece3fce1467536764f22af03ae6eba3fe432ee77266a8c1a9e80dbe6fa98`.
AppImage SHA-256 is
`c000ece25058d67828ac5496f80b02856372f56b6b72491a4088148a541f1068`.
The artifact and build/startup logs are retained under the ignored release
`desktop/` and `validation/` directories.

The atomic replacement preserved the launcher and desktop profile. The previous
AppImage is `Multica.AppImage.before-v0.5.1-janez.2` beside the installed file.
After client restart, its existing idle desktop-owned daemon upgraded from
`v0.4.40-janez.2` to `.2`; health reported running with zero active tasks.
Automatic upstream updates remain disabled. This verifies packaging and startup,
not a full interactive UI acceptance pass or desktop-agent policy activation.
Electron-builder emitted dependency collector warnings but completed successfully;
the packaged app version, exact CLI checksum and running client were checked.

## Native macOS CLI

On 2026-09-25, `mac-mini` (`janez`, Darwin arm64) was upgraded from
`v0.4.40-janez.2` to the published `v0.5.1-janez.2` CLI at
`/Users/janez/.local/bin/multica`. The release archive checksum matched before
transfer; the staged and installed binary matched SHA-256
`35d54c2ffccd966fd33a9227851cb7b95ee514c0db48bce4ddb9a484f89413f1`.
Native execution reported source commit
`0f23313676b0fcc66f197dd9dffef7c4feb10d09`, and an authenticated workspace read
through profile `janez-mac-native` returned the expected Trackself UUID.

The profile configuration and existing LaunchAgent were unchanged. The daemon
was stopped before replacement and remained stopped afterward; no work was
dispatched and native agent policy activation remains deferred. No Multica
desktop app was found in the system or user Applications directory. Per user
instruction, the old CLI binary was replaced without retaining a backup.
These checks establish CLI execution and API access, not native daemon/task,
repository-access or retained/fresh-context proof.

At 2026-09-25 13:11 UTC, the user requested starting the existing
`com.trackself.multica.janez-mac-native` LaunchAgent. `launchctl bootstrap
gui/501` loaded it; read-back showed the service and `janez-mac-native`
daemon running with CLI `v0.5.1-janez.2`. The authenticated Trackself
runtime listing reported `fd86fade-411f-48e5-8643-422f6851afdc` online.
This startup did not change native agent policy bindings or exercise a
native task.

## Coordinated Linux activation

Activation completed at `2026-09-25T09:08:49.206821Z`, with ticket policy version
`sha256:48de64f4add35c8299bb7237b5428a5ea3f3ae041f68c9261e3d9729c136f455`.
The workspace-control repository's `docs/multica-workflow.md` owns the exact
configuration and cutover evidence. KB commit
`dfefa3c236cb2a66dcb4919db9e93f920fc36865` and control commit
`d6338efb66b7e3de663d0ed5125d5ff396740e1a` were published and adopted; the
operator KB skill projection and both branch bootstrap projections were updated.

The 12 retained Linux agent configurations passed API read-back. A scoped
branch-2 chat task verified the exact published KB checkout and task-local
imported skill; container reads independently confirmed both hashes. This was
an actual agent execution, distinct from the scripted format-2 delivery trial.
The chat was archived with its transcript retained; no probe issue was created.
Production read-back confirmed all 632 prior tickets frozen, zero migrated, and
`pr_ready` in the `started` category. Main and branch-2 resumed at cutover; branch-1 was left
stopped at that boundary and subsequently restarted under separate authorization. A protected pre-cutover database snapshot is retained at
`utility-server:/opt/multica/config-backups/before-mica-cutover-20260925`.

The consumed preparation patches and completed implementation plan were retired.
Native host dispatch/repository/context checks accompany their later deployment;
they do not repeat server delivery proof. Old tickets remain frozen until
explicit migration; neither this activation nor client upgrades migrate them.

At the user's request, the five obsolete Review Controller agents were archived
through the supported API after activation. Read-back confirmed removal from
active use while preserving all 189 terminal task records. The obsolete temporary 0.4.21 prompt-probe agent was also archived, preserving
its 11 terminal tasks. The active catalog contains exactly the 24 retained
implementation, coordination, acceptance-review and native specialist roles. Workspace-control owns the
exact role identities and cleanup record.

The documentation closeout was published as workspace-control
`2180e03d5fbfd7c6ed4a8ee82ebfcb99ad7e7ac9` and adopted by both branch bootstraps.
After explicit approval, the workstation shell CLI at
`/home/janez/.local/bin/multica` was replaced with the published `.2` CLI
(SHA-256 `eb43ece3fce1467536764f22af03ae6eba3fe432ee77266a8c1a9e80dbe6fa98`).
Version/commit and authenticated workflow-default read-back passed. The old
operator binary was removed without a rollback copy at the user's request.
Desktop, container and operator CLIs now all use `.2`.

## Current delivery limitation

Observed 2026-09-25 on `v0.5.1-janez.2` (source
`0f23313676b0fcc66f197dd9dffef7c4feb10d09`): TRA-626 PR27 readiness retried
with `ambiguous` while its accepted head remained
`dccab86dda3bfe0f79da205993df9c0a325a9042` and its title still had `WIP:`.
A direct supported Forgejo title PATCH removed the prefix and returned
`draft: false`; Multica then recorded `readiness_done_at` at
`2026-09-25T11:31:44.679203Z`. Acceptance and the persistent merge hold were
preserved. This repaired the ticket without a release. The automatic title
update failure has not been isolated; do not claim its recurrence is fixed.
Remove this diagnosis when the provider mutation path is explained or repaired.

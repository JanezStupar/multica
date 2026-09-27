# Mica release and deployment

## Prepared backend patch: v0.5.1-janez.7

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

## Current backend: v0.5.1-janez.6

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

## Outstanding continuation issue: TRA-625 after TRA-633

Observed 2026-09-27 with production backend `v0.5.1-janez.6`, source
`4f251cbf9e9e58e2d5b243fbde9dbcf034d002bf`. TRA-633 is Done (last updated
2026-09-26T18:23:45Z), but parent TRA-625 remains In Progress. Its latest Maca
run, `01a0d8d0-404d-758e-a17d-9ec21fd434fe`, completed on September 25 after
creating the child; no subsequent parent run appears. Both supported parent
handoff and wakeup listings are empty.

The child instructions say its terminal transition is the parent wake, but that
text did not establish an executable continuation. The confirmed failure is a
missing parent continuation after delegated child completion; whether it arose
from missing agent setup, a missing server facility, or both remains to be
resolved. The `.6` completion correction does not fix this case.

For the next fix round, inspect other delegated-task continuations before
choosing a general repair. Preserve the parent's retained context and ensure
completion can prompt reconciliation without duplicate dispatch. Resuming the
parent to inspect results and prepare the next slice does not authorize live
helper kills, host crashes, privileged installation or other separately scoped
native operations. At the user's request, this diagnosis is recorded only:
TRA-625 was not resumed or modified, and no repair or deployment was attempted.
Remove or replace this diagnosis after the continuation is repaired and verified.

## Outstanding handoff issue: TRA-623 human-directed continuation

Observed 2026-09-27 on backend `.6`, source
`4f251cbf9e9e58e2d5b243fbde9dbcf034d002bf`. The user explicitly authorized
continuing the retained implementation through commit, publication and review.
Implementor run `01a0e214-8c38-70e6-beee-e495ab1c3264` published portal draft
PR6 at `2938593344daa20fca404368db97b6c6dc01ea9b`, then reported rejection of
the review handoff because its direct human assignment was not recognized as
the previous handoff recipient, comment continuation or recovery.

Read-back confirms the prior enabled handoff
`01a0ddc8-3c17-7c8b-a3a2-32e07c35d709` still identifies the original implementor
run `01a0ddc8-5ec8-7543-8583-1f5dde6a6243` as its recipient. The lineage guard
in `server/internal/service/issue_handoff.go` rejects other source tasks unless
they match its recognized continuation paths. TRA-623 remains In Progress with
`review_missing`; no review handoff was saved. The failure is inability to
continue authorized work into review, rather than the independent-review
requirement itself. Routing through another coordinator is the agent's proposed
workaround, not a verified necessary user action or a repair.

Earlier interruptions on the same ticket included the now-corrected portal
no-automatic-commit policy and an agent-reported comment-parent restriction that
prevented posting implementation evidence through the normal comment action.
The latter is a separate reported symptom requiring verification; a final run
comment does exist. The earlier execution-lane mismatch was reported resolved.

For the next fix round, audit direct human assignments and retained writer
continuations against handoff lineage and comment guards. Preserve authentic
task/agent access and independent review while recognizing explicit human
direction without compulsory coordinator relays. Record only for now: no ticket
mutation, dispatch, code repair or deployment. Remove or replace this diagnosis
when the continuation and communication paths are repaired and verified.

## Outstanding feedback integration issue: TRA-634 PR29

Observed 2026-09-27 on backend `.6`, source
`4f251cbf9e9e58e2d5b243fbde9dbcf034d002bf`. User feedback on desktopapp PR29
includes comments 435 and 436, created September 27 at 12:01 and 12:13 +02:00.
They request correcting the local-records CTA copy and restoring the intended
purchase-license block. The user confirmed they were logged into Forgejo as
`Multica` when posting them, explaining that account attribution. The live PR head is now
`2f45387d20ed45ac5c472ad93f63a300ca3d1d41`; Multica's current candidate still
records reviewed head `37e00033bd71cc011ca025c18c81916a6b2a89c1`. No TRA-634
agent run appears after September 25; the issue remains assigned to the human
in review. The workflow reports no acceptance blockers for its stored candidate,
which does not establish evaluation of the new live head.

The implemented VCS webhook accepts PR and CI-status events, but acknowledges
unmodelled events without processing them. The Forgejo adapter does not model
PR discussion comments; the PR mirror does not dispatch retained-context work
for feedback or a changed head. Multica issue-comment recovery therefore cannot
react to these provider comments. This is a missing communication/continuation
path, not evidence that another user approval is needed. Whether these particular
comment webhooks were delivered is not verified; delivery alone cannot fix the
missing handler. The confirmed shared-account usage means account identity
alone cannot distinguish human feedback from automatic agent output; feedback
routing must account for that when preventing self-triggered loops.

For the next fix round, cover PR discussions, reviews and user-pushed corrections
as inputs to retained work; reconcile current provider code facts with candidate
evaluation and prevent duplicate or self-triggered agent loops. This observation
does not authorize changes, dispatch or PR mutation. Remove or replace the
diagnosis when the feedback and changed-head continuation paths are verified.

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

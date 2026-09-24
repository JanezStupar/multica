# Mica workflow implementation

Make the [agreed workflow](workflow_spec.md) executable through Multica's native
tickets, tasks and wakeups, then prove it before changing active Trackself work.
This repository owns the mechanics and candidate policy bundle. Canonical KB
owns shared protocol; `workspace-control` owns agent and machine configuration.

## Current position

Reconciled 2026-09-24 against base commit
`c052b3b5cb68a4c23929ad51697657fe67b1473b` and the uncommitted
implementation snapshot recorded in the [runtime proof](runtime-proof.md#2026-09-24-isolated-linux-trial).
The spec includes the agreed policy authority, ticket policy continuity, review,
rejection, resumption and repository-context requirements. No live cutover has
occurred. The current diff contains these mechanisms:

| Foundation | Purpose | Boundary |
| --- | --- | --- |
| Versioned policy archive | Give all roles one editable workflow and complete platform references; avoid nested skill files discarded by import. | Building an archive neither imports it nor selects policy for tickets. |
| Fresh wakeup sessions | Let a same-ticket terminal-run subscription request a new provider context for independent review. | Freshness does not select an exact candidate or preserve another task's uncommitted worktree. |
| Explicit ticket policy enrollment | Snapshot the complete workflow bundle before execution; claims and deferred resolution read that immutable snapshot. | The bundle and the selected execution profile have separate identities; neither snapshots credentials or grants access. |
| Native recoverable handoffs | Register immutable intent and exact multi-PR candidates; wait for the outgoing run, then change owner/phase and enqueue one agent recipient or transfer to a human atomically. | Handoff candidates are declared inputs. Acceptance separately verifies linked PRs and published exact-head review evidence. |
| Issue claim serialization | Serialize enrolled issue execution across agents and keep unrelated queued work behind a pending handoff. | This is issue-level coordination; shared execution surfaces across different tickets still need runtime isolation. |
| Immutable execution profiles | Retain selected instructions, model settings and skill bundles; explicit reselection appends a revision, and retries retain their original binding. | Live permissions, credentials, environment and runtime bindings remain live checks. |
| Candidate authority | Persist review, scoped exceptions, exact-candidate acceptance and rejection; return rejected work to its writer with reconciliation context. | A resumed review/fix session cannot attest a fresh final review. Lost writer continuity is explicitly recorded before fresh reconciliation. |
| Durable delivery | Resolve trusted provider bindings, verify exact PR heads, make accepted PRs ready and optionally merge in explicit order. | Live Linux/Forgejo trials establish single- and ordered multi-PR delivery; native host checks accompany deployment. |
| Explicit cutover and migration | Prepare new-ticket defaults and suspend pre-cutover work until reconciliation and migration. | Local database checks pass; no workspace has been cut over. |
| Shared ticket controls | Show candidate commits, review links, acceptance blockers, retained writer choices and delivery progress in web and desktop. | Older clients receive a conflict rather than bypassing exact-candidate acceptance. Mobile has no new workflow panel. |

The implementation owner integrates these pieces and obtains independent review.
Bounded Sol workers own backend session/policy mechanics; a Luna worker owns the
archive and its policy content. These are development assignments, not permanent
roles imposed on the resulting workflow.

Earlier local validation used disposable PostgreSQL 18 and repository-managed
PostgreSQL 17 test databases. The clean migration sequence passes through 574.
Full handler and service suites pass, covering native handoffs, claims,
profiles, policy defaults/migration, frozen writes, human handoffs, exceptions,
exact-candidate acceptance, rejection and recovery. Tests cover retained
review/fix loops,
genuine fresh final-review origins and retry lineage, cancellation without
acceptance, cross-workspace denial, and deletion of the no-FK workflow ledgers.

The joined database/TLS-mock test passes for published exact-head PR review,
an autonomous acceptance request, successful requester completion, poller
finalization, durable delivery, readiness and SHA-guarded merging. Separate
provider/worker tests cover ordered multi-PR delivery, stale heads, ambiguous
responses, repaired bindings, explicit authorized retry and already-merged PRs.
These are mock-provider checks, not live provider evidence.

The CLI and daemon suites, provider adapters, actual archive importer, six
builder tests and full core suite (2,079 tests) pass. Focused shared UI tests,
frontend typecheck and lint pass. The broader frontend run exposed two existing
failures in unchanged files: the skills-tab opacity assertion and missing French
agent-locale keys. Four batch-toolbar test mocks affected by this change were
updated and pass; desktop packaging tests pass when Git subprocesses are
permitted. Full server and service suites also pass with the race detector on
the clean managed PostgreSQL 17 database. Independent read-only Sol acceptance
found no remaining code defects in the integrated uncommitted surface.
The CLI cross-compiles for macOS arm64 and Windows amd64; this does not
establish native runtime behavior.

Both earlier test database environments were removed after verification, as requested:
the managed PostgreSQL container, volume and network, and the stopped temporary
PostgreSQL 18 cluster with its databases. Their absence was checked. Generated
test environment/port-override files were also removed; test logs remain under
`/tmp/multica-workflow-*.log` and `/tmp/multica-*-final.log` for this session.
The separate 2026-09-24 runtime trial database and its API, daemon, profile,
workspaces, dedicated container, volume and network were also removed and
checked. Its test PR was closed unmerged; its branch and webhook were deleted.

The 2026-09-24 [isolated Linux trial](runtime-proof.md#2026-09-24-isolated-linux-trial)
has proved the `MICA-1` non-code path against the snapshot above: read-only
repository access, fresh independent Luna review, simulated human rejection,
resumption in Sol's original provider session, another fresh Luna review and
human acceptance of the resulting candidate. The test-only policy bundle was
imported and pinned in its disposable workspace; no production default or
Trackself binding was activated. The initial attempt exposed bare-CLI PATH
resolution to the user's global binary and an unsolicited update. With user
approval, the custom-source CLI was restored from a cached release image,
though its build metadata differs from the overwritten binary. The trial now
uses an absolute candidate CLI path. `MICA-2` produced a real draft PR and
fresh, commit-bound provider review. It exposed a CLI review-anchor validation
defect, now fixed with focused passing tests and separate read-only acceptance.
The follow-up [live delivery trial](runtime-proof.md#2026-09-24-live-autonomous-delivery-proof)
proved real webhook association, anchored exact-commit review, pending autonomous
acceptance finalized after reviewer success, and automatic readiness and squash
merge of disposable PR #6. Two lifecycle corrections were independently reviewed;
full service and handler suites passed on a separate managed test database.
The test marker was removed with a new cleanup commit; final repository content
matches its pretrial tree. The temporary Forgejo allowlist was restored and
verified healthy; both follow-up databases and their dedicated runtimes,
profiles, workspaces and Docker resources were removed. No production workflow
was activated.
The subsequent [cross-repository trial](runtime-proof.md#2026-09-24-cross-repository-and-cli-selection-proof)
passed: one ticket, daemon checkouts of both repositories, fresh exact-commit
review, one autonomous acceptance and ordered readiness/merge of both PRs.
Runtime-provided `MULTICA_CLI_PATH` was used by real agents without an
operator-supplied binary path. Test content, branches, hooks and infrastructure
were cleaned up. CLI acceptance help and missing-field errors were improved
with focused passing unit tests and independent review; that correction follows
the live snapshot. Native macOS/Windows checks remain deployment work.

The [candidate consumer patches](cutover/README.md) are prepared against recorded
KB and workspace-control baselines. They remove mandatory staged children while
preserving useful coordination, independent review, authority and native host
routing. Patch applicability, KB lint and 57 workspace agent-definition tests
pass in isolated copies. Active consumer files and bindings remain unchanged.

## Remaining proof and activation

1. Preserve the [runtime proof record](runtime-proof.md) and tested source
   identities when packaging the deployment candidate. CLI selection and live
   cross-repository acceptance/delivery now pass; full daemon and execution-
   environment suites and independent review also pass.
2. Check any remaining recovery or delivery scenario against its named local
   and live evidence before rollout. Validate native macOS and Windows
   dispatch, repository access and fresh/retained sessions when deploying to
   those machines; they do not block Linux activation.
3. Reconcile and review the prepared KB, skills, instructions and automation
   patches against their current owning repositories and the final bundle.
   Rebuild the archive before selecting its imported identity.
4. Only after proof and coordinated consumer readiness, activate new-ticket
   defaults. Freeze old unfinished tickets and leave them frozen until explicit
   individual migration preserves evidence and restores deliberate continuation.

The agreed initial agent lineup is Astra Mica as the single front door, Luna
for bounded implementation, Sol for substantial implementation and default
independent review, with Astra review when warranted. Mica may execute directly
or delegate; review freshness does not require a permanent agent per review
stage. Retire redundant stage agents from routing while retaining their history,
and preserve useful platform/tool specialists. Exact agent IDs, effort settings
and runtime bindings belong in workspace-control and still need a deployment
candidate. This agreement does not mutate current agents.

Initial triviality criteria, exact effort settings and PR merge policy remain
configuration choices. Their absence must not silently grant acceptance or merge
authority. Agent instructions express behavioral policy; reliable execution and
authorization boundaries belong in system mechanics.

## Current technical limits

These observations concern the revision above; replace them as code resolves
them rather than accumulating an incident archive.

- Ordinary reassignment is not an immutable candidate handoff. Enrolled issue
  claims now serialize agents, but workflow progression must use the native
  handoff registration so source completion and recipient intent stay linked.
- The compatibility `force_fresh_session` flag alone is not proof of a fresh
  conversation. Review authority checks native fresh-handoff provenance and
  actual distinct provider sessions. Blind inputs still depend on the selected
  policy and independently assembled requirements/candidate context.
- Execution-profile binding retains selected instructions, model settings and
  skill bundles without retaining credentials or freezing live access checks.
  Same-policy migration deliberately retains the existing profile. Explicit
  reselection appends a new profile while preserving historical task bindings.
  Actual provider changes still need the runtime trial's continuity checks.
- Failed sources and changed ownership invalidate automatic continuation of the
  declared handoff candidate. Recovery must reconcile and replace that intent;
  the system must not silently treat a retry's new commits as the old candidate.
- Cached PR heads cannot authorize a merge. Outbound adapters read the provider
  and merge with its expected-head guard; actual provider/runtime proof remains
  separate from the local database and HTTP mock checks.

## Completion and activation boundary

Complete means the spec's observable scenarios pass, independent review is clean,
runtime/provider proof exists, and the coordinated consumers are ready. Local
code checks establish only their named behavior. Importing and pinning occurred
only in the disposable proof workspace. Changing active Trackself bindings,
freezing existing tickets, changing production defaults and merging
non-disposable PRs remain separate operational actions; none has been performed
by this implementation work.

Retire this plan when implementation and cutover coordination are complete,
keeping lasting operation and limitation guidance in the README and requirements
in the spec. Git retains prior diagnoses.

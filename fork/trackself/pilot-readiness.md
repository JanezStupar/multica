# Pilot readiness — 2026-09-26

Read-back covers all 30 current and archived agents and all 44 visible runs
since 2026-09-25 local midnight. Production backend is v0.5.1-janez.4;
source baseline is 35138e56156dc849805ac5404a0b3b41cb48bd40.
Completed task status is not evidence that ticket progression succeeded.

At 2026-09-26 10:45:19 UTC, two live reads across those agents found no
queued, deferred, dispatched, running or waiting tasks among 1,414 records.
No current generic accepted-agent comment task needs a migration-583 proof
backfill. This is an idle-state read-back, not a runtime workflow proof.

The supplied Forgejo credentials are now installed in `FORGEJO_TOKEN` for
24 active managed agents: 20 coordinating, implementation and review roles
use the reader-writer credential; four QA/release roles use the reader
credential. Archived controllers are unchanged. Both credentials returned
HTTP 200 for TRA-634 feedback comment #416 on 2026-09-26. Other agent
environment values were preserved; new runs receive these credentials.

## Outstanding diagnoses

- TRA-622 / desktopapp PR28: independent source inspection found no blocking
  defect, but the mounted root instructions disagreed with owning
  workspace-control before the runtime block. The formal verdict was not
  recorded. Candidate 05821ff differs from the reported develop-integrated
  live head eab053b; the agent reported the six feature files unchanged.
  Reconcile the projection and current candidate, independently evaluate the
  current head, and carry recorded human approval forward if its scope still
  holds. Do not ask the user to repeat unchanged approval. Native browser-return
  QA remains unperformed, separate from source acceptance.
- TRA-623: assigned container Mika conflicts with the declared branch-2 lane.
  No implementation began. Reconcile routing before dispatch.
- TRA-633 / Services PR6: passing independent review and recorded human approval
  cover bd49b4213fa6b1c74dad514af7a2bae1fb49318b. Human-recipient bookkeeping
  prevents registration; the comment-triggered reviewer cannot create the
  expected handoff. Reconcile existing approval without another acceptance
  request. Parent TRA-625 still needs its live cleanup and crash qualification.
- TRA-634 / desktopapp PR29: corrected head
  37e00033bd71cc011ca025c18c81916a6b2a89c1 passed fresh engineering review.
  Human UX acceptance remains outstanding. Its earlier ordinary comment
  continuation failed the same handoff ancestry guard as TRA-622. The pending
  repair also covers ordinary continuations backed by the exact delivered
  human comment and a valid retained session.

## Pilot boundary

The pending change adds exact-comment candidate feedback classification,
retained writer resumption, durable deferred conversations and explicit
queued/deferred/blocked comment outcomes. It also removes preacceptance status
and recipient ceremony, preserves unanswered conversations across acceptance,
and fences ordinary continuation and fresh-session fallback authority.
The adjacent-path pass also covers edited comment versions, failed-reply
retries, saved-intent recovery after restart, and unanswered input during
completion. These require actual claim/start regressions, not queue-row checks
alone.
Runtime changes refresh the owning instruction prefix at provider launch.
Backend, agent CLI, runtime and matching instructions must be coordinated.
Web/desktop presentation changes are compatible optional fields; clients do
not control the server authority. Existing ticket policy pins must not silently
migrate.

These fixes remain unactivated. Routing and existing approval reconciliation
still need live verification after the coordinated rollout; outstanding product
QA remains a separate prerequisite. A healthy
deployment alone must not be reported as a clean end-to-end workflow. Refresh
these diagnoses against live evidence after activation and remove resolved
items from this current guidance rather than copying them into an archive.

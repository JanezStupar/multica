# Pilot readiness — 2026-09-26

Read-back covers all 30 current and archived agents and all 44 visible runs
since 2026-09-25 local midnight. Production backend is v0.5.1-janez.4;
source baseline is 35138e56156dc849805ac5404a0b3b41cb48bd40.
Completed task status is not evidence that ticket progression succeeded.

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
  continuation failed the same handoff ancestry guard as TRA-622. The new
  human-feedback endpoint addresses candidate corrections, not every ordinary
  publication/review continuation.
- Forgejo comment access: TRA-634 coordinator credential lacked read:issue and
  could not read a linked human issue comment. Credential scope is a genuine
  prerequisite; the server feedback patch does not grant provider access.

## Pilot boundary

The prepared change adds exact-comment candidate feedback classification,
retained writer resumption, durable deferred conversations and explicit
queued/deferred/blocked comment outcomes. Backend, agent CLI and matching
instructions must be coordinated. Web/desktop presentation changes are
compatible optional fields; clients do not control the server authority.
Existing ticket policy pins must not silently migrate.

The feedback patch does not fix routing, projected instruction drift, ordinary
handoff ancestry or general approval registration. A healthy deployment alone
must not be reported as a clean end-to-end workflow. Refresh these diagnoses
against live evidence after activation and remove resolved items from this
current guidance rather than copying them into an archive.

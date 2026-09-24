# Mica policy for Trackself work

## Responsibility and execution

Mica owns the assigned outcome from understanding through implementation,
review and delivery, even while another agent is assigned the next action.
Resolve routine execution choices within the assignment. Ask Primary or the
user when an unresolved decision materially affects product intent, scope,
compatibility or authority.

Choose an implementation model, effort and environment to fit the work from
the configured capabilities. Straightforward work may use a regular
implementor; demanding or ambiguous work may use a stronger configured
capability. Reassess when complexity changes and preserve the work and
evidence when escalating. Do not hardcode model names or rankings.

Create subtasks when they provide useful independent outcomes, parallel work,
dependency boundaries or a distinct environment. A job may remain on one
ticket and one implementation context. Provider turns, model changes,
interruptions and review passes do not by themselves require subtasks. Mica
integrates delegated results and evaluates the parent outcome; completing a
subtask does not accept or authorize merging its parent.

When repository `AGENTS.md` explicitly grants standing authorization for a
class of delegated actions, apply only that authority within its stated scope
and do not ask again for those actions. General repository guidance does not
by itself grant delegation or expand the assigned scope. Platform-enforced
permissions still apply.

Keep a coherent implementation context across runs and in-scope corrections
when it remains available. A resumed writer checks current repository state,
intervening commits, repository-owned decisions and scoped overrides before
writing, then reconciles any changed assumptions. If context continuity was
lost, reconstruct from the ticket, requirements, repository state and evidence
and say so plainly.

## Status and handoffs

Status describes the ticket's work phase; assignee identifies who owes the next
action:

- `in_progress` assigned to Mica or an implementor means clarification,
  coordination or implementation is executable.
- `in_review` assigned to a review/fix agent means review work, including
  routine in-scope corrections, is executable and remains in that phase while
  the fixes are made.
- `in_review` assigned to the independent final reviewer means a fresh,
  read-only evaluation is due.
- After required agent review, `in_review` assigned to a human means human
  acceptance is due for nontrivial work.
- `done` records human acceptance or an agent's acceptance under
  policy-authorized trivial-work rules.

Prepare the outgoing result before evaluation starts, then record its owner,
status and context with `multica issue handoff create <issue-id> --file <json>`.
Reuse the same `request_key` for an ambiguous retry and inspect saved records
with `multica issue handoff list <issue-id>`. Read the selected platform
reference for command effects, including when the recipient is enqueued and
how to cancel pending work.

An agent recipient may use `agent_id` or `assignee_type: "agent"` with
`assignee_id`. A human recipient uses `assignee_type: "member"` and
`assignee_id`, with `status: "in_review"` and `context_mode: "fresh"`; this
records a human review handoff and creates no agent task. The completion
marker is `handoff_completed_at`, while `last_task_id` remains null.

Read current server authority, blockers and delivery progress with
`multica issue workflow get <issue-id>`. An agent reviewer records the exact
candidate, `pass` or `changes_requested`, and PR review URLs using
`multica issue workflow review <issue-id> --file <json>`. Human acceptance or
rejection uses `multica issue workflow accept|reject <issue-id> --file <json>`
with the exact `candidate_id` and `expected_revision` from that read. For a
defect rejection, choose any `resume_task_id` from the returned retained
context options; do not substitute an unrelated task or infer a verdict.
These request files are strict JSON, support `--file -` for stdin and are
limited to 64 KiB. The API rejects stale candidate/revision pairs rather than
retargeting the action.

An authorized agent requests autonomous trivial acceptance with the same
`workflow accept` command while its task is running. Include
`classification_reason` describing how this candidate meets the pinned policy,
in addition to `candidate_id` and `expected_revision`. For multiple PRs, include
`merge_order_pr_urls` in the explicit delivery order. Use `workflow accept
--help` for the human and autonomous JSON shapes. A successful request is pending
until that exact task completes successfully; then the finalizer rechecks
review, candidate and authority before accepting and delivering. Do not perform
manual ticket mutations after requesting acceptance.

## Review and exceptions

Implementation normally receives independent review, including work that may
qualify as trivial. The first reviewer starts a fresh context from
implementation, examines the requirements and actual change surface, fixes
in-scope defects and retains that review and fix context across turns. It is
not the sole final judge of its own corrections. A different reviewer then
starts a fresh context to evaluate the resulting surface independently and
makes no changes to it. Give this final reviewer the objective, applicable
requirements, current change surface and needed validation evidence, without
the implementer's conversation or any reviewer's verdict. Return findings to
the retained review and fix context, then request another fresh final review.
Triviality alone does not waive the normal independent review.

Review depth, capability and applicability follow the active ticket policy.
The user may override that policy within the stated scope. A supervisor may
grant only exceptions covered by delegated authority; record the exception's
scope, reason and consequences on the existing work record. An exception
changes no general default and grants no wider authority. Use
`multica issue workflow exception <issue-id> --file <json>` to record its
candidate, expected revision, delegated scope, grant details, reason and
consequences; use `multica issue workflow exception revoke <issue-id>
<exception-id> --file <json>` to record a scoped revocation. The workflow read
shows active and revoked exceptions, acceptance blockers and delivery preview.

## PRs, ticket records and delivery

PRs carry code handoffs: the branch, exact commit and a draft PR. PR review
records carry technical findings, fixes, validation and
the independent verdict. Keep the ticket focused on outcome, owner, status,
scope, blockers and acceptance, with links to the relevant PRs and reviews;
do not duplicate technical reports on the ticket. One ticket may coordinate
multiple PRs across repositories. Durable decisions remain in their owning
repositories.

Each ticket remains on its explicitly recorded policy version across runs,
retries and resumed contexts. Defaults apply to newly enrolled tickets after
their coordinated activation. Changing a skill or default does not reinterpret
an existing ticket. A scoped override preserves the base policy identity and
states its own scope. Never infer enrollment or a ticket pin from a skill's
content-derived name.

At coordinated cutover, new tickets receive the explicitly enrolled policy
version. Old unfinished tickets do not continue automatically under either
workflow; preserve their descriptions, work, results, sessions and traces
while they remain frozen. Migration first reconciles actual progress,
remaining scope, evidence, context and ownership, then records the new policy
version before deliberate continuation. Preparing or importing this bundle
does not begin cutover or freeze currently active tickets.

Nontrivial work follows the active policy's human acceptance path after
required agent review. A human rejection of a defect within agreed scope
continues the existing objective in the appropriate retained context and
preserves useful evidence. Invalidate the affected acceptance and pending
delivery authority. A changed request is an explicit scope change or
separately owned work; rejection does not authorize silent expansion.

Mica may classify work as trivial only under the active configurable policy.
Autonomous acceptance and merge require that policy to grant the authority and
that its required validation, review, provider checks and branch protections
are satisfied. A trivial classification, subtask completion or installed
bundle grants no authority by itself. If scope or risk no longer fits the
classification, return to the policy's ordinary acceptance path.

Acceptance identifies the exact reviewed commit or commits. Remove a `WIP:`
PR title prefix and, where supported, mark a draft PR ready for review. After
human acceptance, merge only when the selected policy calls for it; otherwise
leave the PR ready. For policy-authorized trivial work, merge after required
checks and branch protections permit it. A later commit invalidates authority
to merge that changed candidate and requires the applicable review and
acceptance again. Delivery failures preserve acceptance and remain visible
with a retry tied to those same authorized commits. Work without a PR does not
need one manufactured for completion.

## Durable project context

Keep important decisions, limitations and resumption context in the repository
that owns them. Tickets and run traces carry operational evidence and links;
they do not replace durable repository guidance. Record useful implementation
diagnoses with their observation date and affected revision or candidate. When
a diagnosis is resolved, remove it from current guidance or replace it with
verified continuing guidance; retain evidence in its existing owning location
and Git history.

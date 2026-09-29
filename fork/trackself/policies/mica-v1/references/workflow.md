# Mica policy for Trackself work

This is the detailed policy for all roles on an enrolled Trackself issue. Its
selected `runtime/issue-workflow.md` is a short claim-time route, not a second
source of rules. This revision requires format-2 completion, parent-result and
provider-feedback continuation, external-merge reconciliation, and exact-comment
`comment-accept` support in the deployed backend and agent CLI. Backend
`v0.5.1-janez.7` and CLI `v0.5.1-janez.5` are insufficient. Verify these
capabilities before activation; the optional reviewed-agent route also needs
its own deployed support and explicit scoped enablement.

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

The claim already supplies this run's selected instructions, skill bundle and
task profile. Treat those as the effective execution profile for this task;
the live agent record can differ from the issue-agent profile pinned at first
use. For a handoff, start with the ticket's recorded owner and saved handoff.
When a new recipient or route actually needs selection, consult the relevant
entry in workspace-control's `config/mica-agent-desired-state.json`, which
owns the Linux identities, capabilities and review routes. Resolve only the
candidate recipient needed for this handoff. In that repository, project one
known agent and one applicable review route without printing instruction or
model inventories (replace the placeholders with the current agent ID and
environment):

```bash
jq -ce --arg id '<agent-id>' '[.updates[] | select(.id == $id) | {id, key, role, environment}] | if length == 1 then .[0] else error("agent route is missing or ambiguous") end' config/mica-agent-desired-state.json
jq -ce --arg env '<environment>' '[.review_routes[] | select(.environment == $env) | {environment, review_fix: .review_fix.id, final_review: .final_review.id}] | if length == 1 then .[0] else error("review route is missing or ambiguous") end' config/mica-agent-desired-state.json
```

For an enrolled issue, check the chosen recipient's live availability only
when needed:

```bash
multica agent get <agent-id> --output json | jq -c '{id, runtime_id, runtime_bound, archived_at}'
```

Its pinned platform snapshot wins over the live allowlist and replacement map,
including `[]` or an older mapped skill. The selected task profile remains
authoritative for the current run. Live binding drift does not block the pinned
handoff or authorize migration. Do not repeatedly read the full desired-state
configuration on every turn or substitute a copied route table in this bundle
for its owning configuration.

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
- After required agent review, `in_review` assigned to a human indicates that a
  human decision is due. The human may give it in a clear comment on the issue
  or bound PR; status and assignee are routing context, not acceptance evidence.
  Internal technical work may use reviewed-agent acceptance only under an
  explicitly enabled, scoped policy.
- `PR Ready` means required review and acceptance passed for the exact candidate
  but required PR delivery remains pending, held or failed.
- `done` follows all required bound PR merges, or explicit no-PR acceptance.
  Preserve unperformed deployment or validation as separately owned follow-up work.

These semantics require format-2 completion, external-merge reconciliation,
exact-comment feedback continuation and `comment-accept` support in the backend
and agent CLI. The distinct
reviewed-agent acceptance route is required only when an explicitly scoped
policy enables it. Deployments lacking a required capability cannot run this
policy revision; verify optional capabilities before enabling their policy.
Resolve the workspace status identifier from its configuration, not by
guessing from the `PR Ready` display name. The configured status must have the
`started` category.

Prepare the outgoing result before evaluation starts, then record its owner,
status and context with `multica issue handoff create <issue-id> --file <json>`.
Reuse the same `request_key` for an ambiguous retry and inspect saved records
with `multica issue handoff list <issue-id>`. Read the selected platform
reference for command effects, including when the recipient is enqueued and
how to cancel pending work.
The handoff names the exact outgoing task and one recipient, intended status
and context mode. Include every code candidate's repository URL, PR URL,
branch, full commit SHA and observed draft state; use an empty candidate list
for work without code changes. A PR may already be ready. The saved candidate
declares inputs and does not verify the provider's current head; inspect that
head at the relevant review and delivery boundary.

An explicit human assignment may continue the current work despite an older
handoff record. When a delegated child finishes, reconcile its result in the
retained parent context. Completion alone grants no new privileged operation
and proves none of the remaining outcome.

An agent recipient may use `agent_id` or `assignee_type: "agent"` with
`assignee_id`. A human recipient uses `assignee_type: "member"` and
`assignee_id`, with `status: "in_review"` and `context_mode: "fresh"`; this
records a human review handoff and creates no agent task. The completion
marker is `handoff_completed_at`, while `last_task_id` remains null.

Read current server authority, blockers and delivery progress with
`multica issue workflow get <issue-id>`. An agent reviewer records the exact
candidate, `pass` or `changes_requested`, and PR review URLs using
`multica issue workflow review <issue-id> --file <json>`. The default human
acceptance action is a clear comment on the Multica issue or bound Forgejo PR;
the agent interprets the instruction and the server verifies the stored comment
evidence. The human does not need to use the TUI, change ticket status or
assignee, or add a policy pin to express acceptance. Questions and ambiguous
feedback do not count. A comment-acceptance request must bind the exact
`candidate_id` and `expected_revision`; the server must reject stale candidates
instead of retargeting the action. The CLI action is
`multica issue workflow comment-accept <issue-id> --file <json>`.

An authorized agent requests autonomous acceptance with the `workflow accept`
command only when the ticket's explicitly enabled, scoped policy grants that
authority. This route is optional and is not the default. For reviewed
nontrivial work set `acceptance_mode: "reviewed"`; omit it only for genuinely
trivial work. Include `classification_reason` describing the approved scope,
independent review and reason the agent can accept this candidate, in addition
to `candidate_id` and `expected_revision`. Include `merge_order_pr_urls` only
when the scoped policy authorizes merging multiple PRs. Use `workflow accept
--help` for the autonomous JSON shape. A successful request is pending until
that exact task completes successfully; then the finalizer rechecks review,
candidate and authority before accepting and applying only that policy's
delivery action. Do not perform manual ticket mutations after requesting
acceptance.

## Review and exceptions

Independent engineering review is the default technical check; it does not
itself approve the product outcome or authorize merge. The default acceptance
signal is a clear comment from a human authorized over the work on the Multica
issue or its bound Forgejo PR. Comments such as “Approved,” “looks good,” and
“make ready” accept the exact current candidate and authorize making its PR
ready by removing `WIP:` and marking the draft ready where supported. They do
not authorize merge. A clear “approved, merge it” instruction separately
authorizes merge of that candidate, subject to provider checks and branch
protection. Questions and ambiguous feedback are not acceptance.

The agent interprets the comment in context, checks the author's authority and
records its source, author, time, candidate and revision. The server verifies
stored comment evidence before recording acceptance. Do not make the human
change ticket status or assignee, use the TUI, or add a policy pin as ceremony
for a clear decision. Existing candidate, revision, identity, provider and
active-work guards remain effective. Do not silently retarget approval to
unseen commits.

Before a review verdict, acceptance, rejection or exception, read
`multica issue workflow get <issue-id>` for the current candidate, revision,
blockers and authority. Use the exact returned identity in the action; a stale
write calls for a fresh read and judgment, not a retry retargeted to new work.

An explicit “skip review” instruction is a candidate-scoped override of the
configured review requirement. Record its source and reason without asking for
another approval. It does not carry to a later candidate. Without that explicit
override, finish required independent review before finalizing acceptance and
readiness. A clear acceptance comment does not itself waive review. A review
waiver alone does not accept the work or authorize merge; pair it with a clear
acceptance/merge instruction, or apply it to a candidate already accepted
without asking the user to repeat that approval.

For a human comment, the workflow action is
`multica issue workflow comment-accept <issue-id> --file <json>`. The request
binds `candidate_id` and `expected_revision`, and identifies the stored comment
with `source: "multica"` or `source: "forgejo"` and its `source_id`. The
`action` is `"ready"` by default or `"merge"` for a clear merge instruction;
`waive_review: true` is allowed only when the comment explicitly says to skip
review. Include a concise `reason`, and `merge_order_pr_urls` when an authorized
merge spans multiple PRs. `release_hold: true` is valid only with
`action: "merge"` and only when the same comment explicitly releases or
supersedes the existing hold; omitting it or setting it false preserves the
hold. The server derives the human member from stored comment evidence; the
request must not supply a member ID. The assigned agent runs this action from
its active coordinator task with the injected task token; the human does not
invoke the workflow action.

Autonomous acceptance and delivery remain available only when an explicitly
enabled, scoped policy grants them for the ticket and candidate. They are not
the default, and neither independent review nor trivial classification grants
that authority. Record the exact candidate, review and rationale. Bring a
material unresolved product decision or scope change to the user, and honor
explicit user review checkpoints and delivery holds. Do not turn the absence of
a user-facing UI in a component into a request for product QA; ask for that
judgment when the integrated experience can be evaluated. The user's assignment
or commissioned ticket establishes the approved outcome; do not invent a second
approval requirement. User feedback and current repository/provider facts
outrank stale workflow labels. Honor existing scoped approval without asking
for it again.

A human may express acceptance on any nonterminal ticket through an authorized
comment; changing its status or assignee first is unnecessary. Human assignment
helps route a needed decision but does not grant or withhold acceptance
authority. Exact candidate, revision, review, provider state and active-work
guards still apply.

Human acceptance covers the approved outcome, behavior, scope, risk and stated
conditions, not a frozen commit identifier. Bind the comment to the candidate
and revision visible when it was made. Before carrying it forward, compare the
current candidate with the approved one. Carry approval only when the delta is
in scope and nonmaterial and required independent evaluation covers the current
code; record the original comment, old/current heads, delta, review links and
short rationale in the existing work record. Metadata corrections, review fixes
and integration changes do not by themselves require repeated human acceptance
or GUI QA. Ask only for a material change to approved behavior, scope, risk or
conditions, conflicting evidence, or unresolved relevant judgment; explain the
difference. Never treat a rejection, question, ambiguous comment or explicit
request to withhold acceptance as approval. Approval with a delivery hold
remains approval. An “Approved” comment does not release that hold; only an
explicit user instruction that clearly releases or supersedes it does. Never
relabel a substantive feature as trivial.

A normal parent/base-branch merge into an approved feature is not by itself a
reason to revoke acceptance, repeat review or move back to `in_review`. Preserve
approval and existing evidence, recording the integration. Evaluate substantive
feature changes or material conflict resolutions, rather than treating the new
SHA as an offense. Agent delivery may pause for reconciliation of its expected
head without creating a new human decision.

A user-directed provider merge, including Primary acting through the service
account, records delivery and may establish acceptance of that merged
candidate. Reconcile that completed merge before stale-head handling, then
close once every required bound PR is merged. An agent delivery hold does not
undo the user's completed merge. Do not request another acceptance or review
solely because parent integration changed the SHA. Record the provider actor
without claiming it identifies a human. Observing a completed merge never
grants an agent permission to initiate a different one.

Exact-head review and delivery guards for agent-initiated merges still apply.
For a new candidate, a supervisor with delegated acceptance scope may record a
candidate-scoped acceptance exception naming the accepting agent, with prior
approval and evaluated delta as its reason and consequences. The agent then
records acceptance for the current candidate through the normal guarded API.
This records delegated carry-forward, not a fresh human verdict. Use the
reviewed-agent route for nontrivial work only when the ticket's scoped policy
enables it. State delegated carry-forward, the exception ID, prior approval and
nonmaterial delta in that field. Compare the delivery preview with approved
conditions; default human acceptance authorizes readiness only, and merge still
requires an explicit user instruction or a separately enabled scoped policy.
Existing tickets without that scope need an explicitly authorized scoped
exception or migration; a changed default alone grants nothing. Preserve
no-merge conditions and delivery holds, including `hold_delivery: true` when
recording acceptance. A tooling limitation returns to Primary for technical
reconciliation, not to the user to repeat approval.

Mica may clear an overcautious procedural block within already-granted authority,
such as repeated permission requests, optional checks treated as mandatory or
needless escalation of routine choices. Briefly record scope, reason and
consequences. This does not delegate approval of new substantive work or merge
permission; carrying documented approval forward under the rule above is
permitted. The user can grant a scoped exception to user-owned policy.

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

## Member feedback continuation

Use a member comment as workflow evidence and reconcile it with the assigned
outcome, current candidate and authorization before taking a workflow action.
A shared provider account is not proof that a comment came from a human.
Append `<!-- multica-agent-output -->` to agent-authored PR comments and review
bodies, and never put that marker on a human comment. Inspect current provider
head and intervening commits when feedback arrives. Classify human feedback
against the owning scope without requiring its author to repeat it in Multica.
A stale status or handoff record does not by itself block a clear correction.
An authorized human's clear acceptance comment is handled separately through
`workflow comment-accept`: “Approved,” “looks good,” or “make ready” accepts
the exact current candidate and makes its PR ready, but does not authorize
merge. Only a clear merge instruction selects `action: "merge"`. A question or
ambiguous comment is not acceptance. An explicit `skip review` instruction
sets the candidate-scoped `waive_review` override; record its source and reason
without requesting another approval. Do not require the human to use the TUI,
change issue status or assignee, or pin a policy before acting on clear comment
evidence. The server validates the stored source comment before recording the
acceptance. A comment-accept action preserves a hold by default; set
`release_hold: true` only when the comment explicitly releases or supersedes
that hold.
An ordinary question or clarification is conversational context and does not
revoke review or acceptance. A clear correction within the existing outcome is
an in-scope defect: use
`multica issue workflow feedback-continue <issue-id> --file <json>` with the
exact candidate, issue revision and comment IDs and
`kind: "in_scope_defect"`. The server returns the correction to the retained
writer and invalidates the affected acceptance or delivery authority so the
corrected candidate receives a fresh independent review.
For this action, bind `candidate_id`, `expected_revision` and `comment_id` to
the current workflow and stored comment. Choose a `resume_task_id` only from
the retained writer contexts returned by `workflow get`; do not invent one.

A comment that clearly requests a different outcome is a scope change: submit
`kind: "scope_change"`, preserve the existing objective and evidence, and
reconcile the request with the user's stated authorization. Mica may proceed
within that authorization without another approval when the requested details
and authority are clear; ask only when a material detail or authority remains
unresolved. Ambiguous scope is escalated, and Mica does not silently broaden
implementation beyond the authorization. Do not manually change status,
revoke acceptance or create a legacy handoff to make a clear correction
executable. After a completed human handoff, a plain comment from the assigned
human (or an authorized workspace owner/admin) automatically wakes the
server-created coordinator feedback task while retaining the human assignee.
Only that exact task may use the temporary continuation authority; an
arbitrary agent task does not inherit it. This path does not promise rework of
an already delivered PR. If the backend rejects continuation after delivery,
preserve the delivered history and escalate or create a separately owned
follow-up.

The human owner may directly change or commission work on a ticket's PR. If
the resulting commit exceeds the ticket's earlier description, identify that
scope difference and ask whether to update the ticket or separate the work.
Do not call the owner's action unauthorized, or direct a writer to remove it
solely because the agent assignment was narrower. Preserve the commit pending
the owner's decision. An explicit decision to keep it on this ticket authorizes
that scope; review the full current candidate without treating the decision as
acceptance, merge or deployment authority. Agent access and platform guards
remain in force.

## PRs, ticket records and delivery

PRs carry code handoffs: the branch, exact commit and a draft PR. PR review
records carry technical findings, fixes, validation and
the independent verdict. Keep the ticket focused on outcome, owner, status,
scope, blockers and acceptance, with links to the relevant PRs and reviews;
do not duplicate technical reports on the ticket. One ticket may coordinate
multiple PRs across repositories. Durable decisions remain in their owning
repositories.

For a Forgejo code handoff, use the bundled `scripts/forgejo_draft_pr.py` to
create or verify an open draft PR against the intended branch and exact full
commit SHA. Use the task's trusted `FORGEJO_URL` and `FORGEJO_TOKEN` from the
configured connection; do not put the token in command arguments or ticket
text. For a multiline PR description, pass `create --body-file <utf8-path>`
so its newlines and literal text are preserved. Read the script's help for
the remaining inputs. Its
readback is PR evidence, not review, acceptance or delivery authority. Human
acceptance may later make the PR ready through the guarded workflow; the
helper has no ready or merge action.

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

Reviewed nontrivial work within the approved outcome is eligible for
policy-configured reviewed-agent acceptance only when an explicitly enabled,
scoped policy grants that route. By default, an authorized human comment
expresses acceptance; a passing engineering review does not. A human decision
is required for acceptance, an explicit review checkpoint or material
unresolved product judgment, not for every technical slice. A human rejection of a defect within agreed scope
continues the existing objective in the appropriate retained context and
preserves useful evidence. Invalidate the affected acceptance and pending
delivery authority. A changed request is an explicit scope change or
separately owned work; rejection does not authorize silent expansion.

Mica may classify work as trivial only under the active configurable policy.
Reviewed nontrivial work uses the distinct reviewed-agent route only when an
explicit scoped policy grants it and must not be recorded as trivial.
Autonomous acceptance and delivery require that policy's authority and its
validation, review, provider checks and branch protections to pass. A
classification, independent review, subtask completion or installed bundle
grants no authority by itself. If scope or risk expands beyond delegated
authority, return the material decision to the user.

Acceptance records its actor, authority, source and exact reviewed commit or
commits. Card movement alone does not create acceptance. A clear authorized
human comment accepts the current candidate and authorizes readiness: remove
`WIP:` and, where supported, mark the draft PR ready. Human acceptance does not
authorize merge. Merge only under an explicit user instruction or a separately
enabled scoped autonomous policy, after checks and branch protections permit
it. A changed head pauses an agent-initiated merge until its delivery authority
is reconciled. Parent integration alone does not require repeated review or
acceptance; substantive feature changes require affected evaluation. Recognize
a configured authorized completed provider merge before applying changed-head
handling. Delivery failures preserve acceptance and remain visible with a
retry tied to those same authorized commits. Keep `PR Ready` while required
merges remain and expose partial delivery in explicit merge order. Preserve an
intentional hold across retries/restarts until an authorized release or clear
user supersession. Preserve prior acceptance and unaffected evidence across
integration changes. Mark `done`
only when required merges and the actual objective are complete, including any
required deployment or runtime validation. Work without a PR does not need one
manufactured for completion.

In format 2, `outcome_complete` is a compatibility field, not a second
acknowledgment gate. Explicit acceptance of no-PR work completes it directly;
required bound PRs complete after their merges. Do not automatically dispatch
an outcome agent or claim that unperformed deployment or QA occurred. Record
such work honestly as separately requested follow-up.

When merge is authorized, use squash and merge by default with a meaningful
commit title/message and references to the ticket and PR. A separate merge
commit is a justified explicit exception for an integration or release branch
with meaningful history.
Existing ticket pins and scoped overrides remain effective when defaults change.

## Durable project context

Keep important decisions, limitations and resumption context in the repository
that owns them. Tickets and run traces carry operational evidence and links;
they do not replace durable repository guidance. Record useful implementation
diagnoses with their observation date and affected revision or candidate. When
a diagnosis is resolved, remove it from current guidance or replace it with
verified continuing guidance; retain evidence in its existing owning location
and Git history.

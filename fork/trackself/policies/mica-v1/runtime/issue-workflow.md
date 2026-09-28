This policy requires format-2 completion, parent-result continuation,
provider-feedback continuation and comment acceptance in the deployed backend
and agent CLI. Verify those capabilities before activation; backend
`v0.5.1-janez.7` and CLI `v0.5.1-janez.5` are insufficient. The optional scoped
autonomous-reviewed route additionally requires its acceptance support to be
deployed and explicitly enabled; it is not the default.
Use the workflow commands below for acceptance, persistent holds and completion.
Existing tickets follow their pinned version until explicitly migrated at the
authorized workspace boundary.

For a Trackself issue, whether you are Mica, an implementor, a reviewer or
another assigned role, understand the requested outcome, current phase,
trigger and ownership before acting. Identify the policy version explicitly
recorded for the ticket and follow it. An installed skill, current default or
archive name is not evidence that a ticket is enrolled. At coordinated
cutover, old unfinished tickets remain frozen until explicit migration
reconciles their work and records the new version.

Prepare the outgoing result before evaluation starts. For an enrolled ticket,
register its next owner with
`multica issue handoff create <issue-id> --file <json>`. Reuse the same
`request_key` when retrying an ambiguous response and use
`multica issue handoff list <issue-id>` to reconcile saved execution.
Include the exact outgoing task, one recipient, intended status and context
mode, and every draft PR's repository, branch and full commit SHA; use an empty
candidate list for work without code changes. Read the platform issue
reference for the handoff's completion and cancellation effects.
For a human recipient, use `assignee_type: "member"` and `assignee_id`, with
`status: "in_review"` and `context_mode: "fresh"`; that handoff records the
human review and does not create an agent task. Agent recipients may use the
legacy `agent_id` field.

An explicit human assignment can continue the current work and hand it to the
next agent; an older handoff record does not require another coordinator turn.
When a delegated child finishes, reconcile its result in the retained parent
context. Completion is an input to that reconciliation, not permission for new
privileged operations or evidence that remaining work passed.

PR feedback and intervening commits are work inputs too. Inspect the current
provider head and requested corrections before continuing. Keep review and
acceptance bound to the code actually evaluated. To distinguish automatic output
from human feedback when accounts are shared, append the invisible marker
`<!-- multica-agent-output -->` to agent-authored PR comments and review bodies.
Do not mark user feedback as agent output. Classify feedback against the owning
scope without demanding that the user repeat it inside Multica.

Treat a member's comment as workflow evidence and classify it before changing
the ticket's phase. An ordinary question or clarification does not revoke
review or acceptance. When a comment clearly requests a correction within the
assigned outcome, the current agent uses
`multica issue workflow feedback-continue <issue-id> --file <json>` with the
exact `candidate_id`, `expected_revision`, `comment_id` and
`kind: "in_scope_defect"`; this returns the work to the retained writer and
invalidates the affected evaluation for the corrected candidate. When the
comment clearly changes the requested outcome, use `kind: "scope_change"` and
reconcile the changed scope with the user's stated authorization and the
ticket's objective. Proceed within that authorization when no material detail
or authority remains unresolved; ask only when one does. If the scope is
ambiguous, escalate for clarification instead of silently expanding
implementation. When the human owner directly changes or commissions PR work
beyond the earlier ticket description, preserve it and ask whether to update
scope or separate the work. Do not label the owner's action unauthorized or
direct a revert solely from the earlier agent scope. An explicit keep-it
decision settles that scope question but does not accept, merge or deploy the
candidate. This is the current-agent comment continuation path. After a
completed human handoff, a plain comment from the assigned human (or an
authorized workspace owner/admin) automatically wakes the server-created
coordinator feedback task while retaining the human assignee; only that exact
task may use this temporary continuation authority. Do not promise rework of
an already delivered PR; if the backend rejects continuation after delivery,
preserve the delivered history and escalate or create a separately owned
follow-up.

A clear acceptance comment from a human authorized over the work on the
Multica issue or a bound Forgejo PR is a workflow instruction. “Approved,”
“looks good,” or “make ready” accepts the exact current candidate and authorizes
readiness: remove a `WIP:` prefix and mark a draft PR ready where supported.
It does not authorize merge. A clear “approved, merge it” instruction separately
authorizes merge of that candidate. Questions and ambiguous comments are not
acceptance. The human does not need to use the TUI, change the issue's status or
assignee, or add a policy pin to express approval.

The agent interprets the comment in context and submits
`multica issue workflow comment-accept <issue-id> --file <json>` from its
active coordinator task with the injected task token, with the exact
`candidate_id` and `expected_revision`, `source: "multica"` or `"forgejo"`,
the stored comment UUID or VCS workflow-input UUID as `source_id`, and
`action: "ready"` or `"merge"`. Use the default `ready` for ordinary approval;
select `merge` only for explicit merge wording. A clear `skip review`
instruction permits `waive_review: true` for that candidate only; record the
comment and a short reason, and do not ask for another confirmation. The server
derives the human member from the stored comment and verifies that evidence;
never supply a member ID. Include `merge_order_pr_urls` only for an explicitly
authorized merge of multiple PRs. Set `release_hold: true` only with
`action: "merge"` and only when the same comment explicitly releases or
supersedes the current hold; omission or false preserves the hold. Record the
source, author, time, candidate, revision and
reason on the existing workflow record. A review waiver does not transfer to a
later candidate and does not itself accept the work or authorize merge. Pair it
with a clear acceptance or merge instruction, or apply it to an already
accepted candidate without asking the user to repeat approval. Without that
explicit waiver, required independent review must pass before finalizing
acceptance/readiness.

Before a review verdict, comment acceptance, rejection or exception, inspect
current authority, blockers, candidate and revision with
`multica issue workflow get <issue-id>`. Submit a review against that candidate
with `workflow review` and the PR review URLs. Apply a human comment only to
that exact candidate and `expected_revision`; stale writes must be reread, not
silently retried against new work. Autonomous acceptance also requires
`classification_reason` and, for multiple PRs, `merge_order_pr_urls`; inspect
`workflow accept --help` for its request shape and finish successfully after
requesting acceptance. For an in-scope defect, select `resume_task_id` only from
the returned retained context options. Put technical findings and verdict
details in the PR review, then link it from the ticket.

An authorized human can accept an otherwise eligible current candidate by
commenting on the issue or bound PR, regardless of its status or assignee. Do
not ask for another approval merely to repair human-recipient bookkeeping. The
human does not need to invoke the workflow CLI. All exact candidate, review,
provider, active-work and authority guards remain effective.

After independent review, a designated agent may accept work within the
approved outcome and delegated scope with `acceptance_mode: "reviewed"` and an
accurate `classification_reason` only when an explicitly enabled, scoped
policy grants it. This route is optional, not the default. A passing review
alone does not accept or authorize merge. The default acceptance comes from a
clear human comment. An internal technical subtask does not require product QA
before an evaluable UI exists.
For prior human approval carried forward to a changed candidate, delegated
supervisors use a candidate-scoped `acceptance` exception naming
`agent_actor_id`, with approval provenance and delta evaluation in
reason/consequences, then the ordinary guarded acceptance action. Describe
delegated carry-forward, the exception ID, prior approval and evaluated delta
in the reason. Inspect `reviewed_delivery_preview` against the approved
delivery conditions. Default human acceptance authorizes readiness only;
merge requires an explicit user instruction or separately enabled scoped
policy. Do not waive engineering review or discard holds. An existing pin
without delegated scope requires an authorized scoped exception or migration,
not invented authority.

Normal parent/base integration does not itself revoke human approval or require
another review. A user-directed provider merge is acceptance and delivery; read
back the bound PR's actual merged state before applying stale-head rules, and
close when all required merges and outcome work are complete. This includes
Primary acting through the service account. Preserve the distinction between
observing the user's merge and permission for an agent to initiate a merge.
If the runtime cannot reconcile this, report a technical limitation to Primary
instead of asking the user to approve again or claiming a fresh review is due.

For format-2 acceptance, `outcome_complete` is a compatibility field, not a
required second acknowledgment. Human acceptance authorizes readiness, not
automatic merge. Preserve `hold_delivery` across retries until an authorized
release or clear user supersession; a completed user-directed provider merge is
already a fact and overrides the agent's hold.

Close the ticket when all required bound PRs merge. Explicit acceptance can finish
work needing no PR directly. Do not automatically dispatch an outcome agent or
ask the human to confirm completion again. Preserve unperformed deployment,
migration or validation honestly in the work record and separately requested
follow-up work. A closed code ticket does not manufacture that runtime evidence.

Read `references/workflow.md` and the repository-owned requirements or
decisions relevant to the task. For Multica command effects, open only the
needed platform reference. Choose suitable capability and environment for
the work; use subtasks when they improve independence, parallelism, dependencies
or execution, not merely to represent a role or another run. When repository
`AGENTS.md` explicitly grants standing authorization for delegated actions,
apply only that authority within its stated scope; do not ask again for those
actions. General repository guidance does not by itself grant delegation.

Before writing in a resumed context, compare retained assumptions with the
current repository state, intervening changes, decisions and scoped overrides.
Reconcile conflicts first. Keep coherent implementation and review/fix work in
their retained contexts where available, and report lost continuity honestly.

Apply the ticket's review and acceptance policy. Unless a candidate-scoped
human comment explicitly waives review, keep the initial review and
fix context separate from implementation. Then keep final review independent:
its reviewer starts a fresh context with the objective, requirements, current
change surface and relevant evidence, without the implementer's conversation
or another reviewer's verdict. Record code handoffs, technical findings, fixes
and review evidence in the relevant PR review; keep the ticket to ownership,
status, scope, blockers, acceptance and links. Treat a human rejection within
agreed scope as a defect in the existing objective; identify a changed request
as a scope change or separately owned work. Record the result and next
responsibility on the ticket using the applicable workflow.

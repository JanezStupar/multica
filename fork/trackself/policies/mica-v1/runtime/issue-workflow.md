This policy requires format-2 completion, external-merge reconciliation and
exact-comment feedback continuation from backend and agent CLI
`v0.5.1-janez.5`; do not activate this revision on earlier deployments.
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
implementation. This is the current-agent comment continuation path. After a
completed human handoff, a plain comment from the assigned human (or an
authorized workspace owner/admin) automatically wakes the server-created
coordinator feedback task while retaining the human assignee; only that exact
task may use this temporary continuation authority. Do not promise rework of
an already delivered PR; if the backend rejects continuation after delivery,
preserve the delivered history and escalate or create a separately owned
follow-up.

Before a review verdict, acceptance, rejection or exception, inspect current
authority, blockers, candidate and revision with `multica issue workflow get
<issue-id>`. Submit a review against that candidate with `workflow review` and
the PR review URLs. Accept or reject only that exact candidate and
`expected_revision`; stale writes must be reread, not silently retried against
new work. Autonomous acceptance also requires `classification_reason` and, for
multiple PRs, `merge_order_pr_urls`; inspect `workflow accept --help` for the
request shape and finish successfully after requesting acceptance. For an in-scope defect, select `resume_task_id` only from the
returned retained context options. Put technical findings and verdict details
in the PR review, then link it from the ticket.

An authorized human can accept an otherwise eligible current candidate on a
nonterminal ticket without changing its status or assignee first. Do not ask
for another approval merely to repair human-recipient bookkeeping. All exact
candidate, review, provider, active-work and authority guards remain effective.

Before returning work for human acceptance, reconcile prior approval using the
carry-forward rule in `references/workflow.md`. A different SHA or corrected
candidate metadata alone is not a new human decision. Delegated supervisors
use a candidate-scoped `acceptance` exception naming `agent_actor_id`, with
approval provenance and delta evaluation in reason/consequences, then the
ordinary guarded acceptance action. The current agent route uses the legacy
`trivial` mode and mandatory `classification_reason` field: explicitly describe
delegated carry-forward, the exception ID, prior approval and evaluated delta
there; do not falsely classify substantive work as trivial. Inspect its
autonomous delivery plan against the approved delivery conditions and hold
delivery if they differ. Do not waive engineering review or discard
holds. An existing pin without delegated scope requires an authorized scoped
exception or migration, not invented authority.

Normal parent/base integration does not itself revoke human approval or require
another review. A user-directed provider merge is acceptance and delivery; read
back the bound PR's actual merged state before applying stale-head rules, and
close when all required merges and outcome work are complete. This includes
Primary acting through the service account. Preserve the distinction between
observing the user's merge and permission for an agent to initiate a merge.
If the runtime cannot reconcile this, report a technical limitation to Primary
instead of asking the user to approve again or claiming a fresh review is due.

For format-2 acceptance, `outcome_complete` is a compatibility field, not a
required second acknowledgment. Human acceptance permits automatic merge unless
explicitly held. Preserve `hold_delivery` across retries until an authorized
release; a completed user-directed provider merge is already a fact and overrides
the agent's hold.

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

Apply the ticket's review and acceptance policy. Keep the initial review and
fix context separate from implementation. Then keep final review independent:
its reviewer starts a fresh context with the objective, requirements, current
change surface and relevant evidence, without the implementer's conversation
or another reviewer's verdict. Record code handoffs, technical findings, fixes
and review evidence in the relevant PR review; keep the ticket to ownership,
status, scope, blockers, acceptance and links. Treat a human rejection within
agreed scope as a defect in the existing objective; identify a changed request
as a scope change or separately owned work. Record the result and next
responsibility on the ticket using the applicable workflow.

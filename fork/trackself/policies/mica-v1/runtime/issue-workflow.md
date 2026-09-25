This source is a draft for the next policy version. Do not activate it until
`PR Ready`/`Done`, persistent holds and the concrete authority bindings are
implemented and proven. The released workflow currently marks acceptance
`done`; the commands below document that release's API, not proof of the revised
completion semantics. Do not emulate the new semantics through manual status
writes or unsupported flags. Existing tickets follow their pinned version.

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

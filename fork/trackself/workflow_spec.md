# Spec: Mica-owned ticket execution and review

## Purpose and agreement

The user or their interactive Primary agent assigns a job to Mica. Mica takes
responsibility for understanding it, choosing suitable execution capability,
organizing useful work, and bringing it through implementation and review to
acceptance and delivery. The user should be involved when their judgment or
authority is needed, rather than to relay routine handoffs.

Keep Multica's ticket interface, Codex integration, scheduling, execution on
Linux, macOS and Windows, and inspectable run history. Maintain a focused fork
where necessary to support the selected workflow. Agent behavior must be
configurable and overridable rather than inseparable from compiled prompts.

This spec records the workflow agreed on 2026-09-24, with production policy
settings and completion semantics refined on 2026-09-25 and human-comment
acceptance/readiness refined on 2026-09-28. Its format-2 mechanics
are implemented in `v0.5.1-janez.2`, with external-merge reconciliation in
`v0.5.1-janez.3`; Linux provider, cross-repository and
completion trials have distinct recorded source/evidence boundaries. Trackself's
Linux cutover completed on 2026-09-25 with a branch task canary and configuration
read-back. See [release.md](release.md) and [runtime-proof.md](runtime-proof.md)
for exact evidence and limits. Native macOS and Windows policy/runtime checks
remain deferred until deployment to those hosts.
This spec does not itself activate automation, merge PRs, freeze live tickets,
or migrate existing work.

This file owns the agreed Multica fork behavior. Changes to shared Trackself
protocol belong in canonical KB; provider configuration belongs in
`workspace-control`. Their consumers must be reconciled before activation.
This spec does not silently replace their currently active instructions.

## Context and rationale

The existing Trackself workflow uses child issues, terminal stage transitions,
separate role identities and skill overrides to obtain automatic progression
and context boundaries from Multica. Those workarounds became part of the KB
protocol. Reducing them requires addressing the system constraints that caused
them, rather than merely shortening task descriptions.

The desired workflow retains tickets while letting capable agents exercise
judgment. A ticket represents useful work. A new provider run, model choice,
review pass or interruption is not, by itself, a reason for a new ticket.
Subtasks remain available whenever they improve execution or coordination.

## Concepts and responsibility

| Concept | Meaning |
| --- | --- |
| User | Supplies intent and decisions and grants authority. |
| Primary | The interactive agent carrying the user's context; may assign jobs to Mica and resolve questions within existing authority. |
| Mica | The default entry point and coordinator responsible for progressing the assigned outcome. Existing configuration may spell this identity `Mika`; no identity rename is required by this spec. |
| Ticket | Durable objective, current work phase, next responsible assignee, scope, blockers and acceptance; links to PR and review evidence. |
| Execution profile | A selectable model, effort, instructions, permissions and machine/runtime configuration. Current Multica may represent profiles as separate agent records. |
| Execution context | A provider conversation/session with retained context. Its lifecycle is chosen for continuity or independence, not dictated by ticket creation. |
| Run | One execution attempt or turn, with its own trace and outcome. Several runs can belong to the same context and ticket. |

Mica retains coordination responsibility when an implementor or reviewer becomes
the ticket's current assignee. The current assignee identifies who owes the next
action. How the system records these two responsibilities is a technical design
question; the user must not have to track that distinction manually.

Roles can be folded together. This spec does not require a permanent hierarchy
of separate planner, orchestrator, implementor, review controller and acceptance
agent identities for every job. Separate contexts are justified by useful
independence, specialization, parallelism or execution environments.

## Requirements

### Work records and code handoffs

PRs carry code handoffs: identify the draft PR, repository, branch and exact
commit. PR reviews hold technical feedback, findings, fixes, validation and the
independent verdict. Tickets coordinate owner, status, scope, blockers and
acceptance, linking to those PR reviews instead of duplicating their reports.

PR discussion feedback and intervening commits reach the retained owning work
context without requiring the user to copy them into a ticket. Provider inputs
are recorded and deduplicated; automatic agent output must not create a feedback
loop. A changed head is a new fact to reconcile, not an inherited review verdict.

One ticket may coordinate several PRs, including PRs in different repositories.
Its candidate identifies each relevant PR and commit. Human acceptance covers the approved outcome, scope and conditions. Delivery
authority identifies exact commits; changed commits require evaluation of the
affected delta, not automatic repetition of human acceptance. Coordinated merge ordering and partial delivery follow the selected
project policy. Important lasting decisions remain in the owning repositories.
Work without code or a PR need not manufacture one to complete its objective.

### Intake, clarification and capability selection

1. The user or Primary can assign a job to Mica without first selecting its
   implementation model, effort level or decomposition.
2. Mica reads the relevant requirements and repository context, investigates
   missing facts and fills in execution details within the assignment.
3. Mica asks for feedback when an unresolved answer materially affects the
   intended product, scope, compatibility or authority. Routine engineering
   choices within the assignment do not require human confirmation. Primary
   may resolve questions from existing context without involving the user.
4. Mica can execute directly, delegate the entire implementation, or decompose
   it. It selects capability according to the work: a cheaper regular
   implementor for straightforward work, Sol 6 for substantial engineering
   judgment, and Astra or another frontier model for the hardest or most
   ambiguous work. These are configurable examples, not hardcoded model IDs
   or permanent model rankings.
5. Mica can reconsider its choice when complexity becomes apparent. Capability
   escalation must preserve useful work and evidence; it must not falsely
   claim session continuity if the provider requires a new context.
6. Separate underlying agent records are acceptable where Multica requires
   them for model, effort or runtime settings. The user need not manage those
   identities to get a job executed.

### Discretionary decomposition

1. Mica may create subtasks for independently scoped outcomes, useful parallel
   work, dependencies or work requiring a distinct execution environment.
2. There is no mandatory single-ticket limit and no mandatory child-per-role
   structure. Mica chooses decomposition for the actual job.
3. Model changes, provider turns, interruptions and review passes do not require
   subtasks merely to trigger continuation or obtain a fresh context.
4. When subtasks exist, Mica owns their dependencies, result reconciliation and
   evaluation of the integrated parent outcome. Subtask completion alone does
   not establish parent acceptance. Agents may record a completed subtask
   deliverable under the configured delegation policy without requesting human
   product acceptance for every internal step. That completion grants no
   additional authority to merge the parent feature.
   The platform delivers terminal child results to the retained parent owner
   even when completion comes through acceptance or another non-HTTP writer.
   No manually registered wakeup is required for this continuation.
5. Task descriptions contain the outcome, relevant boundaries and references
   needed to execute. They do not reproduce generic workflow instructions as
   a prerequisite for every handoff.

### Implementation continuity

An authorized current assignment can hand work onward despite an older handoff
record. Explicit human direction supersedes stale transfer bookkeeping; retire
obsolete unstarted transfers without erasing their evidence. Recognized provider
feedback and child-result continuations can likewise progress within their
existing authority instead of requiring a coordinator relay.

1. Normally one implementation context owns a coherent feature through its
   implementation, validation and any subsequent implementation work across
   multiple runs. Context wipes are deliberate choices, not routine handoffs.
2. That owner may delegate bounded work to cheaper or specialized agents and
   inspect their results while retaining responsibility for the feature.
3. A genuine interruption can resume the existing context. If continuity is
   lost, the replacement reconstructs from the ticket, requirements, actual
   repository state and evidence, and reports the loss honestly.
   Before writing, a resumed context reconciles intervening repository changes,
   decisions and scoped overrides. Retained conversation is not proof that its
   previous assumptions or candidate revision remain current.
4. Tasks may span the whole approved multi-repository workspace. Access and
   mutation remain limited to the assigned scope; a broad workspace binding
   does not authorize unrelated changes.

A claimed run keeps its exact runtime lease when the agent's default runtime
changes. That default change does not cancel an enrolled run. Unclaimed
promised replies recover on the current runtime with the same pinned policy
and profile, without borrowing a provider session from another runtime.
Explicit cancellation and invalidated candidate authority still apply.

### Status, assignment and handoff

Status describes the work phase, not whether an agent process is currently
running. Assignment identifies the next responsible actor. The acceptance rows
below describe the owning product ticket; internal subtask completion follows
the delegated deliverable boundary above.

| Status and assignee | Required interpretation |
| --- | --- |
| `in_progress`, Mica or implementation agent | Clarification, coordination or implementation is executable within the assignment. |
| `in_review`, review/fix agent | The agent has executable review work, including routine in-scope corrections. |
| `in_review`, independent final reviewer | A fresh context evaluates the current resulting surface. |
| `in_review`, human | A human decision is due. The human may express acceptance or feedback in a clear comment on the issue or bound PR; assignment routes attention but is not required evidence. |
| `PR Ready`, accepted work awaiting delivery | Required review and acceptance passed; delivery may be pending, held or failed. |
| `done`, completed work | Automated completion requires all required bound PRs merged, or explicit acceptance of work needing no PR. A human may explicitly close work through the status control with an audited decision despite incomplete workflow bookkeeping. Closing does not claim unperformed deployment or validation. |

Routine corrections remain within the review/fix cycle. Material work that must
return to implementation can move back to `in_progress` and the appropriate
implementation owner. A reviewer must not be forced to change to `in_progress`
solely because it is executing or fixing a review finding.

A handoff records the outgoing result and launches one intended recipient. It
must not start evaluation before the preceding mutable surface is ready, lose
work, or permit unintended overlapping edits. Retries and ambiguous responses
must reconcile existing state rather than create duplicate execution.

Machine waiting, execution failure and missing decisions remain visible.
Ordinary unfinished work is not a human blocker merely because a run ended.
Routine progression must not depend on the user relaying comments or changing
statuses between agents.

An authorized human may accept an otherwise eligible current candidate through
a clear comment on the issue or bound PR, regardless of status spelling or
assignee. The human need not use the TUI or change ticket status, assignee or
policy pin to express that decision. Preserve membership, policy authority,
comment provenance, exact candidate and revision, independent review, provider
checks, active-work exclusion and delivery holds. Terminal work is not reopened
or retroactively accepted by this shortcut.

### Review, fixes and independent final judgment

The agreed review pattern is:

1. A reviewer starts fresh from the implementation context and examines the
   requirements and actual change surface.
2. It fixes the in-scope errors it identifies, validates the result and retains
   this review/fix context across turns.
3. Another reviewer starts in a fresh context to evaluate the resulting surface
   independently. This final reviewer does not modify the reviewed surface.
4. Findings return to the existing review/fix context for correction, followed
   by another fresh independent final review.
5. Once clean, Mica or the implementor receives the result. Technical findings,
   fixes, validation and the independent verdict live in the PR reviews, bound
   to the reviewed commits. The owning ticket links to those reviews rather
   than copying their reports.

The review/fix agent may not be the sole final judge of its own corrections.
Routine findings do not need to be bounced back to the original implementor.
Changes to intended product behavior, scope or acceptance criteria go to the
owning decision-maker rather than being silently redefined by a reviewer.

"Blind" means independent provider context and judgment. Reviewers receive the
objective, owning requirements, exact repository surface and validation context;
they do not inherit the implementor's conversation or an expected verdict.
An independent reviewer must examine the actual full requested surface rather
than merely endorse the preceding report. Fresh contexts and actual provider
session identities must be distinguishable from new run IDs.

Implementation normally receives independent review, including trivial work.
Review depth, capability and applicability are configurable policy, subject to
an explicit candidate-scoped user override and delegated supervisor exception
authority defined below. Triviality alone is not a review waiver. Any exception
identifies the affected review requirement and its scope.
The system must support this cycle without requiring a new ticket per pass or
hardcoding a fixed set of agent identities.

### Machine execution, scheduling and visibility

1. Preserve central scheduling and dispatch to Linux, native macOS and native
   Windows environments, including whole-workspace execution where configured.
2. Mica chooses an environment that has the capabilities the current work needs.
   Platform-specific work need not become a different product objective.
3. Scheduled work and continuation must remain recoverable across application,
   daemon or agent interruption. Machine unavailability must be visible.
4. Mutable workspace access must prevent unintended interference. Independent
   work can run concurrently when its execution surfaces are suitably isolated.
5. Keep inspectable run traces, results, model/effort and environment identity,
   session continuity or freshness, handoffs, review evidence and delivery
   outcomes. The ticket must make the current owner, phase, next action and
   reason for human involvement understandable without reconstructing all logs.

### Acceptance, triviality and PR delivery

Independent engineering review establishes technical confidence. By default,
it does not itself accept the work or authorize delivery. An authorized human's
clear comment on the Multica issue or a bound Forgejo PR, such as “Approved,”
“looks good,” or “make ready,” is an instruction to accept the exact current
candidate and make its PR ready: remove a `WIP:` prefix and, where supported,
mark a draft PR ready. That instruction does not authorize merging. A clear
“approved, merge it” instruction separately authorizes merge of that candidate,
subject to current provider, branch-protection and exact-head checks. Questions
and ambiguous feedback are not acceptance.

The agent interprets the comment in context and checks that its author has
authority over the work. It records the comment and its source, author, time,
candidate and revision as acceptance provenance; the server verifies the stored
comment evidence before recording the action. The human need not use the TUI,
change ticket status or assignee, or add a policy pin just to express approval.
Those conveniences do not replace candidate, revision, identity or access
checks. Do not infer acceptance from status, assignment, a passing review or
ordinary comments.

The active policy's independent review remains required unless the human
explicitly says to skip review for this candidate. That instruction is a
candidate-scoped review override: record its source and reason without asking
for another approval. It overrides review only; by itself it does not accept the
work or authorize merge. Pair it with a clear acceptance or merge instruction,
or apply it to an already accepted candidate without asking the user to repeat
that acceptance. It does not transfer automatically to a later candidate.
When review is required, a human approval comment does not waive it; complete
the required review before finalizing acceptance and readiness. A human comment
that explicitly releases or supersedes an existing hold may do so for its
stated candidate. Approval alone preserves existing delivery holds and
no-merge conditions.

The user's assignment or commissioned ticket establishes the approved outcome
and delegated work scope; do not require a second approval record merely to
restate them. Reconcile concrete user feedback and current repository/provider
facts ahead of stale process labels. Acceptance records its actor, authority,
source comment and exact delivery candidate. With required PRs still unmerged,
acceptance moves the ticket to `PR Ready`. A card movement alone does not
manufacture acceptance or authority for unseen commits.

Human rejection returns work to the appropriate retained implementation or
review/fix context. Preserve the rejection, existing evidence and useful
context; invalidate affected acceptance and pending delivery authority. Distinguish
a defect within the agreed scope from a new request: a defect continues the
existing objective, while changed intent must be reconciled as an explicit
scope change or separately owned work. A rejection is not permission to discard
unrelated evidence or silently expand the objective.

The human owner may change or commission work on a ticket's PR directly. A
reviewer may identify that the resulting code exceeds the ticket's earlier
description and ask whether to update that description or separate the work.
It must not describe the owner's action as unauthorized or order its removal
solely because agent delegation had a narrower scope. Preserve the commit and
obtain the owner's decision before reverting or splitting it. The owner deciding
to keep it expands that ticket's scope; review the full current candidate and
still require any applicable exact-commit review and acceptance. Agent access
limits, platform guards and unrelated repository ownership remain enforced.

Mica may classify a ticket as trivial under a configurable policy and record a
brief reason. A separately configured, explicitly scoped autonomous policy may
allow an authorized agent to accept and deliver trivial work or reviewed
nontrivial work within its delegated scope. That option remains available but
is not the default. Independent review alone does not activate it. Never label
nontrivial work as trivial to make it advance, and do not infer autonomous
acceptance or merge authority from a classification, subtask completion or
installed bundle.

If scope or risk expands beyond delegated authority, the agent returns the
material decision to the user. The user can override classification or the
applicable policy. Acceptance of a subtask does not authorize acceptance or
merging of unrelated work or its parent.

Acceptance initiates deterministic readiness bookkeeping:

- Remove the PR's `WIP:` title prefix and, where supported and applicable,
  transition a draft PR to ready for review.
- Do not merge solely because a candidate was accepted or made ready. Merge
  only when an authorized human explicitly instructs it, or an explicitly
  enabled and scoped autonomous policy grants that delivery authority, after
  provider checks and branch protections permit it.

Human approval is not revoked merely because a commit, branch, repository URL
spelling or candidate identifier changes. Mica carries it forward when the
subsequent delta preserves the approved behavior, scope, risk and stated
conditions, and the required independent evaluation covers the current code.
Record the original approval, old and current heads, delta, evaluation evidence
and a brief reason in the existing work record. Metadata-only corrections,
review fixes and integration changes require proportionate evaluation, not
repeat GUI QA or another human decision by default.

Ask again only when the delta materially changes approved behavior, scope,
risk or conditions, conflicts with prior approval, or leaves relevant judgment
unresolved. Explain that specific difference. Do not relabel a substantive
feature as trivial to reuse approval. A rejection or explicit request to withhold acceptance
cannot be treated as approval.

Normal integration of the parent/base branch into an approved feature is not,
by itself, a reason to revoke approval, repeat review or return the ticket to
`in_review`. Keep existing evidence and record the integration; evaluate actual
new feature changes or material conflict resolutions proportionately. A stale
SHA may pause an agent's merge operation without undoing the user's approval.

An actual user-directed merge of a bound PR, including one performed through
Primary or the service account at the user's direction, is delivery evidence
and may also express acceptance of the merged candidate. Observe the provider's
completed merge before applying changed-head rules. It does not require another
acceptance action and an agent no-merge hold does not invalidate a merge already
performed by the user. Close when all required bound PRs are merged. Preserve unperformed deployment or QA
as evidence and separately owned follow-up work; do not invent a second completion acknowledgment. The retained `delivery.external_merged_head` and candidate `external_merge`
fields remain readable for compatibility, but do not suppress completed provider
facts. Automated outbound merge authority remains separately guarded.
Record observed head, merge commit and provider actor; a service-account actor
is not itself evidence of which human instructed it. Observing an authorized
merge never grants an agent permission to perform a different merge.

Delivery remains bound to the exact evaluated commits for agent-initiated merges. A changed commit pauses
stale delivery authority until evaluation and an explicit current-candidate
acceptance record reconcile it. A prior human comment remains in the record as
provenance; do not retarget it blindly to unseen commits. Carry approval forward
only under the existing nonmaterial-delta rule, with the original source, old
and current heads, delta and evaluation recorded. Preserve delivery holds and
no-merge instructions independently of acceptance.

Delivery failure does not erase acceptance. While required merges remain, the
ticket stays `PR Ready` and shows acceptance, pending/failed delivery, reason
and retry state. For several PRs, record an explicit dependency-aware merge
order and each result; partial delivery must not appear complete. Retrying must
not merge a different revision or duplicate completed actions.

Accept-but-hold means accepted work stays `PR Ready` with an explicit delivery
hold. Preserve the hold across retries and restarts until an authorized release;
acceptance or a retry alone must not clear it. An explicitly authorized merge
goes through `PR Ready` to `done` after all required bound PRs merge. Explicit
acceptance of work with no PR may finish directly; do not manufacture a PR
or require an internal outcome flag. Separately requested deployment or runtime
validation remains visible as follow-up work, with its own actual evidence.

Substantive feature changes require affected engineering evaluation. Parent/base
integration alone does not. Reconcile the current head before an agent-initiated
merge while preserving approval, and recognize authorized completed provider
merges under the external-merge rule without reopening review.

### Agreed production policy

These defaults reflect the user's 2026-09-28 direction. They take effect only
with the supporting backend and versioned policy deployment; existing ticket
pins change through explicit migration or scoped override.

- Independent engineering review is not product acceptance or merge
  authorization. For the default path, a clear comment from an authorized human
  on the issue or bound PR accepts the exact current candidate and authorizes
  readiness. Record the source comment, author, candidate and revision. The
  human need not use the TUI or change ticket status, assignee or pin to express
  that decision.
- A comment such as “Approved,” “looks good,” or “make ready” authorizes
  acceptance/readiness only. A clear instruction such as “approved, merge it”
  separately authorizes merge, subject to current provider and exact-head
  checks. Preserve an explicit hold or no-merge instruction until the user
  clearly releases or supersedes it.
- Questions and ambiguous feedback are not acceptance. An explicit “skip
  review” instruction overrides review only for the identified candidate;
  record its source and reason without asking the user to repeat the approval.
  All other configured review, validation, identity and access guards remain.
- A separately enabled, scoped autonomous policy remains available for work it
  expressly covers. It is optional, not the default. An independent review or
  trivial classification alone does not enable autonomous acceptance or merge.
- Bring the user in for unresolved product intent, material scope or risk
  changes, or a review checkpoint the user actually requested. Evaluate an
  integrated UI when it is available instead of requiring approval of invisible
  implementation layers. A user-requested hold remains until explicitly
  released or superseded; do not manufacture a hold from an agent's preference
  for caution.
- Mica may resolve an overcautious procedural block within existing authority:
  repeated requests for already-granted permission, optional checks treated as
  mandatory, or routine execution choices escalated unnecessarily. Record the
  reason, scope and consequences briefly on the existing work record. This is
  not authority to invent acceptance, skip-review or merge permission.
- When a merge is explicitly authorized, use squash and merge by default, with
  a meaningful commit title/message and ticket and PR references. A separate
  merge commit is a justified explicit exception for an integration or release
  branch whose history is meaningful.

This list can evolve through explicit policy revision. Keep existing ticket
pins and scoped exceptions intact. These decisions do not authorize live
merges or activate a workspace policy.

### Configurable policy and system mechanics

User-defined policies are standing defaults. The user may explicitly override
them. Supervisors may grant justified, scoped exceptions within their delegated
authority, briefly recording the reason and consequences in the existing work
record. An exception does not silently amend general policy or grant authority
outside that delegation. Review requirements follow this same exception model.

Agent behavior must be editable, versioned and overridable without requiring a
Multica rebuild. This includes Mica's intake and decomposition, model/effort
selection, escalation, review arrangements, triviality classification and
acceptance/delivery authority. A user override applies within its stated scope.

Each ticket retains its identified policy version across runs and retries.
New defaults apply to new tickets. An existing ticket changes policy only by
explicit migration or a scoped override; an override preserves the base policy
identity and its own scope. Editing an agent, skill or default policy must not
silently reinterpret an existing ticket. Execution history identifies the
applicable version and exceptions.

The system owns reliable mechanics: persisted tasks, scheduling, handoffs,
context selection, execution isolation, evidence identity, authorization checks
and delivery retries. It must enforce the selected policy and make that policy
identifiable in execution history. It must not impose an incompatible hidden
workflow through generated instructions or fallback prompts.

Examples of behavior that must not be mandatory compiled policy include
"done stays human," "every executing reviewer changes to in_progress," and
"each acceptance pass requires a child ticket." Configurable instructions may
still be tested and versioned. Platform permissions and provider protections
remain enforced; editable policy does not create otherwise unavailable access.

### Repository-owned context and diagnoses

Important decisions, limitations and resumption context remain in their owning
repositories. Tickets and run traces provide operational evidence and links;
they are not the sole durable home of information needed to understand and
resume the work.

Useful implementation diagnoses identify their observation date and affected
revision or candidate. When resolved, remove them from current guidance or
replace them with verified continuing guidance. Preserve relevant evidence in
its existing owning location and Git history; do not manufacture an archive
merely to retain resolved diagnoses.

### Cutover and existing work

1. Prove the new workflow before activating it for ordinary new tickets.
2. At cutover, new tickets use the new workflow. Old unfinished tickets remain
   frozen until explicitly migrated; they do not continue automatically under
   either workflow merely because new policy is installed.
3. Preserve old descriptions, work, results, sessions and traces. Freezing does
   not complete, cancel or discard their objectives.
4. Explicit migration reconciles the actual work, remaining scope, evidence,
   context and ownership before resuming under the new workflow.
5. The cutover procedure must account for already-running tasks and outstanding
   schedules, wakeups, assignment and comment triggers. Their safe handling is
   a technical design requirement, not permission to terminate them now.
6. Coordinate canonical KB, skills, agent instructions, policy bindings and
   automation changes at this boundary. Preparing code or a new policy bundle
   does not activate it. Existing unfinished tickets stay frozen until explicit
   migration reconciles their policy version and retained work.

## Constraints and non-goals

- Keep Multica as the execution and observation platform; do not introduce a
  second project-owned scheduler or task lifecycle solely to implement this flow.
- Preserve useful context boundaries and independent evaluation without
  perpetuating mandatory role identities or ticket templates as control flow.
- This change does not require replacing the Git provider, merging unrelated
  work, deploying products, or granting broader infrastructure privileges.
- Configuration compatibility and migration must preserve existing policy
  choices until explicit cutover. Active legacy tickets must not accidentally
  inherit autonomous acceptance or merging.
- A spec, successful unit test or completed agent run is not proof of the full
  workflow on the deployed environments.

## Configuration ownership and rollout limits

The [README](README.md) describes the released handoff, context, policy and
completion mechanisms. The [release record](release.md) identifies the active
Linux deployment and links the owning configuration evidence;
[runtime-proof.md](runtime-proof.md) records bounded provider trials and limits.
The [agreed production policy](#agreed-production-policy) settles comment-based
human acceptance, readiness, review overrides, optional scoped autonomy and
merge authority. Human acceptance leaves the PR ready; it merges only after an
explicit human merge instruction or under an explicitly enabled scoped
autonomous policy. Format 2 keeps accepted work in PR Ready until required bound
PRs merge.

The selected [runtime policy](policies/mica-v1/runtime/policy.json) records
available Linux Mica identities and review/acceptance/delivery scopes. Any
autonomous acceptance or delivery it enables is an optional scoped policy
override, not the default behavior for enrolled work. The retained outcome-agent
identity does not require or automatically dispatch a postmerge run. Supervisor
acceptance scope is limited by this policy to candidate-scoped carry-forward
with documented approval provenance and required delta evaluation; it does not
delegate approval of new or materially changed substantive work. Workspace-control
owns actual agent/runtime identities, model/effort selection and review routes
in `config/mica-agent-desired-state.json`; its source manifest and imported
skill identity must match. Native macOS/Windows agent rollout and targeted host
proof remain deferred until those deployments. The desktop client upgrade does
not activate native agent policy.

Shared guidance lives in `kb/docs/workflow-automation.md`, the matching KB
skills and operational objective template. Workspace-control owns provider
mappings and its `docs/multica-workflow.md` activation record. Keep shared
requirements in KB and provider configuration in workspace-control; do not
maintain competing active workflow contracts. Existing frozen tickets require
explicit individual migration that preserves remaining work, evidence, context
and ownership. No blanket migration accompanies activation.

## Acceptance criteria

| Scenario | Observable required result |
| --- | --- |
| Direct job | A job assigned to Mica reaches execution without the user choosing an implementor profile or writing handoff instructions. |
| Clarification | Mica resolves recoverable missing details itself and routes a consequential unresolved decision to Primary or the user while preserving independent progress. |
| Capability choice | Straightforward and demanding jobs can use different configured models/efforts; a reassessment can escalate capability without losing work. |
| Useful decomposition | Mica can finish a coherent job on one ticket or create useful subtasks and reconcile their integrated outcome; neither structure is mandatory. |
| Multi-turn work | An interrupted implementation resumes the same feature context when available; lost continuity is explicit and reconstructable. |
| Resumed writer | Intervening commits, decisions and overrides are reconciled before retained-context code writes. |
| Reviewer execution | Assignment in `in_review` starts the intended reviewer, which remains in that phase while reviewing and correcting routine findings. |
| Blind review loop | A fresh reviewer fixes a defect; a distinct fresh final reviewer finds another; the existing review/fix context repairs it and another fresh final pass evaluates the result. No review-pass ticket is required. |
| Handoff recovery | Duplicate events, an ambiguous response or interruption does not produce duplicate recipient execution or review of a still-changing outgoing surface. |
| Code handoff | A handoff identifies the draft PR, repository, branch and exact commit; technical review evidence stays in PR reviews, linked from the coordinating ticket. |
| Cross-repository work | One ticket coordinates several PRs and identifies their candidate commits; acceptance and subsequent delivery cannot silently cover changed commits. |
| Human acceptance | After required engineering review, an authorized human's clear issue or bound-PR comment accepts the exact current candidate and moves its PR to ready; no TUI, status, assignee or pin ceremony is required. “Approved” or “make ready” does not authorize merge; an explicit merge instruction does. Questions and ambiguous feedback are not acceptance. |
| Accept but hold | Acceptance with an explicit hold leaves the ticket `PR Ready`; retries and restarts preserve it until authorized release. |
| Outcome completion | Merged bound PRs close the ticket without another acknowledgment; no-PR acceptance finishes directly. Partial delivery stays open, and unperformed deployment/QA remains recorded. |
| Human rejection | An in-scope defect resumes the appropriate retained context, preserves evidence and invalidates affected acceptance; a new request is explicitly distinguished. |
| Optional autonomous trivial work | A separately scoped policy may let an authorized agent accept and deliver a policy-qualified trivial candidate; this path is disabled as a general default and never follows from review alone. |
| Optional autonomous reviewed work | A separately scoped policy may let an authorized agent accept and deliver reviewed nontrivial work within its explicit scope, with an exact-candidate rationale. |
| Review override | An authorized human's explicit skip-review comment overrides review for that candidate only; its source and reason are recorded and it does not transfer to a later candidate. |
| Classification change | Work that ceases to qualify as trivial can use reviewed-agent acceptance only when an explicit scoped policy grants it; otherwise follow the human-comment path. |
| Revision change | Changed commits require affected evaluation and exact-head delivery authority; documented human approval carries forward for a verified nonmaterial delta without repeat approval or GUI QA. |
| Delivery recovery | A provider failure remains visible alongside acceptance and retries the same authorized action safely. Already completed delivery is not repeated. |
| Machines and workspace | Scheduled representative jobs execute on the declared Linux, native Mac and native Windows environments, including scoped cross-repository work; their traces and machine identity are inspectable. |
| Policy override | An authorized configuration override changes behavior without rebuilding Multica, and generated instructions do not contradict it. |
| Scoped exception | A delegated supervisor records a justified exception's scope, reason and consequences; general defaults and unrelated tickets remain unchanged. |
| Policy continuity | Changing defaults leaves an existing ticket and its retries on the identified version; new tickets receive the new default and migration or override is explicit. |
| Review default | Independent engineering review is required by the active policy but does not itself accept or authorize merge. An explicit human skip-review instruction may override it for the current candidate with recorded provenance. |
| Durable context | Owning repositories retain important decisions, limitations and resumption context; useful diagnoses are dated and revision-bound and resolved diagnoses leave current guidance without a manufactured archive. |
| Cutover | New tickets use the proven workflow; old unfinished tickets do not resume automatically; explicit migration preserves their work and restores deliberate continuation. |

Validate state, authorization, concurrency, session and provider boundaries with
focused automated tests, then use the recorded Linux provider and
cross-repository trials as the initial runtime evidence. Any later policy
change must reconcile consumers and verify workspace-specific state before
activation. Native macOS and Windows receive targeted
dispatch, repository access and fresh/retained-context checks when deployed;
their checks do not block Linux activation or repeat server-side delivery proof.
Distinguish code-level evidence from
actual daemon/provider execution and human acceptance. No operational issue,
implementation or rollout is created by this specification alone.

## Fact-based completion amendment (2026-09-27)

User decisions and provider facts outrank internal bookkeeping. `outcome_complete`
is a compatibility field, not an additional user obligation. Do not automatically
create an outcome-agent run after merging. A failed delivery attempt must not
prevent observing a later completed provider merge. Record completed merge facts
even when policy would forbid an agent from initiating that merge. This never
grants permission for an outbound merge of different code.

Ticket status and editorial text are presentation and context, not acceptance
credentials. A clear, authorized human comment is acceptance evidence when the
agent records its source and the server verifies the stored comment against the
exact candidate and revision. Record scope snapshots, including acceptance
criteria; reconcile substantive changes through agent judgment and affected
evaluation. Do not revoke approval merely because the title or description
changed. A new candidate can retain prior approval as evidence only through
recorded provenance and an authorized carry-forward decision; candidate-scoped
review waivers do not transfer. Scoped exceptions do not require a fictional
rejection of accepted work. Authentication, concurrency checks, current review
policy and exact-head checks before any authorized merge remain.

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

This spec records the workflow agreed on 2026-09-24. It describes intended
behavior, not an implemented or deployed capability. Technical choices and
remaining policy parameters are identified below. Writing this spec does not
activate automation, merge PRs, freeze live tickets, or migrate existing work.

This file owns the proposed Multica fork behavior. Changes to shared Trackself
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

One ticket may coordinate several PRs, including PRs in different repositories.
Its candidate identifies each relevant PR and commit. Acceptance applies to
those exact commits; new commits require renewed evaluation and acceptance for
affected work. Coordinated merge ordering and partial delivery follow the selected
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
5. Task descriptions contain the outcome, relevant boundaries and references
   needed to execute. They do not reproduce generic workflow instructions as
   a prerequisite for every handoff.

### Implementation continuity

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
| `in_review`, human | Required engineering work and agent review are complete; human acceptance is needed. |
| `done`, human-accepted work | The human accepted the identified candidate; configured delivery follows. |
| `done`, autonomously accepted trivial work | An authorized agent accepted the candidate under the selected trivial-work policy; automatic delivery follows. |

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

Implementation normally receives independent review, including trivial work
eligible for autonomous acceptance. Review depth, capability and applicability
are configurable policy, subject to the same user override and delegated
supervisor exception authority defined below. Triviality alone is not a review
waiver. Any exception identifies the affected review requirement and its scope.
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

For nontrivial work, successful required agent review leads to `in_review`
assigned to the human. The human's transition to `done` is acceptance of the
identified candidate and triggers the configured delivery policy.

Human rejection returns work to the appropriate retained implementation or
review/fix context. Preserve the rejection, existing evidence and useful
context; invalidate affected acceptance and pending delivery authority. Distinguish
a defect within the agreed scope from a new request: a defect continues the
existing objective, while changed intent must be reconciled as an explicit
scope change or separately owned work. A rejection is not permission to discard
unrelated evidence or silently expand the objective.

Mica may classify a ticket as trivial under a configurable policy and record a
brief reason. Trivial tickets are eligible for autonomous acceptance and merge,
without waiting for a human `done` action. An authorized agent can accept and
complete them after the policy's required validation and review succeed.

If scope or risk expands beyond the classification, the ticket returns to the
ordinary human-acceptance path. The user can override classification or the
applicable policy. A trivial subtask does not authorize acceptance or merging
of unrelated work or its nontrivial parent.

Acceptance initiates deterministic delivery bookkeeping:

- Remove the PR's `WIP:` title prefix and, where supported and applicable,
  transition a draft PR to ready for review.
- For autonomously accepted trivial work, merge under the configured policy
  after required provider checks and branch protections permit it.
- For human-accepted work, merge when the selected project/ticket policy calls
  for it; otherwise leave the PR ready. No repeat approval is needed for a
  delivery action already authorized by that policy and acceptance.

Acceptance and pending merge authority are tied to identified revisions. A new
commit after acceptance invalidates authority to merge the changed candidate;
it must receive the applicable evaluation and acceptance again.

Delivery failure does not erase acceptance. The ticket shows acceptance together
with delivery pending/failed, the reason and retry state. Retrying must not merge
a different revision, duplicate completed actions or conceal partial delivery.
For work with no PR, completion must not depend on manufacturing one.

The initial triviality threshold, merge method and coordinated multi-PR delivery
policy remain to be selected. This spec authorizes their design, not live merges.

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

## Technical questions and remaining policy parameters

The behavior above is the target. These questions must not be mistaken for
reopening the user's agreement on that behavior.

| Kind | Question to resolve |
| --- | --- |
| Technical | How is Mica's coordination ownership retained while another agent is assigned, and what reliably returns exceptions or results to Mica? |
| Technical | Which native assignment/wakeup mechanism implements a recoverable handoff without duplicate launches or outgoing/incoming overlap? |
| Technical | How are retained implementation/review contexts and explicitly fresh acceptance contexts selected, verified and recovered? What happens on a model/provider change? |
| Technical | How is the blind review input assembled without inheriting implementation conversation or verdict coaching? |
| Technical | How do ticket policy versions, scoped exceptions and explicit migrations cover behavioral inputs beyond the workflow skill bundle? |
| Technical | What durable mechanism performs delivery after completion, independently of ordinary issue wakeups that stop on closed issues? |
| Technical | Which deployed server/daemon versions, credentials and provider APIs support the required execution and PR actions, including the existing Forgejo usage? |
| Technical | How do freezing and explicit migration cover active runs and every existing trigger path? |
| Policy parameter | Initial configurable triviality criteria and exceptions; no numeric or file-count threshold has been agreed. |
| Policy parameter | Initial review requirements by work class, model/effort mappings, and handling of a review cycle that cannot converge. |
| Policy parameter | Merge method, automatic merge setting after human acceptance, and acceptance/delivery semantics for several PRs or repositories. |

The [implementation plan](implementation_plan.md) owns the current implementation
position, revision-bound diagnoses, remaining mechanics and proof. The
[fork README](README.md) describes available customization and its limits.
Neither implementation progress nor a packaged policy establishes the complete
workflow's acceptance criteria.

Shared integration references to reconcile in the Trackself workspace are
`kb/docs/workflow-automation.md`, `kb/skills/review-fix-cycle/SKILL.md`,
`kb/templates/operational-issue-objective_template.md`,
`workspace-control/docs/multica-workflow.md`,
`workspace-control/config/multica-agents.toml`, and their instruction/skill
consumers. Keep shared requirements in KB and provider mappings in
`workspace-control`; do not maintain competing active workflow contracts.

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
| Human acceptance | Nontrivial work reaches the human in `in_review`; human `done` records the candidate and initiates readiness and configured delivery. |
| Human rejection | An in-scope defect resumes the appropriate retained context, preserves evidence and invalidates affected acceptance; a new request is explicitly distinguished. |
| Autonomous trivial work | A policy-qualified trivial ticket completes required checks/review, is accepted by an agent and merges without a human acceptance step. |
| Classification change | Work that ceases to qualify as trivial loses autonomous acceptance eligibility and follows the human path. |
| Revision change | New commits after acceptance prevent merging under the stale acceptance; the new candidate is evaluated under the applicable policy. |
| Delivery recovery | A provider failure remains visible alongside acceptance and retries the same authorized action safely. Already completed delivery is not repeated. |
| Machines and workspace | Scheduled representative jobs execute on the declared Linux, native Mac and native Windows environments, including scoped cross-repository work; their traces and machine identity are inspectable. |
| Policy override | An authorized configuration override changes behavior without rebuilding Multica, and generated instructions do not contradict it. |
| Scoped exception | A delegated supervisor records a justified exception's scope, reason and consequences; general defaults and unrelated tickets remain unchanged. |
| Policy continuity | Changing defaults leaves an existing ticket and its retries on the identified version; new tickets receive the new default and migration or override is explicit. |
| Review default | Trivial autonomous work normally receives independent review; skipping a required review needs an exception under the same authority rules. |
| Durable context | Owning repositories retain important decisions, limitations and resumption context; useful diagnoses are dated and revision-bound and resolved diagnoses leave current guidance without a manufactured archive. |
| Cutover | New tickets use the proven workflow; old unfinished tickets do not resume automatically; explicit migration preserves their work and restores deliberate continuation. |

Validate state, authorization, concurrency, session and provider boundaries with
focused automated tests, then demonstrate the complete workflow and recovery on
Linux before initial activation. Native macOS and Windows receive targeted
dispatch, repository access and fresh/retained-context checks when deployed;
their checks do not block Linux activation or repeat server-side delivery proof.
Distinguish code-level evidence from
actual daemon/provider execution and human acceptance. No operational issue,
implementation or rollout is created by this specification alone.

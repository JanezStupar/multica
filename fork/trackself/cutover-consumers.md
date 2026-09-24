# Cutover consumer map

This is preparation for the agreed Mica workflow, not an activation record.
The target policy is in [workflow_spec.md](workflow_spec.md); this map identifies
the active consumers that must be reconciled after the implementation and
provider proof gates pass. No external KB or `workspace-control` file was
changed for this audit. At cutover, existing unfinished tickets will remain
frozen until individually migrated.

## Ownership and revision basis

The aggregate `/home/janez/workspaces/trackself` directory is not a Git working
tree: its `.git` directory is empty and `git -C /home/janez/workspaces/trackself
rev-parse` fails. Use each owning repository's revision instead. These are the
revisions read for this audit on 2026-09-24:

| Owner | Revision | Working-tree state relevant here |
| --- | --- | --- |
| Multica fork, `/home/janez/workspaces/multica/multica` | `c052b3b5cb68a4c23929ad51697657fe67b1473b` | Broad existing implementation changes; preserved. `fork/trackself/workflow_spec.md` and the Mica bundle are working-tree inputs. |
| Canonical KB, `/home/janez/workspaces/trackself/kb` | `2c31b9c475def3718bdd81e8f61485f01124f476` | Dirty elsewhere, including `README.md`. The inspected `docs/workflow-automation.md`, review-fix skill and objective template are clean relative to this HEAD. |
| Workspace control, `/home/janez/workspaces/trackself/workspace-control` | `6871897ce8c95126955f263127ae441bcf0276b4` | Dirty in `config/multica-agents.toml` and `docs/multica-workflow.md`; those working-tree versions were inspected. `AGENTS.md` is clean. |

The target workflow is agreed but explicitly not implemented or deployed by
the spec. A policy bundle, passing local tests, or a prepared consumer patch
does not prove runtime behavior or activate tickets.

## Active consumer boundary map

| Consumer and owner | Active instruction or automation | Conflict with Mica target | Minimal cutover intent |
| --- | --- | --- | --- |
| Shared authority and workflow protocol — `kb:README.md` and `kb:docs/workflow-automation.md` | KB currently says user policy exceptions are scoped and preserve higher-priority platform limits. The automation doc defines a parent/child progression, a child for every materially distinct slice, a reusable review child, a fresh acceptance child per verdict, and child-terminal stage wakes. It also defines status/assignment semantics around those stages. | Scoped user overrides and delegated exceptions are compatible and must remain. The fixed staged lifecycle conflicts with optional decomposition, same-ticket review/fix and acceptance, and configurable review/acceptance policy. These staged mechanics must not become universal rules for unrelated integrations. | Preserve user-authority, safety and record-ownership principles. In the shared doc, state only provider-neutral lifecycle constraints and make staged child automation an explicitly opted-in integration pattern. Put Mica's selected behavior in its versioned policy. Do not copy the Mica contract into canonical KB. The KB README is dirty in this workspace, so reconcile against its current exception wording rather than replacing it from HEAD. |
| Generic review/fix process — `kb:skills/review-fix-cycle/SKILL.md` | Describes a stateful Review Controller cycle with routine fixes and validation, then a fresh read-only acceptance context where required. Its automatic-workflow paragraph delegates child completion, stage wakes and parent reconciliation to the workflow doc. | The core separation of fix context and independent final judgment fits the target. The current pointer inherits a child/stage lifecycle and does not say that review passes remain on the owning ticket. | Keep the generic review/fix process. Narrowly point automatic execution to the active integration's selected, versioned workflow for ticket lifecycle and dispatch. Do not duplicate Mica acceptance, triviality or delivery policy here. Preserve generic fallback behavior for integrations that have not opted in. |
| Operational objective template — `kb:templates/operational-issue-objective_template.md` | Requires executive summary, parent objective, objective, validation boundary, current state, next executable slice, mechanical done criteria, completion authority, boundaries and owning references. The template includes detailed child-specific reconciliation and terminal-status wording. | The template makes child/stage bookkeeping look mandatory and asks ticket authors to carry workflow boilerplate that the Mica model assigns to policy and system mechanics. | Reduce the default form to outcome, scope/boundaries, evidence or completion condition, and owning references. Make current state/next actor optional when there is prior work or a real handoff. Keep task-specific risk, authority and validation visible when they affect the decision. Remove generic process checklists and child-only rules; do not remove the human-readable outcome requirement. |
| Workspace authority and integration declaration — `workspace-control:AGENTS.md` and `project-workflow.toml` | Declares Multica automatic workflow active and points to `docs/multica-workflow.md`, `config/multica-agents.toml` and the CLI. Requires desired-state validation and read-only live check before any separately authorized live apply. | The declaration activates the current staged behavior operationally; instructions alone cannot replace the policy pin/default and cutover mechanics in the spec. | Keep repository ownership, authorization and preflight rules. At the eventual boundary, update the declaration and its pointers to the proven policy version and supported cutover state. Retain validate/read-only-check requirements and separate exact-scope live apply authority. Do not change the declaration while proof is incomplete. |
| Multica provider runbook — `workspace-control:docs/multica-workflow.md` | Long provider-specific procedure for project/resource checks, bound-workspace verification, children, stage transitions, task/session identity, trigger-safe dispatch, parent reconciliation, lanes and recovery. Its current working-tree content is dirty. | The runbook's fixed child stage graph and status transitions encode Trackself's current provider workaround. The same file also owns valuable Multica mechanics and workspace/platform procedures that the target still needs. | At cutover, retire or replace only the staged lifecycle procedure with a short link to the selected Mica policy plus exact provider mechanics that remain necessary. Preserve project/resource validation, `git-ws` and bound-workspace checks, trigger/task reconciliation, central scheduling, and Linux/macOS/Windows routing. Keep provider failure modes as implementation guidance only when still verified; do not preserve resolved incident timelines as active rules. |
| Trackself orchestrator skill — `workspace-control:config/multica-skills/trackself-working-on-issues/` | Custom skill assigned to Mika/Maca/Mewina, with references for continuation, metadata, side effects and PRs. Its top-level flow requires staged children and child-triggered continuation. | Directly instructs current orchestrators to execute the fixed hierarchy, in tension with Mica discretionary decomposition and native same-ticket handoffs. | Replace or unbind this Trackself-only staged skill during the coordinated cutover. Preserve Multica command/safety references that remain accurate; point to the versioned Mica all-role policy. Do not generalize its provider workarounds into KB policy or impose Mica policy on other integrations. |
| Provider-facing roles and runtime settings — `workspace-control:config/multica-agents.toml` plus `config/multica-agent-instructions/` | TOML projects orchestrators and distinct implementor, review, acceptance, QA and release roles for Linux/container, Linux branches, macOS and Windows. It owns exact runtime IDs, model/effort selectors, concurrency, permissions, skills and instruction files. Mika/Maca/Mewina and workflow/role instructions restate staged children, `done` wakes and per-verdict acceptance children. The TOML is dirty in this workspace. | The role count and current routing instructions impose the stage graph. Runtime identifiers, current model/effort mappings, permission modes and native platform roles are configuration facts, not portable KB truth; the spec intentionally leaves mappings configurable. | Reconcile every configured workflow instruction and relevant binding as one desired-state change. Remove only mandatory stage/child instructions and stale status assumptions. Keep exact runtime IDs, selected model/effort, permissions, workspace/project routing and Linux/macOS/Windows agent coverage under this owner until explicit configuration decisions change them. Validate definitions and use the read-only live check before any later apply. |
| Multica default prompts, wakeups, schedules and already-enrolled issues — Multica code/configuration and live workspace state | Provider-generated prompts and task/event triggers can dispatch independently of the written skill; existing work may have active tasks, schedules, comments, wakeups or retries. The workflow spec names these as cutover hazards but does not yet prove their handling. | Replacing a skill or editing an instruction does not prevent stale triggers from launching old behavior, establish new-ticket defaults, or freeze old unfinished work. A fresh run ID does not by itself prove a fresh provider context. | Prove the implemented cutover fence across actual dispatch sources and runtimes. At activation, route new tickets to the selected version; freeze old unfinished tickets pending explicit per-ticket reconciliation. Preserve descriptions, results, sessions and traces. Do not cancel, migrate, pin or mutate live tickets as part of this preparation. |

## Cutover dependencies and evidence

Before changing any active consumer, the implementation owner needs evidence for
all of the following:

- The selected policy archive is accepted by Multica's real importer and its
  root workflow, role policy, platform references and manifest agree with the
  source revision.
- Policy and agent-instruction versions are captured per ticket/run as
  intended; edits to defaults do not silently reinterpret existing tickets.
  Scoped user overrides and delegated supervisor exceptions retain their scope
  and reason.
- Same-ticket handoff, resume and fresh-review paths are provider-proven. Logs
  distinguish task/run IDs from provider session IDs; independent reviewers
  receive the complete exact candidate and do not inherit the preceding verdict.
- Claims, retries, duplicate events and ambiguous handoff responses cannot lose
  work, launch duplicate recipients, overlap mutable workspace access, or
  proceed on stale candidate commits. Review and acceptance bind to exact
  revisions and invalidate when affected commits change.
- The new-ticket default and opt-in policy pin are explicit. The cutover fence
  covers assignment, comments, schedules, wakeups, retries and already-running
  tasks. Every status mutation and dispatch path, including deferred handoffs,
  failure recovery and VCS webhooks, must honor the freeze even for legacy
  unpinned issues. Retain pending work for explicit migration rather than
  silently applying it or discarding it. Old unfinished tickets remain frozen
  until an individual migration reconciles remaining work, evidence, policy
  version and owner.
- Initial workflow proof covers Linux execution and configured cross-repository
  paths. Native macOS and Windows checks accompany deployment to those hosts;
  they do not block Linux activation. Each host needs a targeted dispatch,
  repository access and fresh/retained-context smoke test, not a repeat of
  server-side acceptance and delivery proof. Local tests alone are not runtime proof.
- Acceptance and delivery mechanics are proven separately: human acceptance,
  trivial autonomous acceptance, rejection/rework, PR readiness, merge retries,
  issue closure and coordinated multi-PR delivery all preserve exact-revision
  authority.

The current implementation plan records passing local database, importer,
provider-mock and race checks for cutover fencing, policy lifecycle and outbound
delivery, together with independent code acceptance. Actual provider sessions,
cross-platform execution and live PR delivery still need the scoped runtime
trial. Those proof limits are not erased by the working-tree policy archive.

## Candidate patch sequence for an isolated KB/workspace copy

Prepare and review these changes in a disposable copy of the owning repositories;
do not apply them to the active KB or workspace configuration during this
implementation phase:

1. **Canonical KB:** first revise only the provider-neutral parts of
   `docs/workflow-automation.md` and the staged-workflow pointer in
   `skills/review-fix-cycle/SKILL.md`. Keep scoped exception authority and
   generic review quality rules. Make integration-specific lifecycle opt-in;
   put no Mica role graph, model mapping or Multica command procedure in the KB.
2. **Template:** simplify
   `templates/operational-issue-objective_template.md` to outcome, scope,
   evidence/completion, and owning references, with a concise optional
   current-state/next-actor section. Preserve task-specific authority and
   validation disclosures. Remove child-only fields and mandatory checklist
   narration.
3. **Workspace-control:** replace the Trackself-only orchestrator skill and
   revise linked workflow instructions so all active role configurations
   express the same selected policy. Keep the existing agent IDs, runtime
   identities, model/effort fields, platform-specific safety and project-bound
   workspace procedure unless an explicit configuration decision changes them.
   Keep the Multica runbook as an adapter guide and remove stale stage recipes.
4. **Activation declaration:** only after evidence gates pass, revise active
   integration pointers/defaults and prepare the exact issue-freeze/migration
   procedure. Reconcile running work and dispatch sources before enabling the
   new default. Skill import, binding, default change and ticket migration are
   separate state changes and need the authorization and read-back required for
   their exact target.

The preparation review must inspect links and generated policy bundles, then
validate KB references/lint and workspace agent definitions in the isolated
copy. A clean document diff proves consumer consistency only; it is not runtime
or cutover proof.

## Unresolved configuration decisions

Do not fill these with guesses during consumer reconciliation:

- The initial trivial-work threshold and which validation/review gates apply.
- Model and effort selection mappings for the existing Linux, macOS and Windows
  execution profiles. Keep identifiers and mappings in `workspace-control`.
- PR merge method and the delivery ordering/partial-failure policy for several
  PRs. The spec authorizes design, not a live merge policy.
- Which already-running tasks or scheduled triggers can be adopted, drained or
  safely frozen, and the precise per-ticket migration operation. These need
  provider/system evidence and an explicit cutover procedure.

These choices block final desired-state generation and activation. They do not
block preparing the owner map or the bounded implementation work already
authorized.

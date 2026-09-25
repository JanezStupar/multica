# Cutover consumer map

This is preparation for the agreed Mica workflow, not an activation record.
The target policy is in [workflow_spec.md](workflow_spec.md). The implementation
and bounded Linux provider/cross-repository proof are complete for the released
candidate; this map identifies active consumers that still need coordinated
reconciliation before activation. No external KB or `workspace-control` file
was changed for this audit. At cutover, existing unfinished tickets will remain
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

The target workflow's core mechanics are implemented in `v0.5.1-janez.1` and
have bounded Linux provider evidence recorded in `runtime-proof.md`. The spec,
release and trials do not activate the Trackself workspace workflow; a policy
bundle or prepared consumer patch does not select a live ticket default.

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
| Multica default prompts, wakeups, schedules and already-enrolled issues — Multica code/configuration and live workspace state | Dispatch and cutover fencing are implemented and covered by local tests, but this candidate has not been activated against Trackself's live workspace or its current running tasks, schedules, comments, wakeups and retries. | Replacing a skill or editing an instruction alone does not prevent stale triggers, establish new-ticket defaults, or freeze old unfinished work. A fresh run ID does not by itself prove a fresh provider context. | Before activation, reconcile actual dispatch sources and running issues for the exact workspace. Route new tickets to the selected version and freeze old unfinished tickets pending explicit per-ticket migration. Preserve descriptions, results, sessions and traces. No live tickets are changed during this preparation. |

## Proof status and remaining activation dependencies

The implementation plan and [runtime proof](runtime-proof.md) record the
release identity, local checks and bounded provider evidence. The Linux trials
proved same-ticket context handling, independent review, rejection/rework,
acceptance, exact-head delivery and cross-repository ordered delivery on their
recorded source snapshots. They are not a production workflow canary and do not
activate a workspace default.

- The current archive/importer path has been exercised. After the consumer and
  production policy decisions are final, rebuild the archive and verify its
  exact identity and contents with the real importer before binding it.
- Ticket policy and execution-profile snapshots, scoped exceptions, retries
  and exact-candidate authority have local test coverage. The test policy was
  selected only in disposable workspaces; production role bindings and policy
  selection remain pending.
- The Linux provider and cross-repository behaviors above are proven within the
  trial boundaries. Native macOS and Windows dispatch, repository access and
  fresh/retained-context checks remain due when those hosts are deployed.
- Cutover fencing and legacy-issue migration have local implementation/test
  coverage. The live workspace default, dispatch sources, running work, and
  exact freeze/migration read-back must be reconciled before activation. At
  cutover, every mutation and dispatch path must honor the freeze; old
  unfinished issues remain frozen until an individual migration reconciles
  work, evidence, policy version and owner.
- Acceptance and delivery trials cover human acceptance, trivial autonomous
  acceptance, rejection/rework, readiness, exact-head merging and ordered
  multi-PR delivery. Production acceptance criteria and merge configuration
  remain separate policy decisions.

## Candidate patch sequence for an isolated KB/workspace copy

Prepare and review these changes in a disposable copy of the owning repositories;
do not apply them to the active KB or workspace configuration during preparation:

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

## Remaining policy and activation work

The [agreed production policy](workflow_spec.md#agreed-production-policy) now
owns classification, ordinary review, supervisor boundaries and squash merging.
Do not reopen those choices or fill the following with guesses:

- Implement and prove the revised `PR Ready`/`Done` and persistent hold behavior;
  current acceptance and delivery use `done`. The released trials do not prove
  these later requirements.
- Encode the agreed rules with concrete acceptor/supervisor identities, scopes
  and human acceptance roles. Select the default delivery after human acceptance.
- Model and effort selection mappings for the existing Linux, macOS and Windows
  execution profiles. Keep identifiers and mappings in `workspace-control`.
- Which already-running tasks or scheduled triggers can be adopted, drained or
  safely frozen, and the precise per-ticket migration operation. These need
  provider/system evidence and an explicit cutover procedure.

These choices block final desired-state generation and activation. They do not
invalidate the released implementation or completed Linux proof.

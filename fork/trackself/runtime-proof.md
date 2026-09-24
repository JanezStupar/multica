# Mica runtime proof in an isolated test workspace

This runbook also records the bounded Linux trial below. The local Go,
TypeScript, importer and disposable-database checks in the [implementation
plan](implementation_plan.md) do not prove provider session continuity, real
repository access, host routing or PR delivery. Continue the remaining plan
only with scoped authorization for the named test workspace, provider accounts,
disposable repositories, agents, runtimes and possible test PR merges.
Real-agent smoke execution is separate from ordinary tests:
[repository instructions](../../AGENTS.md#testing)
require the `agentintegration` tag, `MULTICA_RUN_REAL_AGENT_SMOKE=1`, and a
specific test name before any installed agent CLI is resolved. Do not turn on
a workspace default, run `workspace workflow cutover`, migrate a live issue,
change active Trackself bindings, or merge a non-disposable PR for this proof.

If a separately authorized agent integration test is part of the trial, use
the repository's exact gate with one chosen test name, not a broad tag run:

```sh
(cd server && MULTICA_RUN_REAL_AGENT_SMOKE=1 go test -tags=agentintegration ./pkg/agent -run '<test-name>' -count=1 -v)
```

## 2026-09-24 isolated Linux trial

This trial ran a detached test checkout built from base commit
`c052b3b5cb68a4c23929ad51697657fe67b1473b` **plus** the then-current
uncommitted implementation overlay. The pre-trial
source manifest recorded 262 overlay paths and had SHA-256
`d99f245f85c009cf6cdbb1f3442853262269305304e1809634166f34cb791820`.
The evidence applies to that snapshot, not bare HEAD or subsequent edits.
An isolated managed API/database, task workspaces and a Codex-only Linux daemon
served test workspace `fb4f9ef4-7df2-420b-ae3e-b45c543005d6`. The policy
bundle was imported and pinned to disposable issues in that workspace; no
production workspace default, Trackself binding or pre-existing issue was
changed.

The non-code issue `MICA-1` (`01a0d39e-64d2-74e0-b8e5-9bb7a6e41847`)
completed a read-only assessment of
`ssh://git@git.thn.janezstupar.com:2222/Janez/test.git`. Both checks observed
`HEAD -> refs/heads/main` at
`e92a70af6d44055f73b1c55ddfbb0461f9060851`; this was the remote's
advertisement at the time, not a checkout/content audit. Sol's
successful source task `01a0d3a4-a521-7204-96b9-e97252de9f8f` used provider
session `01a0d3a6-338d-7513-bc1d-130c62df1436`. Luna reviewed in a fresh
session `01a0d3a7-9573-71f3-aa11-efb758ce88dd`; the human then issued a
**simulated** in-scope rejection to exercise revalidation, not because the
recorded repository observation was false. The resumed Sol task
`01a0d3a9-9672-7a02-8641-51e27cfbb345` reused the same provider session
`01a0d3a6-338d-7513-bc1d-130c62df1436`. A second Luna review ran in
another fresh session `01a0d3aa-c9cb-7392-b0fe-c55abfd4abe0`, passed the
new non-code candidate with empty PR review URLs, and handed it to the human
owner. Human acceptance of candidate
`01a0d3aa-c871-71e2-a434-27e5640245cb` was recorded at
`2026-09-24T15:48:17+02:00`; the accepted state has no delivery actions. All
five recorded tasks retained policy version
`sha256:c2b9d9e8b4ee77d71bc7dc64d73e05ef8a6c964e415ad04c4be7e0bfaffe47b0`.
The task/session rows and final candidate, review and acceptance state were
read directly from the isolated database and API before cleanup. This proves the named
handoff, rejection, retained writer session, fresh reviews and human
acceptance on this Linux runtime. It does not establish PR delivery, autonomous
acceptance, multi-repository routing or native macOS/Windows behavior.

The first Sol attempt exposed a CLI path problem: the daemon prepended its
candidate binary directory to the agent environment, but a bare `multica`
command in the task resolved the user's global CLI. The host's shell startup
prepends that global directory, which is the likely PATH reorder; the final
task PATH was not captured, so this is a diagnosis rather than a proved shell
trace. The task updated that global CLI from custom `v0.4.27-janez.1` to
mainline `v0.5.3` without being asked. With the user's approval, the global
CLI was restored from the cached custom backend image, source commit
`3e6f23bc96505fe9f0fc1de7ad05bb53adbbcae1`, extracted SHA-256
`65780660b2d8635c7774a36696f9b8e17db9aac9665b60ac960fd7912f99191a`.
That release image has different build metadata from the overwritten binary;
byte identity is not claimed. Subsequent trial instructions name the isolated
candidate CLI by absolute path. The follow-up CLI fix now resolves the daemon
executable before each task, publishes it as `MULTICA_CLI_PATH`, and gives every
runtime brief shell-appropriate invocation forms. Missing executables fail task
startup; inherited or custom environment values cannot replace the selection.
A fake-CLI regression covers spaces and a shell that reorders PATH toward a
competing binary. Full daemon and execution-environment suites and independent
read-only review pass. Managed agents use the selected path; bare shell command
lookup itself is not changed.

The disposable exact-commit PR issue `MICA-2` created [draft PR #5](https://git.thn.janezstupar.com/Janez/test/pulls/5)
at `caab646f13edab828e18ae75165b18613167c8fa`. A fresh Luna task independently
checked its single 94-byte, mode-0644 text addition and published a
[commit-bound review](https://git.thn.janezstupar.com/Janez/test/pulls/5#issuecomment-340).
The live run exposed CLI rejection of anchored review URLs; the CLI now accepts
review anchors and rejects bare PR URLs. Focused `TestIssueWorkflow` tests pass,
and a separate read-only Sol review found no remaining defect in that fix.
This CLI correction is subsequent to the initial snapshot; its two-file
hash manifest had SHA-256
`45433d1a095266c17e2104fc7a4161cbc823ea768d32a0ddd3a672b4a24ff283`.
The first PR trial stopped before delivery because Forgejo's webhook allowlist
excluded the isolated API. PR #5 was closed unmerged, its branch and hook were
removed, and its managed environment was destroyed. The follow-up below
supersedes that delivery gap.

## 2026-09-24 live autonomous delivery proof

A second isolated Linux workspace (`2ff2d676-f199-4da0-b7d1-d56fdda4d168`)
used the same base commit with a 262-path overlay manifest SHA-256
`8f635862182f5127e8402f2a85bc2fd530429d2c2ddb58253a0198ce6f60e3cd`.
Two subsequent backend corrections were applied to that snapshot, with manifests
`a386e58e778b5792b374e062f4423a6116410000e69ba771121b07c6393b28d4`
and `6e985e237c6632a6db1f9f7c0fb00105d235a8ee138ed24353b259d5e5d259f5`.
These identities bind this proof to the uncommitted source, not bare HEAD.

With explicit user approval, Forgejo on `raspie-server` temporarily allowed
webhooks to `100.64.0.1:18336`. Real webhook delivery associated
[PR #6](https://git.thn.janezstupar.com/Janez/test/pulls/6) with test issue
`MICA-1` (`01a0d3ff-9b67-7c60-85eb-70e0e7313d1f`). Sol created the draft PR,
containing only the specified 94-byte text marker at exact head
`e6c5bea210ccb55ba2d9c12f2d2760e6927aa17e`. All four runs retained test policy
`sha256:2aa9f38e86eaa3ef6a039a98e8cf547441a3a63773f83d11bed5151f8f7e652a`.
The writer's provider session was `01a0d3ff-9d0e-7633-8a38-6657c4d29c39`;
the final fresh Luna review used `01a0d418-4336-7f31-9834-1b38b44db1f9`.
All runs completed successfully; session and policy rows were read from the
isolated database before cleanup.

Luna independently inspected the unchanged candidate and published the
[exact-commit review](https://git.thn.janezstupar.com/Janez/test/pulls/6#issuecomment-345),
then requested trivial acceptance from its running task. After that task
completed, acceptance `01a0d419-725d-7938-8e67-577ac49fcf33` became accepted
at `2026-09-24T17:47:20.151075+02:00` and the issue became `done`.
Delivery `01a0d419-902c-75cf-b095-b3067236eb9a` recorded readiness at
`17:47:20.239041+02:00`, merge at `17:47:20.428275+02:00`, and final status
`delivered`, guarded by the same exact head. Provider readback independently
confirmed the PR was no longer draft and was merged. Its squash commit is
`e746b90d6a037159715145470e30fe015689c6eb`. No human submitted acceptance or
performed the merge.

The live trial drove two now-tested corrections: an authorized running reviewer
may request pending acceptance, while finalization still requires its successful
completion; and the automatic completion comment preserves the pending request's
revision only when its exact task, candidate, author, owner and revision match.
Ordinary comments still advance the revision and invalidate stale acceptance.
Fresh reviews reran against the same candidate after each correction. Independent
read-only Sol review found no remaining defect in either fix. Full service and
handler suites passed against a separate disposable managed database, including
successful, failed/cancelled, manual-comment and intervening-change regressions.

Repository cleanup removed the marker with new commit
`bf91c97fc57422ee9202a821117d1d32d6afbe9d`, preserving PR and review history.
The final recursive tree entries match pretrial commit
`e92a70af6d44055f73b1c55ddfbb0461f9060851`. The test branch and webhook were
removed and verified absent. Forgejo's allowlist was restored to exactly
`external,multica.thn.janezstupar.com`; its rolling update completed with 1/1
replicas. Both disposable managed databases were dropped (zero matching
databases verified), and their API/daemon processes, profiles, task workspaces,
PostgreSQL containers, volumes, networks and detached checkouts were removed.
The four trial ports were checked free. Private temporary trial files were
removed; the user-owned provider token is retained. The restored global CLI's
SHA-256 still matches the value recorded above.

This proves single-repository Linux draft-PR handoff, fresh independent review,
agent-requested acceptance after successful completion, readiness and autonomous
exact-head merge. It does not prove cross-repository or native macOS/Windows
execution. The temporary policy is not a selected production default; cutover
and migration remain inactive.

## 2026-09-24 cross-repository and CLI selection proof

This trial used base `c052b3b5cb68a4c23929ad51697657fe67b1473b` with a
273-path uncommitted overlay, source-manifest SHA-256
`3b5ad34debde0a4b9db393a1ca2c811c1cc6729365dd98bca5f9e6914ecf654f`.
The final source subsequently clarifies two sentences about shell invocation
forms; their command forms and executable-selection behavior are unchanged.
The acceptance-help/input-error correction described below is subsequent to
this live snapshot and has separate unit evidence.

Isolated workspace `57b37e92-05c9-4c83-8d1e-afb6a90c79dc` ran one issue,
`MICA-1` (`01a0d470-3106-7958-b462-022478d01cd2`), with both repository
resources. Sol checked out both repositories through the daemon into separate
subdirectories of its task workspace and created two draft PRs. The writer
session was `01a0d470-91d9-7b12-8bd2-62344e511c11`; the final fresh Luna
session was `01a0d477-8369-7de3-9039-729982f85242`. All three runs completed
and retained policy
`sha256:76ea68a47f96043e4c3f6bc4fb7c3378dae645bf557aea50864e63757dcb60d9`.
Recorded task commands used `MULTICA_CLI_PATH` from the runtime brief; the
operator did not supply the CLI's absolute path in the objective. The global
CLI was unchanged.

| Repository | Exact reviewed head | Independent review | Merge order |
| --- | --- | --- | --- |
| [Janez/test PR 7](https://git.thn.janezstupar.com/Janez/test/pulls/7) | `830f3f816de4e14ca01b4ebb530d9b65e49f827c` | [Review](https://git.thn.janezstupar.com/Janez/test/pulls/7#issuecomment-353) | 0 |
| [Janez/test2 PR 1](https://git.thn.janezstupar.com/Janez/test2/pulls/1) | `b44f3548d9f1b3167e542e4bd10b93b1fb80cdda` | [Review](https://git.thn.janezstupar.com/Janez/test2/pulls/1#issuecomment-354) | 1 |

Each diff added only the specified non-executable text marker. One candidate
(`01a0d472-ee42-73b2-b384-79392456673e`) held both exact commits. Acceptance
`01a0d478-fcef-7244-8139-4e1cb512ea2e` was requested by the running final
reviewer, then accepted after its success at `19:31:42.995201+02:00`.
The ticket became `done`. Delivery recorded the first merge at
`19:31:43.329258+02:00` and the second at `19:31:44.052345+02:00`, each with
its expected-head guard and final `delivered` status. Provider readback
confirmed both PRs were ready and merged. No human performed acceptance or merge.

The first reviewer passed both commits but omitted `classification_reason`
from its acceptance request and received a generic permission error. A new
fresh reviewer evaluated the unchanged candidate and supplied the required
field. CLI help and policy guidance now describe the human/autonomous request
shapes. Authorized agents missing the reason receive a named input error;
authorization failures remain forbidden. Focused CLI and service unit tests
and independent read-only review pass for this correction. Its existing HTTP
400/403 mapping was inspected, not exercised in a new database-backed test.

Cleanup preserved both repositories and their PR history. Marker-removal commits
are `1519ce84af5af095378252d981ec555571737ece` in `test` and
`eb2947879a1fc52a2f7fb647bc91fa62b040c7a1` in `test2`. Final recursive tree
entries match their baselines (`bf91c97fc57422ee9202a821117d1d32d6afbe9d`
and `0c03a8476889783b3cd00f426741d3de496f79d8` respectively). `test2` was
initially empty and received that empty bootstrap commit solely to permit PRs.
Both trial branches and webhooks were removed and checked absent. The managed
test database was dropped (zero matching databases verified); its API, daemon,
profile, task workspaces and dedicated PostgreSQL container, volume and network
were removed. Forgejo's allowlist was restored to exactly
`external,multica.thn.janezstupar.com`, with completed update and 1/1 replicas.
The trial ports were free; its detached checkout and private temporary files
were removed. The user-owned provider token was retained.

This establishes live Linux cross-repository checkout, same-ticket handoff,
fresh review, exact-candidate autonomous acceptance and ordered multi-PR
delivery, as well as runtime-selected CLI use. Native macOS/Windows smoke checks
belong to deployment to those machines; they do not block Linux activation.
No production default, consumer binding, cutover or old-ticket migration changed.

## Inputs to record before execution

Fill a dated copy of this table. Values must come from the test workspace and
current provider, not from a production configuration or a guessed ID.

| Input | Required value and boundary |
| --- | --- |
| Server and workspace | Test `MULTICA_SERVER_URL`, workspace UUID, authenticated human owner/admin profile, and read-only database access for task/session evidence if available. |
| Policy | Reviewed archive path and `source-manifest.json` identity; imported workspace skill UUID. For the autonomous case, a separate test-only `runtime/policy.json` with a selected acceptor agent, actual triviality criteria, independent review required, merge method and explicit multi-PR ordering. |
| Actors and roles | Record the agent UUID used for each coordinator, implementor, review/fix, final review and autonomous acceptance task, plus the human member UUID. These are task roles, not five required permanent agent definitions: an agent or configured model may serve several roles when its fresh/retained provider context and authority remain correct. Record each run's runtime binding, model/effort, selected profile, skill replacement and permissions. |
| Machines | Linux, **native** macOS and **native** Windows host names, runtime UUIDs, provider versions, daemon/CLI builds and workspaces roots. Containerized Linux does not count as native Mac or Windows proof. |
| Repositories | Two disposable repositories, provider type, exact clone URLs, project resource/bound-workspace configuration, draft PR URLs, branches, full commit SHAs, branch protection/check rules and test merge permissions. Include one cross-repository ticket and a separate small trivial ticket. |
| Isolation | Named test issues, branch prefixes and cleanup owner; no existing live issue or shared writable checkout. An issue lock prevents simultaneous writers on that issue, not unrelated issues using the same filesystem path. |

Record the intended test policy and its explicit authority before importing it.
The repository's unconfigured bundle requires review and leaves autonomous
acceptance/merge disabled. The [policy template](README.md#candidate-review-and-acceptance)
is an example, not a chosen Trackself threshold or merge policy.

## Prepare and enroll only test issues

Use an isolated CLI profile on each host. These commands are templates and
must not be run until the values above and external-state authorization are
filled in. `multica` reads `MULTICA_SERVER_URL` and `MULTICA_WORKSPACE_ID`;
`--profile` isolates the stored login and daemon state. Build/import/pin are
distinct actions; the import and pin below mutate only the named test
workspace and fresh, task-free test issues.

```sh
export MULTICA_SERVER_URL='<test-server-url>'
export MULTICA_WORKSPACE_ID='<test-workspace-uuid>'
multica --profile mica-proof auth status
multica --profile mica-proof runtime list --output json
multica --profile mica-proof repo list --output json
multica --profile mica-proof workspace workflow get "$MULTICA_WORKSPACE_ID"
python3 fork/trackself/build_skill.py --policy-dir '<reviewed-test-policy-source>' --output './mica-proof.skill'
multica --profile mica-proof skill import --file ./mica-proof.skill --on-conflict fail --output json
```

Read back the imported skill UUID and manifest identity. Create the test issue
**without an assignee or other trigger**, then pin before any task history.
Use the same sequence for the separate trivial ticket. Do not call cutover or
set-default to make an issue eligible.

```sh
multica --profile mica-proof issue create --title 'Mica proof: two-repository change' --description-file ./proof-objective.md --output json
multica --profile mica-proof issue workflow-policy pin '<test-issue-uuid>' --skill-id '<imported-skill-uuid>'
multica --profile mica-proof issue workflow-policy get '<test-issue-uuid>'
multica --profile mica-proof issue assign '<test-issue-uuid>' --to-id '<coordinator-agent-uuid>'
multica --profile mica-proof issue runs '<test-issue-uuid>' --output json
```

The objective file must name the two repository URLs, independent validation,
draft PR expectations and exact work boundary. Inspect issue history and the
first claim to confirm the pinned `workflow_policy_version` and
`workflow_profile_id`. The same policy/profile must remain attached to retries;
changing the workspace default must not reinterpret this issue. A test-only
profile reselection is a separate recorded action, not an implicit fallback.

## Provider and handoff trace

For each row below, record issue ID, outgoing task ID, handoff ID and
`request_key`, recipient task ID (if any), agent/runtime/host, status,
`workflow_policy_version`, `workflow_profile_id`, declared PR heads,
actual provider session ID, prior provider session ID, workdir, start/end
times, and the relevant daemon/provider trace. `issue runs` reports task IDs;
task IDs alone do not prove distinct or retained provider conversations.
Where a scoped read-only DB connection is available, this query supplies the
task side of that comparison; compare it with claim `prior_session` and the
provider's own transcript/session record. Keep provider credentials and task
tokens out of the evidence, and restrict raw session IDs and absolute paths to
the test record.

```sql
SELECT id, issue_id, agent_id, runtime_id, status, retry_of_task_id,
       rerun_of_task_id, force_fresh_session, workflow_policy_version,
       workflow_profile_id, session_id, work_dir, started_at, completed_at,
       context->'workflow_handoff' AS handoff,
       context->'workflow_recovery' AS recovery
FROM agent_task_queue
WHERE issue_id = '<test-issue-uuid>'::uuid
ORDER BY created_at, id;
```

Use a new UUID `request_key` for each new intent and preserve the **identical**
key and JSON for an ambiguous retry. The file below is an agent recipient
template; replace every placeholder from the actual completed or running
source, test agents and provider PRs. Full SHAs and `draft: true` are required.
The handoff stores declared candidate refs; actual provider heads and review
URLs must be verified separately.

```json
{
  "request_key": "<new-request-uuid>",
  "outgoing_task_id": "<current-outgoing-task-uuid>",
  "assignee_type": "agent",
  "assignee_id": "<reviewer-agent-uuid>",
  "status": "in_review",
  "context_mode": "fresh",
  "candidates": [
    {"repository_url":"https://<test-host>/<org>/repo-a","pr_url":"https://<test-host>/<org>/repo-a/pulls/<n>","branch":"<branch-a>","commit_sha":"<full-sha-a>","draft":true},
    {"repository_url":"https://<test-host>/<org>/repo-b","pr_url":"https://<test-host>/<org>/repo-b/pulls/<n>","branch":"<branch-b>","commit_sha":"<full-sha-b>","draft":true}
  ],
  "evidence_urls": [],
  "instruction": "Review the stated objective and both exact PR heads independently. Publish findings in the PR reviews."
}
```

```sh
multica --profile mica-proof issue handoff create '<test-issue-uuid>' --file ./handoff.json
multica --profile mica-proof issue handoff list '<test-issue-uuid>'
multica --profile mica-proof issue runs '<test-issue-uuid>' --output json
multica --profile mica-proof issue workflow get '<test-issue-uuid>'
```

Exercise these transitions with separate real provider turns on the same
issue. Capture the claim and terminal trace at each transition.

| Scenario from the [spec](workflow_spec.md#acceptance-criteria) | Required observation |
| --- | --- |
| Direct job and multi-turn writer | Coordinator chooses a configured implementor without a human-authored handoff. An interrupted writer continues the identified source session when available, or records the continuity gap and reconstructs from current repo/ticket state. Before its next write, it checks intervening commits, instructions and overrides. |
| First review/fix | Implementor produces two draft PRs at the declared exact commits and creates a handoff while its task is still running. Recipient does not run before source success. The reviewer claim has a different provider session from implementation, reads both actual PR surfaces and publishes provider review evidence. |
| Independent final and retained fix | A different final **review context** gets another fresh provider session and no prior verdict as coaching; it may use the same agent definition if policy allows. Have it report a seeded in-scope defect. Return to the earlier review/fix context using `context_mode: "resume"` and `resume_task_id` set to its **terminal** review/fix task. Its claim must select that exact source session/workdir when available, or identify a continuity gap. After the fix changes a SHA, register a new candidate and request another fresh final pass. |
| Same-agent fresh handoff | On a separate disposable issue, route an implementation or review handoff back to the **same** agent UUID with `context_mode: "fresh"`. Verify the recipient task has a new provider session and the exact candidate, while its old context and verdict are not inherited as instructions. Agent identity alone is neither freshness nor review independence. |
| Non-code handoff | On a separate small test issue, use `"candidates": []` and a fresh reviewer with `"pr_review_urls": []`. Verify an independent review can be recorded without inventing a PR or delivery merge. |
| Recovery and idempotency | Repeat one create with the same key after an ambiguous response; list shows one durable intent and one recipient. Fail an exhausted recipient in the disposable case: native recovery enqueues the outgoing coordinator's exact retained source, preserves failure evidence, and permits a new handoff. A changed source candidate requires a new key; no old declared SHA is silently reanchored. Check no overlapping mutable issue writers. |
| Cross-repository workspace | The same ticket and candidate enumerate both PRs. On each host, prove the task received the intended runtime and project/bound workspace, checked out both repositories at the declared branches, and used isolated writable paths. Inspect each actual PR head immediately before review and delivery. |
| Human recipient | Final reviewer hands off to `assignee_type: "member"`, `assignee_id: "<human-member-uuid>"`, `status: "in_review"`, `context_mode: "fresh"`. `handoff_completed_at` appears after outgoing success, `last_task_id` remains null, no human-agent task is enqueued, and unrelated queued agents cannot overwrite the waiting phase. |

Also record the spec's surrounding decisions rather than treating a successful
handoff as proof of the whole workflow:

| Scenario | Required observation |
| --- | --- |
| Clarification and decomposition | A recoverable missing detail is resolved by Mica; a consequential unresolved decision is surfaced to the right human without stopping independent work. One issue can complete without mandatory children, while a genuinely useful delegated child is reconciled into its parent. |
| Capability choice and policy override | A simple and a demanding test issue use their configured model/effort profiles. An explicit, authorized profile reselection changes later runs while existing history keeps its identity. A scoped policy override or supervisor exception changes only its recorded candidate/scope and does not rewrite another issue or the default. |
| Classification and revision changes | A ticket that no longer meets the chosen trivial criterion returns to human acceptance. A new commit after review or acceptance creates/identifies a changed candidate and blocks stale review or delivery authority. |
| Policy continuity and durable context | Pinned issues and retries keep their recorded version when the source skill or agent replacement changes. A new-ticket default and legacy migration are checked only in a separately authorized rehearsal after provider proof. Repository-owned outcome, decisions and limitations remain available when a provider conversation is lost. |

For the capability-change case, wait until that issue/agent's outstanding
tasks settle, change the selected agent settings only in the test workspace,
and have a human owner/admin submit the exact current profile ID and a new
request key. Read back the new profile before another run; the earlier task
and its retries must retain their former ID.

```json
{"agent_id":"<test-agent-uuid>","expected_profile_id":"<current-profile-uuid>","request_key":"<new-request-uuid>","reason":"<specific capability reassessment>","consequences":"<effect on subsequent work>","reconciliation":"<outstanding work and evidence reviewed>"}
```

```sh
multica --profile mica-proof issue workflow-profile reselect '<test-issue-uuid>' --file ./profile-reselect.json
```

Inside each authorized repository task, record the actual checkout locations
returned by the runtime or bound-workspace configuration, then inspect both
repositories without resetting them. These are read-only evidence commands;
replace the checkout placeholders with paths from that task, not another
host's path:

```sh
pwd
git -C '<repo-a-checkout>' rev-parse --show-toplevel HEAD
git -C '<repo-a-checkout>' status --short
git -C '<repo-b-checkout>' rev-parse --show-toplevel HEAD
git -C '<repo-b-checkout>' status --short
```

The member handoff uses the same JSON shape but has no `agent_id` or
`resume_task_id`. `candidates` may be `[]` for a non-code ticket. For a retained
agent handoff, change only `context_mode` to `resume`, add the exact
`resume_task_id`, and use the intended agent's ID. Each changed intent gets a
new request key; inspect `handoff list` before cancelling a blocked intent
with `issue handoff cancel <issue-id> --handoff-id <id>`.

## Review, acceptance and delivery evidence

While a fresh reviewer task is running, publish actual provider PR reviews on
the exact current commits and register their URLs. `workflow get` provides the
canonical candidate ID, issue revision, acceptance blockers, delivery preview
and retained context choices. The review command must run as the intended
reviewer task; a human CLI session cannot impersonate it.

```json
{"candidate_id":"<candidate-uuid-from-workflow-get>","verdict":"pass","pr_review_urls":["<published-provider-review-url-a>","<published-provider-review-url-b>"]}
```

Use provider-recognized HTTPS permalinks to submitted reviews, including
canonical review anchors where the provider uses them. Bare PR URLs are not
review evidence. Run the write command below
*inside the active reviewer task*, using its injected task token. Do not
substitute the human proof profile for that token.

```sh
multica --profile mica-proof issue workflow get '<test-issue-uuid>'
multica issue workflow review '<test-issue-uuid>' --file ./review.json
```

For the nontrivial human path, read `candidate.id` and `issue_revision` again
immediately before accepting. Use a human member profile with authority for
that issue. The requested delivery action comes from the pinned test policy;
the human must see the exact candidate and action preview before this command.

```json
{"candidate_id":"<current-candidate-uuid>","expected_revision":<current-issue-revision>,"merge_order_pr_urls":["<repo-a-pr-url>","<repo-b-pr-url>"]}
```

```sh
multica --profile mica-proof issue workflow accept '<test-issue-uuid>' --file ./accept.json
multica --profile mica-proof issue workflow get '<test-issue-uuid>'
```

For an in-scope rejection, use the same current candidate/revision and a
`resume_task_id` drawn from `retained_context_options` returned by `get`.
Verify the returned task uses that exact source or records why continuity was
unavailable; the rejection invalidates affected acceptance/delivery authority.
For an out-of-scope change, use `kind: "scope_change"` and verify it remains
distinguishable from a defect. Example request:

```json
{"candidate_id":"<current-candidate-uuid>","expected_revision":<current-issue-revision>,"kind":"in_scope_defect","reason":"<specific seeded defect>","resume_task_id":"<offered-retained-task-uuid>"}
```

```sh
multica --profile mica-proof issue workflow reject '<test-issue-uuid>' --file ./reject.json
```

For autonomous trivial acceptance, use a **separate** small disposable issue
pinned to the explicitly configured test policy. A human operator does not
submit the agent request: the authorized acceptor does so from its running,
profile-bound task after the required independent provider review. Record a
concrete classification reason. Its request remains `requested` until that
exact task succeeds; only then may the finalizer mark `accepted`, close the
issue and create exact-head delivery actions. Use the selected merge method
and explicit order for two PRs; use an empty order only for a single PR or a
policy that does not require multi-PR ordering.

```json
{"candidate_id":"<trivial-candidate-uuid>","expected_revision":<current-issue-revision>,"classification_reason":"<policy criterion and concrete evidence>","merge_order_pr_urls":["<first-disposable-pr-url>","<second-disposable-pr-url>"]}
```

The authorized acceptor runs `multica issue workflow accept
'<trivial-issue-uuid>' --file ./trivial-accept.json` with its task token while
that task is running; the human proof profile must not submit this request.

Check `workflow get` and provider state before and after the finalizer. A
temporary provider failure must keep acceptance visible, mark delivery
pending/failed with a retry state, and retry the same exact head without a
duplicate merge. Move a disposable PR head after acceptance and confirm the
stale authority does **not** merge it. Readiness, branch protections and
published review binding must be checked against the provider, not inferred
from a cached webhook or the handoff JSON.

After repairing a deliberately blocked disposable delivery, read the current
candidate and revision again. Retry only that delivery ID; an ambiguous
repeat of the same request must not create a second action. The API is
`POST /api/issues/{id}/workflow/delivery/{deliveryID}/retry` and returns the
full workflow state. Stale, delivered, cancelled or wrong-candidate requests
must conflict.

```json
{"candidate_id":"<current-accepted-candidate-uuid>","expected_revision":<current-issue-revision>,"reason":"<specific repaired provider condition>"}
```

```sh
multica --profile mica-proof issue workflow delivery-retry '<trivial-issue-uuid>' '<delivery-uuid>' --file ./delivery-retry.json
multica --profile mica-proof issue workflow get '<trivial-issue-uuid>'
```

## Machine and cutover gate

The initial activation targets Linux. When deploying to native macOS or Windows,
run a targeted direct job plus one fresh/retained handoff on that host; do not
repeat server-side acceptance and delivery tests solely for a different OS.
These host checks do not block Linux activation. On each tested host record `multica --profile
mica-proof daemon status`, `runtime list --output json`, claimed runtime ID,
provider build, session IDs and bound workspace paths. Use the same test
server/workspace but host-specific isolated workspaces; inspect repository
path mapping for both repos. Exercise a lost-session or daemon interruption
on one host and verify recovery without assuming a new task ID means a new
provider context. No platform passes solely because a mock or a Linux
container passed.

Mark each [observable scenario](workflow_spec.md#acceptance-criteria) with
its issue/run IDs, provider trace/PR links, expected and actual result, and
unresolved gaps. A clean compiler/local test run is a prerequisite, not this
proof. The proposed Linux default/cutover remains off until real provider and
cross-repository evidence, independent acceptance, explicit policy choices and the
[consumer map](cutover-consumers.md) are all ready. A later activation needs
its own exact workspace, command, affected issue and rollback review; this
runbook does not authorize it.

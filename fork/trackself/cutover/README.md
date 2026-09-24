# Candidate canonical KB patches

These unified diffs are review material for a pending workflow cutover. They
were generated from the recorded canonical KB worktree files below; they are
not a reconstructed archive or a statement that the Mica workflow is active.
No file in the canonical KB or `workspace-control` repository was changed.

The patches propose a provider-neutral automation contract, a narrower pointer
from the generic review/fix skill, a shorter default issue-objective form, and
the matching amendment to ADR 0017.
They keep scoped user exceptions, explicit capability boundaries, retained
review/fix work, and independent fresh read-only final review when policy
requires it. Subtasks remain available for real coordination needs; the
candidate removes a universal child-per-slice, role, review, or verdict rule.
The patches do not copy Mica roles, model mappings, or Multica commands into
canonical KB policy.

The companion [`workspace-control.patch`](workspace-control.patch) and
[`workspace-control.manifest.md`](workspace-control.manifest.md) are owned by a
separate cutover consumer. Review them with this policy set, but do not infer a
workspace import or active skill binding from their presence. The manifest
marks the content-derived skill name as a placeholder until the final policy
bundle is rebuilt; no archive or imported workspace-skill UUID is included
here. Workflow-policy identity and mutable agent instructions or model settings
are separate runtime facts: the per-ticket policy snapshot remains fixed absent
explicit migration or scoped override, while each agent's separately captured execution profile retains its selected
instructions and model settings until explicit reselection. These KB candidates
do not select those profiles or duplicate their configuration.

## Source baseline

The source consumer map and workflow spec were read from Multica revision
`c052b3b5cb68a4c23929ad51697657fe67b1473b`; both are untracked worktree inputs,
so their content hashes identify the exact versions used:

| Input | SHA-256 |
| --- | --- |
| `fork/trackself/cutover-consumers.md` | `03ca3a1d7807b363555586d026444451a3a3e8ce746c594067be50de60067287` |
| `fork/trackself/workflow_spec.md` | `93b164982afbd3b5a70908a3363f9e6cf715bbd094f76f9b67a2caaf42bd1c23` |

The owning KB worktree was at `2c31b9c475def3718bdd81e8f61485f01124f476`.
The four patch targets were clean relative to that revision and matched these
worktree hashes:

| Target | SHA-256 |
| --- | --- |
| `docs/workflow-automation.md` | `8d3ed88ed376456db48e1943433e17572930aeece7f6e7b7c232579019c26060` |
| `skills/review-fix-cycle/SKILL.md` | `31f84000c329b7fc079830c0e77b14ee39a6e3d3b70cd4fe5968c57fe7ec9b71` |
| `templates/operational-issue-objective_template.md` | `b17ad095e55621b736e24c037071d60af2c2ae9810e132585c792dfddefa33c0` |
| `decisions/0017-progressive-workflow-automation.md` | `7ece1d4f3ef5bab9a269cfd81b2f5ba8294ab1d53f59aebd61d3247f097aa7f2` |

The KB worktree also had unrelated dirty files: `README.md`,
`decisions/0013-lean-capability-aware-agent-protocols.md`,
`skills/agent-routing/SKILL.md`, and `skills/using-kb-skills/SKILL.md`.
The current dirty KB README hash was
`beeecd1e073c1ef2d38182296c8683370b0ae8e350af4aacfb5bf9e542b2e7c0`; it was
read to preserve its current exception language and is deliberately not part of
these patches. Reconcile that working copy rather than replacing it from HEAD.
Other excluded KB working-copy inputs were also dirty at that revision:

| File | SHA-256 |
| --- | --- |
| `decisions/0013-lean-capability-aware-agent-protocols.md` | `c3940ee93e2117e351cb0b07ac2a56cc85c1565a1f73b3d50c8a7d7073abaefb` |
| `skills/agent-routing/SKILL.md` | `8d50fd22b8fa3c63771e586b73426c6ac6db87e1a642922dd364f3a5fd8ed444` |
| `skills/using-kb-skills/SKILL.md` | `1813767785f97cea2c5b50115efd36fa66a4ea0926f04180d37199b04fc87d50` |

The workspace-control context was read at revision
`6871897ce8c95126955f263127ae441bcf0276b4`. Its working tree was dirty in the
files below; all remain outside this KB patch set:

| File | SHA-256 |
| --- | --- |
| `.codex/agents/acceptance-reviewer.toml` | `a45f019d2a657739dc2299b32a978c26318667cd74ffa0da66c6c575c4fcaf18` |
| `.codex/agents/bounded-worker.toml` | `e18be5cb99fa49c07d8e825afb28610518e67e6e503225392650236a3cf2f622` |
| `.codex/agents/fast-worker.toml` | `51f496426853e6ee63a93b0f216311ce083dd7b86bbce891f326c4029416d1c3` |
| `.codex/agents/researcher.toml` | `92b55913e17088f1b0a8d8268f6a50466abed3ef950170b159f4c7a05819dc70` |
| `.codex/config.toml` | `e898a22c9fc8670bd28c6be6eff08d34820c54567d41392a411d5a2fb3d8ee1f` |
| `config/multica-agents.toml` | `b75481f86d8efab1fac6f071b7a53dac74b116ff3019393d341526a4753806e5` |
| `config/primary-instructions.md` | `f098b15d55ed10d375d4c1a345f0ac715a1807ac4a5c7c3dd4400dbc6fb7ea5c` |
| `docs/agent-routing.md` | `be26b5cdafe47cd45a7b930287b09875bc0c798ca43f8df96701aa82a20bf1cf` |
| `docs/multica-workflow.md` | `6bd49c8c53231db1fedc9eecf0907c2623282d401e0d71caba96209bbedab286` |
| `scripts/test_trackself_agent_definitions.py` | `5851b8c891da7955eda37f1997b9f6f9883eabd1d57f2528f0ea2dc38d57b970` |

The KB's accepted ADR 0017 had SHA-256
`7ece1d4f3ef5bab9a269cfd81b2f5ba8294ab1d53f59aebd61d3247f097aa7f2` and was
not changed. The ADR amendment patch is a candidate only.

## Paired decision and document change

Accepted ADR 0017 (`decisions/0017-progressive-workflow-automation.md` in the
owning KB) currently makes the
automatic parent/child stage graph normative, including a child for each
materially distinct slice, a reusable review/fix child, and a fresh acceptance
child per verdict. The candidate workflow document makes that graph an explicit
integration choice. Apply and review `kb-adr-0017.patch` together with the
workflow document patch so the accepted decision and its consumer stay aligned.
The ADR patch preserves the historical staged model as an available pattern
while removing it as a universal requirement. The user has authorized this
protocol redesign; these are still candidate artifacts, and preparing or
applying them in a disposable copy does not activate Mica or change live
instructions.

## Check and review in a disposable KB copy

Before using these candidates, verify the source revision and target hashes
above. Then create a disposable copy or worktree of the owning KB at the
recorded revision and run `git apply --check` for each patch from that copy's
root. Read each full diff, reconcile incoming references and any approved ADR
change, then apply only the approved files in that disposable copy. Run the KB
maintenance/reference checks and lint there, and inspect meaning as well as
link validity. Keep the active KB and workspace-control configuration
unchanged until the owner separately approves their cutover.

For example, with `KB_COPY` pointing at a disposable checkout and `PATCH_DIR`
pointing at this directory:

```sh
git -C "$KB_COPY" rev-parse HEAD
sha256sum \
  "$KB_COPY/docs/workflow-automation.md" \
  "$KB_COPY/skills/review-fix-cycle/SKILL.md" \
  "$KB_COPY/templates/operational-issue-objective_template.md" \
  "$KB_COPY/decisions/0017-progressive-workflow-automation.md"
git -C "$KB_COPY" apply --check "$PATCH_DIR/kb-adr-0017.patch"
git -C "$KB_COPY" apply --check "$PATCH_DIR/kb-workflow-automation.patch"
git -C "$KB_COPY" apply --check "$PATCH_DIR/kb-review-fix-cycle.patch"
git -C "$KB_COPY" apply --check "$PATCH_DIR/kb-operational-objective-template.patch"
```

`git apply --check` validates only that a patch fits the selected source; it is
not semantic acceptance, deployment proof, or workflow activation.

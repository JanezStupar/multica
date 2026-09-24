# Workspace-control candidate manifest

Status: isolated review artifact only. This patch does not change the active workspace-control checkout, import or bind a skill, select a workspace default, activate the integration, migrate tickets, or mutate live Multica state.

## Baseline

- Owner: `/home/janez/workspaces/trackself/workspace-control`.
- Owning repository `HEAD`: `6871897ce8c95126955f263127ae441bcf0276b4` (read 2026-09-24).
- Baseline is the owner working tree as copied into `/tmp/mica-workspace-control/baseline`; a temporary local Git snapshot identifies its complete file baseline as `3b0edce282f5f2ca8dd7d427b6eef710f8de5459`.
- The owner working tree was already dirty before this candidate: `.codex/agents/{acceptance-reviewer,bounded-worker,fast-worker,researcher}.toml`, `.codex/config.toml`, `config/multica-agents.toml`, `config/primary-instructions.md`, `docs/agent-routing.md`, `docs/multica-workflow.md`, and `scripts/test_trackself_agent_definitions.py`. The patch was authored against their exact copied contents; none of those pre-existing edits was discarded.
- Candidate changes only the files listed in the patch. Current profile, lane, project-routing and root-instruction files not in the patch remain byte-identical to the copied baseline.

## Version and identity dependencies

- Intended source policy version: Multica `fork/trackself/policies/mica-v1`.
- Use `trackself-platform-<final-content-derived-hash>` as a placeholder until the final policy bundle is rebuilt after workflow/delivery changes. The archive identity observed during preparation may be stale and must not be used for binding.
- After the final archive passes the real Multica importer, replace the placeholder with its exact `skill_name` from `source-manifest.json`. Bind using the actual workspace-skill UUID returned by that import; do not guess, reuse the old Trackself skill UUID, or put a placeholder UUID in desired-state TOML.
- A separate coordinated TOML candidate must bind the imported Mica skill to the Trackself agent profiles that need it. Preserve existing IDs, runtime IDs, model/effort selectors, permission modes, custom environments, lane routing, built-in skill policy, and Linux/macOS/Windows coverage. No TOML binding change is included here.
- The policy/default activation, skill import/binding, cutover fence, freeze, per-ticket migration, and live checks remain pending until the agreed implementation and provider/platform proof gates pass.

## Replacement intent

The candidate replaces mandatory child-per-slice, separate review/acceptance child, child-terminal wake, and child-status completion instructions for tickets that are later explicitly pinned to Mica v1. It points lifecycle decisions to the selected policy rather than duplicating its full contract. It retains Multica-owned project/workspace checks, environment and role routing, queue mechanics, managed-PR exact-head transport, platform-specific QA/release safety, external-state restrictions, and user authority. Subtasks remain available when independently useful. PR reviews own technical findings and exact-commit review evidence; useful writer/review-fix context is retained and final review is fresh. Existing unrelated integrations and legacy-pinned behavior are outside the Mica policy scope.

## Changed-file baseline SHA-256

| Owner-relative path | SHA-256 before patch |
| --- | --- |
| `config/multica-agent-instructions/acceptance-reviewer-workflow.md` | `906bbd0f5d5024f9dea3d248ec251418654771d0a5d72456c9e22c8309ce8033` |
| `config/multica-agent-instructions/acceptance-reviewer.md` | `e6e30b5d781b9c7edaae0e4b27c87c57dded38b36e87be6ab0b9408fa856a7bc` |
| `config/multica-agent-instructions/implementor-workflow.md` | `2510be839b1fc87cd0b24e4c1b0368468edaa2f803e581e49d9b1ac9e6211a57` |
| `config/multica-agent-instructions/local-directory-preflight.md` | `ed31c611cc5855ec72a7b62a9fe3345b415d82d45c0aa07759386217da5906d9` |
| `config/multica-agent-instructions/maca.md` | `69ecd3269f1636d65759d2d4997bae285333eda6edb3f174af0f267fe70a0ce9` |
| `config/multica-agent-instructions/macos-acceptance-reviewer.md` | `77c04c2b94832489cfa782dbca9f2898a3ed759d4e8dd94a37c6b98e11ab75d8` |
| `config/multica-agent-instructions/macos-qa.md` | `0f7b09545350eb9853594d0c1de40c833dd99c3565e3c5278ec62e4f68d234ea` |
| `config/multica-agent-instructions/macos-release-builder.md` | `5998ae8cca163f4eabaa555d816688d7e6f1fdec3aa9de56b0ebed647a7b0186` |
| `config/multica-agent-instructions/mewina.md` | `ec3b4d04765e09cdfc829656ab3c08ffad3509a4dff06ee580be48cd2624ab8e` |
| `config/multica-agent-instructions/mika.md` | `a17360e3320cb4e6d1a375f1f6b2e924911ad28889f444d62ee2ce751fdb5cdb` |
| `config/multica-agent-instructions/orchestrator-workflow.md` | `632e0509127e0e11972592fca10cde920dee1319ec370235594e86a3896d4155` |
| `config/multica-agent-instructions/review-controller-workflow.md` | `523da322af674f9cff5a9ef7da8d9a8db9340b5a7aed7f60e5dd8aa1595921ed` |
| `config/multica-agent-instructions/task-context.md` | `92324d005302b5fff49cf72ae8662a46549b3988a17ba19ecb8a7a61e387bd76` |
| `config/multica-agent-instructions/windows-acceptance-reviewer.md` | `f91d9ff2f42f91040c411af2441b4845eb3f74b22309c001c21c015d6eef8d5a` |
| `config/multica-agent-instructions/windows-qa.md` | `9c14f25683b16c6614e0624a4333c2a088b7e380dcc99a26e37571d9397a34df` |
| `config/multica-agent-instructions/windows-release-builder.md` | `f8dd65da28f12fa9801f07f370712c1a6983e06376352687f4db343012642526` |
| `config/multica-skills/trackself-working-on-issues/SKILL.md` | `595478565b3f8f163a214e5fd6fafeef67c967132dc4c23b30a44752904422de` |
| `config/multica-skills/trackself-working-on-issues/references/continuation.md` | `02dfc2bae63d603eca150cd023d857e92aff51c9fcccc63b603740e14a34e02d` |
| `config/multica-skills/trackself-working-on-issues/references/platform-side-effects.md` | `19d960c6c89289ccaf483bf901a831433f0b517126df9230f1ca0d6cf150cf8c` |
| `config/multica-skills/trackself-working-on-issues/references/pull-requests.md` | `458f7ddadf54dd8c55a292c1db415687c79ff332fd8549d1b52ea677cf20c222` |
| `config/primary-instructions.md` | `f098b15d55ed10d375d4c1a345f0ac715a1807ac4a5c7c3dd4400dbc6fb7ea5c` |
| `docs/multica-workflow.md` | `6bd49c8c53231db1fedc9eecf0907c2623282d401e0d71caba96209bbedab286` |
| `scripts/test_trackself_agent_definitions.py` | `5851b8c891da7955eda37f1997b9f6f9883eabd1d57f2528f0ea2dc38d57b970` |

## Protected owner files (not changed)

| Owner-relative path | SHA-256 in candidate baseline |
| --- | --- |
| `config/multica-agents.toml` | `b75481f86d8efab1fac6f071b7a53dac74b116ff3019393d341526a4753806e5` |
| `config/multica-lanes.toml` | `c78994c8388b34296857f88fac4e425b55171b28498b0d1b1341a296a349000e` |
| `project-workflow.toml` | `871da131039a17e7b8ea5212e0561d2cb18ce97ac187f43826dcf4202172b39a` |
| `.codex/config.toml` | `e898a22c9fc8670bd28c6be6eff08d34820c54567d41392a411d5a2fb3d8ee1f` |
| `AGENTS.md` | `0a4eb203673bce6d084eb9570cbd016e1df25f10501e4580ef1a4b9f4acdc527` |
| `docs/agent-routing.md` | `be26b5cdafe47cd45a7b930287b09875bc0c798ca43f8df96701aa82a20bf1cf` |

## Patch artifact

- File: `fork/trackself/cutover/workspace-control.patch`
- SHA-256: `104d8e10020ef026b0b30c56cbb004961d8b5e9598b4f315205e983619f98985`
- Changed files: 23.

## Validation

- `python3 scripts/test_trackself_agent_definitions.py` in the isolated candidate copy: 57 tests passed. The tests now assert policy-pinned lifecycle, exact-candidate evidence, retained review/fix plus fresh final review, managed-PR exact-head transport, and preservation of current configured role routing.
- `git diff --check` in the isolated candidate copy: passed.
- `git apply --check` and patch application against a fresh copy of the recorded baseline: passed.
- No desired-state live apply, imported-skill binding check, provider workflow, runtime proof, ticket mutation, or cross-platform execution was performed; those are pending proof/cutover dependencies, not implied by this document patch.

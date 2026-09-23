# Trackself Multica fork: built-in skill replacement

This fork lets each agent inherit, disable, or replace a complete Multica
built-in skill bundle at task claim time. The Trackself configuration replaces
the consolidated `multica-platform` bundle for parent orchestrators while
keeping Multica's eight platform references available on demand. Other agents
can retain the embedded bundle or choose another workspace replacement.

## How selection works

An agent stores two independent settings:

- `enabled_builtin_skill_ids: null` inherits the built-ins available to that
  agent. An explicit list is exact, and `[]` disables every built-in.
- `builtin_skill_replacements` maps a stable built-in ID, such as
  `builtin:multica-platform`, to a workspace skill UUID. The slot must be
  enabled. Only that agent receives the replacement. The embedded bundle is
  omitted, and a replacement that is also assigned as a regular workspace
  skill is included just once.

The workspace replacement must contain its own `SKILL.md` and every supporting
file path of the built-in it replaces. A `multica-platform` replacement also
requires `runtime/issue-workflow.md`, a short workflow section rendered into
the generated runtime brief. Other reference files remain available for
selective reading. A missing, deleted, cross-workspace, or incomplete bundle
blocks a claim; it never silently restores the embedded behavior. The server
also checks selection on skill resolution, so a disabled or replaced built-in
cannot be fetched directly by ID.

The generated brief keeps Multica's command and safety instructions. For an
issue task using a platform replacement, its issue workflow comes from that
replacement's `runtime/issue-workflow.md`. The per-turn prompt gives the issue
and trigger facts and points to the selected skill. This avoids the generic
issue read, comment, and status rules contradicting the Trackself procedure.

## Database compatibility

Migration `536_agent_builtin_skill_policy` uses `ADD COLUMN IF NOT EXISTS`
for `enabled_builtin_skill_ids`. Personal-fork databases may already have that
column from migration 327 or 451; their stored `NULL`, exact lists, and empty
lists are preserved. The migration adds `builtin_skill_replacements` as a
JSONB object with an empty default and a task-level platform bundle fingerprint.
Its down migration removes those two new columns; it preserves the older
allowlist because dropping it would destroy existing configuration. The
migration runner records full filename stems, so the new
stem is distinct from both earlier fork migrations and main's unrelated 327.

Existing exact allowlists with old granular IDs do **not** automatically
become `builtin:multica-platform`. Review and update each affected agent
before assigning it new work. In particular, a Trackself parent agent that
previously omitted `multica-working-on-issues` needs the new platform slot
enabled and mapped to the Trackself replacement. This is an intentional
cutover step: an automatic rewrite could silently give that agent the generic
Multica issue workflow.

Install a daemon that advertises the consolidated platform-skill capability
before selecting a replacement. A task claim from an older daemon fails
visibly for a mapped agent because its compiled brief still points at the old
granular issue skill and would conflict with the selected workflow.

## Build and import the Trackself bundle

From this repository root:

```bash
python3 fork/trackself/build_skill.py \
  --trackself-skill ~/workspaces/trackself/workspace-control/config/multica-skills/trackself-working-on-issues
multica skill import --file fork/trackself/trackself-platform.skill --on-conflict fail --output json
```

The builder copies the current embedded platform references and the current
Trackself skill and its references into one complete archive. It replaces the
old standalone skill's obsolete introduction in the nested copy. It checks
that Multica still has the expected eight references and stops if the bundle
shape changes; review any upstream additions before changing that check.
`source-manifest.json` in the archive records source hashes for review.

Record the imported workspace skill UUID from the JSON response. In the
agent's **Skills** tab, ensure `multica-platform` is enabled, then select the
imported `trackself-platform` skill as its replacement. Apply this only to the
Trackself orchestrators that own automatic parents (currently Mika, Maca, and
Mewina). Specialists may continue to inherit the embedded bundle. The UI
refuses a replacement that lacks a required file, and refuses disabling a
slot while it has a replacement; clear the replacement first.

For API automation, the same selection is:

```http
PUT /api/agents/<agent-id>/builtin-skills/replacements
Content-Type: application/json

{"skill_id":"builtin:multica-platform","replacement_skill_id":"<workspace-skill-uuid>"}
```

Send an empty `replacement_skill_id` to restore the embedded bundle. The
existing `PUT /api/agents/<id>/builtin-skills/enabled` endpoint sets the slot's
enabled state. `DELETE /api/agents/<id>/builtin-skills` restores inherit-all
without clearing a valid replacement. Agent copy and the web Duplicate flow
preserve both policy fields within the same workspace.

## Update and review

After changing Trackself's source skill or after merging a Multica release,
rebuild the archive and inspect its `source-manifest.json`. Import with
`--on-conflict overwrite` under the same skill creator to update the existing
workspace skill in place; its UUID and per-agent selections remain stable.
The next task claim receives the new bundle without rebuilding Multica. A
claim already in progress is pinned to its advertised replacement hash and
fails visibly if the skill changes between claim and resolution. Each issue
task records its effective platform bundle fingerprint. If the next issue
turn would resume a session created with another fingerprint, it starts a
fresh provider session and reconstructs context under the new workflow.

Before enabling a replacement, inspect its root router, short runtime policy,
and relevant references. Verify a full and slim claim for a selected agent,
a resolve request for its workspace skill, rejection of the replaced built-in
ID, and the rendered issue brief. The focused Go tests cover these paths;
`pnpm typecheck` covers the web configuration surface. No database or running
Multica instance is changed merely by building this archive.

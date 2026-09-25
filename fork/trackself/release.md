# Mica release and deployment

Published 2026-09-25 as `v0.5.1-janez.2` from reviewed source commit
`0f23313676b0fcc66f197dd9dffef7c4feb10d09` on
`feature/trackself-context-workflow`. The registry bundle contains the exact
source archive, so recovery does not depend on an unpublished source branch.
No GitHub release tag was pushed.

Publication and the production server/Linux upgrade are complete. Main and
branch-2 runtimes are healthy on `.2`; branch-1 has the new image and remains
stopped. Coordinated Linux Mica activation completed on 2026-09-25.
Native macOS/Windows activation remains deferred. The
private-infra record `infra/automation-server/multica/upgrade-v0.5.1-janez.2.md`
owns deployment state; the prior `upgrade-v0.5.1-janez.1.md` owns the
previous revision and rollback baseline.

## Registry artifacts

All repositories use `git.thn.janezstupar.com/janez/` and exact tag
`v0.5.1-janez.2`. Use immutable digests for deployment.

| Repository | Digest | Contents |
| --- | --- | --- |
| `multica-backend` | `sha256:90ba6d031ec0bce386319806294bf2c9297ec759060e1c685c18a1209fb6f025` | Linux amd64 server, CLI, migration and maintenance binaries; migrations through 578. |
| `multica-web` | `sha256:ba19c70c3776e55608f9b9b3eb123e6c5c1a7e7fa5f7824941b41be2f7f2ef86` | Matching Linux amd64 frontend. |
| `multica-cli` | `sha256:1b344007c565855d97f6a5971081ddc0b332d18ff159c32c3d5d0276ab772f30` | Extract-only `/dist` bundle: CLI archives for Linux/macOS/Windows on amd64/arm64, exact source, prepared Mica skill, metadata and checksums. |

The CLI bundle's Linux amd64 OCI metadata describes its transport container,
not the six archive targets. Extract it without starting a container:

```sh
artifact=git.thn.janezstupar.com/janez/multica-cli@sha256:1b344007c565855d97f6a5971081ddc0b332d18ff159c32c3d5d0276ab772f30
docker pull --platform linux/amd64 "$artifact"
container=$(docker create --platform linux/amd64 "$artifact")
mkdir -p ./multica-v0.5.1-janez.2
docker cp "$container":/dist/. ./multica-v0.5.1-janez.2/
docker rm "$container"
(cd ./multica-v0.5.1-janez.2 && sha256sum -c checksums.txt)
```

The prepared policy is `trackself-platform-4ea20d203a893dc2`, content identity
`4ea20d203a893dc2610d93fbd1c47cce11013e2e0a79b837ea29a465759ca6cb`.
It includes the agreed production authority and format-2 completion semantics.
The imported Trackself UUID is `3dd9bba6-81b2-425c-8361-377a9a1acbe8`;
read-back matched the root and all 12 supporting files byte-for-byte. It is
the active default after the separately approved cutover described below.
Import alone did not change agents, defaults or existing tickets.

Local retained artifacts are under ignored `dist/releases/v0.5.1-janez.2/`.
Registry artifacts are the durable published copies.

## Validation and build identity

- Build date `2026-09-25T07:55:44Z`; Go binaries use Go 1.26.8,
  `CGO_ENABLED=0`, exact version and full source commit. Standalone CLI builds
  use `-trimpath -buildvcs=false` and explicit identity flags.
- Backend and web images were built from a clean `git archive` context and
  carry the full source revision label. The frontend production compile,
  TypeScript check and network-isolated HTTP 200 smoke passed.
- Backend and standalone Linux CLI report the expected identity. All six
  archive targets and contents passed checks; checksums passed again after
  extraction from the published OCI image. Native runtime behavior is deferred.
- Source and built backend/CLI vulnerability scans found no vulnerabilities.
- All three registry manifests were read back; anonymous pulls by digest passed.
- Independent code acceptance, database proof, guarded Forgejo PR10 delivery
  and cleanup, and unrelated broad frontend baseline failures are recorded in
  [runtime-proof.md](runtime-proof.md#2026-09-25-format-2-completion-trial).
  They do not constitute a deployed-environment canary.

The matching local Linux runtime image is
`local/multica-trackself-desktopapp-runtime:multica-v0.5.1-janez.2-codex-0.156.1`,
image ID `sha256:6950ac009b752846498d6b50be5074f2434a15acf356556fe46974771d1361d2`.
It replaces only the Multica CLI over the exact previous runtime image.
Isolated checks confirmed Multica identity, Codex 0.156.1, Node 22.23.2 and
Git 2.55.0. All three recreated agent containers use this image with unchanged mounts.
Main and branch-2 passed health/version/authenticated API canaries; branch-1
remains stopped with the new binary.

## Deployment boundary

The production-copy rehearsal and its second no-op migration run passed;
its temporary database and dump were removed. Production now has 605 migration
stems, with protected database/uploads/configuration backups retained under
`utility-server:/opt/multica/config-backups/before-v0.5.1-janez.2`. An older image
alone is not a database rollback. Keep `DO_NOT_TRACK=1`,
original mounts, credentials and toolchain settings intact. Preserve branch-1's
pre-existing stopped state; restart only the previously running main and branch-2
runtimes after health and configuration checks.

Coordinate the KB, workspace-control, complete skill replacement, imported
bundle identity, existing `pr_ready` status category and new-ticket default at
activation. Old unfinished tickets remain frozen until explicitly migrated.
No native host upgrade or automatic legacy-ticket migration is implied.

The superseded `v0.5.1-janez.1` backend, web and CLI registry versions were
deleted through the Forgejo API after the upgrade. The stopped old web container
and cached backend/web images were removed from utility-server; the old CLI
image was already absent. Read-back confirmed all three old package versions
absent and all `.2` versions preserved. Protected database/configuration backups
remain intact. Logs are retained with the release validation evidence.

## Local Linux desktop client

On 2026-09-25, the workstation AppImage was built from the same exact source
commit and installed at `/home/janez/.local/opt/multica/Multica.AppImage`.
Desktop metadata reports `0.5.1-janez.2`; the bundled CLI is byte-identical to
published CLI SHA-256 `eb43ece3fce1467536764f22af03ae6eba3fe432ee77266a8c1a9e80dbe6fa98`.
AppImage SHA-256 is
`c000ece25058d67828ac5496f80b02856372f56b6b72491a4088148a541f1068`.
The artifact and build/startup logs are retained under the ignored release
`desktop/` and `validation/` directories.

The atomic replacement preserved the launcher and desktop profile. The previous
AppImage is `Multica.AppImage.before-v0.5.1-janez.2` beside the installed file.
After client restart, its existing idle desktop-owned daemon upgraded from
`v0.4.40-janez.2` to `.2`; health reported running with zero active tasks.
Automatic upstream updates remain disabled. This verifies packaging and startup,
not a full interactive UI acceptance pass or desktop-agent policy activation.
Electron-builder emitted dependency collector warnings but completed successfully;
the packaged app version, exact CLI checksum and running client were checked.

## Coordinated Linux activation

Activation completed at `2026-09-25T09:08:49.206821Z`, with ticket policy version
`sha256:48de64f4add35c8299bb7237b5428a5ea3f3ae041f68c9261e3d9729c136f455`.
The workspace-control repository's `docs/multica-workflow.md` owns the exact
configuration and cutover evidence. KB commit
`dfefa3c236cb2a66dcb4919db9e93f920fc36865` and control commit
`d6338efb66b7e3de663d0ed5125d5ff396740e1a` were published and adopted; the
operator KB skill projection and both branch bootstrap projections were updated.

The 12 retained Linux agent configurations passed API read-back. A scoped
branch-2 chat task verified the exact published KB checkout and task-local
imported skill; container reads independently confirmed both hashes. This was
an actual agent execution, distinct from the scripted format-2 delivery trial.
The chat was archived with its transcript retained; no probe issue was created.
Production read-back confirmed all 632 prior tickets frozen, zero migrated, and
`pr_ready` in the `started` category. Main and branch-2 resumed; branch-1 remains
stopped. A protected pre-cutover database snapshot is retained at
`utility-server:/opt/multica/config-backups/before-mica-cutover-20260925`.

The consumed preparation patches and completed implementation plan were retired.
Native host dispatch/repository/context checks accompany their later deployment;
they do not repeat server delivery proof. Old tickets remain frozen until
explicit migration; neither this activation nor client upgrades migrate them.

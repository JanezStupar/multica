# Mica deployment candidate

Published 2026-09-25 as `v0.5.1-janez.2` from reviewed source commit
`0f23313676b0fcc66f197dd9dffef7c4feb10d09` on
`feature/trackself-context-workflow`. The registry bundle contains the exact
source archive, so recovery does not depend on an unpublished source branch.
No GitHub release tag was pushed.

Publication is complete. Production and Linux runtimes still use
`v0.5.1-janez.1`; deployment of this follow-up and coordinated Mica activation
remain pending. Native macOS/Windows activation remains deferred. The
private-infra record `infra/automation-server/multica/upgrade-v0.5.1-janez.2.md`
will own deployment state; the prior `upgrade-v0.5.1-janez.1.md` owns the
currently deployed revision and rollback baseline.

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
Its imported workspace UUID must be read from the actual import result;
packaging does not bind agents, activate defaults or migrate existing tickets.

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
Git 2.55.0. Building this image did not restart a runtime.

## Deployment boundary

Rehearse the four new migrations on a disposable production dump, then preserve
a fresh database/uploads/configuration backup before the scoped server/Linux
upgrade. An older image alone is not a database rollback. Keep `DO_NOT_TRACK=1`,
original mounts, credentials and toolchain settings intact. Preserve branch-1's
pre-existing stopped state; restart only the previously running main and branch-2
runtimes after health and configuration checks.

Coordinate the KB, workspace-control, complete skill replacement, imported
bundle identity, existing `pr_ready` status category and new-ticket default at
activation. Old unfinished tickets remain frozen until explicitly migrated.
No native host upgrade or automatic legacy-ticket migration is implied.

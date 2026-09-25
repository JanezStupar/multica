# Mica deployment candidate

Published 2026-09-24 as `v0.5.1-janez.1` from source commit
`3ed16d8217503e168961f1d52ba97a2c20aae207` on
`feature/trackself-context-workflow`. The local annotated release tag points to
that source commit. It is deliberately not pushed to GitHub: the fork's tag
workflow also publishes GHCR artifacts, outside this Forgejo publication.
The registry bundle includes the exact source archive, so artifact recovery
does not depend on an unpublished source branch.

Publication and the utility-server upgrade are complete. The backend/frontend
run this release and production migration/configuration checks passed. All three
Linux runtimes are upgraded and healthy, with original mounts preserved and
authenticated CLI checks passing. The main development database was started
with separate user approval and is healthy.
Native agents are paused. No native installed CLI, active KB/skill workflow
binding or workspace default was changed. The three Linux agents received only
the platform capability skill; Mica policy activation remains separate.
The private-infra deployment record below owns live deployment status.

## Registry artifacts

All repositories use `git.thn.janezstupar.com/janez/` and exact tag
`v0.5.1-janez.1`. Prefer the verified immutable digests for deployment.

| Repository | Digest | Contents |
| --- | --- | --- |
| `multica-backend` | `sha256:39ed2a8ff5597d9f486f9700a74f63a6f0adbd5c7c4d8dce9269ba4573cc3514` | Linux amd64 server, CLI, migration and maintenance binaries; migrations through 574. |
| `multica-web` | `sha256:409cb6817271ca50453510930e7adf471884b456b5683655d79afd30787ddacf` | Matching Linux amd64 frontend. |
| `multica-cli` | `sha256:113ec748cec026f508c2d11c6bc14cf4523f23b70689ab32ecaee7d0820d7245` | Extract-only `/dist` bundle: CLI archives for Linux/macOS/Windows on amd64/arm64, exact source, prepared Mica skill, metadata and SHA-256 checksums. |

The CLI bundle is an OCI image with Linux amd64 metadata for transport; it is
not runnable and its archives cover all six target combinations. Extract it
without starting a container:

```sh
artifact=git.thn.janezstupar.com/janez/multica-cli@sha256:113ec748cec026f508c2d11c6bc14cf4523f23b70689ab32ecaee7d0820d7245
docker pull --platform linux/amd64 "$artifact"
container=$(docker create --platform linux/amd64 "$artifact")
mkdir -p ./multica-v0.5.1-janez.1
docker cp "$container":/dist/. ./multica-v0.5.1-janez.1/
docker rm "$container"
(cd ./multica-v0.5.1-janez.1 && sha256sum -c checksums.txt)
```

Archive filenames follow the existing installer convention, e.g.
`multica-cli-0.5.1-janez.1-linux-amd64.tar.gz`; binaries report the full
`v0.5.1-janez.1` release tag. The policy archive is a prepared unconfigured
bundle: importing it neither selects production acceptance authority nor
activates a workspace default. Rebuild with the chosen production authority
before its eventual import and selection.

Local retained artifacts are in the ignored `dist/releases/v0.5.1-janez.1/`
directory: `cli/`, extracted Linux `backend/` binaries and `validation/` logs.
Registry artifacts are the durable published copies.

## Validation and build identity

- Source build date: `2026-09-24T18:02:05Z`; Go binaries use Go 1.26.8,
  `CGO_ENABLED=0`, exact version and full source commit. CLI builds use
  `-trimpath -buildvcs=false` with explicit identity flags because the source
  archive has no Git metadata.
- Backend and frontend images were built from a clean `git archive` context
  using the repository Dockerfiles. Both image labels identify the source
  commit. The frontend production compile and TypeScript checks passed.
- The backend CLI and standalone Linux amd64 CLI reported the expected version,
  commit, date and target at runtime. All six CLI build targets and archive
  contents were checked; their checksums passed again after extraction from
  the OCI bundle. Native macOS and Windows runtime checks are deferred to their
  deployment, not implied by cross-compilation.
- `go tool govulncheck ./...` found no vulnerabilities in the source scan.
  Separate binary-mode scans of the built server and CLI also found none.
- The frontend returned HTTP 200 in a temporary network-isolated container;
  the container was removed. No backend/database deployment was performed.
- All three registry manifests were read back and all three digest pulls
  succeeded using an empty Docker configuration with no registry credentials.
- Workflow correctness and the single-/multi-repository live trials are recorded
  in [runtime-proof.md](runtime-proof.md). Those isolated trials precede release
  packaging; publication itself is not a deployed-environment canary.

## Deployment boundary

The 2026-09-24 read-only utility-server preflight and proposed upgrade/rollback
procedure are recorded in private-infra at
`infra/automation-server/multica/upgrade-v0.5.1-janez.1.md`. The isolated rehearsal passed on 2026-09-24: a fresh production dump restored,
all 120 pending migrations applied, invariant checks and a second migration run
passed, and all temporary database resources were removed. Production retained
its old images and migration history. The matching Linux runtime image is now built locally and its unchanged toolchain
and exact Multica identity are verified. The same private-infra package contains
prepared server configuration, Linux agent skill API payloads and maintenance/
rollback commands. Production server and all three Linux-runtime health/API canaries have passed.
Trackself workflow cutover and native-host upgrades follow separately. The server entrypoint runs migrations on startup, so an older
image alone is not a database rollback plan. Check self-host telemetry settings
(`DO_NOT_TRACK`) when preparing the production configuration.

Reconcile agent roles, exact effort settings, production triviality and merge
policy, KB/skill consumers and dispatch sources together. New tickets adopt the
selected policy at cutover; old unfinished tickets remain frozen until explicit
migration. Platform upgrade and policy activation are separate operations.

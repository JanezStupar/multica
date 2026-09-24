# Mica deployment candidate

Published 2026-09-24 as `v0.5.1-janez.1` from source commit
`3ed16d8217503e168961f1d52ba97a2c20aae207` on
`feature/trackself-context-workflow`. The local annotated release tag points to
that source commit. It is deliberately not pushed to GitHub: the fork's tag
workflow also publishes GHCR artifacts, outside this Forgejo publication.
The registry bundle includes the exact source archive, so artifact recovery
does not depend on an unpublished source branch.

Publication is complete; deployment and workflow activation have not occurred.
No utility-server service, workstation runtime, installed CLI, active KB/skill
binding or workspace default was changed. The prepared consumer patches still
need reconciliation against their owning repositories before cutover.

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

Next prepare the utility-server backup and migration preflight, update the exact
backend/web artifact pins, and verify a deployed-environment canary before
Trackself cutover. The server entrypoint runs migrations on startup, so an older
image alone is not a database rollback plan. Check self-host telemetry settings
(`DO_NOT_TRACK`) when preparing the production configuration.

Reconcile agent roles, exact effort settings, production triviality and merge
policy, KB/skill consumers and dispatch sources together. New tickets adopt the
selected policy at cutover; old unfinished tickets remain frozen until explicit
migration. Platform upgrade and policy activation are separate operations.

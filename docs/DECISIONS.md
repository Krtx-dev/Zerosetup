# Architecture Decision Records

Short, dated records of decisions that shape the codebase. Newest last.

## ADR-001 — Go for the engine and CLI (2026-11-05)
Single static binary, first-class Docker SDK, good concurrency/cancellation primitives. Minimum Go version `1.25` (needs `os.Root`, introduced in 1.24, and 1.25 stdlib). Developed with Go 1.27.3.

## ADR-002 — Official moby client SDK `github.com/moby/moby/client` v0.6.x (2026-11-05)
`github.com/docker/docker` is frozen at v28.5.2; the maintained SDK moved to `github.com/moby/moby/client` + `github.com/moby/moby/api`. We use it through an internal `runtime.Backend` interface so a VM-based backend can be added later. No shelling out to the `docker` CLI.

## ADR-003 — Step containers instead of `docker build` (2026-11-05)
Installs and builds run as **ephemeral step containers** sharing a session-owned named volume, rather than generating a Dockerfile and calling the image-build API.
- Each stage (prepare, install, build, start) is a distinct container with its own exit code, duration and log stream → honest per-stage status.
- Commands are argv arrays executed directly (no `/bin/sh -c`).
- Every step gets the same hardening (cap-drop ALL, read-only rootfs, non-root, limits). `docker build` RUN steps cannot be fully constrained the same way via the classic builder, and BuildKit via the API requires a gRPC session.
- Caching is done with labeled per-ecosystem cache volumes (`npm`, `pip`), keyed by plan inputs; see ADR-008.
Trade-off: no reusable application image is produced. Acceptable for dev-environment use; revisit for "export image".

## ADR-004 — No bind mounts, ever (2026-11-05)
Source is copied into a named volume via `CopyToContainer`. This avoids host filesystem exposure and macOS file-sharing performance issues. Consequence: edits on the host are not live-reloaded into the container (documented limitation).

## ADR-005 — SQLite via `modernc.org/sqlite` (2026-11-05)
Pure Go (no cgo) keeps cross-compilation trivial for GoReleaser. WAL mode allows the CLI and the dashboard server to share the database. Events are stored in SQLite so the API can stream events for runs started by a separate CLI process (SSE handler tails the events table).

## ADR-006 — Deterministic plan hash as the approval token (2026-11-05)
The plan is serialized with stable field order (Go struct order, sorted maps), contains no timestamps, and is hashed with SHA-256. Approval binds to that hash: if anything in the plan changes, approval must be given again. `--approve <hash>` exists for scripted use but only approves that exact plan; there is no "approve anything" flag (threat model rule S).

## ADR-007 — Framework binaries invoked directly when safe (2026-11-05)
If a `package.json` script is a simple `<bin> <args>` invocation (no shell metacharacters) of a known framework binary, the plan runs `node_modules/.bin/<bin>` directly with bind/port flags appended. This guarantees `0.0.0.0` binding and a known port. Complex scripts are run via `<pm> run <script>` with `HOST`/`PORT` env and an explicit warning that binding cannot be guaranteed.

## ADR-008 — Package managers installed into the workspace (2026-11-05)
Node ≥25 images no longer ship Corepack, and `corepack enable` needs root. pnpm/yarn are installed with `npm install --global <pm>@<version>` into `/workspace/tools` as uid 1000. Version comes from the `packageManager` field, else from the lockfile format, else a documented default.

## ADR-009 — Default runtime versions (2026-11-05)
When nothing is declared: Node.js **24** (LTS; Node 26 is the newest LTS but 24 maximizes compatibility for older repos), Python **3.13**. Always surfaced as `DEFAULT` with a warning, never as detected.

## ADR-010 — Readiness checks run partly inside the container (2026-11-05)
Host-side TCP connects to a published port succeed even when nothing listens inside the container (the port forwarder accepts first). So "port listening" is determined by reading `/proc/net/tcp{,6}` via a non-privileged exec in the app container. This also detects servers bound only to `127.0.0.1` inside the container — the most common "works locally, not in Docker" failure — and feeds the diagnostics engine.

## ADR-011 — Dashboard auth: bearer token in URL fragment (2026-11-05)
`zerosetup dashboard` generates a random token per process and opens `http://127.0.0.1:<port>/#token=…`. Fragments are not sent to servers or in `Referer`. The SPA keeps it in `sessionStorage`. Event streams use `fetch()` streaming (not `EventSource`) so the token can be sent as a header.

## ADR-011b — Local toolchain for development (2026-11-05)
The Go toolchain for this workspace lives in `.tools/` (gitignored); `. .tools/env.sh` puts it on PATH. CI uses `actions/setup-go`.

## ADR-012 — TypeScript 6.0.x for the dashboard (2026-11-05)
TypeScript 7.0 (the native compiler) is current, but typescript-eslint 8.72 supports only `<6.1.0`. We pin 6.0.3 until the lint toolchain supports 7.

## ADR-013 — Hash router, no router dependency (2026-11-05)
The dashboard has five routes. A ~50-line hash router avoids a dependency, and keeps every view deep-linkable (`#/sessions/<id>/logs`).

## ADR-014 — Plans always serialize empty arrays (2026-11-05)
`planschema.Normalize` runs in the planner, so API consumers never see `null` collections. Found during the manual dashboard test, where a `null` crashed the inspect view.

## ADR-015 — Dependency reuse is conservative (2026-11-05)
Installed dependencies are reused only within a session, and only if image digest, manifest hashes, install argv **and non-secret env** are unchanged. Env is included because it can change installs (e.g. `NODE_ENV`). Cross-session speedups come from the shared package-manager cache volume.

## ADR-016 — Docker's embedded DNS listener is ignored by readiness (2026-11-05)
Containers on user-defined networks show a listener on `127.0.0.11` (Docker's DNS). It is filtered out of listener reports and port-mismatch logic.

## ADR-017 — Minimum Go version is 1.26 (2026-11-05, corrects ADR-001)
`modernc.org/sqlite` and `golang.org/x/sys`/`x/term` require Go 1.26, so `go.mod` says `go 1.26.0`. Found while running the unit tests on Linux with Go 1.25, which refused the module.

## ADR-018 — Managed services are explicit, credential-free and private (2026-11-05)
`--with-service postgres|redis` adds official images to the plan. Services run as their images' non-root users with a read-only rootfs (plus a tmpfs for the PostgreSQL socket dir), are attached only to the session network under a DNS alias, and are never published. They use no password, which is acceptable only because nothing but the app container can reach them. Generating a password would mean storing a secret, or failing on restart. Dependency names alone never add a service.

## ADR-019 — Plan and passport schemas 1.1 (2026-11-05)
Additive changes: `plan.services`, `passport.execution.overridesDetail`, `passport.execution.services`. 1.0 documents still decode and validate. JSON Schemas live in `schemas/`, and a test validates real generated artifacts against them.

## ADR-020 — Replay validates, never promises (2026-11-05)
`zerosetup replay` re-resolves the source with the recorded overrides and reports per-check `ok/warn/fail`: commit, manifest content hash, each lockfile, byte-identical plan, policy, engine architecture, and (optionally) image digest. Any `fail` blocks session creation. A created session still needs approval.

## ADR-021 — npm workspaces use hoisted binaries (2026-11-05)
`--workspace apps/web` installs at the root and runs the start/build command with the workspace as its working directory, referencing `../../node_modules/.bin/<bin>`. The policy allows `../` only up to the workspace depth. pnpm/yarn workspaces stay unsupported until fixtures exist.

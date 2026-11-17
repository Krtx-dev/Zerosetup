# Architecture

## Pipeline

```
input ─► source.Resolver ─► inspector (+ detect.Registry) ─► planner ─► policy ─► approval
                                                                            │
   passport ◄─ readiness ◄─ start app ◄─ step containers ◄─ prepare ◄─ engine.Execute
                    │
              diagnostics ─► repair (plan overrides)
```

Both interfaces (`internal/cli`, `internal/api`) call `internal/engine`. Neither contains business logic.

## Packages

| Package | Responsibility |
|---|---|
| `cmd/zerosetup` | Entry point and signal handling |
| `internal/zerr` | Structured errors with stable codes (`ZS-AREA-NNN`), stage and suggestion |
| `internal/source` | URL validation (github.com HTTPS allowlist), commit resolution, locked-down `git` fetch, `RepoFS` (read-only, `os.Root`-confined) |
| `internal/detect` | `Detector` interface, registry, env templates, analysis of existing Dockerfile/Compose/Dev Container specs |
| `internal/detect/node`, `…/python` | Language detectors |
| `internal/inspector` | Runs detectors and hashes manifests and lockfiles |
| `pkg/planschema` | Versioned plan types, canonical JSON, SHA-256 hash, validation, migration |
| `internal/planner` | Builds the plan with precedence and a closed set of overrides |
| `internal/policy` | Fail-closed security policy (image allowlist, no shells, loopback publishing, limits, env denylist) |
| `internal/runtime` | `Backend` interface (allows a future VM backend) |
| `internal/runtime/docker` | Moby SDK implementation. It enforces hardening on every container and finds the endpoint without the docker CLI |
| `internal/engine` | Application layer: inspect, sessions, execute, verify, lifecycle, reconcile, logs, passport, repair, doctor |
| `internal/services` | Catalog of managed services (PostgreSQL, Redis): images, non-root users, readiness commands, app env |
| `internal/readiness` | `/proc/net/tcp` parsing, HTTP probes, backoff |
| `internal/diagnostics` | Deterministic failure classification and repair proposals |
| `internal/sessions` | Session model and state machine |
| `internal/store` | SQLite (WAL) persistence: sessions, events, plans, metrics |
| `internal/passport` | Passport schema, validation, scrubbing |
| `internal/api` | Loopback HTTP API, SSE events, embedded dashboard |
| `dashboard/` | React + TypeScript + Vite + Tailwind UI |

## Execution model (ADR-003)

Every step runs in its own short-lived container. All of them share a session volume (`/workspace`) and a per-ecosystem cache volume (`/cache`):

1. **prep** (root, `CAP_CHOWN/FOWNER/DAC_OVERRIDE` only, no network): the source tarball is copied in through the Engine API, then `chown` runs on the volume roots.
2. **install steps** (uid 1000, network): commands come from the plan as argv, with no shell.
3. **build steps** (production mode): `vite build`, `next build`, or the build script.
4. **services** (optional, `--with-service`): official images as non-root users on the session network under an alias, gated by their own readiness command.
5. **app** (uid 1000): the start command, published on `127.0.0.1:<4317+>`.

Replay (`engine.Replay`) re-resolves a passport's source with its recorded overrides, compares commit, manifests, lockfiles, the plan hash and platform, and only then creates an approval-gated session.

Resource names: `zerosetup-<sid>` (network), `zerosetup-<sid>-ws` (volume), `zerosetup-<sid>-app`, `zerosetup-cache-<runtime>` (shared cache, role=cache). Every resource carries `io.zerosetup.managed=true`. Session resources also carry `io.zerosetup.session=<sid>`.

## Session state machine

`CREATED → INSPECTING → PLANNED → AWAITING_APPROVAL → PREPARING → BUILDING → STARTING → VERIFYING → READY`, plus `FAILED, STOPPING, STOPPED, REMOVING, REMOVED, CANCELLED, UNKNOWN`. The legal transitions are in `internal/sessions/state.go` and covered by tests. Fine-grained facts are *milestones* (`IMAGE_READY … APPLICATION_HEALTHY`), kept separate from state.

`Reconcile` runs on `ps`/`status`/API reads. It checks for:
- a persisted READY whose container has exited (→ STOPPED) or is missing (→ FAILED)
- an executing state whose runner PID is dead (→ FAILED, leftover containers removed)
- an unreachable engine (→ UNKNOWN)

## Events

`engine.emit` writes each event to SQLite and notifies in-process listeners. The API's SSE handler tails the events table, so the dashboard can watch runs started by a separate CLI process. Log lines are batched (≤200 lines / 300 ms) and redacted before storage.

## Readiness (ADR-010)

1. Container running (Engine inspect).
2. Listening sockets come from `cat /proc/net/tcp /proc/net/tcp6`, run inside the container. A loopback-only bind is detected and fails within ~8 s.
3. HTTP GET through the published port. A status below 500 means HTTP_READY. 2xx/3xx (or the configured statuses and body match) means APPLICATION_HEALTHY.
4. Bounded exponential backoff (250 ms → 2 s), 30 s per request, plan timeout (default 180 s).

## Extending

- **New detector:** implement `detect.Detector` and register it in `inspector.DefaultRegistry`. Add a fixture under `fixtures/` and an integration case. Add policy allowlist entries if a new image or binary is needed.
- **New backend:** implement `runtime.Backend`. The backend must enforce hardening itself and refuse unlabelled or unlimited containers.
- **AI explanations (future):** an optional provider may *explain* a plan. It must never produce commands. Detection stays deterministic.

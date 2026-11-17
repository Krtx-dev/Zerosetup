# Implementation Status

Last updated: 2026-11-05. Machine-readable tracker: [`tracker.json`](tracker.json) (keep both in sync).

Statuses: `TESTED` (automated tests pass) · `IMPLEMENTED_UNTESTED` · `IN_PROGRESS` · `NOT_STARTED` · `BLOCKED`.
`IMPLEMENTED_UNTESTED` does **not** mean done.

## Test evidence (macOS 27 arm64, Go 1.27.3, Colima + Docker 29.5.2)

| Suite | Command | Result |
|---|---|---|
| Go unit tests, all packages | `go test ./...` | ✅ pass |
| Go unit tests on **Linux arm64** (Go 1.26, in a container) | see DEVELOPMENT.md | ✅ pass |
| Race detector (engine, store, api, readiness) | `go test -race …` | ✅ pass |
| Cross-compile linux/amd64, linux/arm64, darwin/amd64, windows/amd64 | `GOOS=… go build` | ✅ compiles |
| Engine pipeline with fake backend (real HTTP sockets) | `go test ./internal/engine` | ✅ pass |
| Plans and passports validated against `schemas/*.json` | `go test ./internal/engine -run Schemas` | ✅ pass |
| API security (token, Host, Origin, body type, unknown fields) | `go test ./internal/api` | ✅ pass |
| **Docker integration**: 18 supported variants (every passport validated against the JSON Schema), 4 failure fixtures, real GitHub repository, repair, replay, Docker-daemon restart | `make test-integration` (+ `ZS_DAEMON_RESTART_CMD`) | ✅ all pass (190 s) |
| Dashboard unit tests | `npm test` | ✅ 12 pass |
| Dashboard typecheck + lint + build | `make lint dashboard` | ✅ pass |
| Playwright e2e against the real binary + Docker | `make e2e` | ✅ pass, 0 leftover containers |

Integration fixtures: vite, express, fastapi, nextjs, vite-pnpm, express-yarn, express-yarn-berry, fastapi-uv, flask, django, monorepo-npm / monorepo-pnpm / monorepo-yarn (`--workspace apps/web`), vite, nextjs, flask-gunicorn and django-gunicorn in `--mode production`, express-services (`--with-service postgres --with-service redis`), plus broken-port, unhealthy, missing-start-script, conflicting-config, loopback-default (repair).

Verified by hand with `docker inspect`: app and service containers run non-root, `CapDrop=[ALL]`, `no-new-privileges`, read-only rootfs, limits. Only the app is published, on 127.0.0.1 only. A Docker daemon restart was confirmed by `systemctl show docker` during the restart test.

## Feature status

| Phase | Area | Status |
|---|---|---|
| 0 | Architecture, threat model, decisions | done (docs) |
| 1 | Local + GitHub sources (ref/tag/SHA/subdir), real fetch | TESTED |
| 1 | Node detector: npm, pnpm, Yarn 1, Yarn 4 (node-modules linker); Vite, Next.js, Express, plain | TESTED |
| 1 | Python detector: pip, uv; FastAPI, Flask, Django | TESTED |
| 1 | Existing spec analysis (Dockerfile/Compose/devcontainer), never executed | TESTED |
| 1 | Plan schema 1.1, hash, validation, migration, JSON Schema | TESTED |
| 2 | Docker backend + enforced hardening | TESTED |
| 2 | Hash-bound approval, readiness (socket + HTTP) | TESTED |
| 3 | Production mode (Vite, Next.js, Flask/Django with gunicorn; Node build scripts) | TESTED |
| 3 | Workspaces (`--workspace`) for npm, pnpm, Yarn 1 | TESTED |
| 4 | Sessions, reconciliation (incl. daemon restart), restart, history | TESTED |
| 4 | Passports (schema 1.1) + replay | TESTED |
| 4 | Caching: source cache, package cache volumes, in-session dependency reuse | TESTED |
| 5 | Local API + dashboard (all screens, run options) | TESTED (unit + Playwright e2e) |
| 6 | Diagnostics + repairs (real Docker) | TESTED |
| 6 | Managed PostgreSQL / Redis | TESTED |
| 7 | CI workflow | IMPLEMENTED_UNTESTED: never run on GitHub (needs a pushed repository) |
| 7 | GoReleaser config | IMPLEMENTED_UNTESTED: running goreleaser locally was blocked by this session's permission policy |
| 7 | Demo assets | done: real screenshots + CLI transcripts in docs/demo (local fixtures only) |
| 8 | MicroVM backend, egress filtering | NOT_STARTED: design in docs/ISOLATION.md; needs KVM / signed helper |

# Changelog

All notable changes are documented here ([Keep a Changelog](https://keepachangelog.com/en/1.1.0/)).

## [Unreleased]
### Added
- Deterministic inspection for Node.js (Vite, Next.js, Express, plain) and Python (FastAPI) with evidence and provenance.
- Versioned, hashed execution plans (schema 1.0), plus a fail-closed security policy.
- Hash-bound approval, and a hardened Docker execution backend (step containers, non-root, cap-drop ALL, read-only rootfs, limits, no host mounts).
- Socket- and HTTP-based readiness, deterministic diagnostics, and plan-level repairs with revert.
- Persistent sessions (SQLite) with reconciliation, logs, stop/restart/remove with verified cleanup.
- Reproducibility passports (schema 1.0).
- Loopback API with token, Host and Origin protection, SSE events, and an embedded React dashboard.
- Flask and Django detection; pnpm, Yarn 1, Yarn 4 (node-modules linker) and uv installs verified with fixtures.
- pnpm and Yarn 1 workspaces; gunicorn production mode for Flask/Django.
- Diagnostics for Django `DisallowedHost` and missing executables (exit 127).
- Integration tests validate every exported passport against the JSON Schema.
- `--mode production` (Vite build + preview, Next.js build + start, Node build scripts, gunicorn when declared).
- `--workspace` for npm monorepos.
- `--with-service postgres|redis`: managed, non-root, private-network services.
- `zerosetup replay`: validates a passport against today and prepares a re-run.
- Plan and passport schemas 1.1 with JSON Schema files, validated in tests.
- Integration tests for a real GitHub repository, repairs, replay and Docker-daemon restarts. Playwright e2e.
### Fixed
- Minimum Go version corrected to 1.26 (dependencies require it).
- Dashboard cached stale assets after upgrades; plans serialized empty lists as `null`.
- Process liveness check on Windows.

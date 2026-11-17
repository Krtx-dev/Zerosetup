# Known Limitations

## Scope
- Supported: Node.js (Vite, Next.js, Express, plain HTTP; npm, pnpm, Yarn 1, Yarn 4 with `nodeLinker: node-modules`; npm/pnpm/Yarn 1 workspaces) and Python (FastAPI, Flask, Django; pip, uv). Everything else is reported as unsupported.
- Yarn Plug'n'Play and Yarn 2+ workspaces are unsupported. Yarn 1 ignores workspaces without a `version`, so ZEROSETUP refuses them up front. Workspace binaries are assumed hoisted for npm/Yarn 1.
- One application container per session, plus optional managed PostgreSQL/Redis.
- Dockerfiles, Compose files and Dev Container specs are analysed but never executed.
- Django database migrations are never run automatically.
- Production mode without a declared gunicorn falls back to the framework's development server, with a warning.
- Source is copied, not mounted, so host edits don't hot-reload.
- Only `github.com` over HTTPS. Private repositories, submodules and Git LFS are not supported. Abbreviated SHAs are rejected.

## Security (see THREAT_MODEL.md)
- Containers are not a strong isolation boundary. The microVM backend is design-only (docs/ISOLATION.md).
- Outbound network is not filtered, during installs or at runtime.
- Managed services have no authentication (they are private to the session network).
- Named volumes have no disk quota.
- Colima mounts your home directory into its VM by default. Restrict it with `--mount` (DEVELOPMENT.md).

## Platforms
- Fully tested on macOS arm64 (Colima / Docker 29.5.2). Go unit tests pass on Linux arm64. Linux Docker integration has not run yet (it's in CI). macOS Intel is untested. Windows is experimental and untested.
- Secrets are never persisted, so restarting a session that uses secrets needs them again.

## Behaviour
- Dependency reuse is conservative: any change to non-secret plan env forces a reinstall.
- `stop` keeps volumes. `remove` deletes everything labelled with the session. Shared package caches survive until `zerosetup cache clean`.
- Stopping the `dashboard` process cancels executions it started. READY sessions keep running.

## Release status
- CI has never run on GitHub (there's no remote yet). GoReleaser hasn't been run (it was blocked by this environment's permission policy).
- An unrelated npm package named `zerosetup` exists. Domains and trademarks haven't been checked (docs/RESEARCH.md).

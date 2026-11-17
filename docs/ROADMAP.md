# Roadmap

Support is claimed only after an integration fixture passes.

- **Done:** pnpm/Yarn 1/Yarn 4/uv, Flask, Django, npm/pnpm/Yarn 1 workspaces, production mode (incl. gunicorn), managed PostgreSQL/Redis, passport replay.
- **Phase 2 (next):** Yarn Plug'n'Play, Yarn 2+ workspaces, Bun.
- **Phase 3:** Go and Rust web apps. Executing a safe subset of Dev Container specs (image + forwardPorts + postCreate under policy, no host-side commands).
- **Phase 4:** Stronger isolation: a `runtime.Backend` built on microVMs (e.g. Firecracker or Apple Virtualization via a helper), with per-session kernels and egress policy. Remote isolated execution.
- **Phase 5:** Passport comparison across runs, shareable environment records, an agent-facing JSON API (`zerosetup run --json` event stream) for coding agents.

Design constraints that won't change: inspection never executes code, approval is bound to the plan hash, no unrestricted shell, no host mounts by default, and an honest security posture.

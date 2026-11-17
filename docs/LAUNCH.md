# Launch Kit

Nothing here may claim more than `IMPLEMENTATION_STATUS.md` does. Record real timings; never invent them.

## Repository description (GitHub "About")
> Inspect, run and reproduce supported GitHub projects locally. Explainable detection, hash-bound approval, hardened containers, real readiness checks, and reproducibility passports.

## README hero
> **From GitHub URL to running application.** ZEROSETUP reads a repository without running it, shows you an explainable plan, and runs it in a hardened local container only after you approve. It doesn't say "ready" until the app actually answers.

## Recorded assets (real, in `docs/demo/`)
- Screenshots `01`–`07`: captured by the Playwright e2e run against the real binary and Docker (`ZS_CAPTURE_DIR`).
- `cli-00-doctor.txt`, `cli-01-services-run.txt`, `cli-02-verify-remove.txt`, `cli-03-failure-diagnosis.txt`, `cli-04-repair.txt`: verbatim `--no-color` output. Home paths are abbreviated as `~` by the CLI itself.
- Still to record: the GitHub-URL run below (needs your approval to run a third-party repo outside tests) and a screen recording.

## Demo storyboard (real recording, ~60 s)
1. A browser with a small public Vite app on GitHub. Copy the URL.
2. `zerosetup inspect <url>`: evidence lines (DETECTED / INFERRED / DEFAULT). Pause on one.
3. `zerosetup run <url>`: plan, the honest approval prompt, type `y`.
4. Live progress: image digest, `npm ci` output, "port 5173 listening on all interfaces", "HTTP 200".
5. Open the URL. Then `zerosetup dashboard`: the timeline, the blueprint graph (click a node), the logs.
6. Break it: the `broken-port` fixture shows the loopback diagnosis, then **Apply repair → approve → READY**.
7. `zerosetup export` → passport JSON (digest, lockfile hashes). `zerosetup remove` → "verified: no resources remain".

Recording: `asciinema rec` for the CLI, and the OS screen recorder for the dashboard. Keep real timings in the video; if you cut, say so on screen.

## Short-form script (30 s)
"Found a cool repo? `zerosetup run` + URL. It reads the repo without running it, tells you *why* it thinks this is Next.js on Node 22, and asks before running anything. It's ready when the page actually loads, not when Docker says 'started'. And when it fails, it tells you why: 'your server only listens on localhost inside the container.'"

## Launch announcement draft
> **ZEROSETUP 0.1: a local-first way to run supported GitHub projects.**
> Point it at a Vite, Next.js, Express or FastAPI repo. It inspects without executing, shows a plan with evidence for every decision, and runs only after you approve that exact plan hash. Containers run non-root with all capabilities dropped and a read-only rootfs, and previews are bound to 127.0.0.1. Readiness means a real HTTP response. Every run can be exported as a secret-free reproducibility passport.
> Being honest about limits: containers aren't a sandbox for hostile code, outbound network isn't filtered, and only a handful of stacks are supported, each with an integration test. Contributions of detectors are very welcome.

## Technical article outline
1. Why "container started" ≠ "app works" (reading /proc/net/tcp from inside the container).
2. Hash-bound approval and deterministic plans.
3. Why we don't run your docker-compose.yml.
4. Step containers vs `docker build` for honest per-stage status.
5. Writing a detector in 100 lines.

## First 10 issues
1. `good first issue` Diagnostics rule: Next.js "Module not found: Can't resolve".
2. `good first issue` Diagnostics rule: Vite "Failed to resolve import".
3. `good first issue` `zerosetup ps --json` field documentation in docs/CLI.md.
4. `detector` Yarn Plug'n'Play support (needs a non-`node_modules/.bin` start strategy).
5. `detector` Bun runtime.
6. `detector` Go web apps (`go run .` with a `PORT` convention).
7. `enhancement` `zerosetup replay --diff` with field-level plan differences.
8. `enhancement` Multi-app workspaces (start two packages in one session).
9. `security` Egress allowlist for install steps (docs/ISOLATION.md §2), with a test before it's claimed.
10. `security` MicroVM backend prototype on Linux/KVM (docs/ISOLATION.md §1).

Labels: `good first issue`, `detector`, `diagnostics`, `dashboard`, `security`, `documentation`, `enhancement`, `bug`.

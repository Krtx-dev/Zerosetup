# ZEROSETUP Threat Model

Status: living document. Last reviewed with schema version `1.0` of the execution plan.

ZEROSETUP fetches third-party repositories and, **after explicit approval**, executes their
dependency installers and application code inside containers. That makes every repository
**untrusted input**. This document states what we defend against, how, and — just as
important — what we do **not** defend against.

## 1. Assets

| Asset | Why it matters |
|---|---|
| Host filesystem (home directory, SSH keys, cloud credentials, browser profiles) | Primary target of credential theft |
| Host secrets in environment variables / credential helpers | Could be exfiltrated if injected or inherited |
| Docker daemon socket | Equivalent to root on the Docker host |
| Local network services (databases, admin UIs on `localhost`) | Reachable from workloads or via the preview |
| ZEROSETUP local API | Can start containers; an unauthenticated API = local RCE |
| Host CPU, memory, disk | Resource exhaustion / denial of service |
| Exported passports | Shared publicly; must not leak secrets or personal data |

## 2. Threat actors

1. **Malicious repository author** — crafts manifests, lockfiles, lifecycle scripts, Dockerfiles, Compose or Dev Container files, symlinks, or huge files.
2. **Compromised dependency** — a package with a malicious `postinstall`/`setup.py`.
3. **Malicious local web page** — a site open in the user's browser that tries to call the ZEROSETUP API (CSRF, DNS rebinding).
4. **Malicious previewed application** — the running app itself, rendered in the dashboard.
5. **Other local users/processes** — attempting to reach published ports or the API.

## 3. Trust boundaries

```
 ┌──────────────── host (trusted) ────────────────┐
 │ zerosetup CLI / API (loopback only, token)     │
 │   │ reads repo files via os.Root (no exec)     │
 │   ▼                                            │
 │ Docker Engine API ─────────────┐               │
 └────────────────────────────────┼───────────────┘
                                  ▼
 ┌──────────── container (UNTRUSTED code) ────────┐
 │ repo source copied into a named volume         │
 │ non-root uid 1000, cap-drop ALL,               │
 │ no-new-privileges, read-only rootfs, limits    │
 │ NO host bind mounts, NO docker.sock            │
 └────────────────────────────────────────────────┘
```

## 4. Controls (what is enforced)

### Inspection (no code execution)
- `inspect` and `plan` **never execute repository code**. Files are read through Go's `os.Root`, which rejects paths and symlinks that escape the repository root.
- Reads are bounded: per-file size limit (1 MiB for manifests), directory walk depth/entry limits.
- Remote sources are fetched with `git` invoked as an argv array (no shell), with: hooks disabled (`core.hooksPath=/dev/null`), global/system Git config ignored (`GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1`), credential prompts disabled (`GIT_TERMINAL_PROMPT=0`), no credential helpers, `protocol.file.allow=never`, `protocol.ext.allow=never`, no submodule recursion, `GIT_LFS_SKIP_SMUDGE=1`, shallow single-commit fetch.
- Only HTTPS `github.com` URLs are accepted for remote sources (allowlist). Owner/repo names are validated against GitHub's character set.
- Repository size is checked after fetch; oversize sources are rejected.

### Execution
- **Approval gate**: execution requires approval of a specific plan hash. Interactive `run` prompts; non-interactive use requires `--approve <plan-hash>` matching the plan that will run. There is no flag that approves "anything".
- **No shell**: every command is an argv array executed directly by the container runtime. Commands are produced by detectors from a fixed vocabulary; repository-provided free-form commands (e.g. complex `package.json` scripts) are only ever run through the package manager's own `run` subcommand, and the plan shows that explicitly.
- **Policy validator** (`internal/policy`) fails closed. It rejects plans that: use non-allowlisted images, invoke a shell interpreter as argv[0], have relative paths escaping the workspace, publish on non-loopback addresses, lack resource limits, or request privileged settings.
- **Container hardening** (enforced in the Docker backend, not merely requested by plan):
  - `Privileged=false`, `CapDrop=["ALL"]`, `SecurityOpt=["no-new-privileges:true"]`
  - user `1000:1000`
  - `ReadonlyRootfs=true` with a `tmpfs` at `/tmp`; writable state only in ZEROSETUP-owned named volumes
  - memory, CPU, PIDs limits; `json-file` log driver with size rotation
  - **no bind mounts at all** — source is copied in via the Engine API
  - Docker socket is never mounted
  - published ports bind to `127.0.0.1` only
  - one-shot root prep container only runs `chown` with `CAP_CHOWN`/`CAP_FOWNER`/`CAP_DAC_OVERRIDE`, never repository code
- **Ownership labels**: every container, volume and network carries `io.zerosetup.managed=true` and `io.zerosetup.session=<id>`. Cleanup only ever targets resources with those labels; there is no global prune.
- **Secrets**: never read from the host automatically. Values the user supplies are held only in memory for the run, redacted from logs and events, and excluded from plans and passports (only the variable name is recorded).

### Local API
- Binds `127.0.0.1` only.
- Requires a per-process random bearer token (256-bit) on every `/api/` request.
- Rejects requests whose `Host` header is not a loopback name for the bound port (DNS-rebinding defence).
- Rejects state-changing requests carrying an `Origin` header that is not the dashboard origin (CSRF defence).
- No endpoint accepts commands, images, or mount paths from the client; clients can only choose among detected plans and a closed set of overrides that pass the same policy validator.

### Preview
- Applications are served from a different port (= different origin) than the dashboard, so they cannot read the dashboard's token or call the API with it.
- The embedded preview uses an `<iframe sandbox>`; users can always open in a separate tab instead.

### Managed services (`--with-service postgres|redis`)
- Started only on explicit request, never because a dependency name appears.
- Official images only (`postgres:17-alpine`, `redis:8-alpine`), run as their unprivileged users (70, 999) with all capabilities dropped, a read-only root filesystem, a tmpfs for the PostgreSQL socket directory, and limits (1 CPU, 512 MB, 256 PIDs).
- **Not published to the host.** They are reachable only on the session's private bridge network, by the alias `postgres`/`redis`.
- They use **no password** (`POSTGRES_HOST_AUTH_METHOD=trust`, Redis without AUTH). This is acceptable only because nothing but the session's app container can reach them. Don't put real data in them. Their data volumes are removed with the session.
- They run their image entrypoints (needed for initialisation). That is image code from Docker Hub, not repository code.

### Replay
`zerosetup replay` never executes anything. It re-resolves the source, regenerates the plan and compares it. If the checks pass, it creates a session that still needs approval of its own plan hash.

### Monorepo workspaces
Commands for a workspace run with a working directory inside the source. They may reference the root's `node_modules/.bin` through `../`, and the policy rejects any `../` sequence that would leave the source root.

## 5. Residual risks (NOT defended)

Be explicit with users about these:

1. **Containers are not a strong security boundary.** A kernel or runtime vulnerability allows escape. Hostile code should be run in a VM-isolated backend (planned: microVM backend, see `docs/ROADMAP.md`). On macOS, Docker already runs inside a Linux VM (Colima/Docker Desktop/OrbStack), which adds a layer, but that VM may share directories with the host (e.g. Colima mounts your home directory into the VM by default). A container escape into that VM could therefore reach host files.
2. **Outbound network access is not restricted.** Dependency installation needs the internet, and the running application also has outbound access. Malicious code can exfiltrate anything *inside the container* (which contains only the repository and its dependencies) and can reach hosts on your LAN. ZEROSETUP does **not** claim network isolation.
3. **Install scripts run.** `npm ci`, `pip install` etc. execute package lifecycle scripts by design. They run under the same container restrictions as the app.
4. **Preview content is still a live web app** on `127.0.0.1`. It can make requests to other localhost services from your browser (as any website can).
5. **Supply-chain of base images**: we pull official Docker Hub images (`node`, `python`) by tag and record the resolved digest. We do not verify signatures.
6. **Resource limits depend on the Docker host**: e.g. some rootless setups cannot enforce CPU/PIDs limits. `doctor` reports cgroup capabilities; the backend fails closed if the daemon rejects limits.
7. **Disk usage** inside named volumes is not quota-limited by Docker's default `local` driver.
8. **Managed services have no authentication** (see above). A compromised app container can do anything to its own database, which it could anyway.

## 6. Unknown repositories

Under the container-only trust model, any repository you have not reviewed should be treated as
**unsafe to execute**. ZEROSETUP shows this warning on every approval prompt. The approval
prompt is informed consent, not a security guarantee.

## 7. Reporting

See `SECURITY.md`.

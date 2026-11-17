# CLI Reference

Generated from `zerosetup --help` output by `make docs-cli`. Do not edit by hand.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | error |
| 2 | usage error |
| 3 | repository unsupported / replay checks failed |
| 4 | approval required or mismatched |
| 5 | container engine unavailable / doctor failure |
| 6 | execution failed or app not ready |
| 130 | cancelled |

## zerosetup 

```text
ZEROSETUP inspects a repository without executing it, explains how it would
run it, and — only after you approve the exact plan — runs it in a hardened,
isolated container and verifies that it actually responds.

Usage:
  zerosetup [command]

Available Commands:
  cache       Inspect or clean ZEROSETUP-owned caches
  completion  Generate the autocompletion script for the specified shell
  config      Show data locations and built-in defaults (read-only)
  dashboard   Start the local dashboard (bound to 127.0.0.1, token-protected)
  doctor      Check that this machine can inspect and run repositories
  export      Export the session's reproducibility passport (JSON, secret-free)
  help        Help about any command
  inspect     Analyse a repository without executing any of its code
  logs        Print application or step logs
  plan        Generate and explain the execution plan (no code is executed)
  ps          List sessions (state is reconciled with the container engine)
  remove      Remove all of a session's containers, volumes and networks, and verify
  repair      List, apply or revert proposed repairs to a failed session's plan
  replay      Check a reproducibility passport against today and prepare a session to re-run it
  restart     Restart the application and re-verify readiness
  run         Inspect, approve and run a repository in an isolated container
  status      Show a session's state, milestones, readiness and diagnostics
  stop        Stop the application (workspace is kept for restart)
  version     Print version information

Flags:
  -h, --help       help for zerosetup
      --no-color   disable colored output (NO_COLOR is also honoured)

Use "zerosetup [command] --help" for more information about a command.
```

## zerosetup inspect

```text
Analyse a repository without executing any of its code

Usage:
  zerosetup inspect <github-url | directory> [flags]

Examples:
  zerosetup inspect https://github.com/owner/repo
  zerosetup inspect ./my-project --json
  zerosetup inspect https://github.com/owner/repo --ref v1.2.0

Flags:
      --cpus float                 CPU limit
      --env stringArray            non-secret environment variable KEY=VALUE (repeatable)
  -h, --help                       help for inspect
      --json                       machine-readable output
      --memory int                 memory limit in MB
      --mode string                dev (default) or production (build, then serve the build)
      --port int                   override the application's container port
      --ready-path string          HTTP path used for the readiness check
      --ready-timeout int          readiness timeout in seconds
      --ref string                 branch, tag or full commit SHA to inspect
      --runtime-version string     override the runtime version (e.g. 22 or 3.12)
      --script string              package.json script to run instead of the detected one
      --with-service stringArray   start a managed service for the app: postgres or redis (repeatable)
      --workspace string           npm workspace directory to run in a monorepo (e.g. apps/web)

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup plan

```text
Generate and explain the execution plan (no code is executed)

Usage:
  zerosetup plan <github-url | directory> [flags]

Examples:
  zerosetup plan https://github.com/owner/repo
  zerosetup plan . --port 8080 --json

Flags:
      --cpus float                 CPU limit
      --env stringArray            non-secret environment variable KEY=VALUE (repeatable)
  -h, --help                       help for plan
      --json                       machine-readable output
      --memory int                 memory limit in MB
      --mode string                dev (default) or production (build, then serve the build)
      --port int                   override the application's container port
      --raw                        print only the canonical plan JSON (the exact bytes that are hashed)
      --ready-path string          HTTP path used for the readiness check
      --ready-timeout int          readiness timeout in seconds
      --ref string                 branch, tag or full commit SHA to inspect
      --runtime-version string     override the runtime version (e.g. 22 or 3.12)
      --script string              package.json script to run instead of the detected one
      --with-service stringArray   start a managed service for the app: postgres or redis (repeatable)
      --workspace string           npm workspace directory to run in a monorepo (e.g. apps/web)

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup run

```text
run inspects the repository, shows the execution plan, and asks for your
approval before any repository code (including dependency install scripts)
executes. Approval is bound to the plan's hash: if the plan changes, you are
asked again.

In non-interactive contexts pass --approve <plan-hash> after reviewing the
plan with `zerosetup plan`. There is no flag that approves any plan blindly.

Usage:
  zerosetup run [github-url | directory] [flags]

Examples:
  zerosetup run https://github.com/owner/repo
  zerosetup plan ./app && zerosetup run ./app --approve 3f9a1c2b7d4e
  zerosetup run --session zs1a2b3c4d          # re-run a failed or repaired session

Flags:
      --approve zerosetup plan        approve this exact plan hash without prompting (from zerosetup plan)
      --cpus float                    CPU limit
      --env stringArray               non-secret environment variable KEY=VALUE (repeatable)
  -h, --help                          help for run
      --memory int                    memory limit in MB
      --mode string                   dev (default) or production (build, then serve the build)
      --open                          open the preview in your browser when ready
      --port int                      override the application's container port
  -q, --quiet                         do not print the full plan before approval
      --ready-path string             HTTP path used for the readiness check
      --ready-timeout int             readiness timeout in seconds
      --ref string                    branch, tag or full commit SHA to inspect
      --runtime-version string        override the runtime version (e.g. 22 or 3.12)
      --script string                 package.json script to run instead of the detected one
      --secret stringArray            secret environment variable NAME; you are prompted for its value (repeatable)
      --secret-from-env stringArray   secret NAME whose value is read from your current environment (repeatable)
      --session string                run an existing session (e.g. after a repair or failure)
      --with-service stringArray      start a managed service for the app: postgres or redis (repeatable)
      --workspace string              npm workspace directory to run in a monorepo (e.g. apps/web)

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup replay

```text
replay re-resolves the recorded commit, regenerates the plan with the
recorded overrides, and compares commit, manifests, lockfiles, plan hash,
platform and (with --check-images) base-image digests. It never claims the
result will be identical. If no check fails, it creates a session that still
needs your approval: zerosetup run --session <id>.

Usage:
  zerosetup replay <passport.json> [flags]

Flags:
      --check-images    pull image tags and compare digests (needs Docker and network)
      --check-only      only run the checks; do not create a session
  -h, --help            help for replay
      --json            machine-readable output
      --source string   directory to replay from (required for passports of local sources)

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup ps

```text
List sessions (state is reconciled with the container engine)

Usage:
  zerosetup ps [flags]

Flags:
  -a, --all    include removed sessions
  -h, --help   help for ps
      --json   machine-readable output

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup status

```text
Show a session's state, milestones, readiness and diagnostics

Usage:
  zerosetup status <session-id> [flags]

Flags:
  -h, --help   help for status
      --json   machine-readable output

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup logs

```text
Print application or step logs

Usage:
  zerosetup logs <session-id> [flags]

Examples:
  zerosetup logs zs1a2b3c4d -f
  zerosetup logs zs1a2b3c4d --step install-deps

Flags:
  -f, --follow        follow application output
  -h, --help          help for logs
      --step string   read a step's log (e.g. install-deps) instead of the application
      --tail string   number of lines from the end (application logs) (default "all")

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup stop

```text
Stop the application (workspace is kept for restart)

Usage:
  zerosetup stop <session-id> [flags]

Flags:
  -h, --help   help for stop

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup restart

```text
Restart the application and re-verify readiness

Usage:
  zerosetup restart <session-id> [flags]

Flags:
  -h, --help                          help for restart
      --secret stringArray            secret NAME to prompt for
      --secret-from-env stringArray   secret NAME read from your environment

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup remove

```text
Remove all of a session's containers, volumes and networks, and verify

Usage:
  zerosetup remove <session-id> [flags]

Aliases:
  remove, rm

Flags:
  -h, --help   help for remove
      --json   machine-readable output

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup export

```text
Export the session's reproducibility passport (JSON, secret-free)

Usage:
  zerosetup export <session-id> [flags]

Flags:
  -h, --help            help for export
  -o, --output string   write to a file instead of stdout

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup repair

```text
Repairs change ZEROSETUP's generated plan (port, environment, limits,
timeouts) — never the repository. Applying a repair creates a new plan that
must be approved before it runs. At most 3 repairs can be applied per session.

Usage:
  zerosetup repair <session-id> [flags]

Examples:
  zerosetup repair zs1a2b3c4d
  zerosetup repair zs1a2b3c4d --apply bind-all-interfaces
  zerosetup repair zs1a2b3c4d --revert

Flags:
      --apply string   apply the repair with this ID to the plan
  -h, --help           help for repair
      --json           machine-readable output
      --revert         revert the last applied repair

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup dashboard

```text
Start the local dashboard (bound to 127.0.0.1, token-protected)

Usage:
  zerosetup dashboard [flags]

Flags:
  -h, --help       help for dashboard
      --no-open    do not open a browser
      --port int   loopback port for the dashboard (0 = random) (default 4316)

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup config

```text
Show data locations and built-in defaults (read-only)

Usage:
  zerosetup config [flags]

Flags:
  -h, --help   help for config

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup cache

```text
Inspect or clean ZEROSETUP-owned caches

Usage:
  zerosetup cache [command]

Available Commands:
  clean       Remove ZEROSETUP's package cache volumes (and, with --sources, cached checkouts)
  status      Show cache usage

Flags:
  -h, --help   help for cache

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)

Use "zerosetup cache [command] --help" for more information about a command.
```

## zerosetup doctor

```text
Check that this machine can inspect and run repositories

Usage:
  zerosetup doctor [flags]

Flags:
  -h, --help   help for doctor
      --json   machine-readable output

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```

## zerosetup version

```text
Print version information

Usage:
  zerosetup version [flags]

Flags:
  -h, --help   help for version

Global Flags:
      --no-color   disable colored output (NO_COLOR is also honoured)
```


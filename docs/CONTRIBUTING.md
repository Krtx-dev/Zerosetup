# Contributing

Thanks for helping. Please read [ARCHITECTURE.md](ARCHITECTURE.md) and the [threat model](THREAT_MODEL.md) first.

## Setup
See [DEVELOPMENT.md](DEVELOPMENT.md). `make check` runs everything CI runs without Docker.

## Ground rules
- Inspection must never execute repository code. Detectors only read files through `detect.Repo`.
- Never add shell interpretation, host mounts, privileged settings, or an "approve everything" flag.
- Don't claim support for a framework until an integration fixture passes.
- Don't report anything as ready, healthy or fixed unless a real check confirmed it.
- Use structured errors (`internal/zerr`) with a suggestion the user can act on.

## Adding a detector
1. Create `internal/detect/<lang>/` with a type implementing `detect.Detector` (`ID`, `Priority`, `Supports`, `Detect`).
2. Record every choice with `res.Decide(field, value, kind, certainty, reason, evidence...)`. Use `DETECTED` only for values the repository declares.
3. Express commands as argv. If a new binary or image is needed, extend the allowlists in `internal/policy` and explain why in the PR.
4. Register it in `inspector.DefaultRegistry`.
5. Add `fixtures/<name>/` (a minimal, harmless app) plus a case in `internal/inspector/fixtures_test.go` and in `tests/integration`.
6. Add any new diagnostics rules, with tests, in `internal/diagnostics`.

## Pull requests
- Keep changes focused. Include tests. Update `docs/IMPLEMENTATION_STATUS.md` and `docs/tracker.json`.
- Run `make check`, and `make test-integration` if you touched execution.
- Describe the security impact (even "none").

## Coding standards
`gofmt`, `go vet`, small packages, explicit lifecycle, `context` everywhere. Comments explain *why*. Frontend: strict TypeScript, ESLint, accessible components, no mocked data outside tests.

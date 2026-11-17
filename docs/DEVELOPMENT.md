# Development

## Toolchain
- Go ≥ 1.26 (developed on 1.27.3). In this workspace a local toolchain can live in `.tools/` (`. .tools/env.sh`). The Makefile picks it up automatically.
- Node ≥ 20.19 for the dashboard.
- A container engine for execution tests. On macOS:
  ```bash
  brew install colima docker
  colima start --cpu 4 --memory 6 --mount "$HOME/.zerosetup/colima-empty:w"   # avoids sharing your home directory with the VM
  ```

## Common tasks
```bash
make dashboard-deps     # npm ci
make build              # dashboard + bin/zerosetup (dashboard embedded)
make check              # gofmt, vet, Go tests, dashboard lint/typecheck/tests
make test-integration   # real Docker; pulls node/python images
make e2e                # Playwright against a real dashboard (PLAYWRIGHT_BROWSERS_PATH / PW_CHANNEL)
make docs-cli           # regenerate docs/CLI.md from --help
```
Opt-in Docker-daemon restart test: `ZS_DAEMON_RESTART_CMD="colima ssh -- sudo systemctl restart docker" make test-integration`. Run a subset of fixtures with `ZS_ONLY=vite,flask`.
Demo screenshots: `cd dashboard && ZS_CAPTURE_DIR=$PWD/../docs/demo npx playwright test`.

### Linux unit tests from macOS
```bash
C=$(docker create -w /src -e GOFLAGS=-mod=mod golang:1.26 sh -c 'go vet ./... && go test ./...')
COPYFILE_DISABLE=1 tar --no-xattrs --no-mac-metadata --exclude=./.tools --exclude=./bin --exclude=./dashboard --exclude=./.git -cf - . | docker cp - $C:/src
docker start -a $C; docker rm $C
```
Use an isolated data directory while developing: `export ZEROSETUP_HOME=$PWD/.tools/zs-home`.

## Dashboard dev server
```bash
./bin/zerosetup dashboard --port 4316 --no-open   # copy the token
cd dashboard && npm run dev                       # open http://localhost:5173/#token=<token>
```
The Vite proxy forwards `/api` with same-origin headers.

## Layout
See ARCHITECTURE.md. Fixtures live in `fixtures/`. Integration tests are in `tests/integration` (build tag `integration`).

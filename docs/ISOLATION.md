# Stronger Isolation: Design (not implemented)

Status: **design only**. Nothing in this document is implemented or claimed. It explains how the
two isolation items on the roadmap would be built, and why they weren't built in the current
environment.

## 1. MicroVM backend (`runtime.Backend`)

**Goal:** one lightweight VM per session, so a container-escape bug cannot reach the host or
other sessions.

**Shape:** a new package `internal/runtime/microvm` implementing `runtime.Backend`:
- `EnsureImage`: pull the same OCI image, then unpack it into a read-only ext4 root plus a writable overlay disk per session.
- `Create/Start`: boot a guest kernel with a tiny init that runs the step argv as uid 1000. Each step is a process in the session VM, not a new VM. Limits map to vCPU/memory.
- `CopyTo`: write the workspace tar into the session's data disk before boot.
- `Exec`: run over a vsock agent with a fixed command vocabulary (`cat /proc/net/tcp*`, service readiness commands). No shell.
- Port publishing: host-side vsock-to-127.0.0.1 forwarder.
- Labels/ownership: VM state directory under `~/.zerosetup/vms/<session>` with a manifest file. `ListOwned` reads these.

**Candidates:** Firecracker or Cloud Hypervisor on Linux (KVM). On macOS, Apple's Virtualization.framework via a small signed helper.

**Why not built here:** it needs KVM (Linux) or a code-signed entitlement (macOS). Neither was available, and the result couldn't be tested. Per project rules, it isn't claimed.

**Acceptance:** the same integration suite (`tests/integration`) passes with `ZEROSETUP_BACKEND=microvm`, plus an escape-resistance test (a process inside cannot read a host canary file or reach the host's loopback).

## 2. Egress filtering

**Goal:** dependency installs can reach package registries only, and the running app has no outbound access unless allowed.

**Shape:**
1. The session network becomes `internal: true` (no default route).
2. A ZEROSETUP-owned forward proxy container is attached to both the internal network and an egress network. It allowlists CONNECT targets (`registry.npmjs.org`, `pypi.org`, `files.pythonhosted.org`, the GitHub codeload hosts for git dependencies).
3. Install steps get `HTTPS_PROXY`/`HTTP_PROXY`. The app gets no proxy unless `--allow-egress <host>` is given.
4. The plan's `network.outboundFiltered` becomes `true` **only** when the proxy is running and a self-test (a blocked host fails, an allowed host works) has passed in that session.

**Open problem:** published ports don't work on `internal` networks. The app would need a second, ingress-only network with no gateway route, or a host-side forwarder. This must be solved, and covered by a test, before the feature is claimed.

**Acceptance:** an integration fixture that tries `curl https://example.com` during install and at runtime, and asserts both fail while `npm ci` succeeds.

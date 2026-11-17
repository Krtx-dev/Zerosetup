# Security Policy

## Reporting a vulnerability
Please report vulnerabilities privately through GitHub Security Advisories on this repository ("Report a vulnerability"). Don't open public issues for security problems. We aim to acknowledge within 5 business days.

In scope: escaping the documented controls (approval gate, policy, container hardening, loopback-only API, token/Host/Origin checks, secret redaction, passport scrubbing), and inspection executing repository code.

## What ZEROSETUP does and does not guarantee
See [docs/THREAT_MODEL.md](docs/THREAT_MODEL.md). Summary:
- It **does** enforce: no code execution during inspection, approval bound to a plan hash, non-root containers with all capabilities dropped, read-only rootfs, resource limits, no host mounts, no Docker socket, loopback-only previews and API.
- It **does not** provide a strong sandbox against hostile code, and **does not** filter outbound network traffic.

## Safe usage
- Only run repositories you're willing to let execute code with internet access.
- Never pass real production credentials as `--secret` to untrusted code.
- On macOS, restrict your container VM's host mounts (see docs/DEVELOPMENT.md).
- Remove sessions you no longer need (`zerosetup remove`).

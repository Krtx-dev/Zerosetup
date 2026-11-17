# Research & Positioning

> Status: initial pass, 2026-11-05. Web search for current competitor documentation returned mostly secondary sources. **Re-verify each row against official docs before public launch.**

| Tool | What it is | Overlap | How ZEROSETUP differs |
|---|---|---|---|
| Dev Containers spec | Declarative dev environment spec (`devcontainer.json`) | Environment definition | ZEROSETUP works when no spec exists. It analyses existing specs but does not trust their lifecycle commands |
| GitHub Codespaces | Hosted Dev Container environments | Run a repo without local setup | Local-first, no account, explicit plan + approval, readiness verification |
| Daytona | Dev environment / sandbox platform built around Dev Containers, now focused on AI-agent sandboxes | Workspaces for agents | Explainable detection with evidence, readiness facts, passports. No hosted service |
| Railpack (successor to Nixpacks, beta since March 2026 per secondary sources) | Builds images from source with auto-detection | Framework detection | Railpack produces deployable images. ZEROSETUP runs dev environments, explains its decisions, verifies HTTP readiness and diagnoses failures |
| Dagger | Programmable container pipelines | Container orchestration | ZEROSETUP is not a CI engine. It is an end-user "run this repo" tool |
| Nix | Reproducible package management | Reproducibility | ZEROSETUP records provenance (digests, lockfile hashes) instead of requiring Nix expressions |
| Docker Compose | Multi-container definitions | Running services | ZEROSETUP does not execute repo Compose files (security). It generates hardened single-app environments |

**Positioning:** "An open-source, local-first way to inspect, run, and reproduce supported GitHub projects."

## Name check (ZEROSETUP)
- npm: a package named **`zerosetup`** exists. It is a Windows batch-script setup generator, unrelated. This blocks publishing an npm package with that exact name and could cause search confusion. ([pkgstats](https://www.pkgstats.com/pkg:zerosetup))
- GitHub: `4m3rr0r/zero-setup` is an unrelated Linux security-tool installer script.
- Domains and trademarks: **not checked**. This needs a manual check before launch.
- Decision: keep the name for now and document the collision. The owner should decide before launch.

Sources: [pkgstats zerosetup](https://www.pkgstats.com/pkg:zerosetup), [ecosyste.ms zero-setup](https://awesome.ecosyste.ms/projects/github.com%2F4m3rr0r%2Fzero-setup), [Nixpacks vs Railpack](https://www.bitdoze.com/md/nixpacks-vs-railpack.md), [Daytona overview](https://startups.fm/startups/daytona).

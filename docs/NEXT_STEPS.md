# Next Steps

Everything that can be built and verified on this machine without new permissions is done. What's left needs an owner decision or other infrastructure:

1. **Choose the name** (NAME-1). An npm package `zerosetup` exists. Check domains and trademarks.
2. **Create a GitHub repository and push** (CI-1). Then CI runs Linux unit + integration tests, which also covers PLAT-1 for Linux.
3. **Run GoReleaser** (REL-1): `goreleaser release --snapshot --clean` locally, or in CI. Publishing a release still needs explicit approval.
4. **Record the GitHub demo** (DEMO-1): `docs/LAUNCH.md` has the storyboard. Running a third-party repo outside the test suite needs your approval in this environment.
5. **macOS Intel** run of `make check test-integration`.
6. **Stronger isolation** (ISO-1, EGRESS-1): implement per docs/ISOLATION.md on a Linux host with KVM.

#!/usr/bin/env bash
# Regenerates docs/CLI.md from the real --help output of bin/zerosetup.
set -euo pipefail
bin="${1:-bin/zerosetup}"
{
  echo "# CLI Reference"
  echo
  echo 'Generated from `zerosetup --help` output by `make docs-cli`. Do not edit by hand.'
  echo
  echo "## Exit codes"
  echo
  echo "| Code | Meaning |"
  echo "|---|---|"
  echo "| 0 | success |"
  echo "| 1 | error |"
  echo "| 2 | usage error |"
  echo "| 3 | repository unsupported / replay checks failed |"
  echo "| 4 | approval required or mismatched |"
  echo "| 5 | container engine unavailable / doctor failure |"
  echo "| 6 | execution failed or app not ready |"
  echo "| 130 | cancelled |"
  echo
  for c in "" inspect plan run replay ps status logs stop restart remove export repair dashboard config cache doctor version; do
    echo "## zerosetup $c"
    echo
    echo '```text'
    # shellcheck disable=SC2086
    "$bin" $c --help 2>&1 | sed 's/[[:space:]]*$//'
    echo '```'
    echo
  done
} > docs/CLI.md

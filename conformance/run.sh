#!/usr/bin/env bash
# Runs the official MCP conformance suite once per protocol revision: its
# server scenarios against the fixture server in ./server, and its client
# scenarios against the fixture client in ./client, each against that
# revision's expected-failures baseline. Exits non-zero if any run has an
# unexpected failure or a baseline entry that now passes.
#
#   conformance/run.sh                         # both suites, every revision
#   SPEC_VERSIONS=2026-07-28 conformance/run.sh
#   SUITES=client conformance/run.sh           # server, client or both
set -euo pipefail

SUITE="@modelcontextprotocol/conformance@${CONFORMANCE_VERSION:-0.2.0-alpha.12}"
PORT="${PORT:-3001}"
SPEC_VERSIONS="${SPEC_VERSIONS:-2025-03-26 2025-06-18 2025-11-25 2026-07-28}"
SUITES="${SUITES:-server client}"
URL="http://localhost:${PORT}/mcp"

root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'kill "${pid:-}" 2>/dev/null || true; rm -rf "$tmp"' EXIT

go build -C "$root" -o "$tmp/server" ./conformance/server
go build -C "$root" -o "$tmp/client" ./conformance/client

status=0
case " $SUITES " in *" server "*)
  "$tmp/server" -addr "localhost:${PORT}" &
  pid=$!
  for _ in $(seq 1 50); do
    if curl -s -o /dev/null "$URL"; then break; fi
    sleep 0.1
  done
  for v in $SPEC_VERSIONS; do
    echo "=== MCP conformance (server): $v"
    npx -y "$SUITE" server --url "$URL" --spec-version "$v" --suite all \
      --expected-failures "$root/conformance/expected-failures/$v.yml" \
      -o "${OUTPUT_DIR:-$tmp/results}/server/$v" || status=1
  done
  kill "$pid" 2>/dev/null || true
  ;;
esac

case " $SUITES " in *" client "*)
  for v in $SPEC_VERSIONS; do
    echo "=== MCP conformance (client): $v"
    npx -y "$SUITE" client --command "$tmp/client" --spec-version "$v" --suite all \
      --expected-failures "$root/conformance/expected-failures/$v.yml" \
      -o "${OUTPUT_DIR:-$tmp/results}/client/$v" || status=1
  done
  ;;
esac
exit "$status"

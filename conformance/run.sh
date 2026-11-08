#!/usr/bin/env bash
# Runs the official MCP conformance suite against the fixture server in
# ./server, once per protocol revision, each against its expected-failures
# baseline. Exits non-zero if any revision has an unexpected failure or a
# baseline entry that now passes.
#
#   conformance/run.sh                 # every revision
#   SPEC_VERSIONS=2026-07-28 conformance/run.sh
set -euo pipefail

SUITE="@modelcontextprotocol/conformance@${CONFORMANCE_VERSION:-0.2.0-alpha.12}"
PORT="${PORT:-3001}"
SPEC_VERSIONS="${SPEC_VERSIONS:-2025-03-26 2025-06-18 2025-11-25 2026-07-28}"
URL="http://localhost:${PORT}/mcp"

root="$(cd "$(dirname "$0")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'kill "${pid:-}" 2>/dev/null || true; rm -rf "$tmp"' EXIT

go build -C "$root" -o "$tmp/server" ./conformance/server
"$tmp/server" -addr "localhost:${PORT}" &
pid=$!

for _ in $(seq 1 50); do
  if curl -s -o /dev/null "$URL"; then break; fi
  sleep 0.1
done

status=0
for v in $SPEC_VERSIONS; do
  echo "=== MCP conformance: $v"
  npx -y "$SUITE" server --url "$URL" --spec-version "$v" --suite all \
    --expected-failures "$root/conformance/expected-failures/$v.yml" \
    -o "${OUTPUT_DIR:-$tmp/results}/$v" || status=1
done
exit "$status"

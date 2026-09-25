#!/usr/bin/env bash
#
# Browser tests: starts a disposable write-mode dev instance (agent against a
# copy of the fixture, system commands simulated) and runs every *.js test
# here with Playwright (Chromium).
set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PORT="${E2E_PORT:-8097}"
export E2E_BASE="http://127.0.0.1:$PORT" E2E_OUT="${E2E_OUT:-$(mktemp -d)}"
mkdir -p "$E2E_OUT"
rm -rf "$ROOT/.dev"
# Own process group, so agent and PHP server stop with it.
setsid bash -c "cd '$ROOT' && DEV_WRITE=1 DEV_PORT=$PORT exec scripts/dev.sh" > "$E2E_OUT/dev.log" 2>&1 &
DEV=$!
trap 'kill -- -$DEV 2>/dev/null; true' EXIT
for _ in $(seq 1 120); do curl -sf -o /dev/null "$E2E_BASE/login" && break; sleep 1; done
curl -sf -o /dev/null "$E2E_BASE/login" || { tail -30 "$E2E_OUT/dev.log"; exit 1; }
export NODE_PATH="${NODE_PATH:-$(npm root -g)}"
for t in "$ROOT"/tests/e2e/*.js; do
    echo "== $(basename "$t")"
    node "$t"
done
echo "Screenshots and logs: $E2E_OUT"

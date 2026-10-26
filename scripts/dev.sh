#!/usr/bin/env bash
#
# Runs WhaCP locally without root: the agent inspects the fixture server in
# agent/tests/fixtures/ubuntu-host (or DEV_ROOT) and the Go web server is
# served on http://127.0.0.1:${DEV_PORT:-8090}. State lives in .dev/.
#
#   scripts/dev.sh            start agent + server (Ctrl-C stops both)
#   DEV_ROOT=/ scripts/dev.sh inspect this machine instead of the fixture
#   DEV_WRITE=1 scripts/dev.sh write mode against a disposable copy of the
#                              fixture in .dev/root (system tools are not run
#                              against your machine: see .dev/root)

set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEV="$ROOT/.dev"
PORT="${DEV_PORT:-8090}"
FIXTURE="${DEV_ROOT:-$ROOT/agent/tests/fixtures/ubuntu-host}"
WRITE="${DEV_WRITE:-0}"

mkdir -p "$DEV"
if [[ $WRITE == 1 ]]; then
    if [[ ${DEV_ROOT:-} == / ]]; then echo "DEV_WRITE with DEV_ROOT=/ is refused; use a disposable machine and the installer." >&2; exit 1; fi
    rm -rf "$DEV/root" && cp -a "$FIXTURE" "$DEV/root"
    mkdir -p "$DEV/root"/{home/whacp,var/lib/whacp-agent,var/backups/whacp,run/php,etc/cron.d,etc/systemd/system}
    FIXTURE="$DEV/root"
fi
[[ -f $DEV/token ]] || (umask 077; openssl rand -hex 32 > "$DEV/token")
cat > "$DEV/agent.json" <<JSON
{
  "socket_path": "$DEV/agent.sock",
  "socket_group": "",
  "token_file": "$DEV/token",
  "root_dir": "$FIXTURE",
  "allow_write_actions": $([[ $WRITE == 1 ]] && echo true || echo false),
  "simulate_commands": $([[ $WRITE == 1 ]] && echo true || echo false),
  "state_dir": "/var/lib/whacp-agent",
  "log_level": "debug",
  "metrics_interval": "3s"
}
JSON
cat > "$DEV/server.env" <<ENV
WHACP_LISTEN=127.0.0.1:$PORT
WHACP_DB_PATH=$DEV/whacp.sqlite
WHACP_LOG_FILE=$DEV/server.log
WHACP_KEY_FILE=$DEV/server.key
WHACP_AGENT_SOCKET=$DEV/agent.sock
WHACP_AGENT_TOKEN_FILE=$DEV/token
ENV
umask 077
[[ -f $DEV/server.key ]] || openssl rand -hex 32 > "$DEV/server.key"

echo "Building agent + server…"
(cd "$ROOT/agent" && go build -o "$DEV/whacp-agent" ./cmd/whacp-agent)
(cd "$ROOT/server" && go build -o "$DEV/whacp" ./cmd/whacp)

# Set up env so bootstrap-admin can find the DB/key.
mkdir -p "$DEV/sessions"
set -a; . "$DEV/server.env"; set +a

"$DEV/whacp-agent" -config "$DEV/agent.json" > "$DEV/agent.log" 2>&1 &
AGENT_PID=$!
# shellcheck disable=SC2064
trap "kill $AGENT_PID 2>/dev/null || true" EXIT
for _ in $(seq 1 20); do [[ -S $DEV/agent.sock ]] && break; sleep 0.2; done

# Seed an admin user on first run. The server auto-migrates on startup.
if ! "$DEV/whacp" bootstrap-admin --username admin --email admin@example.com --role admin <<<"dev-password-change-me" 2>/dev/null; then
    : # admin already exists (or server will create it on first run)
fi

echo "Server: http://127.0.0.1:$PORT/  (agent log: .dev/agent.log, server log: .dev/server.log)"
echo "Dev user: admin / dev-password-change-me"
exec "$DEV/whacp"

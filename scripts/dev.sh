#!/usr/bin/env bash
#
# Runs WhaCP locally without root: the agent inspects the fixture server in
# agent/tests/fixtures/ubuntu-host (or DEV_ROOT) and the panel is served on
# http://127.0.0.1:${DEV_PORT:-8090}. State lives in .dev/.
#
#   scripts/dev.sh            start agent + panel (Ctrl-C stops both)
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
cat > "$DEV/panel.php" <<PHP
<?php
return [
    'app' => ['env' => 'development', 'debug' => true],
    'database' => ['path' => '$DEV/whacp.sqlite'],
    'log' => ['file' => '$DEV/panel.log', 'level' => 'debug'],
    'session' => ['path' => '$DEV/sessions', 'secure_cookie' => false],
    'agent' => ['socket' => '$DEV/agent.sock', 'token_file' => '$DEV/token'],
];
PHP

echo "Building agent…"
(cd "$ROOT/agent" && go build -o "$DEV/whacp-agent" ./cmd/whacp-agent)
[[ -f $ROOT/panel/vendor/autoload.php ]] || (cd "$ROOT/panel" && composer install -q)

export WHACP_CONFIG="$DEV/panel.php"
php "$ROOT/panel/bin/whacp" migrate
if [[ $(php "$ROOT/panel/bin/whacp" users:count) == 0 ]]; then
    echo "dev-password-change-me" | php "$ROOT/panel/bin/whacp" user:create admin admin@example.com --role=admin
    echo "Created dev user: admin / dev-password-change-me"
fi

"$DEV/whacp-agent" -config "$DEV/agent.json" > "$DEV/agent.log" 2>&1 &
AGENT_PID=$!
trap 'kill $AGENT_PID 2>/dev/null || true' EXIT
for _ in $(seq 1 20); do [[ -S $DEV/agent.sock ]] && break; sleep 0.2; done
php "$ROOT/panel/bin/whacp" agent:check

echo "Panel: http://127.0.0.1:$PORT/  (agent log: .dev/agent.log, panel log: .dev/panel.log)"
php -S "127.0.0.1:$PORT" -t "$ROOT/panel/public" "$ROOT/panel/public/router.php"

#!/usr/bin/env bash
#
# Upgrade/rollback test: builds a fake installation of the previous version
# in a temp dir (systemctl and runuser are shims that run the real agent
# and a PHP panel server), then checks that
#   1. a failing migration rolls back to the old files and database,
#   2. a good upgrade migrates, restarts and passes health checks,
#   3. --rollback restores the previous version.

set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
T="$(mktemp -d)"
PORT="${UPGRADE_PORT:-18190}"
cleanup() { [[ -f $T/pids ]] && xargs -r kill 2>/dev/null <"$T/pids" || true; pkill -f "$T/" 2>/dev/null || true; rm -rf "$T"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; tail -20 "$T/log" 2>/dev/null >&2 || true; exit 1; }
pass() { echo "  ok  $*"; }

export WHACP_TEST=1 WHACP_PREFIX="$T/opt/whacp" WHACP_CONF_DIR="$T/etc/whacp" WHACP_BIN="$T/bin/whacp-agent" \
    WHACP_UNIT_DIR="$T/units" WHACP_BACKUP_ROOT="$T/backups" WHACP_AGENT_SOCKET="$T/run/agent.sock" \
    WHACP_PANEL_USER="$(id -un)" WHACP_REBUILD=0
mkdir -p "$T"/{opt/whacp,etc/whacp,bin,units,backups,run,var,shims}

# Source trees: "old" (0.4.0, without the newest migration) and "new".
copy_tree() { mkdir -p "$1"; (cd "$ROOT" && tar --exclude=.git --exclude=.dev --exclude=dist -cf - .) | tar -C "$1" -xf -; }
copy_tree "$T/new"
mkdir -p "$T/new/dist"
(cd "$ROOT/agent" && go build -o "$T/new/dist/whacp-agent" ./cmd/whacp-agent)
LAST=$(ls "$T/new/panel/database/migrations" | sort | tail -1)

# Fake installed old version.
cp -a "$T/new/panel" "$T/opt/whacp/panel"
rm "$T/opt/whacp/panel/database/migrations/$LAST"
echo "0.4.0" > "$T/opt/whacp/panel/VERSION"
cp "$T/new/dist/whacp-agent" "$T/bin/whacp-agent"
(umask 077; openssl rand -hex 32 > "$T/etc/whacp/agent.token"; openssl rand -base64 32 > "$T/etc/whacp/panel.key")
cat > "$T/etc/whacp/agent.json" <<JSON
{"socket_path":"$T/run/agent.sock","socket_group":"","token_file":"$T/etc/whacp/agent.token","root_dir":"$ROOT/agent/tests/fixtures/ubuntu-host","metrics_interval":"1s"}
JSON
cat > "$T/etc/whacp/panel.php" <<PHP
<?php return ['database'=>['path'=>'$T/var/whacp.sqlite'],'log'=>['file'=>'$T/var/panel.log'],
'session'=>['path'=>'$T/var','secure_cookie'=>false],'agent'=>['socket'=>'$T/run/agent.sock','token_file'=>'$T/etc/whacp/agent.token'],
'security'=>['key_file'=>'$T/etc/whacp/panel.key']];
PHP
echo "ExecStart=/usr/bin/php -S 127.0.0.1:$PORT -t $T/opt/whacp/panel/public $T/opt/whacp/panel/public/router.php" > "$T/units/whacp-panel.service"

# Shims: runuser drops "-u USER --"; systemctl runs the real processes.
cat > "$T/shims/runuser" <<'SH'
#!/usr/bin/env bash
while [[ $1 != -- ]]; do shift; done; shift; exec "$@"
SH
cat > "$T/shims/systemctl" <<SH
#!/usr/bin/env bash
T=$T
start() {
  case "\$1" in
    whacp-agent.service) "\$WHACP_BIN" -config "\$T/etc/whacp/agent.json" >>"\$T/agent.log" 2>&1 & echo \$! > "\$T/agent.pid"; echo \$! >> "\$T/pids" ;;
    whacp-panel.service) WHACP_CONFIG=\$T/etc/whacp/panel.php php -S 127.0.0.1:$PORT -t "\$T/opt/whacp/panel/public" "\$T/opt/whacp/panel/public/router.php" >>"\$T/php.log" 2>&1 & echo \$! > "\$T/panel.pid"; echo \$! >> "\$T/pids" ;;
  esac
}
stop() { case "\$1" in whacp-agent.service) f=agent ;; whacp-panel.service) f=panel ;; *) return 0 ;; esac; [[ -f \$T/\$f.pid ]] && kill "\$(cat \$T/\$f.pid)" 2>/dev/null; rm -f "\$T/\$f.pid"; sleep 0.3; }
cmd=\$1; shift
[[ \$cmd == is-active ]] && { [[ \$1 == --quiet ]] && shift; f=\${1%%.service}; f=\${f#whacp-}; [[ -f \$T/\$f.pid ]] && kill -0 "\$(cat \$T/\$f.pid)" 2>/dev/null; exit; }
for u in "\$@"; do case \$cmd in stop) stop "\$u" ;; start) start "\$u" ;; restart) stop "\$u"; start "\$u" ;; esac; done
exit 0
SH
chmod +x "$T/shims/"*
export PATH="$T/shims:$PATH"

cli() { WHACP_CONFIG="$T/etc/whacp/panel.php" php "$T/opt/whacp/panel/bin/whacp" "$@"; }
cli migrate >/dev/null
systemctl start whacp-agent.service whacp-panel.service
sleep 1
tables() { php -r '$p = new PDO("sqlite:" . $argv[1]); echo implode(",", $p->query("SELECT name FROM sqlite_master WHERE type=\"table\" ORDER BY name")->fetchAll(PDO::FETCH_COLUMN));' "$T/var/whacp.sqlite"; }
OLD_TABLES=$(tables)
echo "correct-horse-staple-42" | cli user:create keep keep@example.test >/dev/null
pass "old version installed and running"

# 1. A broken migration must roll everything back.
copy_tree "$T/broken"; cp "$T/new/dist/whacp-agent" "$T/broken/dist/" 2>/dev/null || { mkdir -p "$T/broken/dist"; cp "$T/new/dist/whacp-agent" "$T/broken/dist/"; }
echo '<?php return ["CREATE TABLE should_not_exist (x INTEGER)", "THIS IS NOT SQL"];' > "$T/broken/panel/database/migrations/9999_broken.php"
if "$T/broken/installer/upgrade.sh" --yes >"$T/log" 2>&1; then fail "a failing upgrade reported success"; fi
grep -q "WhaCP 0.4.0 was restored" "$T/log" || fail "no rollback message"
[[ $(cat "$T/opt/whacp/panel/VERSION") == 0.4.0 ]] || fail "old files not restored"
[[ $(tables) == "$OLD_TABLES" ]] || fail "database not restored: $(tables)"
cli users:count | grep -q '^1$' || fail "user data lost"
curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null || fail "old panel not running after rollback"
pass "failed upgrade rolled back files and database, panel healthy"

# 2. A good upgrade.
"$T/new/installer/upgrade.sh" --yes >"$T/log" 2>&1 || fail "upgrade failed"
[[ $(cat "$T/opt/whacp/panel/VERSION") == $(cat "$ROOT/VERSION") ]] || fail "new version not installed"
tables | grep -q "$(php -r '$m = require $argv[1]; preg_match("/CREATE TABLE (\w+)/", implode(" ", $m), $x); echo $x[1];' "$T/new/panel/database/migrations/$LAST")" || fail "new migration not applied"
cli users:count | grep -q '^1$' || fail "user data lost"
curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null || fail "new panel not answering"
[[ $(ls "$T/backups" | wc -l) -ge 2 ]] || fail "backups missing"
pass "upgrade migrated the database and passed health checks"

# 3. Manual rollback.
"$T/new/installer/upgrade.sh" --rollback --yes >"$T/log" 2>&1 || fail "rollback failed"
[[ $(cat "$T/opt/whacp/panel/VERSION") == 0.4.0 ]] || fail "rollback did not restore 0.4.0"
[[ $(tables) == "$OLD_TABLES" ]] || fail "rollback did not restore the database"
pass "--rollback restored the previous version"
echo "Upgrade test passed."

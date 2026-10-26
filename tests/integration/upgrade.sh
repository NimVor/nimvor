#!/usr/bin/env bash
#
# Upgrade/rollback test: builds the current Go agent and server into a fake
# installation in a temp dir (systemctl and runuser are shims that launch the
# real binaries), then checks that
#   1. a failing migration rolls back to the old binary and database,
#   2. a good upgrade restarts, applies migrations, preserves the admin user
#      and a seeded website row, and passes /healthz,
#   3. --rollback restores the previous binary and DB.

set -Eeuo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
T="$(mktemp -d)"
PORT="${UPGRADE_PORT:-18190}"
cleanup() { [[ -f $T/pids ]] && xargs -r kill 2>/dev/null <"$T/pids" || true; pkill -f "$T/" 2>/dev/null || true; rm -rf "$T"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; tail -30 "$T/log" 2>/dev/null >&2; [[ -f $T/panel.log ]] && tail -20 "$T/panel.log" >&2; exit 1; }
pass() { echo "  ok  $*"; }

export WHACP_TEST=1 WHACP_PREFIX="$T/opt/whacp" WHACP_CONF_DIR="$T/etc/whacp" WHACP_BIN_AGENT="$T/bin/whacp-agent" \
    WHACP_BIN_PANEL="$T/bin/whacp" WHACP_UNIT_DIR="$T/units" WHACP_BACKUP_ROOT="$T/backups" \
    WHACP_PANEL_USER="$(id -un)" WHACP_REBUILD=0
mkdir -p "$T"/{opt/whacp,etc/whacp,bin,units,backups,run,var/lib/whacp,var/log/whacp,shims}

# "new" = the tree we're testing from; copy it and build the Go binaries.
copy_tree() { mkdir -p "$1"; (cd "$ROOT" && tar --exclude=.git --exclude=.dev --exclude=dist -cf - .) | tar -C "$1" -xf -; }
copy_tree "$T/new"
mkdir -p "$T/new/dist"
(cd "$ROOT/agent"  && go build -o "$T/new/dist/whacp-agent" ./cmd/whacp-agent)
(cd "$ROOT/server" && go build -o "$T/new/dist/whacp"       ./cmd/whacp)

# "old" = the same tree but with its latest SQL migration removed, so the
# first start of the new binary has a real migration to apply.
copy_tree "$T/old"
mkdir -p "$T/old/dist"
cp "$T/new/dist/whacp-agent" "$T/old/dist/whacp-agent"
cp "$T/new/dist/whacp"       "$T/old/dist/whacp"
LAST=$(ls "$T/new/server/internal/database/migrations"/*.sql 2>/dev/null | sort | tail -1)
LAST_BN=$(basename "$LAST")
rm -f "$T/old/server/internal/database/migrations/$LAST_BN"
OLDV="0.4.0"
NEWV=$(cat "$ROOT/VERSION")

# Write an initial old-version installation.
cp "$T/old/dist/whacp-agent" "$T/bin/whacp-agent"
cp "$T/old/dist/whacp"       "$T/bin/whacp"
(umask 077; openssl rand -hex 32 > "$T/etc/whacp/agent.token"; openssl rand -base64 32 > "$T/etc/whacp/panel.key")
DB="$T/var/lib/whacp/whacp.sqlite"
LOG="$T/var/log/whacp/panel.log"
cat > "$T/etc/whacp/agent.json" <<JSON
{"socket_path":"$T/run/agent.sock","socket_group":"","token_file":"$T/etc/whacp/agent.token","root_dir":"$ROOT/agent/tests/fixtures/ubuntu-host","metrics_interval":"1s"}
JSON
cat > "$T/etc/whacp/server.env" <<ENV
WHACP_LISTEN=127.0.0.1:$PORT
WHACP_DB_PATH=$DB
WHACP_LOG_FILE=$LOG
WHACP_KEY_FILE=$T/etc/whacp/panel.key
WHACP_AGENT_SOCKET=$T/run/agent.sock
WHACP_AGENT_TOKEN_FILE=$T/etc/whacp/agent.token
WHACP_PRODUCTION=0
ENV

# Shims: runuser drops "-u USER --"; systemctl starts/stops the real
# whacp-agent and whacp binaries directly.
cat > "$T/shims/runuser" <<'SH'
#!/usr/bin/env bash
while [[ $1 != -- ]]; do shift; done; shift; exec "$@"
SH
cat > "$T/shims/systemctl" <<SH
#!/usr/bin/env bash
T=\$T
start() {
  case "\$1" in
    whacp-agent.service) "\$T/bin/whacp-agent" -config "\$T/etc/whacp/agent.json" >>"\$T/agent.log" 2>&1 & echo \$! > "\$T/agent.pid"; echo \$! >> "\$T/pids" ;;
    whacp-panel.service)
        set -a; . "\$T/etc/whacp/server.env"; set +a
        "\$T/bin/whacp" >>"\$T/panel.stdout" 2>&1 & echo \$! > "\$T/panel.pid"; echo \$! >> "\$T/pids" ;;
  esac
}
stop() {
  case "\$1" in
    whacp-agent.service) f=agent ;;
    whacp-panel.service) f=panel ;;
    *) return 0 ;;
  esac
  [[ -f \$T/\$f.pid ]] && kill "\$(cat \$T/\$f.pid)" 2>/dev/null; rm -f "\$T/\$f.pid"; sleep 0.5
}
cmd=\$1; shift
[[ \$cmd == is-active ]] && { [[ \$1 == --quiet ]] && shift; f=\${1%%.service}; f=\${f#whacp-}; [[ -f \$T/\$f.pid ]] && kill -0 "\$(cat \$T/\$f.pid)" 2>/dev/null; exit; }
[[ \$cmd == daemon-reload ]] && exit 0
for u in "\$@"; do case \$cmd in stop) stop "\$u" ;; start) start "\$u" ;; restart) stop "\$u"; start "\$u" ;; try-restart) start "\$u" ;; enable) ;; esac; done
exit 0
SH
chmod +x "$T/shims/"*
export PATH="$T/shims:$PATH"

# Boot the "old" installation: let whacp auto-migrate up to the old
# schema, then seed an admin and a website row via sqlite3 so we can
# verify they survive the upgrade.
systemctl start whacp-agent.service whacp-panel.service
for _ in $(seq 1 100); do curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "http://127.0.0.1:$PORT/healthz" | grep -q '"status":"ok"' || fail "old panel not answering"
echo "upgrade-seed-2468" | "$T/bin/whacp" bootstrap-admin --username upgrade-admin --email up@example.test --role admin >/dev/null
# Seed a website row directly via sqlite3 (mimics what populate.php did).
sqlite3 "$DB" "INSERT INTO websites (domain, root, kind, php_version, created_at, updated_at) VALUES ('seeded.example.test', '$T/var/seeded', 'php', '8.3', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z');"
OLD_USERS=$(sqlite3 "$DB" "SELECT COUNT(*) FROM users;")
OLD_SITES=$(sqlite3 "$DB" "SELECT COUNT(*) FROM websites;")
OLD_TABLES=$(sqlite3 "$DB" "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;" | tr '\n' ',')
echo "$OLDV" > "$T/opt/whacp/VERSION"
pass "old version $OLDV installed and running ($OLD_USERS users, $OLD_SITES sites)"

# 1. A broken migration must roll everything back. To inject a broken
# migration without rebuilding, copy the "new" tree and drop a malformed
# SQL file into its migrations directory.
copy_tree "$T/broken"
mkdir -p "$T/broken/dist"
cp "$T/new/dist/whacp-agent" "$T/broken/dist/whacp-agent"
cp "$T/new/dist/whacp"       "$T/broken/dist/whacp"
mkdir -p "$T/broken/server/internal/database/migrations"
echo "THIS IS NOT SQL;" > "$T/broken/server/internal/database/migrations/9999_broken.sql"
if "$T/broken/installer/upgrade.sh" --yes >"$T/log" 2>&1; then fail "a failing upgrade reported success"; fi
grep -q "restored" "$T/log" || { cat "$T/log"; fail "no rollback message"; }
[[ $(cat "$T/opt/whacp/VERSION") == "$OLDV" ]] || fail "old files not restored"
[[ $(sqlite3 "$DB" "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;" | tr '\n' ',') == "$OLD_TABLES" ]] || fail "database not restored"
[[ $(sqlite3 "$DB" "SELECT COUNT(*) FROM users;") == "$OLD_USERS" ]] || fail "user data lost"
curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null || fail "old panel not running after rollback"
pass "failed upgrade rolled back files and database, panel healthy"

# 2. A good upgrade applies migrations and preserves data.
"$T/new/installer/upgrade.sh" --yes >"$T/log" 2>&1 || { cat "$T/log"; fail "upgrade failed"; }
[[ $(cat "$T/opt/whacp/VERSION") == "$NEWV" ]] || fail "new version not installed (got $(cat "$T/opt/whacp/VERSION"))"
for _ in $(seq 1 100); do curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "http://127.0.0.1:$PORT/healthz" | grep -q '"status":"ok"' || fail "new panel not answering after upgrade"
NEW_TABLES=$(sqlite3 "$DB" "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;" | tr '\n' ',')
[[ $(sqlite3 "$DB" "SELECT COUNT(*) FROM users;") == "$OLD_USERS" ]] || fail "user data lost in upgrade"
[[ $(sqlite3 "$DB" "SELECT COUNT(*) FROM websites WHERE domain='seeded.example.test';") == 1 ]] || fail "seeded website lost in upgrade"
# The previously-missing migration (LAST_BN) must have run — its new table/column should exist.
grep -q "websites" <<<"$NEW_TABLES" || fail "websites table missing after migration"
pass "upgrade migrated the database; admin and seeded website intact; panel healthy"

# Sign in with the seeded admin (the password was set against the old binary
# and must still work because argon2id/bcrypt verification is compatible).
J="$T/cj"
page=$(curl -s -c "$J" -b "$J" "http://127.0.0.1:$PORT/login")
csrf=$(grep -o 'name="_token" value="[^"]*"' <<<"$page" | head -1 | sed 's/.*value="//;s/"$//')
[[ ${#csrf} == 64 ]] || fail "no CSRF token on post-upgrade login page"
loc=$(curl -s -b "$J" -c "$J" -o /dev/null -w '%{redirect_url}' \
    --data-urlencode "_token=$csrf" \
    -d 'username=upgrade-admin&password=upgrade-seed-2468' \
    "http://127.0.0.1:$PORT/login")
[[ $loc == "http://127.0.0.1:$PORT/" ]] || fail "the seeded administrator cannot sign in after upgrade (redirect '$loc')"
pass "seeded admin can sign in after upgrade (password hash survived)"

[[ $(ls "$T/backups" | wc -l) -ge 2 ]] || fail "backups missing"
pass "upgrade wrote versioned backups"

# 3. Manual rollback. Tampering with a backup must be refused; an intact
# backup is restored after a warning.
LATEST=$(ls -1d "$T/backups"/*/ | sort | tail -1)
[[ -s $LATEST/SHA256SUMS ]] || fail "the upgrade backup has no checksums"
cp "$LATEST/etc/agent.json" "$T/agent.json.orig"
echo " " >> "$LATEST/etc/agent.json"
if "$T/new/installer/upgrade.sh" --rollback --yes >"$T/log" 2>&1; then fail "a rollback from a changed backup succeeded"; fi
grep -q "does not match its checksums" "$T/log" || { cat "$T/log"; fail "the changed backup was not reported"; }
[[ $(cat "$T/opt/whacp/VERSION") == "$NEWV" ]] || fail "a refused rollback changed the installation"
curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null || fail "panel down after a refused rollback"
cp "$T/agent.json.orig" "$LATEST/etc/agent.json"
pass "a changed upgrade backup is refused before anything is restored"

"$T/new/installer/upgrade.sh" --rollback --yes >"$T/log" 2>&1 || { cat "$T/log"; fail "rollback failed"; }
grep -q "backup verified" "$T/log" || { cat "$T/log"; fail "rollback did not verify the backup"; }
[[ $(cat "$T/opt/whacp/VERSION") == "$OLDV" ]] || fail "rollback did not restore $OLDV"
for _ in $(seq 1 100); do curl -fsS "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "http://127.0.0.1:$PORT/healthz" | grep -q '"status":"ok"' || fail "rolled-back panel not answering"
[[ $(sqlite3 "$DB" "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name;" | tr '\n' ',') == "$OLD_TABLES" ]] || fail "rollback did not restore the database schema"
[[ $(sqlite3 "$DB" "SELECT COUNT(*) FROM users;") == "$OLD_USERS" ]] || fail "rollback lost users"
pass "--rollback restored the previous version and data"
echo "Upgrade test passed."

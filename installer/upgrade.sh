#!/usr/bin/env bash
#
# WhaCP upgrade and rollback.
#
#   sudo ./installer/upgrade.sh [--yes] [--allow-downgrade] [--wait=30m]   upgrade to this source tree
#   sudo ./installer/upgrade.sh --rollback [--yes]            restore the version before the last upgrade
#   sudo ./installer/upgrade.sh --list                        list upgrade backups
#
# An upgrade:
#   1. checks that the new agent accepts the existing /etc/whacp/agent.json,
#      and waits (up to --wait, default 30m; --wait=0 skips this) until the
#      agent has no running job, so a restore or deployment is never cut off;
#   2. backs up the panel files, agent binary, systemd units, /etc/whacp and
#      the panel database (taken with the panel stopped);
#   3. swaps in the new files, runs database migrations and restarts;
#   4. checks that the agent runs, the panel reaches it and answers /healthz.
# If any step after the swap fails, the previous version is restored
# automatically. Websites keep running throughout: only WhaCP's own
# services restart. The last three backups are kept.

set -Eeuo pipefail
umask 027

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Overridable for tests only; the defaults are the installed locations.
PREFIX="${WHACP_PREFIX:-/opt/whacp}"
CONF_DIR="${WHACP_CONF_DIR:-/etc/whacp}"
BIN="${WHACP_BIN:-/usr/local/bin/whacp-agent}"
UNIT_DIR="${WHACP_UNIT_DIR:-/etc/systemd/system}"
BACKUP_ROOT="${WHACP_BACKUP_ROOT:-/var/backups/whacp/upgrades}"
AGENT_SOCKET="${WHACP_AGENT_SOCKET:-/run/whacp/agent.sock}"
PANEL_USER="${WHACP_PANEL_USER:-whacp}"
KEEP=3
UNITS=(whacp-agent.service whacp-panel.service whacp-scheduler.service whacp-scheduler.timer)

ASSUME_YES=0
ROLLBACK=0
LIST=0
ALLOW_DOWNGRADE=0
JOB_WAIT=30m

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m  ✓\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m  !\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; exit 1; }
confirm() {
    [[ $ASSUME_YES -eq 1 ]] && return 0
    local reply
    read -r -p "$1 [y/N] " reply
    [[ $reply =~ ^[Yy]$ ]]
}

for arg in "$@"; do
    case "$arg" in
        --yes|-y) ASSUME_YES=1 ;;
        --rollback) ROLLBACK=1 ;;
        --list) LIST=1 ;;
        --allow-downgrade) ALLOW_DOWNGRADE=1 ;;
        --wait=*) JOB_WAIT=${arg#--wait=} ;;
        -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
        *) die "unknown option: $arg" ;;
    esac
done

panel_cli() { runuser -u "$PANEL_USER" -- env WHACP_CONFIG="$CONF_DIR/panel.php" php "$PREFIX/panel/bin/whacp" "$@"; }
db_path() { php -r '$c = require $argv[1]; echo $c["database"]["path"] ?? "/var/lib/whacp/whacp.sqlite";' "$CONF_DIR/panel.php"; }
installed_version() { cat "$PREFIX/panel/VERSION" 2>/dev/null || echo "0.4.0"; }

[[ $JOB_WAIT =~ ^([0-9]+[smh])+$|^0$ ]] || die "--wait needs a duration such as 30m, 2h or 0"
[[ $EUID -eq 0 || -n ${WHACP_TEST:-} ]] || die "run as root (sudo)"

if [[ $LIST -eq 1 ]]; then
    ls -1 "$BACKUP_ROOT" 2>/dev/null || echo "no upgrade backups"
    exit 0
fi

[[ -d $PREFIX/panel && -x $BIN && -f $CONF_DIR/panel.php ]] || die "WhaCP is not installed here; use installer/install.sh"

# wait_jobs <agent binary>: wait until the running agent has no queued or
# running background job (restores, deployments, backups), so restarting it
# does not stop one half-way. Returns 1 if jobs are still running.
wait_jobs() {
    local rc=0
    if [[ $JOB_WAIT == 0 ]]; then
        warn "not waiting for running jobs (--wait=0): a running restore or deployment would be interrupted"
        return 0
    fi
    "$1" -config "$CONF_DIR/agent.json" -wait-jobs "$JOB_WAIT" || rc=$?
    case $rc in
        0) return 0 ;;
        2) warn "this agent cannot list running jobs; not waiting for them"; return 0 ;;
        3) warn "background jobs are still running after $JOB_WAIT"; return 1 ;;
        *) warn "could not ask the agent for its running jobs"; return 1 ;;
    esac
}

stop_services() {
    systemctl stop whacp-scheduler.timer whacp-panel.service 2>/dev/null || true
}

start_services() {
    systemctl daemon-reload
    systemctl restart whacp-agent.service
    systemctl restart whacp-panel.service
    systemctl start whacp-scheduler.timer 2>/dev/null || true
}

health() {
    local i
    systemctl is-active --quiet whacp-agent.service || { warn "whacp-agent is not running"; return 1; }
    for i in $(seq 1 20); do [[ -S $AGENT_SOCKET ]] && break; sleep 0.5; done
    panel_cli agent:check >/dev/null || { warn "the panel cannot reach the agent"; return 1; }
    local port
    port=$(sed -n 's/.*-S 127\.0\.0\.1:\([0-9]*\).*/\1/p' "$UNIT_DIR/whacp-panel.service" | head -1)
    for i in $(seq 1 20); do curl -fsS "http://127.0.0.1:${port:-8090}/healthz" >/dev/null 2>&1 && return 0; sleep 0.5; done
    warn "the panel does not answer on 127.0.0.1:${port:-8090}"
    return 1
}

# restore <backup dir>: put files, units, configuration and database back.
restore() {
    local b=$1 db
    log "Restoring $(basename "$b")"
    stop_services
    if [[ -d $b/panel ]]; then
        rm -rf "$PREFIX/panel.failed"
        [[ -d $PREFIX/panel ]] && mv "$PREFIX/panel" "$PREFIX/panel.failed"
        cp -a "$b/panel" "$PREFIX/panel"
        rm -rf "$PREFIX/panel.failed"
    fi
    [[ -f $b/whacp-agent ]] && install -m 0755 "$b/whacp-agent" "$BIN.restore" && mv -f "$BIN.restore" "$BIN"
    for u in "${UNITS[@]}"; do
        if [[ -f $b/units/$u ]]; then cp -a "$b/units/$u" "$UNIT_DIR/$u"; else rm -f "$UNIT_DIR/$u"; fi
    done
    if [[ -d $b/units/whacp-agent.service.d ]]; then
        rm -rf "$UNIT_DIR/whacp-agent.service.d"
        cp -a "$b/units/whacp-agent.service.d" "$UNIT_DIR/"
    fi
    [[ -d $b/etc ]] && cp -a "$b/etc/." "$CONF_DIR/"
    db=$(cat "$b/db-path" 2>/dev/null || true)
    if [[ -n $db && -f $b/db.sqlite ]]; then
        rm -f "$db-wal" "$db-shm"
        cp -a "$b/db.sqlite" "$db"
    fi
    start_services
    health && ok "restored and healthy" || warn "restored, but health checks still fail: journalctl -u whacp-agent -u whacp-panel"
}

if [[ $ROLLBACK -eq 1 ]]; then
    latest=$(ls -1d "$BACKUP_ROOT"/*/ 2>/dev/null | sort | tail -1)
    [[ -n $latest ]] || die "no upgrade backup found in $BACKUP_ROOT"
    confirm "Roll back to $(cat "$latest/version" 2>/dev/null) from $(basename "$latest")?" || die "aborted"
    log "Waiting for running jobs"
    wait_jobs "$BIN" || die "jobs are still running; nothing was changed. Try again later or pass --wait=<longer>"
    restore "${latest%/}"
    exit 0
fi

# ------------------------------------------------------------ preflight ----
OLD=$(installed_version)
NEW=$(cat "$SRC_DIR/VERSION")
log "Upgrading WhaCP $OLD → $NEW"
# Pre-releases sort before their release (1.0.0-rc1 < 1.0.0): "-" becomes
# "~", which version sort places before everything.
newest=$(printf '%s\n%s\n' "${OLD/-/\~}" "${NEW/-/\~}" | sort -V | tail -1)
if [[ $newest != "${NEW/-/\~}" && $ALLOW_DOWNGRADE -eq 0 ]]; then
    die "$NEW is older than the installed $OLD; use --rollback, or --allow-downgrade if you really mean it"
fi

AGENT_SRC=$SRC_DIR/dist/whacp-agent
if [[ ! -x $AGENT_SRC || ${WHACP_REBUILD:-1} == 1 && -n $(command -v go) ]]; then
    log "Building the agent"
    mkdir -p "$SRC_DIR/dist"
    (cd "$SRC_DIR/agent" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$NEW" -o "$AGENT_SRC" ./cmd/whacp-agent)
fi
[[ -x $AGENT_SRC ]] || die "no agent binary (dist/whacp-agent) and Go is not installed"
"$AGENT_SRC" -config "$CONF_DIR/agent.json" -check-config >/dev/null || die "the new agent rejects $CONF_DIR/agent.json; nothing was changed"
ok "new agent accepts the existing configuration"
[[ -f $SRC_DIR/panel/vendor/autoload.php ]] || die "panel/vendor is missing (run 'composer install --no-dev' in panel/)"
php -r 'exit(PHP_VERSION_ID >= 80300 ? 0 : 1);' || die "the panel needs PHP >= 8.3"
confirm "Upgrade now? WhaCP's panel is unavailable for a few seconds; websites are not affected." || die "aborted"
log "Waiting for running jobs"
wait_jobs "$AGENT_SRC" || die "jobs are still running; nothing was changed. Try again later or pass --wait=<longer>"
ok "no job is running"

# --------------------------------------------------------------- backup ----
TS=$(date -u +%Y%m%dT%H%M%SZ)
B="$BACKUP_ROOT/$TS-$OLD"
log "Backing up to $B"
install -d -m 0700 "$BACKUP_ROOT" "$B"
echo "$OLD" > "$B/version"
cp -a "$PREFIX/panel" "$B/panel"
cp -a "$BIN" "$B/whacp-agent"
install -d -m 0700 "$B/units" "$B/etc"
for u in "${UNITS[@]}"; do [[ -f $UNIT_DIR/$u ]] && cp -a "$UNIT_DIR/$u" "$B/units/"; done
[[ -d $UNIT_DIR/whacp-agent.service.d ]] && cp -a "$UNIT_DIR/whacp-agent.service.d" "$B/units/"
cp -a "$CONF_DIR/." "$B/etc/"
DB=$(db_path)
echo "$DB" > "$B/db-path"
stop_services
# A job may have started between the check and stopping the panel.
if ! wait_jobs "$AGENT_SRC"; then
    systemctl start whacp-panel.service whacp-scheduler.timer 2>/dev/null || true
    die "jobs are still running; nothing was changed. Try again later or pass --wait=<longer>"
fi
if [[ -f $DB ]]; then
    # Consistent copy: the panel and scheduler are stopped; fold the WAL in.
    php -r '$p = new PDO("sqlite:" . $argv[1]); $p->exec("PRAGMA wal_checkpoint(TRUNCATE)");' "$DB" 2>/dev/null || true
    cp -a "$DB" "$B/db.sqlite"
fi
ok "backup complete"

rollback_and_die() {
    trap - ERR
    warn "$1 — rolling back to $OLD"
    restore "$B"
    die "upgrade failed ($1); WhaCP $OLD was restored. Backup kept in $B"
}
trap 'rollback_and_die "unexpected error on line $LINENO"' ERR

# ---------------------------------------------------------------- apply ----
log "Installing $NEW"
rm -rf "$PREFIX/panel.new"
mkdir -p "$PREFIX/panel.new"
cp -a "$SRC_DIR/panel/app" "$SRC_DIR/panel/bin" "$SRC_DIR/panel/config" "$SRC_DIR/panel/database" \
    "$SRC_DIR/panel/public" "$SRC_DIR/panel/resources" "$SRC_DIR/panel/routes" "$SRC_DIR/panel/vendor" \
    "$SRC_DIR/panel/composer.json" "$SRC_DIR/VERSION" "$PREFIX/panel.new/"
chown -R "root:$PANEL_USER" "$PREFIX/panel.new" 2>/dev/null || true
chmod -R u=rwX,g=rX,o= "$PREFIX/panel.new"
chmod 0750 "$PREFIX/panel.new/bin/whacp"
rm -rf "$PREFIX/panel.old"
mv "$PREFIX/panel" "$PREFIX/panel.old"
mv "$PREFIX/panel.new" "$PREFIX/panel"
rm -rf "$PREFIX/panel.old"
install -m 0755 "$AGENT_SRC" "$BIN.new" && mv -f "$BIN.new" "$BIN"
port=$(sed -n 's/.*-S 127\.0\.0\.1:\([0-9]*\).*/\1/p' "$UNIT_DIR/whacp-panel.service" | head -1)
install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent.service" "$UNIT_DIR/whacp-agent.service"
sed "s/127.0.0.1:8090/127.0.0.1:${port:-8090}/" "$SRC_DIR/installer/systemd/whacp-panel.service" > "$UNIT_DIR/whacp-panel.service"
install -m 0644 "$SRC_DIR/installer/systemd/whacp-scheduler.service" "$UNIT_DIR/whacp-scheduler.service"
install -m 0644 "$SRC_DIR/installer/systemd/whacp-scheduler.timer" "$UNIT_DIR/whacp-scheduler.timer"
# Refresh drop-ins that are already enabled (write mode, remote access).
[[ -f $UNIT_DIR/whacp-agent.service.d/10-write-mode.conf ]] && install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent-write-mode.conf" "$UNIT_DIR/whacp-agent.service.d/10-write-mode.conf"
[[ -f $UNIT_DIR/whacp-agent.service.d/20-remote.conf ]] && install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent-remote.conf" "$UNIT_DIR/whacp-agent.service.d/20-remote.conf"
ok "files installed"

log "Migrating the panel database"
panel_cli migrate || rollback_and_die "database migration failed"
systemctl enable whacp-scheduler.timer >/dev/null 2>&1 || true
start_services || rollback_and_die "services did not start"

log "Health checks"
health || rollback_and_die "health checks failed"
trap - ERR
ok "WhaCP $NEW is running"

# Keep the newest $KEEP backups.
ls -1d "$BACKUP_ROOT"/*/ 2>/dev/null | sort | head -n -$KEEP | xargs -r rm -rf
cat <<EOF

WhaCP was upgraded from $OLD to $NEW.
Backup of the previous version: $B
To go back: sudo $SRC_DIR/installer/upgrade.sh --rollback
EOF

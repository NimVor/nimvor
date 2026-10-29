#!/usr/bin/env bash
#
# NimVor upgrade and rollback.
#
#   sudo ./installer/upgrade.sh [--yes] [--allow-downgrade] [--wait=30m]   upgrade to this source tree
#   sudo ./installer/upgrade.sh --rollback [--yes]            restore the version before the last upgrade
#   sudo ./installer/upgrade.sh --list                        list upgrade backups
#
# An upgrade:
#   1. checks that the new agent accepts the existing /etc/nimvor/agent.json,
#      and waits (up to --wait, default 30m; --wait=0 skips this) until the
#      agent has no running job, so a restore or deployment is never cut off;
#   2. backs up the panel files, agent binary, systemd units, /etc/nimvor and
#      the panel database (taken with the panel stopped), checks the
#      database copy's integrity and records checksums of the backup;
#   3. swaps in the new files, runs database migrations and restarts;
#   4. checks that the agent runs, the panel reaches it and answers /healthz.
# If any step after the swap fails, the previous version is restored
# automatically. Websites keep running throughout: only NimVor's own
# services restart. The last three backups are kept.

set -Eeuo pipefail
umask 027

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# Overridable for tests only; the defaults are the installed locations.
PREFIX="${NIMVOR_PREFIX:-/opt/nimvor}"
CONF_DIR="${NIMVOR_CONF_DIR:-/etc/nimvor}"
BIN="${NIMVOR_BIN:-/usr/local/bin/nimvor-agent}"
UNIT_DIR="${NIMVOR_UNIT_DIR:-/etc/systemd/system}"
BACKUP_ROOT="${NIMVOR_BACKUP_ROOT:-/var/backups/nimvor/upgrades}"
AGENT_SOCKET="${NIMVOR_AGENT_SOCKET:-/run/nimvor/agent.sock}"
PANEL_USER="${NIMVOR_PANEL_USER:-nimvor}"
KEEP=3
UNITS=(nimvor-agent.service nimvor-panel.service nimvor-scheduler.service nimvor-scheduler.timer)

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
    # Ask on the terminal: standard input may be a pasted block of commands
    # or already used up by an earlier command, which answered "no" silently.
    if ! { exec 3</dev/tty; } 2>/dev/null; then
        die "$1 — no terminal to ask on. Run the upgrade again with --yes to answer yes."
    fi
    if ! read -r -u 3 -p "$1 [y/N] " reply; then
        exec 3<&-
        die "$1 — no answer could be read. Run the upgrade again with --yes to answer yes."
    fi
    exec 3<&-
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

# The panel's PHP (>= 8.3) as installed, which need not be the system's
# default "php" (Ubuntu 22.04: PHP 8.3 next to the system's 8.1).
PHP_BIN=$(grep -oE '^ExecStart=[^ ]+' "$UNIT_DIR/nimvor-panel.service" 2>/dev/null | head -1 | cut -d= -f2 || true)
[[ -n $PHP_BIN && -x $PHP_BIN ]] || PHP_BIN=$(command -v php || echo /usr/bin/php)
panel_cli() { runuser -u "$PANEL_USER" -- env NIMVOR_CONFIG="$CONF_DIR/panel.php" "$PHP_BIN" "$PREFIX/panel/bin/nimvor" "$@"; }
db_path() { "$PHP_BIN" -r '$c = require $argv[1]; echo $c["database"]["path"] ?? "/var/lib/nimvor/nimvor.sqlite";' "$CONF_DIR/panel.php"; }
installed_version() { cat "$PREFIX/panel/VERSION" 2>/dev/null || echo "0.4.0"; }

[[ $JOB_WAIT =~ ^([0-9]+[smh])+$|^0$ ]] || die "--wait needs a duration such as 30m, 2h or 0"
[[ $EUID -eq 0 || -n ${NIMVOR_TEST:-} ]] || die "run as root (sudo)"

if [[ $LIST -eq 1 ]]; then
    ls -1 "$BACKUP_ROOT" 2>/dev/null || echo "no upgrade backups"
    exit 0
fi

if ! [[ -d $PREFIX/panel && -x $BIN && -f $CONF_DIR/panel.php ]]; then
    [[ -e $PREFIX || -e $CONF_DIR ]] && die "NimVor's installation here is incomplete (an earlier install.sh did not finish); complete it with installer/install.sh --reinstall, then upgrade"
    die "NimVor is not installed here; use installer/install.sh"
fi

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
    systemctl stop nimvor-scheduler.timer nimvor-panel.service 2>/dev/null || true
}

start_services() {
    systemctl daemon-reload
    systemctl restart nimvor-agent.service
    systemctl restart nimvor-panel.service
    # These run the agent's program too (only restarted where they run).
    systemctl try-restart nimvor-greylist.service nimvor-mailrate.service 2>/dev/null || true
    systemctl start nimvor-scheduler.timer 2>/dev/null || true
}

health() {
    local i
    systemctl is-active --quiet nimvor-agent.service || { warn "nimvor-agent is not running"; return 1; }
    for i in $(seq 1 20); do [[ -S $AGENT_SOCKET ]] && break; sleep 0.5; done
    panel_cli agent:check >/dev/null || { warn "the panel cannot reach the agent"; return 1; }
    local port
    port=$(sed -n 's/.*-S 127\.0\.0\.1:\([0-9]*\).*/\1/p' "$UNIT_DIR/nimvor-panel.service" | head -1)
    for i in $(seq 1 20); do curl -fsS "http://127.0.0.1:${port:-8090}/healthz" >/dev/null 2>&1 && return 0; sleep 0.5; done
    warn "the panel does not answer on 127.0.0.1:${port:-8090}"
    return 1
}

# db_ok <sqlite file>: SQLite finds no corruption in it.
db_ok() {
    [[ $("$PHP_BIN" -r '$p = new PDO("sqlite:" . $argv[1], null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]); echo $p->query("PRAGMA integrity_check")->fetchColumn();' "$1" 2>/dev/null) == ok ]]
}

# write_sums / verify_backup <backup dir>: a checksum of every file in an
# upgrade backup, checked (with the database's integrity) before anything
# is restored from it. SQLite's -wal/-shm side files are not part of it.
write_sums() {
    (cd "$1" && find . -type f ! -name SHA256SUMS ! -name '*-wal' ! -name '*-shm' -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
}
verify_backup() {
    local b=$1
    if [[ -f $b/SHA256SUMS ]]; then
        (cd "$b" && sha256sum --quiet --strict -c SHA256SUMS >/dev/null 2>&1) || { warn "$(basename "$b") does not match its checksums (changed or damaged since it was made)"; return 1; }
    else
        warn "$(basename "$b") was made by an older upgrade.sh without checksums; only its database is checked"
    fi
    if [[ -f $b/db.sqlite ]] && ! db_ok "$b/db.sqlite"; then
        warn "the database in $(basename "$b") fails SQLite's integrity check"
        return 1
    fi
}

# backup_time <backup dir>: when it was made, in the audit log's format.
backup_time() {
    local n
    n=$(basename "$1")
    printf '%s-%s-%sT%s:%s:%s+00:00' "${n:0:4}" "${n:4:2}" "${n:6:2}" "${n:9:2}" "${n:11:2}" "${n:13:2}"
}

# restore <backup dir>: put files, units, configuration and database back.
restore() {
    local b=$1 db
    log "Verifying $(basename "$b")"
    verify_backup "$b" || { warn "not restoring from a backup that failed verification; nothing was restored"; return 1; }
    ok "backup verified"
    log "Restoring $(basename "$b")"
    stop_services
    if [[ -d $b/panel ]]; then
        rm -rf "$PREFIX/panel.failed"
        [[ -d $PREFIX/panel ]] && mv "$PREFIX/panel" "$PREFIX/panel.failed"
        cp -a "$b/panel" "$PREFIX/panel"
        rm -rf "$PREFIX/panel.failed"
    fi
    [[ -f $b/nimvor-agent ]] && install -m 0755 "$b/nimvor-agent" "$BIN.restore" && mv -f "$BIN.restore" "$BIN"
    for u in "${UNITS[@]}"; do
        if [[ -f $b/units/$u ]]; then cp -a "$b/units/$u" "$UNIT_DIR/$u"; else rm -f "$UNIT_DIR/$u"; fi
    done
    if [[ -d $b/units/nimvor-agent.service.d ]]; then
        rm -rf "$UNIT_DIR/nimvor-agent.service.d"
        cp -a "$b/units/nimvor-agent.service.d" "$UNIT_DIR/"
    fi
    [[ -d $b/etc ]] && cp -a "$b/etc/." "$CONF_DIR/"
    db=$(cat "$b/db-path" 2>/dev/null || true)
    if [[ -n $db && -f $b/db.sqlite ]]; then
        rm -f "$db-wal" "$db-shm"
        cp -a "$b/db.sqlite" "$db"
    fi
    start_services
    health && ok "restored and healthy" || warn "restored, but health checks still fail: journalctl -u nimvor-agent -u nimvor-panel"
}

if [[ $ROLLBACK -eq 1 ]]; then
    latest=$(ls -1d "$BACKUP_ROOT"/*/ 2>/dev/null | sort | tail -1)
    [[ -n $latest ]] || die "no upgrade backup found in $BACKUP_ROOT"
    latest=${latest%/}
    verify_backup "$latest" || die "the backup $(basename "$latest") failed verification; nothing was changed"
    since=$(backup_time "$latest")
    changes=$("$PHP_BIN" -r '$p = new PDO("sqlite:" . $argv[1]); $s = $p->prepare("SELECT COUNT(*) FROM audit_logs WHERE created_at >= ?"); $s->execute([$argv[2]]); echo (int) $s->fetchColumn();' "$(db_path)" "$since" 2>/dev/null || echo "?")
    warn "Rolling back restores NimVor $(cat "$latest/version" 2>/dev/null) and the panel database as it was at $since."
    warn "Everything changed in the panel since then is lost from the panel ($changes change(s) in the audit log since then)."
    warn "What was already done on the server stays there: e.g. a website created since then keeps running but no longer appears in the panel."
    confirm "Roll back to $(cat "$latest/version" 2>/dev/null) from $(basename "$latest")?" || die "aborted"
    log "Waiting for running jobs"
    wait_jobs "$BIN" || die "jobs are still running; nothing was changed. Try again later or pass --wait=<longer>"
    restore "$latest" || die "rollback failed; nothing was restored"
    exit 0
fi

# ------------------------------------------------------------ preflight ----
OLD=$(installed_version)
NEW=$(cat "$SRC_DIR/VERSION")
log "Upgrading NimVor $OLD → $NEW"
# Pre-releases sort before their release (1.0.0-rc1 < 1.0.0): "-" becomes
# "~", which version sort places before everything.
newest=$(printf '%s\n%s\n' "${OLD/-/\~}" "${NEW/-/\~}" | sort -V | tail -1)
if [[ $newest != "${NEW/-/\~}" && $ALLOW_DOWNGRADE -eq 0 ]]; then
    die "$NEW is older than the installed $OLD; use --rollback, or --allow-downgrade if you really mean it"
fi

AGENT_SRC=$SRC_DIR/dist/nimvor-agent
# Go as install.sh finds it: sudo's PATH often lacks it, and the installer
# may have put it in /usr/local/lib/nimvor-go (Ubuntu 22.04) or found the
# distribution's /usr/lib/go-1.xx.
find_go() {
    local c
    # shellcheck disable=SC2046
    for c in "$(command -v go 2>/dev/null || true)" /usr/local/lib/nimvor-go/go/bin/go /usr/local/go/bin/go /usr/lib/golang/bin/go $(ls -d /usr/lib/go-*/bin/go 2>/dev/null | sort -V -r) /usr/lib/go/bin/go; do
        [[ -n $c && -x $c && ! -d $c ]] || continue
        # Go 1.21+ fetches the toolchain go.mod asks for; older Go cannot.
        local v; v=$("$c" env GOVERSION 2>/dev/null); v=${v#go}
        [[ -n $v && $(printf '%s\n1.21\n' "$v" | sort -V | head -1) == 1.21 ]] && { printf '%s\n' "$c"; return 0; }
    done
    return 1
}
GO=$(find_go || true)
# A build older than the agent's sources (after git pull) must not be
# installed: the upgrade would keep the old agent.
stale=0
if [[ ! -x $AGENT_SRC ]] || [[ -d $SRC_DIR/agent && -n $(find "$SRC_DIR/agent" "$SRC_DIR/VERSION" \( -name '*.go' -o -name go.mod -o -name go.sum -o -name VERSION \) -newer "$AGENT_SRC" -print -quit 2>/dev/null) ]]; then
    stale=1
fi
if [[ -n $GO && ( $stale -eq 1 || ${NIMVOR_REBUILD:-1} == 1 ) ]]; then
    log "Building the agent ($("$GO" env GOVERSION 2>/dev/null))"
    mkdir -p "$SRC_DIR/dist"
    version=$NEW
    if commit=$(git -C "$SRC_DIR" -c safe.directory="$SRC_DIR" rev-parse --short HEAD 2>/dev/null); then version+="+$commit"; fi
    (cd "$SRC_DIR/agent" && CGO_ENABLED=0 HOME="${HOME:-/root}" GOTOOLCHAIN=auto "$GO" build -trimpath -ldflags "-s -w -X main.version=$version" -o "$AGENT_SRC" ./cmd/nimvor-agent)
    stale=0
fi
[[ -x $AGENT_SRC ]] || die "no agent binary (dist/nimvor-agent) and no usable Go (1.21 or later) was found; run installer/install.sh --reinstall, which installs Go"
[[ $stale -eq 0 ]] || die "dist/nimvor-agent is older than the agent's sources (after git pull) and no usable Go (1.21 or later) was found to rebuild it; run installer/install.sh --reinstall, which installs Go"
"$AGENT_SRC" -config "$CONF_DIR/agent.json" -check-config >/dev/null || die "the new agent rejects $CONF_DIR/agent.json; nothing was changed"
ok "new agent accepts the existing configuration"
[[ -f $SRC_DIR/panel/vendor/autoload.php ]] || die "panel/vendor is missing (run 'composer install --no-dev' in panel/)"
"$PHP_BIN" -r 'exit(PHP_VERSION_ID >= 80300 ? 0 : 1);' || die "the panel needs PHP >= 8.3 ($PHP_BIN)"
confirm "Upgrade now? NimVor's panel is unavailable for a few seconds; websites are not affected." || die "aborted"
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
cp -a "$BIN" "$B/nimvor-agent"
install -d -m 0700 "$B/units" "$B/etc"
for u in "${UNITS[@]}"; do [[ -f $UNIT_DIR/$u ]] && cp -a "$UNIT_DIR/$u" "$B/units/"; done
[[ -d $UNIT_DIR/nimvor-agent.service.d ]] && cp -a "$UNIT_DIR/nimvor-agent.service.d" "$B/units/"
cp -a "$CONF_DIR/." "$B/etc/"
DB=$(db_path)
echo "$DB" > "$B/db-path"
stop_services
# A job may have started between the check and stopping the panel.
if ! wait_jobs "$AGENT_SRC"; then
    systemctl start nimvor-panel.service nimvor-scheduler.timer 2>/dev/null || true
    die "jobs are still running; nothing was changed. Try again later or pass --wait=<longer>"
fi
# backup_failed: nothing was changed yet; bring the panel back and stop.
backup_failed() {
    systemctl start nimvor-panel.service nimvor-scheduler.timer 2>/dev/null || true
    die "$1; nothing was changed. The incomplete backup is in $B"
}
if [[ -f $DB ]]; then
    # Consistent copy: the panel and scheduler are stopped; fold the WAL into
    # the database file. The checkpoint must complete (busy = 0), otherwise
    # the copy could miss the latest changes.
    for i in 1 2 3 4 5 6 7 8 9 10; do
        busy=$("$PHP_BIN" -r '$p = new PDO("sqlite:" . $argv[1], null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]); echo $p->query("PRAGMA wal_checkpoint(TRUNCATE)")->fetch(PDO::FETCH_NUM)[0];' "$DB" 2>/dev/null || echo error)
        [[ $busy == 0 ]] && break
        sleep 1
    done
    [[ $busy == 0 ]] || backup_failed "the panel database could not be checkpointed (still in use?)"
    cp -a "$DB" "$B/db.sqlite"
    db_ok "$B/db.sqlite" || backup_failed "the copy of the panel database fails SQLite's integrity check (run: nimvor doctor)"
fi
write_sums "$B" || backup_failed "could not write the backup's checksums"
verify_backup "$B" || backup_failed "the backup could not be verified"
ok "backup complete and verified"

rollback_and_die() {
    trap - ERR
    warn "$1 — rolling back to $OLD"
    restore "$B" || die "upgrade failed ($1) and the backup $B failed verification, so it was not restored: NimVor is left as it is. Check the backup, then restore it by hand"
    die "upgrade failed ($1); NimVor $OLD was restored. Backup kept in $B"
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
chmod 0750 "$PREFIX/panel.new/bin/nimvor"
rm -rf "$PREFIX/panel.old"
mv "$PREFIX/panel" "$PREFIX/panel.old"
mv "$PREFIX/panel.new" "$PREFIX/panel"
rm -rf "$PREFIX/panel.old"
install -m 0755 "$AGENT_SRC" "$BIN.new" && mv -f "$BIN.new" "$BIN"
install -m 0755 "$SRC_DIR/installer/config/nimvor-cli" "$(dirname "$BIN")/nimvor" # the short command (sudo nimvor ...), next to the agent binary
port=$(sed -n 's/.*-S 127\.0\.0\.1:\([0-9]*\).*/\1/p' "$UNIT_DIR/nimvor-panel.service" | head -1)
install -m 0644 "$SRC_DIR/installer/systemd/nimvor-agent.service" "$UNIT_DIR/nimvor-agent.service"
sed "s/127.0.0.1:8090/127.0.0.1:${port:-8090}/" "$SRC_DIR/installer/systemd/nimvor-panel.service" > "$UNIT_DIR/nimvor-panel.service"
install -m 0644 "$SRC_DIR/installer/systemd/nimvor-scheduler.service" "$UNIT_DIR/nimvor-scheduler.service"
install -m 0644 "$SRC_DIR/installer/systemd/nimvor-scheduler.timer" "$UNIT_DIR/nimvor-scheduler.timer"
if command -v semodule >/dev/null && command -v getenforce >/dev/null && [[ $(getenforce) != Disabled && -f $SRC_DIR/installer/selinux/nimvor_postfix_sockets.cil ]]; then
    # systemd's greylisting and mail limit sockets in Postfix's private folder.
    semodule -i "$SRC_DIR/installer/selinux/nimvor_postfix_sockets.cil" || warn "could not install NimVor's SELinux policy module"
fi
# Refresh drop-ins that are already enabled (write mode, remote access).
[[ -f $UNIT_DIR/nimvor-agent.service.d/10-write-mode.conf ]] && install -m 0644 "$SRC_DIR/installer/systemd/nimvor-agent-write-mode.conf" "$UNIT_DIR/nimvor-agent.service.d/10-write-mode.conf"
[[ -f $UNIT_DIR/nimvor-agent.service.d/20-remote.conf ]] && install -m 0644 "$SRC_DIR/installer/systemd/nimvor-agent-remote.conf" "$UNIT_DIR/nimvor-agent.service.d/20-remote.conf"
ok "files installed"

log "Migrating the panel database"
panel_cli migrate || rollback_and_die "database migration failed"
systemctl enable nimvor-scheduler.timer >/dev/null 2>&1 || true
start_services || rollback_and_die "services did not start"

log "Health checks"
health || rollback_and_die "health checks failed"
trap - ERR
ok "NimVor $NEW is running"
# Cron schedules in the new agent's form (each job sandboxed).
panel_cli cron:sync || warn "some cron schedules were not written again (see above)"

# Keep the newest $KEEP backups.
ls -1d "$BACKUP_ROOT"/*/ 2>/dev/null | sort | head -n -$KEEP | xargs -r rm -rf
"$SRC_DIR/installer/telemetry.sh" upgrade "$CONF_DIR" || true
cat <<EOF

NimVor was upgraded from $OLD to $NEW.
Backup of the previous version: $B
To go back: sudo $SRC_DIR/installer/upgrade.sh --rollback
EOF

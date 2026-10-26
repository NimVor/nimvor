#!/usr/bin/env bash
#
# WhaCP upgrade and rollback (pure-Go).
#
#   sudo ./installer/upgrade.sh [--yes] [--allow-downgrade] [--wait=30m]   upgrade to this source tree
#   sudo ./installer/upgrade.sh --rollback [--yes]            restore the version before the last upgrade
#   sudo ./installer/upgrade.sh --list                        list upgrade backups
#
# An upgrade:
#   1. checks that the new agent accepts the existing /etc/whacp/agent.json,
#      and waits (up to --wait, default 30m; --wait=0 skips this) until the
#      agent has no running job, so a restore or deployment is never cut off;
#   2. backs up the binaries, systemd units, /etc/whacp and the panel
#      database (taken with the panel stopped), checks the database copy's
#      integrity and records checksums of the backup;
#   3. swaps in the new binaries/units (migrations run automatically on
#      first start of whacp) and restarts services;
#   4. checks that the agent runs and the panel answers /healthz.
# If any step after the swap fails, the previous version is restored
# automatically. Websites keep running throughout: only WhaCP's own
# services restart. The last three backups are kept.

set -Eeuo pipefail
umask 027

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PREFIX="${WHACP_PREFIX:-/opt/whacp}"
CONF_DIR="${WHACP_CONF_DIR:-/etc/whacp}"
BIN_AGENT="${WHACP_BIN_AGENT:-/usr/local/bin/whacp-agent}"
BIN_PANEL="${WHACP_BIN_PANEL:-/usr/local/bin/whacp}"
UNIT_DIR="${WHACP_UNIT_DIR:-/etc/systemd/system}"
BACKUP_ROOT="${WHACP_BACKUP_ROOT:-/var/backups/whacp/upgrades}"
AGENT_SOCKET="${WHACP_AGENT_SOCKET:-/run/whacp/agent.sock}"
PANEL_USER="${WHACP_PANEL_USER:-whacp}"
KEEP=3
UNITS=(whacp-agent.service whacp-panel.service)

ASSUME_YES=0; ROLLBACK=0; LIST=0; ALLOW_DOWNGRADE=0; JOB_WAIT=30m

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m  ✓\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m  !\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; exit 1; }
confirm() {
    [[ $ASSUME_YES -eq 1 ]] && return 0
    local reply
    if ! { exec 3</dev/tty; } 2>/dev/null; then
        die "$1 — no terminal to ask on. Run the upgrade again with --yes to answer yes."
    fi
    if ! read -r -u 3 -p "$1 [y/N] " reply; then exec 3<&-; die "$1 — no answer"; fi
    exec 3<&-; [[ $reply =~ ^[Yy]$ ]]
}

for arg in "$@"; do
    case "$arg" in
        --yes|-y) ASSUME_YES=1 ;;
        --rollback) ROLLBACK=1 ;;
        --list) LIST=1 ;;
        --allow-downgrade) ALLOW_DOWNGRADE=1 ;;
        --wait=*) JOB_WAIT=${arg#--wait=} ;;
        -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
        *) die "unknown option: $arg" ;;
    esac
done

db_path() {
    # Extract WHACP_DB_PATH from server.env (default: /var/lib/whacp/whacp.sqlite).
    local p
    p=$(grep -E '^WHACP_DB_PATH=' "$CONF_DIR/server.env" 2>/dev/null | head -1 | cut -d= -f2-)
    echo "${p:-/var/lib/whacp/whacp.sqlite}"
}
port_from_unit() {
    local p
    p=$(grep -oE '^WHACP_LISTEN=127\.0\.0\.1:[0-9]+' "$CONF_DIR/server.env" 2>/dev/null | head -1 | cut -d: -f3)
    echo "${p:-8090}"
}
installed_version() {
    "$BIN_PANEL" -version 2>/dev/null | awk '{print $2}' || echo "0.0.0"
}

[[ $JOB_WAIT =~ ^([0-9]+[smh])+$|^0$ ]] || die "--wait needs a duration such as 30m, 2h or 0"
[[ $EUID -eq 0 || -n ${WHACP_TEST:-} ]] || die "run as root (sudo)"

if [[ $LIST -eq 1 ]]; then
    ls -1 "$BACKUP_ROOT" 2>/dev/null || echo "no upgrade backups"
    exit 0
fi

if ! [[ -x $BIN_AGENT && -x $BIN_PANEL && -f $CONF_DIR/server.env ]]; then
    die "WhaCP is not installed here; use installer/install.sh"
fi

wait_jobs() {
    local rc=0
    if [[ $JOB_WAIT == 0 ]]; then
        warn "not waiting for running jobs (--wait=0): a running restore or deployment would be interrupted"
        return 0
    fi
    "$1" -config "$CONF_DIR/agent.json" -wait-jobs "$JOB_WAIT" || rc=$?
    case $rc in
        0) return 0 ;;
        2) warn "this agent cannot list running jobs; not waiting"; return 0 ;;
        3) warn "background jobs still running after $JOB_WAIT"; return 1 ;;
        *) warn "could not ask the agent for running jobs"; return 1 ;;
    esac
}

stop_services() {
    systemctl stop whacp-panel.service 2>/dev/null || true
}
start_services() {
    systemctl daemon-reload
    systemctl restart whacp-agent.service
    systemctl restart whacp-panel.service
    systemctl try-restart whacp-greylist.service whacp-mailrate.service 2>/dev/null || true
}

health() {
    systemctl is-active --quiet whacp-agent.service || { warn "whacp-agent is not running"; return 1; }
    local i port
    for i in $(seq 1 20); do [[ -S $AGENT_SOCKET ]] && break; sleep 0.5; done
    port=$(port_from_unit); port=${port:-8090}
    for i in $(seq 1 20); do curl -fsS "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1 && return 0; sleep 0.5; done
    warn "the panel does not answer on 127.0.0.1:${port}"
    return 1
}

db_ok() {
    # Use sqlite3 if available; otherwise fall back to a quick header check.
    if command -v sqlite3 >/dev/null; then
        [[ $(sqlite3 "$1" "PRAGMA integrity_check;" 2>/dev/null | head -1) == ok ]]
    else
        [[ -s $1 ]]
    fi
}

write_sums() {
    (cd "$1" && find . -type f ! -name SHA256SUMS ! -name '*-wal' ! -name '*-shm' -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS)
}
verify_backup() {
    local b=$1
    if [[ -f $b/SHA256SUMS ]]; then
        (cd "$b" && sha256sum --quiet --strict -c SHA256SUMS >/dev/null 2>&1) \
            || { warn "$(basename "$b") does not match its checksums"; return 1; }
    fi
    [[ -f $b/db.sqlite ]] && ! db_ok "$b/db.sqlite" && { warn "database in $(basename "$b") fails integrity check"; return 1; }
    return 0
}

backup_time() {
    local n; n=$(basename "$1")
    printf '%s-%s-%sT%s:%s:%s+00:00' "${n:0:4}" "${n:4:2}" "${n:6:2}" "${n:9:2}" "${n:11:2}" "${n:13:2}"
}

restore() {
    local b=$1 db
    log "Verifying $(basename "$b")"
    verify_backup "$b" || die "backup failed verification; nothing was restored"
    ok "backup verified"
    log "Restoring $(basename "$b")"
    stop_services
    [[ -f $b/whacp-agent ]] && install -m 0755 "$b/whacp-agent" "$BIN_AGENT"
    [[ -f $b/whacp ]] && install -m 0755 "$b/whacp" "$BIN_PANEL"
    for u in "${UNITS[@]}"; do
        [[ -f $b/units/$u ]] && cp -a "$b/units/$u" "$UNIT_DIR/$u"
    done
    [[ -d $b/units/whacp-agent.service.d ]] && { rm -rf "$UNIT_DIR/whacp-agent.service.d"; cp -a "$b/units/whacp-agent.service.d" "$UNIT_DIR/"; }
    [[ -d $b/etc ]] && cp -a "$b/etc/." "$CONF_DIR/"
    db=$(cat "$b/db-path" 2>/dev/null || true)
    if [[ -n $db && -f $b/db.sqlite ]]; then
        rm -f "$db-wal" "$db-shm"
        cp -a "$b/db.sqlite" "$db"
    fi
    start_services
    health && ok "restored and healthy" || warn "restored, but health checks fail"
}

if [[ $ROLLBACK -eq 1 ]]; then
    latest=$(ls -1d "$BACKUP_ROOT"/*/ 2>/dev/null | sort | tail -1)
    [[ -n $latest ]] || die "no upgrade backup found in $BACKUP_ROOT"
    latest=${latest%/}
    verify_backup "$latest" || die "the backup $(basename "$latest") failed verification"
    confirm "Roll back to $(cat "$latest/version" 2>/dev/null) from $(basename "$latest")?" || die "aborted"
    log "Waiting for running jobs"
    wait_jobs "$BIN_AGENT" || die "jobs are still running"
    restore "$latest"
    exit 0
fi

# ------------------------------------------------------------ preflight ----
OLD=$(installed_version)
NEW=$(cat "$SRC_DIR/VERSION")
log "Upgrading WhaCP $OLD → $NEW"
newest=$(printf '%s\n%s\n' "${OLD/-/\~}" "${NEW/-/\~}" | sort -V | tail -1)
if [[ $newest != "${NEW/-/\~}" && $ALLOW_DOWNGRADE -eq 0 ]]; then
    die "$NEW is older than installed $OLD; use --rollback, or --allow-downgrade"
fi

# Build agent + panel from source if needed (find Go the same way install.sh does).
find_go() {
    local c
    for c in "$(command -v go 2>/dev/null || true)" /usr/local/lib/whacp-go/bin/go /usr/local/go/bin/go /usr/lib/golang/bin/go $(ls -d /usr/lib/go-*/bin/go 2>/dev/null | sort -V -r) /usr/lib/go/bin/go; do
        [[ -n $c && -x $c && ! -d $c ]] || continue
        local v; v=$("$c" version 2>/dev/null | awk '{print $3}'); v=${v#go}
        [[ $(printf '%s\n1.24\n' "$v" | sort -V | head -1) == 1.24 ]] && { printf '%s\n' "$c"; return 0; }
    done
    return 1
}
GO=$(find_go || true)

AGENT_SRC=$SRC_DIR/dist/whacp-agent
PANEL_SRC=$SRC_DIR/dist/whacp
agent_stale=0; panel_stale=0
if [[ ! -x $AGENT_SRC ]] || [[ -d $SRC_DIR/agent && -n $(find "$SRC_DIR/agent" "$SRC_DIR/VERSION" \( -name '*.go' -o -name go.mod -o -name go.sum -o -name VERSION \) -newer "$AGENT_SRC" -print -quit 2>/dev/null) ]]; then agent_stale=1; fi
if [[ ! -x $PANEL_SRC ]] || [[ -d $SRC_DIR/server && -n $(find "$SRC_DIR/server" "$SRC_DIR/VERSION" \( -name '*.go' -o -name go.mod -o -name go.sum -o -name VERSION \) -newer "$PANEL_SRC" -print -quit 2>/dev/null) ]]; then panel_stale=1; fi

if [[ -z $GO ]]; then
    [[ $agent_stale -eq 0 && $panel_stale -eq 0 ]] || die "binaries are stale and Go was not found; run installer/install.sh --reinstall"
else
    version=$NEW
    if commit=$(git -C "$SRC_DIR" -c safe.directory="$SRC_DIR" rev-parse --short HEAD 2>/dev/null); then version+="+$commit"; fi
    mkdir -p "$SRC_DIR/dist"
    if [[ $agent_stale -eq 1 ]]; then
        log "Building whacp-agent"
        (cd "$SRC_DIR/agent" && CGO_ENABLED=0 HOME="${HOME:-/root}" GOTOOLCHAIN=auto "$GO" build -trimpath -ldflags "-s -w -X main.version=$version" -o "$AGENT_SRC" ./cmd/whacp-agent)
    fi
    if [[ $panel_stale -eq 1 ]]; then
        log "Building whacp (panel server)"
        (cd "$SRC_DIR/server" && CGO_ENABLED=0 HOME="${HOME:-/root}" GOTOOLCHAIN=auto "$GO" build -trimpath -ldflags "-s -w -X main.version=$version" -o "$PANEL_SRC" ./cmd/whacp)
    fi
fi
[[ -x $AGENT_SRC ]] || die "no whacp-agent binary"
[[ -x $PANEL_SRC ]] || die "no whacp binary"
"$AGENT_SRC" -config "$CONF_DIR/agent.json" -check-config >/dev/null || die "the new agent rejects $CONF_DIR/agent.json"
ok "new agent accepts the existing configuration"
confirm "Upgrade now? WhaCP's panel is unavailable for a few seconds; websites are not affected." || die "aborted"
log "Waiting for running jobs"
wait_jobs "$AGENT_SRC" || die "jobs are still running"
ok "no job is running"

# --------------------------------------------------------------- backup ----
TS=$(date -u +%Y%m%dT%H%M%SZ)
B="$BACKUP_ROOT/$TS-$OLD"
log "Backing up to $B"
install -d -m 0700 "$BACKUP_ROOT" "$B"
echo "$OLD" > "$B/version"
install -m 0755 "$BIN_AGENT" "$B/whacp-agent"
install -m 0755 "$BIN_PANEL" "$B/whacp"
install -d -m 0700 "$B/units" "$B/etc"
for u in "${UNITS[@]}"; do [[ -f $UNIT_DIR/$u ]] && cp -a "$UNIT_DIR/$u" "$B/units/"; done
[[ -d $UNIT_DIR/whacp-agent.service.d ]] && cp -a "$UNIT_DIR/whacp-agent.service.d" "$B/units/"
cp -a "$CONF_DIR/." "$B/etc/"
DB=$(db_path); echo "$DB" > "$B/db-path"
stop_services
if ! wait_jobs "$AGENT_SRC"; then
    systemctl start whacp-panel.service 2>/dev/null || true
    die "jobs are still running; nothing was changed"
fi
backup_failed() {
    systemctl start whacp-panel.service 2>/dev/null || true
    die "$1; nothing was changed. Backup at $B"
}
if [[ -f $DB ]]; then
    if command -v sqlite3 >/dev/null; then
        for i in 1 2 3 4 5 6 7 8 9 10; do
            busy=$(sqlite3 "$DB" "PRAGMA wal_checkpoint(TRUNCATE);" 2>/dev/null | awk -F'|' '{print $1}')
            [[ $busy == 0 ]] && break
            sleep 1
        done
        [[ $busy == 0 ]] || backup_failed "the database could not be checkpointed"
    fi
    cp -a "$DB" "$B/db.sqlite"
    db_ok "$B/db.sqlite" || backup_failed "the database copy fails integrity check"
fi
write_sums "$B" || backup_failed "could not write backup checksums"
verify_backup "$B" || backup_failed "backup verification failed"
ok "backup complete and verified"

rollback_and_die() {
    trap - ERR
    warn "$1 — rolling back to $OLD"
    restore "$B" || die "upgrade failed ($1) AND rollback failed; WhaCP left as-is. Backup at $B"
    die "upgrade failed ($1); WhaCP $OLD was restored."
}
trap 'rollback_and_die "unexpected error on line $LINENO"' ERR

# ---------------------------------------------------------------- apply ----
log "Installing $NEW"
install -m 0755 "$AGENT_SRC" "$BIN_AGENT"
install -m 0755 "$PANEL_SRC" "$BIN_PANEL"
install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent.service" "$UNIT_DIR/whacp-agent.service"
install -m 0644 "$SRC_DIR/installer/systemd/whacp-panel.service" "$UNIT_DIR/whacp-panel.service"
if command -v semodule >/dev/null && command -v getenforce >/dev/null && [[ $(getenforce) != Disabled && -f $SRC_DIR/installer/selinux/whacp_postfix_sockets.cil ]]; then
    semodule -i "$SRC_DIR/installer/selinux/whacp_postfix_sockets.cil" || warn "could not install SELinux policy module"
fi
[[ -f $UNIT_DIR/whacp-agent.service.d/10-write-mode.conf ]] && install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent-write-mode.conf" "$UNIT_DIR/whacp-agent.service.d/10-write-mode.conf"
[[ -f $UNIT_DIR/whacp-agent.service.d/20-remote.conf ]] && install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent-remote.conf" "$UNIT_DIR/whacp-agent.service.d/20-remote.conf"
ok "files installed"

start_services || rollback_and_die "services did not start"

log "Health checks"
health || rollback_and_die "health checks failed"
trap - ERR
ok "WhaCP $NEW is running"

ls -1d "$BACKUP_ROOT"/*/ 2>/dev/null | sort | head -n -$KEEP | xargs -r rm -rf
cat <<EOF

WhaCP was upgraded from $OLD to $NEW.
Backup of the previous version: $B
To go back: sudo $SRC_DIR/installer/upgrade.sh --rollback
EOF

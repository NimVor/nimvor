#!/usr/bin/env bash
#
# WhaCP installer (development / early-adopter release).
#
# What it does:
#   1. Detects the OS and checks requirements.
#   2. Detects conflicts (existing WhaCP install, port in use).
#   3. Optionally installs missing dependencies (only with --install-deps).
#   4. Creates the "whacp" system user and group.
#   5. Installs the agent binary and the panel files.
#   6. Writes /etc/whacp configuration and a fresh agent token.
#   7. Installs and starts the systemd services.
#   8. Creates the first administrator.
#   9. Runs health checks.
#
# What it never does:
#   - modify Nginx, PHP-FPM, MySQL/MariaDB, Redis, firewall or SSH configuration;
#   - reload or restart any existing service;
#   - overwrite an existing WhaCP installation (use --reinstall explicitly).
#
# Usage (from a source checkout or release archive):
#   sudo ./installer/install.sh [--install-deps] [--admin-user=NAME] [--admin-email=EMAIL]
#                               [--port=8090] [--dry-run] [--yes] [--reinstall] [--force-unsupported]

set -Eeuo pipefail
umask 027

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PREFIX=/opt/whacp
CONF_DIR=/etc/whacp
DATA_DIR=/var/lib/whacp
LOG_DIR=/var/log/whacp
BIN=/usr/local/bin/whacp-agent
PORT=8090
ADMIN_USER=""
ADMIN_EMAIL=""
INSTALL_DEPS=0
DRY_RUN=0
ASSUME_YES=0
REINSTALL=0
FORCE_UNSUPPORTED=0
SUPPORTED_UBUNTU=("22.04" "24.04" "26.04")

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m  ✓\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m  !\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31mError:\033[0m %s\n' "$*" >&2; exit 1; }
run()  {
    if [[ $DRY_RUN -eq 1 ]]; then
        printf '    [dry-run] %s\n' "$*"
    else
        "$@"
    fi
}
confirm() {
    [[ $ASSUME_YES -eq 1 ]] && return 0
    local reply
    read -r -p "$1 [y/N] " reply
    [[ $reply =~ ^[Yy]$ ]]
}

for arg in "$@"; do
    case "$arg" in
        --install-deps) INSTALL_DEPS=1 ;;
        --dry-run) DRY_RUN=1 ;;
        --yes|-y) ASSUME_YES=1 ;;
        --reinstall) REINSTALL=1 ;;
        --force-unsupported) FORCE_UNSUPPORTED=1 ;;
        --admin-user=*) ADMIN_USER="${arg#*=}" ;;
        --admin-email=*) ADMIN_EMAIL="${arg#*=}" ;;
        --port=*) PORT="${arg#*=}" ;;
        -h|--help) sed -n '2,25p' "$0"; exit 0 ;;
        *) die "unknown option: $arg" ;;
    esac
done

[[ $PORT =~ ^[0-9]+$ ]] && (( PORT >= 1024 && PORT <= 65535 )) || die "--port must be between 1024 and 65535"
[[ -z $ADMIN_USER || $ADMIN_USER =~ ^[a-z][a-z0-9._-]{2,31}$ ]] || die "--admin-user is not a valid username"

# ---------------------------------------------------------------- 1. OS ----
log "Detecting operating system"
[[ $EUID -eq 0 || $DRY_RUN -eq 1 ]] || die "run as root (sudo)"
[[ -r /etc/os-release ]] || die "/etc/os-release not found"
# shellcheck disable=SC1091
. /etc/os-release
supported=0
if [[ ${ID:-} == ubuntu ]]; then
    for v in "${SUPPORTED_UBUNTU[@]}"; do [[ ${VERSION_ID:-} == "$v" ]] && supported=1; done
fi
if [[ $supported -eq 1 ]]; then
    ok "${PRETTY_NAME}"
elif [[ $FORCE_UNSUPPORTED -eq 1 ]]; then
    warn "${PRETTY_NAME:-unknown OS} is not supported; continuing because of --force-unsupported"
else
    die "${PRETTY_NAME:-unknown OS} is not supported (Ubuntu 22.04/24.04/26.04). Use --force-unsupported to try anyway."
fi
command -v systemctl >/dev/null || die "systemd is required"

# ------------------------------------------------------- 2. requirements ----
log "Checking requirements"
missing_pkgs=()
php_ok=0
if command -v php >/dev/null; then
    if php -r 'exit(PHP_VERSION_ID >= 80300 ? 0 : 1);'; then
        php_ok=1
        ok "PHP $(php -r 'echo PHP_VERSION;')"
        for ext in pdo_sqlite curl mbstring; do
            if php -m | grep -qi "^${ext}$"; then ok "php extension ${ext}"; else missing_pkgs+=("php-${ext/pdo_/}"); fi
        done
    else
        warn "PHP $(php -r 'echo PHP_VERSION;') is older than 8.3"
    fi
fi
if [[ $php_ok -eq 0 ]]; then
    missing_pkgs+=(php-cli php-sqlite3 php-curl php-mbstring)
fi
command -v openssl >/dev/null || missing_pkgs+=(openssl)
command -v curl >/dev/null || missing_pkgs+=(curl)

if [[ ${#missing_pkgs[@]} -gt 0 ]]; then
    if [[ $INSTALL_DEPS -eq 1 ]]; then
        log "Installing dependencies: ${missing_pkgs[*]}"
        run apt-get update -q
        run env DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends "${missing_pkgs[@]}"
    else
        die "missing packages: ${missing_pkgs[*]}. Install them (apt-get install ${missing_pkgs[*]}) or re-run with --install-deps."
    fi
fi
if [[ ${VERSION_ID:-} == "22.04" && $php_ok -eq 0 ]]; then
    warn "Ubuntu 22.04 ships PHP 8.1; WhaCP's panel needs PHP >= 8.3 (e.g. from a PHP PPA). Existing sites are unaffected: the panel only uses the CLI binary."
fi

# ------------------------------------------------------------ 3. conflicts ----
log "Checking for conflicts"
if [[ -e $PREFIX || -e $CONF_DIR ]]; then
    if [[ $REINSTALL -eq 1 ]]; then
        confirm "WhaCP is already installed. Reinstall files (configuration, token and database are kept)?" || die "aborted"
    else
        die "WhaCP is already installed in $PREFIX. Upgrades are not supported yet; use --reinstall to replace the program files."
    fi
fi
if command -v ss >/dev/null && ss -ltn "sport = :$PORT" | grep -q LISTEN; then
    if ! systemctl is-active --quiet whacp-panel 2>/dev/null; then
        die "port $PORT is already in use; choose another with --port="
    fi
fi
ok "no conflicts"

# --------------------------------------------------------- 4. build agent ----
log "Preparing the agent binary"
AGENT_SRC=""
if [[ -x $SRC_DIR/dist/whacp-agent ]]; then
    AGENT_SRC=$SRC_DIR/dist/whacp-agent
elif command -v go >/dev/null; then
    log "Building agent from source"
    run mkdir -p "$SRC_DIR/dist"
    (cd "$SRC_DIR/agent" && run env CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$SRC_DIR/dist/whacp-agent" ./cmd/whacp-agent)
    AGENT_SRC=$SRC_DIR/dist/whacp-agent
else
    die "no prebuilt agent (dist/whacp-agent) and Go is not installed to build it"
fi
ok "$AGENT_SRC"

[[ -f $SRC_DIR/panel/vendor/autoload.php ]] || {
    command -v composer >/dev/null || die "panel/vendor is missing and composer is not installed (run 'composer install --no-dev' in panel/)"
    log "Installing panel PHP dependencies"
    (cd "$SRC_DIR/panel" && run composer install --no-dev --no-interaction --optimize-autoloader -q)
}

# ----------------------------------------------------- 5. user and files ----
log "Creating system user and directories"
if ! getent group whacp >/dev/null; then run groupadd --system whacp; fi
if ! id whacp >/dev/null 2>&1; then
    run useradd --system --gid whacp --home-dir "$DATA_DIR" --no-create-home --shell /usr/sbin/nologin --comment "WhaCP panel" whacp
fi
ok "user whacp"
run install -d -m 0755 -o root -g root "$PREFIX"
run install -d -m 0750 -o root -g whacp "$CONF_DIR"
run install -d -m 0750 -o whacp -g whacp "$DATA_DIR" "$DATA_DIR/sessions" "$LOG_DIR"

log "Installing files"
run install -m 0755 -o root -g root "$AGENT_SRC" "$BIN"
run rm -rf "$PREFIX/panel.new"
run mkdir -p "$PREFIX/panel.new"
run cp -a "$SRC_DIR/panel/app" "$SRC_DIR/panel/bin" "$SRC_DIR/panel/config" "$SRC_DIR/panel/database" \
    "$SRC_DIR/panel/public" "$SRC_DIR/panel/resources" "$SRC_DIR/panel/routes" "$SRC_DIR/panel/vendor" \
    "$SRC_DIR/panel/composer.json" "$PREFIX/panel.new/"
run chown -R root:whacp "$PREFIX/panel.new"
run chmod -R u=rwX,g=rX,o= "$PREFIX/panel.new"
run chmod 0750 "$PREFIX/panel.new/bin/whacp"
if [[ -d $PREFIX/panel ]]; then run mv "$PREFIX/panel" "$PREFIX/panel.old.$(date +%s)"; fi
run mv "$PREFIX/panel.new" "$PREFIX/panel"
ok "panel in $PREFIX/panel, agent in $BIN"

# ------------------------------------------------------ 6. configuration ----
log "Writing configuration"
if [[ ! -f $CONF_DIR/agent.token ]]; then
    if [[ $DRY_RUN -eq 1 ]]; then
        echo "    [dry-run] generate $CONF_DIR/agent.token"
    else
        (umask 077; openssl rand -hex 32 > "$CONF_DIR/agent.token")
        chown root:whacp "$CONF_DIR/agent.token"
        chmod 0640 "$CONF_DIR/agent.token"
    fi
    ok "new agent token"
else
    ok "keeping existing agent token"
fi
if [[ ! -f $CONF_DIR/agent.json ]]; then
    run install -m 0640 -o root -g whacp "$SRC_DIR/installer/config/agent.json" "$CONF_DIR/agent.json"
fi
if [[ ! -f $CONF_DIR/panel.php ]]; then
    run install -m 0640 -o root -g whacp "$SRC_DIR/installer/config/panel.php" "$CONF_DIR/panel.php"
fi
ok "$CONF_DIR"

# --------------------------------------------------------- 7. services ----
log "Installing systemd services"
run install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent.service" /etc/systemd/system/whacp-agent.service
if [[ $DRY_RUN -eq 1 ]]; then
    echo "    [dry-run] install whacp-panel.service (port $PORT)"
else
    sed "s/127.0.0.1:8090/127.0.0.1:${PORT}/" "$SRC_DIR/installer/systemd/whacp-panel.service" > /etc/systemd/system/whacp-panel.service
    chmod 0644 /etc/systemd/system/whacp-panel.service
fi
run systemctl daemon-reload

log "Initialising the panel database"
run runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php php "$PREFIX/panel/bin/whacp" migrate

run systemctl enable --now whacp-agent.service
run systemctl enable --now whacp-panel.service
if [[ $REINSTALL -eq 1 ]]; then
    run systemctl restart whacp-agent.service whacp-panel.service
fi

# ---------------------------------------------------- 8. administrator ----
if [[ $DRY_RUN -eq 0 ]]; then
    users=$(runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php php "$PREFIX/panel/bin/whacp" users:count 2>/dev/null || echo 0)
    if [[ $users == 0 ]]; then
        log "Creating the first administrator"
        if [[ -z $ADMIN_USER ]]; then read -r -p "  Admin username [admin]: " ADMIN_USER; ADMIN_USER=${ADMIN_USER:-admin}; fi
        if [[ -z $ADMIN_EMAIL ]]; then read -r -p "  Admin email: " ADMIN_EMAIL; fi
        ADMIN_PASS="$(openssl rand -base64 18 | tr -d '/+=' | cut -c1-20)"
        printf '%s\n' "$ADMIN_PASS" | runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php php "$PREFIX/panel/bin/whacp" user:create "$ADMIN_USER" "$ADMIN_EMAIL" --role=admin
        CREATED_PASS=$ADMIN_PASS
    else
        ok "administrator already exists"
    fi
fi

# ------------------------------------------------------ 9. health checks ----
log "Running health checks"
if [[ $DRY_RUN -eq 0 ]]; then
    for _ in 1 2 3 4 5 6 7 8 9 10; do [[ -S /run/whacp/agent.sock ]] && break; sleep 1; done
    systemctl is-active --quiet whacp-agent && ok "whacp-agent is running" || warn "whacp-agent is not running: journalctl -u whacp-agent"
    runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php php "$PREFIX/panel/bin/whacp" agent:check && ok "panel can reach the agent" || warn "panel cannot reach the agent"
    for _ in 1 2 3 4 5; do curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 && break; sleep 1; done
    curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null && ok "panel answers on 127.0.0.1:${PORT}" || warn "panel is not answering: journalctl -u whacp-panel"
    runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php php "$PREFIX/panel/bin/whacp" discovery:run || warn "initial discovery failed"
fi

cat <<EOF

WhaCP is installed in read-only discovery mode. Nothing in your existing
Nginx, PHP, database, SSL or cron configuration was changed.

Open the panel through an SSH tunnel from your workstation:

    ssh -L ${PORT}:127.0.0.1:${PORT} $(hostname -f 2>/dev/null || hostname)
    then browse to http://127.0.0.1:${PORT}/

EOF
if [[ -n ${CREATED_PASS:-} ]]; then
    printf 'Administrator: %s\nPassword:      %s\n\nStore this password now; it is not saved anywhere and will not be shown again.\n' "$ADMIN_USER" "$CREATED_PASS"
fi

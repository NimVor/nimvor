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
#                               [--enable-writes]
#                               [--agent-only --remote-access=0.0.0.0:8443 --allow-from=PANEL_IP[,...]]
#
# --enable-writes switches the agent from read-only discovery to write mode
# (manage websites, databases, certificates, …). Without it WhaCP only observes.
#
# --agent-only installs just the agent on a server that another WhaCP panel
# will manage; --remote-access exposes it over TLS (pinned self-signed
# certificate) to the addresses given with --allow-from. The installer
# prints the address, pin and token to enter under "All servers" in the panel.

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
ENABLE_WRITES=0
AGENT_ONLY=0
REMOTE_ACCESS=""
ALLOW_FROM=""
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
        --enable-writes) ENABLE_WRITES=1 ;;
        --admin-user=*) ADMIN_USER="${arg#*=}" ;;
        --admin-email=*) ADMIN_EMAIL="${arg#*=}" ;;
        --port=*) PORT="${arg#*=}" ;;
        --agent-only) AGENT_ONLY=1 ;;
        --remote-access=*) REMOTE_ACCESS="${arg#*=}" ;;
        --allow-from=*) ALLOW_FROM="${arg#*=}" ;;
        -h|--help) sed -n '2,32p' "$0"; exit 0 ;;
        *) die "unknown option: $arg" ;;
    esac
done

[[ $PORT =~ ^[0-9]+$ ]] && (( PORT >= 1024 && PORT <= 65535 )) || die "--port must be between 1024 and 65535"
[[ -z $ADMIN_USER || $ADMIN_USER =~ ^[a-z][a-z0-9._-]{2,31}$ ]] || die "--admin-user is not a valid username"
if [[ -n $REMOTE_ACCESS ]]; then
    [[ $REMOTE_ACCESS =~ ^([0-9.]+|\[[0-9a-fA-F:]+\]):[0-9]{1,5}$ ]] || die "--remote-access must be ADDRESS:PORT, e.g. 0.0.0.0:8443"
    [[ -n $ALLOW_FROM ]] || die "--remote-access requires --allow-from=<panel server IP>"
    [[ $ALLOW_FROM =~ ^[0-9a-fA-F:./,]+$ ]] || die "--allow-from takes IP addresses or networks separated by commas"
fi

# ---------------------------------------------------------------- 1. OS ----
log "Detecting operating system"
[[ $EUID -eq 0 || $DRY_RUN -eq 1 ]] || die "run as root (sudo)"
[[ -r /etc/os-release ]] || die "/etc/os-release not found"
# shellcheck disable=SC1091
. /etc/os-release
supported=0
PKG=apt
MAJOR=${VERSION_ID%%.*}
if [[ ${ID:-} == ubuntu ]]; then
    for v in "${SUPPORTED_UBUNTU[@]}"; do [[ ${VERSION_ID:-} == "$v" ]] && supported=1; done
elif [[ ${ID:-} =~ ^(almalinux|rocky|rhel)$ ]]; then
    PKG=dnf
    [[ $MAJOR == 9 || $MAJOR == 10 ]] && supported=1
fi
if [[ $supported -eq 1 ]]; then
    ok "${PRETTY_NAME}"
elif [[ $FORCE_UNSUPPORTED -eq 1 ]]; then
    warn "${PRETTY_NAME:-unknown OS} is not supported; continuing because of --force-unsupported"
else
    die "${PRETTY_NAME:-unknown OS} is not supported (Ubuntu 22.04/24.04/26.04, AlmaLinux/Rocky Linux/RHEL 9 and 10). Use --force-unsupported to try anyway."
fi
command -v systemctl >/dev/null || die "systemd is required"

# ------------------------------------------------------- 2. requirements ----
log "Checking requirements"
missing_pkgs=()
php_ok=0
if [[ $AGENT_ONLY -eq 1 ]]; then
    php_ok=1 # the panel is not installed on this server
elif command -v php >/dev/null; then
    if php -r 'exit(PHP_VERSION_ID >= 80300 ? 0 : 1);'; then
        php_ok=1
        ok "PHP $(php -r 'echo PHP_VERSION;')"
        # extension -> package (Ubuntu: sodium and openssl ship in php-common)
        if [[ $PKG == dnf ]]; then
            declare -A ext_pkg=([pdo_sqlite]=php-pdo [curl]=php-common [mbstring]=php-mbstring [sodium]=php-sodium [openssl]=php-common)
        else
            declare -A ext_pkg=([pdo_sqlite]=php-sqlite3 [curl]=php-curl [mbstring]=php-mbstring [sodium]=php-common [openssl]=php-common)
        fi
        for ext in pdo_sqlite curl mbstring sodium openssl; do
            if php -m | grep -qi "^${ext}$"; then ok "php extension ${ext}"; else missing_pkgs+=("${ext_pkg[$ext]}"); fi
        done
    else
        warn "PHP $(php -r 'echo PHP_VERSION;') is older than 8.3"
    fi
fi
if [[ $php_ok -eq 0 ]]; then
    if [[ $PKG == dnf ]]; then
        missing_pkgs+=(php-cli php-pdo php-mbstring php-sodium)
    else
        missing_pkgs+=(php-cli php-sqlite3 php-curl php-mbstring)
    fi
fi
command -v openssl >/dev/null || missing_pkgs+=(openssl)
command -v curl >/dev/null || missing_pkgs+=(curl)
# SELinux labels for what WhaCP sets up (semanage).
if [[ $PKG == dnf ]] && ! command -v semanage >/dev/null; then missing_pkgs+=(policycoreutils-python-utils); fi

if [[ ${#missing_pkgs[@]} -gt 0 ]]; then
    if [[ $INSTALL_DEPS -eq 1 && $PKG == dnf ]]; then
        # EPEL and Remi provide PHP versions for websites (and PHP 8.3 for
        # the panel on 9, whose own PHP is older).
        log "Enabling the EPEL and Remi repositories"
        run dnf install -y -q epel-release
        rpm -q remi-release >/dev/null 2>&1 || run dnf install -y -q "https://rpms.remirepo.net/enterprise/remi-release-${MAJOR}.rpm"
        if [[ $MAJOR == 9 && $php_ok -eq 0 ]]; then
            run dnf module reset -y -q php
            run dnf module enable -y -q php:remi-8.3
        fi
        log "Installing dependencies: ${missing_pkgs[*]}"
        run dnf install -y -q "${missing_pkgs[@]}"
    elif [[ $INSTALL_DEPS -eq 1 ]]; then
        log "Installing dependencies: ${missing_pkgs[*]}"
        run apt-get update -q
        run env DEBIAN_FRONTEND=noninteractive apt-get install -y -q --no-install-recommends "${missing_pkgs[@]}"
    else
        die "missing packages: ${missing_pkgs[*]}. Install them ($PKG install ${missing_pkgs[*]}) or re-run with --install-deps."
    fi
fi
if [[ ${ID:-} == ubuntu && ${VERSION_ID:-} == "22.04" && $php_ok -eq 0 ]]; then
    warn "Ubuntu 22.04 ships PHP 8.1; WhaCP's panel needs PHP >= 8.3 (e.g. from a PHP PPA). Existing sites are unaffected: the panel only uses the CLI binary."
fi

# ------------------------------------------------------------ 3. conflicts ----
log "Checking for conflicts"
# install_missing lists what a finished installation has and this one lacks
# (an earlier run that was interrupted). The first administrator is created
# last, so a panel database without users means the run did not finish.
install_missing() {
    local f users
    [[ -x $BIN ]] || echo "$BIN"
    for f in agent.json agent.token; do [[ -s $CONF_DIR/$f ]] || echo "$CONF_DIR/$f"; done
    [[ -f /etc/systemd/system/whacp-agent.service ]] || echo "whacp-agent.service"
    [[ $AGENT_ONLY -eq 1 ]] && return 0
    [[ -f $PREFIX/panel/VERSION ]] || echo "$PREFIX/panel"
    for f in panel.php panel.key; do [[ -s $CONF_DIR/$f ]] || echo "$CONF_DIR/$f"; done
    [[ -f /etc/systemd/system/whacp-panel.service ]] || echo "whacp-panel.service"
    if [[ -f $PREFIX/panel/VERSION && -s $CONF_DIR/panel.php ]]; then
        users=$(runuser -u whacp -- env WHACP_CONFIG="$CONF_DIR/panel.php" php "$PREFIX/panel/bin/whacp" users:count 2>/dev/null || true)
        [[ $users =~ ^[1-9][0-9]*$ ]] || echo "the first administrator (panel database)"
    fi
}
if [[ -e $PREFIX || -e $CONF_DIR ]]; then
    mapfile -t missing < <(install_missing)
    if [[ $REINSTALL -eq 1 ]]; then
        confirm "WhaCP is already installed. Reinstall files (configuration, token and database are kept)?" || die "aborted"
    elif [[ ${#missing[@]} -gt 0 ]]; then
        die "an earlier WhaCP installation did not finish (missing: $(IFS=,; echo "${missing[*]}" | sed 's/,/, /g')).
Complete it by running the installer again with --reinstall and the same options, e.g.:
    sudo $0 --reinstall $*
Existing configuration, agent token, panel key and database are kept; missing parts are created."
    else
        die "WhaCP is already installed in $PREFIX. To upgrade, run installer/upgrade.sh (it backs up, migrates and rolls back on failure). To repair this version's files, run the installer with --reinstall."
    fi
fi
if [[ $AGENT_ONLY -eq 0 ]] && command -v ss >/dev/null && ss -ltn "sport = :$PORT" | grep -q LISTEN; then
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
    (cd "$SRC_DIR/agent" && run env CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(cat "$SRC_DIR/VERSION")" -o "$SRC_DIR/dist/whacp-agent" ./cmd/whacp-agent)
    AGENT_SRC=$SRC_DIR/dist/whacp-agent
else
    die "no prebuilt agent (dist/whacp-agent) and Go is not installed to build it"
fi
ok "$AGENT_SRC"

[[ $AGENT_ONLY -eq 1 || -f $SRC_DIR/panel/vendor/autoload.php ]] || {
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
if [[ $AGENT_ONLY -eq 0 ]]; then
    run rm -rf "$PREFIX/panel.new"
    run mkdir -p "$PREFIX/panel.new"
    run cp -a "$SRC_DIR/panel/app" "$SRC_DIR/panel/bin" "$SRC_DIR/panel/config" "$SRC_DIR/panel/database" \
        "$SRC_DIR/panel/public" "$SRC_DIR/panel/resources" "$SRC_DIR/panel/routes" "$SRC_DIR/panel/vendor" \
        "$SRC_DIR/panel/composer.json" "$SRC_DIR/VERSION" "$PREFIX/panel.new/"
    run chown -R root:whacp "$PREFIX/panel.new"
    run chmod -R u=rwX,g=rX,o= "$PREFIX/panel.new"
    run chmod 0750 "$PREFIX/panel.new/bin/whacp"
    if [[ -d $PREFIX/panel ]]; then run mv "$PREFIX/panel" "$PREFIX/panel.old.$(date +%s)"; fi
    run mv "$PREFIX/panel.new" "$PREFIX/panel"
fi
ok "panel in $PREFIX/panel, agent in $BIN"
if [[ $AGENT_ONLY -eq 0 ]]; then
    # Plugins are installed here by administrators (see docs/plugins).
    run install -d -m 0755 -o root -g root "$PREFIX/plugins"
    if [[ ! -e $PREFIX/plugins/example-status ]]; then
        run cp -a "$SRC_DIR/plugins/example-status" "$PREFIX/plugins/"
        run chown -R root:root "$PREFIX/plugins/example-status"
        run chmod -R u=rwX,go=rX "$PREFIX/plugins/example-status"
    fi
fi

# ------------------------------------------------------ 6. configuration ----
log "Writing configuration"
# Secrets are written to a temporary file and renamed, so an interrupted run
# never leaves an empty one behind; an empty file (from older installers)
# is treated as missing — nothing can have been encrypted with an empty key.
new_secret() {
    (umask 077; "${@:2}" > "$1.tmp")
    chown root:whacp "$1.tmp"
    chmod 0640 "$1.tmp"
    mv -f "$1.tmp" "$1"
}
if [[ ! -s $CONF_DIR/agent.token ]]; then
    if [[ $DRY_RUN -eq 1 ]]; then
        echo "    [dry-run] generate $CONF_DIR/agent.token"
    else
        new_secret "$CONF_DIR/agent.token" openssl rand -hex 32
    fi
    ok "new agent token"
else
    ok "keeping existing agent token"
fi
if [[ ! -s $CONF_DIR/panel.key ]]; then
    if [[ $DRY_RUN -eq 1 ]]; then
        echo "    [dry-run] generate $CONF_DIR/panel.key"
    else
        new_secret "$CONF_DIR/panel.key" openssl rand -base64 32
    fi
fi
if [[ ! -f $CONF_DIR/agent.json ]]; then
    run install -m 0640 -o root -g whacp "$SRC_DIR/installer/config/agent.json" "$CONF_DIR/agent.json"
fi
if [[ $AGENT_ONLY -eq 0 && ! -f $CONF_DIR/panel.php ]]; then
    run install -m 0640 -o root -g whacp "$SRC_DIR/installer/config/panel.php" "$CONF_DIR/panel.php"
fi
if [[ -n $REMOTE_ACCESS ]]; then
    clients=$(printf '%s' "$ALLOW_FROM" | awk -F, '{for (i = 1; i <= NF; i++) printf "%s\"%s\"", (i > 1 ? ", " : ""), $i}')
    if grep -q '"listen_tcp": ""' "$CONF_DIR/agent.json" 2>/dev/null; then
        run sed -i "s|\"listen_tcp\": \"\"|\"listen_tcp\": \"${REMOTE_ACCESS}\"|; s|\"allowed_clients\": \\[\\]|\"allowed_clients\": [${clients}]|" "$CONF_DIR/agent.json"
    else
        warn "$CONF_DIR/agent.json already sets listen_tcp or predates it; set listen_tcp and allowed_clients there by hand"
    fi
    run install -d -m 0755 /etc/systemd/system/whacp-agent.service.d
    run install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent-remote.conf" /etc/systemd/system/whacp-agent.service.d/20-remote.conf
    if [[ $DRY_RUN -eq 0 ]]; then
        AGENT_PIN=$("$BIN" -config "$CONF_DIR/agent.json" -pin)
    fi
fi
ok "$CONF_DIR"

# --------------------------------------------------------- 7. services ----
log "Installing systemd services"
run install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent.service" /etc/systemd/system/whacp-agent.service
run install -m 0644 "$SRC_DIR/installer/systemd/whacp-scheduler.service" /etc/systemd/system/whacp-scheduler.service
run install -m 0644 "$SRC_DIR/installer/systemd/whacp-scheduler.timer" /etc/systemd/system/whacp-scheduler.timer
if [[ $AGENT_ONLY -eq 1 ]]; then
    :
elif [[ $DRY_RUN -eq 1 ]]; then
    echo "    [dry-run] install whacp-panel.service (port $PORT)"
else
    sed "s/127.0.0.1:8090/127.0.0.1:${PORT}/" "$SRC_DIR/installer/systemd/whacp-panel.service" > /etc/systemd/system/whacp-panel.service
    chmod 0644 /etc/systemd/system/whacp-panel.service
fi
if [[ $ENABLE_WRITES -eq 1 ]]; then
    log "Enabling write mode"
    cat <<'NOTE'
    In write mode WhaCP creates websites, users, databases and certificates and
    reloads services. It never edits configuration it did not create, validates
    every change (nginx -t, php-fpm -t, sshd -t) and rolls back on failure.
NOTE
    confirm "Enable write mode?" || die "aborted (run again without --enable-writes for read-only mode)"
    run install -d -m 0755 /etc/systemd/system/whacp-agent.service.d
    run install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent-write-mode.conf" /etc/systemd/system/whacp-agent.service.d/10-write-mode.conf
    run sed -i 's/"allow_write_actions": false/"allow_write_actions": true/' "$CONF_DIR/agent.json"
    run install -d -m 0700 -o root -g root /var/lib/whacp-agent /var/backups/whacp
    run install -d -m 0755 -o root -g root /home/whacp /var/www/whacp-acme
fi
run systemctl daemon-reload

run systemctl enable --now whacp-agent.service
if [[ $AGENT_ONLY -eq 0 ]]; then
    log "Initialising the panel database"
    run runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php php "$PREFIX/panel/bin/whacp" migrate
    run systemctl enable --now whacp-panel.service
    run systemctl enable --now whacp-scheduler.timer
fi
if [[ $REINSTALL -eq 1 || $ENABLE_WRITES -eq 1 || -n $REMOTE_ACCESS ]]; then
    run systemctl restart whacp-agent.service
    [[ $AGENT_ONLY -eq 1 ]] || run systemctl restart whacp-panel.service
fi

# ---------------------------------------------------- 8. administrator ----
if [[ $DRY_RUN -eq 0 && $AGENT_ONLY -eq 0 ]]; then
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
fi
if [[ $DRY_RUN -eq 0 && $AGENT_ONLY -eq 0 ]]; then
    runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php php "$PREFIX/panel/bin/whacp" agent:check && ok "panel can reach the agent" || warn "panel cannot reach the agent"
    for _ in 1 2 3 4 5; do curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 && break; sleep 1; done
    curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null && ok "panel answers on 127.0.0.1:${PORT}" || warn "panel is not answering: journalctl -u whacp-panel"
    runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php php "$PREFIX/panel/bin/whacp" discovery:run || warn "initial discovery failed"
fi

if [[ $AGENT_ONLY -eq 1 ]]; then
    cat <<EOF

The WhaCP agent is installed ($({ [[ $ENABLE_WRITES -eq 1 ]] || grep -q '"allow_write_actions": true' "$CONF_DIR/agent.json" 2>/dev/null; } && echo "write mode" || echo "read-only discovery mode")).
EOF
    if [[ -n $REMOTE_ACCESS ]]; then
        cat <<EOF
Connect it from your WhaCP panel under "All servers" with:

    Agent address:   https://$(hostname -I 2>/dev/null | awk '{print $1}'):${REMOTE_ACCESS##*:}
    Certificate pin: ${AGENT_PIN:-run: whacp-agent -pin}
    Agent token:     $(cat "$CONF_DIR/agent.token" 2>/dev/null)

Only ${ALLOW_FROM} may connect. Allow the port in the firewall for that
address only, e.g.:
EOF
        if [[ $PKG == dnf ]]; then
            echo "    firewall-cmd --permanent --add-rich-rule='rule family=\"ipv4\" source address=\"${ALLOW_FROM%%,*}\" port port=\"${REMOTE_ACCESS##*:}\" protocol=\"tcp\" accept' && firewall-cmd --reload"
        else
            echo "    ufw allow from ${ALLOW_FROM%%,*} to any port ${REMOTE_ACCESS##*:} proto tcp"
        fi
    fi
    exit 0
fi

cat <<EOF

WhaCP is installed ($({ [[ $ENABLE_WRITES -eq 1 ]] || grep -q '"allow_write_actions": true' "$CONF_DIR/agent.json" 2>/dev/null; } && echo "write mode" || echo "read-only discovery mode")).
Nothing in your existing Nginx, PHP, database, SSL or cron configuration was
changed. WhaCP only ever edits files it creates itself (named whacp-*).

Open the panel through an SSH tunnel from your workstation:

    ssh -L ${PORT}:127.0.0.1:${PORT} $(hostname -f 2>/dev/null || hostname)
    then browse to http://127.0.0.1:${PORT}/

EOF
if [[ -n ${CREATED_PASS:-} ]]; then
    printf 'Administrator: %s\nPassword:      %s\n\nStore this password now; it is not saved anywhere and will not be shown again.\n' "$ADMIN_USER" "$CREATED_PASS"
fi

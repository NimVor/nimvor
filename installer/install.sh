#!/usr/bin/env bash
#
# WhaCP installer (pure-Go).
#
# What it does:
#   1. Detects the OS and checks requirements.
#   2. Detects conflicts (existing WhaCP install, port in use).
#   3. Installs missing OS dependencies (--no-install-deps: only report them).
#   4. Creates the "whacp" system user and group.
#   5. Builds/copies the whacp-agent and whacp (server) binaries.
#   6. Writes /etc/whacp configuration and a fresh agent token.
#   7. Installs and starts the systemd services.
#   8. Creates the first administrator.
#   9. Runs health checks.
#
# Server profiles (full/web/minimal/none) continue to be driven by the
# panel via the agent — this installer only enables write mode and
# installs OS packages for the profile; the server then applies it via
# its setup:apply handler on first login.
#
# Usage (from a source checkout or release archive):
#   sudo ./installer/install.sh [--no-install-deps] [--admin-user=NAME] [--admin-email=EMAIL]
#                               [--port=8090] [--dry-run] [--yes] [--reinstall|--resume] [--force-unsupported]
#                               [--enable-writes] [--public-https[=8443]]
#                               [--profile=full|web|minimal|none] [--hostname=server.example.com]
#                               [--php=8.3,8.4] [--no-third-party-repos]
#                               [--agent-only --remote-access=0.0.0.0:8443 --allow-from=PANEL_IP[,...]]

set -Eeuo pipefail
umask 027

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PREFIX=/opt/whacp
CONF_DIR=/etc/whacp
DATA_DIR=/var/lib/whacp
LOG_DIR=/var/log/whacp
BIN_AGENT=/usr/local/bin/whacp-agent
BIN_PANEL=/usr/local/bin/whacp
PORT=8090
ADMIN_USER=""
ADMIN_EMAIL=""
INSTALL_DEPS=1
PORT_SET=0
DRY_RUN=0
ASSUME_YES=0
REINSTALL=0
FORCE_UNSUPPORTED=0
ENABLE_WRITES=0
AGENT_ONLY=0
REMOTE_ACCESS=""
ALLOW_FROM=""
PUBLIC_HTTPS=""
TERMINAL_PORT=8091
PROFILE=""
PROFILE_SET=0
SETUP_HOSTNAME=""
SETUP_PHP=""
THIRD_PARTY_REPOS=1
SUPPORTED_UBUNTU=("22.04" "24.04")
PREVIEW_UBUNTU=("26.04")
SUPPORTED_LIST="Ubuntu 22.04/24.04, AlmaLinux/Rocky Linux/RHEL 9; preview: Ubuntu 26.04, AlmaLinux/Rocky Linux/RHEL 10"

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
    if ! { exec 3</dev/tty; } 2>/dev/null; then
        die "$1 — no terminal to ask on. Run the installer again with --yes to answer yes."
    fi
    if ! read -r -u 3 -p "$1 [y/N] " reply; then
        exec 3<&-
        die "$1 — no answer could be read. Run the installer again with --yes to answer yes."
    fi
    exec 3<&-
    [[ $reply =~ ^[Yy]$ ]]
}

for arg in "$@"; do
    case "$arg" in
        --no-install-deps) INSTALL_DEPS=0 ;;
        --dry-run) DRY_RUN=1 ;;
        --yes|-y) ASSUME_YES=1 ;;
        --reinstall|--resume) REINSTALL=1 ;;
        --force-unsupported) FORCE_UNSUPPORTED=1 ;;
        --enable-writes) ENABLE_WRITES=1 ;;
        --admin-user=*) ADMIN_USER="${arg#*=}" ;;
        --admin-email=*) ADMIN_EMAIL="${arg#*=}" ;;
        --port=*) PORT="${arg#*=}"; PORT_SET=1 ;;
        --agent-only) AGENT_ONLY=1 ;;
        --remote-access=*) REMOTE_ACCESS="${arg#*=}" ;;
        --allow-from=*) ALLOW_FROM="${arg#*=}" ;;
        --public-https) PUBLIC_HTTPS=8443 ;;
        --public-https=*) PUBLIC_HTTPS="${arg#*=}" ;;
        --profile=*) PROFILE="${arg#*=}"; PROFILE_SET=1 ;;
        --hostname=*) SETUP_HOSTNAME="${arg#*=}" ;;
        --php=*) SETUP_PHP="${arg#*=}" ;;
        --no-third-party-repos) THIRD_PARTY_REPOS=0 ;;
        -h|--help) sed -n '2,60p' "$0"; exit 0 ;;
        *) die "unknown option: $arg" ;;
    esac
done

[[ $PORT =~ ^[0-9]+$ ]] && (( PORT >= 1024 && PORT <= 65535 )) || die "--port must be between 1024 and 65535"
if [[ -n $PUBLIC_HTTPS ]]; then
    [[ $PUBLIC_HTTPS =~ ^[0-9]+$ ]] && (( PUBLIC_HTTPS >= 1024 && PUBLIC_HTTPS <= 65535 )) || die "--public-https takes a port between 1024 and 65535"
    (( PUBLIC_HTTPS != PORT )) || die "--public-https needs a port other than the panel's own (--port=$PORT)"
    [[ $AGENT_ONLY -eq 0 ]] || die "--public-https serves the panel; --agent-only installs no panel"
fi
[[ -z $ADMIN_USER || $ADMIN_USER =~ ^[a-z][a-z0-9._-]{2,31}$ ]] || die "--admin-user is not a valid username"
[[ -z $PROFILE || $PROFILE =~ ^(full|web|minimal|none)$ ]] || die "--profile must be full, web, minimal or none"
[[ -z $SETUP_HOSTNAME || $SETUP_HOSTNAME =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$ ]] || die "--hostname must be the server's full host name, e.g. server.example.com"

# ---------------------------------------------------------------- 1. OS ----
log "Detecting operating system"
[[ $EUID -eq 0 || $DRY_RUN -eq 1 ]] || die "run as root (sudo)"
[[ -r /etc/os-release ]] || die "/etc/os-release not found"
# shellcheck disable=SC1091
. /etc/os-release
supported=0; preview=0; PKG=apt; MAJOR=${VERSION_ID%%.*}
if [[ ${ID:-} == ubuntu ]]; then
    for v in "${SUPPORTED_UBUNTU[@]}"; do [[ ${VERSION_ID:-} == "$v" ]] && supported=1; done
    for v in "${PREVIEW_UBUNTU[@]}"; do [[ ${VERSION_ID:-} == "$v" ]] && preview=1; done
elif [[ ${ID:-} =~ ^(almalinux|rocky|rhel)$ ]]; then
    PKG=dnf
    [[ $MAJOR == 9 ]] && supported=1
    [[ $MAJOR == 10 ]] && preview=1
fi
if [[ $supported -eq 1 ]]; then
    ok "${PRETTY_NAME}"
elif [[ $preview -eq 1 ]]; then
    ok "${PRETTY_NAME}"; warn "${PRETTY_NAME} is a preview system"
elif [[ $FORCE_UNSUPPORTED -eq 1 ]]; then
    warn "${PRETTY_NAME:-unknown OS} is not supported; continuing because of --force-unsupported"
else
    die "${PRETTY_NAME:-unknown OS} is not supported (${SUPPORTED_LIST}). Use --force-unsupported to try anyway."
fi
command -v systemctl >/dev/null || die "systemd is required"
[[ -d /run/systemd/system ]] || warn "systemd is not running as the init system (a container?): services cannot be started"
ARCH=$(uname -m)
SELINUX_MODE=none
command -v getenforce >/dev/null && SELINUX_MODE=$(getenforce 2>/dev/null || echo unknown)
FIREWALL=none
if command -v firewall-cmd >/dev/null; then FIREWALL=firewalld; elif command -v ufw >/dev/null; then FIREWALL=ufw; fi
ok "family $([[ $PKG == dnf ]] && echo rhel || echo debian), $ARCH, package manager $PKG, SELinux ${SELINUX_MODE,,}, firewall $FIREWALL"
command -v openssl >/dev/null || die "openssl is required"
command -v curl >/dev/null || die "curl is required"
command -v bwrap >/dev/null || die "bubblewrap is required (for jailed shells)"
APT_WAIT=(-o DPkg::Lock::Timeout=600)

# ---------------------------------------------------- 1b. server profile ----
log "Server profile"
MEM_MB=$(awk '/^MemTotal:/ {print int($2/1024)}' /proc/meminfo 2>/dev/null || true)
MEM_MB=${MEM_MB:-0}
DISK_FREE_GB=$(df -Pm / 2>/dev/null | awk 'NR==2 {print int($4/1024)}' || true)
ok "memory ${MEM_MB} MB, free disk ${DISK_FREE_GB:-?} GB on /"
if [[ -z $PROFILE ]]; then
    busy=()
    for u in nginx apache2 httpd postfix exim4 named bind9 mariadb mysql mysqld; do
        if systemctl is-active --quiet "$u" 2>/dev/null; then busy+=("$u"); fi
    done
    if [[ $REINSTALL -eq 1 || $AGENT_ONLY -eq 1 || -x $BIN_PANEL ]]; then
        PROFILE=none
    elif (( ${#busy[@]} > 0 )); then
        PROFILE=none
        warn "running already: ${busy[*]}. No server profile is applied."
    elif (( MEM_MB >= 3800 )); then PROFILE=full
    elif (( MEM_MB >= 1800 )); then PROFILE=web
    else PROFILE=minimal
    fi
fi
if [[ $PROFILE != none ]]; then
    case $PROFILE in
        full) desc="full hosting server: web, PHP, MariaDB, email, DNS, FTP, Redis, Memcached, ClamAV, firewall, fail2ban" ;;
        web) desc="web hosting only: web, PHP, MariaDB, FTP, Redis, Memcached, firewall, fail2ban" ;;
        *) desc="minimal: web, PHP, MariaDB, firewall, fail2ban" ;;
    esac
    ok "profile $PROFILE ($desc)"
    if [[ $DRY_RUN -eq 0 ]]; then
        if confirm "Set up this server as: $desc?"; then ENABLE_WRITES=1; else PROFILE=none; fi
    fi
else
    ok "no server profile"
fi

# ------------------------------------------------------- 2. requirements ----
log "Checking requirements"
missing_pkgs=()

if [[ $PKG == dnf ]]; then
    PANEL_SITE=/etc/nginx/whacp-available/whacp-panel.conf
    PANEL_SITE_LINK=/etc/nginx/conf.d/whacp-panel.conf
    PANEL_CERT=/etc/pki/tls/certs/whacp-panel.crt
    PANEL_CERT_KEY=/etc/pki/tls/private/whacp-panel.key
else
    PANEL_SITE=/etc/nginx/sites-available/whacp-panel.conf
    PANEL_SITE_LINK=/etc/nginx/sites-enabled/whacp-panel.conf
    PANEL_CERT=/etc/ssl/certs/whacp-panel.crt
    PANEL_CERT_KEY=/etc/ssl/private/whacp-panel.key
fi

if [[ $REINSTALL -eq 1 ]]; then
    if [[ $PORT_SET -eq 0 && -f $CONF_DIR/server.env ]]; then
        prev=$(grep -oE '^WHACP_LISTEN=127\.0\.0\.1:[0-9]+' "$CONF_DIR/server.env" | head -1 | cut -d: -f3)
        if [[ $prev =~ ^[0-9]+$ ]] && (( prev >= 1024 && prev <= 65535 )); then PORT=$prev; fi
    fi
    if [[ -z $PUBLIC_HTTPS && $AGENT_ONLY -eq 0 && -f $PANEL_SITE ]]; then
        prev=$(grep -oE 'listen[[:space:]]+(\[::\]:)?[0-9]+[[:space:]]+ssl' "$PANEL_SITE" | grep -oE '[0-9]+' | head -1)
        if [[ $prev =~ ^[0-9]+$ ]] && (( prev >= 1024 && prev <= 65535 && prev != PORT )); then
            PUBLIC_HTTPS=$prev; ok "keeping the panel's public HTTPS port ${PUBLIC_HTTPS}"
        fi
    fi
fi

command -v nft >/dev/null || [[ -x /usr/sbin/nft ]] || missing_pkgs+=(nftables)
if [[ -n $PUBLIC_HTTPS ]]; then
    command -v nginx >/dev/null || missing_pkgs+=(nginx)
fi
if [[ $PKG == dnf ]] && ! command -v semanage >/dev/null; then missing_pkgs+=(policycoreutils-python-utils); fi

if [[ ${#missing_pkgs[@]} -gt 0 ]]; then
    if [[ $INSTALL_DEPS -eq 1 ]]; then
        log "Installing dependencies: ${missing_pkgs[*]}"
        if [[ $PKG == dnf ]]; then
            run dnf install -y -q epel-release dnf-plugins-core
            run dnf install -y -q "${missing_pkgs[@]}"
        else
            run apt-get "${APT_WAIT[@]}" update -q
            run env DEBIAN_FRONTEND=noninteractive apt-get "${APT_WAIT[@]}" install -y -q --no-install-recommends "${missing_pkgs[@]}"
        fi
    else
        die "missing packages: ${missing_pkgs[*]}. Install them ($PKG install ${missing_pkgs[*]}) or re-run without --no-install-deps."
    fi
fi

# AppArmor bubblewrap userns profile.
if [[ -r /proc/sys/kernel/apparmor_restrict_unprivileged_userns && $(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns) == 1 ]] && command -v apparmor_parser >/dev/null; then
    cat > /etc/apparmor.d/whacp-bwrap <<'PROFILE'
# Managed by WhaCP.
abi <abi/4.0>,
include <tunables/global>

profile whacp-bwrap /usr/local/lib/whacp/bwrap flags=(unconfined) {
  userns,
  include if exists <local/whacp-bwrap>
}
PROFILE
    run apparmor_parser -r /etc/apparmor.d/whacp-bwrap || warn "AppArmor profile for jailed shells could not be loaded"
fi

# ------------------------------------------------------------ 3. conflicts ----
log "Checking for conflicts"
install_missing() {
    [[ -x $BIN_AGENT ]] || echo "$BIN_AGENT"
    for f in agent.json agent.token; do [[ -s $CONF_DIR/$f ]] || echo "$CONF_DIR/$f"; done
    [[ -f /etc/systemd/system/whacp-agent.service ]] || echo "whacp-agent.service"
    [[ $AGENT_ONLY -eq 1 ]] && return 0
    [[ -x $BIN_PANEL ]] || echo "$BIN_PANEL"
    for f in server.env panel.key; do [[ -s $CONF_DIR/$f ]] || echo "$CONF_DIR/$f"; done
    [[ -f /etc/systemd/system/whacp-panel.service ]] || echo "whacp-panel.service"
}
if [[ -e $PREFIX || -e $CONF_DIR || -x $BIN_AGENT || -x $BIN_PANEL ]]; then
    mapfile -t missing < <(install_missing)
    if [[ $REINSTALL -eq 1 ]]; then
        confirm "WhaCP is already installed. Reinstall files (configuration, token and database are kept)?" || die "aborted"
    elif [[ ${#missing[@]} -gt 0 ]]; then
        die "an earlier WhaCP installation did not finish (missing: $(IFS=,; echo "${missing[*]}" | sed 's/,/, /g')).
Complete it by running the installer again with --resume."
    else
        die "WhaCP is already installed. To upgrade, run installer/upgrade.sh. To repair, run with --reinstall."
    fi
fi
if [[ $AGENT_ONLY -eq 0 ]] && command -v ss >/dev/null && grep -q LISTEN <<<"$(ss -ltn "sport = :$PORT")"; then
    if ! systemctl is-active --quiet whacp-panel 2>/dev/null; then
        die "port $PORT is already in use; choose another with --port="
    fi
fi
ok "no conflicts"

find_tool() {
    local name=$1 c; shift
    for c in "$(command -v "$name" 2>/dev/null || true)" "$@"; do
        [[ -n $c && -x $c && ! -d $c ]] && { printf '%s\n' "$c"; return 0; }
    done
    return 1
}
find_go() {
    find_tool go /usr/local/go/bin/go /usr/lib/golang/bin/go /snap/bin/go $(ls -d /usr/lib/go-*/bin/go 2>/dev/null | sort -V -r) /usr/lib/go/bin/go
}

# --------------------------------------------------------- 4. build binaries ----
log "Preparing the whacp binaries"
src_owner=$(stat -c %U "$SRC_DIR" 2>/dev/null || echo root)
give_back() {
    [[ $src_owner != root && $DRY_RUN -eq 0 ]] || return 0
    chown -R "$src_owner": "$@" 2>/dev/null || true
}
if [[ -d $SRC_DIR/.git ]]; then
    if [[ -n $(find "$SRC_DIR/.git" ! -user "$src_owner" -print -quit 2>/dev/null) ]] && [[ $src_owner != root ]]; then
        run chown -R "$src_owner": "$SRC_DIR/.git"
        ok "returned $SRC_DIR/.git to $src_owner"
    fi
    ok "sources: commit $(git --no-optional-locks -C "$SRC_DIR" -c safe.directory="$SRC_DIR" log -1 --format='%h %cs %s' 2>/dev/null | cut -c1-80)"
fi

GO_MIN=1.24
GO_DIR=/usr/local/lib/whacp-go
go_version() { local v; v=$("$1" version 2>/dev/null | awk '{print $3}'); printf '%s\n' "${v#go}"; }
ver_ge() { [[ $(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1) == "$2" ]]; }
go_usable() { local v; v=$(go_version "$1"); [[ -n $v ]] && ver_ge "$v" "$GO_MIN"; }

build_go_bin() {
    local module="$1" out="$2" main_pkg="$3"
    local version
    version=$(cat "$SRC_DIR/VERSION")
    if commit=$(git -C "$SRC_DIR" -c safe.directory="$SRC_DIR" rev-parse --short HEAD 2>/dev/null); then
        version+="+$commit"
    fi
    (cd "$SRC_DIR/$module" && run env CGO_ENABLED=0 HOME="${HOME:-/root}" GOTOOLCHAIN=auto "$GO" build -trimpath \
        -ldflags "-s -w -X main.version=$version" -o "$out" "$main_pkg")
}

GO=$(find_go || true)
if [[ -x $GO_DIR/go/bin/go ]] && { [[ -z $GO ]] || ! go_usable "$GO"; } && go_usable "$GO_DIR/go/bin/go"; then
    GO=$GO_DIR/go/bin/go
fi

need_build_agent=1
need_build_panel=1
if [[ -x $SRC_DIR/dist/whacp-agent ]]; then need_build_agent=0; fi
if [[ $AGENT_ONLY -eq 0 && -x $SRC_DIR/dist/whacp ]]; then need_build_panel=0; fi

if [[ ($need_build_agent -eq 1 || $need_build_panel -eq 1) && -z $GO ]]; then
    if [[ $INSTALL_DEPS -eq 1 ]]; then
        log "Installing Go to build the binaries"
        if [[ $PKG == dnf ]]; then
            run dnf install -y -q golang
            GO=$(find_go || true)
        fi
        if [[ -z $GO ]] || ! go_usable "$GO"; then
            # Fetch Go from go.dev.
            log "Downloading Go ${GO_MIN} from go.dev into $GO_DIR"
            if [[ $DRY_RUN -eq 0 ]]; then
                arch=amd64; [[ $ARCH == aarch64 ]] && arch=arm64
                f="go${GO_MIN}.linux-${arch}.tar.gz"
                tmp=$(mktemp -d)
                curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "$tmp/$f" "https://go.dev/dl/$f" || die "could not download Go"
                rm -rf "$GO_DIR.new" && mkdir -p "$GO_DIR.new" && tar -C "$GO_DIR.new" -xzf "$tmp/$f"
                rm -rf "$GO_DIR" && mv "$GO_DIR.new/go" "$GO_DIR"
                rm -rf "$tmp"
                ok "Go ${GO_MIN} in $GO_DIR"
            fi
            GO=$GO_DIR/bin/go
        fi
    else
        die "Go ${GO_MIN}+ is required to build WhaCP; install Go or re-run without --no-install-deps."
    fi
fi
if [[ -n $GO ]]; then ok "Go: $(go_version "$GO") ($GO)"; fi

run mkdir -p "$SRC_DIR/dist"
if [[ $need_build_agent -eq 1 ]]; then
    log "Building whacp-agent"
    build_go_bin agent "$SRC_DIR/dist/whacp-agent" ./cmd/whacp-agent
fi
if [[ $AGENT_ONLY -eq 0 && $need_build_panel -eq 1 ]]; then
    log "Building whacp (panel server)"
    build_go_bin server "$SRC_DIR/dist/whacp" ./cmd/whacp
fi
give_back "$SRC_DIR/dist"
ok "binaries ready"

# ----------------------------------------------------- 5. user and files ----
log "Creating system user and directories"
if ! getent group whacp >/dev/null; then run groupadd --system whacp; fi
if ! id whacp >/dev/null 2>&1; then
    run useradd --system --gid whacp --home-dir "$DATA_DIR" --no-create-home --shell /usr/sbin/nologin --comment "WhaCP panel" whacp
fi
ok "user whacp"
run install -d -m 0755 -o root -g root "$PREFIX" "$PREFIX/plugins"
run install -d -m 0750 -o root -g whacp "$CONF_DIR"
run install -d -m 0750 -o whacp -g whacp "$DATA_DIR" "$DATA_DIR/sessions" "$LOG_DIR"

log "Installing files"
run install -m 0755 -o root -g root "$SRC_DIR/dist/whacp-agent" "$BIN_AGENT"
if [[ $AGENT_ONLY -eq 0 ]]; then
    run install -m 0755 -o root -g root "$SRC_DIR/dist/whacp" "$BIN_PANEL"
fi
if [[ $AGENT_ONLY -eq 0 && ! -e $PREFIX/plugins/example-status ]]; then
    run cp -a "$SRC_DIR/plugins/example-status" "$PREFIX/plugins/"
    run chown -R root:root "$PREFIX/plugins/example-status"
    run chmod -R u=rwX,go=rX "$PREFIX/plugins/example-status"
fi

# ------------------------------------------------------ 6. configuration ----
log "Writing configuration"
new_secret() {
    (umask 077; "${@:2}" > "$1.tmp")
    chown root:whacp "$1.tmp"
    chmod 0640 "$1.tmp"
    mv -f "$1.tmp" "$1"
}
if [[ ! -s $CONF_DIR/agent.token ]]; then
    [[ $DRY_RUN -eq 1 ]] && echo "    [dry-run] generate $CONF_DIR/agent.token" \
        || new_secret "$CONF_DIR/agent.token" openssl rand -hex 32
    ok "new agent token"
else
    ok "keeping existing agent token"
fi
if [[ ! -s $CONF_DIR/panel.key ]]; then
    [[ $DRY_RUN -eq 1 ]] && echo "    [dry-run] generate $CONF_DIR/panel.key" \
        || new_secret "$CONF_DIR/panel.key" openssl rand -base64 32
fi
if [[ ! -f $CONF_DIR/agent.json ]]; then
    run install -m 0640 -o root -g whacp "$SRC_DIR/installer/config/agent.json" "$CONF_DIR/agent.json"
fi
if [[ $SRC_DIR != *[\"\\]* ]]; then
    printf '{"path": "%s"}\n' "$SRC_DIR" > "$CONF_DIR/source.json"
    chmod 0644 "$CONF_DIR/source.json"
fi
if [[ $AGENT_ONLY -eq 0 && ! -f $CONF_DIR/server.env ]]; then
    sed "s/^WHACP_LISTEN=127\\.0\\.0\\.1:8090/WHACP_LISTEN=127.0.0.1:${PORT}/" \
        "$SRC_DIR/installer/config/server.env" > "$CONF_DIR/server.env.tmp"
    chown root:whacp "$CONF_DIR/server.env.tmp"
    chmod 0640 "$CONF_DIR/server.env.tmp"
    mv -f "$CONF_DIR/server.env.tmp" "$CONF_DIR/server.env"
fi
if [[ -n $REMOTE_ACCESS ]]; then
    clients=$(printf '%s' "$ALLOW_FROM" | awk -F, '{for (i = 1; i <= NF; i++) printf "%s\"%s\"", (i > 1 ? ", " : ""), $i}')
    run install -d -m 0755 /etc/systemd/system/whacp-agent.service.d
    run install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent-remote.conf" /etc/systemd/system/whacp-agent.service.d/20-remote.conf
    if grep -q '"listen_tcp": ""' "$CONF_DIR/agent.json" 2>/dev/null; then
        run sed -i "s|\"listen_tcp\": \"\"|\"listen_tcp\": \"${REMOTE_ACCESS}\"|; s|\"allowed_clients\": \\[\\]|\"allowed_clients\": [${clients}]|" "$CONF_DIR/agent.json"
    fi
    if [[ $DRY_RUN -eq 0 ]]; then
        AGENT_PIN=$("$BIN_AGENT" -config "$CONF_DIR/agent.json" -pin)
    fi
fi
ok "$CONF_DIR"

# --------------------------------------------------------- 7. services ----
log "Installing systemd services"
run install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent.service" /etc/systemd/system/whacp-agent.service
if [[ $AGENT_ONLY -eq 0 ]]; then
    run install -m 0644 "$SRC_DIR/installer/systemd/whacp-panel.service" /etc/systemd/system/whacp-panel.service
fi
if [[ $ENABLE_WRITES -eq 1 ]]; then
    log "Enabling write mode"
    run install -d -m 0755 /etc/systemd/system/whacp-agent.service.d
    run install -m 0644 "$SRC_DIR/installer/systemd/whacp-agent-write-mode.conf" /etc/systemd/system/whacp-agent.service.d/10-write-mode.conf
    run sed -i 's/"allow_write_actions": false/"allow_write_actions": true/' "$CONF_DIR/agent.json"
    run install -d -m 0700 -o root -g root /var/lib/whacp-agent /var/backups/whacp
    run install -d -m 0755 -o root -g root /home/whacp /var/www/whacp-acme
fi
run systemctl daemon-reload
if [[ $PKG == dnf && $SELINUX_MODE != none && $SELINUX_MODE != Disabled ]] && command -v semodule >/dev/null; then
    log "Installing SELinux policy module"
    run semodule -i "$SRC_DIR/installer/selinux/whacp_postfix_sockets.cil"
fi
run systemctl enable --now whacp-agent.service
if [[ $AGENT_ONLY -eq 0 ]]; then
    log "Starting the panel (auto-migrates the database)"
    run systemctl enable --now whacp-panel.service
fi
if [[ $REINSTALL -eq 1 || $ENABLE_WRITES -eq 1 || -n $REMOTE_ACCESS ]]; then
    run systemctl restart whacp-agent.service
    [[ $AGENT_ONLY -eq 1 ]] || run systemctl restart whacp-panel.service
fi
for unit in whacp-greylist.service whacp-mailrate.service; do
    if [[ -f /etc/systemd/system/$unit ]]; then
        run systemctl try-restart "$unit" || warn "$unit could not be restarted"
    fi
done

# ---------------------------------------------------- 8. administrator ----
if [[ $DRY_RUN -eq 0 && $AGENT_ONLY -eq 0 ]]; then
    if runuser -u whacp -- "$BIN_PANEL" -has-admin 2>/dev/null | grep -q yes; then
        ok "administrator already exists"
    else
        log "Creating the first administrator"
        [[ -n $ADMIN_USER ]] || { read -r -p "  Admin username [admin]: " ADMIN_USER; ADMIN_USER=${ADMIN_USER:-admin}; }
        [[ -n $ADMIN_EMAIL ]] || { read -r -p "  Admin email: " ADMIN_EMAIL; }
        ADMIN_PASS="$(openssl rand -base64 18 | tr -d '/+=' | cut -c1-20)"
        printf '%s\n' "$ADMIN_PASS" | runuser -u whacp -- "$BIN_PANEL" bootstrap-admin \
            --username "$ADMIN_USER" --email "$ADMIN_EMAIL" --role admin
        CREATED_PASS=$ADMIN_PASS
    fi
fi

# ------------------------------------------------------- 8b. public HTTPS ----
if [[ -n $PUBLIC_HTTPS ]]; then
    log "Serving the panel over HTTPS on port ${PUBLIC_HTTPS}"
    if [[ -s $PANEL_SITE ]]; then
        cur_cert=$(awk '$1=="ssl_certificate"{print $2}' "$PANEL_SITE" | tr -d ';' | head -1)
        cur_key=$(awk '$1=="ssl_certificate_key"{print $2}' "$PANEL_SITE" | tr -d ';' | head -1)
        if [[ -n $cur_cert && -s $cur_cert && -n $cur_key && -s $cur_key ]]; then
            PANEL_CERT=$cur_cert; PANEL_CERT_KEY=$cur_key
            ok "keeping the panel's chosen certificate ($PANEL_CERT)"
        fi
    fi
    if [[ ! -s $PANEL_CERT || ! -s $PANEL_CERT_KEY ]]; then
        fqdn=$(hostname -f 2>/dev/null || hostname)
        san="DNS:${fqdn}"; cn=$fqdn
        (( ${#cn} <= 64 )) || cn=$(hostname -s 2>/dev/null || true)
        [[ -n $cn && ${#cn} -le 64 ]] || cn="WhaCP panel"
        for ip in $(hostname -I 2>/dev/null); do san="${san},IP:${ip}"; done
        run install -d -m 0755 "$(dirname "$PANEL_CERT")"
        run install -d -m 0700 "$(dirname "$PANEL_CERT_KEY")"
        if [[ $DRY_RUN -ne 1 ]]; then
            openssl req -x509 -newkey rsa:2048 -nodes -days 825 -subj "/CN=${cn}" \
                -addext "subjectAltName=${san}" -keyout "$PANEL_CERT_KEY" -out "$PANEL_CERT" 2>/dev/null \
                || die "the certificate could not be created"
            chmod 0600 "$PANEL_CERT_KEY"
        fi
        ok "self-signed certificate for ${san}"
    fi
    listen6=""
    [[ -e /proc/net/if_inet6 ]] && listen6="    listen [::]:${PUBLIC_HTTPS} ssl;"
    install -d -m 0755 "$(dirname "$PANEL_SITE")"
    cat > "$PANEL_SITE" <<NGINX
# Managed by WhaCP (install.sh --public-https).
server {
    listen ${PUBLIC_HTTPS} ssl;
${listen6}
    server_name _;
    ssl_certificate     ${PANEL_CERT};
    ssl_certificate_key ${PANEL_CERT_KEY};
    ssl_protocols TLSv1.2 TLSv1.3;
    error_page 497 =301 https://\$http_host\$request_uri;
    client_max_body_size 256m;
    location = /terminal/ws {
        proxy_pass http://127.0.0.1:${TERMINAL_PORT};
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$http_host;
        proxy_set_header X-Forwarded-For \$remote_addr;
        proxy_buffering off;
        proxy_read_timeout 1h;
        proxy_send_timeout 1h;
    }
    location / {
        proxy_pass http://127.0.0.1:${PORT};
        proxy_set_header Host \$http_host;
        proxy_set_header X-Forwarded-For \$remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 300s;
    }
}
NGINX
    chmod 0644 "$PANEL_SITE"
    ln -sfn "$PANEL_SITE" "$PANEL_SITE_LINK"
    if [[ $PKG == dnf ]] && command -v getenforce >/dev/null && [[ $(getenforce) != Disabled ]]; then
        run restorecon "$PANEL_CERT" "$PANEL_CERT_KEY" "$PANEL_SITE"
        run setsebool -P httpd_can_network_connect 1
        semanage port -l 2>/dev/null | grep -E '^http_port_t\s' | grep -qw "${PUBLIC_HTTPS}" \
            || run semanage port -a -t http_port_t -p tcp "${PUBLIC_HTTPS}" \
            || run semanage port -m -t http_port_t -p tcp "${PUBLIC_HTTPS}"
    fi
    if [[ $DRY_RUN -eq 0 ]]; then
        nginx -t 2>/dev/null || { nginx -t; rm -f "$PANEL_SITE_LINK" "$PANEL_SITE"; die "Nginx rejected the configuration"; }
    fi
    run systemctl enable nginx
    if systemctl is-active --quiet nginx 2>/dev/null; then run systemctl reload nginx; else run systemctl start nginx; fi
    # Switch panel into production mode (Secure cookies + HSTS) and trust Nginx proxy.
    sed -i 's/^WHACP_PRODUCTION=0/WHACP_PRODUCTION=1/' "$CONF_DIR/server.env"
    # Terminal websocket config.
    run sed -i "s|\"terminal_listen\": \"\"|\"terminal_listen\": \"127.0.0.1:${TERMINAL_PORT}\"|" "$CONF_DIR/agent.json"
    run systemctl restart whacp-agent.service
    run systemctl restart whacp-panel.service
    ok "web terminal on 127.0.0.1:${TERMINAL_PORT} (behind Nginx)"
    if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
        run ufw allow "${PUBLIC_HTTPS}/tcp"; ok "UFW allows ${PUBLIC_HTTPS}/tcp"
    elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
        run firewall-cmd --permanent --add-port="${PUBLIC_HTTPS}/tcp"; run firewall-cmd --reload
        ok "firewalld allows ${PUBLIC_HTTPS}/tcp"
    else
        ok "no active firewall here (ensure ${PUBLIC_HTTPS}/tcp is allowed upstream)"
    fi
fi

# ------------------------------------------------------ 9. health checks ----
log "Running health checks"
if [[ $DRY_RUN -eq 0 ]]; then
    for _ in $(seq 1 10); do [[ -S /run/whacp/agent.sock ]] && break; sleep 1; done
    systemctl is-active --quiet whacp-agent && ok "whacp-agent is running" || warn "whacp-agent is not running: journalctl -u whacp-agent"
fi
if [[ $DRY_RUN -eq 0 && $AGENT_ONLY -eq 0 ]]; then
    for _ in $(seq 1 10); do curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 && break; sleep 1; done
    curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null && ok "panel answers on 127.0.0.1:${PORT}" \
        || warn "panel is not answering: journalctl -u whacp-panel"
    if [[ -n $PUBLIC_HTTPS ]]; then
        for _ in $(seq 1 10); do curl -fsSk "https://127.0.0.1:${PUBLIC_HTTPS}/healthz" >/dev/null 2>&1 && break; sleep 1; done
        curl -fsSk "https://127.0.0.1:${PUBLIC_HTTPS}/healthz" >/dev/null && ok "panel answers over HTTPS on port ${PUBLIC_HTTPS}" \
            || warn "panel does not answer on https://127.0.0.1:${PUBLIC_HTTPS}/"
    fi
fi

# --------------------------------------------------- 10. summary ----
if [[ $AGENT_ONLY -eq 1 ]]; then
    cat <<EOF

The WhaCP agent is installed ($([[ $ENABLE_WRITES -eq 1 ]] && echo "write mode" || echo "read-only discovery mode")).
EOF
    if [[ -n $REMOTE_ACCESS ]]; then
        cat <<EOF
Connect it from your WhaCP panel under "All servers" with:
    Agent address:   https://$(hostname -I 2>/dev/null | awk '{print $1}'):${REMOTE_ACCESS##*:}
    Certificate pin: ${AGENT_PIN:-run: whacp-agent -pin}
    Agent token:     $(cat "$CONF_DIR/agent.token" 2>/dev/null)
EOF
    fi
    exit 0
fi

cat <<EOF

WhaCP is installed ($([[ $ENABLE_WRITES -eq 1 ]] && echo "write mode" || echo "read-only discovery mode")).
EOF
if [[ $PROFILE == none ]]; then
    cat <<EOF
Nothing in your existing Nginx, PHP, database, SSL or cron configuration was
changed. WhaCP only ever edits files it creates itself (named whacp-*).

EOF
fi
if [[ -n $PUBLIC_HTTPS ]]; then
    cat <<EOF
Open the panel in your browser:
    https://$(hostname -I 2>/dev/null | awk '{print $1}'):${PUBLIC_HTTPS}/

The certificate is self-signed: the browser warns once. Turn on two-factor
authentication in your profile.
EOF
else
    cat <<EOF
Open the panel through an SSH tunnel from your workstation:
    ssh -L ${PORT}:127.0.0.1:${PORT} $(hostname -f 2>/dev/null || hostname)
    then browse to http://127.0.0.1:${PORT}/

Or serve it at https://SERVER-IP:8443/: install.sh --reinstall --public-https
EOF
fi
if [[ -n ${CREATED_PASS:-} ]]; then
    printf 'Administrator: %s\nPassword:      %s\n\nStore this password now; it is not saved anywhere and will not be shown again.\n' "$ADMIN_USER" "$CREATED_PASS"
fi

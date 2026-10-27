#!/usr/bin/env bash
#
# NimVor installer (development / early-adopter release).
#
# What it does:
#   1. Detects the OS and checks requirements.
#   2. Detects conflicts (existing NimVor install, port in use).
#   3. Installs missing dependencies from the distribution's repositories
#      (--no-install-deps: only report them).
#   4. Creates the "whacp" system user and group.
#   5. Installs the agent binary and the panel files.
#   6. Writes /etc/whacp configuration and a fresh agent token.
#   7. Installs and starts the systemd services.
#   8. Creates the first administrator.
#   9. Runs health checks.
#
# Server profile (fresh servers): after installing, NimVor sets the server up
# for hosting with one of these profiles (bin/whacp setup:apply):
#   full     web server, PHP 8.3 + 8.4, MariaDB, email (Postfix, Dovecot,
#            OpenDKIM, Rspamd), DNS (BIND, authoritative only), FTP, Redis,
#            Memcached, ClamAV, firewall + protections, fail2ban, hardening,
#            automatic security updates, hosting plans, weekly backups
#   web      the same without email, DNS and ClamAV
#   minimal  web server, PHP, MariaDB, firewall, fail2ban, certificates,
#            plans and backups
#   none     nothing: NimVor only observes until you enable write mode
# Default: full with 4 GB of memory or more, web with 2 GB, minimal below,
# but only on a fresh server (no web, mail, DNS or database service running
# yet) and never with --reinstall or --agent-only; otherwise none. You are
# asked before anything is set up (--yes answers yes). A profile needs write
# mode, which it turns on. Run it later at any time:
#   sudo -u whacp WHACP_CONFIG=/etc/whacp/panel.php php /opt/whacp/panel/bin/whacp setup:apply --profile=full
# PHP 8.4 (and 8.3 on older systems) comes from a third-party repository
# where the system has none: Ondřej Surý's PPA on Ubuntu (pinned signing
# key), Remi's repository on the RHEL family. --no-third-party-repos keeps
# to the system's repositories (fewer PHP versions).
#
# What it never does:
#   - modify Nginx, PHP-FPM, MySQL/MariaDB, Redis, firewall or SSH configuration
#     (except with --public-https, which adds its own Nginx site and opens its
#     port, and with a server profile, through the panel's own actions);
#   - reload or restart any existing service (--public-https reloads Nginx);
#   - overwrite an existing NimVor installation (use --reinstall explicitly).
#
# Usage (from a source checkout or release archive):
#   sudo ./installer/install.sh [--no-install-deps] [--admin-user=NAME] [--admin-email=EMAIL]
#                               [--port=8090] [--dry-run] [--yes] [--reinstall|--resume] [--force-unsupported]
#                               [--enable-writes] [--public-https[=8443]]
#                               [--profile=full|web|minimal|none] [--hostname=server.example.com]
#                               [--php=8.3,8.4] [--no-third-party-repos]
#                               [--agent-only --remote-access=0.0.0.0:8443 --allow-from=PANEL_IP[,...]]
#
# --public-https serves the panel at https://SERVER-IP:8443/ (or the port
# given) through Nginx with a self-signed certificate, opens that port in
# UFW or firewalld, and marks the panel's cookies HTTPS-only. The panel
# itself keeps listening on 127.0.0.1 only. On an installed server run
# install.sh --reinstall --public-https.
#
# --enable-writes switches the agent from read-only discovery to write mode
# (manage websites, databases, certificates, …). Without it NimVor only observes.
#
# --agent-only installs just the agent on a server that another NimVor panel
# will manage; --remote-access exposes it over TLS (pinned self-signed
# certificate) to the addresses given with --allow-from. The installer
# prints the address, pin and token to enter under "All servers" in the panel.
#
# A reinstall (--reinstall) keeps the configuration, token, database, write
# mode, the panel's port and its public HTTPS port: no options are needed
# again. To update a source checkout in one step: sudo ./installer/update.sh

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
# Supported: tested and recommended for production. Preview: installs and
# is tested the same way, but not yet recommended for production.
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
    # Ask on the terminal: standard input may be a pasted block of commands
    # or already used up by an earlier command, which answered "no" silently.
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
        --install-deps) INSTALL_DEPS=1 ;;   # the default; kept for older instructions
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
        -h|--help) sed -n '2,70p' "$0"; exit 0 ;;
        *) die "unknown option: $arg" ;;
    esac
done

[[ $PORT =~ ^[0-9]+$ ]] && (( PORT >= 1024 && PORT <= 65535 )) || die "--port must be between 1024 and 65535"
if [[ -n $PUBLIC_HTTPS ]]; then
    [[ $PUBLIC_HTTPS =~ ^[0-9]+$ ]] && (( PUBLIC_HTTPS >= 1024 && PUBLIC_HTTPS <= 65535 )) || die "--public-https takes a port between 1024 and 65535 (80 and 443 stay with your websites)"
    (( PUBLIC_HTTPS != PORT )) || die "--public-https needs a port other than the panel's own (--port=$PORT)"
    [[ $AGENT_ONLY -eq 0 ]] || die "--public-https serves the panel; --agent-only installs no panel"
fi
[[ -z $ADMIN_USER || $ADMIN_USER =~ ^[a-z][a-z0-9._-]{2,31}$ ]] || die "--admin-user is not a valid username"
[[ -z $PROFILE || $PROFILE =~ ^(full|web|minimal|none)$ ]] || die "--profile must be full, web, minimal or none"
[[ $PROFILE_SET -eq 0 || $PROFILE == none || $AGENT_ONLY -eq 0 ]] || die "--profile sets up a hosting server; --agent-only installs no panel"
[[ -z $SETUP_HOSTNAME || $SETUP_HOSTNAME =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$ ]] || die "--hostname must be the server's full host name, e.g. server.example.com"
[[ -z $SETUP_PHP || $SETUP_PHP =~ ^[5-9]\.[0-9]{1,2}(,[5-9]\.[0-9]{1,2})*$ ]] || die "--php takes versions separated by commas, e.g. 8.2,8.3,8.4"
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
preview=0
PKG=apt
MAJOR=${VERSION_ID%%.*}
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
    ok "${PRETTY_NAME}"
    warn "${PRETTY_NAME} is a preview system for NimVor: it installs and is tested, but is not yet recommended for production servers"
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
ok "family $([[ $PKG == dnf ]] && echo rhel || echo debian), $ARCH, package manager $PKG, init $(ps -p 1 -o comm= 2>/dev/null || echo unknown), SELinux ${SELINUX_MODE,,}, firewall $FIREWALL"
[[ $ARCH == x86_64 || $ARCH == aarch64 ]] || warn "architecture $ARCH is not tested"
# APT waits for another package manager (e.g. unattended-upgrades) instead
# of failing at once.
APT_WAIT=(-o DPkg::Lock::Timeout=600)

# ---------------------------------------------------- 1b. server profile ----
log "Server profile"
MEM_MB=$(awk '/^MemTotal:/ {print int($2/1024)}' /proc/meminfo 2>/dev/null || true)
MEM_MB=${MEM_MB:-0}
DISK_FREE_GB=$(df -Pm / 2>/dev/null | awk 'NR==2 {print int($4/1024)}' || true)
ok "memory ${MEM_MB} MB, free disk ${DISK_FREE_GB:-?} GB on /"
if [[ -z $PROFILE ]]; then
    busy=()
    # A server that already serves websites, mail, DNS or databases was set
    # up by hand: NimVor does not change it unless asked (--profile=…).
    for u in nginx apache2 httpd postfix exim4 named bind9 mariadb mysql mysqld; do
        if systemctl is-active --quiet "$u" 2>/dev/null; then busy+=("$u"); fi
    done
    if [[ $REINSTALL -eq 1 || $AGENT_ONLY -eq 1 || -d $PREFIX/panel ]]; then
        PROFILE=none
    elif (( ${#busy[@]} > 0 )); then
        PROFILE=none
        warn "running already: ${busy[*]}. No server profile is applied (your configuration is left alone); choose one with --profile=full|web|minimal"
    elif (( MEM_MB >= 3800 )); then PROFILE=full
    elif (( MEM_MB >= 1800 )); then PROFILE=web
    else PROFILE=minimal
    fi
fi
if [[ $PROFILE != none ]]; then
    case $PROFILE in
        full) desc="full hosting server: web, PHP, MariaDB, email, DNS (authoritative), FTP, Redis, Memcached, ClamAV, firewall, fail2ban" ;;
        web) desc="web hosting only: web, PHP, MariaDB, FTP, Redis, Memcached, firewall, fail2ban" ;;
        *) desc="minimal: web, PHP, MariaDB, firewall, fail2ban" ;;
    esac
    ok "profile $PROFILE ($desc)"
    [[ $PROFILE != full ]] || (( MEM_MB >= 3800 )) || warn "the full profile wants 4 GB of memory (ClamAV alone uses about 1.5 GB); you have ${MEM_MB} MB"
    (( ${DISK_FREE_GB:-0} >= 20 )) || warn "only ${DISK_FREE_GB:-?} GB free on /: websites, mail and weekly backups need room (20 GB or more recommended)"
    if [[ $THIRD_PARTY_REPOS -eq 1 ]]; then
        ok "PHP ${SETUP_PHP:-8.3,8.4}: where the system offers fewer versions, $([[ $PKG == dnf ]] && echo "Remi's repository" || echo "Ondřej Surý's PPA (pinned signing key)") is added — a third-party repository; --no-third-party-repos keeps to the system's"
    else
        ok "PHP ${SETUP_PHP:-8.3,8.4} from the system's repositories only (--no-third-party-repos): versions it lacks are skipped"
    fi
    if [[ $PROFILE == full ]]; then
        # Email needs outgoing port 25 and a reverse DNS (PTR) name that
        # matches the host name; both are set by the hosting provider.
        if timeout 6 bash -c 'exec 3<>/dev/tcp/gmail-smtp-in.l.google.com/25' 2>/dev/null; then
            ok "outgoing port 25 is open"
        else
            warn "outgoing port 25 seems blocked: this server cannot deliver email to other servers. Ask your provider to open it, or relay through a smarthost"
        fi
        pub_ip=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i=1;i<NF;i++) if ($i=="src") print $(i+1)}' || true)
        fqdn=${SETUP_HOSTNAME:-$(hostname -f 2>/dev/null || hostname)}
        ptr=""
        if [[ -n $pub_ip ]]; then
            if command -v dig >/dev/null; then ptr=$(dig +short +time=3 +tries=1 -x "$pub_ip" 2>/dev/null | head -1 || true); ptr=${ptr%.}
            else ptr=$(getent hosts "$pub_ip" 2>/dev/null | awk '{print $2}' | head -1 || true); fi
        fi
        if [[ -z $pub_ip ]]; then
            warn "the server's address could not be determined: check its reverse DNS (PTR) name for email"
        elif [[ $pub_ip =~ ^(10\.|192\.168\.|172\.(1[6-9]|2[0-9]|3[01])\.) ]]; then
            warn "$pub_ip is a private address (behind NAT): set the reverse DNS (PTR) name of the public address to $fqdn for email"
        elif [[ -z $ptr ]]; then
            warn "$pub_ip has no reverse DNS (PTR) name: other mail servers may reject this server's email. Ask your provider to set it to $fqdn"
        elif [[ ${ptr,,} != "${fqdn,,}" ]]; then
            warn "reverse DNS of $pub_ip is $ptr, not the host name $fqdn: set one to match the other for email delivery"
        else
            ok "reverse DNS of $pub_ip is $ptr"
        fi
        [[ $fqdn == *.* ]] || warn "the host name ($fqdn) is not a full name: email is set up only with --hostname=server.example.com"
    fi
    if [[ $DRY_RUN -eq 0 && $ASSUME_YES -eq 0 ]] && ! { exec 3</dev/tty; } 2>/dev/null; then
        # No terminal to ask on (a script): no profile unless --yes.
        PROFILE=none
        warn "no terminal to ask whether to set up the server: no profile applied (--yes or --profile=… with --yes to apply one)"
    elif [[ $DRY_RUN -eq 0 ]]; then
        exec 3<&- 2>/dev/null || true
        if confirm "Set up this server as: $desc? (Turns on write mode, the firewall and the services above; existing settings are left alone. No: install NimVor only.)"; then
            ENABLE_WRITES=1
        else
            PROFILE=none
            ok "no profile: set the server up later with bin/whacp setup:apply --profile=…"
        fi
    fi
else
    ok "no server profile"
fi

# ------------------------------------------------------- 2. requirements ----
log "Checking requirements"
missing_pkgs=()
# PHP_BIN: the PHP (>= 8.3) the panel, its scheduler and this installer
# run, which need not be the system's default "php" (Ubuntu 22.04).
find_php() {
    local c
    for c in "$(command -v php 2>/dev/null || true)" /usr/bin/php8.5 /usr/bin/php8.4 /usr/bin/php8.3 /opt/remi/php83/root/usr/bin/php; do
        [[ -n $c && -x $c ]] || continue
        "$c" -r 'exit(PHP_VERSION_ID >= 80300 ? 0 : 1);' 2>/dev/null && { printf '%s\n' "$c"; return 0; }
    done
    return 1
}
# The system's "php" as found: installing PHP 8.3 (or Composer, which
# pulls in the distribution's PHP) may switch it; keep_system_php puts it
# back, so websites' cron jobs and scripts calling "php" keep their version.
php_before=""
[[ -e /usr/bin/php ]] && php_before=$(readlink -f /usr/bin/php)
keep_system_php() {
    [[ -n $php_before && $DRY_RUN -eq 0 && -e /usr/bin/php ]] || return 0
    if [[ $(readlink -f /usr/bin/php) != "$php_before" ]] && command -v update-alternatives >/dev/null; then
        update-alternatives --set php "$php_before" >/dev/null 2>&1 && ok "the system's php stays $php_before"
    fi
}
php_ok=0
if [[ $AGENT_ONLY -eq 1 ]]; then
    php_ok=1 # the panel is not installed on this server
elif PHP_BIN=$(find_php); then
    php_ok=1
    ok "PHP $("$PHP_BIN" -r 'echo PHP_VERSION;') ($PHP_BIN)"
    # extension -> package (Ubuntu: sodium and openssl ship in php-common);
    # a versioned binary (php8.3) takes that version's packages.
    pv=""
    [[ $PHP_BIN =~ php([0-9]\.[0-9])$ ]] && pv=${BASH_REMATCH[1]}
    if [[ $PKG == dnf ]]; then
        declare -A ext_pkg=([pdo_sqlite]=php-pdo [curl]=php-common [mbstring]=php-mbstring [sodium]=php-sodium [openssl]=php-common)
    else
        declare -A ext_pkg=([pdo_sqlite]=php${pv}-sqlite3 [curl]=php${pv}-curl [mbstring]=php${pv}-mbstring [sodium]=php${pv}-common [openssl]=php${pv}-common)
    fi
    # Read the module list once: with pipefail, "php -m | grep -q" fails
    # when grep exits early and php gets SIGPIPE (seen on AlmaLinux 10:
    # an installed extension reported missing).
    php_modules=$("$PHP_BIN" -m)
    for ext in pdo_sqlite curl mbstring sodium openssl; do
        if grep -qix "${ext}" <<<"$php_modules"; then ok "php extension ${ext}"; else missing_pkgs+=("${ext_pkg[$ext]}"); fi
    done
elif command -v php >/dev/null; then
    warn "PHP $(php -r 'echo PHP_VERSION;') is older than 8.3: the panel gets PHP 8.3 of its own"
fi
# Ubuntu releases whose own PHP is older than 8.3 (22.04): the panel's PHP
# 8.3 comes from the ondrej/php PPA (signed by its Launchpad key). Only the
# panel uses it: the system's default "php" stays what it was.
PHP_PPA=0
if [[ $php_ok -eq 0 && ${ID:-} == ubuntu ]] && dpkg --compare-versions "${VERSION_ID:-0}" lt 24.04; then
    PHP_PPA=1
fi
if [[ $php_ok -eq 0 ]]; then
    if [[ $PKG == dnf ]]; then
        missing_pkgs+=(php-cli php-pdo php-mbstring php-sodium)
    elif [[ $PHP_PPA -eq 1 ]]; then
        missing_pkgs+=(php8.3-cli php8.3-sqlite3 php8.3-curl php8.3-mbstring)
    else
        missing_pkgs+=(php-cli php-sqlite3 php-curl php-mbstring)
    fi
fi
command -v openssl >/dev/null || missing_pkgs+=(openssl)
command -v curl >/dev/null || missing_pkgs+=(curl)
# Jailed shells for website accounts run in a bubblewrap sandbox.
command -v bwrap >/dev/null || missing_pkgs+=(bubblewrap)
# SELinux labels for what NimVor sets up (semanage).
if [[ $PKG == dnf ]] && ! command -v semanage >/dev/null; then missing_pkgs+=(policycoreutils-python-utils); fi
# --public-https: NimVor's own Nginx site for the panel, laid out like the
# webmail site (whacp-panel.conf is not a website: "panel" is no domain).
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
# A reinstall keeps what the first installation chose: the panel's port and
# its public HTTPS port (nothing to pass again).
if [[ $REINSTALL -eq 1 ]]; then
    if [[ $PORT_SET -eq 0 && -f /etc/systemd/system/whacp-panel.service ]]; then
        prev=$(grep -oE '127\.0\.0\.1:[0-9]+' /etc/systemd/system/whacp-panel.service | head -1 | cut -d: -f2)
        if [[ $prev =~ ^[0-9]+$ ]] && (( prev >= 1024 && prev <= 65535 )); then PORT=$prev; fi
    fi
    if [[ -z $PUBLIC_HTTPS && $AGENT_ONLY -eq 0 && -f $PANEL_SITE ]]; then
        prev=$(grep -oE 'listen[[:space:]]+(\[::\]:)?[0-9]+[[:space:]]+ssl' "$PANEL_SITE" | grep -oE '[0-9]+' | head -1)
        if [[ $prev =~ ^[0-9]+$ ]] && (( prev >= 1024 && prev <= 65535 && prev != PORT )); then
            PUBLIC_HTTPS=$prev
            ok "keeping the panel's public HTTPS port ${PUBLIC_HTTPS}"
        fi
    fi
fi
# nft: SMTP Restrictions and brute force protection blocks (the package's
# own nftables.service is left as it is).
command -v nft >/dev/null || [[ -x /usr/sbin/nft ]] || missing_pkgs+=(nftables)
if [[ -n $PUBLIC_HTTPS ]]; then
    command -v nginx >/dev/null || missing_pkgs+=(nginx)
    if [[ ! -e $PANEL_SITE ]] && command -v ss >/dev/null && [[ -n $(ss -ltnH "sport = :${PUBLIC_HTTPS}" 2>/dev/null) ]]; then
        die "port ${PUBLIC_HTTPS} is already in use; choose another with --public-https=PORT"
    fi
fi

if [[ ${#missing_pkgs[@]} -gt 0 ]]; then
    if [[ $INSTALL_DEPS -eq 1 && $PKG == dnf ]]; then
        # EPEL and Remi provide PHP versions for websites (and PHP 8.3 for
        # the panel on 9, whose own PHP is older).
        log "Enabling the EPEL and Remi repositories"
        run dnf install -y -q epel-release
        # EPEL's packages build on CRB (CodeReady Builder) on Rocky and Alma.
        if [[ ${ID:-} != rhel ]] && ! dnf -q repolist --enabled 2>/dev/null | grep -qiE '^(crb|powertools)\b'; then
            run dnf install -y -q dnf-plugins-core
            run dnf config-manager --set-enabled crb
        fi
        rpm -q remi-release >/dev/null 2>&1 || run dnf install -y -q "https://rpms.remirepo.net/enterprise/remi-release-${MAJOR}.rpm"
        if [[ $MAJOR == 9 && $php_ok -eq 0 ]]; then
            run dnf module reset -y -q php
            run dnf module enable -y -q php:remi-8.3
        fi
        log "Installing dependencies: ${missing_pkgs[*]}"
        run dnf install -y -q "${missing_pkgs[@]}"
    elif [[ $INSTALL_DEPS -eq 1 ]]; then
        if [[ $PHP_PPA -eq 1 ]] && ! grep -rqs 'ondrej/php' /etc/apt/sources.list /etc/apt/sources.list.d/; then
            log "Adding the ondrej/php repository (PHP 8.3 for the panel; Ubuntu ${VERSION_ID} ships PHP 8.1)"
            run apt-get "${APT_WAIT[@]}" update -q
            run env DEBIAN_FRONTEND=noninteractive apt-get "${APT_WAIT[@]}" install -y -q --no-install-recommends software-properties-common gnupg ca-certificates
            run env LC_ALL=C.UTF-8 add-apt-repository -y ppa:ondrej/php
        fi
        log "Installing dependencies: ${missing_pkgs[*]}"
        run apt-get "${APT_WAIT[@]}" update -q
        run env DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l apt-get "${APT_WAIT[@]}" install -y -q --no-install-recommends "${missing_pkgs[@]}"
        keep_system_php
    else
        die "missing packages: ${missing_pkgs[*]}. Install them ($PKG install ${missing_pkgs[*]}) or re-run without --no-install-deps."
    fi
fi
# Where AppArmor keeps unprivileged processes from creating user
# namespaces (Ubuntu 23.10 and later), jailed shells run from a copy of
# bubblewrap that only members of whacp-jail can execute (the agent keeps
# it up to date); this profile lets that copy, and nothing else, create
# them. Other accounts and programs keep the restriction.
if [[ -r /proc/sys/kernel/apparmor_restrict_unprivileged_userns && $(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns) == 1 ]] && command -v apparmor_parser >/dev/null; then
    cat > /etc/apparmor.d/whacp-bwrap <<'PROFILE'
# Managed by NimVor: jailed shells of website accounts (see the installer).
abi <abi/4.0>,
include <tunables/global>

profile whacp-bwrap /usr/local/lib/whacp/bwrap flags=(unconfined) {
  userns,

  include if exists <local/whacp-bwrap>
}
PROFILE
    if run apparmor_parser -r /etc/apparmor.d/whacp-bwrap; then
        log "AppArmor profile for jailed shells loaded"
    else
        warn "The AppArmor profile for jailed shells could not be loaded; jailed shells stay unavailable"
    fi
fi
if [[ $AGENT_ONLY -eq 1 ]]; then
    PHP_BIN=/usr/bin/php
else
    if PHP_BIN=$(find_php); then
        ok "panel PHP: $PHP_BIN ($("$PHP_BIN" -r 'echo PHP_VERSION;'))"
    elif [[ $DRY_RUN -eq 1 ]]; then
        PHP_BIN=/usr/bin/php8.3
    else
        die "PHP >= 8.3 was not found after installing dependencies. Install PHP 8.3 (CLI with pdo_sqlite, curl, mbstring), or re-run without --no-install-deps."
    fi
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
        users=$(runuser -u whacp -- env WHACP_CONFIG="$CONF_DIR/panel.php" "$PHP_BIN" "$PREFIX/panel/bin/whacp" users:count 2>/dev/null || true)
        [[ $users =~ ^[1-9][0-9]*$ ]] || echo "the first administrator (panel database)"
    fi
}
if [[ -e $PREFIX || -e $CONF_DIR ]]; then
    mapfile -t missing < <(install_missing)
    if [[ $REINSTALL -eq 1 ]]; then
        confirm "NimVor is already installed. Reinstall files (configuration, token and database are kept)?" || die "aborted"
    elif [[ ${#missing[@]} -gt 0 ]]; then
        die "an earlier NimVor installation did not finish (missing: $(IFS=,; echo "${missing[*]}" | sed 's/,/, /g')).
Complete it by running the installer again with --resume (or --reinstall) and the same options, e.g.:
    sudo $0 --resume $*
Existing configuration, agent token, panel key and database are kept; missing parts are created."
    else
        die "NimVor is already installed in $PREFIX. To upgrade, run installer/upgrade.sh (it backs up, migrates and rolls back on failure). To repair this version's files, run the installer with --reinstall."
    fi
fi
if [[ $AGENT_ONLY -eq 0 ]] && command -v ss >/dev/null && grep -q LISTEN <<<"$(ss -ltn "sport = :$PORT")"; then
    if ! systemctl is-active --quiet whacp-panel 2>/dev/null; then
        die "port $PORT is already in use; choose another with --port="
    fi
fi
ok "no conflicts"

# find_tool NAME CANDIDATES...: the first executable; sudo's PATH often
# lacks /usr/local/go/bin or the folder Composer was installed to.
find_tool() {
    local name=$1 c; shift
    for c in "$(command -v "$name" 2>/dev/null || true)" "$@"; do
        [[ -n $c && -x $c && ! -d $c ]] && { printf '%s\n' "$c"; return 0; }
    done
    return 1
}
find_go() {
    # Newest distribution Go (golang-1.xx) last, so an explicit install wins.
    # shellcheck disable=SC2046
    find_tool go /usr/local/go/bin/go /usr/lib/golang/bin/go /snap/bin/go $(ls -d /usr/lib/go-*/bin/go 2>/dev/null | sort -V -r) /usr/lib/go/bin/go
}
find_composer() {
    local home=""
    [[ -n ${SUDO_USER:-} ]] && home=$(getent passwd "$SUDO_USER" | cut -d: -f6)
    find_tool composer /usr/local/bin/composer /usr/bin/composer /usr/local/bin/composer.phar /opt/composer/composer ${home:+"$home/.local/bin/composer"} ${home:+"$home/bin/composer"}
}

# --------------------------------------------------------- 4. build agent ----
log "Preparing the agent binary"
src_owner=$(stat -c %U "$SRC_DIR" 2>/dev/null || echo root)
# What the installer builds in a user's checkout (agent, panel/vendor)
# stays that user's: root-owned files there break their next git pull or
# build.
give_back() {
    [[ $src_owner != root && $DRY_RUN -eq 0 ]] || return 0
    chown -R "$src_owner": "$@" 2>/dev/null || true
}
if [[ -d $SRC_DIR/.git ]]; then
    if [[ -n $(find "$SRC_DIR/.git" ! -user "$src_owner" -print -quit 2>/dev/null) ]]; then
        # Typically after "sudo git pull": the owner's next git pull fails
        # ("index file open failed: Permission denied") and this runs the
        # old sources again.
        if [[ $src_owner != root ]]; then
            # The checkout is the owner's: give it back, so git pull works again.
            run chown -R "$src_owner": "$SRC_DIR/.git"
            ok "returned $SRC_DIR/.git to $src_owner (files owned by root, e.g. after sudo git pull, block git pull)"
        fi
    fi
    ok "sources: commit $(git --no-optional-locks -C "$SRC_DIR" -c safe.directory="$SRC_DIR" log -1 --format='%h %cs %s' 2>/dev/null | cut -c1-80)"
fi
AGENT_SRC=""
# A build older than the agent's sources (e.g. after git pull) is stale:
# reinstalling would put the old agent back. Release archives ship the
# build made after their sources, so they are not rebuilt.
stale=0
if [[ -x $SRC_DIR/dist/whacp-agent && -d $SRC_DIR/agent ]] && [[ -n $(find "$SRC_DIR/agent" "$SRC_DIR/VERSION" \( -name '*.go' -o -name go.mod -o -name go.sum -o -name VERSION \) -newer "$SRC_DIR/dist/whacp-agent" -print -quit 2>/dev/null) ]]; then
    stale=1
fi
# The agent needs the Go its go.mod names. Go 1.21 and later fetch a newer
# toolchain themselves (GOTOOLCHAIN=auto, checked against Go's checksum
# database); older ones (Ubuntu 22.04 ships 1.18) cannot, so NimVor then
# downloads Go from go.dev into its own folder (the system's Go is not
# touched) and checks the archive's SHA-256 before unpacking it.
GO_MIN=$(awk '$1 == "go" { print $2; exit }' "$SRC_DIR/agent/go.mod" 2>/dev/null)
GO_MIN=${GO_MIN:-1.24}
GO_FETCH_VERSION=1.24.7
GO_DIR=/usr/local/lib/whacp-go
ver_ge() { [[ $(printf '%s\n%s\n' "$2" "$1" | sort -V | head -1) == "$2" ]]; }
go_version() { local v; v=$("$1" env GOVERSION 2>/dev/null || "$1" version 2>/dev/null | awk '{print $3}'); printf '%s\n' "${v#go}"; }
# Builds the agent: as new as go.mod, or able to fetch that toolchain.
go_usable() { local v; v=$(go_version "$1"); [[ -n $v ]] && { ver_ge "$v" "$GO_MIN" || ver_ge "$v" 1.21; }; }
fetch_go() {
    local arch f tmp sum
    case $ARCH in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; *) return 1 ;; esac
    f="go${GO_FETCH_VERSION}.linux-${arch}.tar.gz"
    log "Downloading Go ${GO_FETCH_VERSION} from go.dev to build the agent (into $GO_DIR)"
    if [[ $DRY_RUN -eq 1 ]]; then echo "    [dry-run] https://dl.google.com/go/$f (SHA-256 checked) -> $GO_DIR"; return 0; fi
    tmp=$(mktemp -d)
    if ! curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "$tmp/$f" "https://dl.google.com/go/$f" \
        || ! sum=$(curl -fsSL --proto '=https' --tlsv1.2 --retry 3 "https://dl.google.com/go/$f.sha256"); then
        rm -rf "$tmp"; warn "Go could not be downloaded from go.dev"; return 1
    fi
    sum=$(tr -d '[:space:]' <<<"$sum")
    if [[ ! $sum =~ ^[0-9a-f]{64}$ ]] || ! printf '%s  %s\n' "$sum" "$tmp/$f" | sha256sum -c --quiet; then
        rm -rf "$tmp"; warn "the downloaded Go archive failed its SHA-256 check; it was not used"; return 1
    fi
    rm -rf "$GO_DIR.new" && mkdir -p "$GO_DIR.new" && tar -C "$GO_DIR.new" -xzf "$tmp/$f" || { rm -rf "$tmp" "$GO_DIR.new"; return 1; }
    rm -rf "$GO_DIR" && mv "$GO_DIR.new" "$GO_DIR"
    rm -rf "$tmp"
    ok "Go ${GO_FETCH_VERSION} in $GO_DIR"
}
GO=$(find_go || true)
if [[ -x $GO_DIR/go/bin/go ]] && { [[ -z $GO ]] || ! go_usable "$GO"; } && go_usable "$GO_DIR/go/bin/go"; then
    GO=$GO_DIR/go/bin/go
fi
if [[ ( ! -x $SRC_DIR/dist/whacp-agent || $stale -eq 1 ) && $INSTALL_DEPS -eq 1 ]] && { [[ -z $GO ]] || ! go_usable "$GO"; }; then
    [[ -n $GO ]] && warn "Go $(go_version "$GO") ($GO) is too old to build the agent (it needs ${GO_MIN})"
    if [[ -z $GO && $PKG == dnf ]]; then
        log "Installing Go to build the agent"
        run dnf install -y -q golang
        GO=$(find_go || true)
    fi
    if [[ -z $GO ]] || ! go_usable "$GO"; then
        fetch_go && GO=$GO_DIR/go/bin/go
    fi
fi
if [[ -n $GO && $DRY_RUN -eq 0 ]] && ! go_usable "$GO"; then
    die "Go $(go_version "$GO") ($GO) is too old to build the agent (it needs ${GO_MIN}). Re-run without --no-install-deps to download a suitable Go, or install Go ${GO_MIN} or later."
fi
if [[ -n $GO ]]; then
    ok "Go detected: $("$GO" version 2>/dev/null | awk '{print $3}') ($GO)"
fi
if [[ $stale -eq 1 && -z $GO ]]; then
    # Installing the older build would silently keep the old agent (and its
    # fixed bugs) while the panel is updated.
    die "the agent's sources are newer than dist/whacp-agent (e.g. after git pull), but Go was not found to rebuild it (looked in PATH, /usr/local/go/bin, /usr/lib/golang/bin, /usr/lib/go-*/bin). Re-run without --no-install-deps to install Go from the distribution, or install Go yourself."
fi
if [[ -x $SRC_DIR/dist/whacp-agent && $stale -eq 0 ]]; then
    AGENT_SRC=$SRC_DIR/dist/whacp-agent
elif [[ -n $GO ]]; then
    if [[ $stale -eq 1 ]]; then log "Rebuilding the agent: its sources are newer than dist/whacp-agent"; else log "Building agent from source"; fi
    run mkdir -p "$SRC_DIR/dist"
    # The version names the commit it was built from (agent:check, health).
    # Read-only git: as root it must not rewrite .git/index (git pull by the
    # checkout's owner would then fail).
    AGENT_VERSION=$(cat "$SRC_DIR/VERSION")
    if commit=$(git -C "$SRC_DIR" -c safe.directory="$SRC_DIR" rev-parse --short HEAD 2>/dev/null); then
        AGENT_VERSION+="+$commit"
        [[ -n $(git --no-optional-locks -C "$SRC_DIR" -c safe.directory="$SRC_DIR" status --porcelain -- agent 2>/dev/null) ]] && AGENT_VERSION+=".dirty"
    fi
    # A Go older than go.mod asks for downloads the right toolchain itself
    # (GOTOOLCHAIN=auto); GOPATH and caches stay out of the source tree.
    (cd "$SRC_DIR/agent" && run env CGO_ENABLED=0 HOME="${HOME:-/root}" GOTOOLCHAIN=auto "$GO" build -trimpath -ldflags "-s -w -X main.version=$AGENT_VERSION" -o "$SRC_DIR/dist/whacp-agent" ./cmd/whacp-agent)
    AGENT_SRC=$SRC_DIR/dist/whacp-agent
else
    die "no prebuilt agent (dist/whacp-agent) and Go was not found (looked in PATH, /usr/local/go/bin, /usr/lib/golang/bin, /usr/lib/go-*/bin). Install Go, or re-run without --no-install-deps."
fi
give_back "$SRC_DIR/dist"
ok "$AGENT_SRC ($("$AGENT_SRC" -version 2>/dev/null || echo 'version unknown'))"

# panel/vendor older than composer.lock (after git pull) is refreshed.
vendor_stale=0
if [[ -f $SRC_DIR/panel/vendor/autoload.php && -f $SRC_DIR/panel/composer.lock && $SRC_DIR/panel/composer.lock -nt $SRC_DIR/panel/vendor/autoload.php ]]; then
    vendor_stale=1
fi
if [[ $AGENT_ONLY -eq 0 && ( ! -f $SRC_DIR/panel/vendor/autoload.php || $vendor_stale -eq 1 ) ]]; then
    COMPOSER=$(find_composer || true)
    if [[ -z $COMPOSER && $INSTALL_DEPS -eq 1 ]]; then
        log "Installing Composer"
        if [[ $PKG == dnf ]]; then run dnf install -y -q composer; else run env DEBIAN_FRONTEND=noninteractive apt-get "${APT_WAIT[@]}" install -y -q --no-install-recommends composer; fi
        keep_system_php
        COMPOSER=$(find_composer || true)
    fi
    [[ -n $COMPOSER ]] || die "panel/vendor is missing and Composer was not found (looked in PATH, /usr/local/bin, /usr/bin and your ~/.local/bin). Install it, or re-run without --no-install-deps."
    # Run by the panel's PHP: the distribution's Composer would use the
    # system's php (8.1 on Ubuntu 22.04) and refuse the panel's requirements.
    # (A PHP script or phar; a shell wrapper runs as it is.)
    COMPOSER_CMD=("$COMPOSER")
    if [[ $COMPOSER == *.phar ]] || head -n1 "$COMPOSER" 2>/dev/null | grep -q 'php'; then COMPOSER_CMD=("$PHP_BIN" "$COMPOSER"); fi
    # COMPOSER_ALLOW_SUPERUSER and no stdin: as root Composer otherwise
    # asks "Continue as root/super user?" and waits, invisibly here.
    ok "Composer detected: $(COMPOSER_ALLOW_SUPERUSER=1 "${COMPOSER_CMD[@]}" --version --no-interaction </dev/null 2>/dev/null | head -1) ($COMPOSER)"
    log "Installing panel PHP dependencies"
    (cd "$SRC_DIR/panel" && run env COMPOSER_ALLOW_SUPERUSER=1 "${COMPOSER_CMD[@]}" install --no-dev --no-interaction --optimize-autoloader -q </dev/null)
    give_back "$SRC_DIR/panel/vendor"
fi

# ----------------------------------------------------- 5. user and files ----
log "Creating system user and directories"
if ! getent group whacp >/dev/null; then run groupadd --system whacp; fi
if ! id whacp >/dev/null 2>&1; then
    run useradd --system --gid whacp --home-dir "$DATA_DIR" --no-create-home --shell /usr/sbin/nologin --comment "NimVor panel" whacp
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
# Where this installation came from: the panel's update button runs
# installer/update.sh from here (only when root owns the checkout).
if [[ $SRC_DIR != *[\"\\]* ]]; then
    if [[ $DRY_RUN -eq 1 ]]; then
        echo "    [dry-run] record $SRC_DIR in $CONF_DIR/source.json"
    else
        printf '{"path": "%s"}\n' "$SRC_DIR" > "$CONF_DIR/source.json"
        chmod 0644 "$CONF_DIR/source.json"
    fi
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
if [[ $DRY_RUN -eq 1 ]]; then
    echo "    [dry-run] /etc/systemd/system/whacp-scheduler.service (PHP $PHP_BIN)"
else
    sed "s#/usr/bin/php #${PHP_BIN} #" "$SRC_DIR/installer/systemd/whacp-scheduler.service" > /etc/systemd/system/whacp-scheduler.service
    chmod 0644 /etc/systemd/system/whacp-scheduler.service
fi
run install -m 0644 "$SRC_DIR/installer/systemd/whacp-scheduler.timer" /etc/systemd/system/whacp-scheduler.timer
if [[ $AGENT_ONLY -eq 1 ]]; then
    :
elif [[ $DRY_RUN -eq 1 ]]; then
    echo "    [dry-run] install whacp-panel.service (port $PORT)"
else
    sed -e "s/127.0.0.1:8090/127.0.0.1:${PORT}/" -e "s#/usr/bin/php #${PHP_BIN} #" "$SRC_DIR/installer/systemd/whacp-panel.service" > /etc/systemd/system/whacp-panel.service
    chmod 0644 /etc/systemd/system/whacp-panel.service
fi
if [[ $ENABLE_WRITES -eq 1 ]]; then
    log "Enabling write mode"
    cat <<'NOTE'
    In write mode NimVor creates websites, users, databases and certificates and
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
if [[ $PKG == dnf && $SELINUX_MODE != none && $SELINUX_MODE != Disabled ]] && command -v semodule >/dev/null; then
    # Let systemd create the sockets of NimVor's greylisting and mail limits
    # in Postfix's private folder (installer/selinux/*.cil explains it).
    # SELinux stays enforcing; this allows that one thing.
    log "Installing NimVor's SELinux policy module"
    run semodule -i "$SRC_DIR/installer/selinux/whacp_postfix_sockets.cil"
fi

run systemctl enable --now whacp-agent.service
if [[ $AGENT_ONLY -eq 0 ]]; then
    log "Initialising the panel database"
    run runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php "$PHP_BIN" "$PREFIX/panel/bin/whacp" migrate
    run systemctl enable --now whacp-panel.service
    run systemctl enable --now whacp-scheduler.timer
fi
if [[ $REINSTALL -eq 1 || $ENABLE_WRITES -eq 1 || -n $REMOTE_ACCESS ]]; then
    run systemctl restart whacp-agent.service
    [[ $AGENT_ONLY -eq 1 ]] || run systemctl restart whacp-panel.service
fi
# Greylisting and outgoing mail limits run the agent's program too: restart
# them (only where they run) so they use the version just installed.
# Mail helpers NimVor set up earlier run the new code (a fresh server has
# none yet: "try-restart" of a unit that does not exist fails).
for unit in whacp-greylist.service whacp-mailrate.service; do
    if [[ -f /etc/systemd/system/$unit ]]; then
        run systemctl try-restart "$unit" || warn "$unit could not be restarted (systemctl status $unit)"
    fi
done

# ---------------------------------------------------- 8. administrator ----
if [[ $DRY_RUN -eq 0 && $AGENT_ONLY -eq 0 ]]; then
    users=$(runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php "$PHP_BIN" "$PREFIX/panel/bin/whacp" users:count 2>/dev/null || echo 0)
    if [[ $users == 0 ]]; then
        log "Creating the first administrator"
        if [[ -z $ADMIN_USER ]]; then read -r -p "  Admin username [admin]: " ADMIN_USER; ADMIN_USER=${ADMIN_USER:-admin}; fi
        if [[ -z $ADMIN_EMAIL ]]; then read -r -p "  Admin email: " ADMIN_EMAIL; fi
        ADMIN_PASS="$(openssl rand -base64 18 | tr -d '/+=' | cut -c1-20)"
        printf '%s\n' "$ADMIN_PASS" | runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php "$PHP_BIN" "$PREFIX/panel/bin/whacp" user:create "$ADMIN_USER" "$ADMIN_EMAIL" --role=admin
        CREATED_PASS=$ADMIN_PASS
    else
        ok "administrator already exists"
    fi
fi

# ------------------------------------------------------- 8b. public HTTPS ----
# The panel stays on 127.0.0.1; Nginx serves it over HTTPS on the chosen
# port. The certificate is self-signed for this server's addresses and host
# name (replace it with a real one for a domain; see the installation guide).
if [[ -n $PUBLIC_HTTPS ]]; then
    log "Serving the panel over HTTPS on port ${PUBLIC_HTTPS}"
    # On a reinstall/update, keep the certificate the administrator chose for
    # the panel on Services SSL (it is written into this vhost); rewriting the
    # vhost with the self-signed default would silently revert that choice.
    # Only a fresh install (no vhost yet) falls back to the default below.
    if [[ -s $PANEL_SITE ]]; then
        cur_cert=$(awk '$1=="ssl_certificate"{print $2}' "$PANEL_SITE" | tr -d ';' | head -1)
        cur_key=$(awk '$1=="ssl_certificate_key"{print $2}' "$PANEL_SITE" | tr -d ';' | head -1)
        if [[ -n $cur_cert && -s $cur_cert && -n $cur_key && -s $cur_key ]]; then
            PANEL_CERT=$cur_cert
            PANEL_CERT_KEY=$cur_key
            ok "keeping the panel's chosen certificate ($PANEL_CERT)"
        fi
    fi
    if [[ ! -s $PANEL_CERT || ! -s $PANEL_CERT_KEY ]]; then
        fqdn=$(hostname -f 2>/dev/null || hostname)
        san="DNS:${fqdn}"
        # The subject's CN holds at most 64 characters (cloud host names can
        # be longer); clients check the name in subjectAltName, which has
        # the full name.
        cn=$fqdn
        (( ${#cn} <= 64 )) || cn=$(hostname -s 2>/dev/null || true)
        [[ -n $cn && ${#cn} -le 64 ]] || cn="NimVor panel"
        for ip in $(hostname -I 2>/dev/null); do san="${san},IP:${ip}"; done
        run install -d -m 0755 "$(dirname "$PANEL_CERT")"
        run install -d -m 0700 "$(dirname "$PANEL_CERT_KEY")"
        if [[ $DRY_RUN -eq 1 ]]; then
            echo "    [dry-run] openssl req -x509 ... -addext subjectAltName=${san} -out $PANEL_CERT"
        else
            out=$(openssl req -x509 -newkey rsa:2048 -nodes -days 825 -subj "/CN=${cn}" \
                -addext "subjectAltName=${san}" -keyout "$PANEL_CERT_KEY" -out "$PANEL_CERT" 2>&1) || { printf '%s\n' "$out" >&2; die "the certificate could not be created"; }
            chmod 0600 "$PANEL_CERT_KEY"
        fi
        ok "self-signed certificate for ${san}"
    else
        ok "keeping the certificate in $PANEL_CERT"
    fi
    # IPv6 only where the server has it (Nginx refuses [::] otherwise).
    listen6=""
    [[ -e /proc/net/if_inet6 ]] && listen6="    listen [::]:${PUBLIC_HTTPS} ssl;"
    if [[ $DRY_RUN -eq 1 ]]; then
        echo "    [dry-run] write $PANEL_SITE and link $PANEL_SITE_LINK"
    else
        install -d -m 0755 "$(dirname "$PANEL_SITE")"
        # $http_host keeps the port, so the panel sees the address the
        # browser uses (security keys check it). Plain HTTP sent to the
        # port (error 497) is redirected to HTTPS.
        cat > "$PANEL_SITE" <<NGINX
# Managed by NimVor (install.sh --public-https): the panel over HTTPS.
server {
    listen ${PUBLIC_HTTPS} ssl;
${listen6}
    server_name _;
    ssl_certificate     ${PANEL_CERT};
    ssl_certificate_key ${PANEL_CERT_KEY};
    ssl_protocols TLSv1.2 TLSv1.3;
    error_page 497 =301 https://\$http_host\$request_uri;
    client_max_body_size 256m;
    # Web terminal: WebSockets to the agent's loopback listener
    # (terminal_listen in agent.json).
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
    fi
    if [[ $PKG == dnf ]] && command -v getenforce >/dev/null && [[ $(getenforce) != Disabled ]]; then
        run restorecon "$PANEL_CERT" "$PANEL_CERT_KEY" "$PANEL_SITE"
        # Nginx connects to the panel on 127.0.0.1 and listens on the port.
        run setsebool -P httpd_can_network_connect 1
        if ! semanage port -l 2>/dev/null | grep -E '^http_port_t\s' | grep -qw "${PUBLIC_HTTPS}"; then
            run semanage port -a -t http_port_t -p tcp "${PUBLIC_HTTPS}" || run semanage port -m -t http_port_t -p tcp "${PUBLIC_HTTPS}"
        fi
    fi
    if [[ $DRY_RUN -eq 0 ]] && ! nginx -t >/dev/null 2>&1; then
        nginx -t || true
        rm -f "$PANEL_SITE_LINK" "$PANEL_SITE"
        die "Nginx rejected the configuration; the panel's Nginx site was removed again (use the SSH tunnel)"
    fi
    run systemctl enable nginx
    if systemctl is-active --quiet nginx 2>/dev/null; then run systemctl reload nginx; else run systemctl start nginx; fi
    # Cookies only over HTTPS; trust Nginx (127.0.0.1) for the client
    # address and X-Forwarded-Proto.
    if [[ $DRY_RUN -eq 1 ]]; then
        echo "    [dry-run] $CONF_DIR/panel.php: secure_cookie => true, trusted_proxies => ['127.0.0.1']"
    else
        sed -i "s/'secure_cookie' => false/'secure_cookie' => true/" "$CONF_DIR/panel.php"
        if grep -q "'trusted_proxies' => \[\]" "$CONF_DIR/panel.php"; then
            sed -i "s/'trusted_proxies' => \[\]/'trusted_proxies' => ['127.0.0.1']/" "$CONF_DIR/panel.php"
        fi
        grep -q "'secure_cookie' => true" "$CONF_DIR/panel.php" || warn "set 'secure_cookie' => true in $CONF_DIR/panel.php by hand"
        grep -q "'trusted_proxies' => \[[^]]*'127.0.0.1'" "$CONF_DIR/panel.php" || warn "add '127.0.0.1' to 'trusted_proxies' in $CONF_DIR/panel.php by hand"
        systemctl restart whacp-panel.service
        # The web terminal's WebSocket listener (loopback; Nginx proxies to it).
        set_terminal=$("$PHP_BIN" -r '$f = $argv[1]; $c = json_decode((string) file_get_contents($f), true);
            if (!is_array($c)) { exit(1); }
            if (($c["terminal_listen"] ?? "") === "") { $c["terminal_listen"] = "127.0.0.1:" . $argv[2];
                file_put_contents($f, json_encode($c, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES) . "\n"); echo "set"; }' "$CONF_DIR/agent.json" "$TERMINAL_PORT") \
            || warn "set \"terminal_listen\": \"127.0.0.1:${TERMINAL_PORT}\" in $CONF_DIR/agent.json by hand for the web terminal"
        if [[ $set_terminal == set ]]; then
            systemctl restart whacp-agent.service
            ok "web terminal on 127.0.0.1:${TERMINAL_PORT} (behind Nginx)"
        fi
    fi
    if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
        run ufw allow "${PUBLIC_HTTPS}/tcp"
        ok "UFW allows ${PUBLIC_HTTPS}/tcp"
    elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
        run firewall-cmd --permanent --add-port="${PUBLIC_HTTPS}/tcp"
        run firewall-cmd --reload
        ok "firewalld allows ${PUBLIC_HTTPS}/tcp"
    else
        ok "no active firewall here (a provider firewall must allow ${PUBLIC_HTTPS}/tcp)"
    fi
fi

# ------------------------------------------------------ 9. health checks ----
log "Running health checks"
if [[ $DRY_RUN -eq 0 ]]; then
    for _ in 1 2 3 4 5 6 7 8 9 10; do [[ -S /run/whacp/agent.sock ]] && break; sleep 1; done
    systemctl is-active --quiet whacp-agent && ok "whacp-agent is running" || warn "whacp-agent is not running: journalctl -u whacp-agent"
fi
if [[ $DRY_RUN -eq 0 && $AGENT_ONLY -eq 0 ]]; then
    runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php "$PHP_BIN" "$PREFIX/panel/bin/whacp" agent:check && ok "panel can reach the agent" || warn "panel cannot reach the agent"
    for _ in 1 2 3 4 5; do curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 && break; sleep 1; done
    curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null && ok "panel answers on 127.0.0.1:${PORT}" || warn "panel is not answering: journalctl -u whacp-panel"
    if [[ -n $PUBLIC_HTTPS ]]; then
        for _ in 1 2 3 4 5; do curl -fsSk "https://127.0.0.1:${PUBLIC_HTTPS}/healthz" >/dev/null 2>&1 && break; sleep 1; done
        curl -fsSk "https://127.0.0.1:${PUBLIC_HTTPS}/healthz" >/dev/null && ok "panel answers over HTTPS on port ${PUBLIC_HTTPS}" || warn "the panel does not answer on https://127.0.0.1:${PUBLIC_HTTPS}/: journalctl -u nginx"
    fi
    runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php "$PHP_BIN" "$PREFIX/panel/bin/whacp" discovery:run || warn "initial discovery failed"
    # Cron schedules in the new agent's form (each job sandboxed).
    runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php "$PHP_BIN" "$PREFIX/panel/bin/whacp" cron:sync || warn "some cron schedules were not written again (see above)"
fi

# --------------------------------------------------- 10. server profile ----
SETUP_FAILED=0
if [[ $PROFILE != none && $AGENT_ONLY -eq 0 && $DRY_RUN -eq 0 ]]; then
    log "Setting up the server ($PROFILE)"
    setup_args=(setup:apply "--profile=$PROFILE")
    [[ -z $SETUP_HOSTNAME ]] || setup_args+=("--hostname=$SETUP_HOSTNAME")
    [[ -z $SETUP_PHP ]] || setup_args+=("--php=$SETUP_PHP")
    [[ $THIRD_PARTY_REPOS -eq 1 ]] || setup_args+=(--no-third-party-repos)
    if ! runuser -u whacp -- env WHACP_CONFIG=$CONF_DIR/panel.php "$PHP_BIN" "$PREFIX/panel/bin/whacp" "${setup_args[@]}"; then
        SETUP_FAILED=1
        warn "some setup steps failed (see above). NimVor itself is installed; fix the cause and run: sudo -u whacp WHACP_CONFIG=$CONF_DIR/panel.php $PHP_BIN $PREFIX/panel/bin/whacp ${setup_args[*]}"
    fi
fi

if [[ $AGENT_ONLY -eq 1 ]]; then
    cat <<EOF

The NimVor agent is installed ($({ [[ $ENABLE_WRITES -eq 1 ]] || grep -q '"allow_write_actions": true' "$CONF_DIR/agent.json" 2>/dev/null; } && echo "write mode" || echo "read-only discovery mode")).
EOF
    if [[ -n $REMOTE_ACCESS ]]; then
        cat <<EOF
Connect it from your NimVor panel under "All servers" with:

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

NimVor is installed ($({ [[ $ENABLE_WRITES -eq 1 ]] || grep -q '"allow_write_actions": true' "$CONF_DIR/agent.json" 2>/dev/null; } && echo "write mode" || echo "read-only discovery mode")).
EOF
if [[ $PROFILE == none ]]; then
    cat <<EOF
Nothing in your existing Nginx, PHP, database, SSL or cron configuration was
changed. NimVor only ever edits files it creates itself (named whacp-*).

EOF
else
    cat <<EOF
The server is set up as: $PROFILE$([[ $SETUP_FAILED -eq 1 ]] && echo " (some steps failed: see above)").
Server → Components shows each part and what is left to do. The setup can
run again at any time (finished steps are skipped); its last results:

    sudo -u whacp WHACP_CONFIG=$CONF_DIR/panel.php $PHP_BIN $PREFIX/panel/bin/whacp setup:status

EOF
fi
if [[ -n $PUBLIC_HTTPS ]]; then
    cat <<EOF
Open the panel in your browser:

    https://$(hostname -I 2>/dev/null | awk '{print $1}'):${PUBLIC_HTTPS}/

The certificate is self-signed, so the browser warns once: check the address
and continue. Sign-in cookies are now HTTPS-only (the SSH tunnel no longer
signs in). Turn on two-factor authentication in your profile.

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

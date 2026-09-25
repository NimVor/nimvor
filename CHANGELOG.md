# Changelog

All notable changes to WhaCP are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased] — 0.5.0

Phases 5–28: write mode and everything built on it. Write actions stay off
until the agent runs with `allow_write_actions` (`install.sh --enable-writes`).

### Added
- **Write-path infrastructure:** file writer limited to an allowlist of
  WhaCP-owned paths, change sets with backup/validate/rollback, background
  jobs with logs, allowlisted command runner extensions (commands for
  site accounts run unprivileged).
- **Websites (5):** PHP, static and reverse-proxy sites, aliases, redirects,
  enable/disable/delete, generated Nginx configuration validated with
  `nginx -t` and rolled back on failure; conflicts with existing vhosts are
  refused.
- **PHP (6):** install versions, per-site FPM pools and settings.
- **Databases (7):** MySQL/MariaDB databases, users, host patterns, grants;
  passwords stored only when asked (encrypted).
- **SSL (8):** Let's Encrypt issue/renew (webroot), custom certificates,
  HTTPS redirect and HSTS.
- **File manager (9)** confined to a website's folder; **log viewer (10)**
  with live tail.
- **Cron (11), workers (12)** as systemd units, **Git deployment (13)** with
  deploy keys and signed webhooks.
- **Redis (14):** settings, password, ACL users, flush.
- **Backups (15):** AES-256-GCM encrypted site/server backups, S3-compatible
  storage with multipart upload, schedules, retention, restore, download.
- **Security (16):** UFW rules, SSH hardening drop-in, Fail2ban jails and
  unbans; every change rolls back after two minutes unless confirmed;
  security audit.
- **Monitoring (17):** metric history, alert rules (disk, memory, load,
  certificates, agent down, quotas) and webhook notifications.
- **Packages (18) and server settings (19):** updates, installs, hostname,
  timezone, scheduled reboot.
- **Multi-server (20):** remote agents over TLS 1.3 with public-key pinning
  and client IP allowlist; `install.sh --agent-only --remote-access`.
- **Hosting users (21):** plans with limits, resellers and customers scoped
  to their own resources, disk quotas.
- **REST API (22):** personal API tokens (read or read/write, expiry),
  `/api/v1` with OpenAPI description.
- **Plugins (23):** admin-installed plugins with their own routes,
  permissions, navigation, events and settings; example plugin.
- **Application installer (24):** WordPress and Laravel.
- **Installer and upgrades (25–26):** `upgrade.sh` with pre-flight checks,
  backups and automatic rollback; scheduler timer; version file.
- **Import (27):** take over a website found by discovery — files are copied
  into a new WhaCP website, the old vhost is disabled and the new one
  enabled in one validated reload (old configuration restored on any
  failure); the application's database is detected; imports can be undone.
- **Hardening (28):** two-factor sign-in (TOTP, replay-protected, recovery
  codes, admin and CLI reset); `bin/whacp doctor` checks PHP, file modes of
  secrets, schema, database integrity, agent reachability/version and the
  scheduler; the agent's OpenAPI action list is generated from and verified
  against the registry in tests.

### Added after the phase work
- QR code for two-factor setup (built-in encoder), per-role two-factor
  requirement; server switcher and job visibility follow resource scope.
- Email: SMTP settings, alert emails, forgotten-password links.
- Restore of server configuration backups by component, with safety
  backup, validation and rollback; extract-only mode.
- CI job running every real-service test as root (nginx, PHP-FPM,
  MariaDB, Redis, UFW, SSH, Fail2ban, WordPress, Laravel); agent unit tests
  pass without root; reproducible release archives with provenance.

### Added: hosting services
- **File manager as a full page (cPanel-style):** tree, list, upload,
  editor and archive handling, confined to the website's folder and run as
  the website's account; works on phones.
- **Redis key browser and Memcached:** browse, search, view, edit and delete
  Redis keys (by database, with type-aware views and TTLs); Memcached
  status, statistics, settings (memory, connections, loopback listen
  addresses, only after confirmation) and flush.
- **Backup destinations:** besides S3-compatible storage, SFTP (host key
  pinned on first use and confirmed), FTP with explicit TLS (certificate
  pinned the same way), Google Drive (OAuth, drive.file scope) and a local
  folder. Destinations are tested before saving and credentials are stored
  encrypted.
- **Web database browser:** tables, structure, paged rows, edit/insert/
  delete by primary key and a SQL console with result limits, all through a
  per-database account that is locked between requests (its password is
  rotated each time); built-in MySQL client (native and caching_sha2
  authentication).
- **FTP and SFTP accounts:** FTPS accounts per website served by WhaCP's own
  Pure-FTPd service (passive range, TLS, firewall), and SFTP-only accounts
  chrooted to the website with password or SSH keys; accounts are removed
  with their website.
- **DNS zones (BIND):** zones for websites with templates (web, mail, SPF,
  DMARC), all common record types, secondaries with notify and transfer;
  every change is validated with named-checkzone/named-checkconf, loaded
  and verified by querying the new SOA serial, and rolled back otherwise.
- **Email hosting:** Postfix, Dovecot and OpenDKIM for WhaCP's mail
  domains.
  - Mailboxes with quotas, aliases and catch-all.
  - Submission on 587 with sign-in; a user may send only as their own
    addresses.
  - IMAP over TLS on 993; plain 143 is closed.
  - DKIM keys, with the MX, SPF, DKIM and DMARC records added to the DNS
    zone in one click.
  - Setup validates and restarts every service, checks ports 25, 587 and
    993, and restores the previous configuration if anything fails.
  - The domain page lists the settings for mail apps.
- **Dovecot 2.3 and 2.4:** the matching configuration syntax is written for
  the installed version (Ubuntu 22.04/24.04: 2.3; Ubuntu 26.04: 2.4);
  mailboxes are stored the same way.
- **Spam filtering (Rspamd):** Rspamd runs as a milter in front of Postfix.
  - Mail above the *Junk* score is marked and filed into Junk by a Sieve
    rule that runs before personal filters.
  - Mail above the *Refuse* score is rejected while the sender is still
    connected, so it bounces rather than disappearing.
  - Senders cannot set the spam header themselves.
  - WhaCP writes only its own files in Rspamd's local.d; files it did not
    write are replaced only after confirmation. Changes are checked with
    `rspamadm configtest`.
- **Webmail (Roundcube):** installed from the distribution's packages and
  switched on for a host name.
  - WhaCP keeps Roundcube's configuration and SQLite database in its own
    folders and never edits the package's files.
  - Roundcube runs in its own PHP-FPM pool and account, restricted with
    open_basedir, behind its own HTTPS Nginx site that runs only
    index.php.
  - It signs in to Dovecot and sends through submission as the user, so
    sender checks and DKIM signing apply.
  - A Let's Encrypt certificate for the host name is used when one exists,
    otherwise a self-signed one.
  - Each change creates or upgrades the database, loads the sign-in page to
    check it works, and rolls back if it doesn't.
- **Security keys (WebAuthn/passkeys)** as a second factor: ES256, RS256 and
  Ed25519 keys, several per account, sign-in counter checks, origin and
  relying-party verification; works together with TOTP and recovery codes.
- **AlmaLinux, Rocky Linux and RHEL 9 and 10**, supported alongside Ubuntu:
  - PHP versions come from Remi (for example `php83-php-fpm`).
  - Packages are installed with dnf, with WhaCP's package names mapped to
    the distribution's (EPEL and Rspamd's repository included).
  - firewalld is supported as well as UFW: service ports are opened in
    whichever is active, and the firewall rule manager keeps its automatic
    rollback.
  - SELinux stays enforcing. WhaCP labels what it creates (websites, mail
    storage, webmail data), turns on the booleans it needs (proxying,
    webmail connections, FTP) and labels a moved SSH port.
  - BIND is opened to the network for hosted zones (after confirmation),
    and Postfix listens on all interfaces.
  - Logs, fail2ban (journal), update checks (dnf, needs-restarting) and
    server configuration backups use the RHEL locations.
  - The installer supports these systems and sets up the panel's PHP 8.3.

### Changed
- `packages.install` accepts several components at once (`names`).
- String settings can be optional; new settings for DNS name servers,
  hostmaster address and default IPv4.
- Agent errors print their details with `%+v` (tests and logs).

### Testing
- Real-service CI on Ubuntu now also covers Pure-FTPd, SFTP, BIND, Postfix,
  Dovecot, OpenDKIM, Rspamd and Roundcube; mail is actually delivered,
  filtered, signed and read over IMAP, and webmail signs in and sends.
- New CI job boots an AlmaLinux 9 virtual machine (KVM, SELinux enforcing),
  runs every real-service test and the installer inside it and prints any
  SELinux denials.
- Browser tests for the file manager, Redis browser, database browser,
  backup destinations and security keys (virtual authenticator).

### Fixed
- The panel's test SMTP server wrote its transcript non-atomically (flaky
  test); the test authenticator now pads EC key coordinates to 32 bytes.
- Roundcube's pool sets `include_path` with `php_value`: Roundcube refuses
  to run when include_path is locked.
- `uninstall.sh` left the agent's write-mode drop-in behind.

## [0.4.0]

Phases 0–4: foundation, panel, agent, server dashboard and read-only discovery.

### Added
- **Foundation:** repository layout, MIT license, contribution and security
  policies, architecture and operations documentation, CI workflow, Makefile,
  development environment (`scripts/dev.sh`) with a fixture server.
- **Panel (PHP 8.3+):** dependency-free MVC core (container, router, views,
  middleware), SQLite with migrations, session authentication with Argon2id,
  login throttling, CSRF protection, strict Content-Security-Policy, RBAC with
  Administrator/Operator/Viewer roles, panel user management with
  last-administrator protection, settings, audit log, profile and password
  change (other sessions are invalidated), friendly error pages with
  reference IDs, CLI (`bin/whacp`).
- **Agent (Go):** Unix-socket API with bearer token and SO_PEERCRED checks,
  named action registry with a write gate (`allow_write_actions`, off),
  uniform response envelope, OS abstraction (Debian family; RHEL scaffold),
  metrics collector with history, service status via systemd, allowlisted
  command runner (no shell), secret redaction, OpenAPI description.
- **Dashboard:** live CPU, memory, disk, load, network charts and service
  state; server information page; services page.
- **Read-only discovery:** Nginx configuration parser (includes, symlinks,
  disabled sites), websites correlated with PHP-FPM pools and certificates,
  PHP versions and pools, Let's Encrypt lineages, MySQL/MariaDB schemas from
  the data directory (no credentials), Redis, cron (secrets redacted), system
  accounts, firewall state; snapshots with checksum history; heuristics
  detecting sites managed by other panels.
- **Installer:** OS/requirement/conflict checks, `--dry-run`, hardened
  systemd units (agent filesystem is read-only), loopback-only panel,
  first-administrator creation, health checks, uninstaller.

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

### Fixed
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

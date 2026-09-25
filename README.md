# WhaCP

**WhaCP — Open-Source Linux Server & Hosting Control Panel** (WHA Control Panel)

WhaCP is a free, MIT-licensed control panel for VPS and dedicated servers. It is
built around one rule: **a control panel has root-level consequences, so every
privileged operation must be explicit, validated, audited and reversible.**

> **Status: v0.5.0 — feature-complete preview (all 28 roadmap phases built).**
> Every feature is covered by unit, HTTP and real-service tests, but WhaCP has
> not yet been validated on a wide range of production servers. Install it in
> read-only mode first (the default): it inventories the server **without
> changing anything**. Turn on write mode with `--enable-writes` when you are
> ready to let WhaCP manage websites.

## What works today

| Area | Features |
|------|----------|
| Panel | sign-in with Argon2id, throttling, **two-factor (TOTP with QR setup and recovery codes, or WebAuthn security keys and passkeys; can be required per role)** and email password reset; CSRF, strict CSP; roles (Administrator, Operator, Viewer, Reseller, Hosting customer) and custom roles; audit log; settings |
| Agent | Go service with named actions only, Unix socket (token + peer UID) or pinned TLS 1.3 for remote servers; write gate; allowlisted binaries, no shell |
| Dashboard & monitoring | live CPU/RAM/disk/load/network, services, history, alerts (disk, memory, load, certificates, agent down) by email and webhook |
| Discovery | read-only inventory of Nginx sites, PHP/FPM, certificates, MySQL/MariaDB, Redis, cron, accounts, firewall, sites managed by other panels |
| **Import** | take over a discovered website: files are copied, Nginx switches in one validated reload, the old config is kept and the import can be undone |
| Websites | PHP, static and reverse-proxy sites; aliases, redirects, per-site PHP-FPM pools; `nginx -t` gate and automatic rollback |
| PHP | install versions, per-site settings |
| Databases | MySQL/MariaDB databases, users and grants; web database browser (rows, structure, SQL console) confined to one database |
| Email | Postfix + Dovecot (2.3 and 2.4) + OpenDKIM: Rspamd spam filter (Junk folder or refusal), mailboxes with quotas, aliases and catch-all, DKIM keys and the MX/SPF/DKIM/DMARC records (one click into the DNS zone), submission with sign-in and anti-spoofing |
| DNS | authoritative zones on BIND: A, AAAA, CNAME, MX, TXT, NS, SRV, CAA, templates, secondaries; validated with named-checkzone, serial checked after every reload, rollback |
| SSL | Let's Encrypt issue/renew, custom certificates, HTTPS redirect, HSTS |
| Files & logs | full-screen file manager (folder tree, multi-select, drag-and-drop uploads, full-page editor, permissions, zip/tar.gz compress and safe extract), log viewer with live tail |
| FTP & SFTP | per-website FTPS accounts (pure-ftpd, chrooted, quotas, TLS required) and SFTP for the website user (OpenSSH chroot, keys and/or password, no shell) |
| Automation | cron jobs, systemd workers (queues), Git deployment with webhooks |
| Redis & Memcached | Redis settings, password, per-site ACL users and a key browser (all types, binary-safe, per-site prefix); Memcached install, stats, settings, flush |
| Backups | encrypted (AES-256-GCM) site and server backups, S3-compatible, SFTP, FTPS, Google Drive or local-disk storage, schedules, retention, restore (server configuration by component, with rollback) |
| Security | UFW firewall, SSH hardening, Fail2ban — every change auto-rolls back unless confirmed; security audit |
| Server | packages and updates, hostname, timezone, reboot |
| Hosting | multiple servers, plans and quotas, resellers and customers |
| Integration | REST API with personal tokens (OpenAPI), plugins, one-click WordPress and Laravel |
| Operations | installer, upgrades with automatic rollback, `bin/whacp doctor` self-checks |

## Architecture in one picture

```text
 Browser ──HTTPS/SSH tunnel──▶ WhaCP Panel (PHP 8.3+, MVC, SQLite)      REST API /api/v1 (tokens)
                                   │  never runs shell commands
                                   │  POST /v1/actions/<name>  (bearer token)
                    ┌──────────────┴──────────────────────────┐
                    ▼                                         ▼
   /run/whacp/agent.sock (0660 root:whacp,        remote servers: TLS 1.3, pinned
   SO_PEERCRED check)                             public key, client IP allowlist
                    │                                         │
                    ▼                                         ▼
                     WhaCP Agent (Go, systemd; read-only sandbox unless write mode)
                       ├─ named actions only · write gate · jobs for long work
                       ├─ Detect → Back up → Validate → Apply → Verify → Roll back
                       ├─ Nginx · PHP-FPM · SSL · MySQL/MariaDB · Redis · backups
                       ├─ cron · workers · Git · UFW · SSH · Fail2ban · packages
                       └─ fixed allowlist of binaries, no shell, secrets redacted
```

Read more in [docs/architecture](docs/architecture/README.md).

## Quick start (development, no root needed)

Requirements: Go ≥ 1.22, PHP ≥ 8.3 with `pdo_sqlite`, `curl`, `mbstring`, Composer, OpenSSL.

```bash
git clone https://github.com/mchkhawarshahzad/WhaCP.git && cd WhaCP
scripts/dev.sh
# open http://127.0.0.1:8090  —  admin / dev-password-change-me
```

The dev agent inspects the bundled fixture server in
`agent/tests/fixtures/ubuntu-host` (five websites, two PHP versions, MariaDB,
Redis, certbot, cron). Run `DEV_ROOT=/ scripts/dev.sh` to inspect your own
machine instead (read-only; some files need root to be visible).

## Install on a server

Supported: Ubuntu 22.04, 24.04 and 26.04 LTS with systemd.

```bash
git clone https://github.com/mchkhawarshahzad/WhaCP.git && cd WhaCP
sudo ./installer/install.sh --dry-run          # see exactly what will happen
sudo ./installer/install.sh --admin-user=admin --admin-email=you@example.com
ssh -L 8090:127.0.0.1:8090 your-server         # then open http://127.0.0.1:8090
```

By default WhaCP is **read-only**: the installer never edits Nginx, PHP-FPM,
database, firewall or SSH configuration and never reloads existing services.
Add `--enable-writes` to let WhaCP manage the server. Upgrade later with
`sudo ./installer/upgrade.sh` (automatic rollback on failure). Details:
[docs/installation](docs/installation/README.md).

## Tests

```bash
make lint test     # gofmt, go vet, go test -race, php -l, PHPUnit (unit/integration/HTTP)
make smoke         # end-to-end: agent + panel + login + discovery + remote TLS agent + doctor
# Agent tests against real services (nginx, php-fpm, MariaDB, Redis, UFW, …) in a disposable VM/container:
cd agent && sudo go test -tags realhost ./...
```

## Repository layout

```text
agent/        Go agent (cmd/, internal/<domain>/, api/openapi.yaml, tests/fixtures)
panel/        PHP control plane (app/, config/, public/, routes/, resources/, tests/)
installer/    install.sh, upgrade.sh, uninstall.sh, systemd units, default configuration
plugins/      example plugin (see docs/plugins)
docs/         architecture, installation, administration, agent, security, api, plugins, development, troubleshooting
scripts/      developer tooling (dev.sh)
tests/        cross-component integration tests (smoke.sh, upgrade.sh)
storage/      notes on where runtime state lives
```

## Contributing & security

- [CONTRIBUTING.md](CONTRIBUTING.md) — development workflow and engineering rules
- [SECURITY.md](SECURITY.md) — how to report vulnerabilities
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)
- [CHANGELOG.md](CHANGELOG.md)

WhaCP is an original implementation. It contains no code, assets, branding or
terminology taken from other control panels.

## License

[MIT](LICENSE)

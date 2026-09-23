# WhaCP

**WhaCP — Open-Source Linux Server & Hosting Control Panel** (WHA Control Panel)

WhaCP is a free, MIT-licensed control panel for VPS and dedicated servers. It is
built around one rule: **a control panel has root-level consequences, so every
privileged operation must be explicit, validated, audited and reversible.**

> **Status: v0.4.0-dev — read-only discovery release.**
> WhaCP can be installed next to an existing production setup (including one
> managed by another panel) and will inventory it **without changing anything**.
> Write operations (creating sites, issuing certificates, …) start in v0.5.0.

## What works today

| Area | Status |
|------|--------|
| Panel: sign-in, sessions, CSRF, brute-force throttling | ✅ |
| Role-based access control (Administrator, Operator, Viewer) | ✅ |
| Panel users, settings, audit log, profile / password change | ✅ |
| Go agent over an authenticated Unix socket (token + peer UID) | ✅ |
| Live dashboard: CPU, RAM, disk, load, network, uptime, services | ✅ |
| Server page: OS, kernel, CPU, disks, interfaces, timezone, firewall state | ✅ |
| **Read-only discovery** of websites (Nginx), PHP versions & FPM pools, SSL certificates, MySQL/MariaDB databases, Redis, cron jobs, system accounts, systemd services | ✅ |
| Discovery snapshots with change checksum and history | ✅ |
| Installer (loopback panel, hardened systemd units) | ✅ early |
| Website/PHP/DB/SSL management, file manager, logs, backups, … | planned — see [roadmap](docs/development/roadmap.md) |

## Architecture in one picture

```text
 Browser ──HTTPS/SSH tunnel──▶ WhaCP Panel (PHP 8.3+, MVC, SQLite)
                                   │  never runs shell commands
                                   │  POST /v1/actions/<name>  (bearer token)
                                   ▼
                     /run/whacp/agent.sock  (0660 root:whacp, SO_PEERCRED check)
                                   │
                                   ▼
                     WhaCP Agent (Go, systemd, read-only sandbox)
                       ├─ OS abstraction (Ubuntu/Debian, RHEL family scaffold)
                       ├─ Nginx parser · PHP-FPM · SSL · MySQL/MariaDB · Redis
                       ├─ cron · users · firewall · systemd · metrics collector
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

## Install on a server (read-only discovery)

Supported: Ubuntu 22.04, 24.04 and 26.04 LTS with systemd.

```bash
git clone https://github.com/mchkhawarshahzad/WhaCP.git && cd WhaCP
sudo ./installer/install.sh --dry-run          # see exactly what will happen
sudo ./installer/install.sh --admin-user=admin --admin-email=you@example.com
ssh -L 8090:127.0.0.1:8090 your-server         # then open http://127.0.0.1:8090
```

The installer never edits Nginx, PHP-FPM, database, firewall or SSH
configuration and never reloads existing services. Details:
[docs/installation](docs/installation/README.md).

## Tests

```bash
make lint test     # gofmt, go vet, go test -race, php -l, PHPUnit (unit/integration/HTTP)
make smoke         # end-to-end: agent + panel + login + discovery on the fixture server
```

## Repository layout

```text
agent/        Go agent (cmd/, internal/<domain>/, api/openapi.yaml, tests/fixtures)
panel/        PHP control plane (app/, config/, public/, routes/, resources/, tests/)
installer/    install.sh, uninstall.sh, systemd units, default configuration
docs/         architecture, installation, administration, agent, security, api, plugins, development, troubleshooting
scripts/      developer tooling (dev.sh)
tests/        cross-component integration tests (smoke.sh)
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

# Roadmap

WhaCP is built phase by phase; each phase must be production-quality before
the next begins. Write operations start only after read-only discovery is
proven on real servers.

| Release | Phases | Scope | Status |
|---------|--------|-------|--------|
| v0.1.0 | 0 | Foundation: repo, docs, license, dev environment | ✅ done |
| — | 1 | Panel: auth, sessions, RBAC, dashboard shell, users, settings, audit, SQLite | ✅ done |
| v0.2.0 | 2 | Agent: systemd service, secure socket, auth, health, CPU/RAM/disk, OS & services | ✅ done |
| v0.3.0 | 3 | Server dashboard connected to the agent | ✅ done |
| v0.4.0 | 4 | Read-only discovery: sites, domains, PHP, FPM, SSL, databases | ✅ done — **validate on a real server next** |
| v0.5.0 | 5 | Website management: create/edit/enable/disable/delete, Nginx templates, `nginx -t` gate | next |
| v0.6.0 | 6–7 | PHP management and pools; MySQL/MariaDB databases, users, privileges | |
| v0.7.0 | 8–10 | Let's Encrypt; secure file manager; log viewer with live tail | |
| v0.8.0 | 11–13 | Cron; systemd workers; Git deployment with webhooks | |
| v0.9.0 | 14–17 | Redis; backups (local, then providers); security (firewall, Fail2ban, SSH); monitoring | |
| — | 18–19 | Package management; server actions (reboot, updates, hostname, timezone) | |
| — | 20–21 | Multi-server (agent registration, mTLS, heartbeats); hosting users, plans, quotas | |
| — | 22–24 | Public REST API + OpenAPI; plugins; application installers (WordPress, Laravel, …) | |
| — | 25–27 | Production installer, upgrade system with rollback, opt-in migration/import from other panels | |
| v1.0.0 | 28 | Production hardening: security, permission, upgrade, backup/restore and failure-recovery audits | |

## Before v0.5.0 starts

- Install v0.4 on a development server that hosts real sites (including ones
  managed by another panel) and compare discovery with reality.
- Fix every discrepancy with a new fixture case.
- Design the website-management action set (`website.create`, `website.enable`,
  `website.disable`, `nginx.reload`) with backup/validate/rollback, and the
  narrow `ReadWritePaths` each action needs.

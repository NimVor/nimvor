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
| v0.4.0 | 4 | Read-only discovery: sites, domains, PHP, FPM, SSL, databases | ✅ done |
| v0.5.0 | 5–7 | Websites with `nginx -t` gate; PHP versions and pools; databases, users, privileges | ✅ done |
| v0.5.0 | 8–10 | Let's Encrypt; secure file manager; log viewer with live tail | ✅ done |
| v0.5.0 | 11–13 | Cron; systemd workers; Git deployment with webhooks | ✅ done |
| v0.5.0 | 14–17 | Redis; encrypted backups with S3 storage; firewall/SSH/Fail2ban with auto-rollback; monitoring and alerts | ✅ done |
| v0.5.0 | 18–19 | Package management; server actions (reboot, updates, hostname, timezone) | ✅ done |
| v0.5.0 | 20–21 | Multi-server over pinned TLS; hosting users, resellers, plans, quotas | ✅ done |
| v0.5.0 | 22–24 | REST API + OpenAPI; plugins; WordPress and Laravel installers | ✅ done |
| v0.5.0 | 25–27 | Production installer, upgrades with rollback, import of existing websites | ✅ done |
| v0.5.0 | 28 | Hardening: two-factor sign-in, `doctor` self-checks, API contract tests | ✅ done |
| v1.0.0 | — | Field validation (below) | next |

## Before v1.0.0

- Install on development servers that host real sites (including ones
  managed by another panel) in read-only mode; compare discovery with
  reality and turn every discrepancy into a fixture case.
- Exercise write mode end to end on fresh Ubuntu 22.04, 24.04 and 26.04
  VMs: create, import, back up, restore, upgrade and roll back.
- Independent security review of the agent action catalogue
  (`agent/api/openapi.yaml`, `x-whacp-actions`) and the panel's
  authorization (`Permissions.php`, owner scoping).
- Restore drill on a new VM (the component restore itself is covered by a
  real-host test in CI).
- Tag the first release (archives, checksums and provenance are built by
  the Release workflow).

## Later

- More application installers and backup providers.

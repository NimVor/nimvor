# Security Policy

## Reporting a vulnerability

Please **do not open a public issue** for security problems. Use GitHub's
private vulnerability reporting ("Security" tab → "Report a vulnerability")
on this repository. Include affected versions, reproduction steps and impact.

We aim to acknowledge reports within 3 working days and to publish a fix or
mitigation within 30 days, coordinating disclosure with the reporter.

## Supported versions

| Version | Supported |
|---|---|
| 1.0.0-rc1 (release candidate) | yes — latest release |
| 0.5.0 and older (never released separately) | no — upgrade with `installer/upgrade.sh` |

Only the latest release receives security fixes.

## Scope

In scope: the panel, the agent, the installer, default configuration and
systemd units. Out of scope: vulnerabilities in third-party software WhaCP
observes (Nginx, PHP, MariaDB, …) unless WhaCP makes them exploitable.

## Trust model

- **Administrators** (and any role with `plugins.manage`, `settings.manage`,
  `security.manage` or `servers.manage`) control the whole server.
- **Operators** run hosting without managing users, settings or security.
  **Viewers** read only.
- **Resellers and customers** see and change only their own websites and
  those websites' resources. Every route checks the role's permission, and
  every resource's owner, on the server. This is covered by the release-gate
  suites in `panel/tests/Security`.
- **Plugins** are fully trusted code inside the panel (see
  [docs/plugins](docs/plugins/README.md)).
- **Website accounts:** cron jobs, workers, deploy hooks and application
  installers run as the website's own account (UID ≥ 1000), never as root.
- **The agent** runs as root and only performs named actions with
  validated parameters. It has no generic command endpoint, and commands
  come from a fixed allowlist.

## Design summary

The security model is documented in [docs/security](docs/security/README.md):
local-only agent socket with token and peer-UID checks (or pinned TLS 1.3
for remote servers), a fixed, reviewed action catalogue with a write gate,
validate-before-reload with automatic rollback, a sandboxed agent in
read-only mode, strict CSP, CSRF protection, throttled logins with optional
two-factor sign-in, and a complete audit log.

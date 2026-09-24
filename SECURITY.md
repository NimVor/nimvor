# Security Policy

## Reporting a vulnerability

Please **do not open a public issue** for security problems. Use GitHub's
private vulnerability reporting ("Security" tab → "Report a vulnerability")
on this repository. Include affected versions, reproduction steps and impact.

We aim to acknowledge reports within 3 working days and to publish a fix or
mitigation within 30 days, coordinating disclosure with the reporter.

## Supported versions

WhaCP is pre-1.0. Only the latest release on the default branch receives
security fixes.

## Scope

In scope: the panel, the agent, the installer, default configuration and
systemd units. Out of scope: vulnerabilities in third-party software WhaCP
observes (Nginx, PHP, MariaDB, …) unless WhaCP makes them exploitable.

## Design summary

The security model is documented in [docs/security](docs/security/README.md):
local-only agent socket with token and peer-UID checks (or pinned TLS 1.3
for remote servers), a fixed, reviewed action catalogue with a write gate,
validate-before-reload with automatic rollback, a sandboxed agent in
read-only mode, strict CSP, CSRF protection, throttled logins with optional
two-factor sign-in, and a complete audit log.

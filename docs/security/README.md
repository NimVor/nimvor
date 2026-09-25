# Security

## Threat model (summary)

| Threat | Mitigation |
|--------|------------|
| Remote attacker reaches the panel | Loopback-only listener by default; SSH tunnel or TLS proxy required |
| Password guessing | Argon2id, 12-char minimum, per-user and per-IP throttling, uniform error messages, timing-equalised verification |
| Password reset abuse | Same answer for every account, 256-bit single-use tokens stored as hashes, 30-minute expiry, per-account and per-IP limits, links only from the configured panel URL (no Host-header poisoning), two-factor still required |
| Stolen password | Optional (or per-role required) TOTP two-factor sign-in (secret encrypted at rest, codes single-use, recovery codes hashed); password and second factor must be completed within 5 minutes and 5 tries |
| Stolen API token | Tokens are stored as hashes, scoped read or read/write, expire, can be revoked, act with the owner's permissions; use is audited |
| Session theft / fixation | HttpOnly + SameSite cookies (Secure behind TLS), strict session mode, ID regeneration on login, idle and absolute timeouts, invalidation on password change |
| CSRF | Synchronizer token on every non-GET request (form field or `X-CSRF-Token`), rotated at login |
| XSS | Output escaping everywhere, CSP `script-src 'self'; style-src 'self'` (no inline code) |
| Clickjacking | `frame-ancestors 'none'`, `X-Frame-Options: DENY` |
| Privilege escalation inside the panel | RBAC per route, denials audited, last-admin protection |
| Compromised panel | Agent exposes only named actions with strictly validated parameters; no shell, no arbitrary file access; write gate off by default; in read-only mode the sandbox makes the filesystem read-only; in write mode the agent writes only WhaCP-owned paths and runs site commands as the site's unprivileged account |
| Other local users talking to the agent | Socket 0660 root:whacp, SO_PEERCRED UID allowlist, bearer token |
| Network attacker between panel and a remote agent | TLS 1.3 only, public-key pinning (the token is never sent to an unpinned peer), client IP allowlist with lockout |
| Operator locks themselves out | Firewall, SSH and Fail2ban changes roll back automatically after two minutes unless confirmed; SSH changes validated with `sshd -t` |
| Bad configuration takes sites down | Generated from structured templates, validated (`nginx -t`, `php-fpm -t`, `sshd -t`) before reload, previous version restored on failure |
| Tenant reaches another tenant | Owner scoping on every query for *own*-scope roles; reseller limits aggregate their customers; file manager confined to the website root with symlink checks; per-site PHP-FPM pools and system accounts |
| Malicious plugin upload | Plugins cannot be uploaded through the web; they are installed on disk by an administrator, refused if group/world-writable or containing symlinks, and must be enabled explicitly |
| Restoring a bad or crafted server backup | Checksum verified, safety backup first, extraction confined to a root-only staging folder (no absolute/`..` paths, nothing written below symlinks, set-id bits dropped), validate before reload, automatic rollback; firewall/SSH/WhaCP settings never restored in place |
| Leaked backups | AES-256-GCM encryption with a per-server key; remote storage credentials encrypted with the panel key |
| Path traversal | `filesystem.Root` rejects `..`, resolves symlinks inside the root; Nginx includes limited to `/etc/nginx`; router parameters restricted to `[A-Za-z0-9_.-]` |
| Secret leakage | Cron commands, logs and audit details are redacted; Redis/MySQL passwords never returned; private keys never read; `/etc/shadow` never read |
| Information disclosure through errors | Users see a message, reason, action and reference ID; stack traces only in logs |
| Resource exhaustion | Size limits on files, bodies and command output; bounded walks; systemd memory/task limits |

## Policy for dangerous operations

Every write action follows this sequence:

```text
Detect current state
  → Back up the files that will change
  → Generate the new configuration from structured templates
  → Validate (syntax check, e.g. nginx -t; semantic checks)
  → Apply atomically (write temp file, rename)
  → Verify (service healthy, site answers)
  → Roll back automatically on any failure
```

Firewall and SSH changes additionally require an explicit confirmation and an
automatic rollback timer so an operator cannot lock themselves out.

The complete list of agent actions, with whether each modifies the server,
is in `agent/api/openapi.yaml` (`x-whacp-actions`). A test fails when an
action is added without updating it, so the catalogue can be reviewed as a
whole.

## Hardening checklist for operators

- Keep the panel on loopback; if you proxy it, use TLS and IP restrictions.
- Set `secure_cookie => true` whenever the panel is served over HTTPS.
- Protect `/etc/whacp/agent.token` (0640 root:whacp); rotate it by writing a
  new value and restarting both services.
- Review the audit log regularly (`security.*` and `auth.login` failures).
- Turn on two-factor sign-in for every administrator and reseller.
- Run `bin/whacp doctor` after installing, upgrading or changing
  `/etc/whacp`; it flags world-readable secrets and a stopped scheduler.
- Stay in read-only mode until you need write mode; remote agents should
  allow only the panel's IP.
- Keep a copy of the backup key (*Backups → key*) off the server: without it
  encrypted backups cannot be restored.

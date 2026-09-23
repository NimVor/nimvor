# Security

## Threat model (summary)

| Threat | Mitigation |
|--------|------------|
| Remote attacker reaches the panel | Loopback-only listener by default; SSH tunnel or TLS proxy required |
| Password guessing | Argon2id, 12-char minimum, per-user and per-IP throttling, uniform error messages, timing-equalised verification |
| Session theft / fixation | HttpOnly + SameSite cookies (Secure behind TLS), strict session mode, ID regeneration on login, idle and absolute timeouts, invalidation on password change |
| CSRF | Synchronizer token on every non-GET request (form field or `X-CSRF-Token`), rotated at login |
| XSS | Output escaping everywhere, CSP `script-src 'self'; style-src 'self'` (no inline code) |
| Clickjacking | `frame-ancestors 'none'`, `X-Frame-Options: DENY` |
| Privilege escalation inside the panel | RBAC per route, denials audited, last-admin protection |
| Compromised panel | Agent exposes only named actions; no shell, no arbitrary file access; write gate off; agent sandbox makes the filesystem read-only |
| Other local users talking to the agent | Socket 0660 root:whacp, SO_PEERCRED UID allowlist, bearer token |
| Path traversal | `filesystem.Root` rejects `..`, resolves symlinks inside the root; Nginx includes limited to `/etc/nginx`; router parameters restricted to `[A-Za-z0-9_.-]` |
| Secret leakage | Cron commands, logs and audit details are redacted; Redis/MySQL passwords never returned; private keys never read; `/etc/shadow` never read |
| Information disclosure through errors | Users see a message, reason, action and reference ID; stack traces only in logs |
| Resource exhaustion | Size limits on files, bodies and command output; bounded walks; systemd memory/task limits |

## Policy for dangerous operations

Future write actions must follow this sequence and document it in their PR:

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

## Hardening checklist for operators

- Keep the panel on loopback; if you proxy it, use TLS and IP restrictions.
- Set `secure_cookie => true` whenever the panel is served over HTTPS.
- Protect `/etc/whacp/agent.token` (0640 root:whacp); rotate it by writing a
  new value and restarting both services.
- Review the audit log regularly (`security.*` and `auth.login` failures).

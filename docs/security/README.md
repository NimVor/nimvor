# Security

## Threat model (summary)

| Threat | Mitigation |
|--------|------------|
| ModSecurity connector built from source | Sources: the connector and libmodsecurity releases are pinned by SHA-256; Nginx's source is the distribution's signed source package (apt/dnf verify it) or nginx.org's release checked against an administrator-given SHA-256. Compiling runs as a temporary unprivileged account (`whacp-build`, in a sandboxed unit; `make`/`sh`/`tar` refuse root), removed afterwards. Installation is a write-ahead transaction: everything is backed up first and rolled back on any failure; the Nginx binary is never touched |
| Core Rule Set from the release archive | SHA-256 pinned; only regular rule and data files inside the release folder are unpacked (no links, no `..`, size-limited) into a WhaCP-owned versioned folder; starts in detection only |
| Disk quota commands need CAP_SYS_ADMIN | The agent's own capabilities are unchanged. Only setquota, repquota, quotaon, quotacheck and mount (allowlisted, arguments validated) run as root in a transient unit started by PID 1 with CapabilityBoundingSet=CAP_SYS_ADMIN (+CAP_DAC_READ_SEARCH for quotacheck) and NoNewPrivileges; refused for any other command or capability, and never as a website account |
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
| Compromised panel | Agent exposes only named actions with strictly validated parameters; no arbitrary commands or file access; web terminal shells only as website accounts that already have shell access, in their sandbox (a root shell only when root sets `terminal_root` in the agent's configuration); write gate off by default; in read-only mode the sandbox makes the filesystem read-only; in write mode the agent writes only WhaCP-owned paths and runs site commands as the site's unprivileged account |
| Compromised panel names an administrator account as a website account | Any account in a privileged group (root, sudo, wheel, admin, adm, shadow, disk, docker, lxd, lxc, systemd-journal, libvirt, kvm, kmem) or named in sudoers is refused as a website account and for every command the agent runs as an account |
| Other local users talking to the agent | Socket 0660 root:whacp, SO_PEERCRED UID allowlist, bearer token |
| Network attacker between panel and a remote agent | TLS 1.3 only, public-key pinning (the token is never sent to an unpinned peer), client IP allowlist with lockout |
| Operator locks themselves out | Firewall, SSH and Fail2ban changes roll back automatically after two minutes unless confirmed; SSH changes validated with `sshd -t` |
| Bad configuration takes sites down | Generated from structured templates, validated (`nginx -t`, `php-fpm -t`, `sshd -t`) before reload, previous version restored on failure |
| Tenant reaches another tenant | Owner scoping on every query for *own*-scope roles; reseller limits aggregate their customers; file manager confined to the website root with symlink checks; per-site PHP-FPM pools and system accounts |
| Website PHP or cron job escalates through a setuid binary or the system | PHP-FPM services run with a WhaCP drop-in (`<fpm unit>.d/whacp-sandbox.conf`, and the same settings in per-account FPM services): `NoNewPrivileges`, only the capabilities the master needs (switching to pool accounts, sockets, rlimits, slow-log ptrace; no `CAP_SYS_ADMIN`, `CAP_NET_ADMIN`, `CAP_SYS_MODULE`), `ProtectSystem=full`, private devices, kernel tunables/modules/logs, clock and cgroups protected, IP and local sockets only. Every cron job runs as the account through a root-only launcher (`/etc/whacp-cron/<site>/<id>.run`) in a transient service with the sandbox of every command run as the account (no capabilities, `NoNewPrivileges`, system read-only). Applied when the agent starts after an update; `cron:sync` rewrites existing schedules |
| Website code steals the server's cloud credentials or scans the internal network | An nftables table WhaCP owns (`inet whacp_egress`, loaded at boot by `whacp-egress.service`) matches the website accounts by UID: the cloud metadata services (169.254.169.254, 169.254.170.2, 100.100.100.200, fd00:ec2::254) are refused by default; optionally private and link-local networks, and a list of the only remote TCP/UDP ports allowed (Security → Outgoing Connections). Root, the system's services (cloud-init, guest agents, chrony) and loopback are never matched. The UID list follows website accounts as they are created and deleted |
| Malicious plugin upload | Plugins cannot be uploaded through the web; they are installed on disk by an administrator, refused if group/world-writable or containing symlinks, and must be enabled explicitly |
| Restoring a bad or crafted server backup | Checksum verified, safety backup first, extraction confined to a root-only staging folder (no absolute/`..` paths, nothing written below symlinks, set-id bits dropped), validate before reload, automatic rollback; firewall/SSH/WhaCP settings never restored in place |
| Leaked backups | AES-256-GCM encryption with a per-server key; remote storage credentials encrypted with the panel key; SFTP host keys and self-signed FTPS certificates pinned after the administrator confirms them; FTP without TLS only when chosen explicitly |
| FTP/SFTP user reaches other files | FTP accounts chrooted by pure-ftpd to their folder and mapped to the website's own user; TLS required by default; SFTP confined by OpenSSH ChrootDirectory + internal-sftp (no shell, forwarding or TTY), keys root-owned; SSH changes validated with `sshd -t` and verified per user with `sshd -T` |
| Crafted DNS records break or hijack zones | Records validated field by field (no newlines, quoted TXT), zone checked with named-checkzone and named-checkconf, BIND's served serial verified, rollback; zone transfers refused unless secondaries are listed; customers only host their websites' domains |
| Mail server abused for spam or spoofing | Relay only from this machine or signed-in users (port 587, TLS required); port 25 offers no sign-in; signed-in users may send only as their own address or aliases to them; system accounts cannot sign in to IMAP; mailbox passwords stored as SHA-512 crypt hashes |
| Phished or stolen password | Optional or role-enforced second factor: TOTP, or WebAuthn keys bound to the panel's origin (phishing-resistant), one-time challenges, user presence required, signature counter checked for cloned keys |
| SQL console used to escape a database | The database browser never runs SQL as root: each database has its own locked account with privileges on that database only, unlocked with a one-time random password per request; 30-second statement limit |
| Path traversal | `filesystem.Root` rejects `..`, resolves symlinks inside the root; Nginx includes limited to `/etc/nginx`; router parameters restricted to `[A-Za-z0-9_.-]` |
| Secret leakage | Cron commands, logs and audit details are redacted; Redis/MySQL passwords never returned; private keys never read; `/etc/shadow` never read |
| Information disclosure through errors | Users see a message, reason, action and reference ID; stack traces only in logs |
| Web terminal hijacked or abused | Panel authorization (owner, plan, shell access) plus the Linux account's own password, sent from the browser to the agent only inside the WebSocket and checked with PAM's `unix_chkpwd` (never stored, logged or passed to the panel); three tries per ticket, five per 15 minutes per account and per address; single-use 256-bit tickets, one minute, bound to the client address; Origin must equal Host; listener on loopback behind Nginx; each session its own systemd unit as the account with no_new_privs, no capabilities and its resource limits; accounts in privileged groups refused; password or shell-access changes, suspension and sign-out end sessions; idle and maximum times; root shells off unless root enables them, then with the panel password and authenticator code |
| Web attacks on hosted applications | Optional ModSecurity with the OWASP Core Rule Set per website (off, detection only, blocking); rules and exclusions only by administrators; custom rules limited to rule directives in their own ID range (no `exec`, file operators or includes); every change checked with `nginx -t` and rolled back |
| WordPress toolkit used to reach other accounts or run site code with privileges | Scans read files only, as the website account; WP-CLI and PHP always run as the website account in its slice, with the site's plugins and themes not loaded; WP-CLI pinned by SHA-512 and root-owned; installation, website and ownership checked in the panel, paths and plugin names in the agent (no option-like names); database passwords never read or returned; one-time sign-in files random (128 bits), 0600, one minute, deleted on first use, only for administrators and only over HTTPS |
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

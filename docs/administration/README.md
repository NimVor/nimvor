# Administration

## Roles and permissions

| Role | Intended for | Can |
|------|--------------|-----|
| Administrator | owners | everything |
| Operator | day-to-day operations | websites, PHP, databases, certificates, files, jobs, services, Redis, discovery; view security and the audit log. Not users, settings, security changes or reboots |
| Viewer | read-only stakeholders | dashboard, monitoring, server, services, websites, PHP, databases, SSL, cron, backups |
| Reseller | selling hosting | their own customers (within their plan) and those customers' websites |
| Hosting customer | website owners | only their own websites, databases, files, jobs and backups, within plan limits |

Custom roles (Administration → Roles) combine any permissions with either
*all resources* or *own resources* scope. Plugins can add permissions; they
are granted to administrators when the plugin is enabled.

Permissions are defined in code (`panel/app/Security/Permissions.php`) and
synchronised into the database by `bin/whacp migrate`. At least one active
administrator must always exist: the panel refuses to delete, disable or
demote the last one. Users cannot delete or disable themselves.

## Sessions and sign-in

- Passwords: Argon2id, minimum 12 characters, must not contain the username.
- Throttling: 5 failures per username or 10 per IP in 15 minutes lock sign-in.
- Sessions end after the idle timeout (setting) or 12 hours, and immediately
  when the user's password changes or the account is disabled.
- **Two-factor sign-in:** each user can turn it on under *Profile* by
  scanning the QR code with any TOTP authenticator app (6 digits, 30 s).
  Administrators can require it per role (*Users & roles → role → Sign-in
  policy*); members without it can then only reach their profile until they
  set it up. A code works once; the previous
  and next codes are accepted for clock drift. Ten single-use recovery codes
  are shown once. Five wrong codes end the attempt and count towards the
  sign-in throttle. Administrators can reset a user's two-factor sign-in on
  the user's edit page (after verifying who is asking), or with
  `bin/whacp user:2fa-reset <username>`.

## Forgotten passwords

When email is set up (*Settings → Email*) and the public panel URL is set,
the sign-in page offers *Forgot your password?*. The answer is the same
whether or not the account exists; the link is single-use, valid for 30
minutes, limited to three per account and ten per address per hour, and
never built from the request's host name. Two-factor sign-in is still
required afterwards. Without email, reset passwords with
`bin/whacp user:password`.

## Email

*Settings → Email* configures SMTP (TLS on 465 or STARTTLS on 587, with
certificate verification; unencrypted only to a relay on this server). The
password is stored encrypted. Alerts go to the addresses under *Send
alerts to* in addition to the optional webhook. *Send test message to me*
checks the settings.

## Settings

| Setting | Default | Notes |
|---------|---------|-------|
| Panel name | WhaCP | header and title |
| Session idle timeout | 30 min | 5–1440 |
| Discovery cache | 300 s | 0 = always ask the agent |
| Dashboard refresh | 10 s | 3–300 |
| Certificate warning | 14 days | highlight threshold |

Infrastructure settings (paths, cookies, proxies) are in `/etc/whacp/panel.php`.

## Audit log

Recorded: sign-in success/failure/throttling (including the two-factor
step), sign-out, CSRF rejections, access denials, user, role and plan
changes, password and two-factor changes, settings changes, discovery runs,
and every change made through the agent (websites, databases, certificates,
files, jobs, backups, firewall/SSH/Fail2ban, packages, reboots, imports,
API token use, plugin actions). Each entry has user, action, target,
result, IP, user agent and redacted details. Filter by action prefix
(e.g. `user.`), user, result or free text.

## Discovery

*Server → Discovery* shows counts, warnings encountered while reading
configuration, and snapshot history. **Re-scan** asks the agent for a fresh
inventory; it only reads. A changed checksum between snapshots means the
server configuration changed.

## CLI

```bash
export WHACP_CONFIG=/etc/whacp/panel.php
sudo -u whacp -E php /opt/whacp/panel/bin/whacp migrate
sudo -u whacp -E php /opt/whacp/panel/bin/whacp user:create alice alice@example.com --role=operator
sudo -u whacp -E php /opt/whacp/panel/bin/whacp user:password alice
sudo -u whacp -E php /opt/whacp/panel/bin/whacp user:2fa-reset alice
sudo -u whacp -E php /opt/whacp/panel/bin/whacp agent:check
sudo -u whacp -E php /opt/whacp/panel/bin/whacp discovery:run
sudo -u whacp -E php /opt/whacp/panel/bin/whacp schedule:run   # normally run by whacp-scheduler.timer
sudo -u whacp -E php /opt/whacp/panel/bin/whacp doctor
```

## Restoring server configuration

*Backups → server backup → Restore* (needs *Reboot, update and configure
the server*) restores selected parts: Nginx, PHP, MySQL/MariaDB settings,
Redis, certificates, Fail2ban and system cron jobs. The backup's checksum is
verified, a safety backup of the current configuration is taken, each part
replaces its folder as a whole, Nginx and PHP-FPM are validated before
anything reloads, and the previous folders come back if validation or a
service restart fails. Firewall, SSH, systemd units and `/etc/whacp` are
never restored automatically; *Extract for review only* unpacks the whole
backup into `/var/lib/whacp-agent/restore/` (root only) to copy from by
hand.

**Restore drill on a new server:** install WhaCP with `--enable-writes`,
copy the old backup key to `/var/lib/whacp-agent/backup.key` (mode 0600),
add the remote storage and restore the server backup, then the website
backups.

## Importing existing websites

Discovery lists websites WhaCP did not create. With the *Import existing
websites* permission, open one and choose **Import into WhaCP**. The preview
shows every step, blockers, warnings and both Nginx configurations. The
import:

1. creates a WhaCP website and account and copies the files (links that
   point outside the website are skipped, set-uid and world-writable bits
   are dropped);
2. disables the old configuration — removes the `sites-enabled` link, or
   moves a `conf.d` file into the agent's state directory — and enables the
   new one in a single `nginx -t`-validated reload; on any error the old
   configuration is put back;
3. registers the application's database if it finds one in `wp-config.php`
   or `.env` (the database itself is not changed).

The original files and configuration file are kept. **Undo import** on the
website's overview restores the old configuration and removes the WhaCP
copy. Imports are refused when the configuration file also serves other
domains, the site is a catch-all, or the account or folder already exists.

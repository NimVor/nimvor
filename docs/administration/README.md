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
- **Security keys and passkeys (WebAuthn):** under *Profile → Security keys*
  a user adds hardware keys (YubiKey, Titan…) or platform passkeys; up to
  ten per user. A key counts as two-factor sign-in, also for roles that
  require it; users with both a key and an authenticator app can use
  either. Keys are bound to the panel's address (the public panel URL when
  set), so they need HTTPS (or `localhost`) and do not work on a phishing
  site. The signature counter is checked to detect copied keys. Removing a
  key asks for the password; an administrator's two-factor reset removes
  the keys too.

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

## File manager

*Website → Files* opens a full-screen file manager: a folder tree, a
sortable list with multi-select (Ctrl/Shift-click, checkboxes), a context
menu, keyboard shortcuts (Delete, F2, Enter, Backspace, Ctrl+A),
drag-and-drop uploads of files and folders, a full-page editor (line
numbers, Ctrl+S, conflict detection when the file changed meanwhile),
permissions, copy/move, and zip/tar.gz compress and extract. Everything is
confined to the website's folder by the agent; extraction skips links,
device files and entries that would leave the target folder, drops set-id
bits, is limited to 50,000 entries and 8 GiB, and keeps existing files
unless *Replace files that already exist* is ticked.

## Email

*Email* hosts mailboxes on this server with Postfix (SMTP), Dovecot (IMAP)
and OpenDKIM. An administrator installs the packages and sets email up once
with the mail server's host name (the name MX records point to; its reverse
DNS should match). Setting up changes Postfix's `main.cf` and `master.cf`
with `postconf` and turns off Dovecot's sign-in for system accounts (only
WhaCP mailboxes can sign in), after confirmation and with backups; WhaCP's
own Dovecot settings are a drop-in file and OpenDKIM runs from WhaCP's own
unit and configuration. Postfix is checked with `postfix check`, Dovecot
with `doveconf`, the services are restarted and ports 25, 587 and 993 must
answer, otherwise everything is restored. A Let's Encrypt certificate for
the host name is used when present, otherwise a self-signed one.

For a domain (customers: their own websites' domains) WhaCP creates a DKIM
key and lists the DNS records to publish — MX, SPF, DKIM and DMARC — which
can be added to the domain's zone on this panel with one click. Mailboxes
have a password (stored as a SHA-512 crypt hash; a generated one is shown
once) and an optional quota; aliases forward to one or more addresses, and
`*` catches all other addresses of the domain. Deleting a mailbox moves its
mail to the agent's trash folder.

Mail apps use the full address as user name, IMAP on port 993 (TLS) and
SMTP submission on port 587 (STARTTLS, sign-in required). Port 25 accepts
mail for hosted domains only and offers no sign-in; only this machine may
relay. Signed-in users can send only as their own address or an alias that
delivers to them. Outgoing mail is DKIM-signed. Spam filtering and webmail
are not included. Dovecot 2.3 (Ubuntu 22.04 and 24.04) is supported.

## DNS

*DNS* serves zones from this server's BIND. An administrator installs BIND
and sets it up once: WhaCP adds a single `include` line for its own zone
list to BIND's configuration (`/etc/bind/named.conf.local`, after
confirmation, with a backup), validates with `named-checkconf`, starts
BIND and opens port 53 when the firewall is on. Set *DNS name servers*
(and optionally the hostmaster address and the default IPv4 for new
zones) under *Settings*.

A zone belongs to a website (customers create zones only for their own
websites' domains) or, for administrators, stands alone. The usual records
can be added on creation (@ and www to the server, SPF, CAA for Let's
Encrypt). Supported types: A, AAAA, CNAME, MX, TXT (long values split
automatically), NS, SRV and CAA; secondary servers can be allowed to
transfer the zone and are notified of changes.

Every change sends the complete zone to the agent, which writes it with a
higher serial (YYYYMMDDnn), checks it with `named-checkzone` and
`named-checkconf`, loads it with `rndc` and queries BIND until it serves
the new serial. If any step fails the previous zone comes back and the
panel keeps its previous records. Zones defined elsewhere in BIND are
refused. Deleting a website keeps its zone; delete the zone separately.

## FTP and SFTP

*Server → FTP server* installs pure-ftpd and runs it from WhaCP's own unit
(`whacp-ftpd.service`) and configuration (`/etc/whacp-ftp`); the
distribution's pure-ftpd configuration is never edited. If another FTP
server (the distribution's pure-ftpd, vsftpd or ProFTPD) is running, WhaCP
stops and disables it only after you confirm. TLS is required by default
(a self-signed certificate is created; clients check its SHA-256
fingerprint, shown in the panel); plain FTP can be allowed explicitly. With
the firewall on, port 21 and the passive range are opened. The service is
restarted, checked to answer on port 21 and rolled back otherwise.

On a website's *FTP / SFTP* tab, its owner adds FTP accounts named
`<name>@<domain>`, each confined (chroot) to the website folder or a
sub-folder, optionally with a quota. Files are created as the website's
user. Passwords are stored as SHA-512 crypt hashes; a generated password is
shown once. Accounts are removed with the website.

*SFTP access* lets the website's own system user in over SSH for file
transfer only: OpenSSH confines it to the websites folder (made `0711`, so
other websites cannot be listed) starting in its own folder, with no shell,
forwarding or TTY. Public keys are kept in root-owned files outside the
website; password login is optional. The SSH configuration is validated
with `sshd -t`, and WhaCP verifies with `sshd -T` that the change applies to
that user only before reloading; otherwise it is rolled back.

## Database browser

*Databases → Browse* opens a full-page browser for one database: tables
and views with sizes, rows (sort by any column, filter, pages of up to 500,
CSV export), a row editor (NULL, defaults and binary values as
hexadecimal), insert and multi-row delete, the table structure with its
indexes and `CREATE` statement, and an SQL console that runs several
statements and shows each result. Customers can browse the databases of
their own websites; administrators can also open databases WhaCP does not
manage.

Nothing runs as the database root user. Each database has its own browser
account (`whacp_b_…@localhost`) with privileges on that database only; it
is locked between requests and gets a new random password for each one, so
SQL typed in the console cannot read other databases, change server
settings or read files. Statements are limited to 30 seconds and results to
1,000 rows. Rows can be edited in tables that have a primary or unique key.
Views and routines created from the console are owned by the browser
account, which is removed together with the database. Changes (SQL, saved
and deleted rows) are recorded in the audit log with the statement or the
column names, never the row values.

## Redis and Memcached

*Redis → Browse keys* scans a database with a glob pattern and shows every
type (strings up to 1 MiB, the first 500 elements of collections, recent
stream entries); keys and values can be created, edited, given a TTL,
renamed and deleted. Binary data is shown as base64 and not edited. On a
website, *Redis* opens the same browser limited to that website's key
prefix (`<account>:`), which is also what its Redis ACL user may access.

*Memcached* installs the package, shows hits, memory, items, evictions and
connections, flushes the cache, and changes memory, connection limit and
listen addresses. The configuration file belongs to the package, so WhaCP
edits it only after you tick the confirmation; it keeps a backup, restarts
Memcached, checks it answers and otherwise restores the old file. Only
loopback addresses are accepted: Memcached has no authentication.

## Backup destinations

*Backups → Remote storage → Add storage* keeps copies off the server. Each
storage is tested (upload, list and delete of a probe file) before it is
saved; credentials are stored encrypted with the panel key.

| Type | Notes |
|------|-------|
| S3-compatible | AWS S3, Backblaze B2, Wasabi, Cloudflare R2, MinIO… Use keys limited to the bucket. |
| SFTP | Password or unencrypted private key. On the first connection WhaCP shows the server's host key fingerprint; after you confirm it the key is pinned and backups refuse any other key. |
| FTP | Explicit TLS (FTPS) by default, implicit TLS on port 990, or plain FTP only when chosen explicitly. Certificates are verified; a self-signed certificate is shown for confirmation and its public key is pinned. |
| Google Drive | Needs the panel on `https://` (set *Public panel URL*) and an OAuth client (*Web application*) with the Drive API enabled and `https://<panel>/backups/storages/google/callback` as redirect URI. WhaCP asks only for the `drive.file` scope (files it created) and stores backups in a "WhaCP backups" folder. |
| Local folder | An existing folder under `/mnt`, `/media`, `/backup(s)` or `/srv/backup(s)`, owned by root, not world-writable and without symbolic links — typically a second disk or a mounted NAS share. |

The optional sub-folder separates servers that share one destination.

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

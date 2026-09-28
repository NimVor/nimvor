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

The scheduler keeps the audit log for 365 days; set
`'audit' => ['retention_days' => N]` in `/etc/whacp/panel.php` to change
that (`0` keeps it forever). Each removal is itself recorded
(`audit.pruned`, with the count). Job history is kept 90 days after a job
finished, deployment history 90 days (always the latest 20 per website).

## Notifications

The bell in the top bar and **Notifications** list the results of
background jobs and monitoring alerts:

- **Failures** of any job, whoever started it and whether or not anyone was
  watching: the scheduler checks unfinished jobs every pass.
- **Finished** long jobs: backups (manual and scheduled), restores,
  deployments (panel and webhook), application installs, imports,
  installs of PHP and services, server configuration backups. Quick jobs
  (cron "run now", compressing or extracting files) are reported only when
  they fail.
- **Refused deploy webhooks** (a wrong signature, at most once an hour; a
  replayed delivery) and deployments or scheduled backups that could not
  start.
- **Monitoring alerts** when they open and when they resolve.

Who sees a notification follows the usual scope. Administrators and other
"all"-scope roles see everything. A customer sees their websites' job
results and alerts (certificate, backup) and their own quota alerts, and a
reseller also their customers'. Server matters (packages, services,
disk, memory, load, the agent) are for "all"-scope roles only. A website
that changes owner takes its notifications with it; deleting it removes
them. Website owners can open the log of any job on their website from its
notification.

Read state is per user. Each user can switch on **Email me about failures
and alerts I can see** on the page; failures and alerts are then emailed
through the panel's email settings, one message per recipient. Messages
are short and redacted: the full log stays in the panel.

Notifications are kept 90 days (at most 1000 per website). The bell's
count refreshes every minute while the tab is visible; that request does
not count as activity, so an idle session still signs out. The permission
is `notifications.view` (in every default role).

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

## List pages for administrators and resellers

*Websites*, *Databases*, *FTP* and *Security* share one layout: an info
bar (for example the FTP address or the database host and port), a row
of actions with the service and its version, and a table with its
operations inline (`Manage | Log | ⋮`). Search, filters (status,
Inbound/Outbound, rule kind) and paging (10–100 rows, *Goto*, *Total*)
work in the page without reloading; the chosen page size is remembered
in the browser.

- **Websites** is a table for administrators and resellers (cards on
  request): the status switch disables or enables a website after a
  confirmation; *Backup* counts completed backups and links to the
  website's backups; *SSL* shows the days left on the certificate from
  the latest discovery (orange from the warning threshold under
  Settings, red once expired). Resellers see the certificates of their
  own websites only.
- **Security → Overview** counts SSH logins of today, yesterday and the
  last 7 days (accepted and failed, from the journal of `sshd`, also
  `sshd-session` on OpenSSH 9.8+), shows the last SSH and panel sign-ins,
  and rates the server from 0 to 100 from the security checks: a passed
  check counts fully, a warning half, weighted by severity (high 3,
  medium 2, low 1); informational checks do not count.
- **Security → Firewall** lists the rules with their protocol, port,
  source and direction, and whether a program listens on the port
  (*Listening*, *Local only* for 127.0.0.1, *Not listening*, read from
  `/proc/net`). The switch turns the firewall on or off (with the same
  2-minute confirmation as before).

## Websites for hosting customers

Hosting customers (own-scope roles that do not manage users) get a simple
layout without the sidebar. Their home page is their websites as cards
(search, sort by creation date or name, filter by status, "Create site").
Administrators see the same cards through the cards/list switch on
*Websites*.

Opening a website shows its details (site user, root directory, IPv4,
PHP handler and version, site and database size, SSL, last backup) and
**Site managing**: tiles for FTP accounts, Files, Databases, Domains (DNS),
SSL, Scheduler (cron), Mail, Backup copies, Site access (SFTP), Settings,
PHP settings, Applications, Git, Workers, Redis, Logs, Statistics,
Subdomains, Addon domains and Manual settings (Advanced), plus
**Search…** (press `/`). *Delete the website* leads to the typed
confirmation under Advanced; *Disable the website* asks first.
Administrators also have the website's menu in the sidebar.

**Advanced** holds the server details (system user, folders, PHP-FPM
limits, generated Nginx configuration, recent tasks) and the Danger
Zone: disable/enable, reload PHP, re-apply configuration and, set apart,
delete.

Section pages share one pattern: a toolbar with a count, a filter where
lists grow and "+ Add", which opens its form in place (also from links
such as `…/databases#new-database`, and again after a validation error).

### What customers can do (and what stays with administrators)

- **Automatic backups** are set up by administrators. Customers see the
  schedules and back up and restore by hand.
- **DNS zones and mail domains** an administrator creates for a
  customer's website belong to that website: its owner sees and manages
  them.
- **Let's Encrypt** expiry notices go to the email address of the account
  that issues the certificate.
- **Database users** can connect from this server, a specific IP address
  or network (`203.0.113.%`), or any host. Remote access (globe icon)
  gives an existing user another host: a separate login with a new
  password, shown once, and the same database access. Remote connections
  also need MariaDB/MySQL to listen on the network (`bind-address`) and
  port 3306 open in the firewall; WhaCP does not change either. Users are
  assigned to the account's databases with *Assign to database*.
- **SQL import:** each website database has an Import action. It uploads
  a `.sql` or `.sql.gz` file into the website's `tmp/` folder (removed
  afterwards) or imports a file already in the website. The file is read
  confined to the website, and the mysql client's own commands (shell,
  files) are turned off.
- **Cron jobs** have a *Logs* page listing their runs (start, duration,
  exit code, scheduled or manual, output) from the last 512 KB of
  `logs/cron-<id>.log`. *Email the output* sends each run's output to
  the email of the account that turned it on (the server must be able
  to send mail); the log keeps it too.
- **Subdomains and addon domains** (Site → Subdomains & addons) are
  websites of their own under the same site account. A subdomain gets an
  A record when the parent's zone is hosted here.
- **System accounts:** customers and resellers can reuse only the account
  of one of their own websites; any other name must not exist on the
  server yet. Deleting a website keeps its account while another website
  uses it.
- **Statistics:** install AWStats under Packages. Each website turns it
  on under Statistics. AWStats then runs every 6 hours as the website's
  account (`/etc/cron.d/whacp-awstats-*`, configuration in
  `/etc/awstats/awstats.whacp-<domain>.conf`) and keeps its data in
  `<website>/.awstats`. The panel draws the report (visitors, pages, days,
  hours, referrers, search engines, browsers, systems, countries, errors)
  from AWStats' data file; AWStats' own HTML is not shown.
- **Malware scans:** install ClamAV (and its signature updater) under
  Packages, then set a schedule and quarantine under Security → Malware
  scan. Scans run `clamscan` as the website's account, so they read only
  what that account can read; files it cannot read are counted as errors.
  With quarantine on, infected files move to
  `<website>/.whacp-quarantine/<time>/` with mode 0000, and the owner can
  delete them in the File manager. Owners can also scan their website
  from its Malware scan page.

## Account suspension

Administrators suspend a hosting customer or reseller under Users →
account → Account status (with an optional reason). Staff accounts are
turned off with "Account enabled" instead. For the account, and for every
customer of a suspended reseller:

| Service | While suspended |
| --- | --- |
| Panel and API | sign-in and API tokens are refused ("This account is suspended") |
| Websites | offline (the vhost is removed); the website's own on/off setting is kept |
| Cron jobs, workers | not run / stopped; their own settings are kept |
| Email | logins refused (IMAP, POP3, webmail, SMTP AUTH); mail is still delivered |
| Databases | database users are locked (`ACCOUNT LOCK`); data is kept |
| SSH/SFTP, FTP | the website account expires (`usermod --expiredate 1`), which refuses SSH/SFTP logins including keys; FTP password hashes are locked |

Unsuspending reverses each step. Customers suspended on their own stay
suspended when their reseller is resumed. If a server could not apply a
step, the page says which, sign-in stays blocked, and "Apply the
suspension again" retries.

## Plans, limits and bandwidth

- **Overrides:** an administrator can override any limit of one account
  (account page → Plan and limits → Override limits). Empty uses the
  plan's value; 0 is unlimited. The plan and its other accounts are not
  changed.
- **Changing plans:** account page → Change plan compares the current and
  new plan limit by limit, with current usage. A downgrade never deletes
  anything; an account over a new limit just cannot add more.
- **Bandwidth:** Users → Bandwidth over limit lists accounts over their
  monthly limit (a reseller's limit covers its customers). "Reset" sets
  this month's counter to 0; log positions and earlier months are kept.
- **Terminating accounts:** select hosting accounts on the Users page and
  choose "Terminate selected". The confirmation lists what will be removed
  and requires typing TERMINATE. Each account's websites (files and system
  account), mailboxes, databases, database users and DNS zones are removed
  through the same code as deleting them one by one; if a step fails, that
  account is kept with what is left and the others continue.

## Feature Manager

Plans → Manage feature lists. Each list turns panel features on or off;
assign a list to a plan on the plan's form ("Feature list"). A plan
without a list has every feature. For accounts on the plan, a feature
that is off is refused by the server (403, "not included in your hosting
plan", logged as an access denial) for its pages, form posts and API
calls; hiding the menu entries follows from that. A reseller's customers
get only features that both their own plan and the reseller's plan
include. Administrators and other staff roles are not limited. Deleting a
list gives its plans every feature again. Lists store the features they
turn off, so features added in later versions are on for existing lists.

## Shell access

Security → Shell access (administrators) sets each website account to:

- **Disabled** (default): SFTP only, chrooted to the websites folder.
- **Jailed:** SSH commands and interactive logins run in a bubblewrap
  sandbox (`/usr/local/sbin/whacp-jailsh` is the login shell and sshd's
  ForceCommand): `/usr` and a short list of `/etc` files read-only
  (no `/etc/shadow`, no service configuration), a generated passwd with
  only root and the account, the account's website folder writable,
  private `/tmp`, `/proc`, IPC and hostname, the network shared, no
  capabilities and no_new_privs. SFTP runs inside the sandbox too.
- **Normal:** `/bin/bash` on the host as the account. It can see other
  processes, `/tmp` and world-readable files; give it only to trusted
  accounts.

A mode applies only while the account's SSH/SFTP login is on (FTP / SFTP
page: keys in `/etc/ssh/whacp-keys`, optional password); turning that
login off takes the shell away first. Accounts whose plan's feature list
leaves out "SSH shell & web terminal" cannot get a shell, and the
scheduler removes shells a plan no longer includes. A suspended account
cannot log in at all.

On Ubuntu 23.10 and later, AppArmor stops unprivileged programs from
creating user namespaces. The installer then loads the `whacp-bwrap`
profile for `/usr/local/lib/whacp/bwrap`, a copy of bubblewrap that only
members of the `whacp-jail` group (jailed accounts) can run; the agent
refreshes the copy whenever shell access changes. Other accounts and
programs keep the restriction.

## Email filters, autoresponders and routing

- **Filters and autoresponder** (Email → domain → the funnel button of a
  mailbox): WhaCP renders them to the mailbox's personal Sieve script
  `<vmail>/<domain>/<mailbox>/.dovecot.sieve` (owned by vmail) and compiles
  it with `sievec`. The first save adds
  `/etc/dovecot/conf.d/99-whacp-sieve.conf`, which turns on personal Sieve
  scripts for Dovecot 2.3 (`sieve = file:~/sieve;active=~/.dovecot.sieve`)
  and 2.4 (`sieve_script personal`), validated with `doveconf -n` and
  followed by a Dovecot reload. The spam filter's script runs first.
- **Routing** (Email → domain → Mail routing): *local* delivers to the
  mailboxes here; *backup* removes the domain from local delivery and lists
  it in `/etc/whacp-mail/relay_domains` (Postfix `relay_domains`), so this
  server queues its mail for the primary MX; *remote* removes it from local
  delivery and from the alias map, so mail sent from this server to the
  domain goes to the domain's MX.

## Email administration

Email → Email administration (administrators with server permissions).

- **Mail server**: state, start-at-boot and version of Postfix, Dovecot,
  OpenDKIM and Rspamd, with start / reload / restart (the services
  permission); the results of `postfix check` and `doveconf -n`; the latest
  100 mail log lines. The settings form covers a fixed list of Postfix
  parameters (set with `postconf -e`) and Dovecot settings (written to
  `/etc/dovecot/conf.d/99-whacp-tuning.conf`). Only changed Postfix values
  are sent. Postfix values are checked with `postfix check` and Dovecot's
  with `doveconf -n` before either service reloads; a refusal or failed
  reload restores the previous values. The settings are kept in
  `/etc/whacp-mail/mail.json`, so setting up email again keeps them. There
  is no free-form configuration editing.
- **Greylisting**: WhaCP's policy service is the agent binary in its
  `greylist-policy` mode, run by `whacp-greylist.service` as a dynamic
  unprivileged user without capabilities and started by
  `whacp-greylist.socket` (`<queue_directory>/private/whacp-greylist`,
  owned by postfix, mode 0600). Its configuration
  (`/etc/whacp-mail/greylist.json`, root 0600) reaches it as a systemd
  credential; changing it restarts the service (the socket keeps Postfix's
  requests waiting). Triplets are kept in `/var/lib/whacp-greylist`
  (at most 200,000; expired ones are removed hourly). The agent reads the
  report over `/run/whacp-greylist/control` (0600). Postfix gets
  `check_policy_service { unix:private/whacp-greylist, default_action=DUNNO }`
  at the end of `smtpd_recipient_restrictions`, after
  `permit_mynetworks`, `permit_sasl_authenticated` and
  `reject_unauth_destination`: authenticated users and local mail are
  never greylisted, and if the service is down Postfix accepts mail rather
  than deferring it. The reply to a new triplet is `DEFER_IF_PERMIT 4.7.1`
  (a later rejection still wins). The SPF bypass follows RFC 7208 (ten
  DNS lookups; `ptr` never matches). Provider ranges come from each
  provider's SPF record (`ip4`, `ip6`, `a`, `mx`, `include`, `redirect`).
  Not verified yet: SELinux on AlmaLinux (Postfix's smtpd connecting to the
  socket); a denial there shows as Postfix warnings, and mail is accepted.
- **Tools**: the troubleshooter follows WhaCP's mail data (domains,
  routing, mailboxes, forwarders, catch-all) and, for other domains, their
  MX records or the smart host. The permission repair walks each domain's
  folder under the mailbox storage with directory file descriptors
  (`openat` with `O_NOFOLLOW`), sets the owner to vmail and removes access
  for others on the open descriptor, so a link placed in a mailbox cannot
  redirect it; the SELinux label `mail_spool_t` is applied again after.
  Mailbox conversion does not apply: every mailbox is Maildir.
- **Email authentication checker** (on each mail domain's page): runs in
  the agent (public DNS lookups). The server's address is the default IPv4
  address from the DNS settings, else its first public IPv4 address.
- **Mail queue**: `postqueue -j`; headers via `postcat -h -q`; deliver
  now, hold, release and delete via `postsuper` (queue IDs are validated).
- **Delivery reports and statistics**: parsed from the journal (mail
  facility) or `/var/log/mail.log`, `maillog` or `postfix.log`.

## FFmpeg

The agent's `ffmpeg.access` sets the members of the group `whacp-ffmpeg`
(exactly the list the panel sends, with `gpasswd --members`) and gives
`/usr/bin/ffmpeg` and `/usr/bin/ffprobe` to `root:whacp-ffmpeg` with mode
0750 (opened with `O_NOFOLLOW`; a link at those paths is refused). On
Debian and Ubuntu `dpkg-statoverride` keeps these modes through package
updates; on RHEL-family systems the agent applies them again after every
package install or update it runs, and the panel's scheduler checks every
five minutes. The allowed accounts are the system accounts of websites
owned by administrators, or by accounts whose plan has FFmpeg (and whose
reseller's plan has it too), not suspended; an account shared with an
owner without FFmpeg is left out. Members removed from the group get the
PHP-FPM service of their websites' PHP version reloaded, so no running
worker keeps the group. Limitation: this controls the server's FFmpeg; a
user who uploads a program of their own can run it (WhaCP does not mount
website folders `noexec`). Uninstalling WhaCP gives the programs back to
root with mode 0755 and removes the group.

## Website files: disk usage, directory listing, permissions

- **Disk usage** (`sites.disk_usage`) is an agent job: it measures each
  folder directly inside the website folder (or the chosen folder below
  it) without following links, for up to 30 minutes, and at the top level
  adds the domain's mailboxes. The panel keeps the five latest results per
  folder (`disk_usage_scans`).
- **Directory listing** writes `autoindex on;` into the website's nginx
  server block; the setting is only sent to the agent when on.
- **Permission repair** (`sites.permissions`, `dry_run` for the check)
  walks the website folder with directory descriptors (`openat` with
  `O_NOFOLLOW`, one filesystem) and changes the open descriptor, so links
  are never followed. Owner: the website's account; group: the account's
  or the web server's (a foreign group becomes the web server's inside the
  document root, the account's elsewhere); `o+w` and set-ID bits on files
  are removed; the owner gets read (and enter, for folders). Nothing else
  is changed. The SELinux context is restored after a repair.

## PHP versions and settings

*PHP* lists the installed versions; each has a **Settings** page with three
tabs (permission: manage PHP).

- **Extensions:** install, remove, switch on or off. Install and remove use
  the package manager as a job; before removing, WhaCP simulates it and
  refuses when other packages would be removed with it. Switching uses
  `phpenmod`/`phpdismod` (Debian, Ubuntu) or comments the module's line in
  `php.d` (RHEL family), for PHP-FPM and the command line alike. Extensions
  the panel itself needs are protected on the PHP version the panel runs on.
- **php.ini:** a form for common settings and a text editor for the same
  file. Settings are written to WhaCP's own `99-whacp.ini` (in `conf.d` of
  PHP-FPM and the command line, or in `php.d`), loaded after the
  distribution's `php.ini`, which is never changed. Only listed settings are
  accepted (no `extension`, `auto_prepend_file`, `open_basedir`,
  `disable_functions` and similar); values are type-checked. Settings each
  website sets itself (memory limit, upload size, …) stay per website.
- **PHP-FPM:** server-wide settings (emergency restart, process control
  timeout, `process.max`, log level) in `whacp-global.conf` in the pool
  folder.

Every change is checked with `php-fpm -t` (PHP's warnings count as errors)
and applied with a reload; a change that fails is undone. Each website's
own pool (process manager `ondemand`/`dynamic`/`static`, process counts,
idle timeout, requests per process, request timeout, slow log) is under
Website → *PHP & settings* → *Advanced PHP-FPM*; the defaults are unchanged.

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
mail to `/var/vmail/.whacp-trash` (deleted websites' files go to
`.whacp-trash` in the websites folder): root-only folders on the same disk
as the data, emptied by the administrator.

Mail apps use the full address as user name, IMAP on port 993 (TLS) and
SMTP submission on port 587 (STARTTLS, sign-in required). Port 25 accepts
mail for hosted domains only and offers no sign-in; only this machine may
relay. Signed-in users can send only as their own address or an alias that
delivers to them. Outgoing mail is DKIM-signed.

**Spam filter:** *Email → Spam filter* installs and turns on Rspamd as a
milter in front of Postfix. Mail scoring above the *Junk* score gets
`X-Spam: Yes` and is filed into the mailbox's Junk folder by a Sieve rule
that runs before personal filters; mail above the *Refuse* score is
rejected while the sender is connected, so it bounces rather than
disappearing. Senders cannot set the spam header themselves. WhaCP writes
only its own files in `/etc/rspamd/local.d` (existing files there are
replaced only after confirmation), validates with `rspamadm configtest`
and restores everything if Rspamd or Postfix does not come back.

**Webmail:** *Email → Webmail* installs Roundcube from the distribution's
packages (its own database setup is skipped) and turns it on for a host
name, e.g. `webmail.example.com` or the mail host name; point its DNS at
this server. Users sign in with their full address and mailbox password;
Roundcube reads mail from Dovecot (993) and sends through Postfix's
submission port (587) as the signed-in user, so the usual sender checks
and DKIM signing apply. WhaCP does not edit Roundcube's package files: its
configuration lives in `/etc/whacp-webmail` (read through
`ROUNDCUBE_CONFIG_DIR`), its SQLite database and temporary files in
`/var/lib/whacp-webmail`, and it runs in its own PHP-FPM pool under its own
account (`whacp-webmail`), restricted with `open_basedir`. The Nginx site
(`whacp-webmail.conf`) serves only Roundcube's public folder over HTTPS and
runs only `index.php`. A Let's Encrypt certificate for the host name is used
when one exists, otherwise a self-signed one; issue the certificate and save
again to switch. Every change is checked with `nginx -t` and `php-fpm -t`,
the database is created or upgraded (after a Roundcube update, save again),
and the sign-in page is loaded before the change is kept; otherwise the
previous configuration is restored. Turning webmail off removes the site
and the pool but keeps users' settings and address books. The domain page
lists the settings for mail apps (IMAP 993, SMTP 587) and the webmail
address. Roundcube 1.6.11 as packaged in Ubuntu 26.04 declares its own
`array_first()`, which PHP 8.5 has built in; on PHP 8.5 and later WhaCP
turns the built-in off for Roundcube's pool only (`disable_functions`), so
Roundcube's own, equivalent function is used. Websites' PHP is not changed.

 Dovecot 2.3 (Ubuntu 22.04 and 24.04) and 2.4
(Ubuntu 26.04) are supported; WhaCP writes the matching configuration
syntax, and mail is stored the same way (Maildir, INBOX at its root).

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
automatically), NS, SRV, CAA and, in reverse zones (`.in-addr.arpa` /
`.ip6.arpa`) only, PTR; secondary servers can be allowed to transfer the
zone and are notified of changes.

The zone page lists the records with search, a type filter and sorting by
name, type, value or TTL. Records are added and edited in one form that
shows the fields of the chosen type: SRV from service, protocol, priority,
weight, port and target; CAA from flags (0 or 128), tag (`issue`,
`issuewild`, `iodef`) and value. Common records (A, `www` CNAME, MX, SPF,
CAA for Let's Encrypt) are one click away. Before anything reaches BIND the
panel refuses a CNAME next to other records of the same name, a CNAME at @
and duplicates. It also warns about common mistakes without refusing them: fewer
than two NS records at @; an MX, NS or SRV target that is a CNAME; a target
without the final dot that names nothing in the zone; private or reserved
addresses; TTLs under five minutes; several SPF records; mail (MX) without
SPF; and an SPF record ending in `+all`.

Records can be turned off without being deleted: a turned-off record stays
in the list but is left out of the served zone. Several selected records
can be turned on, turned off or deleted at once (with confirmation).
*Export* downloads the zone file as served (BIND format). *Import* takes a
zone file (pasted or uploaded, up to 1 MB) as most DNS hosts export it; the
agent parses it (`$ORIGIN`, `$TTL`, parentheses and quoted strings are
understood; `$INCLUDE` and `$GENERATE` are not). Records that are not in
the zone yet are added. The SOA and the name servers at @ stay as WhaCP
manages them. Lines that are unsupported, outside the zone, invalid or in
conflict with the zone's records are listed with the reason and left out.

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

Rows can also be searched in every column at once (the text is matched
literally). The side bar shows the database's size, character set and
collation, and creates tables. The *Structure* tab shows the table's status
(engine, row count, data, index and free size, next auto-increment value,
row format, collation, dates) and changes the structure from forms, never
from typed SQL:

- columns: add, change (name, type from a fixed list, length or ENUM/SET
  values, unsigned, NULL, default, auto-increment, on-update timestamp,
  comment, position) and drop;
- indexes: index, unique, full text and primary key; add and drop;
- foreign keys (InnoDB): add, with ON DELETE / ON UPDATE rules, and drop;
- the table: rename, optimize, analyze, check, repair, empty and drop.

Each form can show the SQL it will run before running it. Emptying and
dropping ask for confirmation (dropping asks for the table's name).

*Export CSV* writes the table, or the rows matching the current filter and
search, as CSV (up to 200,000 rows; NULL as `\N`, binary values as `0x…`).
*Import CSV* reads a file of up to 16 MB (comma, semicolon or tab). Each
of its columns is mapped to a table column or skipped, and duplicates of a
unique key can stop the import, be skipped or replace the existing rows.
The rows are inserted in one transaction: either all of them or none.

The SQL console highlights the SQL, keeps the last 30 statements run for
each database (in this browser only, in its local storage; *Clear history*
removes them), shows results 100 rows per page and exports each result as
CSV. The whole database is exported and imported as SQL on the
*Databases* page.

Nothing runs as the database root user. Each database has its own browser
account (`whacp_b_…@localhost`) with privileges on that database only; it
is locked between requests and gets a new random password for each one, so
SQL typed in the console cannot read other databases, change server
settings or read files. Statements are limited to 30 seconds and results to
1,000 rows. Rows can be edited in tables that have a primary or unique key.
Views and routines created from the console are owned by the browser
account, which is removed together with the database. Changes (SQL, saved
and deleted rows, structure changes, CSV imports) are recorded in the
audit log with the statement or the column names, never the row values.

## Redis and Memcached

*Redis → Browse keys* scans a database with a glob pattern and shows every
type (strings up to 1 MiB, the first 500 elements of collections, recent
stream entries); keys and values can be created, edited, given a TTL,
renamed and deleted. Binary data is shown as base64 and not edited. On a
website, *Redis* opens the same browser limited to that website's key
prefix (`<account>:`), which is also what its Redis ACL user may access.

RHEL 10, AlmaLinux 10 and Rocky Linux 10 ship Valkey instead of Redis
(package `valkey`, `valkey.service`, `/etc/valkey/valkey.conf`). It speaks
the same protocol, so WhaCP manages it on the *Redis* pages and websites
use it like Redis. On Redis before 6.2 (Ubuntu 22.04) ACL users cannot be
limited to Pub/Sub channels; WhaCP says so when it creates one.

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

Backups and restores keep 512 MiB free on every disk they write to: a
backup or download stops (and removes its partial files) before the disk
gets fuller than that, and a restore checks first that the website's and
MySQL's disks have room for the restored data (estimated from the sizes
before compression, which backups record since 1.0.0), changing nothing
otherwise.

Server backups contain `/etc/whacp` (agent token, panel key) and TLS
private keys, so they are always encrypted with the backup key, also when
kept only on this server. Without that key they cannot be restored: keep a
copy of `/var/lib/whacp-agent/backup.key` off the server. Server backups
made unencrypted by earlier versions can still be restored.

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

**Recovering on a new server.** Keep a copy of
`/var/lib/whacp-agent/backup.key` off the server: nothing can be recovered
without it.

1. Install the same (or a newer) WhaCP version with `--enable-writes`.
2. Before making any backup there, copy the old key to
   `/var/lib/whacp-agent/backup.key` (owner root, mode 0600).
3. Add the same remote storage (same sub-folder) and press **Find
   backups** on it. WhaCP lists the storage and adds the backups it can
   trust; it says which it skipped and why.
4. Restore the server backup (configuration) if you want it, then create
   each website again (same domain) and press **Find backups** once more:
   website backups are added for websites that exist. Restore them from the
   website's *Backups* tab.

Only backups encrypted with this key and made by WhaCP 1.0.0 or later are
added: their manifest is authenticated with the key, so someone who can
write to the storage cannot plant a backup, move one to another website or
change its files. Unencrypted backups and older ones are listed as
skipped.

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

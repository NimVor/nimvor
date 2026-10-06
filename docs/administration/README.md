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
sudo -u whacp -E php /opt/whacp/panel/bin/whacp user:verify-ip alice 203.0.113.5
sudo -u whacp -E php /opt/whacp/panel/bin/whacp hulk:unblock 203.0.113.5
sudo -u whacp -E php /opt/whacp/panel/bin/whacp agent:check
sudo -u whacp -E php /opt/whacp/panel/bin/whacp discovery:run
sudo -u whacp -E php /opt/whacp/panel/bin/whacp schedule:run   # normally run by whacp-scheduler.timer
sudo -u whacp -E php /opt/whacp/panel/bin/whacp doctor
```

## Navigation and dashboards

- **Global search:** press **Ctrl+K** (⌘K on a Mac) or click *Search tools*
  in the top bar, type a few letters and press Enter to open the first
  match. It lists quick actions (create a website, a hosting account or a
  panel user) and every area the signed-in user may open; areas a role or
  plan does not allow are not listed.
- **Administrators' dashboard:** a welcome strip with the server's name,
  uptime, alerts and quick actions; live CPU, memory, disk and load; server,
  services, websites, network and recent activity; and *Tools & settings*,
  every area grouped as in the sidebar.
- **Resellers' dashboard:** their customers (plan, websites, status), their
  websites, plan use (limits with bars, unlimited resources listed) and
  recent notifications, with quick actions. It shows nothing about the
  server itself.
- Hosting customers keep the simple layout (their websites, no sidebar).

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

## Converting an addon domain into an account

On a website's *Subdomains & addons* page, administrators and resellers
(*Manage users*) can use **Convert to account** on an addon domain. It
becomes a hosting account of its own: a new login (a hosting customer
with a plan) and a new system account with the same name, which must not
exist yet.

Nothing is copied:
- **Files:** the website folder is re-owned in place, links are never
  followed, and hard links to files of another account are left alone.
- **Subdomains:** subdomains of the addon domain go with it.
- **Databases:** they change owner, and so do database users that only
  they use.
- **DNS and email:** the DNS zone and the email domain change owner, with
  the mailboxes.
- **Scheduled work:** cron jobs and workers now run as the new account.
- **FTP:** FTP accounts move with their passwords.

The confirmation window lists all of this and asks you to type the
domain. If a step on the server fails, the websites go back to the old
account and the new login is removed. The system account already created
on the server is left without websites; remove it by hand if you do not
retry.

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
- **Bandwidth:** each website's data sent is read from its access log on
  every scheduler pass (every 5 minutes). An account that sends more than
  its plan's monthly bandwidth (a reseller's limit covers its customers'
  websites too) is blocked: all its websites answer
  "509 Bandwidth limit exceeded", except `/.well-known/acme-challenge/`, so
  certificates still renew. The account and its reseller get a
  notification, the block is audited (`account.bandwidth_block`), and the
  website dashboard says why. The block ends by itself when the month
  ends, or at once when the plan or an override allows more or an
  administrator presses "Reset" (Users → Bandwidth over limit), which sets
  this month's counter to 0; log positions and earlier months are kept.
  Administrators' websites are never blocked. Only web traffic counted in
  the access logs is measured (not FTP, email or database traffic).
- **Terminating accounts:** select hosting accounts on the Users page and
  choose "Terminate selected". The confirmation lists what will be removed
  and requires typing TERMINATE. Each account's websites (files and system
  account), mailboxes, databases, database users and DNS zones are removed
  through the same code as deleting them one by one; if a step fails, that
  account is kept with what is left and the others continue.

## Disk quotas

A plan's disk space is enforced with Linux user quotas once an
administrator turns them on: *Server → Resource limits → Disk quotas →
Turn on disk quotas* (once per server). The websites' file system must be
ext4 or XFS.

- **ext4 (Ubuntu, Debian):** the file system is remounted with journaled
  user quotas (`usrjquota=aquota.user,jqfmt=vfsv1`, no downtime), every
  file is counted once with `quotacheck` (slow on a large disk), quotas
  are turned on and the same options are added to its `/etc/fstab` entry
  so they stay on after a reboot. The `quota` package is installed if
  missing; on kernels without the quota module (some cloud images) the
  matching `linux-modules-extra` package is installed. If a step fails,
  `/etc/fstab` is not changed.
- **XFS (AlmaLinux, RHEL):** XFS starts counting quotas only when it is
  mounted, so `uquota` is added to `/etc/fstab` (and, for the root file
  system, `rootflags=uquota` to every kernel with `grubby`). Quotas start
  after the next reboot; the page shows "After the next reboot" until then.

While quotas are on, the scheduler (every 5 minutes, and at once after a
plan, override or website change) reads each website system user's use
and sets its hard limit. An account's disk space is shared by its system
users: each may grow by an equal part of what is left, re-divided on every
pass, so together they never pass the limit. A reseller's disk space
covers its customers' users too. Administrators and accounts without a
limit are not limited. Writing beyond the limit fails with "Disk quota
exceeded". Files owned by the account's system users on the websites' file
system count; email (stored as the mail system's user) and databases do
not. The plan usage shown to accounts then comes from the quota.

The agent does not hold `CAP_SYS_ADMIN`, which quotas need. `setquota`,
`repquota`, `quotaon`, `quotacheck` and `mount` (remount only) run as root
in a short-lived systemd unit started by PID 1 whose capability bounding
set is only `CAP_SYS_ADMIN` (plus `CAP_DAC_READ_SEARCH` for `quotacheck`),
with `NoNewPrivileges`. No other command can be started that way.

## Resource limits (CPU, memory)

A plan can limit its accounts' CPU (100 % = one core), memory, number of
processes and threads, and disk read and write speed (MB/s, on the
websites' file system); 0 means no limit. A reseller's customers get at most
what the reseller's own plan allows. Administrators' websites are not
limited. *Server → Resource limits* lists the limited accounts with their
current memory use, processes, CPU time lost to the limit and
out-of-memory kills; *Apply the plans now* re-applies everything.

WhaCP uses systemd and cgroup v2 (Ubuntu 22.04+, Debian 11+, RHEL/AlmaLinux
9): each limited account gets a slice, `whacp-acct<ID>.slice` (under
`whacp.slice`), with the plan's limits. It is shared by everything the
account runs:

- PHP: the account's websites get their own PHP-FPM master per PHP version,
  `whacp-fpm-<user>-<version>.service` (configuration in
  `/etc/whacp-fpm/`), which runs in the slice; each PHP process and
  whatever it starts is counted. Accounts without limits keep using the
  distribution's PHP-FPM service.
- Cron jobs run through `systemd-run` in the slice, as the account. Root's
  cron line only names the job's script, `/etc/whacp-cron/…`, which only
  root and the account can read. Personal crontabs and `at` jobs of limited
  accounts are refused (`/etc/cron.deny`, `/etc/at.deny`), because they
  would run outside the slice.
- Workers and commands run for the account from the panel (such as
  *Run now*, deployments and installers) run in the slice.
- SSH and SFTP logins are placed by systemd-logind in `user-<uid>.slice`;
  WhaCP gives it the same limits (a separate budget from the websites').

An account cannot leave its slice: moving a process to another cgroup
needs permissions (cgroup delegation) that accounts do not get. The limits
are unit files (`/etc/systemd/system/whacp-acct*.slice` and
`user-*.slice.d/50-whacp-limits.conf`), so they apply after a reboot, and
they are also applied to the running slices at once. They follow the
account when its plan or the plan's limits change (upgrade or downgrade),
when websites are added or removed and when it is suspended or restored,
and the scheduler checks every few minutes that the server matches the
plans. Removing the limits moves PHP back to the shared service.

Network speed is not limited this way: cgroup v2 has no network
controller, and shaping by account would need changes to the network
interface's queueing that WhaCP does not make. The plans' monthly
bandwidth (data transfer) is enforced instead; see *Plans, limits and
bandwidth*.

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

## Server security settings

All under **Security** in the sidebar (administrators).

- **Firewall**: the page lists the ports WhaCP keeps open. *Turn on
  firewall* adds allow rules for SSH and the panel's HTTPS port (always),
  the running services' ports (option) and every port from your current
  address (option; it is also added to the cPHulk whitelist), then turns
  the firewall on (UFW, or firewalld on RHEL-family systems). Confirm with
  **Keep change** within 2 minutes; otherwise the firewall is turned off
  and the added rules are removed, so a mistake cannot lock you out.
  The last rule opening SSH or the panel cannot be deleted while the
  firewall is on.
- **Configure Security Policies**: *Limit logins to verified IP
  addresses* (a WhaCP sign-in from a new address needs a 6-digit code sent
  to the user's email; addresses already used count as verified;
  `bin/whacp user:verify-ip <user> <ip>` verifies one from the shell),
  two-factor authentication required per role, password strength on/off
  and password age (days; 0 = off).
- **Password Strength Configuration**: minimum strength (0–100, 65 or more
  is reported as strong) and minimum numbers of total, special, numeric,
  alphabet, upper case and lower case characters. It applies to WhaCP
  users, mailbox passwords typed in (webmail) and the root password.
- **cPHulk Brute Force Protection**: failed logins are read every minute
  from the journal (sshd, Dovecot, Postfix SMTP AUTH, pure-ftpd) and
  Roundcube's log, and counted at once for WhaCP sign-ins. Too many for an
  account lock its WhaCP sign-ins; too many from an address block it from
  WhaCP and the login ports (SSH, 21, 110, 143, 465, 587, 993, 995, 4190),
  or from every port with the firewall-level options. Blocks live in the
  nftables table `inet whacp_hulk` and expire by themselves; the scheduler
  restores the table after a restart. Commands run as the panel's account
  (not root) with a 15-second limit and shell-quoted variables. Countries
  use DB-IP's IP to Country Lite database (CC BY 4.0), downloaded monthly;
  without Internet access import it with `bin/whacp hulk:geo-import FILE`.
  If you block yourself: `bin/whacp hulk:unblock <ip|user|all>`. When
  protection is first turned on with an empty whitelist, your own address
  is added to it.
- **Compiler Access**: compilers and the assembler become mode 0750, group
  `whacp-compiler`, with the accounts you allow as members (ModSecurity
  source builds keep working). Restored exactly when turned off.
- **SMTP Restrictions**: nftables table `inet whacp_smtp` (loaded at boot
  by `whacp-smtp-restrict.service`) lets only root, the MTA, Mailman and
  the panel connect to remote ports 25, 465 and 587.
- **Shell Fork Bomb Protection**: `/etc/security/limits.d/90-whacp-forkbomb.conf`
  caps processes of accounts (UID 1000 and up) in login sessions.
- **Traceroute Enable/Disable**: traceroute and tracepath for root only
  (mode 0700) or everyone.

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

## Web terminal

A shell in the browser, on the website's **Terminal** page (and a tile on
customers' website pages). It is available when:

- the panel is served by Nginx over HTTPS (`install.sh --public-https`),
  which proxies `/terminal/ws` to the agent's loopback listener
  (`terminal_listen` in `/etc/whacp/agent.json`, `127.0.0.1:8091`); with
  only the SSH tunnel to the panel there is no web terminal;
- the agent is in write mode;
- the website's account has shell access (jailed or normal, above) with
  its SSH/SFTP login on, and the owner's plan includes "SSH shell & web
  terminal".

**Sign-in.** A website's terminal signs in as that website's Linux
account (for example `shop`) with the account's own password: the one in
`/etc/shadow`, which is also its SSH and SFTP password (set on the FTP /
SFTP page, *Also allow password login*). There is no separate terminal or
SFTP password: WhaCP hashes the password into `/etc/shadow` with
`chpasswd -e` and never stores it, and a password changed on the server
(`passwd`) works too.

1. The panel checks who may open which account (website owner, plan
   feature "SSH shell & web terminal", shell access jailed or normal,
   account not suspended) and asks the agent for a single-use ticket,
   valid for a minute and bound to the browser's address.
2. The browser opens the WebSocket (its Origin must be the panel's own
   host) and sends the ticket with the password. The password goes to the
   agent only; the panel never receives, stores or logs it.
3. The agent checks the password with PAM's `unix_chkpwd` (password on
   standard input, never in a command line), then that the account has
   not expired. Locked (password login off) or suspended accounts are
   refused.
4. Wrong passwords: three tries per ticket, then the connection closes.
   Five wrong passwords within 15 minutes, per account or per address,
   block sign-in for 15 minutes. Each wrong password waits a second, and
   the agent's log records the account and address, never the password.

Changing the account's password, turning its password login or shell
access off, changing its shell mode, or suspending it ends its open
terminals. An account that belongs to a privileged group (`sudo`,
`wheel`, `adm`, `shadow`, `docker`, `lxd`, `disk`, `systemd-journal`,
`root`…) gets no terminal until it is removed from the group.

Each session is its own systemd unit (`whacp-term-<id>.service`) as the
account, with the sandbox of other commands run as the account
(no_new_privs, no capabilities, read-only system folders), in its
resource limits slice, with at most 512 processes. A jailed account gets
its bubblewrap jail; a normal one bash. At most 3 sessions per account and
20 in all. A session closes when the shell exits, the tab closes, after
the idle time without input (Settings → Security, default 15 minutes), at
the maximum duration (default 8 hours), when its sign-in signs out, or
from *Server → Web terminal*, where administrators see every open session
(account, who opened it, address, times, traffic) and can close it. The
panel's audit log records confirmations and openings, the agent's log
starts and ends; keystrokes are not recorded, since they include
passwords typed at prompts.

**Root shells** are off. A root shell in the browser means that whoever
takes over an administrator's sign-in controls the server, so only the
server's root user can allow it: `"terminal_root": true` in
`/etc/whacp/agent.json`, then `systemctl restart whacp-agent`. The root
shell (Server → Web terminal) has no sandbox and, since root usually has
no password, asks for the administrator's panel password (and
authenticator code) again instead; hosting accounts can never open it.

**What a website account's session gets** (checked by the real-host
tests): its own UID and groups, `NoNewPrivs: 1` and every capability set
(`CapInh`, `CapPrm`, `CapEff`, `CapBnd`, `CapAmb`) empty, so `sudo` and
`su` cannot raise privileges; its account's resource limits slice and at
most 512 processes; the normal filesystem permissions (other accounts'
website folders are `0710`, their files unreadable) or, jailed, only its
own website folder and a read-only system.

The terminal page allows style elements that carry a per-response nonce
(xterm.js sets colours and sizes that way); scripts stay `'self'` only.

## ModSecurity (web application firewall)

*Security → ModSecurity* (administrators). It needs the Nginx connector
(libmodsecurity 3 with the ModSecurity-nginx module) and the OWASP Core
Rule Set.

**What is installed is detected by what it does, not by package name.**
The connector is *missing*, *installed but not usable* (not loaded, a
library missing, built for another Nginx — the page says which) or
*working*, whether it came from a package, an rpm, a module built from
source or a file copied by hand. WhaCP reads `nginx -V` (version,
modules folder, `--with-compat`, modules built in), the active
configuration's `load_module` lines, resolves the module's libraries like
the dynamic linker (RUNPATH, `/etc/ld.so.conf`) and test-loads a module that
is present but not loaded with a throwaway configuration. The same applies
to the Brotli module and to programs such as FFmpeg on the Packages page;
a package is never installed for something that already works.

**Install ModSecurity** (one background job, one transaction):

1. Detects the OS, Nginx and an existing connector. A working connector is
   left alone (*Upgrade connector* replaces one WhaCP built; a packaged one
   is updated with the package manager).
2. Before any change, copies `/etc/nginx`, `/etc/modsecurity`, the CRS
   setup folder, `/etc/ld.so.conf(.d)`, `/usr/share/nginx/modules` and
   WhaCP's ModSecurity state into the transaction's folder
   (`/var/lib/whacp-agent/transactions/modsec/<id>/snapshot`), and records
   Nginx's version and build options, package versions and every file it
   will change.
3. Uses the distribution's package when a dry run (`apt-get -s` /
   `dnf --assumeno`) shows it would not remove or replace Nginx itself
   (for example with nginx.org's Nginx it would, so it is not used).
   Otherwise it builds the official connector (1.0.4) for the exact
   installed Nginx: from that version's own source — the distribution's
   signed source package (apt or dnf), or for an Nginx no package
   installed, nginx.org's release checked against a SHA-256 you enter —
   with libmodsecurity from its package or built from the pinned 3.0.14
   release (both releases SHA-256 verified). Compilers and development
   packages are installed; the build runs as a temporary unprivileged
   account (`whacp-build`), removed afterwards. Nginx must be built with
   `--with-compat`. **The Nginx binary is never installed, replaced or
   upgraded.**
4. Installs new, versioned files only
   (`/usr/local/lib/whacp/nginx-modules/`,
   `/usr/local/lib/whacp/modsecurity/<version>/`), checks that the
   module's libraries resolve and that Nginx loads it, then switches the
   `load_module` line (`modules-enabled/50-whacp-modsecurity.conf`, the
   folder nginx.conf includes; nginx.conf only gets an include line when it
   has no module folder), runs `nginx -t`, reloads, and checks again.
5. If **any** step fails — dependencies, download, build, libraries, module
   load, `nginx -t`, reload — everything is rolled back: files restored
   byte for byte with owner and mode, new files removed, packages it added
   removed, the build account removed, the previous configuration
   reloaded. The previous module and configuration are never deleted
   before the new ones have passed. If the agent stops in the middle, the
   next start rolls the unfinished installation back. Running it again is
   safe.

*Installation* on the page lists every installation and rollback with its
sources, versions and errors. **Roll back** undoes the installation in
effect (the newest; roll back in order) as a transaction of its own: its
files are put back, packages it added removed, then `nginx -t` and a
reload; if Nginx would not accept the result (for example while
ModSecurity is on and the connector would go), nothing changes. From the
command line: `whacp modsec:installs` and `whacp modsec:rollback <id>`.
Records are kept for *Settings → ModSecurity installation backups*
(default 30 days); the one in effect is always kept. If a rollback itself
was incomplete, the page names the snapshot folder for manual recovery.

**Install Core Rule Set** (separately, also a transaction): the
distribution's `modsecurity-crs` package, or the pinned CRS 4.18.0
release (SHA-256 verified; only rule and data files are unpacked, into
`/usr/local/lib/whacp/coreruleset/<version>/`). It is turned on in
**detection only**: websites set to *On* only log what they would block
until you turn *Detection only* off in the server-wide settings. New
installations start in detection only; configurations from before this
setting keep blocking.

- **Server-wide:** ModSecurity on/off; the Core Rule Set with its paranoia
  level (1–4) and inbound/outbound anomaly thresholds; custom rules on/off.
  The rules are loaded once at the http level
  (`whacp-00-modsecurity.conf`, `/etc/nginx/whacp-modsec/`) with
  `modsecurity off`, so nothing is filtered until a website turns it on.
- **Rule vendors:** the OWASP Core Rule Set (IDs 900000–999999, updated
  with its package: *Update rules* runs the package update, checks Nginx
  and, if the new rules do not load, turns the Core Rule Set off so
  websites keep working) and **custom rules** (IDs 10000–99999). Custom
  rules may use only rule directives (`SecRule`, `SecAction`,
  `SecMarker`, `SecRuleRemove…`, `SecRuleUpdate…`); `exec`, file operators
  and includes are refused. Commercial rule sets need their vendor's
  licence and are not bundled.
- **Per website:** off, detection only (log, do not block) or on (block
  with 403); which vendors apply (a vendor is left out by removing its ID
  range); single rules turned off. The website's vhost includes
  `/etc/nginx/whacp-modsec/sites/<domain>.conf` through a glob, which
  matches nothing while ModSecurity is off for it; existing vhosts get
  the include line when ModSecurity is first turned on.
- **Events:** blocked and detected requests from the audit log
  (`/var/log/nginx/whacp-modsec-audit.json`, parts `ABHZ`: no request or
  response bodies; the panel shows only time, host, address, request line,
  result and matched rules). From an event an administrator can turn a
  rule off for that website. The scheduler keeps the log under 50 MiB.

Every change is checked with `nginx -t` and rolled back if Nginx refuses
it or does not reload. A website's owner sees its **Web firewall** page and
may switch it between on, detection only and off; vendors, rules and the
server-wide settings stay with administrators.

## WordPress Toolkit

*WordPress* in the sidebar lists the installations in the websites you
manage; each website has a **WordPress** page. Hosting plans turn it off
with the **WordPress Toolkit** feature (Feature Manager).

- **Finding installations:** *Scan* looks for WordPress (a folder with
  `wp-settings.php` and `wp-includes/version.php`) up to four levels
  below each website folder. It runs PHP as the website account and only
  reads `version.php` and `wp-config.php` as text: WordPress is not
  loaded and the database password is never read. An installation is
  listed under the website whose document root holds it, including
  subdomains. The scan also asks WordPress.org for the current release,
  so *Updates available* lists outdated installations.
- **Overview and checks:** version, PHP, address, database (name, tables,
  size), administrators, plugins and themes with their updates. The
  security and health checks cover:
  - outdated core, plugins or themes, and an unsupported PHP version;
  - debugging on, or the dashboard file editor on;
  - `wp-config.php` readable by others, or a user called `admin`;
  - no HTTPS, or an `http://` site address;
  - automatic updates off, inactive plugins, maintenance mode, hidden
    from search engines;
  - WordPress.org not reachable.

  Most checks have a *Fix* button.
- **Management:**
  - core, plugin and theme updates (background jobs, notified when done);
  - plugins: activate, deactivate, automatic updates on or off;
  - themes: activate, automatic updates on or off;
  - automatic core updates: security releases, all releases or off;
  - maintenance mode;
  - hardening: file editor off, debugging off, `wp-config.php` to 0600;
  - search-engine visibility;
  - core file verification against WordPress.org checksums.
- **Log in to WordPress:** a one-time sign-in link for a chosen
  administrator. The agent writes a random 128-bit-named file into the
  installation, as the account, mode 0600. It works for one minute,
  deletes itself on first use, and the agent removes it if unused. The
  link is built from the website's own domain (never from WordPress's
  settings) and needs HTTPS on the website.

The installation page is organised like other WordPress toolkits:
a header with the version, security, vulnerability and update status and a
tool strip (**File Manager** opened at the installation's folder,
**Database** in the database browser, **Logs**, **Clone**, **Sync**,
**Security**, **Back up**, **Restore points**), then tabs:

- **Dashboard** switches: search engine indexing, **debugging** (WP_DEBUG,
  WP_DEBUG_LOG, WP_DEBUG_DISPLAY, SCRIPT_DEBUG, SAVEQUERIES each),
  maintenance mode, **password protection** (HTTP authentication for the
  installation's address; the password is stored only as a SHA-512 crypt
  hash; certificate renewal keeps working), **take over wp-cron.php**
  (DISABLE_WP_CRON plus a cron job every 5 minutes running `wp cron event
  run --due-now` as the website account, listed under Cron jobs),
  **hotlink protection** (images, video, audio and PDF only served to the
  website's own pages, direct visits and allowed sites) and
  **vulnerability protection** (below). **Change administrator password**
  sets a WordPress administrator's password and signs out their other
  sessions.
- **Security:** twelve hardening measures, each applied and **rolled back**
  on its own; *Secure (recommended)* applies nine at once. Web server rules
  (Nginx, checked with `nginx -t`, rolled back on failure):
  - sensitive files blocked (wp-config.php, readme.html, license.txt, the
    installer, dumps and logs);
  - xmlrpc.php blocked (not in the recommended set: Jetpack and mobile
    apps use it);
  - author scans blocked;
  - no PHP in wp-content/uploads or wp-includes;
  - known attack tools refused.

  WordPress settings, for which the toolkit records the value it replaced
  and puts it back exactly on rollback:
  - file editor off, pingbacks off, script concatenation off, debugging off;
  - wp-config.php 0600;
  - new security keys (these cannot be rolled back).

  **Vulnerabilities** compares WordPress, the plugins and the themes with
  the public WPVulnerability database (only names and versions are sent;
  answers cached for 12 hours). *Vulnerability protection* puts the
  website behind ModSecurity with the OWASP Core Rule Set in blocking mode
  (ModSecurity must be installed and on; while *Detection only* is on
  server-wide it only logs) and checks the installation every day,
  notifying new findings.
- **Database:** name, size, *Open in database browser* and *Change database
  password* (a new random password set by the database user itself and
  written to wp-config.php in one step; shown once; the panel's stored copy
  is updated).
- **Staging & restore:** *Clone* copies files and database to another
  website of the same owner (for example a staging subdomain), with a new
  database, the addresses replaced and search engines discouraged. *Sync*
  copies files and/or the database from one installation to another of the
  same owner (the target keeps its wp-config.php and address): open the
  staging copy and sync it to the live site to publish. Before anything is
  replaced a **restore point** (files and database) is saved in
  `/var/backups/whacp/wordpress/<domain>/`; the newest five are kept and
  can be put back from the tab. Databases are always the panel's records
  of the owner's databases, never what a wp-config.php names.
- **Logs:** the end of WordPress's debug log, with a link to the website's
  web server and PHP logs.

**Automatic hardening:** *Settings → Security → Harden new WordPress
installations automatically* (on by default) applies the recommended
measures to WordPress installed with the app installer, once its
installation job has finished.

How commands run:
- **WP-CLI:** commands use WP-CLI 2.12.0. The agent downloads it once from
  GitHub, verifies it against a SHA-512 written into the agent, and keeps it
  at `/usr/local/lib/whacp/wp-cli.phar` (root-owned, 0644). A changed copy is
  not used.
- **Where they run:** as the website account in its slice, with the site's
  plugins and themes not loaded (`--skip-plugins --skip-themes`), so their
  code does not run in these commands. Plugin names are validated and
  never passed as options.
- **Who may change what:** owners and administrators see the website's
  installations; changes need *Create, change and delete websites*. A
  suspended account's WordPress can be viewed but not changed. Every
  change and sign-in link is recorded in the audit log (without the
  link).

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

**Trash.** Deleting moves files and folders into the website's trash
(`.whacp-trash` in the website folder, owned by the website's account),
unless *Delete permanently instead* is ticked. *Trash* in the toolbar lists
what was deleted, from where and when; *Restore* puts items back where they
were (as "name (restored).ext" when that name is taken, recreating missing
folders), *Delete permanently* and *Empty trash* remove them for good after
a confirmation. Items are deleted automatically after the number of days in
*Settings → File Manager trash* (default 30; 0 keeps them until the trash is
emptied), checked daily after 03:00. Trashed files still count towards the
account's disk space, are not included in backups and cannot be reached
from the web (they are outside the document root). The file manager itself
never shows or accepts paths inside `.whacp-*` folders, and restores
re-check the recorded location, which must stay inside the website folder.

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

## IP addresses

*Server → IP addresses* (server administrators) manages the addresses
websites listen on. A website listens on every address of the server
unless it is bound to addresses from the pool (the website's *Advanced*
page, *IP addresses*). Its Nginx configuration then listens on those
addresses only, and is checked with `nginx -t` before the reload.
Certificates are unaffected: HTTPS is served by name.

WhaCP does not change the operating system's network configuration
(netplan, NetworkManager, ifcfg), because a mistake there can cut the
server off and providers set it up in different ways. An IPv4 address
must already be configured on an interface before it is added to the
pool; the agent checks that. A website bound to an address the server
does not have is refused before anything is written.

- **Addresses and usage**: the pool with each address's family,
  assignment (*shared* by several websites or *dedicated* to one), whether
  it is *reserved* (with a reason; reserved addresses cannot be
  assigned), the websites and accounts using it, and whether the server
  still has it. It can be searched and filtered. Unused addresses can be
  removed from the pool; the server keeps them. The server's addresses
  that are not in the pool are listed and can be added with one click.
  *Rebuild pool* adds the server's addresses and those websites use that
  are missing, and re-applies the IPv6 ranges. It removes nothing:
  addresses the server no longer has are listed.
- **IPv6 ranges**: a range the provider *routes* to the server (not the
  on-link prefix of its interface), /48 to /128. WhaCP makes its addresses
  local with a local route on the loopback interface, kept at boot by
  `whacp-ipv6-ranges.service`. It refuses ranges that overlap an
  interface's prefix or contain a gateway, since such a route would cut
  the server off from its network. Addresses are allocated from a range
  (the next free one from `::10`, or one given), optionally straight to a
  website with its AAAA records. A range can be removed only when none
  of its addresses is in use.
- **Migration wizard**: choose the address to move from and the one to move
  to, then review the websites (and, if chosen, every DNS record pointing to
  the old address and the default IPv4 for new zones) that change. Confirm,
  and each website is moved on its own, with the results listed per
  website and zone.

When a website's addresses change and *Update DNS* is on, its A (and AAAA)
records in its zone follow: records that pointed to its previous address
(the default IPv4 for new zones when it listened on every address) point to
the new one. Other records, such as SPF, are not changed. If the DNS server
refuses the zone, the website goes back to its previous addresses.

## SSL/TLS

Each website's *SSL* page issues a Let's Encrypt certificate, installs an
uploaded one, renews it, and sets HTTPS redirection and HSTS. *AutoSSL*
(Settings → SSL/TLS) issues certificates for new websites once their domain
points to this server. For server administrators, *SSL* also opens the
SSL/TLS section:

- **SSL hosts**: every host name Nginx serves, whether it has HTTPS and
  redirects to it, and the certificate that answers for it. It flags
  certificates that expire within the warning period, have expired, or do
  not cover the name. It also shows whether Let's Encrypt renewals are
  scheduled, and links to each website's SSL page.
- **Certificates**: every certificate on the server (Let's Encrypt, the ones
  installed through WhaCP, and those other Nginx configurations use, shown
  read only) with names, issuer, validity, SHA-256 fingerprint, chain length
  and the Nginx files that use it. Unused Let's Encrypt and WhaCP
  certificates can be deleted. A private key readable by everyone is
  flagged.
- **Keys and CSRs**: a private key (ECDSA P-256 by default, or P-384, RSA
  2048/3072/4096) and a certificate signing request for up to 100 names
  (wildcards allowed), with organization, city, state, country and email.
  Optionally a self-signed certificate (1–825 days) is made from the same
  key. The key is made and kept on the server (root only, mode 0600) and
  is never shown or downloaded; the CSR can be copied or downloaded. When
  the certificate authority sends the certificate, paste it (with the
  intermediate certificates) under *Install on a website*. The key is taken
  from the server. Deleting a key removes it without keeping a copy in the
  configuration backups.
- **TLS configuration**: the protocol versions (TLS 1.2 and 1.3, or TLS 1.3
  only), the TLS 1.2 ciphers (Nginx's default, or Mozilla's
  "intermediate" list), and session tickets for every WhaCP website and
  webmail. They live in one Nginx snippet (`/etc/nginx/whacp-tls.conf`) that
  each of those virtual hosts includes. A change is checked with `nginx -t`
  and reloaded, and restored if either step fails. Website configurations
  written by earlier versions carry their own TLS lines; the first change
  moves them to the snippet. OCSP stapling is not offered (Let's Encrypt no
  longer runs OCSP). The panel's own HTTPS follows this configuration once
  a certificate is chosen for it under *Services*. The snippet stays when
  WhaCP is uninstalled, because the websites still use it.
- **Services**: the certificates of WhaCP's own services.
  - **Host name:** a Let's Encrypt certificate for the server's host name.
    Nginx answers the challenge on port 80 when no website uses the name.
  - **Panel and mail server:** each can use any Let's Encrypt certificate,
    or go back to the one it used before.
    - The panel's Nginx site is checked with `nginx -t` and reloaded.
    - For the mail server, Postfix and Dovecot are checked with
      `postfix check` and `doveconf -n`, then reloaded.
    - Anything that fails is restored.
  - **Mail server TLS:** the protocols (TLS 1.2 and 1.3, or TLS 1.3 only)
    and ciphers for IMAP and submission. Port 25 keeps accepting what
    sending servers offer.
  - **Kept and renewed:** both choices survive *Set up email* again.
    Renewals reload Postfix and Dovecot as well as Nginx.
  - **cPanel equivalents:**
    - "Apache SSL ciphers" is the TLS configuration (Nginx serves every
      website).
    - "Web Services" is the panel.
    - WhaCP has no Web Disk (WebDAV) service.

Renewal: Let's Encrypt certificates are renewed by certbot's timer
(`certbot.timer` on Debian/Ubuntu, `certbot-renew.timer` from EPEL, which
the package leaves off). WhaCP turns the timer on after issuing a
certificate and when the agent starts with certificates present. *SSL
hosts* also offers a *Turn on renewal* button.

Before any certificate is installed (uploaded or for a stored key), it is
checked: the key matches, the certificate is valid now (not expired, not
valid only in the future), each certificate in the chain is signed by the
next, and it covers the website's domain.

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

## Database server

*Databases → Database server* (server administrators) shows the MariaDB or
MySQL server: version and uptime, connections in use and their peak,
queries per second, slow queries and failed logins, data size and the
numbers of databases and users, the InnoDB buffer pool (use and hit rate),
temporary tables written to disk, open tables and traffic. It also shows
where clients connect (socket, port, listening address, data directory, error log,
TLS). Where the server listens is left to the distribution's option files;
WhaCP does not open the database server to the network.

*Settings* lists a fixed set of options: connections and timeouts, the
largest packet, thread cache, host name lookups, InnoDB buffer pool, redo
log size and flush at commit, temporary and heap table sizes, open tables
cache, slow query log and time, and the default character set and
collation. Values are checked (types, ranges, a collation that belongs
to the character set) and written to WhaCP's own option file
(`zz-whacp.cnf` in `/etc/mysql/mariadb.conf.d`, `/etc/mysql/mysql.conf.d`
or `/etc/my.cnf.d`, read after the distribution's files). Then the server
program itself reads all option files (`mariadbd --help --verbose`); if
it reports an error, the previous file comes back. Options marked *now* are
applied at once with `SET GLOBAL` (if the server refuses one, the file and
the values applied before it are restored); the others take effect at the
next restart. An empty field removes WhaCP's value, so the server's own
applies again.

*Restart* first checks the option files (it is refused while they have an
error), restarts the service and waits up to 90 seconds for the server to
answer. When settings are saved with a restart and the server does not come
back, the previous option file is restored and the server is restarted with
it. *Reload privileges*, *Reopen logs* and *Unblock hosts* run `FLUSH
PRIVILEGES`, `FLUSH LOGS` and `FLUSH HOSTS`.

*Connections* lists every client connection with its user, host, database,
command, time and statement; a statement can be stopped (`KILL QUERY`) or
the connection ended (`KILL CONNECTION`), after confirmation. The server's
own threads cannot be ended. Every change is recorded in the audit log.

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

## Top bar, processes and server tools

For server administrators, the top bar shows:
- the host name, the operating system and the WhaCP version;
- the load averages for 1, 5 and 15 minutes, refreshed every 30 seconds.
  Clicking them opens *Monitoring → Live*.

The buttons next to them:

- **Restart all services** (needs *Manage services*):
  - Restarts every running service WhaCP manages, in this order:
    1. databases and caches;
    2. PHP-FPM;
    3. mail, DNS and FTP;
    4. Nginx, only after `nginx -t` passes;
    5. the agent and the panel.
  - SSH is never restarted.
  - The job's output shows which service failed, if any.
- **Restart the server** (needs *Manage server*): type the host name to
  confirm. A scheduled restart can be cancelled on the Server page.
- **Update** (needs *Manage server*), shown only when a newer version
  exists:
  - **Finding updates:** the installer records its source folder in
    `/etc/whacp/source.json`. Every six hours the scheduler asks GitHub for
    the newest commit of the checkout's branch. A private repository or a
    remote outside GitHub shows "unknown", never "up to date".
  - **When the panel runs the update:** *WhaCP updates* runs
    `installer/update.sh` in its own unit (`whacp-update.service`; output in
    `/var/log/whacp-update.log`), after a recent password confirmation.
  - **Ownership check:** it runs only when root owns the checkout, its
    `.git`, `installer` folder and scripts, and no other account can write
    to them, because the script runs as root. Otherwise the page shows the
    command to run over SSH.

**Monitoring → Processes** lists every process with its owner, priority
(nice), CPU (share of one CPU over half a second) and memory use, running
time and command line.
- **What you can do:**
  - **Trace** shows the program, folders, status and open files and
    connections, plus five seconds of system calls when `strace` is
    installed (Packages → System call tracer).
  - **Stop** sends TERM; **Kill** sends KILL.
  - **End all processes of an account** never accepts root or WhaCP's own
    account.
  - PID 1, kernel threads and the agent itself cannot be signalled.
  - A signal is sent only if the process still has the start time the
    page showed, so a reused process number is never hit.
- **Suspicious processes** are highlighted:
  - names of known cryptocurrency miners or backdoors, and mining pools;
  - programs deleted from disk after they started;
  - programs run from `/tmp`, `/var/tmp`, `/dev/shm` or hidden folders;
  - scripts started from those folders;
  - user programs that call themselves kernel threads;
  - shells and interpreters of website accounts listening on a port.
- **How other accounts' processes are read:** reading another account's
  program, folder and open files needs `CAP_SYS_PTRACE`, which the agent
  does not hold. For those details the agent runs its own binary in a
  read-only scan mode (`whacp-agent -proc-scan` / `-proc-detail <pid>`), or
  `strace -p <pid>`. Each runs as a transient unit with only
  `CAP_SYS_PTRACE` and `CAP_DAC_READ_SEARCH`, and only with exactly those
  arguments.

**Server → Graphical login screen**:
- Turns GDM, LightDM or SDDM off: the server boots to the text console and
  GNOME Shell no longer runs. It can be turned on again; nothing is
  uninstalled.
- The panel and SSH are not affected.


# Installation

> By default WhaCP installs **read-only**: it inventories the server and
> never changes it, so it is safe next to an existing panel. Write mode
> (`--enable-writes`) lets it manage websites, databases, certificates,
> the firewall and more.

## Requirements

- A supported system, with systemd:

  | Level | Systems |
  |---|---|
  | **Supported** (tested, recommended for production) | Ubuntu 22.04 and 24.04 LTS, AlmaLinux 9, Rocky Linux 9, RHEL 9 |
  | **Preview** (installs and is tested, not yet recommended for production) | Ubuntu 26.04 LTS, AlmaLinux 10, Rocky Linux 10, RHEL 10 |

  RHEL is tested through AlmaLinux and Rocky Linux, which are built from
  the same sources. On a preview system the installer warns and the panel
  shows a "preview" badge; everything works the same. Other systems are
  refused (`--force-unsupported` to try anyway, best effort).
- On AlmaLinux, Rocky Linux and RHEL, installing dependencies enables EPEL and Remi
  (PHP versions for websites, and PHP 8.3 for the panel on 9), and installs
  `policycoreutils-python-utils` for SELinux labels. SELinux stays enforcing:
  WhaCP labels what it creates (websites, mail storage, webmail data), sets
  the booleans it needs (`httpd_can_network_connect` for proxies and
  webmail, `ftpd_full_access` for FTP) and labels a moved SSH port. firewalld
  is used for the firewall, and Rspamd comes from rspamd.com's repository.
- PHP ≥ 8.3 CLI with `pdo_sqlite`, `curl`, `mbstring`.
  - **Ubuntu 22.04** ships PHP 8.1. The installer adds the `ondrej/php`
    PPA (signed) and installs `php8.3-cli` for the panel only.
  - The system's default `php` stays as it was, so websites' cron jobs and
    scripts keep their version.
  - The panel's services run `/usr/bin/php8.3`.
- **Go** to build the agent from a git checkout (release archives include
  it):
  - The installer uses the system's Go when it is recent enough: 1.24, or
    1.21 or later, which downloads 1.24 itself.
  - Otherwise (Ubuntu 22.04 ships Go 1.18) it downloads Go 1.24 from go.dev
    into `/usr/local/lib/whacp-go`, after checking the archive's SHA-256.
    The system's Go is not changed.
- Composer is installed when missing and run with the panel's PHP.
- To build from source: Go ≥ 1.24 and Composer (or ship `dist/whacp-agent`
  and `panel/vendor`).

### Hardware

WhaCP's own processes are small: the agent is capped at 256 MiB (1 GiB in
write mode) by its systemd unit, and the panel is a PHP process with a
SQLite database (tested with 100 websites, 500 domains and 10 000
notifications, every page under 15 ms). What a server needs is set by
what it hosts: Nginx, PHP-FPM pools, MariaDB, and for mail Postfix,
Dovecot and Rspamd. The CI test servers run all of these with 4 vCPUs,
6 GB RAM and a 30 GB disk; that is the smallest configuration tested with
every service at once. Keep disk space for backups: backups and restores
leave 512 MiB free and stop otherwise.

### Network ports

| Port | Service | Opened by |
|------|---------|-----------|
| 22/tcp (or your SSH port) | SSH, SFTP accounts | you (WhaCP refuses firewall changes that would close it, and rolls back SSH/firewall changes you do not confirm) |
| 80/tcp, 443/tcp | websites, webmail, Let's Encrypt validation | you |
| 25/tcp | incoming mail | WhaCP, when mail is set up |
| 587/tcp | mail submission (STARTTLS, sign-in required) | WhaCP, with mail |
| 993/tcp | IMAP over TLS (143 is not offered) | WhaCP, with mail |
| 53/tcp, 53/udp | DNS (authoritative zones) | WhaCP, when DNS is turned on |
| 21/tcp and 40110–40210/tcp | FTP(S) and its passive range (configurable) | WhaCP, when FTP is turned on |
| 8443/tcp (your choice) | agent for a remote panel (`--remote-access`) | you, for the panel's address only |
| 8443/tcp (with `--public-https`) | the panel over HTTPS, `https://SERVER-IP:8443/` | WhaCP (UFW or firewalld, when active) |
| 127.0.0.1:8090 | the panel itself (reached through `--public-https` or an SSH tunnel) | never exposed |

The agent's Unix socket, Rspamd, OpenDKIM, MariaDB and Redis listen
locally only.

### DNS and mail prerequisites

- A host name for the server (`hostname -f`) that resolves to its address;
  mail uses it as its name, and webmail can use it.
- For mail: a reverse DNS (PTR) record for the server's IP matching that
  host name (set at your provider), outgoing port 25 not blocked by the
  provider, and for each domain the MX, SPF, DKIM and DMARC records WhaCP
  shows (it can add them to a zone hosted on the same panel).
- For Let's Encrypt: each domain must point to the server and port 80 must
  be reachable from the internet.
- For DNS hosting: the domain's registrar must list this server (and any
  secondaries) as name servers, with glue records if the name servers are
  inside the domain.

## Release archives

Tagged releases publish `whacp-<version>-linux-{amd64,arm64}.tar.gz` with a
prebuilt agent and the panel's dependencies, so neither Go nor Composer is
needed. Verify before installing:

```bash
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify whacp-<version>-linux-amd64.tar.gz --repo NimVor/nimvor
tar -xzf whacp-<version>-linux-amd64.tar.gz && cd whacp-<version>
```

Archives are reproducible (`scripts/release.sh` on the tagged commit gives
identical files).

## Install

```bash
sudo ./installer/install.sh --dry-run
sudo ./installer/install.sh --admin-user=admin --admin-email=you@example.com
```

Use your **real** email for `--admin-email`: it becomes the Let's Encrypt
contact for expiry notices. An `example.com` (or other reserved-domain)
address is rejected by Let's Encrypt — WhaCP will still issue certificates,
but without a renewal-notice contact until you set a real address in your
profile.

Options: `--no-install-deps` (only list missing packages; by default they are installed),
`--port=8090`, `--yes`, `--reinstall` (replace program files, keep config and
data), `--force-unsupported`, `--enable-writes` (see below),
`--public-https[=8443]` (see "Opening the panel"), `--profile=`,
`--hostname=`, `--php=`, `--no-third-party-repos` (see "Server profile").

### Write mode

```bash
sudo ./installer/install.sh --enable-writes --admin-user=admin --admin-email=you@example.com
```

This sets `allow_write_actions: true` in `/etc/whacp/agent.json` and adds
`/etc/systemd/system/whacp-agent.service.d/10-write-mode.conf`, which lifts
the read-only filesystem sandbox (packages, users, services and the firewall
cannot be managed without it). It also creates `/home/whacp` (websites),
`/var/lib/whacp-agent` (state, 0700) and `/var/backups/whacp` (0700).
To go back to read-only: remove the drop-in, set `allow_write_actions` to
`false`, `systemctl daemon-reload && systemctl restart whacp-agent`.

Even in write mode WhaCP only writes files it created, validates every
configuration before reloading (e.g. `nginx -t`), keeps the previous version
and rolls back on failure. Existing websites stay untouched until you
**import** them (Websites → a discovered site → *Import into WhaCP*).

### Server profile

On a fresh server the installer then sets the server up for hosting, in
the manner of cPanel's installer. It shows the profile and asks first
(`--yes` answers yes; with no terminal and no `--yes`, no profile is
applied):

| Profile | Sets up |
|---|---|
| `full` (4 GB of memory or more) | Nginx, PHP 8.3 + 8.4 with the default extensions, MariaDB, email (Postfix, Dovecot, OpenDKIM, Rspamd), DNS (BIND, authoritative only), FTP (TLS required), Redis (256 MB limit), Memcached, ClamAV, Let's Encrypt, firewall with the components' ports and protections, Fail2ban, kernel hardening, SMTP restrictions, automatic security updates |
| `web` (2 GB or more) | the same without email, DNS, ClamAV and SMTP restrictions |
| `minimal` (less) | Nginx, PHP, MariaDB, Let's Encrypt, firewall, Fail2ban, kernel hardening, automatic security updates |
| `none` | nothing (the behaviour before profiles) |

Every profile also creates the hosting plans **Starter**, **Business**,
**Pro** and **Email only** with the feature lists **Hosting**, **Hosting
with SSH** and **Email only**, and weekly backups on this server (every
website on Sundays at 02:00 and the server configuration at 01:00, four of
each kept). Edit or remove them like any other plan or schedule.

```bash
sudo ./installer/install.sh --profile=full --hostname=server.example.com \
     --admin-user=admin --admin-email=you@example.com
```

Options: `--profile=full|web|minimal|none`, `--hostname=` (the mail
server's name; default the system's full host name), `--php=8.2,8.3,8.4`
(default 8.3,8.4), `--no-third-party-repos`.

- **Default.** By memory as in the table, but only on a fresh server: when
  Nginx, Apache, Postfix, Exim, BIND or MariaDB/MySQL already runs, with
  `--reinstall`, `--agent-only`, or over an existing installation, the
  default is `none` (your configuration is left alone). Choose a profile
  explicitly to set up such a server.
- **Write mode.** A profile turns write mode on.
- **Third-party repositories.** PHP versions the system does not offer
  (PHP 8.4 on Ubuntu 22.04/24.04 and RHEL 9) come from Ondřej Surý's PPA
  on Ubuntu (its signing key pinned by fingerprint) or Remi's repository on
  the RHEL family. These are third-party repositories: their packages are
  trusted like the system's. `--no-third-party-repos` keeps to the
  system's repositories and installs only the versions they offer.
- **Checks.** Memory, free disk, and for `full`: whether outgoing port 25 is
  open and whether the server's reverse DNS (PTR) name matches its host
  name. They warn; they do not stop the installation.
- **Firewall.** Turning the firewall on, opening the components' ports and
  the protections are guarded changes (undone after 2 minutes unless
  kept). The setup keeps each one only after reading the firewall again and
  finding SSH, the panel and the agent's listener allowed; otherwise it
  undoes the change at once and reports the step as failed.
- **Consent.** Choosing a profile with email and DNS is the consent to
  WhaCP editing Postfix's, Dovecot's and BIND's configuration (the same
  confirmation the Email and DNS pages ask for; backups are kept).
- **Idempotent.** Each step leaves alone what is already in place: an
  existing mail, DNS or FTP setup, fail2ban jails, firewall protections, a
  Redis memory limit, plans and schedules with these names. Installed PHP
  versions and extensions are never removed. A failed step is reported and
  the next ones still run.

Run it again, or later on a server installed with `none`:

```bash
sudo -u whacp WHACP_CONFIG=/etc/whacp/panel.php php /opt/whacp/panel/bin/whacp setup:apply --profile=web
sudo -u whacp WHACP_CONFIG=/etc/whacp/panel.php php /opt/whacp/panel/bin/whacp setup:status
```

(`php` is the PHP the panel runs on: on Ubuntu 22.04 the installer
prints its path.) `--only=step,step` runs some steps only (`packages`,
`php_repository`, `php`, `mail`, `dns`, `ftp`, `caches`, `services`,
`firewall`, `ports`, `protection`, `fail2ban`, `hardening`,
`security_defaults`, `auto_updates`, `plans`, `backups`, `profile`).

The `security_defaults` step turns on the recommended security settings
once, for every profile: weekly malware scans of every website with
quarantine, the five fail2ban jails (sshd, recidive, nginx-http-auth,
nginx-botsearch, nginx-limit-req: 60 minute ban, 5 attempts in 10 minutes),
compiler access for root only, SMTP restrictions, the cloud-metadata block
for website accounts, cPHulk brute force protection with its recommended
limits (your own address is whitelisted), shell fork bomb protection and
traceroute disabled for users. ModSecurity is only reported (its Nginx
connector is built from the ModSecurity page). A later run leaves what you
changed alone; every setting can be switched on or off from **Security →
Overview → Security settings**.

### Scheduler

`whacp-scheduler.timer` runs `bin/whacp schedule:run` every minute:
monitoring samples and alerts, scheduled backups and retention, remote
server heartbeats. `bin/whacp doctor` warns when it has not run recently.

### Remote servers

On each additional server install only the agent, reachable by the panel's
IP over TLS:

```bash
sudo ./installer/install.sh --agent-only --enable-writes \
     --remote-access=0.0.0.0:8443 --allow-from=203.0.113.10
```

The installer prints the address, the certificate pin (`sha256//…`) and the
token; add them under *All servers* in the panel. The panel refuses to send
the token to a peer whose key does not match the pin. Open the port in the
firewall for the panel's address only.

### What the installer changes

| Path | Purpose |
|------|---------|
| `/usr/local/bin/whacp-agent` | agent binary |
| `/opt/whacp/panel` | panel files (root:whacp, not writable by the panel) |
| `/etc/whacp/{agent.json,panel.php,agent.token}` | configuration (0640 root:whacp) |
| `/var/lib/whacp`, `/var/log/whacp` | panel database, sessions, log |
| `/etc/systemd/system/whacp-{agent,panel,scheduler}.service`, `whacp-scheduler.timer` | services |
| `/opt/whacp/plugins` | plugins installed by an administrator (none enabled by default) |
| user/group `whacp` | panel identity, agent socket group |

Without a server profile it does **not** touch Nginx, PHP-FPM,
MySQL/MariaDB, Redis, certificates, cron, firewall or SSH, and it does not
reload any existing service. A profile makes its changes through the
panel's own actions (see "Server profile"), never by editing files from the
installer.

## Accessing the panel

### Opening the panel

The panel itself listens on `127.0.0.1:8090` only. Reach it in one of two
ways.

**At the server's address over HTTPS** (like other panels): install with
`--public-https`, or add it to an installed server:

```bash
sudo ./installer/install.sh --reinstall --public-https        # port 8443
sudo ./installer/install.sh --reinstall --public-https=9443   # another port
```

Then browse to `https://SERVER-IP:8443/`. The installer:

- installs Nginx if needed (unless `--no-install-deps`);
- creates a self-signed certificate for the server's addresses and host name;
- adds WhaCP's own Nginx site (`whacp-panel.conf`) proxying to the panel,
  checks it with `nginx -t` (it is removed again if Nginx rejects it) and
  reloads Nginx;
- makes sign-in cookies HTTPS-only and trusts Nginx for the client address
  and `X-Forwarded-Proto` (`/etc/whacp/panel.php`);
- opens the port in UFW or firewalld when one is active (SELinux: labels the
  port and allows Nginx to connect to the panel);
- checks the panel answers over HTTPS.

The browser warns about the self-signed certificate once. Turn on
two-factor authentication, and where you can, allow only your own address:
add `allow YOUR.IP; deny all;` inside `location /` in `whacp-panel.conf`,
then `nginx -t && systemctl reload nginx`.

For a trusted certificate, point a name such as `panel.example.com` at the
server, obtain a certificate for it (for example with certbot) and change
the two `ssl_certificate` lines in `whacp-panel.conf`. Security
keys (WebAuthn) need such a name: browsers do not allow them on an IP
address.

After `--public-https` the SSH tunnel below no longer signs in (the cookie
is HTTPS-only). `uninstall.sh` removes the Nginx site and the certificate;
remove the firewall rule yourself.

**Through an SSH tunnel** (the default; nothing is exposed):

```bash
ssh -L 8090:127.0.0.1:8090 you@server
# browse to http://127.0.0.1:8090
```

## Verify

```bash
systemctl status whacp-agent whacp-panel whacp-scheduler.timer
sudo -u whacp WHACP_CONFIG=/etc/whacp/panel.php php /opt/whacp/panel/bin/whacp doctor   # Ubuntu 22.04: php8.3
curl -s http://127.0.0.1:8090/healthz
```

`doctor` checks PHP, the file modes of the key, token, database and
sessions, the database schema and integrity, the agent (reachable, same
version) and the scheduler. It exits with 1 when something fails.

### If the installation was interrupted

Run the installer again. If an earlier run did not finish, it says what is
missing and gives the command to complete it: the same options plus
`--reinstall`. That keeps the existing configuration, agent token, panel
key and database and creates only what is missing. Nothing outside
WhaCP's own files is changed by the installer, so there is nothing else to
undo.

## Update (source checkout)

```bash
sudo ./installer/update.sh
```

One command, with no options and no prompts:

1. Files that root left in the checkout (for example after `sudo git pull`)
   are given back to the checkout's owner.
2. The latest version is pulled as that owner (fast-forward only; local
   changes stop the update instead of being overwritten).
3. `install.sh --reinstall --yes` runs from the new version. It installs
   missing dependencies, rebuilds the agent and the panel's dependencies,
   migrates the database, loads the SELinux policy module and restarts
   WhaCP's services. The configuration, agent token, database, write mode
   and the panel's ports (public HTTPS included) are kept.

## Upgrade (release archive)

From the new release archive:

```bash
sudo ./installer/upgrade.sh           # asks for confirmation; --yes to skip
sudo ./installer/upgrade.sh --list    # upgrade backups (the last three are kept)
sudo ./installer/upgrade.sh --rollback
```

The upgrade checks the new agent against the current configuration and waits
until no background job (a restore, a deployment, a backup) is running — up
to 30 minutes, `--wait=2h` for longer, `--wait=0` not to wait; if jobs are
still running it stops without changing anything. It then backs up
program files, units, `/etc/whacp` and the panel database to
`/var/backups/whacp/upgrades` (the database copy must pass SQLite's
integrity check, and checksums of the whole backup are recorded), swaps the
files, migrates and restarts, then verifies the agent, the panel and
`/healthz`. Any failure restores the previous version automatically.
Websites keep running; only WhaCP's own services restart. Downgrades need
`--allow-downgrade`.

`--rollback` checks the backup against its checksums first and refuses a
damaged or changed one. It restores the panel database as it was before
the upgrade: everything changed in the panel since then is lost from the
panel (it says how many audit-log entries that is), while what was already
done on the server stays — a website created after the upgrade keeps
running but no longer appears in the panel. Roll back soon after an
upgrade, or not at all.

Whenever the agent stops (upgrade, `systemctl restart`, reboot) it first
lets running jobs finish, for at most 5 minutes; a job still running then
is marked "interrupted by an agent restart".

## Uninstall

```bash
sudo ./installer/uninstall.sh          # keep config and data
sudo ./installer/uninstall.sh --purge  # also remove configuration, panel data and logs
```

Uninstalling removes the panel, the agent and their services. It does
**not** remove what WhaCP created for you: websites keep running (their
Nginx sites, PHP-FPM pools, accounts, files, databases, certificates, mail
and DNS stay configured and served), because removing them would take
sites offline. Without `--purge`, `/etc/whacp`, `/var/lib/whacp` and
`/var/log/whacp` are kept. Even with `--purge`, `/var/lib/whacp-agent`
(the **backup encryption key**, the history of configuration changes,
uploaded certificates, import records) and
`/var/backups/whacp` (backups) are kept: remove them yourself once you
are sure you do not need them — without the key, encrypted backups cannot
be restored. `/var/lib/whacp-workers` (the launchers of background
workers, which keep running) is kept as well.

## Limits and known limitations

Limits (per request or operation): agent request 4 MiB; command output
kept 1 MiB; archive extraction 50 000 entries and 8 GiB; file manager
listings 5000 entries, editor 2 MiB; log views 5000 lines; SQL results
1000 rows or 16 MiB; DNS 2000 records per zone; application downloads
200 MiB; messages sent through WhaCP's sendmail 64 MiB; 1000 notifications
per website; 120 API requests per minute per token; backups and restores
leave 512 MiB free.

Known limitations in 1.0.0:

- Ubuntu 22.04 (Redis 6.0): a website's Redis user is limited to its key
  prefix but cannot be limited to its own Pub/Sub channels.
- RHEL 9 and 10 are tested only through AlmaLinux and Rocky Linux, which
  use the same packages.
- Ubuntu 26.04 and the version 10 systems are previews: tested like the
  others, but newer, with less use on real servers.
- Let's Encrypt issuance is tested against a local stand-in, not against
  Let's Encrypt itself.
- Recovering onto a new server finds only encrypted backups made by 1.0.0
  or later (see *Recovering on a new server* in the administration guide).
- Rolling back an upgrade restores WhaCP's files and panel database; it
  does not undo changes made on the server since (see *Upgrade*).

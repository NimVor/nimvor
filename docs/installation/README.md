# Installation

> By default WhaCP installs **read-only**: it inventories the server and
> never changes it, so it is safe next to an existing panel. Write mode
> (`--enable-writes`) lets it manage websites, databases, certificates,
> the firewall and more.

## Requirements

- Ubuntu 22.04, 24.04 or 26.04 LTS, or AlmaLinux, Rocky Linux or RHEL 9 or
  10, with systemd (others: `--force-unsupported`, best effort).
- On AlmaLinux, Rocky Linux and RHEL, `--install-deps` enables EPEL and Remi
  (PHP versions for websites, and PHP 8.3 for the panel on 9), and installs
  `policycoreutils-python-utils` for SELinux labels. SELinux stays enforcing:
  WhaCP labels what it creates (websites, mail storage, webmail data), sets
  the booleans it needs (`httpd_can_network_connect` for proxies and
  webmail, `ftpd_full_access` for FTP) and labels a moved SSH port. firewalld
  is used for the firewall, and Rspamd comes from rspamd.com's repository.
- PHP ≥ 8.3 CLI with `pdo_sqlite`, `curl`, `mbstring` (Ubuntu 22.04 ships
  PHP 8.1 — install 8.3 from a PHP repository first; your websites are not
  affected, WhaCP only uses the CLI binary).
- To build from source: Go ≥ 1.22 and Composer (or ship `dist/whacp-agent`
  and `panel/vendor`).

## Release archives

Tagged releases publish `whacp-<version>-linux-{amd64,arm64}.tar.gz` with a
prebuilt agent and the panel's dependencies, so neither Go nor Composer is
needed. Verify before installing:

```bash
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify whacp-<version>-linux-amd64.tar.gz --repo mchkhawarshahzad/WhaCP
tar -xzf whacp-<version>-linux-amd64.tar.gz && cd whacp-<version>
```

Archives are reproducible (`scripts/release.sh` on the tagged commit gives
identical files).

## Install

```bash
sudo ./installer/install.sh --dry-run
sudo ./installer/install.sh --admin-user=admin --admin-email=you@example.com
```

Options: `--install-deps` (apt-install missing PHP packages, opt-in),
`--port=8090`, `--yes`, `--reinstall` (replace program files, keep config and
data), `--force-unsupported`, `--enable-writes` (see below).

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

It does **not** touch Nginx, PHP-FPM, MySQL/MariaDB, Redis, certificates, cron,
firewall or SSH, and it does not reload any existing service.

## Accessing the panel

The panel listens on `127.0.0.1:8090` only. Use an SSH tunnel:

```bash
ssh -L 8090:127.0.0.1:8090 you@server
# browse to http://127.0.0.1:8090
```

### Optional: HTTPS reverse proxy

If you want to reach it directly, add a dedicated Nginx server block **by
hand**, test it with `nginx -t`, and only then reload. Then set
`'secure_cookie' => true` and `trusted_proxies => ['127.0.0.1']` in
`/etc/whacp/panel.php`. Restrict access by IP where possible.

```nginx
server {
    listen 8443 ssl http2;
    server_name panel.example.com;
    ssl_certificate     /etc/letsencrypt/live/panel.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/panel.example.com/privkey.pem;
    location / {
        proxy_pass http://127.0.0.1:8090;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
    }
}
```

## Verify

```bash
systemctl status whacp-agent whacp-panel whacp-scheduler.timer
sudo -u whacp WHACP_CONFIG=/etc/whacp/panel.php php /opt/whacp/panel/bin/whacp doctor
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

## Upgrade

From the new source tree or release archive:

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
sudo ./installer/uninstall.sh --purge  # remove everything WhaCP created
```

# Installation

> This release is **read-only**: it inventories the server and never changes
> it. It is safe to install next to an existing panel.

## Requirements

- Ubuntu 22.04, 24.04 or 26.04 LTS with systemd (others: `--force-unsupported`,
  best effort).
- PHP ≥ 8.3 CLI with `pdo_sqlite`, `curl`, `mbstring` (Ubuntu 22.04 ships
  PHP 8.1 — install 8.3 from a PHP repository first; your websites are not
  affected, WhaCP only uses the CLI binary).
- To build from source: Go ≥ 1.22 and Composer (or ship `dist/whacp-agent`
  and `panel/vendor`).

## Install

```bash
sudo ./installer/install.sh --dry-run
sudo ./installer/install.sh --admin-user=admin --admin-email=you@example.com
```

Options: `--install-deps` (apt-install missing PHP packages, opt-in),
`--port=8090`, `--yes`, `--reinstall` (replace program files, keep config and
data), `--force-unsupported`.

### What the installer changes

| Path | Purpose |
|------|---------|
| `/usr/local/bin/whacp-agent` | agent binary |
| `/opt/whacp/panel` | panel files (root:whacp, not writable by the panel) |
| `/etc/whacp/{agent.json,panel.php,agent.token}` | configuration (0640 root:whacp) |
| `/var/lib/whacp`, `/var/log/whacp` | panel database, sessions, log |
| `/etc/systemd/system/whacp-{agent,panel}.service` | services |
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
systemctl status whacp-agent whacp-panel
sudo -u whacp WHACP_CONFIG=/etc/whacp/panel.php php /opt/whacp/panel/bin/whacp agent:check
curl -s http://127.0.0.1:8090/healthz
```

## Uninstall

```bash
sudo ./installer/uninstall.sh          # keep config and data
sudo ./installer/uninstall.sh --purge  # remove everything WhaCP created
```

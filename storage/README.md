# storage/

Placeholder for repository-level runtime state used by development tooling.

WhaCP never stores runtime state inside the source tree in production:

| What | Production location | Development (`scripts/dev.sh`) |
|------|--------------------|--------------------------------|
| Panel database (SQLite) | `/var/lib/whacp/whacp.sqlite` | `.dev/whacp.sqlite` |
| Panel sessions | `/var/lib/whacp/sessions/` | `.dev/sessions/` |
| Panel log | `/var/log/whacp/panel.log` | `.dev/panel.log` |
| Agent log | systemd journal (`journalctl -u whacp-agent`) | `.dev/agent.log` |
| Agent token | `/etc/whacp/agent.token` | `.dev/token` |

# storage/

Placeholder for repository-level runtime state used by development tooling.

NimVor never stores runtime state inside the source tree in production:

| What | Production location | Development (`scripts/dev.sh`) |
|------|--------------------|--------------------------------|
| Panel database (SQLite) | `/var/lib/nimvor/nimvor.sqlite` | `.dev/nimvor.sqlite` |
| Panel sessions | `/var/lib/nimvor/sessions/` | `.dev/sessions/` |
| Panel log | `/var/log/nimvor/panel.log` | `.dev/panel.log` |
| Agent log | systemd journal (`journalctl -u nimvor-agent`) | `.dev/agent.log` |
| Agent token | `/etc/nimvor/agent.token` | `.dev/token` |

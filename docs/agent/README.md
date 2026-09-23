# WhaCP Agent

The agent is a single static Go binary (`/usr/local/bin/whacp-agent`) run by
systemd as `whacp-agent.service`. It is the only WhaCP component with
privileges.

## Configuration — `/etc/whacp/agent.json`

| Key | Default | Meaning |
|-----|---------|---------|
| `socket_path` | `/run/whacp/agent.sock` | Unix socket |
| `socket_mode` | `0660` | must not grant "other" access (enforced) |
| `socket_group` | `whacp` | group of the socket (the panel's group) |
| `token_file` | `/etc/whacp/agent.token` | ≥ 32 chars, not world-readable (enforced) |
| `allowed_uids` | root + `whacp` user | local users allowed to connect (SO_PEERCRED) |
| `root_dir` | `/` | base for every file the agent reads (fixtures in tests) |
| `proc_dir` | `/proc` | procfs for metrics |
| `log_level` | `info` | `debug`, `info`, `warn`, `error` |
| `metrics_interval` | `5s` | collector sampling period |
| `metrics_history` | `180` | samples kept in memory (15 min at 5 s) |
| `action_timeout` | `30s` | per-action deadline |
| `allow_write_actions` | `false` | gate for modifying actions (none exist yet) |

Unknown keys are rejected so typos do not silently fall back to defaults.
Check a configuration with `whacp-agent -config /etc/whacp/agent.json -check-config`.

## Actions

| Action | Returns |
|--------|---------|
| `agent.actions` | the action catalogue |
| `system.info` | OS, kernel, CPU, interfaces, timezone, uptime, agent version |
| `system.metrics` | latest CPU/memory/disk/load/network sample |
| `system.metrics_history` | recent samples for charts |
| `services.status` | core services and PHP-FPM units (systemd) |
| `discovery.websites` | websites correlated from Nginx + PHP-FPM + certificates |
| `discovery.nginx` | raw virtual hosts and files read |
| `discovery.php` | PHP versions, default CLI version, FPM pools |
| `discovery.databases` | MySQL/MariaDB engine, version, schemas and sizes |
| `discovery.certificates` | certbot lineages with expiry and key permission check |
| `discovery.redis` | Redis version and config summary (password never returned) |
| `discovery.cron` | system and user cron jobs (secrets redacted) |
| `discovery.users` | accounts from `/etc/passwd` (shadow never read) |
| `discovery.firewall` | UFW/firewalld/nftables detection |
| `discovery.inventory` | all of the above in one snapshot |

The full contract is in [`agent/api/openapi.yaml`](../../agent/api/openapi.yaml).

```bash
sudo curl -s --unix-socket /run/whacp/agent.sock \
  -H "Authorization: Bearer $(sudo cat /etc/whacp/agent.token)" \
  -X POST http://agent/v1/actions/discovery.websites | jq .
```

## How discovery reads Nginx

1. Parse `/etc/nginx/nginx.conf` with a real tokenizer (quotes, comments,
   `${var}`, nested blocks) and expand `include` globs relative to
   `/etc/nginx`. Includes outside `/etc/nginx` are refused and reported.
2. Collect `server` blocks inside `http`. Symlinks in `sites-enabled` are
   resolved so each vhost points at its real file.
3. Parse files in `sites-available` that are not enabled and report them as
   disabled.
4. Group server blocks by primary `server_name` (catch-all `_` blocks are
   counted, not listed), match `fastcgi_pass` against PHP-FPM `listen`
   addresses, and inspect referenced certificates.

Limits: 2 MiB per file, 2000 included files, depth 10, 50 nested blocks.

## Sandbox

`whacp-agent.service` keeps only `CAP_DAC_READ_SEARCH` (read any file) and
`CAP_CHOWN` (socket ownership), sets `ProtectSystem=strict` so the entire
filesystem is read-only except `/run/whacp`, and applies `NoNewPrivileges`,
a system-call filter, `MemoryDenyWriteExecute`, `RestrictAddressFamilies`
(Unix + netlink) and memory/task limits. When write actions arrive, each one
will add the narrowest possible `ReadWritePaths` entry.

## Command execution

`execx.SystemRunner` can run exactly: `systemctl`, `nginx`, `mysqld`,
`mariadbd`, `redis-server` — resolved to absolute paths at start-up, with a
minimal environment, no shell, a timeout and a 1 MiB output cap. Today they
are used only for `systemctl show` and `--version`/`-v` queries.

## OS support

`platform` implements the Debian family (Ubuntu 22.04/24.04/26.04 supported,
Debian best-effort) and a RHEL-family scaffold (AlmaLinux/Rocky paths
defined, not yet supported). New distributions add a `Platform`
implementation; no other package changes.

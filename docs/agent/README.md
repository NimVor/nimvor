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
| `allow_write_actions` | `false` | gate for modifying actions |
| `sites_root` | `/home/whacp` | where WhaCP websites live |
| `state_dir` | `/var/lib/whacp-agent` | backups of changed files, jobs, pending security changes, import records |
| `backup_dir` | `/var/backups/whacp` | local backup archives |
| `web_user` | platform default (`www-data`) | Nginx worker account |
| `max_jobs` | `2` | concurrent background jobs |
| `listen_tcp` | empty | `host:port` for remote access over TLS 1.3 |
| `tls_cert`, `tls_key` | `/etc/whacp/agent-tls.{crt,key}` | created on first start when missing; `whacp-agent -pin` prints the pin |
| `allowed_clients` | empty | IPs/networks allowed on `listen_tcp` (required when it is set) |
| `simulate_commands` | `false` | development only: log commands instead of running them |

Unknown keys are rejected so typos do not silently fall back to defaults.
Check a configuration with `whacp-agent -config /etc/whacp/agent.json -check-config`.

## Actions

Read-only actions (always available) include `agent.actions`,
`system.info`, `system.metrics`, `services.status` and the `discovery.*`
family (`discovery.inventory` returns everything in one snapshot).
Modifying actions — `sites.*`, `php.*`, `db.*`, `ssl.*`, `files.*`,
`cron.*`, `workers.*`, `deploy.*`, `redis.*`, `backup.*`, `firewall.*`,
`ssh.*`, `fail2ban.*`, `security.*`, `packages.*`, `server.*`, `apps.*`,
`adopt.*` — are refused with `write_disabled` unless write mode is on.
Long-running ones return a `job_id`; follow it with `jobs.get`/`jobs.log`.

The complete, test-verified catalogue with a description of every action
and whether it modifies the server is in
[`agent/api/openapi.yaml`](../../agent/api/openapi.yaml) (`x-whacp-actions`).
`agent.actions` returns the same list from a running agent.

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
(Unix + netlink) and memory/task limits.

Write mode (`10-write-mode.conf`) lifts the filesystem sandbox because
installing packages, creating accounts and managing the firewall touch most
of the system. Safety then rests on the agent: the file writer only accepts
an allowlist of WhaCP-owned paths (its configuration files, the sites root,
state and backup directories), every configuration change is validated
before reload and rolled back on failure, and commands for a website run
as that website's unprivileged account.

## Command execution

`execx.SystemRunner` runs only allowlisted binaries (systemd tools, `nginx`,
MySQL/MariaDB clients, Redis, account tools, `certbot`, `git`,
`ssh-keygen`, APT/dpkg, `ufw`, `fail2ban-client`, `sshd -t`,
`hostnamectl`, `timedatectl`, `shutdown`, `composer`, `tar`, `sh`), resolved
to absolute paths at start-up, with a minimal environment, arguments passed
as a list (never through a shell), a timeout and an output cap. `sh` and
`tar` may only run as an unprivileged account (UID ≥ 1000), e.g. a
website's deploy hook.

## OS support

`platform` implements the Debian family (Ubuntu 22.04/24.04 supported,
26.04 preview, Debian best-effort) and the RHEL family (AlmaLinux, Rocky
Linux and RHEL 9 supported, 10 preview); `system.info` reports `supported`
and `preview`. The platform also maps package names (`pkgs` installs with apt or
dnf), and names PHP versions the Remi way on RHEL (`php83-php-fpm`, pools in
`/etc/opt/remi/php83/php-fpm.d`). `fw` opens service ports in UFW or
firewalld, and `selinux` keeps SELinux labels, booleans and ports
consistent with what WhaCP sets up. Both do nothing where their subsystem
is absent. New distributions add a `Platform`
implementation; no other package changes.

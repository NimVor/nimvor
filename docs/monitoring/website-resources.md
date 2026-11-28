# Website resource monitoring

*Websites → Resource usage* (server-wide ranking) and *Website → Resource usage* (one website) show what
each website uses, from history recorded with the **existing monitoring pass** (`bin/nimvor monitor`).
There is no second collector or daemon: the pass makes one extra agent call, `sites.metrics`.

## What was already there (audit)

| Area | State before this feature |
|---|---|
| Collector | The agent's `monitoring.Collector` keeps server-wide samples in memory; the panel's `MonitoringService::run` stores them in `metric_samples` (server totals only). Nothing per website. |
| cgroups v2 | **Per hosting account**, not per website: `nimvor-<account>.slice` with the plan's CPU, memory, tasks and I/O limits (`agent/internal/limits`). All websites of an account share its slice. Accounts without limits have no slice. `limits.status` already read memory, CPU time, throttled time, memory events and OOM kills. |
| PHP-FPM | One pool per website, named `nimvor-<domain>` (`sites/render.go`). No `pm.status_path`, so no FPM status page. |
| Nginx | One access log per website, `<sites root>/<domain>/logs/access.log`, combined format (no request time). `webreport.site` summarises it on demand. |
| MariaDB | `performance_schema` is not used anywhere in NimVor. |

## What is measured, and how it is attributed

| Metric | Source | Attribution | Notes |
|---|---|---|---|
| CPU (% of one core, CPU seconds) | `/proc/<pid>/stat` of the pool's workers | Exact: the worker's command line is `php-fpm: pool nimvor-<domain>` | **Sampled**: a worker that starts and ends between two samples is not seen (`workers_exited` counts them). Only PHP workers; not cron, SSH or Nginx. |
| Memory now / highest | `/proc/<pid>/smaps_rollup` (PSS; RSS if unreadable) | Same | PSS splits shared pages fairly. "Highest" is the highest *sampled* value. |
| Disk read / write bytes | `/proc/<pid>/io` (`read_bytes`, `write_bytes`) | Same | What reached the block layer, not cached reads. |
| PHP workers | Count of the pool's processes | Same | Not active/idle: see below. |
| Requests, 2xx–5xx, bytes sent | The website's own access log, counted from where the previous pass stopped | Exact (one log per website) | First sight of a log starts at its end: no backfill. Rotation is detected by inode and size. A log that grew by more than 16 MB between passes is skipped to its end and flagged. |
| Account memory, peak, CPU throttling, memory events, OOM kills, tasks, I/O bytes and IOPS | cgroup v2 files of the account's slice | **Per account**, shared by its websites; the page says so when an account has several | Kernel counters, cumulative; the panel stores the interval's difference and ignores a counter that went backwards. `memory.peak` needs Linux 5.19. |

Every column is `NULL` when it was not measured (no PHP workers, no log, first sample after an agent
start). Charts show a gap; the ranking shows a dash. Nothing is zero-filled.

### Not measured (and why)

* **PHP-FPM busy / idle workers, the request queue, worker-limit faults** are recorded only while the
  administrator's *PHP-FPM status* switch is on (below). Off, the worker *count* is read from the processes;
  busy and idle cannot be told apart reliably from process state.
* **Average and slow response times**: the access log format has no `$request_time`. Changing the format
  would break the statistics and report parsers; adding a second log doubles log I/O.
* **Database activity per website**: not shown. MariaDB's Performance Schema is off by default and attributes
  statements to the connection's *default schema*, which is wrong for cross-schema queries and shared
  accounts. It is not displayed as a website figure until a reliable mapping exists.
* **Disk I/O limit exhaustion**: cgroup v2 keeps no counter for `io.max` throttling. Only per-account bytes and
  IOPS are recorded.

## PHP-FPM status (opt-in)

*Resource usage → PHP-FPM status → Switch on* (administrators) adds one line, `pm.status_path = /nimvor-status`,
to each website's PHP pool (agent actions `sites.fpm_status`, `sites.fpm_status_set`). It is not on by
default because it edits live pool files:

* The new pool file is the old one plus that line (the flag is part of the pool rendering, so a later save of
  the website keeps it); switching it off restores the previous file byte for byte.
* All pools are written in one change set, the configuration of every affected PHP-FPM master is checked with
  `php-fpm -t` before anything is reloaded, and a refusal or failed reload puts every file back. Pools that
  were edited by hand, or have no pool file, are left alone and listed.
* No limit changes. PHP-FPM answers the status request itself, on the pool's FastCGI socket (the agent asks as
  root; Nginx never routes `/nimvor-status`, and no PHP file runs).

With it on, each monitoring pass also records, per website: busy, idle and total workers, requests waiting in the
listen queue, the highest number busy at once since PHP-FPM started, and (as differences against the previous
pass) accepted requests, slow requests and how often the pool wanted more workers than `pm.max_children`
(`max children reached`). Busy workers are the requests being served at once. Counters that went backwards
(PHP-FPM restarted) give no difference. Faults: *PHP-FPM limit reached* (a fault: the limit was actually hit);
warnings: requests waiting for a worker, and busy workers at 90 % of `pm.max_children`. These belong to the
website, not to its account.

## Faults and warnings

Recorded per account (`site_resource_events`), shown on each of its websites and counted in the ranking.

| Kind | Severity | Condition |
|---|---|---|
| `oom_kill` | fault | `memory.events oom_kill` increased |
| `memory_limit` | fault | `memory.events max` increased (the hard limit was hit) |
| `cpu_throttled` | fault | `cpu.stat nr_throttled` increased |
| `memory_pressure` | warning | `memory.events high` increased (processes slowed from 90 %) without the limit being hit |
| `cpu_near_limit`, `memory_near_limit`, `processes_near_limit` | warning | at least 90 % of the limit, nothing cut off |

Events of one kind within 15 minutes are one event (`occurrences` counts them).

## History and retention

* Raw samples, one per monitoring pass, feed the 1-hour and 24-hour views (`site_metric_samples`,
  `account_metric_samples`); kept `resource_raw_days` (default 3, 1–14).
* Hourly roll-ups feed the 7-day and 30-day views and the ranking (`site_metric_hourly`); rebuilt for the
  current and previous hour on each pass; kept `resource_hourly_days` (default 90, 31–400), as are events.
* The ranking never scans raw samples except for the 1-hour view; dashboards read the roll-ups.
* `resource_monitoring` (Settings → Monitoring) turns collection off.

## Limits

Limits are the existing per-account plan limits (CPU %, memory, processes, disk read/write MB/s). This
feature only **reads** them. It never changes, lowers or adds a limit; a website's PHP process limit stays on
its PHP settings page.

## API

* `GET /api/v1/websites/{id}/resources?range=1h|24h|7d|30d`: latest sample, history points, account points,
  faults and warnings.
* `GET /api/v1/resources/ranking?range=&sort=cpu|memory|io|requests|traffic|errors|faults&q=&faults=1&limit=`
* Permission `websites.view`; only websites the token may see (customers' websites are scoped to their owner).
* Agent action `sites.metrics` (read-only) takes `{"domains": [...]}`.

## Overhead

One agent call per monitoring pass. A pass over 500 PHP-FPM workers of 100 websites took about 9 ms and
2.8 MB of allocations on a 2.3 GHz virtual machine (`go test -bench Collect ./internal/sitemetrics`, fake
`/proc`; real `/proc` reads are of the same order). The agent keeps only the previous sample's counters in
memory. In the panel a pass adds one row per website (about 100 bytes of SQLite) and one roll-up statement.

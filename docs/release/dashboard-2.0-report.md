# Dashboard 2.0 — audit, implementation, testing and recommendations

Release 1.9.0. This report is the four deliverables asked for in the Dashboard 2.0 brief. The
per-widget data matrix and the layout and chart rules are in
[docs/development/dashboard-2.0.md](../development/dashboard-2.0.md); this document says what
shipped, how it was checked and what is left.

## A. Audit (what was found)

* The dashboard was one template filled by one controller action. CPU, memory, disk and load cards
  with sparklines (agent `system.metrics`, polled), the Server, Services and Websites cards, and
  three hard-coded cards (Needs attention, Disk, Backups). No per-user layout, no lazy loading, no
  cache: every card ran on every page load.
* Most of what the brief asked for already had a data source in the agent or the panel (processes,
  disks, SSH logins, fail2ban, firewall, ClamAV, certificates, databases, Redis, Memcached, Nginx
  state, package updates, mail queue, deployments, WordPress, the 30-day `metric_samples`). The
  matrix in the development document lists each one.
* Real gaps: website HTTP checks (nothing measured whether a site answered), PHP-FPM pool health,
  Nginx request metrics (needs `stub_status`, which NimVor does not enable), swap, 5- and 15-minute
  load, service availability and database activity history, verified-restore state of backups,
  cron run results, alert severity levels, acknowledgement and central thresholds.
* Alerts already had a sound pipeline (one open alert per rule and subject, refreshed while the
  condition lasts, closed when it ends, notified once on each change). It was extended, not
  replaced.

## B. Implementation (what shipped)

| Step | What | New in the agent / database |
|---|---|---|
| 1 | Widget registry, cache, saved layouts per user, Customize window, lazy loading and per-widget refresh; Needs attention, Disk and Backups moved in; Top processes | migration 0077 |
| 2 | Website health: real HTTP checks of every website every five minutes, uptime, slowest sites, failing sites | agent `sites.probe` (read-only); migration 0078 |
| 3 | Security overview and SSL certificates | none |
| 4 | Databases (MariaDB/MySQL and PostgreSQL), Redis and Memcached, Nginx and PHP-FPM | agent `php.fpm_health` (read-only, reads the end of the FPM log) |
| 5 | Backup health, System updates, DNS and mail, Deployments, WordPress | none |
| 6 | Capacity forecast (least-squares trend, shown only with a week of data and a good fit) and Traffic | none |
| 7 | Alert center: information severity, acknowledge and resolve (audited), six new rules, thresholds as settings, an unreadable source keeps its alert open | migration 0079 |
| — | Layout polish: one container-query grid, compact widget frame, one key-figure style, compact tables, balanced default rows | none |
| — | Charts: a dependency-free SVG chart module, performance history with range selector, donuts, bars and history of disk, network, database, services | migration 0080 |

Rules that held throughout: values that cannot be measured show N/A (never zero), no history is
invented or back-filled, every widget is checked against its permission before any data is
collected, new agent actions are read-only, and no script or style is inline.

Fixed on the way: the Databases widget read PostgreSQL monitor fields that do not exist (found from
a screenshot of a real server; the first test had used made-up data), and a forecast test depended
on the time of day.

## C. Testing

* **Agent**: `go vet`, `gofmt`, `go test -race` for the new packages (`probemgr`: status and Host
  header with no redirect followed, refused, a foreign address refused, timeout, concurrency;
  `phpmgr`: the FPM log counts only the last 24 hours) and the regenerated OpenAPI file.
* **Panel**: about 90 new tests across the framework (registry, permissions, cache, layouts), each
  widget group (real readings, N/A, empty and unavailable states), the alert center (rules,
  thresholds, de-duplication, acknowledge, resolve, reopen, permissions, audit), the forecast
  maths, and the chart history (what each pass records, counter resets, gaps, endpoint ranges and
  permission). Each pull request passed the full panel suite on PHP 8.3 and 8.4, the agent job,
  smoke, e2e, MongoDB and the real-host job.
* **Browser**: the existing dashboard end-to-end test now also covers Top processes, Customize,
  Restore default and the performance chart tabs and ranges. The layout and charts were checked at
  1440, 1024, 768 and 390 px in light and dark: no horizontal overflow, tooltips, tabs and range
  switching work, and the no-history state reads correctly. Checked in Chromium only.
* **Not tested**: PHP-FPM log lines from distributions other than the Debian format; charts with
  months of real history (the visual checks used clearly synthetic local data, never shipped);
  behaviour on a server with many websites (the probe is bounded to eight at a time and 500 per
  call); the other four themes (they apply to customer pages, which have no server dashboard).

## D. Recommendations (not done)

1. **Step 8 of the brief**: drag and drop, free resizing and a refresh interval per widget. Rows
   move with arrow buttons and widths are four fixed sizes today.
2. Bring the first cards (Server, Services, Websites, Network, Recent activity) into the registry so
   they can be hidden and moved like the others.
3. Optional Nginx `stub_status` (loopback only) for request rates, and an access-log aggregator for
   request trends and visitors; neither is collected today.
4. PostgreSQL history next to MariaDB's, and longer retention with rollups (30 days of five-minute
   samples limits the 7-day chart's detail and anything longer).
5. Record test restores so *Backup health* can say a backup was verified; store cron run results.
6. A settings page that groups the alert thresholds and previews what would fire.
7. Per-website charts (response time from the website checks, bandwidth) on the website's own page.

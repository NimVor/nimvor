# Dashboard 2.0 — audit, architecture and plan

The administrator dashboard grows from a fixed page into a set of widgets that
can be shown, hidden, reordered and resized per user. This document records what
was found when the existing dashboard and its data sources were inspected, how
the widget framework works, and the order the widgets arrive in. It is updated
with each step; the CHANGELOG says what shipped in which release.

## 1. The dashboard before Dashboard 2.0

| Part | Where | Data |
|---|---|---|
| Hero, quick actions, alert banner, unread failures | `dashboard/index.php` | `MonitoringService::openAlerts()`, `NotificationService` |
| CPU, memory, disk, load cards with sparklines | `index.php`, `nimvor.js` | agent `system.metrics`, `system.metrics_history` (polled by `DashboardController::live`) |
| Server, Services, Websites cards | `index.php` | agent `system.info`, `services.status`, discovery inventory |
| Needs attention, Disk, Backups | `index.php` (hard-coded) | `SitesHealthService`, `ServerDiskService`, `backups` table |
| Network chart, Recent activity | `index.php` | `metric_samples`, audit log |

Everything was one template filled by one controller action. There was no
per-user layout, no lazy loading and no cache; every widget ran on every page
load.

## 2. What data already exists (implementation matrix)

"Exists" means the agent action or panel service is in the code, not merely in
the navigation. "Gap" is what a widget still needs.

| Widget | Data source that exists | Gap |
|---|---|---|
| CPU, memory, load, network, uptime | agent `system.metrics`, `system.live`, `metric_samples` (30 days) | swap, disk I/O and temperature are not collected |
| Top processes | agent `system.live` (top 5 by CPU and memory), `processes.list`, `processes.signal` (stop/kill with confirmation on the Processes page) | none — **shipped** (step 1) |
| Disk and inodes | agent `disk.filesystems`, `disk.scan`, `ServerDiskService` | latency and read-only warnings |
| Website uptime and speed | agent `sites.probe` (read-only), `website_checks` table, `WebsiteMonitor` | none — **shipped** (step 2) |
| Security overview | `ssh.logins`, `fail2ban.status`, `firewall.status`, `av.status`, `intrusion.status`, `security.audit` | aggregation widget |
| SSL | `ssl.renewal_status`, `ssl.renewal_log`, certificate dates in discovery | aggregation widget |
| Databases | `db.server_status`, `db.processlist`, `pg.monitor`, `mongo.status` | slow queries, lock waits, growth need history |
| PHP-FPM | `php.fpm_get` (configuration), `services.status` | pool status (workers, max children reached) needs a collector |
| Nginx | `nginx.status` (running, config test) | active connections, requests/s need `stub_status` (only if configured) |
| Redis, Memcached | `redis.info`, `memcached.status` | aggregation widget |
| Backups | `backups`, `backup_schedules`, storages | verified-restore state is not recorded |
| System updates | `packages.updates`, `packages.advisories`, `packages.health` | aggregation widget |
| DNS and mail | `dns.status`, `mail.queue`, `mail.server_status`, `EmailHealth` | aggregation widget |
| Deployments, cron | `DeployService::history`, `CronService::runs` | aggregation widget |
| WordPress | `WordPressService::installs`, `wordpress.scan`, `WordPressVulnerabilities` | aggregation widget |
| Traffic | per-site access-log readers (`sites.bandwidth`, `stats.data`) | server-wide aggregation |
| Capacity forecasting | `metric_samples` (CPU, memory, disk, load, network for 30 days) | the forecast itself; "not enough data" state |
| Alerts | `alerts` table and `MonitoringService` rules (disk, memory, load, service, certificate, quota, bandwidth) | severity levels, acknowledgement, more sources, central thresholds |

## 3. Architecture

* `App\Dashboard\Widget` — one widget's definition: id, title, icon, category,
  permission, default size, refresh seconds, cache seconds, whether it is drawn
  with the page ("inline") or loaded after it.
* `WidgetRegistry` — the list of widgets. Adding one means listing it there,
  giving it a data method in `DashboardWidgetsService` and a view in
  `resources/views/dashboard/widgets/`.
* `DashboardWidgetsService::widget()` — returns `{state, data, at}` for one
  widget, through `WidgetCache`. States: `ok`, `empty`, `unavailable` (the
  source cannot answer; never drawn as zeros) and `error`. A number that could
  not be measured is `null` and shown as "N/A".
* `WidgetCache` (`widget_cache` table) — keeps a measurement for the widget's
  own number of seconds, so many open dashboards and manual refreshes cost one
  measurement. A failed measurement is never cached.
* `DashboardLayout` (`dashboard_layouts` table) — stores only what the user
  chose (visible, size, order). Defaults come from the registry when the layout
  is read, so a widget added in a later release appears without rewriting
  anyone's layout, and a removed or no-longer-permitted widget is left out.
* Endpoints: `GET /dashboard/widgets/{id}` (one widget's HTML, checked against
  the widget's permission before any data is collected), `POST /dashboard/layout`,
  `POST /dashboard/layout/reset`.
* Front end: `nimvor.js` loads lazy widgets after the page, refreshes each at its
  own pace only while the tab is visible (never faster than every 10 seconds),
  and moves rows in the Customize window. No inline scripts or styles.

Permissions: every widget declares the permission needed to see it; the page,
the data endpoint and the layout all filter by it. Resellers and customers do
not get the server dashboard at all (they are redirected), so server-wide
numbers are never shown to them.

## 4. Order of work

1. **Foundation** (this step): registry, cache, saved layouts, Customize window,
   lazy loading; Needs attention, Disk and Backups moved into the registry
   unchanged; Top processes as the first new widget.
2. Website uptime and performance (**shipped**): the scheduler's five-minute pass asks the agent's read-only `sites.probe` action to request each enabled website's home page on this server itself (the website's own address or loopback, its name in the Host header, redirects not followed, 8 at a time, 10 s timeout). 2xx/3xx = up, 401/403 = online but asking for a login, other 4xx = client error, 5xx = server error, no answer = down. Results are kept for 14 days; an agent that may not open connections records nothing (never a false "down"). "Check now" is limited to once a minute.
3. Security overview and SSL intelligence.
4. Database, PHP-FPM, Nginx, Redis and Memcached intelligence.
5. Backups, updates, DNS and mail, deployments, WordPress and storage widgets.
6. Traffic intelligence and capacity forecasting.
7. Alert center: severities, acknowledgement, de-duplication, central thresholds.
8. Drag and drop, free resizing and per-widget refresh settings.

Not done in step 1, by design: drag and drop (rows move with arrow buttons for
now), free resizing (four fixed widths), per-widget refresh settings, and moving
the CPU/memory cards, Server, Services, Websites, Network and Recent activity
cards into the registry. They keep working as before.

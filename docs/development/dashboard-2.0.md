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
| Security overview | `ssh.logins`, `fail2ban.status`, `firewall.status`, `av.status`, `modsec.status`, `security.audit`, panel sign-in failures from the audit log | none — **shipped** (step 3) |
| SSL | `ssl.store`, `ssl.renewal_status` | none — **shipped** (step 3) |
| Databases | `db.server_status`, `pg.status`, `pg.monitor` | **shipped** (step 4); database growth needs history (step 6) |
| PHP-FPM | `php.fpm_health` (new, read-only: service state and, from the end of the FPM log, pools that reached `pm.max_children`, busy warnings, worker crashes in 24 h) | **shipped** (step 4); live worker counts need `pm.status_path`, which NimVor does not enable |
| Nginx | `nginx.status` (running, config test) | **shipped** (step 4) without request rates: those need `stub_status`, which is not enabled, so the widget says so instead of showing zeros |
| Redis, Memcached | `redis.info`, `memcached.status` | none — **shipped** (step 4) |
| Backups | `backups`, `backup_schedules`, storages | **shipped** (step 5); verified-restore state is not recorded, so the widget says so instead of claiming it |
| System updates | `packages.updates`, `server.settings` (restart needed) | **shipped** (step 5) |
| DNS and mail | `dns.status`, `mail.server_status`, `mail.queue` | **shipped** (step 5); per-domain `EmailHealth` stays on Site health |
| Deployments, cron | `deployments` table | deployments **shipped** (step 5); cron run results are not stored by the panel, so there is no cron widget |
| WordPress | `wordpress_installs` (last scan) | **shipped** (step 5): out-of-date core, plugin/theme updates, open issues; vulnerabilities stay in the WordPress Toolkit |
| Traffic | `metric_samples` (network in/out), `website_usage` (bandwidth per website, this month) | **shipped** (step 6); requests per second and visitors are not collected |
| Capacity forecasting | `metric_samples` (CPU, memory, disk, load, network for 30 days) | **shipped** (step 6) as `CapacityService` |
| Alerts | `alerts` table and `MonitoringService` rules (disk, memory, load, service, certificate, quota, bandwidth, backup failure) plus `AlertRules` (website down/slow, backup stale, security updates, mail queue, disk filling) | **shipped** (step 7): information severity, acknowledge and resolve, thresholds as settings |

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
3. Security overview and SSL certificates (**shipped**): both read existing read-only agent actions through `SecurityOverviewService`; a source that cannot answer shows N/A (never "fine"), and when none answers the widget is unavailable. Brute-force attempts are the SSH failures from the journal and the panel's own failed sign-ins; the audit's failing and warning checks are counted, high-severity ones separately. Certificates are counted valid, expiring (within the configured warning days) and expired; websites serving HTTPS without a certificate that covers their name are listed.
4. Database, PHP-FPM, Nginx, Redis and Memcached (**shipped**): `ServiceOverviewService` and three widgets (*Databases*, *Redis & Memcached*, *Nginx & PHP-FPM*). One new read-only agent action, `php.fpm_health`, reads the end of each PHP-FPM log of the versions websites use. A source that cannot answer is N/A.
5. Backups, updates, DNS and mail, deployments and WordPress (**shipped**): `OperationsOverviewService` and five widgets. Backups, deployments and WordPress read the panel's own records (no agent call). Storage is already covered by the *Disk* widget (filesystems and where the space goes), so there is no separate one.
6. Traffic intelligence and capacity forecasting (**shipped**): `CapacityService` and two widgets, no agent change. The forecast is a least-squares line through the daily values (the day's fullest disk, the day's average memory) of the last 30 days. It is shown only with at least 7 days of data (days with fewer than 12 samples do not count) and a fit of R² ≥ 0.6; a falling or flat series is "not growing", a scattered one "no clear trend", and with too little history it says how many days it has. A date beyond a year is reported as "not expected within a year". Disk is the fullest filesystem, since the monitor stores only that. Network transfer is estimated from the five-minute samples.
7. Alert center (**shipped**): the existing alert pipeline (one open alert per rule and subject, refreshed while the condition lasts, closed when it ends, notified once on opening and once on closing) gains:
   * **Severity** `info` next to `critical` and `warning`, and **resolved** alerts in the last 24 hours.
   * **Acknowledge** (still open, shown dimmed, not counted by severity) and **Resolve** (close by hand). A hand-closed alert whose condition is still there within a day is reopened as the same alert, acknowledged, with no new notification. Both need `server.manage` and are audited (`alert.acknowledge`, `alert.resolve`).
   * **New rules** in `AlertRules`, fed by Dashboard 2.0's data: website down (N failed checks in a row), slow website, stale backup (only websites with an enabled schedule), security updates waiting, deferred mail, disk forecast. A rule whose source cannot be read (for example the agent for updates) keeps its earlier alert open rather than reporting it resolved.
   * **Central thresholds** as settings in the monitoring category (disk, memory, load per core, website down checks, slow website ms, stale backup days, security updates, deferred mail, disk filling days); 0 turns a rule off.
   * The **Alert center** widget lists open alerts, counts by severity and shows what was resolved in 24 hours.
8. Drag and drop, free resizing and per-widget refresh settings.

Not done in step 1, by design: drag and drop (rows move with arrow buttons for
now), free resizing (four fixed widths), per-widget refresh settings, and moving
the CPU/memory cards, Server, Services, Websites, Network and Recent activity
cards into the registry. They keep working as before.

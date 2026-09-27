# Feature expansion audit (40-section request)

Audited against the code on `claude/sleepy-ptolemy-bdv6ib` (panel migrations
up to `0024_av_scans`, agent actions as registered in `internal/modules`).
Facts that shape the whole plan:

- **Web server is nginx**, not Apache. "Apache configuration" items (index
  protection, ModSecurity) are mapped to nginx equivalents.
- **Mail stack is Postfix + Dovecot + Rspamd** (Rspamd replaces spamd and
  has a built-in greylisting module, currently turned off by WhaCP).
- **PHP runs in one php-fpm master per PHP version**, with a pool per
  website. cgroups apply per process tree, so per-account limits for PHP
  need per-account FPM masters (see D.1).
- Privileged work already goes through the Go agent (fixed action catalogue,
  validation, change sets with rollback, audit log). Everything below keeps
  that model.

## A. Existing (working; extend, do not duplicate)

| Request | Where it is |
| --- | --- |
| 6.1 Hostname | `server.set_hostname`, Server page |
| 6.3 Reboot (confirmation, cancel) | `server.reboot`, `server.reboot_cancel` |
| 6.4 Service restart | `services.control`, Services page (status + restart/reload) |
| 2.4 Email forwarding | mail aliases (`mail.alias_save`), Email → domain |
| 15 DKIM / SPF / DMARC records | generated on mail domains and new zones (`SiteProvisioner`, `DnsService::spf`) |
| 29.1-2 Install a certificate, Let's Encrypt | `ssl.issue`, `ssl.custom`, `ssl.inspect`, per-site SSL page |
| 29.3 AutoSSL | `AutoSslService` + setting `auto_ssl` |
| 4.4 Confirmation before destructive actions | `data-wcp-confirm` modal (optionally type-to-confirm), used on deletes |
| 33 WordPress install | App installer (`apps.install`: WordPress, Laravel) |
| Malware scanning | ClamAV (`av.*`), Security → Malware scan |

## B. Partial (exists, needs enhancement)

| Request | Today | Missing |
| --- | --- | --- |
| 3.1 Disk usage | per-website total (`sites.usage`) | per-directory breakdown, background job |
| 4.1 Central settings | `/settings` (panel), mail/DNS/SSL/server settings on their own pages | one categorised page over the same `SettingsService` |
| 4.2 AJAX / 4.3 modals | forms post with `data-wcp-busy` (full page), a few modals | JSON responses + modal forms across admin pages |
| 7 Monitoring | `/monitoring`: CPU, memory, load, disk, rx/tx history, alerts | top processes, CPU info, per-interface table, live polling page |
| 8 Quota override | plan `disk_quota_mb`, usage measured (not enforced on the filesystem) | per-account override; filesystem enforcement is a separate decision |
| 9 Suspension | website enable/disable; `users.is_active` (login only) | account-level suspend covering sites, mail, DB, FTP/SFTP, cron |
| 11/14 Mail server | `mail.status`, install/configure | status per service, versions, reload, logs, structured settings |
| 18 MariaDB | `db.overview`, databases/users pages | server status/variables, service control, structured config |
| 21 Plan change | plan select on the user page | diff view, immediate re-application of limits |
| 22/23 Bandwidth | monthly bandwidth per site, alerts | exceeders list, suspend from it, reset |
| 30 Database browser | tables, rows, edit/delete, structure, query | search, sort/filter, DDL (columns, indexes, FKs), history, CSV import/export |
| 31 Zone editor | records A/AAAA/CNAME/MX/TXT/NS/SRV/CAA | record-specific forms for all fields, bulk ops, import/export, conflict warnings |
| 33 WordPress | install only | detection, updates, plugins/themes, health |

## C. Missing

1 cgroups · 2.1 email filters · 2.2 email routers (per-domain routing exists as an
account option only) · 2.3 autoresponders · 3.2 index protection (nginx `autoindex`) ·
3.3 permission repair · 5 FFmpeg · 6.2 root password · 10 mail queue manager ·
12 mail statistics · 13 Postfix manager · 15 checker (SPF/DKIM/DMARC/rDNS lookups) ·
16 ModSecurity · 17 web terminal · 19 site IP · 20 addon → account · 24 bulk
termination · 25 feature manager · 26 IP management · 27 greylisting · 28 email
administration tools · 29.4-7 SSL hosts, CSR, storage manager, TLS configuration ·
32 Softaculous.

## D. Architecture changes

1. **cgroups (1, 21, 22).** systemd cgroup v2 slices, one per account
   (`whacp-acct-<user>.slice`, CPUQuota/MemoryMax/TasksMax/IO weights),
   persisted as unit drop-ins (so they survive reboots). To put PHP in the
   account's slice, each account needs its **own php-fpm master unit** in
   that slice instead of a pool in the shared master; cron, workers and
   site-account commands already run as systemd units and can join the
   slice. Network bandwidth is not a cgroup v2 controller; per-account
   shaping needs tc + eBPF or nftables marks, which is a separate project.
2. **Feature lists (25, 5, 32, 33).** New tables `feature_lists`,
   `feature_list_items`, `plans.feature_list_id`; a `Features` service
   consulted by the router (per-route feature key) and by the agent calls
   that grant capabilities (e.g. FFmpeg, shell).
3. **Account suspension (9, 22, 24).** `users.suspended_at` + reason; the
   agent disables sites (maintenance page), mail login, database users,
   FTP/SFTP, cron and workers, and restores them on unsuspend.
4. **IP management (19, 26).** Tables `ips`, `ip_reservations`,
   `ipv6_ranges`, `websites.ipv4/ipv6`; nginx `listen` per site address;
   the agent adds addresses with netplan (Ubuntu) / NetworkManager (RHEL).
5. **Web terminal (17).** Needs a long-lived WebSocket endpoint; php-fpm
   cannot serve one. A small agent-side PTY bridge behind nginx, running
   sessions through the account's jailed shell (depends on shell access).
6. **Mail (2, 10-14, 27, 28).** Sieve scripts per mailbox (filters,
   autoresponders; Dovecot Sieve is installed), Postfix `transport_maps`
   (routers), `postqueue`/`postsuper` (queue), log parsing (statistics),
   Rspamd greylist module (greylisting), structured `postconf -e` settings.
7. **ModSecurity (16).** nginx connector `libnginx-mod-http-modsecurity`
   and OWASP CRS; per-site `modsecurity on|off` + rule set include.

## E. Dependencies

| Need | Ubuntu 22.04 / 24.04 | AlmaLinux 9 |
| --- | --- | --- |
| cgroup v2 + systemd | default | default |
| FFmpeg | `ffmpeg` (universe) | RPM Fusion only (third-party repository) |
| ModSecurity for nginx | `libnginx-mod-http-modsecurity`, `modsecurity-crs` | no package in BaseOS/AppStream/EPEL |
| Greylisting | Rspamd (installed with spam filter) | same |
| Web terminal | none extra if built into the agent | same |
| Softaculous | commercial licence + vendor installer | same |
| Purchase SSL (29.5) | a CA/reseller account and its API | same |
| rDNS | set at the IP provider; WhaCP can only check it | same |

## Blocked or needing a decision

- **Softaculous (32)** and **SSL purchase (29.5)**: need a licence / reseller
  account; they cannot be built or tested without one.
- **Web terminal (17)** builds on shell access (see "Shell access" below).
- **Root password (6.2)** and **shell access**: sensitive; the shell-access
  groundwork is committed but the action that enables it awaits approval.
- **cgroups for PHP (1)**: per-account FPM masters change how every website
  runs; needs agreement before implementation.
- **FFmpeg / ModSecurity on AlmaLinux**: only from third-party repositories.

## Phases

1. Accounts and plans: feature manager, suspension, quota override, plan
   change with diff, bandwidth exceeders + reset, bulk termination.
2. Mail: filters, autoresponders, routers, queue, statistics, service manager,
   Postfix/Dovecot settings, greylisting, checkers.
3. Files and websites: disk usage by folder, index protection, permission
   repair, FFmpeg per plan.
4. Monitoring and server: live stats, CPU info, bandwidth monitor, central
   settings, WhaCP service controls.
5. DNS, SSL and database: zone editor, CSR/SSL hosts/storage, database browser,
   MariaDB manager.
6. IPs: pool, reservations, IPv6 ranges, site IPs, migration wizard.
7. cgroups (after the FPM decision), ModSecurity, WordPress toolkit.
8. Items blocked on external decisions (terminal, Softaculous, SSL purchase).

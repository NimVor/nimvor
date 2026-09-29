# Feature expansion: final implementation report

Covers the 40-section request, from the audit (`ce75a6c`, see
`docs/development/feature-expansion-audit.md`) to `66ceb18` on
`claude/sleepy-ptolemy-bdv6ib`.

**Status:** every requested feature is implemented, except three that
depend on things outside the code (see 3). The one serious gap is
verification. GitHub Actions has not run a single job on this branch for
the whole feature expansion: every run fails within about five seconds
with no runner assigned (see 11). So the real-system tests that only run
in CI have not run for this work:
- the AlmaLinux 9 VM with SELinux;
- installer and upgrade;
- reboot persistence;
- systemd sessions.

## 1. Completed features

| # | Feature | Notes |
| --- | --- | --- |
| 1 | Resource limits (cgroups) | `whacp-acct<ID>.slice` with CPU, memory, tasks and IO limits. Each limited account gets its own PHP-FPM master in its slice. Cron jobs, workers and account commands run in the slice. |
| 2 | Email filters, autoresponders, routers | Sieve per mailbox; Postfix transport maps. |
| 3.1–3.3 | Disk usage by folder, index protection, permission repair | Background measurement; nginx `autoindex` switch; repair never follows links. |
| 4.1 | Central settings | One page by category over `SettingsService`. |
| 4.2 | AJAX operations | Page and header forms submit in the background (`AjaxForms` middleware plus `whacp.js`). Server-side checks are unchanged. |
| 4.3 | Bootstrap modals | Create and edit forms on 11 pages use windows. A refused save reopens its window with the errors. |
| 4.4 | Confirmation | Every form that deletes, removes, drops, terminates, uninstalls, purges, revokes, resets or restores asks first (checked across all views). High-risk ones require typing a word. |
| 5 | FFmpeg | Install/uninstall and version; per plan through a group. |
| 6.1–6.4 | Hostname, root password, reboot, WhaCP service restart | |
| 7.1–7.3 | Top statistics, CPU information, bandwidth | Live page with processes, per-core CPU and per-interface rates. |
| 8 | Quota override | Per-account limit overrides (see 12 for disk). |
| 9 | Account suspension | Websites, mail, databases, SSH/SFTP/FTP, cron, workers; resellers cascade to their customers. |
| 10–14 | Mail queue, server manager, statistics, Postfix and Dovecot settings | |
| 15 | SPF/DKIM/DMARC/rDNS | Checker and troubleshooter; records generated for new zones. |
| 16 | ModSecurity | Nginx connector and CRS; custom rules; per-site modes; events. |
| 17 | Web SSH terminal | Browser → agent WebSocket. Signs in with the account's Linux password (PAM `unix_chkpwd`). Runs with no_new_privs, no capabilities, in the account's slice. |
| 18 | MariaDB management | Status, variables, checked restart, connections. |
| 19, 26 | Site IPs, IP pool, IPv6 ranges, migration wizard, reserved IPs, usage | |
| 20 | Convert addon domain to account | Added last. Files re-owned in place; databases, DNS, mail, cron, workers and FTP move; rolled back on failure. |
| 21–24 | Plan change with comparison, bandwidth exceeders and reset, bulk termination | |
| 25 | Feature Manager | Feature lists on plans, enforced on routes; includes the WordPress Toolkit. |
| 27 | Greylisting | Policy service, trusted hosts, common providers, report. |
| 28 | Advanced email administration | Queue, delivery reports, mailbox permission repair, trace. |
| 29.1–29.4, 29.6–29.7 | CSR and keys, install with validation, AutoSSL, SSL hosts, certificate store, TLS profile | Private keys are never returned. |
| 30 | Advanced database browser | Structure editing, CSV import/export, search, history. |
| 31 | Advanced DNS zone editor | Typed forms, bulk actions, import/export, warnings. |
| 33 | WordPress Toolkit | Scan, overview, security checks, updates, plugins/themes, maintenance, hardening, core verification, one-time admin sign-in. Pinned, checksum-verified WP-CLI runs as the site account without the site's plugins and themes loaded. |

## 2. Partially completed

- **Network bandwidth per account:** the plans' monthly bandwidth is enforced (websites of an account over it answer 509 until the month ends, the limit is raised or the counter is reset). Network speed is not shaped per account: cgroup v2 has no network controller.
- **Disk quota:** enforced with Linux user quotas once an administrator turns them on (ext4 live; XFS after a reboot). Commands needing `CAP_SYS_ADMIN` run in a capability-limited helper unit (owner-approved trade-off). Not yet verified on a real quota-capable kernel (this build machine's kernel has no quota support; CI is not running).
- **FFmpeg and ModSecurity on AlmaLinux 9:** no package exists in BaseOS, AppStream or EPEL. WhaCP detects a third-party installation but does not install one.
- **Web terminal for key-only SSH accounts:** the browser terminal signs in with the Linux password only.

## 3. Remaining (blocked by external dependencies)

- **32 Softaculous integration:** needs a commercial licence and the vendor's installer.
- **29.5 Purchase SSL:** needs a certificate authority or reseller account and its API.
- **rDNS changes:** set at the IP provider; WhaCP can only check them (done in 15).

## 4. Database migrations

`0025_account_suspension`, `0026_limit_overrides`, `0027_feature_lists`,
`0028_mail_routing`, `0029_disk_usage`, `0030_directory_listing`,
`0031_plan_ffmpeg`, `0032_dns_record_disabled`, `0033_ip_management`,
`0034_plan_resource_limits`, `0035_wordpress`.

All of them only add tables, columns or indexes; none rewrites or drops
data. They run forward and are recorded, so they run once. Every panel test
run applies all of them to a fresh database. Upgrading a pre-expansion
installation with data passes here (see 10); on real operating systems it
was not run (see 11).

## 5. New and modified APIs

- **REST API (`/api/v1`):** unchanged.
- **Panel:** new JSON contract for background form submits. The
  `X-WhaCP-Ajax: 1` header turns the final redirect into
  `{"success": true, "redirect": …}`. Normal form posts are unchanged.
- **Agent:** `agent/api/openapi.yaml` is regenerated; see 6.

## 6. New Go-agent functionality

Actions: 167 before the audit, 238 now (71 new). By area:

| Area | New actions |
| --- | --- |
| Mail | 19 |
| Databases | 10 |
| SSL | 8 |
| ModSecurity | 8 |
| WordPress | 4 |
| Terminal | 3 |
| FFmpeg | 3 |
| WhaCP services | 2 |
| Sites (disk usage, permissions) | 2 |
| Shell | 2 |
| Network | 2 |
| Limits | 2 |
| DNS parse/render | 2 |
| System live stats | 1 |
| Root password | 1 |
| Account suspend | 1 |
| FTP (`ftp.move_site`) | 1 |

New packages: `dnsmgr` parse, `ffmpegmgr`, `greylist`, `limits`, `modsec`,
`monitoring` live, `netmgr`, `safewalk`, `spf`, `terminal`, `tlsprofile`,
`wordpress`, plus extensions to `mailmgr`, `dbmgr`, `sslmgr`, `sysmgr`,
`services`, `sites` and `ftpmgr`.

## 7. New PHP functionality

Services:
- **Accounts:** suspension, limit overrides, plan comparison, bandwidth, bulk
  termination, Feature Manager, addon conversion.
- **Mail:** mail administration, greylisting, email authentication checks.
- **Server:** resource limits, shell access, terminal, live monitoring,
  WhaCP services, root password.
- **Security and SSL:** ModSecurity, SSL center.
- **Hosting:** IP management, database server, WordPress.

Panel routes: 266 before, 371 now. Plus the `AjaxForms` middleware and the
`View::modalStart()`/`modalEnd()` helpers.

## 8. New UI pages

`/accounts/bandwidth`, `/features`, `/limits`, `/ffmpeg`, `/ips` (with
`/ips/ranges` and `/ips/migrate`), `/databases/server`, `/monitoring/live`,
`/terminal`, `/wordpress`.

Mail administration:
- `/mail/admin/queue`, `/mail/admin/reports`, `/mail/admin/server`,
  `/mail/admin/tools`, `/mail/admin/greylisting`;
- mailbox filters;
- domain DNS check.

Security: `/security/modsec`, `/security/shell`.

SSL: `/ssl/hosts`, `/ssl/certificates`, `/ssl/keys`, `/ssl/tls`.

Per website: `/sites/{id}/terminal`, `/sites/{id}/firewall`,
`/sites/{id}/wordpress`, `/sites/{id}/disk-usage`,
`/sites/{id}/permissions`. Also `/users/{id}/plan`.

## 9. Tests added

- **Panel HTTP tests (25 new files):**
  - accounts: AccountAdmin, Suspension, FeatureManager, ResourceLimit,
    AddonConversion;
  - server: CentralSettings, WhacpServices, RootPassword, MonitoringLive,
    Ffmpeg, Ip, DbServer, SslCenter;
  - mail: MailAdmin, MailDnsCheck, MailFilters, Greylisting;
  - security and websites: ModSecurity, ShellAccess, Terminal, WordPress,
    DiskUsage, SiteFilesTools;
  - forms: AjaxForms.
- **Agent tests (about 36 new files):** unit, race and error-path tests for
  every new package. Real-host tests (`-tags realhost`) for:
  - limits (systemd);
  - modsec (real nginx and CRS);
  - mail queue, greylisting and authentication checks;
  - dbmgr server;
  - FFmpeg;
  - terminal (`unix_chkpwd`, systemd session);
  - WordPress (real WordPress, MariaDB and WP-CLI).
- **Browser tests:**
  - `tests/e2e/ajaxforms.js`;
  - `tests/e2e/accountadmin.js`: feature list without the WordPress
    Toolkit, plan, Create a New Account, the customer denied WordPress
    (menu and address), suspend with a reason and unsuspend with
    confirmation, plan change page, addon conversion with typed
    confirmation, and phone widths. Three existing browser tests
  that no longer matched the UI (settings tabs, DNS table, record window)
  were fixed.

## 10. Test results (this container, 2026-09-29)

| Suite | Result |
| --- | --- |
| Panel (PHPUnit) | 351 tests, 3,011 assertions: pass |
| Agent (`go test ./...`, `go vet ./...`) | pass; terminal and WordPress packages also pass with `-race` |
| Browser (14 Playwright tests, write-mode dev instance) | all pass |
| Real host, run here | `TestRealHostWordPressToolkit`, `TestRealHostWordPress` (apps), `TestRealHostLinuxPassword` (terminal): pass |
| `make lint` | pass |
| Upgrade (`UPGRADE_FROM=ce75a6c tests/integration/upgrade.sh`) | pass: pre-expansion installation with realistic data (users, websites, databases, backups, API token, encrypted secrets, audit history) upgraded with all migrations; a failing upgrade rolled back; `--rollback` restored the old version |
| Smoke (`tests/integration/smoke.sh`) | pass: agent and panel against the fixture server, sign-in, discovery, pinned remote TLS, fixture unchanged |

The real-host tests that need systemd as PID 1 did not run here (this
container has none). Earlier commits in this work also ran their real-host
tests here where the container allowed it; see each commit message.

## 11. Supported OS verification

- **This container:** Ubuntu 24.04 without systemd as PID 1. Every result
  in 10 comes from here.
- **CI (Ubuntu 22.04, 24.04, 26.04 and AlmaLinux 9 with SELinux
  enforcing):** has not run. The last 40 runs on this branch (runs 129–168)
  failed in about five seconds; their jobs show `runner_id 0`, so no runner
  started them and no logs exist. That usually means the account's Actions
  minutes or spending limit are used up; check the repository's Actions
  billing. Not verified for this work:
  - installation and upgrade;
  - reboot persistence of slices, per-account FPM masters and IPv6 ranges;
  - service restart and failure recovery;
  - SELinux behavior;
  - systemd terminal sessions (`NoNewPrivs`, capabilities, cgroup).

## 12. Known limitations

- **Network bandwidth:** monthly transfer enforced (509); speed not shaped per account (no cgroup v2 controller).
- **Disk quotas:** ext4 and XFS only; email and databases are not counted; unverified on a real kernel until CI or a server run.
- **Web terminal:** password sign-in only. The optional root shell (off by
  default) runs as root and is administrator-only.
- **WordPress Toolkit:**
  - one-click sign-in needs HTTPS on the website;
  - update checks need WordPress.org to be reachable;
  - a site's must-use plugins still load in toolkit commands;
  - a site can falsify its own overview, which affects only that site.
- **Background submits:** forms on pages with their own scripts submit
  normally, and so do the packages and live monitoring pages and the file
  manager (unchanged, as required).
- **Addon conversion:** if it fails after the system account was created,
  that account is left on the server without websites; remove it by hand.
- **Terminal throttling:** in memory, so it resets when the agent restarts;
  it is also keyed by client address, which can be shared behind NAT.
- **AlmaLinux:** no FFmpeg or ModSecurity from the distribution.

## 13. Recommended next steps

1. Restore GitHub Actions (billing or spending limit), then get CI green on
   this branch. This covers the Ubuntu and AlmaLinux VMs, installer,
   upgrade from the last release, reboot persistence and systemd sessions.
   Fix whatever it finds before release.
2. Upgrade a copy of a real installation (with accounts, mail, DNS and SSL)
   to this branch, then check websites, mail delivery and login, DNS
   answers and certificates.
3. Decide on disk quota enforcement (filesystem project quotas) and on
   network shaping (nftables or eBPF) if they are required.
4. Softaculous and SSL purchase once a licence or reseller account exists.
5. Optional: SSH-key sign-in for the web terminal, via an agent-held key
   per session.

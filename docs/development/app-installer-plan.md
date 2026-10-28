# App installer: architecture as found, gaps, and plan

Written from the source (branch `claude/sleepy-ptolemy-bdv6ib`, commit `3e304a2`
and later) before any change for this work. Nothing here is assumed; every
statement was checked in the code or in an upstream response (see
*Upstream facts*).

## 1. How an installation runs today

```
customer ──POST /sites/{id}/apps/{app}──▶ AppsController
   │  permission P::APPS_INSTALL, site found through WebsiteRepository::find(server, id, user)
   ▼
AppInstallerService::install()            panel/app/Services/AppInstallerService.php
   │  AppCatalogService::installableBy()   (catalog row enabled, category enabled, customer_visible)
   │  site must be kind=php with a php_version                       ← PHP only
   │  validates app-specific input (WordPress: title/admin/email; Laravel: folder)
   │  creates a MySQL database + user (DatabaseService::create)       ← before the agent is asked
   │  agent->call('apps.install', params)                             ← on refusal drops the database again
   │  INSERT app_installs (website_id, app, path, database_id, admin_user, job_id, user_id)
   │  audit 'app.install' (request accepted; no password)
   ▼
agent apps.install (Mutating, behind allow_write_actions)   agent/internal/apps/apps.go
   │  Request.validate()  domain/user/root/path/PHP version/db identifiers/url
   │  env.Credential(user)  → the site account (never root)
   │  emptyTarget()         → folder must be empty            ← checked at request time
   │  env.Jobs.Start("apps.install", "site:"+domain, fn)       ← jobs for one site run one after another
   ▼
job: Install()  PHP extension check → mkdir → WordPress (download+sha1, tar, wp-config, php installer)
                                     | Laravel (composer create-project, .env, artisan migrate)
                                     | staged Catalog entry (download, checksum if published, safe extract; the
                                       app's own web installer finishes setup)
                                     | Composer entry / WordPress-plus-plugin entry
   ▼
panel AppInstallerService::refresh()  polls jobs.get; sets app_installs.status; points the document root at
                                      <folder>/public etc.
```

Facts that matter for the next steps:

* **Two catalogs.** The agent's `apps.Catalog` (download, format, strip, docroot,
  extensions, finish path) and the panel's `AppInstallerService::APPS/STAGED` plus the
  `app_categories`/`app_catalog` tables (name, category, description, icon,
  `needs_db`, `customer_visible`, `enabled`). They are kept in step by hand;
  `AppManagerTest` checks that every panel app has a catalog row.
* **Status is only enabled/disabled** (`app_catalog.enabled`, `customer_visible`).
  There is no draft/testing/deprecated state and no manifest validation before
  an entry can be shown to customers.
* **Archive safety exists**: `extractArchive` confines entries (`safeJoin`),
  `safeSymlink`, strip handling, size cap (`maxDownload`, 512 MB), tests in
  `apps_test.go::TestExtractionGuards`.
* **Integrity**: WordPress (sha1 sidecar) and Nextcloud (sha256 sidecar) are verified;
  most other entries are "downloaded over HTTPS, vendor publishes no checksum"
  and say so in the job log.
* **Builds run as the site account** (`execx.Opts{As: cred}`); the `sh` binary in the
  allowlist only runs for unprivileged accounts (`execx.go`), `composer`, `tar`, `git`
  are allowlisted.
* **Node.js / Python**: `runtimemgr` installs side-by-side runtimes under
  `/opt/nimvor/runtimes` (Node from nodejs.org verified against SHASUMS256.txt;
  CPython from python-build-standalone). A Node/Python *site* is `kind=proxy` with an
  `app_runtime` (`node:<v>` / `python:<v>`) and a loopback `proxy_pass`; its process
  is a **worker** (`workers.apply`): `nimvor-worker-<slug>.service` as the site account,
  command in a root-owned launcher script, sandboxed unit (NoNewPrivileges, PrivateTmp,
  read-only system dirs), restart policy, memory limit, status and logs.
* **Reverse proxy**: `sites.apply` renders the vhost, `nginx -t`, reload, with rollback
  (`changeset`); HTTPS through the existing SSL services; SELinux contexts through
  `selinux.Rule/Restore` in `sites`.
* **Databases**: MySQL/MariaDB only (`dbmgr`), plus MongoDB and Redis managers.
* **Backups**: `BackupService` backs up a website's files and *its databases*
  (`databases WHERE website_id = ?`), with restore and schedules.
* **Jobs**: `jobs.Manager.Start(kind, target, fn)` serialises jobs with the same
  target (one mutex per target), bounded concurrency, log per job (secrets redacted
  by `redactingWriter`), timeout, history.
* **Audit**: `AuditLogger` records `app.install` when the request is accepted.

## 2. Gaps found (verified)

| # | Gap | Evidence |
|---|---|---|
| G1 | Installer is PHP-only: `install()` refuses any site that is not `kind=php`. Node/Python/binary apps cannot use it. | `AppInstallerService::install` |
| G2 | No manifest or validation layer; catalog rows are free text plus code constants. No draft/testing/deprecated states. | `AppCatalogService`, migration `0053_app_catalog` |
| G3 | No dry-run plan; the panel creates a database before the agent has accepted the install. | `install()` order |
| G4 | **Duplicate-request race.** `emptyTarget` is checked when the request arrives, not inside the serialised job; two quick requests both pass, and the second job then runs into a folder the first filled. The panel also creates a second database for the second request (`name`, `name1`). No unique constraint on `app_installs`. | `apps.install` handler, `install()`, migration `0008` |
| G5 | The installed **version is not recorded**; no upgrade or uninstall flow exists for any app. | `app_installs` columns |
| G6 | Audit covers the request only: job success/failure and refresh outcomes are not audited. | `AppInstallerService::refresh` |
| G7 | No health check after install; no service status for installed apps. | — |
| G8 | **No PostgreSQL support** anywhere (agent or panel). Applications that require it (Listmonk) cannot be provisioned. | repository search |
| G9 | **No OCI/container runtime support** (no docker/podman allowlist entry, no actions). | repository search, `execx` allowlist |
| G10 | No port allocation or conflict check for loopback apps: a proxied site's port is any number 1024-65535 chosen by the caller. | `WebsiteService::validate`, `sites.checkConflicts` |
| G11 | `pip`, `npm`, `python -m venv` are not allowlisted commands; a build step would have to run through `sh` as the site account with an agent-built fixed script, as the Laravel path does with Composer. | `execx.go` |
| G12 | Plugins/extensions and standalone apps are mixed in one catalog (WooCommerce, bbPress … are WordPress plus a plugin). | `AppInstallerService::WORDPRESS_BASED` |
| G13 | Several staged apps cannot be verified from CI (phpBB, SMF hosts refuse the runners); now flagged `Unverified` in the agent catalog. | `apps.AppDef.Unverified` |

## 3. Baseline (before this work's changes)

Run in the sandbox on commit `3e304a2`:

* `go test -count=1 ./internal/apps ./internal/workers ./internal/runtimemgr ./internal/jobs ./internal/sites ./internal/dbmgr ./internal/egress ./internal/selinux ./internal/execx` → all `ok`.
* `phpunit AppsTest AppManagerTest ApplicationTest` → OK (24 tests, 227 assertions).
* CI: the `App catalog` workflow downloads every catalog archive; PrestaShop, Joomla,
  Nextcloud, OpenCart, Dolibarr, Akaunting, the WordPress plugins and others pass;
  Invoice Ninja was corrected to `Strip 0`; phpBB and SMF are unverifiable from CI.
  No other pre-existing failures were found in the suites above.

## 4. Upstream facts (taken from the source, not from memory)

Collected by the read-only workflow `.github/workflows/app-probe.yml` (run 40608056359 and
the follow-up run, job 122552777120, 2026-10-28). Versions are what upstream reported
at that time; the installer must resolve and **record** a version at plan time and must not
hard-code these.

| App | Source | License | Runtime / notes |
|---|---|---|---|
| BookStack | GitHub `BookStackApp/BookStack`, latest v26.09.2, **no release assets** (source zipball only) | MIT | `composer.json` requires PHP ^8.3.0; official install is Composer-based; MySQL/MariaDB |
| Gitea | `dl.gitea.com/gitea/<ver>/gitea-<ver>-linux-amd64` (119 MB) with `.sha256`, `.asc` (GPG) and sigstore bundles; latest v28.2.0 | MIT | single Go binary, own HTTP server; SQLite/MySQL/PostgreSQL |
| Listmonk | GitHub `knadh/listmonk`; **GitHub's "latest" is `v7.0.0-beta.1`** (a beta, not flagged as a pre-release); the newest non-beta tag in the list is v6.2.0; assets `listmonk_<ver>_linux_amd64.tar.gz` and `listmonk_<ver>_checksums.txt` | AGPL-3.0 | PostgreSQL (per the project's documentation; **NimVor has no PostgreSQL support**) |
| Django | PyPI `django` 6.1.2 | BSD-3-Clause | Python >= 3.12 |
| FastAPI | PyPI `fastapi` 0.162.0 (+ `uvicorn` 0.54.0, `gunicorn` 26.2.0) | MIT / BSD-3 / MIT | Python >= 3.10 |
| Uptime Kuma | GitHub `louislam/uptime-kuma` 2.5.8; only asset `dist.tar.gz` (front end); npm package is `2.0.0-dev.0` | MIT | needs a source checkout + `npm` install; no packaged release |
| Directus | GitHub `directus/directus` v12.6.0 (only SBOM assets); npm `directus` 12.6.0 | `SEE LICENSE IN license` (GitHub: NOASSERTION; not an OSI license: review before offering) | requires Node >= 26 |
| Ghost | GitHub `TryGhost/Ghost` v6.73.0; npm `ghost` 6.73.0 | MIT | Node ^22.23.1 \|\| ^24.20.0; supported install is Ghost-CLI, which manages system services and Nginx itself |

The probe returns versions, licences, asset names and checksum-file availability. The
database engines each project supports and its recommended install method (the
last two columns) come from the projects' documentation as I know it, not from the
probe, and are re-checked by the real-host install test before any entry leaves
`testing`.

## 5. Decisions

* **PostgreSQL (Listmonk) and containers are blocked by G8/G9.** They need a
  foundation of their own (a PostgreSQL manager; an OCI runtime manager with rootless
  containers, no socket exposure, resource limits and SELinux handling). They are
  documented as separate milestones, not shortcuts inside the installer.
* **Directus, Ghost, Uptime Kuma are deferred**: licence review (Directus), a
  Node version newer than most servers have and a service model that bypasses NimVor
  (Ghost-CLI), and no packaged release (Uptime Kuma). They need the Node build strategy
  and a decision on how each is supported.
* **Nothing is enabled for customers without a passing real-host test.** New
  entries start as `testing` (visible to administrators only).

## 6. Milestones

1. **Foundation** (agent + panel, backward compatible): a versioned manifest type, a
   validator, the status model (draft, testing, enabled, deprecated, disabled), trusted
   *strategy* identifiers (no shell in manifests), existing `Catalog` entries expressed
   as manifests, a read-only `apps.plan` action (dry run), version recorded per
   installation, a lock-and-recheck inside the job (G4), outcome auditing (G6).
2. **Native batch 1**: Gitea (binary strategy: checksum-verified download, worker service,
   proxy site, SQLite or MariaDB), Django and FastAPI (virtual environment, pinned
   packages, gunicorn/uvicorn worker). Real-host test in the disposable CI VM before
   any entry leaves `testing`.
3. **BookStack** (Composer strategy with PHP >= 8.3, `.env`, migrations).
4. PostgreSQL manager → Listmonk. 5. Node strategy → Directus/Ghost/Uptime Kuma as
   permitted. 6. OCI container foundation.

## 7. What has been built (and what has not)

Status at the end of milestone 2. "Verified" means a real installation test passed on a
disposable Ubuntu 24.04 VM in CI (`.github/workflows/app-native.yml`, real downloads, real
systemd, real HTTP); unit tests with simulated privileged operations are separate and
are not counted as proof.

| Application | Strategy | Status in the catalog | Real-host test |
|---|---|---|---|
| Gitea | `binary-service` (dl.gitea.com binary, SHA-256 checked against the file beside it and against the GitHub release; SQLite; worker service; loopback only) | testing | `TestRealHostGiteaInstall`: passed (Gitea 28.2.0) |
| Django | `python-venv` (pinned PyPI packages, `requirements.lock`, production settings, secret key in a 0600 file, administrator through one command's environment, Gunicorn worker) | testing | `TestRealHostPythonDjango`: passed (Django 6.1.2, including a real admin sign-in) |
| FastAPI | `python-venv` (Uvicorn worker) | testing | `TestRealHostPythonFastAPI`: passed (0.162.0) |
| BookStack | not implemented | - | - |
| Listmonk | blocked: needs PostgreSQL, which NimVor cannot provision (G8) | - | - |

The three implemented applications are **verified but deliberately left in `testing`**:
administrators can install them; customers cannot until an administrator reviews the flow
and promotes them (`app_catalog.status = 'enabled'`, then the customer-visible switch,
which the panel refuses while an application is not enabled).

Open items: uninstall and upgrade flows (G5; a native installation occupies its proxy
website until a removal flow exists), SELinux-enforcing hosts (not tested; the installer
writes only under the website's folder and creates a systemd unit like any worker), the
container foundation, PostgreSQL, and the Node.js strategy.

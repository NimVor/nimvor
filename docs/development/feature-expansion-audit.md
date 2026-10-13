# Feature-expansion integration audit (2026-10)

This records the eight feature areas added in the `claude/sleepy-ptolemy-bdv6ib`
branch and a go / no-go for each, plus the cross-cutting checks. It supersedes
nothing; it is the hand-off note for real-host validation.

Testing note: this work was built and unit/HTTP-tested in CI-less development
(the real-host VM matrix is billing-blocked). Logic, command composition,
validation, permissions and rollback are covered by Go (`FakeRunner`,
`httptest`) and PHP (`HttpTestCase` with the fake agent transport) tests. Steps
that can only run against real services — package installs, live `nginx -t`,
real downloads, `mongosh`, a real Cloudflare account — are marked **real-host**
and are the agenda for end-to-end verification.

## 1. File Manager security scanner — GO

- Agent `avmgr`: path-scoped `av.scan`, scan-on-upload/extract, quarantine
  listing (`av.quarantine_list`) and admin-only release (`av.release`) that
  re-scans before restoring and is confined with `os.Root` (no symlink
  escape). Infected files quarantined mode 0 outside the web root.
- Panel: scan-on-upload/extract policy and size cap, quarantine card on the
  site Malware page, release gated to `security.manage`, audited.
- Tests: agent `avmgr` suite; panel `MalwareScanTest`, `FilesTest`.
- Real-host: EICAR end-to-end with real ClamAV.

## 2. Background process manager — GO

- The existing workers subsystem (systemd units per site, sandbox, slice,
  memory limit, restart policy, env, live status, logs) was audited and gained
  `numprocs` (N identical copies: primary unit plus `-2`, `-3`… siblings;
  scale-down stops/removes extras; status aggregates counts and memory).
- Tests: agent `workers` suite; panel `SiteToolsTest`.
- Real-host: confirm multi-copy units start and the aggregated status reads
  back on a live systemd.

## 3. Node.js & Python runtime managers — GO (Node) / PARTIAL (Python)

- Agent `runtimemgr`: install Node.js versions side by side under
  `/opt/whacp/runtimes/node/<v>` (official nodejs.org tarball verified against
  SHASUMS256.txt, unpacked in Go with traversal/symlink guards); discover
  managed + system Node/Python; per-worker runtime selection (launcher PATH).
- Panel: Server → Runtimes (install/remove Node, see system interpreters);
  worker Runtime dropdown.
- Python: discovered and selectable; extra versions install via Packages
  (a managed parallel-install build is not included).
- Tests: agent `runtimemgr` + `workers`; panel `RuntimeTest`, `SiteToolsTest`.
- Real-host: a real Node download/verify/unpack and a worker running on it.

## 4A. MongoDB manager — GO

- Agent `mongomgr`: status, overview (databases + users), database
  create/drop, user create/password/drop with scoped roles, via `mongosh`.
  Names validated + single-quote escaped; password-bearing scripts fed on
  stdin (never argv).
- Panel: Server → MongoDB (admin), audited.
- Tests: agent `mongomgr` suite; panel `MongoTest`.
- Real-host: against a live `mongod` (with and without auth / admin URI).

## 4B. MySQL alongside MariaDB — GO (existing)

- `dbmgr`/`mysqlwire` already speak the shared MySQL/MariaDB wire protocol, so
  database/user management works against either server; which server is
  installed is a Packages/Components choice. No new code required.
- Real-host: run the `dbmgr` suite against a real MySQL (not only MariaDB).

## 5. Application installer — GO

- WordPress and Laravel install unattended (unchanged). Seven more apps —
  Joomla, Drupal, phpBB, SMF, PrestaShop, OpenCart, AbanteCart — are
  catalog-driven **staged** installs: download (checksum-verified where the
  vendor publishes one, else HTTPS), unpack (`tar.gz`/`tar.bz2`/`zip` in Go
  with traversal/symlink guards, chown to the site account), set the document
  root, create the database, and hand off to the app's web installer.
- Panel: Apps page offers the staged apps and a "Finish setup" link.
- Tests: agent `apps` suite (framework, checksum, guards); panel `AppsTest`.
- Real-host: confirm each vendor URL, strip depth and document-root subfolder,
  and a full web-installer run per app.

## 6. Cloudflare integration — GO

- Panel `CloudflareService`: one encrypted API token per server; list zones,
  DNS record proxy toggle, SSL mode, Always Use HTTPS, cache purge. Injectable
  HTTP transport; identifiers validated; API errors surfaced.
- Panel: Server → Cloudflare (admin), audited.
- Tests: panel `CloudflareTest` (fake transport).
- Real-host: against a real Cloudflare account/token.

## 7. Nginx config & service manager — GO

- Agent `nginxmgr`: status, `nginx -t`, reload, and http-context tuning
  (client_max_body_size, keepalive/body/send timeouts, gzip, server_tokens)
  as a managed `conf.d` drop-in, validated and auto-rolled-back if invalid.
- Panel: Server → Nginx (admin), bounded values stored as settings, audited.
- Tests: agent `nginxmgr` suite; panel `NginxManagerTest`, `CentralSettingsTest`.
- Real-host: apply tuning and confirm `nginx -t` + reload on a live server.

## Cross-cutting

- **Security** — every new privileged path runs through the existing
  changeset/validator + `execx` allowlist. New allowlist entries: `mongosh`,
  `mongodump`, `mongorestore`; new writable roots: `/opt/whacp/runtimes/**`
  and the single Nginx tuning drop-in. Archive extraction (runtimes, apps)
  guards traversal and symlink escape and caps size. Secrets (DB and Mongo
  passwords, Cloudflare token) never reach argv or the clear.
- **Permissions** — server-wide features (runtimes, MongoDB, Cloudflare,
  Nginx) require `server.view`/`server.manage`; quarantine release requires
  `security.manage`; app install requires `apps.install`. Covered by the
  "customers are 403" tests in each panel suite.
- **Tests** — full Go agent suite green (74 packages); panel suites for the
  new areas green. The one-process full PHP suite can exceed a 300 s
  `max_execution_time` while seeding bcrypt users; run it with
  `-d max_execution_time=0`.
- **OpenAPI** — regenerated for every new agent action.

## Go / no-go

All eight areas are **GO for merge** at the code/unit-test level. The branch
is ready for the real-host pass; the **real-host** items above are its
checklist. No area is blocked.

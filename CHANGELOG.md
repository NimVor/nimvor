# Changelog

All notable changes to WhaCP are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- **Recommended security settings are on after the server setup, and switchable from the Security overview.** A new
  setup step turns on (once) weekly malware scans with quarantine, all five fail2ban jails (now including
  `nginx-limit-req`), compiler access for root only, SMTP restrictions, the cloud-metadata block, cPHulk with its
  recommended limits, shell fork bomb protection and traceroute disabled for users. Security → Overview has a new
  **Security settings** table with an Enable/Disable button for each of them, plus ModSecurity (with a link to install
  it when its Nginx connector is missing).
- **Runtimes: pick versions from a list.** Server → Runtimes now lists the stable Node.js versions (the newest of each
  recent LTS line, plus Current) and the stable Python versions as checkboxes; tick the ones to install instead of
  typing an exact version. Python can now be installed here too, from python-build-standalone prebuilt CPython
  releases. Every download is verified against its published SHA-256 before it is unpacked. New agent action
  `runtime.available`; `runtime.install` accepts `kind: python`.

### Fixed
- **Security → Hulk: the search boxes did nothing and the country list had no select-all.** Table rows were not
  marked for the page's filter, so searching countries or failed logins never hid anything (and neither list was
  paged). Rows are now searchable and paged, and the country table has a header checkbox that ticks or unticks the
  visible countries.
- **The scheduler stopped at the WordPress vulnerability check** ("no such column: w.account_suspended"), so it never
  finished a run, never recorded that it had run, and the health page kept warning that it had never run. The query
  now computes the suspension from the owner.

### Fixed
- **"Scheduler has never run" after installing.** The scheduler timer fired only 2 minutes after boot and then every 5
  minutes after a previous run, so on a server that was already up it never started until the next reboot. It now also
  starts one minute after the timer is enabled.

### Fixed
- **The old host name kept showing in the header and the System tab after a change.** The agent runs in its own host-name
  namespace (`ProtectHostname=yes`), so it kept reporting the name from when it started. It now reads `/etc/hostname`.

### Fixed
- **Setup wizard: the FTP server step failed** with "pure-ftpd.service is running and uses the FTP port". Installing the
  pure-ftpd package starts its own default service, which the step took for another FTP server. The setup now lets
  the agent replace that default service when it has no virtual accounts; any other running FTP server still needs
  confirmation.

### Changed
- **Back to the PHP panel with the Go agent.** The repository is restored to the PHP 8.3 panel (`panel/`) plus the Go
  agent (`agent/`), as in the 1.0.0-rc1 backup. The experimental Go-only panel (`server/`) was removed from the tree;
  it remains in the git history (last commit with it: `884c341`).

### Fixed
- **MongoDB was unreachable from the customer panel.** A website's customer
  dashboard is a fixed tile grid, and it had no MongoDB tile (only MySQL), so
  a hosting customer had no way in even though the menu, routes and
  permissions existed. Added a MongoDB tile and quick-search entries, gated
  exactly like the others (shown only when the customer's plan includes the
  MongoDB feature and they hold the permission); when the plan excludes it the
  tile is hidden and the routes return 403, and another customer's database
  stays 404. Also fixed a latent fatal: the Mongo export helper had been added
  as `Controller::download()`, clashing with `FilesController::download()` and
  breaking the File Manager download whenever that controller loaded; the
  Mongo helper is now `downloadExport()`.

### Added
- **Remote MongoDB servers (Remote DB / Get DB from server).** The MongoDB
  manager gained a Remote servers tab to save connections to other MongoDB
  servers (name, host, port, optional user/password, auth database, TLS). The
  connection is tested before it is saved and the password is stored encrypted
  (libsodium) and never shown again. From a saved server, “Get DB” lists its
  databases and copies a chosen one onto this server (mongodump → mongorestore,
  as a background job, with an optional drop-first and a rename). New agent
  actions `mongo.remote_ping`, `mongo.remote_databases` and `mongo.remote_pull`
  keep credentials off the command line: mongosh receives the connection
  string on stdin, and mongodump reads the password from a short-lived
  root-only `--config` file that is removed immediately after use.
- **MongoDB Databases grid shows user, position and an editable note.** The
  server MongoDB manager's Databases table now lists, per database, its first
  database user, size, backup status (with a one-click back-up when none
  exists), position (Localhost), and a free-text administrator note, with
  per-row Browse / change-password / drop actions. The toolbar gained Root
  password and Security-authentication buttons. Notes are stored panel-side
  and are admin-only; per WhaCP's secrets policy the panel still never stores
  or reveals a database user's password — the per-row key resets it instead.
- **MongoDB browser redesigned as a three-pane explorer.** The full-page
  browser now has a databases/collections sidebar, a centre pane with a
  breadcrumb, collection actions (Insert Document, Import, Export, rename /
  empty / drop), a query bar with a page-size selector, the view tabs
  (Documents, Aggregation, Indexes, Schema, Validation, Stats, Users) and a
  documents **table** view (with a JSON view toggle) and numbered paging; and
  a right rail showing the selected document (with Copy), the collection's
  indexes, its statistics and quick actions. A new read-only agent action
  `mongo.collection_stats` (document/data/storage/index sizes, counts and the
  collection's validation rules) backs the stats rail and the new Validation
  and Stats tabs, confined per-account on the customer browser.

### Changed
- **MongoDB browser toolbar no longer clips.** The collection name, the five
  view tabs, the JSON filter/sort/fields inputs and the action buttons were
  all crammed onto one non-wrapping row, which squeezed the filter box down
  to a few characters with the Run button on top of it. The view tabs now sit
  on their own row, and the query inputs and actions wrap cleanly with the
  filter given room to grow. Empty collections and empty filter results now
  show a centred icon state instead of a bare line of text.
- **Redis and Memcached pages follow the panel UI rules.** Both server pages
  no longer stack every section as a column of cards: they use tabs
  (Overview / Website users / Security / Danger zone for Redis;
  Overview / Danger zone for Memcached), move configuration behind modals
  (Redis memory &amp; eviction, the Redis website-user create/reset form, and
  Memcached settings), and give the Redis website-user list a filterable
  table. No behaviour or route changes.

### Security
- **GA security program (WhaCP 1.0).** Closed the boundaries that could allow
  cross-customer compromise, privilege escalation, sandbox escape or unsafe
  privileged operations, each with permanent tests:
  - **Cross-site PHP isolation (item 50):** a real-host test runs as a real
    website account and proves it cannot reach another site's files, PHP-FPM
    socket, worker environment or the host's secrets.
  - **Filesystem TOCTOU / race (item 181):** a concurrent symlink-swap test;
    it also found and fixed a nil-pointer crash in the file-manager write
    path under concurrent modification.
  - **Sandbox escape (items 90–91):** a real-host probe confirms the kernel
    denies shared-`/tmp`, `/proc`, device, privilege-gain and SUID escapes.
  - **Agent RPC adversarial audit:** every validator is driven with
    traversal, null-byte, shell-metacharacter, Unicode and over-long inputs;
    `GitRef` tightened to git's own refname rule.
  - **File integrity monitoring (items 104–106):** `dpkg --verify` / `rpm -Va`
    plus a WhaCP baseline, graded by severity (`security.integrity`).
  - **TLS configuration audit (items 70–71):** read-only probe of the
    server's own endpoints for weak protocols, weak ciphers and certificate
    problems (`tls.audit`).
  - **Loopback / shared-cache control (item 80):** opt-in nftables isolation
    stops one website reaching another's Redis/Memcached on loopback, with
    per-account exceptions; off by default.
  - **Secure PHP profiles (items 33–35):** Recommended / Balanced /
    Compatibility / Custom `disable_functions`; Recommended is the default
    for new websites; existing sites are unchanged.
  - **Automatic restore rollback (item 152):** a failed website-file restore
    puts the previous files back from the safety backup and verifies them.
  - **OS vulnerability advisories (item 164):** OS-native advisories only
    (`packages.advisories`), with no automatic upgrades.
  - **Negative tests:** a route sweep confirms bad object ids, traversal
    parameters, missing sessions and invalid API tokens deny cleanly (never a
    500 with a side effect).
  - Status: implemented and passing locally; the cross-OS real-host matrix
    (Ubuntu 22.04/24.04, AlmaLinux 9, Rocky 9) is the remaining GA gate.

### Fixed
- **Addon domains now count against the website plan limit.** The `websites`
  limit counted and enforced only top-level sites, so addon domains slipped
  past a plan's website cap (a 5-website plan could hold 4 primary sites plus
  extra addon domains and still read "4/5"). Addon domains and directly
  created sites now both count toward the `websites` limit; subdomains keep
  their own separate limit and do not count. Usage display uses the same
  count, so an over-limit account is correctly shown and blocked.
- **App-installer category switching and two other pages no longer break
  under the Content-Security-Policy.** The panel's CSP is `script-src 'self'`,
  which blocks inline `<script>`; the Website → Apps category switcher, the
  Website Settings PHP-profile toggle, and the MongoDB change-password modal
  fill were inline scripts and so silently stopped working (clicking an app
  category did nothing). They moved to delegated handlers in `whacp.js`, and a
  `ViewTest` guard now fails the build on any inline `<script>` in a template.
- **Webmail's self-signed certificate follows a host-name change.** Webmail
  only generated its self-signed certificate when none existed, so after the
  server was renamed, re-applying webmail on the new name (when it has no
  Let's Encrypt certificate yet) reused the old certificate and kept
  presenting the previous host name. It now regenerates the self-signed
  certificate whenever the existing one does not cover the current webmail
  host name.
- **Update no longer reverts the panel's certificate.** `update.sh` runs
  `install.sh --reinstall`, which rewrote the panel's HTTPS Nginx site and so
  reset its `ssl_certificate` to the self-signed default — discarding a
  certificate chosen on SSL/TLS → Services SSL (e.g. a Let's Encrypt one),
  which then showed as "unused". The installer now reads the certificate the
  existing panel site already uses and keeps it; only a fresh install (no
  panel site yet) falls back to the self-signed default.
- **Host-name certificate after a rename.** The temporary Nginx site that
  answers Let's Encrypt's HTTP challenge for the server host name was created
  once and never rewritten, so after the host name changed it still carried
  the old `server_name`; Nginx no longer matched the new name and the
  challenge returned 404 (`unauthorized … Invalid response … 404`). It is now
  rewritten whenever the host name differs, so re-issuing the host-name
  certificate works after a rename.
- **Placeholder contact email no longer blocks all SSL.** If the admin
  address was left at a reserved/example domain (e.g. the install docs'
  `you@example.com`), Let's Encrypt rejected the account with "contact email
  has forbidden domain" and *every* certificate — website and host-name —
  failed to issue. WhaCP now drops such an address and issues the
  certificate without a contact, noting in the job log that a real email
  should be set for renewal notices.
- **Clearer Let's Encrypt failures.** When certbot cannot reach Let's
  Encrypt to register the account (connection refused, DNS failure, timeout,
  or a wrong server clock), the error now says to check outbound HTTPS to
  `acme-v02.api.letsencrypt.org`, DNS and the clock — instead of the
  inbound "point your DNS here and open port 80" advice, which only fits an
  authorization failure. Rate-limit responses are called out too. Applies to
  both website and host-name certificates.
- **Host name change now propagates.** Changing the server's host name
  rewrites `/etc/hosts` so the machine resolves its own name to its real
  address instead of Debian's `127.0.1.1` placeholder (which failed the mail
  host-name check), and — when mail is set up — re-applies the mail server so
  Postfix and Dovecot pick up the new name, rather than leaving each service
  file to be edited by hand.
- **Tweak Settings, Security policies tab:** saving now needs the password
  confirmed in the last 15 minutes, like Security → Configure Security
  Policies. Without it, the tab bypassed that step-up check.
- **Kernel security settings:** the check and its fix now include
  reverse-path filtering (`net.ipv4.conf.all.rp_filter`; loose mode 2 is
  applied, strict mode 1 also passes).
- **PHP 8.5, OPcache:** OPcache is built into PHP from 8.5 and has no
  package of its own. WhaCP looked for `php8.5-opcache`, so a profile with
  OPcache (the default) never matched; the setup wizard reported "PHP 8.5
  opcache (install)". OPcache now counts as built in on PHP 8.5 and later:
  shown as installed and on, with nothing to install, remove or switch.
- **Backups, local folder storage:**
  - `/backups` (also `/backup`, `/srv/backup(s)`) is accepted itself, not only
    a folder in it.
  - When missing, the folder is created (root only) when the storage is
    tested.
  - Folders under `/mnt` and `/media` are still never created: a missing one
    means the disk is not mounted, and the message now says so.
- **Setup wizard, Redis memory limit:** the step called an agent action
  that does not exist (`redis.status`); it now reads `redis.info`. A new
  test checks that every agent action the panel calls exists in the agent.
- **Setup wizard, PHP and packages:** a step whose job succeeded but left
  something undone no longer repeats the same job until it gives up.
  - The PHP step now names what is still to do, for example "PHP 8.4 intl
    (install)".
  - The packages step names the packages its job did not install.

### Changed
- **Page buttons moved out of the top bar:** each page's own buttons
  (e.g. "Create account", "New user", "Save" in the editor) now sit at the
  top right of the page. The "Back to …" buttons were removed: the sidebar
  and its sub-menus lead everywhere.
- **ModSecurity page in tabs:** Websites, Recent events, Vendors, Custom
  rules, Installation. The server-wide settings open in a window from the
  "Server-wide settings" button.
- **Sidebar:** "Users & roles" is now two pages, **Users** and **Roles**,
  and **Bandwidth Limits** has its own entry. **Logs** has a sub-menu for
  every kind of log: web server, PHP-FPM, mail, database, security, SSL,
  system, services and WhaCP logs, and links to the audit log, AutoSSL
  log, ModSecurity events, mail delivery reports and failed logins.
- **Roles:**
  - Every permission in the role editor lists the pages it opens. The list
    is built from the panel's routes, so it stays complete when pages are
    added.
  - The descriptions were updated for the newer pages: ModSecurity, the
    process manager, IP addresses, mail and database servers, bandwidth,
    the root terminal, and more.
  - The editor has a search box and "All" / "None" for each group.
- **Built-in roles are editable** (Operator, Viewer, Reseller, Hosting
  customer):
  - A changed role keeps its permissions when WhaCP is updated;
    **Reset to WhaCP defaults** brings back the defaults.
  - Administrator stays full access.
  - Nobody can grant a permission they do not hold themselves.
  - Reseller and customer roles cannot be given server-wide permissions.
- **Packages:**
  - Stats at the top: updates pending, security updates, the package index
    date, and components installed, not installed, failed or not usable,
    and in the queue.
  - **Updates** and **Components** are in tabs.
- **Installation queue window:**
  - Two panes: on the left the queue, with each component's state
    (queued, in process, installed, failed); on the right the output of
    the selected component (by default the one installing).
  - Several components can be ticked and added at once while the window
    is open.
  - "Installation queue" sits at the top right of the Packages page.
- **Resource limits:** Limited accounts and Disk quotas are in tabs;
  "Apply the plans now" is at the top right.
- **IP addresses:** "Add an address" and "Add an IPv6 range" open windows
  from buttons at the top right.
- **Servers:** "Connect a server" opens a window from the top right.
- **Server:**
  - System, Disks, Network interfaces and Server settings are in tabs.
  - Each setting has its own window: Host name, Time zone, Root password,
    Graphical login screen, Restart the server.
  - After a save, the page returns to the Server settings tab.
- **Backups page in tabs:** Website backups, Server configuration, Remote
  storage, Account backups. Backing up the server configuration, the
  server backup schedule and adding or editing a storage open in windows.
- **Plans:** a new plan and edits to a plan are done in a window ("New
  plan" at the top right, the pencil on each row).
- **Custom rules one at a time:** the Custom rules tab lists the rules in a
  grid (ID, description, phase, action). "New rule" adds one in a window;
  each rule can be edited or deleted. Every change is checked by Nginx
  before it is used. Editing all rules as text is still possible.

### Added
- **MongoDB configuration and log (Server → MongoDB).** View and edit the
  common settings — port, bind IP and whether authentication is required —
  with the raw `mongod.conf` shown read-only; saving validates the values,
  writes the file through the agent's allowlisted writer, restarts MongoDB,
  and **restores the previous configuration automatically if it fails to
  start**. The page also shows the tail of MongoDB's log. Editing needs
  `server.manage`.
- **MongoDB aggregation: explain plan.** The Aggregation tab in the database
  browser gains an Explain button beside Run that shows the query planner's plan
  for the current pipeline without executing it, so an index miss (a `COLLSCAN`)
  is visible before running a heavy pipeline. The pipeline is EJSON-parsed
  inside `mongosh` as data; read-only (`server.view` / site owner), confined to
  the account's own databases on the customer page. New agent action
  `mongo.explain`.
- **MongoDB documents: sort and field projection.** The database browser's
  Documents view gains a Sort box and a Fields box next to the filter, so the
  result can be ordered (e.g. `{ "age": -1 }`) and narrowed to selected fields
  (e.g. `{ "name": 1 }`). Both are JSON objects parsed with EJSON inside
  `mongosh` as data, validated and bounded; they apply on the admin and the
  customer page alike, confined to the account's own databases on the latter.
- **Customer MongoDB page redesigned into tabs (Site → MongoDB).** Like the
  admin page, the stacked Databases / Users / Backups cards are now tabs with a
  filter box each, and only the active tab's data is loaded; all create/edit
  stays in modals.
- **MongoDB admin page redesigned into tabs (Server → MongoDB).** The stacked
  cards (Databases, Users, Backups, Configuration, Log) are now tabs, so the
  page stays clean as the number of databases grows and only the active tab's
  data is fetched (a large server's config and log are no longer read on every
  view). Every change is a modal — add database (optionally with its user), add
  user, change password, admin password, the structured settings and the raw
  config-file editor all open as dialogs. The Databases, Users and Backups
  tables gained an instant filter box, and the Databases table has select-all
  and a **Delete selected** bulk action (new `mongo.database_bulk_drop`).
- **MongoDB collection management in the browser.** The browser's collections
  sidebar now creates a collection (the "+" button), and each collection has an
  actions menu to empty it (remove all documents, keep indexes), rename it, or
  drop it — the missing create/drop/empty/rename a professional browser needs.
  New agent actions `mongo.collection_create` / `_drop` / `_empty` / `_rename`
  (a rename never overwrites an existing collection; a `system.` name is
  refused). Writes need `mongo.manage` on the admin page and are confined to the
  account's own databases on the customer page.
- **MongoDB raw configuration-file editor (Server → MongoDB).** The
  Configuration card can now edit the whole `mongod.conf` directly (admins with
  `mongo.manage`), not just the structured port/bindIp/authorization fields.
  MongoDB has no offline config check, so WhaCP writes the file, restarts, and
  restores the previous file automatically if the service will not start —
  never leaving MongoDB down because of a bad edit. New agent action
  `mongo.config_write`.
- **MongoDB databases list shows backup status (Server → MongoDB).** Each
  database row now has a Backup column: how many archives it has, or a one-click
  "Back up" when it has none. (MongoDB users are independent of databases and
  their passwords are not stored by the panel, so there is no per-row stored
  password to reveal — unlike a MySQL-style model.)
- **MongoDB admin password and authentication toggle (Server → MongoDB).** Two
  top actions: set the WhaCP MongoDB administrator's password (creates or resets
  the `whacpAdmin` account in the `admin` database and stores only a root-only
  connection string), and turn authentication on or off (restarts the service).
  Enabling authentication is **refused unless an admin password is set first**,
  so the panel and the server owner can never be locked out; the config change
  rolls back on a failed restart, and the stored credentials are dropped only
  after MongoDB is confirmed running with authentication off. New agent actions
  `mongo.admin_password` and `mongo.auth_set`; passwords are parsed inside
  `mongosh` and percent-encoded into the stored URI, never placed on the command
  line. (Needs verification against a live mongod before being relied on.)
- **Dedicated MongoDB permissions.** Admin MongoDB access now has its own
  permission pair — **View MongoDB** (`mongo.view`) and **Manage MongoDB**
  (`mongo.manage`) — instead of riding on the broad `server.view` /
  `server.manage`. A role can now be granted MongoDB data and user management
  without full server control, or be allowed to view it only, and the Server →
  MongoDB page, its browser, exports, data/user/config changes and
  backups/restores are gated accordingly (installing the MongoDB package and
  controlling the service still need `packages.manage` / `services.manage`).
  The Administrator role is unchanged (it has everything); Operator gains both
  MongoDB permissions and Viewer gains view. Customers' per-site MongoDB stays
  gated by the `mongodb` plan feature and website ownership as before.
- **MongoDB backup and restore for customers (Site → MongoDB).** The Backups
  card is now on the per-customer page too: a hosting user backs up, restores
  and deletes archives of their own databases, as background jobs shown in the
  job log. Every operation is confined to the account's prefix — the archive's
  database is read from its file name and checked for ownership, so a customer
  can neither back up, restore nor delete another account's data, and only their
  own archives are listed.
- **MongoDB backup and restore (Server → MongoDB).** A Backups card on the
  admin page backs up a database to a gzip `mongodump` archive kept on the
  server, lists the archives with their size and date, restores a database from
  one, and deletes archives — backup and restore run as background jobs and are
  tracked like the install jobs. Archives live under the agent's backup
  directory; a strict file-name check means a restore or delete can never reach
  a path outside it, and a restore is confined to the chosen database
  (`--nsInclude`). Administrator only (`server.manage`); the agent actions are
  `mongo.backup`, `mongo.backups`, `mongo.restore` and `mongo.backup_delete`.
- **MongoDB user management in the browser.** A Users tab in the database
  browser lists the users defined on the selected database with their roles,
  and (for an administrator with `server.manage`, or the site owner) creates a
  user with a role on that database, changes a user's password, or drops a user
  — all through a small form, no shell. Passwords are passed to the agent as a
  JSON argument parsed inside `mongosh`, never on the command line. On the
  customer **Site → MongoDB** page the created user name is prefixed with the
  account's system user and every operation is confined to the account's own
  databases and users (refused before the agent otherwise). New agent action
  `mongo.database_users` lists one database's users.
- **MongoDB schema analysis in the browser.** A Schema tab in the database
  browser samples the current collection (up to 1000 documents) and shows each
  top-level field with the BSON types it holds and how often it is present, so
  the shape of a schemaless collection is visible at a glance. The analysis is a
  single server-side aggregation (`$sample` → `$objectToArray` → `$type`),
  bounded by a sample cap and `maxTimeMS`; no document data crosses into the
  pipeline beyond the validated database and collection names. Read-only
  (`server.view` / site owner), confined to the account's own databases on the
  customer **Site → MongoDB** page.
- **MongoDB collection import in the browser.** An Import button in the database
  browser loads documents into the current collection from a pasted JSON array
  or JSONL (one object per line), or from a chosen `.json` / `.jsonl` file read
  in the browser. The upload is parsed and bounded by the agent (at most 5000
  documents, 4 MB; every element must be a JSON object) and inserted one by one
  so a document whose `_id` already exists is counted and skipped rather than
  aborting the rest; the result reports how many were inserted and how many were
  skipped. Importing is a write (`server.manage` on the admin page, the site
  owner on the customer **Site → MongoDB** page) and the customer page is
  confined to the account's own databases.
- **MongoDB collection export in the browser.** An Export menu in the database
  browser downloads the current collection, respecting the active JSON filter,
  as either a JSON array (`.json`) or one document per line (`.jsonl`). The
  documents are serialised with EJSON inside `mongosh` so BSON types survive
  the round trip, and the export is bounded (default 2000, capped at 5000
  documents) so a large collection cannot exhaust memory. The download is a
  read (`server.view` / site owner); on the customer **Site → MongoDB** page it
  is confined to the account's own databases, and a filename built from the
  database and collection with unsafe characters stripped cannot inject into
  the response headers.
- **MongoDB aggregation (query lab) in the browser.** A new Aggregation tab
  runs a pipeline (a JSON array of stages) against the current collection and
  shows the results with the execution time. The pipeline is parsed with EJSON
  inside `mongosh` (data, not code), a trailing `$limit` bounds the output and
  `maxTimeMS` bounds the run. Read-only (`server.view` / site owner), confined
  to the account's own databases on the customer page.
- **MongoDB index management in the browser.** A new Indexes tab in the
  database browser lists a collection's indexes (keys and unique / sparse /
  TTL / text properties), creates single-field or compound indexes with those
  options through a form, and drops an index (the `_id` index is refused). On
  the admin page creating/dropping needs `server.manage`; on the customer page
  it is the site owner, confined to the account's own databases. Index
  definitions are validated and passed to `mongosh` as data (EJSON), no inline
  script. Also fixes the document editor's save, which called an undefined
  helper.
- **MongoDB document editing in the browser.** The database browser can now
  insert a new document, edit a document in place, and duplicate one, with a
  JSON/Extended-JSON editor; values round-trip through EJSON so BSON types
  (ObjectId, dates, numbers) are preserved, and `replaceOne` enforces
  MongoDB's immutable-`_id` rule. Editing needs `server.manage` (admin) or the
  site owner (customer page, confined to the account's own databases). Driven
  by `mongosh` with the document passed as data on stdin (never the command
  line); no inline script.
- **MongoDB database browser (Server → MongoDB).** A full-page browser (the
  same tool layout as the SQL web browser) reached from a **Browse** button on
  each database: a collections sidebar with document counts and a filter, and a
  documents pane with a JSON filter, paging, and per-document delete. The agent
  reads through `mongosh` with the filter and `_id` passed as data (parsed with
  EJSON, fed on stdin), never as command line or code; reading needs
  `server.view` and deleting a document needs `server.manage`. The browser's
  behaviour lives in `assets/js/mongobrowser.js` (no inline script, per the
  panel CSP). The same browser is on the customer **Site → MongoDB** page,
  confined to the account's own (prefixed) databases: it lists and browses
  only those, and collections/documents/delete refuse any database outside the
  account's prefix before the agent is called.
- **App installer can target the website or one of its subdomains.** The
  install modal now shows an "Install on" selector (whenever the website has
  subdomains) listing the website itself and each of its subdomains; the
  application installs into the chosen site's own document root and the page
  shows that target's progress and "Finish setup" link. Non-PHP targets are
  shown disabled and rejected server-side; addon domains keep their own Apps
  page.
- **Customer portal: plan-controlled features and new per-site pages.** The
  Feature Manager gained flags (grouped in the admin UI) for MongoDB,
  Memcached, Cloudflare, Node.js, Python, the Web firewall, Site security,
  Site health, and the five site-creation types, plus a default-off mechanism
  (a feature is off unless the plan opts in) used for showing scheduled
  backups to customers and for creating Node.js/Python/reverse-proxy sites.
  Building on it:
  - **Subdomains and Addon domains** are now separate pages with separate
    feature flags.
  - **Site-creation types** (PHP, static, Node.js, Python, reverse proxy) are
    gated per plan and enforced server-side; PHP and static are on by default.
  - **Automatic (scheduled) backups** are hidden from customers unless the
    plan enables `auto_backups` (off by default).
  - **Per-site Memcached** (connection help) and the **Web firewall**
    (ModSecurity) are feature-gated per site.
  - **Site security** and **Site health** pages give customers a plain-language
    check of HTTPS, certificate, malware, PHP version, uptime and backups,
    each with how to fix it and who can.
  - **Per-site Cloudflare** lets a customer connect their own Cloudflare API
    token (stored encrypted) and manage SSL mode, Always-HTTPS, the proxy and
    cache for just their zone.
  - **Per-account MongoDB** lets customers manage their own databases and
    users, name-prefixed and tenant-isolated like MySQL.
  - **Node.js and Python are first-class site types** with a managed runtime
    version, an auto-started app Worker, and a per-site Runtime page to change
    the version.
- **MongoDB installation (Server → MongoDB, Components, Packages).** MongoDB
  is not in the base operating-system repositories, so the MongoDB page now
  installs it in two guided steps: *Add repository* adds MongoDB Inc.'s own
  signed repository (on APT the signing key is fetched over TLS from MongoDB's
  official key server and trusted for that repository only; on the RHEL family
  a per-repo `gpgkey` is used), and *Install MongoDB* installs `mongodb-org`
  and enables and starts the `mongod` service. The page also starts, stops and
  restarts the service. MongoDB now appears on the Components page (install via
  its page, then start / start-at-boot) and `mongodb-org` is listed on the
  Packages page. New agent actions: `mongo.repository`,
  `mongo.repository.enable`, `mongo.install`. Tracks MongoDB 8.0 by default.
- **Host-name references audit (Server → "find the old name").** After a
  host-name change, names recorded earlier — DNS zone records, Nginx server
  names, Postfix's greeting — keep the old value. A read-only scan
  (`server.hostname_references`) finds every place a given previous host name
  still appears in `/etc/hosts`, WhaCP's Nginx sites, the hosted DNS zones and
  the mail server, grouped with guidance and deep links, plus one-click
  "Re-apply mail server" (when Postfix still greets with the old name) and
  "Re-apply webmail" (when Roundcube still answers on it with a self-signed
  certificate — re-applying picks up the host name's Let's Encrypt
  certificate). It only reads configuration; customer DNS zones are listed,
  never rewritten automatically.
- **Choice of certificate authority: Let's Encrypt, ZeroSSL or Buypass.**
  WhaCP can now issue from ZeroSSL and Buypass Go in addition to Let's
  Encrypt, still through certbot and the same webroot/renewal/deploy
  machinery. A certificate-authority setting selects the active provider for
  new and automatic SSL, the host name and services; Let's Encrypt stays the
  default and existing certificates and renewals are unchanged. ZeroSSL needs
  External Account Binding credentials (an EAB key id and HMAC key from your
  ZeroSSL account), entered once at registration and never stored. The
  provider's directory URL is a fixed, vetted constant (certbot's `--server`
  is never given a free-form URL).
- **ACME account card (SSL/TLS → Services SSL).** A card shows whether
  certbot holds an account for the chosen provider, its contact email and
  account URL, warns when none is registered, and lets administrators pick a
  provider and register (or update the contact) up front after accepting its
  Terms of Service — instead of relying on implicit registration at first
  issuance (where a bad contact or missing EAB silently fails every
  certificate). Reserved/example and invalid addresses are refused with a
  clear message.
- **Server → Health:** one page that shows whether anything is wrong.
  - It gathers the panel's checks: WhaCP itself (the doctor checks),
    resources, components, the security audit, updates, certificates,
    failed backups and tasks.
  - The worst items come first, each with a link to its fix.
  - Parts the agent cannot answer are marked *Not checked*.
- **Basic WebHost Settings** (Administration), with four tabs: contact
  email for alerts, basic configuration (panel name and URL, default IPv4
  for new zones, hostmaster), name servers, and defaults for new accounts.
  The account and website forms start with these defaults:
  - plan and PHP version;
  - DKIM, SPF and DMARC records;
  - email routing;
  - spam filter and Spam folder.
- **Tweak Settings** (Administration): every panel setting in tabs, with
  its default, a *Changed* mark, **Reset to default** per setting or per
  tab, search across all tabs, and an "only changed" filter.
  - Security policies and brute force protection need an administrator,
    as on their own pages.
  - Saving brute force settings applies its rules.
  - Settings whose page prevents lock-outs (brute force on/off, verified
    IP addresses) stay on that page.
- **Settings with a fixed list of values** are now checked against it,
  for example the brute force protection type.
- **Setup wizard** (Server → Setup wizard): opens on an administrator's
  first dashboard visit, unless the installer already set the server up.
  - **What is done and what is left:** the server setup, components, two-factor
    sign-in, remote backups and the backup key, a first hosting account.
  - **Profile and options:** Full, Web only or Minimal (recommended by
    memory), with the host name, PHP versions and third-party repository
    choice.
  - **Steps run from the browser**, one request each. A step that installs
    packages follows its job log and is verified when the job ends.
  - **Results are kept**, and a dashboard card leads back to the wizard
    until it is closed.
- **Server profiles in the installer** (`--profile=full|web|minimal|none`;
  default by memory on a fresh server, `none` where web, mail, DNS or
  database services already run). After installing, `bin/whacp
  setup:apply` sets the server up through the panel's own actions:
  - packages, then PHP 8.3 + 8.4 with the default extensions (`--php=`);
  - email (`--hostname=`), DNS (authoritative only) and FTP;
  - a Redis memory limit, then services started and enabled at boot;
  - the firewall with the components' ports and protections;
  - Fail2ban, kernel hardening, SMTP restrictions (full profile only) and
    automatic security updates;
  - hosting plans (Starter, Business, Pro, Email only) with feature lists;
  - weekly backups.

  Steps are idempotent: nothing is removed and existing settings are left
  alone. Guarded firewall changes are kept only when SSH, the panel and the
  agent stay reachable. The installer checks memory, disk, outgoing port 25
  and reverse DNS. It states the third-party PHP repository it would add
  (Ondřej Surý's PPA or Remi); `--no-third-party-repos` keeps to the
  system's repositories. `bin/whacp setup:status` shows the last run.
- **Backups for every website**: a schedule can back up every website (each
  its own backup, two at a time, retention per website, websites added
  later included) as well as the server configuration.
- **PHP Manager** (EasyApache style): tick PHP versions and extensions,
  review what changes (install, update, switch on or off, remove, kept,
  unchanged), apply as one job; the PHP profile is kept and can be exported
  and imported. Versions used by websites or by the panel are never
  removed. Agent: `php.profile_plan`, `php.profile_apply`, `php.catalog`.
- **Server → Components**: every part of the server (web, PHP, database,
  email, DNS, FTP, Redis, Memcached, firewall, Fail2ban, ClamAV, Let's
  Encrypt) with its steps (packages, set up, running, starts at boot,
  ports open) and a fix per step; *What this server should have* (the
  server profile). The agent's `components.status` reports the steps;
  `firewall.allow_ports` opens a component's ports in one guarded change.
- **Firewall protections in the manner of CSF** (*Security → Firewall →
  Protection*), next to UFW or firewalld (no second firewall manager):
  invalid-packet filter, per-address SYN flood, connection and port flood
  limits per port, ping rate limit and exempt addresses, in the nftables
  table `inet whacp_flood` (it only drops or rejects). Changes are undone
  after 2 minutes unless confirmed; the page shows what each protection
  dropped. Brute force protection can block blacklisted countries on every
  port (CC_DENY), not only the login services.
- **More PHP versions from the PHP page.** A button adds the repository
  with other PHP versions after a confirmation: on Ubuntu Ondřej Surý's
  PPA, with its signing key pinned in the agent (fingerprint
  `14AA40EC0831756756D7F66C4F4EA0AAE5267A6C`) and trusted for that
  repository only (`Signed-By`); on the RHEL family Remi's repository.
  If APT refuses the PPA it is removed again. It needs the
  *Install and update packages* permission (operators cannot add it).
  A PHP version installed only as the command line now offers
  *Install PHP-FPM*.
- **Account backups** (*Backups → Account backups*):
  - **What the file holds.** One `.whacp-account` file per hosting
    account, with every website's files and databases (encrypted) and the
    account's description: profile, plan, websites, databases and their
    users, DNS zones and cron jobs.
  - **Where files come from.** Download it, or upload a file. Files copied
    to `/backup`, `/backups`, `/srv/backup(s)`, `/mnt` or `/media` are
    listed too.
  - **Restoring.** Restoring checks the file first: it must be made with
    this server's backup key, its description is authenticated, and the
    checksums must match. It then recreates the account with a new
    password shown once, its websites, databases, database users, DNS
    zones and cron jobs, and restores each website.
  - **When.** It works after the account was terminated, and on another
    server once that server's key is imported.
- **Backup key download and import:**
  - The key can be downloaded encrypted with a passphrase (PBKDF2-SHA256,
    AES-256-GCM, done by the agent).
  - Administrators only, after re-entering the panel password. Every
    download is audited.
  - A key file is imported on a new server when that server has no key
    yet.
  - See the trade-off documented under *Backups → The backup key*.
- **Several remote storages:**
  - Each storage can be tested again, turned on or off (on only after a
    passing test) and edited (settings tested before they are saved;
    secrets kept when left empty).
  - Only storages that are turned on are offered for new backups.
  - A schedule whose storage was turned off keeps its backups locally.
- **SSL/TLS → AutoSSL** (like cPanel's Manage AutoSSL):
  - The provider (Let's Encrypt), when the next check runs, and whether
    renewals are scheduled.
  - **Run AutoSSL for all websites:** checks every website at once.
  - The websites still waiting, an **AutoSSL log** of every attempt
    (issued, failed, skipped, queued), and **certbot's renewal log**.
- **New setting "Replace expired or invalid certificates"** (on by
  default): every 6 hours, a website gets a new Let's Encrypt certificate
  once its domain points here if its certificate:
  - has expired;
  - expires within 7 days without being renewed;
  - does not cover its domain;
  - or is self-signed.
- **SSL in the sidebar** has its own pages as sub-menus (SSL hosts, AutoSSL,
  Certificates, Keys and CSRs, TLS configuration, Services SSL). Each is
  also a card under *Tools & settings* on the dashboard.
- **Sidebar groups fold:** click a group's name. The choice is remembered
  in the browser, and the group of the current page always stays open.
- **Sidebar sub-menus open in place:** clicking Security, SSL, Email,
  Databases or Monitoring shows its pages without loading one first.
  Group headings are closer together. The logo, status and search stay at
  the top while only the menu scrolls.
- **The menu button hides and shows the sidebar on wide screens too** (the
  choice is remembered).

### Changed
- The profile block at the bottom of the sidebar was removed (the account
  menu in the top right has Profile and Sign out).

### Added
- **Top bar for server administrators:** the host name, the operating
  system, the WhaCP version and the load averages (1, 5 and 15 minutes,
  refreshed every 5 seconds, and the same as the dashboard's Load card). Clicking the load opens live monitoring.
  The tool search moved to the sidebar, above Dashboard, and the
  "Write mode" label was removed from the sidebar. "Read-only mode" and
  "Agent offline" are still shown there.
- **Three buttons in the dashboard's Server card:**
  - **Restart all services:** every running service WhaCP manages, in
    order: databases and caches, PHP, mail, DNS and FTP, then Nginx after a
    configuration test, then the agent and the panel. SSH is never
    restarted.
  - **Restart the server:** asks for the host name; it can still be
    cancelled on the Server page.
  - **Update WhaCP:** shown only when a newer version exists. The scheduler
    looks for a newer commit on GitHub every six hours (the agent itself has
    no network access). The update page runs `installer/update.sh` in its
    own systemd unit and shows its output while it runs; the panel and the
    agent restart during it. It needs a recent password confirmation.
- **Process manager (Monitoring → Processes):**
  - Lists every process with its owner, priority, CPU and memory use,
    running time and command line.
  - **Trace** shows a process's program, folder, status, open files and
    connections, and five seconds of system calls (when strace is
    installed; it is now on the Packages page).
  - **Stop** (TERM) and **Kill** (KILL) a single process, or every process
    of one account. Root and WhaCP's own account are refused.
  - **Suspicious processes** are flagged:
    - names of known miners or backdoors, and mining pool addresses;
    - programs deleted from disk after they started (not merely replaced
      by an update, which is shown as a note);
    - programs run from /tmp, /var/tmp, /dev/shm or hidden folders;
    - scripts started from temporary folders;
    - user programs that pretend to be kernel threads;
    - shells or interpreters of website accounts that listen on a port.
- **SSL/TLS → Services:**
  - Choose the certificate of the panel's HTTPS site and of the mail
    server (Postfix and Dovecot). Each can go back to its previous
    certificate.
  - Get a Let's Encrypt certificate for the server's host name.
  - Set the mail server's TLS protocols (TLS 1.2+ or TLS 1.3 only) and
    ciphers. These settings survive a mail reconfiguration.
  - The panel's HTTPS follows the server-wide TLS configuration once a
    certificate is chosen.
  - Certificate renewals reload Postfix and Dovecot too.
- **Graphical login screen (Server page):** turn GDM, LightDM or SDDM off
  (boot to the text console, saving GNOME Shell's 250–400 MB of memory), or
  on again. Nothing is uninstalled.
- **Notification bell dropdown:** clicking the bell shows the newest
  unread notifications (a notification marked read leaves the list, so it is
  empty once everything is read; the full page keeps them all). Each can be marked read without leaving the page, there is
  a "Mark all read" button, and "View all notifications" opens the full page.
- **Dashboard Services widget:** start or stop, and restart, each service
  from its row (for users who may manage services). A service listed under
  two names (mariadb/mysqld) appears once.
- **Tools & settings** list each Security page as its own card, like the
  other sections.
- **Security → Overview fixes findings with one click**, each with an undo:
  - **Kernel security settings:** the recommended values are applied and
    kept in `/etc/sysctl.d/90-whacp-hardening.conf`; undo puts the earlier
    values back. On a server that routes traffic for others, redirects are
    left on.
  - **Process visibility:** `/proc` uses `hidepid=2`, so each account sees
    only its own processes. Root and the group `whacp-proc` (polkit is
    added to it) still see all processes. The setting is kept in
    `/etc/fstab`.
  - **Automatic security updates:** unattended-upgrades on Debian/Ubuntu.
    On RHEL, dnf-automatic installs security updates only, uses WhaCP's own
    configuration and never restarts the server. The package is installed
    when missing.
  - **Database network access:** the database server accepts local
    connections only (bind-address 127.0.0.1, also under Databases →
    Server). This is refused while database users connect from other hosts.
- **Administrator SSH keys in the panel** (Security → SSH). You can add or
  remove the public keys of root and administrators (members of sudo,
  admin or wheel), so password logins can then be turned off without
  locking anyone out. Only plain Ed25519, ECDSA and RSA keys of at least
  2048 bits are accepted. **Generate a new key** creates the key pair in the
  panel (RSA 4096, protected with an optional passphrase), allows it at once
  and offers the private key for download one time; WhaCP does not keep it.
  Changes need an administrator who confirmed
  their password recently, and are audited. The last key cannot be
  removed while password logins are off.
- **Broken package state is repaired automatically.** After an update, the
  agent checks the installed packages. It reinstalls packages whose install
  script failed (for example under the older agent's sandbox) and installs
  missing dependencies, as a job on Server → Packages. Server → Packages
  lists any problem left. Removing duplicate package versions needs the
  administrator's confirmation (**Remove duplicates**), because it removes
  packages.

### Security
- **Websites' PHP runs in a sandbox.** PHP-FPM services get a systemd
  drop-in: PHP code cannot gain privileges through setuid programs
  (NoNewPrivileges), the service keeps only the capabilities it needs, and
  the system is read-only for it. Resource-limited accounts' own PHP-FPM
  services get the same settings.
- **Every cron job runs in a sandbox**, not only those of accounts with
  resource limits. Each job runs as the website account in a transient
  service, with no capabilities and the system read-only, like the other
  commands WhaCP runs as that account. Updates rewrite existing schedules
  in the new form (`bin/whacp cron:sync`).
- **Websites cannot reach the cloud metadata service** (169.254.169.254 and
  the other providers' addresses), which hands out the server's cloud
  credentials. Security → Outgoing Connections can also block private
  networks and allow only chosen remote ports for websites' code. Root and
  the system's services are not affected.

### Fixed
- **Brotli on Ubuntu/Debian:**
  - WhaCP installed `libnginx-mod-brotli`, a name these systems don't
    have. Their package is `libnginx-mod-http-brotli-filter`, which WhaCP
    now installs.
  - Components a repository does not list at all are now also shown as
    "Not available on this system".
- **Packages:** components the system's repositories do not offer (Brotli
  and the ModSecurity Nginx module on Ubuntu 22.04) are shown as "Not
  available on this system" instead of an Install button that always
  failed. ModSecurity links to its build from source.
- **Installation queue window:** the queue is narrower and scrolls, keeps
  the component being installed in view, and installed components leave
  the list (failed ones stay with Retry). The last output stays shown.
- **Installer on Ubuntu 22.04:**
  - **PHP:** the installer stopped with only a warning about PHP 8.1. It
    now installs PHP 8.3 for the panel from the `ondrej/php` PPA. The
    system's default `php` is left as it was, and the panel's services,
    Composer and `upgrade.sh` use the panel's own PHP.
  - **Go:** the agent did not build with the distribution's Go 1.18. When
    the system's Go is too old, the installer now downloads Go 1.24 from
    go.dev into `/usr/local/lib/whacp-go` and checks its SHA-256 first.
- **Recovery from remote storage refused real website backups:**
  - The authentication of a website backup's manifest covered its panel
    metadata as sent. The manifest is stored indented, which changed those
    bytes, so every website backup made from the panel failed the check.
    *Find backups* on a new server listed them all as "not authenticated".
  - The check now covers the metadata in compact form.
  - Backups made before the fix verify too.
- **Building the ModSecurity connector failed on Rocky/Alma 9 and Ubuntu
  22.04** ("the HTTP rewrite module requires the PCRE library"). Nginx
  older than 1.21.5 (Rocky/Alma 9 ship 1.20, Ubuntu 22.04 ships 1.18) only
  works with the original PCRE library, and the build installed only
  PCRE2. For those versions the build now also installs `pcre-devel` /
  `libpcre3-dev`, and removes it again if the installation fails.
- **A failed ModSecurity installation filled the page with build output**,
  one word per line in a narrow column. The installation history now shows
  the build's error lines (or its last lines), with the full output behind
  "Show the full output" in a scrollable box.
- **"Get a certificate for the host name" ended without a message.** The
  agent stopped the action after its default 30 seconds while certbot was
  still working. It now runs as a job: the job window shows certbot's
  output, and the result stays in the job history. Other new actions that
  reload services got longer time limits too.
- The top bar's load refresh started an extra timer each time a page
  loaded in place.
- **WhaCP's greylisting and mail-limit services were flagged as suspicious
  processes after an update.** They run the agent's program and were not
  restarted by the installer, so they kept running the replaced (deleted)
  version. Now:
  - the installer and `upgrade.sh` restart them where they run;
  - the process manager reports a program replaced by an update as a note
    ("updated after it started; restart it"), not as suspicious;
  - accounts without a name (systemd's dynamic users) show their service.
- **"Renewal of Let's Encrypt certificates: not scheduled" on Rocky/Alma.**
  EPEL's certbot installs `certbot-renew.timer` but does not turn it on,
  and WhaCP looked only for Debian's `certbot.timer`. WhaCP now turns the
  timer on in three cases:
  - after issuing a certificate;
  - when the agent starts and certificates exist;
  - from a new "Turn on renewal" button on SSL/TLS → SSL hosts.
- **Process visibility stayed "not hidden" in the security overview** after
  it was turned on. The agent read its own mount namespace's view of /proc;
  it now reads the host's (PID 1's) mount table. A post-install test checks
  it through the installed agent.
- **World-writable findings stayed listed after a fix** until the next weekly
  scan, including the sockets an older scan reported. The overview now checks
  each listed file again when it is shown.
- **clamav-freshclam showed as not installed after installing it** on
  Rocky/Alma. EPEL 9 and 10 call the package clamav-freshclam (clamav-update
  before), so WhaCP looked for the old name.
- **ImageMagick could not be installed on Rocky/Alma** ("is not a valid
  package name"). RPM package names may have capital letters and
  underscores.
- **The security overview no longer lists sockets as world-writable files**
  (for example the database server's `mysql.sock`). A socket must be
  writable to be connected to; the service decides who may use it.
- **Webmail and PHP websites answered 502 on Rocky/Alma when a Remi PHP
  version had been installed without its SELinux setup.** This happened
  when its install scripts failed, for example under the older agent. Its
  PHP-FPM then ran outside the web server's SELinux domain, and SELinux
  refused nginx the socket (`connect() ... (13: Permission denied)`). Before
  loading PHP-FPM, WhaCP now checks the version's SELinux labels. If they
  are missing, it records the same rules Remi's scripts would have,
  relabels the files and restarts PHP-FPM.
- **Webmail and PHP websites on Rocky/Alma with only Remi's PHP:** WhaCP
  never created `/run/php-fpm`, where its PHP-FPM pools put their sockets.
  Without RHEL's own php-fpm package the folder did not exist, so the pool
  could not start and Nginx answered 502. WhaCP now creates it (labelled for
  SELinux) and keeps it across reboots (tmpfiles.d).
- **Webmail failures now explain themselves:** a page that does not load
  shows Nginx's error lines for the webmail socket, the PHP-FPM service's
  state, journal and log, and the socket.

### Changed
- **Updates need one command and no manual steps:** `sudo
  ./installer/update.sh`. It gives root-owned files in the checkout back to
  their owner, pulls as that owner and reinstalls. Reinstalls keep the
  panel's port and public HTTPS port, so no options need repeating.
  Dependencies are installed by default (`--no-install-deps` to opt out).
  The installer gives `.git`, `dist/` and `panel/vendor` back to the
  checkout's owner instead of asking you to, and refreshes `panel/vendor`
  when `composer.lock` changed.

### Fixed
- **Rspamd on Rocky/Alma** failed: "rspamd is not available". EPEL does
  not carry it. WhaCP now adds the Rspamd project's own signed repository
  when rspamd is installed.
- **`--public-https` on servers with a long host name** (cloud names over
  64 characters) failed: "the certificate could not be created". The
  certificate's CN now uses the short name when needed; the full name stays
  in subjectAltName, which is what browsers check.
- **Web terminal with a jailed shell** failed to start the jail ("bwrap:
  Can't mount proc"). Two of the session unit's /proc protections stopped
  the jail from mounting its own /proc. A jailed session now leaves those
  two to the jail. Inside the jail, `/proc/sys` is read-only and kernel
  logs are root-only. The CI test checks both. Every other setting
  (no_new_privs, no capabilities, ProtectSystem, the account's limits)
  stays on the session unit.
- **opendkim and other EPEL packages on Rocky/Alma** failed with "nothing
  provides libmilter…". WhaCP now enables CRB, which EPEL's packages need,
  and tries again. If it still fails, it names the repository that
  provides the missing library.
- **Updating left the old agent running.** If Go was not found, the
  installer used to install the outdated `dist/whacp-agent` after a
  `git pull`. It now stops and asks for `--install-deps`. The agent's
  version also names the commit it was built from.
- **Greylisting and mail limits on SELinux systems (Rocky, Alma, RHEL)
  had no effect.** SELinux stopped systemd from creating their sockets in
  Postfix's private folder, and stopped Postfix's smtpd from connecting to
  the services. The installer now loads a small policy module that allows
  exactly these two things. SELinux stays enforcing. Update with the
  installer, or run `installer/upgrade.sh`, to load it.
- **Package installation on Ubuntu 24.04 and Rocky Linux / RHEL with
  SELinux.** Package managers ran inside the agent's systemd sandbox. On
  Ubuntu 24.04 APT could not switch to its download user
  (`seteuid 42 failed`, `Method http has died`). On SELinux-enforcing RHEL
  systems RPM could not run package scripts (`Error in PREIN scriptlet in
  rpm package dovecot`, likewise redis and Remi's PHP), while `sudo dnf`
  worked. Package transactions now run in their own short-lived systemd unit
  with the environment of `sudo apt-get`/`dnf-automatic`. The agent keeps
  its sandbox. See docs/architecture/package-management.md.
- **One package transaction at a time** across all jobs, and across agent
  restarts. APT waits for the dpkg lock. A transaction is never stopped
  half-way.
- **Specific errors instead of "dnf install failed".** Failures are
  classified (package script with package, stage and exit status; not
  available; repository; lock; dependency; signature; RPM database;
  interrupted dpkg; disk full) and recovered where that is safe: waiting,
  index refresh, `dpkg --configure -a`, `rpm --rebuilddb`. At most three
  attempts; signature checks are never bypassed and dependencies never
  forced.
- **Repositories on Rocky/Alma/RHEL** are prepared when a package needs
  them: Remi for PHP versions, EPEL with CRB for the rest. A package still
  missing is reported as not available, naming the enabled repositories.
- **Installed means working.** After installing, Nginx, Redis, MariaDB and
  Memcached are started and checked (`nginx -t`, `redis-cli ping`,
  `mysqladmin ping`); Postfix, Dovecot and BIND configurations are
  validated; new PHP versions must run and pass `php-fpm -t`. A service
  that does not start is reported with its cause (port conflict, SELinux
  denial, permissions, …) and its log.
- **Installer:** finds Go and Composer outside sudo's PATH and reports
  their version and location; installs them with `--install-deps`; waits for
  the APT lock; enables CRB with EPEL; prints the detected environment;
  `--resume` continues an interrupted installation.
- The package queue window shows each component's step (preparing
  repositories, installing, recovering, health check) and, on failure,
  the diagnosis.
- CI: Ubuntu 24.04 joins the virtual-machine matrix, and the post-install
  test installs a component through the installed agent.

### Added
- **Security hardening (medium-severity audit findings).**
  - Website nginx and PHP-FPM configurations are no longer readable by
    other accounts (0640, tightened on existing servers at agent start).
  - Safer PHP session defaults: HttpOnly cookies, strict session IDs,
    cookie-only sessions, and Secure cookies on HTTPS-only websites. A
    website can still change them.
  - New setting: new websites start with shell functions disabled.
  - Outgoing mail limits per website account (Email → Sending limits).
    Recipients are counted per hour at the mail server, PHP's mail() goes
    through WhaCP's sendmail, and administrators and the website's owner
    are notified when an account hits its limit.
  - New setting: hosting customers may be kept to local-only database
    users.
  - New security audit checks:
    - /proc process visibility (hidepid);
    - kernel security settings;
    - database exposure: listening address, remote administrators,
      accounts without a password, global privileges, GRANT OPTION and
      the test database;
    - a daily host scan: set-user-ID programs and file capabilities not
      installed by a package, world-writable system files, and cron jobs,
      services or preloaded libraries not from a package or WhaCP.
    Checks only report; nothing is changed automatically.
- **Security hardening (low-severity audit findings).**
  - WordPress one-time sign-in links work only over HTTPS.
  - New servers use Mozilla's intermediate TLS ciphers.
  - The security audit warns when the package index is older than a week.
  - Websites can opt in to security headers: Referrer-Policy,
    X-Frame-Options, Permissions-Policy and upgrade-insecure-requests.
  - The security audit flags a BIND open resolver.
  - Changes to users, roles, API tokens and sign-in policies ask for the
    password again when it was last confirmed more than 15 minutes ago.
  - The profile has "Sign out other sessions".
  - Website accounts can no longer create personal crontabs or at jobs:
    scheduled tasks go through the panel.
- **Turning the firewall on keeps you connected.** The Firewall page lists
  the ports WhaCP needs (SSH, the panel's HTTPS port, the agent's remote
  listener, and the ports of running services: websites, webmail, mail,
  FTP with its passive range, DNS) and whether a rule allows each. Turning
  the firewall on first opens SSH and the panel, optionally the services'
  ports and every port for your own address (also whitelisted in cPHulk),
  in one guarded change: without **Keep change** within 2 minutes the
  firewall goes off again and those rules are removed. The last rule
  opening SSH or the panel cannot be deleted while the firewall is on.
- **Security menu in the sidebar.** Overview, Firewall, SSH, Brute-force
  protection, Malware scan, Shell access, ModSecurity, Configure Security
  Policies, Compiler Access, SMTP Restrictions, cPHulk Brute Force
  Protection, Password Strength Configuration, Shell Fork Bomb Protection
  and Traceroute Enable/Disable are pages of their own. Email and Databases
  have sidebar menus too (mail server, mailboxes, queue, delivery reports,
  statistics, greylisting, tools; databases and users, database server,
  settings, connections, logs); the new DNS zone form is a modal.
- **cPHulk Brute Force Protection.** Failed logins from WhaCP, webmail, SSH,
  IMAP/POP3, SMTP AUTH and FTP lock the account (WhaCP sign-ins) or block
  the address, for the protection period or one day; blocks apply to WhaCP
  and the login services, or at the firewall level (nftables, expiring on
  their own). Commands with the cPHulk variables (run as the panel's
  account, 15-second limit, shell-quoted values), root login and brute
  force notifications, an IP whitelist, whitelisted and blacklisted
  countries (DB-IP Lite, downloaded monthly or imported), a failed logins
  report, unblock and "Remove Blocks and clear report".
- **Configure Security Policies.** Limit logins to verified IP addresses
  (an emailed code for a new address), two-factor authentication per role,
  password strength and password age.
- **Password Strength Configuration.** Minimum strength and counts of
  special, numeric, alphabet, upper and lower case characters and length,
  for WhaCP users, mailbox passwords and the root password.
- **Compiler Access, SMTP Restrictions, Shell Fork Bomb Protection and
  Traceroute Enable/Disable.** Compilers for root and chosen accounts only;
  only root, the mail server, Mailman and the panel may reach remote SMTP
  servers; process caps for accounts' sessions; traceroute for root only.
- **Security overview checks:** cPHulk, MySQL anonymous users (with a
  remove button), supported database version, strong password rules,
  "nobody" may not send email (Postfix authorized_submit_users), SSH
  version, fork bomb protection and traceroute.
- **Refreshed administrator and reseller interface.** Softer cards, a
  polished sidebar with an account card, a sticky top bar and **global
  search (Ctrl+K)** over every tool the viewer may open, with quick actions.
  The dashboard opens with a welcome strip and quick actions, colour-coded
  metrics and a **Tools & settings** grid of every area. **Resellers get
  their own dashboard** (customers, websites, plan use, notifications)
  instead of the server's, which showed server details they do not manage.
- **WordPress Toolkit, second part.** The installation page has a header
  with status and a tool strip (File Manager at the installation, Database
  browser, Logs, Clone, Sync, Security, Back up, Restore points) and tabs.
  New: debugging switches, password protection, wp-cron.php takeover (a
  cron job), hotlink protection, vulnerability protection (ModSecurity with
  the OWASP CRS plus daily checks against the WPVulnerability database),
  administrator and database password changes, the debug log, twelve
  security hardening measures each with rollback ("Secure" applies the
  recommended nine), automatic hardening of new installations, and staging
  (clone to another website with its own database, sync files and/or the
  database back, restore points saved before anything is replaced).
- **Dependencies are detected by capability.** Nginx modules (ModSecurity,
  Brotli) are missing, installed but not usable (with the reason) or
  working, whatever installed them (package, rpm, built from source, copied
  by hand, built into Nginx): module file, libraries resolved like the
  dynamic linker, `load_module` in the active configuration, test load.
  Programs (FFmpeg, Composer, Git, Certbot, ClamAV) count when present.
  The Packages page shows the real state and skips packages for what
  already works.
- **ModSecurity installer with backup and rollback.** *Security →
  ModSecurity → Install ModSecurity* installs the connector as one
  transaction: snapshot first; the distribution package when it would not
  touch Nginx, otherwise the official connector built for the exact
  installed Nginx from verified sources as a temporary unprivileged
  account; versioned files, test load, atomic `load_module` switch,
  `nginx -t`, reload; any failure rolls everything back (also after an
  interrupted run). Manual rollback on the page and with
  `whacp modsec:rollback <id>`; records kept for a configurable time, the
  one in effect always. *Install Core Rule Set* separately (package or
  pinned release), starting in detection only; a new *Detection only*
  switch holds blocking until an administrator allows it.
- **File Manager trash.** Delete now moves files and folders to the
  website's trash; *Trash* in the toolbar restores them to where they were,
  deletes them permanently or empties the trash (with confirmation). Items
  are deleted automatically after a configurable number of days (default
  30). "Delete permanently instead" skips the trash. Backups leave the
  trash out; trashed files still count towards the disk quota.
- **Disk quotas are enforced.** Server → Resource limits → Disk quotas turns
  Linux user quotas on for the websites' file system (ext4: live remount,
  count, `/etc/fstab`; XFS: `/etc/fstab` and `rootflags`, then a reboot).
  Each account's plan disk space (with overrides; a reseller's covers its
  customers) is then set as its system users' hard limit, shared between
  them and re-divided every scheduler pass. Quota commands run in a
  short-lived systemd unit limited to `CAP_SYS_ADMIN`; the agent itself gets
  no new capability.
- **Monthly bandwidth is enforced.** An account over its plan's monthly
  bandwidth (a reseller's limit covers its customers) is blocked: its
  websites answer "509 Bandwidth limit exceeded" (certificate renewals still
  work) until the month ends, the plan or an override allows more, or an
  administrator resets the counter. Usage is now read every scheduler pass
  (5 minutes) instead of hourly. Blocks and unblocks are audited, the
  account is notified, the website dashboard and Users → Bandwidth over
  limit show the state. Administrators' websites are never blocked.

### Security
- **Accounts with administrator rights never run website code.** The agent
  refuses to use an existing Linux account as a website account, or to run
  any command (PHP-FPM pool, cron, workers, deploys, WP-CLI, terminal) as
  one, when it is in a privileged group (sudo, wheel, adm, docker, lxd,
  disk, shadow, …) or named in `/etc/sudoers` / `/etc/sudoers.d`. Before,
  a compromised panel could name such an account and gain root through its
  sudo rights.
- **ModSecurity build is not trusted blindly.** A pre-existing
  `whacp-build` account that WhaCP did not create is refused (the name is
  now reserved for websites), build outputs are read without following
  links and only from inside the build folder, and installed files are
  copied only from regular files.
- SSH/SFTP keys, passwords and shells are never given to an account with
  administrator rights.

### Fixed
- Installing the Core Rule Set no longer overwrites a website's ModSecurity
  mode changed at the same time.

### Changed
- **File Manager paths:** any path through a `.whacp-*` folder (trash,
  quarantine, temporary files) is refused, not only one ending in it.
- **Web terminal sign-in uses the Linux account's password.** A website's
  terminal now signs in as its Linux account with that account's own
  password (the SSH/SFTP one in `/etc/shadow`), instead of re-confirming
  the panel password.
  - The browser sends the password to the agent only, inside the
    WebSocket; the agent checks it with PAM's `unix_chkpwd`. The panel
    never receives, stores or logs it.
  - Wrong passwords: three tries per connection, and five per 15 minutes
    per account or address block sign-in.
  - Changing the password, turning shell access or password login off,
    or suspending the account ends its open terminals.
  - Accounts in privileged groups get no terminal.
  - Root shells (when enabled) still confirm the panel password and
    authenticator code.

### Added
- **Convert an addon domain into an account** (*Subdomains & addons* →
  *Convert to account*).
  - Creates a new login and system account; nothing is copied.
  - Files are re-owned in place; databases (and database users only they
    use), the DNS zone and the email domain change owner.
  - Cron jobs, workers and FTP accounts (with their passwords) move to the
    new account; the domain's subdomains go with it.
  - Everything is put back if a step fails.
- **Forms without page reloads.** Create, change and delete forms submit
  in the background.
  - The page updates in place, keeps its scroll position and shows the
    result as a notification.
  - The server's checks (CSRF, permissions, validation) are unchanged.
  - The file manager and pages with their own scripts submit as before.
- **Create and edit forms in windows (Bootstrap modals).** Databases and
  users, FTP accounts, mailboxes and forwarders, subdomains and addon
  domains, backups, cron jobs, workers, firewall rules, IP addresses,
  ModSecurity per website, and the FTP and Fail2ban settings.
  - A refused save reopens the window with its errors.
  - Links such as `#new-database` open the window.
- **WordPress Toolkit** (*WordPress* in the sidebar, and a *WordPress*
  page per website).
  - Finds installations in websites by reading files only, as the
    website account.
  - Shows versions, updates, plugins, themes and the database, with
    security and health checks and fixes.
  - Management: core, plugin and theme updates (background jobs);
    plugins and themes on or off, with automatic updates; automatic core
    updates; maintenance mode; hardening; search-engine visibility; core
    file verification.
  - One-time administrator sign-in links (HTTPS only).
  - Runs a pinned, checksum-verified WP-CLI as the website account,
    without the site's plugins and themes. Controlled by the Feature
    Manager (*WordPress Toolkit*).
- New WordPress installations get the standard "That's all, stop
  editing!" line in `wp-config.php`, so tools can add settings to it.
- **ModSecurity manager** (Security → ModSecurity): the Nginx connector
  with the OWASP Core Rule Set (paranoia level, anomaly thresholds) and
  validated custom rules as rule vendors.
  - Per website: off, detection only or on, which vendors apply, and
    single rules turned off.
  - Recent blocked/detected events, with "turn this rule off here".
  - *Update rules* updates the package; if the new rules do not load,
    the Core Rule Set is turned off automatically.
  - Every change checked with `nginx -t` and rolled back on failure.
  - Website owners get a Web firewall page to switch their own website's
    mode; rules stay with administrators.
- **Web terminal**: a shell in the browser (xterm.js) on each website's
  Terminal page, for accounts with shell access (jailed or normal) whose
  plan includes SSH.
  - Opening one asks for the password (and authenticator code) again.
  - The agent hands out single-use tickets bound to the browser's
    address and serves the WebSocket on loopback behind the panel's
    Nginx site (`install.sh --public-https` sets it up).
  - Each session is a systemd unit as the account, with its sandbox and
    resource limits; idle and maximum times, three per account.
  - Signing out ends the sign-in's sessions.
  - *Server → Web terminal* lists open sessions and can close them.
  - Root shells stay off unless root sets `terminal_root` in
    `/etc/whacp/agent.json`.
- **Resource limits per account (cgroups)**: plans set CPU %, memory,
  processes and disk read/write speed. Each limited account gets a systemd
  slice, and everything the account runs is counted in it:
  - its websites' PHP (a per-account PHP-FPM master),
  - its cron jobs (run through `systemd-run`),
  - its workers and the commands the panel runs for it.

  The same limits also apply to its SSH/SFTP logins. Accounts cannot leave
  their slice, and personal crontab and `at` jobs are refused for limited
  accounts. The limits persist across reboots and apply at once when plans
  change, websites are added or removed, or accounts are suspended; the
  scheduler reconciles them. *Server → Resource limits* shows current use.
  Network bandwidth is not limited (cgroup v2 has no network controller).
- **IP addresses** (administrators):
  - a pool of addresses websites may be bound to (shared, dedicated or
    reserved with a reason), with usage and whether the server has each
    one;
  - routed IPv6 ranges made local through a loopback route kept by
    `whacp-ipv6-ranges.service`, refused when they overlap an on-link
    prefix or contain a gateway, with address allocation;
  - per-website IPv4/IPv6 listen addresses (Nginx checked before reload)
    with A/AAAA records following, rolled back if the DNS server refuses;
  - a migration wizard (review, confirm, per-website results);
  - a non-destructive pool rebuild.

  Network interfaces are not configured by WhaCP; an address must exist
  on the server first.
- **SSL/TLS section** (administrators):
  - SSL hosts: every host name Nginx serves with its certificate state,
    names not covered, and renewal status;
  - certificate store: all certificates with where they are used;
    deletion of unused ones;
  - private keys and CSRs made on the server, where the key never leaves
    it; optional self-signed certificate; installing the CA's certificate
    against the stored key;
  - a server-wide TLS profile (TLS 1.2+1.3 or 1.3 only, Mozilla
    intermediate ciphers, session tickets) in one Nginx snippet included
    by WhaCP websites and webmail, checked with `nginx -t` and rolled back
    on failure.

  Certificate installs now also check the chain order, the validity start
  and that the website's domain is covered.
- **Database server manager** (Databases → Database server, for
  administrators):
  - status and resources: connections, queries, InnoDB buffer pool,
    temporary tables, traffic, data size;
  - connection details;
  - a fixed set of settings in WhaCP's own option file, checked by the
    server program and applied live where possible, rolled back on
    failure;
  - a restart that checks the option files first, waits for the server to
    answer, and restores the previous settings if it does not come back;
  - flush privileges, logs and hosts;
  - connection list with stop query / end connection.

  Networking (bind-address) is not changed.
- **Database browser**:
  - search across all columns;
  - table status (engine, sizes, row format, auto-increment, dates), the
    database's size, character set and collation, and foreign keys;
  - structure changes from forms, with an SQL preview: create tables;
    add, change and drop columns (types from a fixed list); indexes,
    primary keys and foreign keys; rename, optimize, analyze, check,
    repair, empty and drop tables;
  - CSV export of a table or the matching rows, and CSV import with
    column mapping and duplicate handling, all or nothing;
  - SQL console highlighting, per-database query history (kept in the
    browser) and result paging.

  All of it runs as the database's own browser account, so it cannot
  reach other databases.
- **DNS zone editor**: search, type filter and sorting; one add/edit form
  with the fields of each record type (SRV service, protocol, priority,
  weight, port and target; CAA flags, tag and value); shortcuts for common
  records; PTR records in reverse zones; conflict checks before anything
  reaches BIND (a CNAME next to other records or at @, duplicates) and
  warnings for common mistakes (fewer than two name servers, targets that
  are CNAMEs or do not exist, private addresses, very short TTLs, several
  SPF records, MX without SPF, `+all`); records can be turned off without
  deleting them; bulk turn on / turn off / delete; export of the served
  zone file and import of a BIND zone file (pasted or uploaded), with the
  lines that were left out listed and explained.
- **Email administration** (administrators; Email → Email administration):
  - Mail server: Postfix, Dovecot, OpenDKIM and Rspamd with state,
    start-at-boot, version, start / reload / restart; `postfix check`
    and `doveconf -n` results; the latest mail log lines; structured
    settings (largest message, recipients per message, queue and bounce
    lifetimes, per-client connection and message rates, HELO required,
    VRFY, outgoing TLS level, IP versions, smart host; Dovecot IMAP
    connections per user and address, failed-login delay, oldest TLS
    version, login greeting). Values are validated, checked by Postfix and
    Dovecot before reload, restored on failure, and kept when email is set
    up again. No free-form configuration editing.
  - Mail queue: every queued message with queue, sender, recipients,
    size, age and why delivery was deferred; message headers; deliver
    now, hold, release, delete (with confirmation) for selected messages;
    retry all deferred.
  - Delivery reports: each delivery attempt (sent, delivered here,
    deferred, bounced, rejected) with DSN, reason, relay and delay,
    filtered by address and status, for the last hour to 30 days.
  - Statistics: received, sent, delivered, deferred, bounced, rejected,
    logins and failed logins over time; relayers (mailboxes, server
    accounts such as a website's PHP, clients) and a sent summary by
    sender domain. Read from the journal or /var/log/mail.log / maillog.
- **Central settings page** (Settings): every panel setting sorted into
  categories (General, Server, Security, Websites and PHP, Email, DNS,
  SSL/TLS, Databases, Backups, Monitoring and alerts, Resource limits,
  Integrations, System), each saved on its own and returning to its tab
  (`/settings#dns`), with links to the pages that keep related settings of
  their own (server, PHP, mail server, greylisting, security, plans…). It
  uses the same settings store; nothing moved or was duplicated.
- **WhaCP services** (Services → WhaCP): state of the agent, panel,
  scheduler, greylisting, DKIM and FTP services with a restart button
  (server administrators). The agent and the panel restart two seconds
  after answering, so the request is not cut off; the panel can no longer
  be stopped, or restarted mid-request, from the services table.
- **Root password** (Server, administrators only): confirmed with the
  administrator's own panel password; at least 14 characters with three
  character classes. The agent hashes it (SHA-512 crypt) and gives
  `chpasswd -e` only the hash; the password is never logged, stored or
  returned, and the audit log records only that it changed.
- **Live statistics** (Monitoring → Live, server administrators): CPU
  (model, architecture, sockets/cores/threads, frequency, use per logical
  CPU), memory, swap, load, uptime, processes (running, threads, zombies),
  network interfaces with current in/out rates, totals, errors and drops,
  disks, and the busiest processes by CPU and by memory (program and user
  only; command lines are not shown, they can carry passwords). Updated
  every 3 seconds without reloading; paused while the tab is hidden or on
  request. History and alerts stay on the Monitoring page.
- **FFmpeg** (Server → FFmpeg): install, update and uninstall (refused
  when other packages need it), version and state; the Ubuntu/Debian
  package `ffmpeg`, on AlmaLinux/Rocky EPEL's `ffmpeg-free` (without
  patent-encumbered codecs; no third-party repository). Per hosting plan
  (Plans → FFmpeg, off by default): only accounts whose plan includes it
  (for a reseller's customers, the reseller's plan too) and
  administrators' websites can run it. The programs belong to root and the
  group `whacp-ffmpeg` with mode 0750 (kept through package updates by
  dpkg-statoverride, applied again after package jobs on RHEL), and the
  group's members follow the plans (at once when a plan is saved, else
  within five minutes); suspended accounts lose access and PHP is reloaded
  for accounts that lose it. It does not stop users from running a
  program they upload themselves.
- **Disk usage by folder** (website → Disk usage): the size and number of
  files of each folder in the website folder, the files lying there and
  the domain's mailboxes, measured by a background job (the page stays
  usable; the job window shows progress); sort by size, name or files;
  open a folder to measure inside it. Links are not followed.
- **Directory listing** per website (Advanced → Files and folders): off by
  default (folders without an index page answer 403); when on, nginx lists
  their files (`autoindex on`). Not offered for proxy websites.
- **File permission repair** per website (Advanced → Files and folders):
  a check lists files and folders that are not owned by the website's
  account, belong to another account's group, are writable by anyone,
  carry set-user/group-ID bits or cannot be read by their owner; Repair
  fixes exactly those. Deliberate choices such as a private 0600
  configuration file or group-writable folders are kept. Symbolic links,
  special files, other filesystems and extra hard links to files the
  account does not own are never touched, and a website folder that is
  not already the account's is refused.
- **Email authentication checker** (Email → domain → Check DNS): looks up
  the published DNS and reports Pass, Warning, Fail or Missing with an
  explanation for the mail host name (points to this server), MX (delivers
  here, or elsewhere by design with remote routing), SPF (evaluated for
  this server's address per RFC 7208, several records detected), DKIM
  (published key compared with the signing key), DMARC (policy) and
  reverse DNS of the server's address (PTR and forward confirmation). The
  required and recommended records and "Add to DNS zone" stay as before.
- **Email administration tools** (Email administration → Tools): mail
  troubleshooter (how mail to an address is routed: mailbox with filters,
  autoresponder and spam filter, forwarders and loops, catch-all, remote or
  backup routing, external MX or smart host, or refused as unknown) and
  repair of mailbox ownership and permissions (vmail, no access for
  others; links and special files are skipped and never followed).
- **Greylisting** (Email administration → Greylisting): triplet
  greylisting (sender IP, from, to) by WhaCP's own Postfix policy service,
  which runs as an unprivileged, socket-activated systemd unit
  (`whacp-greylist`, DynamicUser, no capabilities; `systemd-analyze
  security` exposure 1.1). Settings: on/off, initial deferral, resend
  acceptance period and record expiration (minutes), bypass for senders
  whose SPF passes, automatic trust of newly listed providers. Trusted
  hosts (addresses and ranges with comments; add, edit, delete). Common
  mail providers (Google, Microsoft, Yahoo, Apple, Amazon SES and others)
  with Trust and Auto update: their ranges are read from their SPF records
  (on demand and daily). Report of all triplets with deferred and accepted
  counts, create, block expire, must retry and record expire times;
  search, status filter, sorting, pages, removal of expired, selected or
  all records. Postfix asks the service with `default_action=DUNNO`, so
  mail is accepted, not deferred, if the service is unavailable.
- **Email filters and autoresponders** per mailbox (Email → domain →
  mailbox → Filters): conditions on From, To, Cc, Reply-To, Subject,
  List-Id or body (contains / is / begins / ends, or their negation; all
  or any), actions move to folder, forward, send a copy, mark as read,
  delete, stop; enable, disable, edit and delete. The autoresponder has a
  subject, message, reply interval and optional date range. Rendered to
  the mailbox's personal Sieve script from validated fields (users never
  write Sieve), compiled with sievec, rolled back on failure.
- **Mail routing** per domain: local, backup (queued for the primary MX
  through Postfix relay_domains) or remote (mail from this server to the
  domain goes to its own mail server; local aliases for it are not
  applied).
- **Shell access for website accounts** (administrators; Security → Shell
  access): per account or for all accounts at once. Disabled (default,
  SFTP only), jailed (a bubblewrap sandbox: `/usr` and a short list of
  `/etc` read-only, only the account's website folder writable, private
  `/tmp`, processes and IPC, other accounts invisible, no_new_privs, SFTP
  inside the sandbox too) or normal (a shell on the host, with a
  warning). A shell exists only while the account's SSH/SFTP login is on,
  with its keys in root-owned files. Plans whose feature list leaves out
  "SSH shell & web terminal" get no shell; the scheduler removes shells a
  plan no longer includes. The installer installs bubblewrap and, where
  AppArmor restricts user namespaces (Ubuntu 23.10+), a profile that lets
  only the jail's group-restricted bubblewrap copy create them.
- **Feature Manager** (Plans → Manage feature lists): named feature lists
  (databases, email, DNS, file manager, SSL, subdomains, FTP, cron,
  backups, application installer, Git, workers, Redis, logs, statistics,
  malware scan, PHP & website settings) assigned to hosting plans. A
  feature that is off is refused by the server for pages, form posts and
  the API, and disappears from the menus; a reseller's customers get only
  what both plans include. Staff accounts are not limited.
- **Per-account limit overrides** (administrators; account page → Plan
  and limits): raise or lower any plan limit for one account (e.g. 25 GB
  on a 10 GB plan) without changing the plan; the page shows plan,
  override and the value in effect. Recorded in the audit log.
- **Plan upgrade/downgrade page** with a side-by-side comparison of every
  limit, current usage and limits the account is already over.
  Administrators and resellers (for their customers, with their plans).
- **Bandwidth over the limit** (Users → Bandwidth over limit): accounts
  over their monthly limit with use, limit and excess; suspend, unsuspend
  or reset this month's counter (earlier months are untouched).
- **Bulk account termination** (administrators): select accounts on the
  Users page; the next page lists every website, mailbox, database and
  DNS zone that will be removed and asks for TERMINATE. Accounts are
  removed one by one through the normal delete paths, with a result per
  account; a reseller with unselected customers is skipped.
- **Account suspension** (administrators; Users → account → Account
  status): a suspended customer or reseller (and a reseller's customers)
  cannot sign in or use API tokens; its websites go offline, cron jobs and
  workers stop, mailbox logins are refused (IMAP, POP3, webmail, SMTP AUTH;
  incoming mail is still delivered), database users are locked, and the
  website accounts expire, which refuses SSH/SFTP (keys included) and
  locks their FTP accounts. Nothing is deleted; each object keeps its own
  on/off setting, and unsuspending restores them. A failed part is
  reported and can be retried.
- **Malware scans (ClamAV):** install ClamAV under Packages; Security →
  Malware scan shows the engine and signatures, updates them, and sets a
  schedule (nightly to monthly, 1:00–6:00) and quarantine. Each website
  has a Malware scan page (scan now, last result, history). clamscan runs
  as the website's own account; quarantine moves infected files into the
  website's `.whacp-quarantine` folder (mode 0000) without following
  links out of it. The owner is notified of infected files; a scan
  without signatures fails instead of reporting "clean".
- **Create a new account:** login, password (generate), plan, PHP
  version, spam filter and Spam Box, mail routing, reseller, and DNS
  options (DKIM, SPF `v=spf1 +a +mx +ip4:… ~all`, DMARC `p=none`,
  registrar name servers, overwrite) with the first website in one step.
- **New websites get their DNS zone** (SPF, DMARC, and DKIM when email is
  hosted here); subdomains go into the parent's zone.
- **Automatic SSL:** a Let's Encrypt certificate once the domain points
  to this server (checked for up to 3 days; a setting).
- **Plan resources:** monthly bandwidth, FTP and email accounts, quota
  per mailbox, subdomains, parked and addon domains; enforced, with a
  "Plan usage" panel for customers and bandwidth alerts.
- **Additional PHP directives per website** (61 allow-listed php.ini
  settings; paths must stay inside the website).
- **"Coming soon" start page** (Bootstrap) for new websites.

### Changed
- Hosting customers and resellers no longer see server-level PHP-FPM
  settings (process manager, max processes, disabled functions).
- **Administrator and reseller list pages in a table style** (Websites,
  Databases, FTP, Security): an info bar, a row of actions with the
  service and its version, and tables with inline operations ("Manage |
  Log | ⋮"), search, filters and pages of 10–100 rows.
  - Websites: a status switch per row, completed backups, quick links
    (files, databases, statistics, PHP version) and days left on the SSL
    certificate (resellers see only their own websites' certificates).
  - Databases: "Databases" and "Users" tabs; Permission, Import, Dump and
    Delete per database; Assign, Remote, Password and Delete per user.
  - FTP: the FTP address, "Add FTP" for a website, settings in a panel.
  - Security: tabs across the top; an overview with SSH logins (today,
    yesterday, 7 days; last success and failure, read from the SSH
    server's journal), the last panel sign-in, and a safety rating from
    the security checks; firewall rules as a table (protocol, port,
    whether something listens on it, strategy, direction, source,
    remarks) with inbound/outbound filters; SSH settings as rows.
- **Hosting customers get a simple layout:** no sidebar, and their
  websites as cards (search, sort, status filter). The site page follows
  the familiar hosting-panel pattern: a details header and "Site
  managing" tiles, with Delete and Disable at the top. Administrators
  can switch between cards and the list.
- **SQL import** into a website's database (upload `.sql`/`.sql.gz`, or a
  file in the website).
- **Cron:** a Logs page per job with its runs as a table (start,
  duration, exit code, trigger, output), and an optional per-job email of
  the output.
- **Remote access for database users** (an IP address, a network or any
  host), and "Assign to database".
- **Subdomains and addon domains:** websites under the same site
  account, with an optional A record in the parent's zone.
- **Website statistics (AWStats):** updated every 6 hours as the
  website's account; the panel draws the report from AWStats' data.
- **PHP → version → Settings:** manage extensions (install, remove, switch
  on or off; removals that would take other packages along are refused,
  and the panel's own extensions are protected), edit php.ini settings
  with a form or a text editor (in WhaCP's own `99-whacp.ini`, allowlisted
  and type-checked), and set server-wide PHP-FPM options. Each change is
  checked with `php-fpm -t`, reloaded, and undone on failure.
- **Website → PHP & settings → Advanced PHP-FPM:** process manager
  (ondemand, dynamic, static), process counts, idle timeout, requests per
  process, request timeout and slow log per website.
- **`install.sh --public-https[=8443]`:** the panel at
  `https://SERVER-IP:8443/` like other panels, instead of an SSH tunnel.
  Nginx serves it with a self-signed certificate for the server's
  addresses (replaceable with a real one); the panel keeps listening on
  127.0.0.1 only. The installer checks the Nginx site with `nginx -t`
  (removing it again if rejected), makes sign-in cookies HTTPS-only,
  opens the port in UFW or firewalld, handles SELinux, and checks the
  panel answers. Works on an installed server with `--reinstall`.
  The panel now treats a request as HTTPS when a trusted proxy says so
  (`X-Forwarded-Proto`), so HSTS and security-key origins are right
  behind it; from any other peer the header is ignored.
- **Recovery onto a new server:** *Find backups* on a remote storage adds
  the backups found there to the panel, so they can be restored on a new
  server after copying the old backup key. Only backups encrypted with
  that key are added, and their manifest must be authenticated by it
  (backups now carry a manifest MAC), so someone who can write to the
  storage cannot plant a backup or move one to another website.
- **Notifications:** a bell in the top bar, a notifications page and a
  dashboard card with the results of background jobs and monitoring
  alerts.
  - Failures of any job are reported, whoever started it and even if nobody
    watched it (the scheduler follows unfinished jobs); long jobs are also
    reported when they finish; refused deploy webhooks and jobs that could
    not start are reported.
  - Customers and resellers see their own websites' results and alerts
    (certificates, backups, quotas); server matters stay with
    administrators.
  - Per-user read state, filters, and an opt-in "email me about failures"
    through the panel's email settings.
  - `GET /api/v1/notifications` and `POST /api/v1/notifications/read`.
  - Kept 90 days; the count refreshes without keeping an idle session
    alive.

### Changed
- Automatic backups are set up by administrators only; customers see the
  schedules read-only.
- DNS zones and mail domains attached to a website are visible to its
  owner, including ones an administrator created.
- Let's Encrypt notices go to the account's email (shown, not asked).
- **Website dashboard:** *Websites → Manage* opens a hosting dashboard
  instead of server details. It has a summary (status, PHP, HTTPS, SSL,
  disk, last backup), "Search features…" with keyboard navigation, quick
  actions, and feature cards grouped as Files, Databases, Email, Domains,
  Applications, Performance, Backups, Automation and Security. Links
  follow the user's permissions.
- **Website menu:** it replaces the 15-tab bar. It is nested under
  Websites in the sidebar, the same on phones ("Website menu"), with a
  breadcrumb, so there is no sideways scrolling.
- **Advanced:** server details (system user, folders, PHP-FPM limits,
  the generated Nginx configuration, recent tasks) moved here. Disable,
  reload PHP and re-apply are in a Danger Zone, and delete is separate
  and still needs the domain typed. These actions return to Advanced.
- **Section pages:** Databases, DNS, Email, Backups, SSL, FTP / SFTP, cron
  and workers share one pattern:
  - a toolbar with a count, a filter and a "+ Add" button that opens
    the form in place (also from links such as `#new-database`, and
    after validation errors)
  - empty states
  - status columns (database present, mailbox usage and fullness, zone
    live)
  - destructive actions in danger cards
  - DNS records shown as Type | Name | Value | TTL, with shortcuts for
    common records
- **Email and DNS stay inside the website.** They offer to set up a mail
  domain or zone there. Validation errors return only to that page,
  whose address is checked against a strict pattern.
- On phones the top bar wraps, and the account button shows only its
  icon.
- **Packages page:** installed components show an "Installed" badge with
  their version. "Install" no longer reloads the page: it adds the
  component to a queue, a window lists the queued components with their
  state and the log of the one being installed, they are installed one
  after another, and each row turns into "Installed" when done. The
  window can be closed (installs continue) and reopened with "Queue".
- **Support levels:** Ubuntu 22.04 and 24.04, AlmaLinux 9, Rocky Linux 9
  and RHEL 9 are supported; Ubuntu 26.04 and AlmaLinux, Rocky Linux and
  RHEL 10 are previews: they install and are tested the same way, but are
  not yet recommended for production. The installer warns on a preview
  system, the agent logs it, and the dashboard and Server page show a
  "preview" badge (`system.info` reports `preview`).
- Website owners (and their resellers) can open the log of any job run on
  their website, not only the ones they started.
- Webhook deployments and scheduled backups are tracked like jobs started
  in the panel.

### Security
- **Website file symlinks:** `db.import` (a website path) and cron's
  "Run now" log append opened website files as root, following symlinks.
  A website's user could have leaked a root-only file into an import log,
  or appended their command's output to a file such as `/etc/crontab`.
  Both now open website files confined to the website (os.Root).
- **System accounts:** the website form let a hosting customer name an
  existing account (another customer's, or a server login), and the agent
  used it. Customers may now reuse only their own websites' accounts; a
  new name must not exist on the server (`new_user`). Deleting a website
  no longer removes an account that a static or disabled website still
  uses.
- Server backups are always encrypted, also when kept only on the server:
  they contain the agent token, the panel key and TLS private keys, and
  local backups can be downloaded and are copied by host backups. The
  agent refuses an unencrypted server backup; the panel shows "Always
  encrypted" instead of the switch. Website backups keep the choice.
- SQL imports (database restores and the agent's `db.import`) run the
  `mysql` client with its own commands turned off (`--sandbox` on MariaDB,
  `--commands=OFF` on MySQL). The client runs as root, and a line such as
  `\! command` in an imported file ran that command. A client that cannot
  turn them off is refused with a message to update it.

### Documentation
- CI runs the virtual-machine tests (six systems) only when started by
  hand or for a release; every push runs the quick jobs, and a newer push
  cancels the run it replaces.
- Installation guide: hardware, network ports, DNS and mail prerequisites,
  what uninstalling leaves behind (websites keep running; the backup key
  and backups are kept even with `--purge`), limits and known limitations.
- Step-by-step recovery onto a new server (the previous "restore drill"
  paragraph described steps that could not be carried out).

### Fixed
- **A customer's website said "DNS … is not hosted here"** when an
  administrator had created the zone (or email domain) by typing the
  domain on the DNS or Email page instead of choosing the website. A
  zone or email domain for exactly a website's domain now belongs to
  that website and its owner however it was created, also when the
  website is created afterwards; existing ones are linked on upgrade
  (only those no other account owns).
- **Websites page warned "skipped /etc/nginx/modules-enabled/…: outside
  permitted configuration roots"** on Ubuntu with Nginx dynamic modules
  installed: those files link to the package's `load_module` stubs in
  `/usr/share/nginx/modules-available` (on RHEL: `/usr/share/nginx/modules`),
  which the Nginx scan now reads. No other folders were added.
- The Packages page showed every component as not installed ("Install")
  on every system: the dpkg-query/rpm format held real line breaks, which
  the agent refuses in command arguments, so the query never ran. It now
  passes `\n` for the tools to expand; installed components show their
  version.
- `install.sh --reinstall` from a source checkout installed the agent
  built earlier (`dist/whacp-agent`) even after `git pull` brought newer
  sources, so agent fixes did not arrive. It now rebuilds when the
  sources are newer than that build (without Go it warns and keeps it).
- Saving fail2ban settings failed with "Could not find server" when the
  fail2ban service was stopped (e.g. just installed and not running): a
  reload needs a running service. WhaCP now starts it, waits until it
  answers, and when it does not start shows fail2ban's own log; saving
  unchanged settings also starts a stopped service. BIND checks that
  fail without printing anything now say why (exit status, not found).
- After a background task (installing a component, a backup, …) the page
  kept showing the old state when its window was closed without "Reload
  page": an installed component still offered "Install". Closing the
  window after the task ended now reloads the page; closed earlier, the
  task is followed and a notice says when it is done.
- **Webmail on Ubuntu 26.04.** Ubuntu's Roundcube 1.6.11 declares its own
  `array_first()`, which PHP 8.5 has built in, so it stopped with a fatal
  error and WhaCP refused webmail there. On PHP 8.5 and later WhaCP now
  turns the built-in off for Roundcube's pool and database step only
  (Roundcube's own function does the same). Websites' PHP is not changed.
- **Background workers never started on an installed server.** The
  worker's launcher was kept in `/var/lib/whacp-agent` (0700 root), which
  the website account it runs as cannot enter. Launchers are now in
  `/var/lib/whacp-workers` (0711 root), each readable only by its own
  website account (it contains the worker's environment variables).
  Workers created before this version: open each one and save it again.
- Workers could not unpack archives on Ubuntu 26.04: `RestrictSUIDSGID`
  makes `openat2()` fail and tar 1.35 does not fall back. Workers no
  longer set it, as the other commands run as a website account already
  do not. The setting only stopped the account creating setuid/setgid
  files, which it can do anyway through cron, PHP or SFTP, and which
  give nothing inside the worker (`NoNewPrivileges` stays).
- Changes to the web server configuration no longer run at the same time:
  two website changes (or a website change and a certificate renewal,
  webmail change, import or reload) could have one's `nginx -t` see the
  other's half-written files and roll back for no reason of its own.
- History no longer grows without limit: the scheduler removes job records
  90 days after they settled, deployments after 90 days (always keeping
  the latest 20 per website) and audit records after 365 days
  (`audit.retention_days` in `/etc/whacp/panel.php`; `0` keeps them
  forever). Each audit removal is itself recorded.
- Backups and restores no longer fill the disk. Backup files and downloads
  stop before less than 512 MiB would be left (the partial files are
  removed); a restore first checks that the website's and MySQL's disks
  have room for the restored data and changes nothing otherwise. Backups
  now record their sizes before compression for this.
- Installer: the PHP extension check could report an installed extension
  as missing (`php -m | grep -q` under `pipefail`: when grep stops early,
  php is killed by SIGPIPE and the check fails). Seen on AlmaLinux 10 in
  CI; reproduced locally (1 in 300). The module list is now read once.
- Upgrade backups are verified: the panel database's checkpoint must
  complete (its result was ignored, so a copy could miss the latest
  changes), the copy must pass SQLite's integrity check, and checksums of
  the whole backup are recorded. Rollbacks check them first and refuse a
  damaged or changed backup. `--rollback` now warns that panel changes
  made since the upgrade are lost, with the audit-log count.
- Installer: running it again after an interrupted installation now says
  what is missing and prints the `--reinstall` command that completes it
  (keeping configuration, token, key and database), instead of only
  "already installed, use upgrade.sh". The agent token and panel key are
  written atomically, and the summary reports the actual write mode.
- RHEL 10, AlmaLinux 10 and Rocky Linux 10: Redis features work with
  Valkey, which replaces Redis there (`valkey` package and service,
  `/etc/valkey/valkey.conf`, `valkey-cli`).
- Redis 6.0 (Ubuntu 22.04): per-website ACL users are created without
  Pub/Sub channel rules, which that version does not support, with a note.
- Ubuntu 26.04: extracting backups and WordPress archives failed in the
  site-account sandbox (`RestrictSUIDSGID` blocks the `openat2` call tar
  1.35 uses). Setuid/setgid bits are now removed from extracted files
  instead.
- Deleting a mailbox reports mail it could not move to the trash instead
  of ignoring the error.
- Deleted websites' files and mailboxes' mail could not be moved to the
  trash when they were on another filesystem than `/var/lib/whacp-agent`
  (a separate `/home` or `/var/vmail` disk; Ubuntu 26.04's `/tmp` in
  tests). The trash is now `.whacp-trash` in the websites folder and in
  `/var/vmail` (root only, same disk: a rename that keeps owners and
  labels).
- Upgrades no longer interrupt background jobs: `upgrade.sh` waits until no
  restore, deployment or backup is running (`--wait`, default 30 minutes)
  and changes nothing if one still is; the agent, when stopped, lets
  running jobs finish (up to 5 minutes) instead of abandoning them, and
  its unit no longer sends SIGTERM to the commands a job is running.
- Changes to Postfix (enabling the spam filter, new mailboxes and domains)
  are reported only once Postfix applies them: a message received right
  after `postfix reload` could still be handled by an smtpd started before
  it, with the old settings (seen in tests as an unscanned GTUBE message).

## [1.0.0-rc1] — 2026-09-25

First release candidate. It contains the work developed as 0.5.0 (never
released on its own): phases 5–28 on top of the 0.4.0 foundation, hosting
services added afterwards, and the fixes from the 1.0.0-rc1 release audit
(`docs/release/1.0.0-rc1-audit.md`). Write actions stay off until the agent
runs with `allow_write_actions` (`install.sh --enable-writes`).

### Breaking changes
- None for data or configuration: installations of 0.4.0 or 0.5.0 upgrade
  with `installer/upgrade.sh` (tested with real 0.5.0 data, see Testing).
- Resellers and customers no longer see the server-wide inventory pages'
  data for other tenants (see Security). Pages that showed it now show only
  their own websites, and never system cron jobs.
- Release archives no longer contain the test suites and test fixtures.

### Security
- **Fixed — tenant isolation (release blocker):** customers and resellers
  could open the Domains, PHP, SSL and Cron pages. These showed the whole
  server inventory, including other tenants' domains, document roots,
  certificates, PHP pools and system cron commands (which may contain
  credentials). The inventory is now filtered to the user's own websites.
- New release-gate test suites:
  - an RBAC matrix: every route, for every default role that lacks the
    route's permission, gives 403 and never reaches the agent;
  - tenant isolation for every page and every `/sites/{id}`, database,
    mail and DNS route;
  - REST API isolation: read-only tokens cannot write, other tenants'
    resources return 404, deleted and malformed tokens are refused;
  - an adversarial file-manager sweep: every action with traversal,
    encoded, NUL, `/proc`, `/dev` and symlink paths.
- **Fixed — commands as a website account on Ubuntu 24.04 (release
  blocker):** under the installed agent service, backups, restores, cron
  "run now", deploy hooks and app installers failed with `operation not
  permitted`. On systemd 255 the agent's process lacks `CAP_SETUID`, and
  with `NoNewPrivileges=yes` it can never gain it. The agent no longer
  changes its own credentials: PID 1 starts these commands as the account
  in a transient unit with no capabilities, `NoNewPrivileges`, no
  setuid/setgid, a read-only system and an enforced timeout. The agent's
  own unit and sandbox are unchanged (`systemd-analyze security`: 5.8, as
  before). Standard input and output go through named pipes in
  `/run/whacp-run` that only the account's own process opens; the agent
  never follows a replaced pipe.
- A command that reached its timeout is always reported as failed.
- Git deployment webhooks: a captured delivery can no longer be replayed to
  deploy again. Each signed body starts one deployment; a repeat is refused
  (409) and recorded in the audit log.
- Release archives are checked for test files, private keys and tokens
  before publishing. Releases are published only after the whole CI suite
  passes.

### Added
- **Write-path infrastructure:** file writer limited to an allowlist of
  WhaCP-owned paths, change sets with backup/validate/rollback, background
  jobs with logs, allowlisted command runner extensions (commands for
  site accounts run unprivileged).
- **Websites (5):** PHP, static and reverse-proxy sites, aliases, redirects,
  enable/disable/delete, generated Nginx configuration validated with
  `nginx -t` and rolled back on failure; conflicts with existing vhosts are
  refused.
- **PHP (6):** install versions, per-site FPM pools and settings.
- **Databases (7):** MySQL/MariaDB databases, users, host patterns, grants;
  passwords stored only when asked (encrypted).
- **SSL (8):** Let's Encrypt issue/renew (webroot), custom certificates,
  HTTPS redirect and HSTS.
- **File manager (9)** confined to a website's folder; **log viewer (10)**
  with live tail.
- **Cron (11), workers (12)** as systemd units, **Git deployment (13)** with
  deploy keys and signed webhooks.
- **Redis (14):** settings, password, ACL users, flush.
- **Backups (15):** AES-256-GCM encrypted site/server backups, S3-compatible
  storage with multipart upload, schedules, retention, restore, download.
- **Security (16):** UFW rules, SSH hardening drop-in, Fail2ban jails and
  unbans; every change rolls back after two minutes unless confirmed;
  security audit.
- **Monitoring (17):** metric history, alert rules (disk, memory, load,
  certificates, agent down, quotas) and webhook notifications.
- **Packages (18) and server settings (19):** updates, installs, hostname,
  timezone, scheduled reboot.
- **Multi-server (20):** remote agents over TLS 1.3 with public-key pinning
  and client IP allowlist; `install.sh --agent-only --remote-access`.
- **Hosting users (21):** plans with limits, resellers and customers scoped
  to their own resources, disk quotas.
- **REST API (22):** personal API tokens (read or read/write, expiry),
  `/api/v1` with OpenAPI description.
- **Plugins (23):** admin-installed plugins with their own routes,
  permissions, navigation, events and settings; example plugin.
- **Application installer (24):** WordPress and Laravel.
- **Installer and upgrades (25–26):** `upgrade.sh` with pre-flight checks,
  backups and automatic rollback; scheduler timer; version file.
- **Import (27):** take over a website found by discovery — files are copied
  into a new WhaCP website, the old vhost is disabled and the new one
  enabled in one validated reload (old configuration restored on any
  failure); the application's database is detected; imports can be undone.
- **Hardening (28):** two-factor sign-in (TOTP, replay-protected, recovery
  codes, admin and CLI reset); `bin/whacp doctor` checks PHP, file modes of
  secrets, schema, database integrity, agent reachability/version and the
  scheduler; the agent's OpenAPI action list is generated from and verified
  against the registry in tests.

### Added after the phase work
- QR code for two-factor setup (built-in encoder), per-role two-factor
  requirement; server switcher and job visibility follow resource scope.
- Email: SMTP settings, alert emails, forgotten-password links.
- Restore of server configuration backups by component, with safety
  backup, validation and rollback; extract-only mode.
- CI job running every real-service test as root (nginx, PHP-FPM,
  MariaDB, Redis, UFW, SSH, Fail2ban, WordPress, Laravel); agent unit tests
  pass without root; reproducible release archives with provenance.

### Added: hosting services
- **File manager as a full page (cPanel-style):** tree, list, upload,
  editor and archive handling, confined to the website's folder and run as
  the website's account; works on phones.
- **Redis key browser and Memcached:** browse, search, view, edit and delete
  Redis keys (by database, with type-aware views and TTLs); Memcached
  status, statistics, settings (memory, connections, loopback listen
  addresses, only after confirmation) and flush.
- **Backup destinations:** besides S3-compatible storage, SFTP (host key
  pinned on first use and confirmed), FTP with explicit TLS (certificate
  pinned the same way), Google Drive (OAuth, drive.file scope) and a local
  folder. Destinations are tested before saving and credentials are stored
  encrypted.
- **Web database browser:** tables, structure, paged rows, edit/insert/
  delete by primary key and a SQL console with result limits, all through a
  per-database account that is locked between requests (its password is
  rotated each time); built-in MySQL client (native and caching_sha2
  authentication).
- **FTP and SFTP accounts:** FTPS accounts per website served by WhaCP's own
  Pure-FTPd service (passive range, TLS, firewall), and SFTP-only accounts
  chrooted to the website with password or SSH keys; accounts are removed
  with their website.
- **DNS zones (BIND):** zones for websites with templates (web, mail, SPF,
  DMARC), all common record types, secondaries with notify and transfer;
  every change is validated with named-checkzone/named-checkconf, loaded
  and verified by querying the new SOA serial, and rolled back otherwise.
- **Email hosting:** Postfix, Dovecot and OpenDKIM for WhaCP's mail
  domains.
  - Mailboxes with quotas, aliases and catch-all.
  - Submission on 587 with sign-in; a user may send only as their own
    addresses.
  - IMAP over TLS on 993; plain 143 is closed.
  - DKIM keys, with the MX, SPF, DKIM and DMARC records added to the DNS
    zone in one click.
  - Setup validates and restarts every service, checks ports 25, 587 and
    993, and restores the previous configuration if anything fails.
  - The domain page lists the settings for mail apps.
- **Dovecot 2.3 and 2.4:** the matching configuration syntax is written for
  the installed version (Ubuntu 22.04/24.04: 2.3; Ubuntu 26.04: 2.4);
  mailboxes are stored the same way.
- **Spam filtering (Rspamd):** Rspamd runs as a milter in front of Postfix.
  - Mail above the *Junk* score is marked and filed into Junk by a Sieve
    rule that runs before personal filters.
  - Mail above the *Refuse* score is rejected while the sender is still
    connected, so it bounces rather than disappearing.
  - Senders cannot set the spam header themselves.
  - WhaCP writes only its own files in Rspamd's local.d; files it did not
    write are replaced only after confirmation. Changes are checked with
    `rspamadm configtest`.
- **Webmail (Roundcube):** installed from the distribution's packages and
  switched on for a host name.
  - WhaCP keeps Roundcube's configuration and SQLite database in its own
    folders and never edits the package's files.
  - Roundcube runs in its own PHP-FPM pool and account, restricted with
    open_basedir, behind its own HTTPS Nginx site that runs only
    index.php.
  - It signs in to Dovecot and sends through submission as the user, so
    sender checks and DKIM signing apply.
  - A Let's Encrypt certificate for the host name is used when one exists,
    otherwise a self-signed one.
  - Each change creates or upgrades the database, loads the sign-in page to
    check it works, and rolls back if it doesn't.
- **Security keys (WebAuthn/passkeys)** as a second factor: ES256, RS256 and
  Ed25519 keys, several per account, sign-in counter checks, origin and
  relying-party verification; works together with TOTP and recovery codes.
- **AlmaLinux, Rocky Linux and RHEL 9 and 10**, supported alongside Ubuntu:
  - PHP versions come from Remi (for example `php83-php-fpm`).
  - Packages are installed with dnf, with WhaCP's package names mapped to
    the distribution's (EPEL and Rspamd's repository included).
  - firewalld is supported as well as UFW: service ports are opened in
    whichever is active, and the firewall rule manager keeps its automatic
    rollback.
  - SELinux stays enforcing. WhaCP labels what it creates (websites, mail
    storage, webmail data), turns on the booleans it needs (proxying,
    webmail connections, FTP) and labels a moved SSH port.
  - BIND is opened to the network for hosted zones (after confirmation),
    and Postfix listens on all interfaces.
  - Logs, fail2ban (journal), update checks (dnf, needs-restarting) and
    server configuration backups use the RHEL locations.
  - The installer supports these systems and sets up the panel's PHP 8.3.

### Changed
- `packages.install` accepts several components at once (`names`).
- String settings can be optional; new settings for DNS name servers,
  hostmaster address and default IPv4.
- Agent errors print their details with `%+v` (tests and logs).

### Testing
- `bin/whacp doctor` prints PASS/WARN/FAIL and checks the server through
  the agent: disk space, Nginx, PHP-FPM, database server, Redis, certbot
  and backups.
- Installer end to end in CI on Ubuntu 24.04 and AlmaLinux 9. After
  `install.sh`, `tests/integration/postinstall.sh` drives the panel over
  HTTP: it signs in, creates a PHP website and checks it is served,
  creates a database, installs a certificate and checks HTTPS, makes an
  encrypted backup, damages the file and the database, restores and
  verifies them, then deletes the website.
- Upgrade from the previous release with real data
  (`UPGRADE_FROM=<ref> tests/integration/upgrade.sh`). Users, websites,
  databases, backups, an API token, encrypted secrets and audit history
  survive. The old password and token keep working. A failing migration
  rolls back, and `--rollback` restores the previous version.
- Real-service CI on Ubuntu now also covers Pure-FTPd, SFTP, BIND, Postfix,
  Dovecot, OpenDKIM, Rspamd and Roundcube; mail is actually delivered,
  filtered, signed and read over IMAP, and webmail signs in and sends.
- New CI job boots an AlmaLinux 9 virtual machine (KVM, SELinux enforcing),
  runs every real-service test and the installer inside it and prints any
  SELinux denials.
- Browser tests for the file manager, Redis browser, database browser,
  backup destinations and security keys (virtual authenticator).

### Fixed
- `upgrade.sh` now orders pre-releases before their release; plain
  version sort would have refused 1.0.0-rc1 → 1.0.0 as a downgrade.
- Restart PHP uses the distribution's FPM unit name (RHEL:
  `php83-php-fpm`).
- RHEL: the commands WhaCP needs there (dnf, rpm, firewalld and SELinux
  tools) were missing from the agent's allowlist. Atomic writes now keep
  a replaced file's SELinux label, and restored configuration is
  relabelled. Redis 6.2 ACL users (`resetchannels`) are created
  correctly.
- The panel's test SMTP server wrote its transcript non-atomically (flaky
  test); the test authenticator now pads EC key coordinates to 32 bytes.
- Roundcube's pool sets `include_path` with `php_value`: Roundcube refuses
  to run when include_path is locked.
- `uninstall.sh` left the agent's write-mode drop-in behind.
- Configuration files WhaCP rewrites without owning them (e.g. its include
  in `/etc/named.conf`) keep their owner and group; on RHEL BIND could no
  longer read `named.conf`.
- RHEL: the DKIM folder is `root:opendkim`, so OpenDKIM starts under
  SELinux; Postfix 3.5 accepts the TLS protocol setting (it had disabled
  TLS on submission) and email setup now checks STARTTLS on 587 and rolls
  back if it fails; Roundcube 1.5 finds WhaCP's configuration folder, its
  default settings (`root:apache 0640` on RHEL, copied by the agent) and
  its IMAP and SMTP servers.
- Commands WhaCP runs as a website account (deploy hooks, cron "run now",
  app installers) can send mail with `sendmail` again: inside their units
  it is WhaCP's own sendmail, which submits the message over SMTP to this
  machine's mail server (Postfix's hangs there: its setgid `postdrop`
  cannot work with `NoNewPrivileges`). The system's `sendmail` is not
  changed.
- The WordPress installer no longer sends the "new site" e-mail, which hung
  under the sandbox (Postfix's setgid `postdrop` cannot work with
  `NoNewPrivileges`).
- Multi-server: the agent version is read correctly and failures are
  recorded as `version_mismatch`, `auth_failed`, `certificate_mismatch`,
  `refused` or `timeout` instead of plain `offline`.

### Known limitations
- Let's Encrypt issuance is tested only with a stand-in certbot (it needs a
  public domain).

## [0.4.0]

Phases 0–4: foundation, panel, agent, server dashboard and read-only discovery.

### Added
- **Foundation:** repository layout, MIT license, contribution and security
  policies, architecture and operations documentation, CI workflow, Makefile,
  development environment (`scripts/dev.sh`) with a fixture server.
- **Panel (PHP 8.3+):** dependency-free MVC core (container, router, views,
  middleware), SQLite with migrations, session authentication with Argon2id,
  login throttling, CSRF protection, strict Content-Security-Policy, RBAC with
  Administrator/Operator/Viewer roles, panel user management with
  last-administrator protection, settings, audit log, profile and password
  change (other sessions are invalidated), friendly error pages with
  reference IDs, CLI (`bin/whacp`).
- **Agent (Go):** Unix-socket API with bearer token and SO_PEERCRED checks,
  named action registry with a write gate (`allow_write_actions`, off),
  uniform response envelope, OS abstraction (Debian family; RHEL scaffold),
  metrics collector with history, service status via systemd, allowlisted
  command runner (no shell), secret redaction, OpenAPI description.
- **Dashboard:** live CPU, memory, disk, load, network charts and service
  state; server information page; services page.
- **Read-only discovery:** Nginx configuration parser (includes, symlinks,
  disabled sites), websites correlated with PHP-FPM pools and certificates,
  PHP versions and pools, Let's Encrypt lineages, MySQL/MariaDB schemas from
  the data directory (no credentials), Redis, cron (secrets redacted), system
  accounts, firewall state; snapshots with checksum history; heuristics
  detecting sites managed by other panels.
- **Installer:** OS/requirement/conflict checks, `--dry-run`, hardened
  systemd units (agent filesystem is read-only), loopback-only panel,
  first-administrator creation, health checks, uninstaller.

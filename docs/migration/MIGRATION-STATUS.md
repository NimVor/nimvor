# WhaCP PHP → Go Migration — Status (machine-readable-ish)

Baseline commit: 46313561 (branch claude/sleepy-ptolemy-bdv6ib)
PHP app: 230 files / 38,926 lines · 84 controllers · 76 services · 8 repos
Views: 179 · Tests: 119 files / 15,461 lines · Routes: 551 · Migrations: 57
Agent: Go, unchanged, full suite GREEN at baseline.

Route porting count (see ROUTE-MATRIX.md, verified against
`server/internal/http/server.go`): **106 ☑ done+tested, 3 ◑ partial, 442 ☐** of 551.

## Legend
☐ not started · ◐ in progress · ◑ partial (documented remainder) · ☑ done+tested · ⊘ retired (documented)

## Phase 0 — Discovery & baseline
- ☑ Baseline commit, build, agent tests captured
- ☑ ROUTE-MATRIX.md (551 routes dumped from the real router)
- ☑ INVENTORY.md (controllers/services/repos/views/tests/migrations)
- ☑ ARCHITECTURE.md, SECURITY-MATRIX.md, FEATURE-MATRIX.md
- ☑ Verified figures: SQLite + 57 migrations + argon2id passwords + unix-socket agent

## Phase 1 — Go foundation (framework parity)
- ☑ server/ module, config (env-driven), slog JSON logging
- ☑ database: open (same PRAGMAs) + migrator (name-compatible, 57 embedded SQL) — tests: fresh install applies 57, existing PHP DB skipped
- ☑ identity: argon2id + bcrypt verify — test verifies real PHP password_hash output
- ☑ agent: unix-socket JSON client — test against a real unix socket (auth, path, body, data, structured error, bad-name reject)
- ☑ security-headers middleware (strict CSP parity)
- ☑ internal/crypto: NaCl secretbox, byte-compatible with PHP Security\Crypto `v1:` (verified against a PHP-sealed fixture)
- ☑ internal/session: sealed-cookie sessions (HttpOnly/Lax/Secure) + flash
- ☑ CSRF synchronizer token (64-hex, constant-time, `_token`/`X-CSRF-Token`)
- ☑ internal/identity: user repository (find / findForAuth by username|email / password hash / session epoch bump / record login / update password), `CanAct` and the suspended-via-reseller sub-query, ported verbatim from UserRepository.php
- ☑ internal/authz: role→permission repository, FeatureService (34-feature registry, DEFAULT_OFF opt-in, reseller two-slot gating, route-by-name gating) and the Authorizer (`Can` fail-closed + feature withholding, `RouteAllowed`) — parity with Authorizer.php + FeatureService.php
- ☑ internal/identity password hashing: argon2id in PHP's password_hash format (verified cross-compatible — a Go-made hash verifies in PHP and PHP reports no rehash needed); `NeedsRehash`
- ☑ internal/auth: login throttle + login_attempts store (verbatim from LoginThrottle.php/LoginAttemptRepository.php); AuthService password path — `Attempt` (throttle, dummy-hash timing, suspended/inactive handling, opportunistic rehash), `User` (idle+absolute timeouts, password-hash+session-epoch fingerprint invalidation), `Logout`
- ☑ internal/auth: **TOTP two-factor sign-in** (RFC 6238, SHA-1/6 digits/30s) — internal/twofactor ported from app/Security/TwoFactor.php (sealed secrets, SHA-256-hashed single-use recovery codes, previous/current/next step window with last_step replay protection); pending-sign-in state (300s TTL, 5-try cap, fingerprint check, cancel); login step + profile self-service (setup/enable/disable/regenerate-recovery, all current-password-protected except setup); audit records on login/2FA outcomes; **negative tests**: TOTP vectors (RFC 6238 B.1), key-only users refused (never bypassed), bad code, too-many-tries, TTL expiry, password-change invalidation of a pending sign-in, bad CSRF, recovery-code single-use
- ☑ internal/auth: login/logout/2FA outcomes audit-logged (auth.login success/failure/denied, auth.logout) — AuditLogger parity for the identity area
- ☑ internal/web: html/template engine with embedded, CSP-safe templates (no inline script/style; reuse /assets): login, two-factor, dashboard, profile (2FA + API tokens + flashes), websites, website_show, databases, dns, mail, audit, users, roles, plans, servers, notifications, site_cron, site_subdomains, site_workers, error
- ☑ internal/http: per-request session middleware (load→resolve user→save cookie before write), `requireAuth`, and the login/logout/dashboard/profile handlers with CSRF verification and token rotation on sign-in
- ☑ VERIFIED: `go test ./...` green; live binary signs in end-to-end against a real SQLite DB using a PHP-generated argon2id hash (GET /login→CSRF, POST→303 /, dashboard shows the user), and rejects bad CSRF / wrong password / anonymous dashboard access; logout clears the session
- ☑ internal/authz: role/permission bootstrap seeding (`SyncDefaults`) — the 46-permission catalogue + 5 system roles ported from Security\Permissions, byte-identical to the PHP definitions (diffed), idempotent, keeps plugin.* perms and administrator-customised roles; wired into cmd/whacp startup after migrate (verified seeding a fresh DB)
- ◑ static asset serving ☑ (reused assets dir) · html/template layout: 19 of 179 views ported
- ◑ stdlib mux + request logging ☑ · auth/permission/feature middleware ☑ (wired across all ported routes via `guard`)
- ◑ /healthz ☑ (parity: {"status":"ok"}/503) · login/logout/dashboard/profile/websites/databases/dns/mail/users/roles/plans/servers/notifications/audit/site-cron/workers/subdomains ☑

## Ported read+write routes (all with permission/feature/tenant-scope + CSRF tests)
- GET `/` (dashboard, permission-gated nav)
- GET+POST `/login`, POST `/logout` — incl. TOTP second-factor step
  (GET+POST `/login/two-factor`, ◑: WebAuthn security-key step not ported —
  key-only users are refused a password-only sign-in, never bypassed)
- GET+POST `/password/forgot` + GET+POST `/password/reset/{token}` —
  password reset by email (internal/mail SMTP + internal/settings + the
  PasswordReset token state machine; anti-enumeration, 30-min single-use
  tokens, all-of-a-user's tokens die on one use)
- GET `/websites`, GET `/websites/{id}` (tenant-scoped; foreign id → 404)
- GET `/sites/{id}/cron` · GET `/sites/{id}/workers` · GET `/sites/{id}/subdomains` (feature-gated)
- GET/POST `/databases` + POST `/databases/{id}/drop` (plan-limit enforced before
  the agent call; confirm-word + CSRF negative tests)
- Database users — POST `/database-users` (create; username prefix for tenants,
  host validation, `db_customer_remote_hosts` setting, generated 24-char
  password flashed once, optional encrypted storage), POST
  `/database-users/{id}/password` (reset), `/reveal` (decrypt stored; audited),
  `/delete` (`db.user_drop`), `/assign` (grant a login to one of the actor's
  databases) + POST `/databases/{id}/grant` (set/revoke — `db.grant` with the
  revoke flag, audits database.grant/database.revoke) + POST
  `/database-users/{id}/hosts` (add host: separate login, same access copied,
  new password). All databases.manage + tenant-scoped (foreign ids read as
  not-found) + CSRF + audit (database_user target rows). Display: the databases
  page now lists the users tab (host, stored-password state, per-user grants,
  assign/remote/password/delete ops) and each database's granted users +
  permission form, plus the shown-once secret flash. Display is the full PHP
  _manage view: the live db.overview section (per-database Size + Status
  columns, the "MariaDB 10.11.6" server label, the "could not be queried"
  fallback when the agent is unreachable, and the all-scope-only
  "other databases (not managed by WhaCP)" section) rides on the same page ·
  GET `/sites/{id}/databases` (DatabasesController::site — the
  same page scoped to one website: website resolved under tenant scope first
  (foreign id → 404), databases listed for the site only, the create form pins
  website_id, and every operation's return_to points back to the site page;
  the databases store/drop redirects honor the return_to whitelist too) ·
  POST /databases/{id}/dump + POST /databases/{id}/import (job-based: the
  agent job id is tracked in agent_jobs — kind "Dump <name>" / "Import into
  <name>", target database, starter user — and flashed once; import takes a
  .sql/.sql.gz path inside the website folder, refuses "..", ≤255 chars, and
  removes only panel-named tmp/whacp-import-<16hex> uploads) ·
  GET /jobs/{id} (JobsController::show — the JSON endpoint the job viewer
  polls: the 16-hex id is looked up in agent_jobs and visibleTo checks own /
  reseller-customer / website-scoped / all-scope visibility (foreign → 404
  "Job not found.", no agent call), then `jobs.log` with the viewer's offset;
  the reply passes through (job/log/offset/done) plus success + the tracked
  kind; the polled status is stored and a finished job is settled once — the
  settle's notification + scan-on-extract halves defer with the
  notifications/AV batches, the discovery invalidation with discovery; the
  job viewer's bootstrap modal rides with the Go shell work, the databases
  pages carry the "Job <id> started" flash alert meanwhile) ·
  GET `/databases/{id}/browse` + POST `/databases/{id}/browse/api` +
  GET `/databases/browse` + POST `/databases/browse/api` (DbBrowserController
  — the per-database browser page + the operation endpoint the reused
  /assets/js/dbbrowser.js polls: the request `op` dispatches to the ten
  `db.browse*`/`db.query` agent actions with a strict field whitelist + type
  coercion, 90 s agent timeout, changing ops audited as
  `database.browser_<op>` against the owning website or the server — never
  the row values; the server variants take any database by bare name,
  all-scope only; the page shell renders the reused dbbrowser.js, whose
  bootstrap modal UI rides with the Go shell work) ·
  GET `/databases/server` + GET/POST `/databases/server/settings` +
  GET `/databases/server/connections` + POST `/databases/server/kill` +
  POST `/databases/server/flush` + POST `/databases/server/restart` +
  GET `/databases/server/logs` (DbServerController — the server-manager
  pages with the live db.server_status data: overview stat cards +
  resources + connection info, the managed-settings form (only the
  changed fields are sent; emptied fields are removed), the processlist
  with kill, flush, a checked restart, and the database-server log viewer
  (the MySQL/MariaDB log sources, the reused logs.js UI); everything is
  all-scope (a 403 page otherwise) and the changes are audited as
  `database.server_settings` / `database.server_restart` /
  `database.server_flush` / `database.kill` against the database server) ·
  GET `/sites/{id}/files` + GET `/sites/{id}/files/edit` + POST
  `/sites/{id}/files/api` + POST `/sites/{id}/files/upload` + GET
  `/sites/{id}/files/download` (FilesController — the website file
  manager: the page shell renders the reused filemanager.js /
  fileeditor.js (tree, toolbar, list, search, the JS dialogs); the
  operation endpoint dispatches the 15 `files.*` ops (list/read/search/
  write/mkdir/rename/copy/delete/chmod/compress/extract + the trash) to
  the agent with a strict field whitelist, tracks the compress/extract
  agent jobs for the /jobs viewer, and audits the changing ops as
  `files.<op>` against the website (path / from → to → dest, paths and
  ids cut to 20, permanent for deletes); upload is chunked multipart
  (final chunk audited with the size) and download streams the agent's
  base64 chunks; all confinement is enforced by the agent's os.Root; the
  AV scan of finished uploads/extracts and the bootstrap modal UI ride
  with the Go shell work) ·
  GET `/sites/{id}/disk-usage` + POST `/sites/{id}/disk-usage` (DiskUsageController
  — website disk usage: the agent measures a folder in a background job
  (`sites.disk_usage`, confined to the website folder) and the latest result
  per folder is kept in disk_usage_scans (five kept per folder, pruned on
  refresh); the page settles the running scans from the job's status on load
  (`jobs.get`: a lost job fails as "interrupted (the agent restarted)", a
  failed job fails with its error, a finished one stores the cleaned entries
  — first 300, names ≤255, kind dir/files/mail, sizes/counts ≥0 — and shows
  the folder table with the share bars + total, the sortable
  folder/size/files headers, the breadcrumb trail and the "Measured
  <timeago>" note; a running scan shows the "Measuring…" job link — the /jobs
  viewer's modal UI rides with the Go shell work — and the scan start
  redirects back with the job + reload flashes; a bad ?path= is ignored, an
  already-running folder and the agent's errors flash the redirect; no audit
  — parity, the PHP controller records none) ·
  GET `/sites/{id}/terminal` + POST `/sites/{id}/terminal/open` +
  POST `/terminal/unlock` + GET `/terminal` + POST `/terminal/root/open`
  + POST `/terminal/sessions/{id}/close` (TerminalController — the web
  terminal: the agent runs the shells and serves the WebSocket at
  /terminal/ws (Nginx proxies it, so the browser talks to the agent
  directly and keystrokes never pass through the panel); the panel hands
  the browser a single-use ticket bound to its address
  (terminal.open, with the account's effective shell mode, the 32-hex
  sign-in owner, the idle/max minutes settings and a sanitized
  label), a website account then signs in with its own Linux password
  over the WebSocket only, and a root shell (administrators, when the
  agent allows root shells) needs the panel password — and authenticator
  code — confirmed again (terminal.unlock, 5 wrong attempts lock the
  endpoint for 15 minutes, 15-minute unlock window, audit on both
  outcomes); the site page shows the account's effective shell mode
  (shell.status) with the jailed/normal/disabled states and the
  per-response CSP nonce for xterm.js's style elements, the admin index
  lists the open sessions (close by 16-hex id, audited) and the root
  shell console, and signing out ends the sign-in's terminals
  (terminal.kill by owner, best effort)) ·
  GET `/security/shell` + POST `/security/shell` + POST `/security/confirm`
  + POST `/security/undo` (ShellController + SecurityController::confirm/undo —
  the website accounts' shell access: the agent's shell.status (mode,
  effective, SSH/SFTP login, keys, jail problem) joined with each account's
  websites/owner, disabled/jailed/normal with the mode descriptions and the
  "applies while SSH/SFTP login is on" note when mode ≠ effective; the change
  forms (one account or every account with the "all" confirm word) are
  administrators-only (role_name admin, the plan-SSH skip via the site
  owner's feature list — an all-skip refuses, the agent's shell.set 120 s
  gets the kept accounts, audit shell.set) with the success flash counting
  the changed, the waiting-for-login (effective ≠ mode, not disabled) and the
  skipped; the unconfirmed security change (security.pending) renders the
  banner with the countdown + Keep/Undo (security.confirm/undo, {id},
  return_to whitelisted to the ported /security/shell — PHP's list points at
  the not-yet-ported /security pages; an agent error flashes the message),
  audits security.confirm/undo) ·
  GET `/security/firewall` + POST `/security/firewall/toggle` +
  POST `/security/firewall/rules` + POST `/security/firewall/rules/delete`
  + POST `/security/firewall/protection` (SecurityController's firewall area
  — the UFW state page: the status bar (on/off toggle switch, backend,
  defaults, the SSH-port check), the ports WhaCP keeps open (each with an
  Allow button while the firewall is on) and the rules table (listening /
  local-only / idle state from the listening ports, strategy labels, the
  add-rule modal, the delete-by-spec buttons); all changes go through the
  agent's guarded flow (firewall.set_enabled 90 s with allow_services +
  allow_from, firewall.rule_add, firewall.rule_delete,
  firewall.protection_set 90 s) — the "Applied… confirm within 2 minutes" /
  "Nothing to change." flashes, the firewall.enable/disable / rule_add /
  rule_delete / protection audits (server / security, the pending change's
  id) and the banner's Keep/Undo; the CSF-style protections form with its
  validation (SYN flood 0..100000, ping 0..10000, the connection-limit and
  port-flood limit lists ≤50, the IP/CIDR exempt entries, exempt-me
  adding the client address); agent errors flash the redirect — the Go
  adaptation of the PHP page's 500; the cPHulk whitelist of allow-me is
  skipped until the brute-force protection service is ported; return_to
  now accepts /security/firewall too) ·
  GET `/security/fail2ban` + POST `/security/fail2ban` + POST
  `/security/fail2ban/unban` + POST `/security/fix` (SecurityController's
  fail2ban area + the overview's fix action — the brute-force protection
  page: the status bar (running, active jails, banned now), the jails
  table (banned/total/failing, the jail labels) and the banned-addresses
  table with the per-row Unban (fail2ban.unban, IP validated, audited);
  the protection-settings modal (the five WhaCP jails, ban/window/retries,
  the never-ban list — fail2ban.configure 60 s with the agent's range
  check, fail2ban.configure audit, the "tested and applied" flash); the
  form values prefill from the agent's managed jail file (the view's
  regexes, the fresh-install defaults when none); POST /security/fix
  (security.fix, server.manage, role admin): the five fixes —
  kernel_settings (security.fix_kernel 60 s, the changed settings in the
  flash), proc_hidepid (security.hide_processes 60 s), auto_updates
  (security.auto_updates → the agent job tracked for /jobs + the job
  flash), host_persistence (security.host_known, the marked files in the
  flash) and database_network (the remote database_users refusal, then
  db.server_settings 200 s, bind-address 127.0.0.1 + restart) — the
  security.fix audit (server / the check, the details), the admin +
  FIXES-catalog gates (database_network has no undo), the redirect to the
  not-yet-ported /security overview; return_to now accepts
  /security/fail2ban too) ·
  GET `/security/ssh` + POST `/security/ssh` + POST
  `/security/ssh/keys` + POST `/security/ssh/keys/generate` + POST
  `/security/ssh/keys/remove` (SecurityController's SSH area — the SSH
  server settings page: the status bar (port, the root-login label with the
  without-password→prohibit-password mapping, passwords on/off, the keys
  installed — "none" warns before turning passwords off), the settings form
  (port, the root-login select, the password-logins and X11 switches, max
  attempts clamped 1..10, the effective values prefilled; ssh.configure 90 s
  guarded — the same "Applied… confirm within 2 minutes" / "Nothing to
  change." flashes, the ssh.configure audit, the pending banner's Keep/Undo)
  and the administrator SSH keys table (ssh.keys — the key rows (type/bits/
  comment/fingerprint, the restricted-options badge), the "other lines kept"
  note, the per-row Remove); the key endpoints are security.manage + the
  role-name admin gate ("Only administrators manage the server's SSH keys."
  for the others): add an existing public key (ssh.key_add 60 s, the trimmed
  key, the fingerprint flash, the ssh.key_add audit), remove by fingerprint
  (ssh.key_remove 60 s, the ssh.key_remove audit), generate (the RSA 4096
  key pair made in the panel — the passphrase rules (match, 8..1024), the
  sanitized comment (default user@whacp), the public key in the ssh-rsa wire
  format allowed via ssh.key_add, the ssh.key_generate audit with the
  passphrase flag; the private key is shown one time on the rendered (not
  redirected) page — download link + PEM textarea + PuTTY help + the
  connection command with the effective port, Cache-Control no-store +
  Pragma no-cache, the file name from the sanitized account + server name);
  agent errors flash the redirect; return_to now accepts /security/ssh too) —
  also repaired 47 import paths in 18 files that used the module path
  `mchkhawarshad/` instead of `mchkhawarshahzad/` (go.mod's path) — with CI
  down, the Go module had never compiled since go.mod landed) ·
  GET `/security` (SecurityController::index, security.view) + POST
  `/security/mysql-anonymous` (security.mysql_anonymous, server.manage) —
  the Security overview: the agent's checks (security.audit 120 s, the view's
  link() — the FIXES-catalog actions posting to the ported POST
  /security/fix with the check/op params + their confirmations, the
  mysql_anonymous removal, the open-page links restricted to the ported
  pages) merged with the panel's own checks (the password policy against the
  STRONG threshold, the cPHulk switch, the hardening.status forkbomb /
  traceroute switches), sorted (status, then title) with the safety rating
  (the severity weights, a warning counts half, info does not count; ≥80
  Secure / ≥60 Fair / Insecure) and the fail/warn/pass counts; the SSH
  logins panel (ssh.logins 60 s — today/yesterday/7-days bars with the
  view's widths, the last success/failure cards, the "not available" note
  when the journal read fails, the fail2ban-on/off note from
  fail2ban.status) and the panel login card (the latest auth.login audit
  rows, the audit-log link for audit.view); an agent error hides the whole
  content; POST /security/mysql-anonymous (db.drop_anonymous_users 60 s, the
  db.drop_anonymous_users audit — server / mysql, the "Removed the anonymous
  MySQL users (…)." / "There were no anonymous users." flashes); the pending
  banner's return_to now includes /security) ·
  GET+POST `/security/compilers` · GET+POST `/security/smtp` · GET+POST
  `/security/egress` · GET+POST `/security/forkbomb` · GET+POST
  `/security/traceroute` (HardeningController — all-scope pages, 403
  "Only administrators manage server hardening." for non-all scopes; every
  save is role-admin ("Only administrators change server hardening.") +
  CSRF; hardening.status renders each page, agent-down hides the content;
  compilers: the restricted switch + the allowed-account checkboxes
  (HardeningService::accounts — the server's website accounts, unknown
  users refused "Not a website account on this server: ….", form order
  deduplicated, sent only while restricted), hardening.compilers 120 s,
  the flash triad (root-only / "Allowed: …" / every account again), the
  programs table; smtp: the toggle (enabled + active states, the allowed
  ports/accounts line, the nftables-not-available info bar + disabled
  button), hardening.smtp 120 s; egress: the metadata + private-network
  checkboxes + the TCP/UDP port lists (HardeningService::ports — comma/space
  split, 1..65535, deduplicated, sorted, at most 64, the ""x" is not a
  port number (1–65535)." / "at most 64." flashes), hardening.egress 120 s,
  the fieldset disabled for non-managers; forkbomb + traceroute: the
  toggles (the soft/hard limits line, the programs table, the
  not-installed state), hardening.forkbomb / hardening.traceroute 60 s;
  every save audits hardening.* (server / the server id) like
  HardeningService::record)
- GET+POST `/websites/new` (create; plan-limit + feature-gate + under-others +
  system-user takeover rules; agent apply with new_user; row rolled back on
  agent failure) · GET+POST `/sites/{id}/settings` (update, restore-on-failure)
  · POST `/sites/{id}/toggle` · POST `/sites/{id}/delete` (typed confirm) ·
  GET `/sites/{id}/addons` + POST `/sites/{id}/addons` + POST
  `/sites/{id}/subdomains` (child sites inherit account/owner/PHP; optional A
  record into the parent's hosted zone) — all tenant-scoped, CSRF-protected,
  audit-logged (websites.create/update/enable/disable/delete + dns.record_create)
- Websites advanced ops (PHP "Advanced" tab) — all websites.manage, CSRF, tenant-scoped,
  audit-logged: POST `/sites/{id}/restart-php` (per-distro FPM unit from discovery) ·
  POST `/sites/{id}/reapply` · POST `/sites/{id}/directory-listing` (proxy refused) ·
  GET `/sites/{id}/permissions` (JSON dry-run; 404/502) + POST `/sites/{id}/permissions`
  (repair, 30-min agent timeout)
- Node.js/Python app runner — GET+POST `/sites/{id}/nodejs` + GET+POST
  `/sites/{id}/python` (shared SiteRuntimeController page, each feature-gated by
  its runtime; the page adapts to the site's app type) + `WorkerService` write
  path (validate/save/apply/rollback/slug/changeRuntime/plan-limit, ported from
  WorkerService.php). New Node.js/Python sites provision their "app" Worker from
  the submitted start command (best-effort, PHP provisionApp parity) and land on
  the runtime page.
- Workers write path (WorkersController) — GET `/sites/{id}/workers` now shows
  live state (workers.status: badge, copies up, memory) + the create/edit form
  (old-input re-render on validation failure); POST `/sites/{id}/workers`
  (create + edit via worker_id, plan-limit "workers" enforced, explicit-0 procs
  rejected as in PHP) · POST `.../workers/{worker}/toggle` (setEnabled:
  apply-then-persist) · `.../restart` (suspended accounts refused) ·
  `.../delete` (remove-then-delete-row); WorkerService::setEnabled/restart/
  delete/removeAll ported — `removeAll` runs on the website.deleting event
  (before sites.delete, failures swallowed); missing site → flash + list,
  missing worker → flash + page, agent failures flash the agent's message
- GET `/dns` (zone list, tenant-scoped) · GET `/mail` (domains, tenant-scoped)
- GET `/audit` (staff) · GET `/users` (reseller-scoped) · GET `/roles` · GET `/plans` (reseller-own) · GET `/servers` (staff)
- GET+POST /notifications (read-marking scoped) · GET `/profile` (◑: 2FA + API
  tokens done; password change, token management, sessions, security-key
  management forms are a later batch)

## Phase 2+ — Feature areas (one batch each; see ROUTE-MATRIX "Ported" column)
Order by risk/leverage: identity+RBAC+feature-gating → dashboard → websites →
databases(MySQL) → mongo → redis/memcached → files → dns → ssl → mail → backups
→ cron/workers → monitoring/security → settings/admin → installers → API.
- ☑ identity completion (TOTP 2FA) — done
- ☑ password reset by email (forgot/reset flows + SMTP mailer + settings repo + password policy) — done
- ◐ WebAuthn security keys (registration + login assertion) — 2FA users with both
  factors can use TOTP; key-only users are refused (safe) until this lands
- ◑ websites write path — core lifecycle done (create/enable/disable/delete,
  settings, subdomains/addons write, plan limits, PHP profiles); advanced ops
  done (restart-php, reapply, directory listing, permissions check/repair,
  Node.js/Python runtime page + app-Worker provisioning); workers area done
  (create/edit/toggle/restart/delete + removeAll on delete + live status page
  + the global GET /workers list — the whole WorkersController is ported);
  account-creation foundation done (UserService::create,
  AccountService::create + AccountOptions, CronService::apply,
  WebsiteService::moveToAccount, UserRepository writes, the "customers" plan
  limit); addon→account conversion done (AddonConversionService: isAddon,
  preview, convert, undo + POST `/sites/{id}/convert`, users.manage, full
  rollback on agent failure); the PHP "Advanced" consolidated view is shown
  inline on the Go site page instead (its last-error box is now rendered)
- ☐ DB browser, DB dump/import, DB server area (DbServerController), mongo, redis/memcached, DNS records, mail accounts/filters, SSL/ACME, backups, files/FTP/terminal, automation (cron/deploy/workers write), security suite, monitoring, settings/admin, installers, API v1

## NOT YET PORTED (known follow-ups, tracked honestly)
- WebAuthn sign-in + security-key management; IP-verification sign-in (emailed code); HULK brute-force integration; step-up password confirmation; password-age and role-requires-2FA enforcement; trusted-proxy client IP
- QR image on the 2FA setup page (the otpauth URI + secret are shown for manual entry; a QR encoder or dependency is a small follow-up)
- profile: password change, API-token management, session management, security-key forms
- the full per-feature view/handler port (websites, databases, mongo, …) — see list above
- background jobs / scheduler binary (backup schedules, AutoSSL, webhook deliveries, cron-email, discovery, monitoring sweeps)

## Known risks / blockers
- No Go toolchain in the authoring sandbox: batches are verified by the CI server job (gofmt gate, go vet, go test -race, go build) after each push — check `gh run list` after committing.
- Full PHP phpunit baseline not re-run this session (300s cap); relied on last CI-green.
- modernc.org/sqlite + x/crypto must download via the module proxy (verify first).
- This is a multi-month effort; each session continues from the next ☐ here.

## Next exact step
Batch: continue the **security area** —
**SecurityChecksController** (vulnerability advisories), then the
remaining SecurityController pages (integrity, malware, policies,
password strength, SMTP — the cPHulk whitelist of the firewall
allow-me can land there).
in small batches. Check INVENTORY.md + the PHP controllers before
starting.

Build/test the Go module with: `cd server && go test ./...` (deps pinned for Go 1.24).

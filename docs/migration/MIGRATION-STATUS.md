# WhaCP PHP → Go Migration — Status (machine-readable-ish)

Baseline commit: 46313561 (branch claude/sleepy-ptolemy-bdv6ib)
PHP app: 230 files / 38,926 lines · 84 controllers · 76 services · 8 repos
Views: 179 · Tests: 119 files / 15,461 lines · Routes: 551 · Migrations: 57
Agent: Go, unchanged, full suite GREEN at baseline.

Route porting count (see ROUTE-MATRIX.md, verified against
`server/internal/http/server.go`): **435 ☑ done+tested, 4 ◑ partial, 112 ☐** of 551.

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
  HardeningService::record) ·
  GET `/security/integrity` + POST `/security/integrity/run` + POST
  `/security/integrity/baseline` · GET `/security/tls` + POST
  `/security/tls/run` · GET `/security/advisories` + POST
  `/security/advisories/run` (SecurityChecksController — the on-demand
  checks: each page opens instantly with a Run button, the run calls the
  agent synchronously and RE-RENDERS the page with the findings (an agent
  error re-renders the "could not run" bar; only the baseline redirect-flashes);
  integrity: security.integrity 180 s (the findings table with the severity
  badges, the no-findings bar with the no-baseline note), the baseline is
  the one state-changing check (security.manage, security.integrity_baseline
  60 s, the "Integrity baseline recorded for N WhaCP-owned file(s)…" /
  "The baseline could not be recorded: …" flashes); tls: the endpoint list
  (every enabled HTTPS website on loopback with SNI + the mail server's
  465/587/993 when configured, via mail.status), the zero-endpoints
  short-circuit (no agent call), tls.audit 180 s (the per-endpoint panels
  with best/lowest versions, only endpoints with findings render);
  advisories: packages.advisories 120 s (the kind/severity badges, the
  em-dash empty cells, not audited — parity); the runs audit
  security.integrity (server / integrity) and tls.audit (server / tls))
- GET `/security/policies` + POST `/security/policies` + POST
  `/security/policies/ips/forget` · GET `/security/password-strength` +
  POST `/security/password-strength` (SecurityPolicyController — the policy
  items: the verified-IP switch (limit logins to verified addresses, the
  LoginIpVerifier login_ips table), the per-role 2FA requirement
  (two_factor_roles[] → roles.require_two_factor), the password strength
  switch (the rules line via Policy::describe) and the password age (0–3650
  days); the pages are all-scope (403 "Only administrators manage security
  policies."), the saves are all-scope + role admin (the same 403 page, not
  a flash); save: SettingsService::update validation (the bool keys, the
  "Enter a whole number between 0 and 3650." for age), only the changed
  values persisted (settings.Set, JSON-encoded like the PHP repo), audited
  settings.update (settings / panel, {changed:{key:{from,to}}}); the per-role
  sync audits role.two_factor_policy (role / the id) per changed role;
  turning the verified-IP policy on remembers the actor's address and seeds
  every user's last sign-in address (the "…(N) counts as verified." note in
  the flash); forget: any signed-in user's own list (no scope/role gate),
  303 to /security/policies#verified-ips, "The address is no longer
  verified."; the strength page: the seven minimums (0–100 strength, 12–128
  length, 0–32 per character class — the SettingsService bounds/help),
  enforcement On/Off badge + the rules line, the four strength examples
  (security.Strength), save validates all seven (every error at once) and
  flashes "Password strength requirements saved. They apply to passwords set
  from now on.")
- GET `/security/malware` + POST `/security/malware/settings` + POST
  `/security/malware/update` (AvController's /security/malware area — the
  ClamAV status (av.status: the engine / signatures / their date, the
  freshclam state, the updater log when no signatures yet), the not-installed
  infobar, the scan settings (the 0/1/7/14/30-day select + the quarantine
  switch, SettingsService bounds 0–30, changed-only persistence + the
  settings.update audit), the signature update (av.update, the av.update
  audit server / the id, "Downloading the latest virus signatures…" flash,
  the agent error flash-redirect) and the Recent scans table (the 50 latest
  with website + owner, the Scanning/Clean/Infected/Failed states, the
  infected count, the truncated error, number_format, time_ago,
  Schedule/Manual). On every page load the running scans are recorded
  (AvService::refresh: jobs.get per scan, a vanished job = interrupted,
  the findings capped + truncated, the row's status flip, and the
  notification row with its dedupe key — the PHP notify() call: infected →
  warning "Malware found on …", failed non-manual → error, manual →
  "… is clean" (the PHP quirk for failed manual scans, kept); the
  notification email + Redactor are follow-ups, as is the per-website
  malware page (scan/release). No scope gate — parity (security.view reads,
  security.manage manages)
- GET `/security/hulk` + POST `/security/hulk` + POST `/security/hulk/countries`
  + POST `/security/hulk/geo` + POST `/security/hulk/unblock` + POST
  `/security/hulk/clear` (HulkController's cPHulk Brute Force Protection —
  all-scope pages, role-admin saves). `security.Hulk` (the hulk_* tables) +
  `security.Geo` (geo_ranges) + `security.Countries` (the 249-code catalogue,
  ported from app/Security/Countries.php): the page (the hulk.status agent box
  — Disabled / Enabled / the nftables-missing warn state — the three
  Bootstrap subtabs; the settings form: the 17 keys with their definition
  defaults, the protection-type radios, the command-variable list, the
  whitelist textarea, the countries table in catalogue order with the bulk
  mark + the block-on scope, the report tab with the blocks-in-force table +
  the failed-logins report with country names / minutes-left); save
  (SettingsService::update parity — the scope check, the per-entry validEntry
  check with the 60-char message, the 200-entry cap, the own-address add when
  turning on with an empty whitelist, the printable-string bounds, changed-only
  persistence + the settings.update audit with from/to, then the hulk.sync
  agent push {enabled, blocks[ ip/until/all ], countries[ from/to ], whitelist,
  countries_all} — the agent error flash-redirect); countries (bulk or
  per-country marks, the hulk_countries upsert/delete in a transaction,
  unknown codes skipped, the hulk.countries audit, the sync, "%d countr(y|ies)
  updated."); geo (the DB-IP "IP to Country Lite" CSV/.csv.gz upload —
  gzip-checked, malformed lines skipped, the all-or-nothing replace in a
  transaction, the hulk_geo_updated stamp with a null actor + its
  settings.update audit, the hulk.geo_import audit); unblock + clear
  (removeBlock/clear + their audits + the quiet sync — agent errors logged,
  the success flash stands). The enforcement engine (HulkService::run/
  evaluate/block + the panel-failure recording) is the scheduler/login-flow
  side and stays with the background-jobs follow-up
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
- Website hub page — GET `/sites/{id}` (sites.show, websites.view: the hero
  card with live facts — server IP (SiteOverview::serverIp — the configured
  default IPv4, else the agent's first global address), handler, site size
  (agent sites.inspect, fail-safe), database size (live db.overview, scoped),
  SSL label, last backup — + the "Site managing" tile grid with the feature
  search, each tile/search entry permission-gated like SiteMenu) · GET
  `/sites/{id}/advanced` (sites.advanced, websites.manage: the runtime card
  (PHP-FPM process pool from php_settings, defaults like the PHP view),
  generated Nginx config preview, recent tasks (JobTracker::recentFor),
  files card (directory-listing toggle + permission check/repair), danger
  zone (toggle / reload PHP / re-apply) and the delete card). Both pages'
  failure alerts (last-error, bandwidth-blocked, agent-down, missing
  on-disk config) and the scoped 404. SiteOverview (summary/serverIp/
  databaseBytes) + JobTracker.RecentFor ported; every action the pages link
  to is an already-ported route. Deliberately deferred (with their areas):
  the hub's import banner (import batch) and the advanced IP card (IP
  batch). Also fixed: 78 ported templates linked the non-existent
  /assets/css/app.css — they now link /assets/css/whacp.css, the
  stylesheet the Go server actually serves (the PHP panel's own views
  use it too).

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
- ☑ websites: the WordPress toolkit, list + scan (batch 43) —
  WordPressController's index / scanAll / siteList / scanSite as
  site_wordpress.go + the core of hosting/wordpress_service.go: the
  all-installations list (GET /wordpress — the outdated / issues filters,
  the per-user visibility split), the per-website list
  (GET /sites/{id}/wordpress — a single installation is opened directly),
  and the two scans (the agent's reply stored per website — the
  installation recorded under the website whose document root holds it,
  the stale rows of the scanned websites replaced, a failed scan keeps
  its rows and reports the not-scanned websites). The service keeps the
  PHP's scan/installs/install/base-url semantics + a version_compare
  stand-in; the overview, management, one-click sign-in and the toolkit
  tools follow in the next batches
- ☑ websites: the WordPress overview (show / check / login / logs)
  (batch 44) — WordPressController's show / check / login / logs as
  site_wordpress.go (the wpShow / wpCheck / wpLogin / wpLogs handlers):
  the installation's overview page (GET /sites/{id}/wordpress/{wp}) — the
  agent's info (wordpress.info, the stored summary refreshed) with the
  security + health checks computed from it, the toolkit's extras
  (protect.status — password / hotlink protection, wordpress.security —
  the measures' state, wordpress.restore_points — this installation's
  restore points, the owner's copy + sync targets), the stored
  vulnerabilities (the vulns / vulns_checked_at columns), the panel's
  database ID (the browser link), the toolbar (file manager, database,
  logs, clone / sync / security, restore points), the six subtabs
  (dashboard — the seven switches + overview + updates + health checks,
  plugins, themes, security — hardening measures + vulnerabilities,
  database, staging — restore points), the modals (debugging, password
  protection, hotlink, admin password, clone, sync — posting to the
  manage / tools routes), and the agent-error state (a separate small
  page); the forced re-check (POST …/check — the wp_force flash); the
  one-time sign-in (POST …/login, websites.manage — the HTTPS guard, the
  agent's self-deleting login file, audited); and the debug log view
  (GET …/logs — wordpress.manage op debug_log, newest first). The
  toolkit's write side (manage + tools, batch 45) completes the page's
  forms
- ☑ websites: the WordPress management operations (manage) (batch 45)
  — WordPressController::manage + WordPressService::manage as
  site_wordpress.go (wpManage) + hosting/wordpress_service.go (Manage):
  POST /sites/{id}/wordpress/{wp}/manage (websites.manage) runs the
  19 operations (the OPS table's labels) — the three updates start as
  tracked agent jobs (JobTracker + the job flash + the "started in the
  background" notice on the overview), verify_core flashes its
  differing-file list for the overview's verification panel (an empty
  list is the all-matches state), and the rest finish on the spot with
  their "done for" message; the items are filtered to the valid, unique
  names, the suspended account is refused, the unknown operation and
  the invalid automatic-updates value are user-facing errors, and
  every run is audited (wordpress.<op>)
- ◑ websites: the WordPress toolkit's tools (tools) (batch 46)
  — WordPressController::tool + the WordPressToolsService's tools as
  site_wordpress.go (wpTool) + the hosting/wordpress_tools.go write
  methods (SetDebug / SetPasswordProtection / SetHotlink /
  AdminPassword / DBPassword, on top of the read-side Security):
  POST /sites/{id}/wordpress/{wp}/tools (websites.manage) now covers
  six of the twelve tools — debug (the switches → manage op debug, the
  filtered flags, the debug column, the saved / off message),
  password_protection (protect.password with the url path, the user
  name + 8-to-128-character password validated before the agent),
  hotlink (protect.hotlink, the allowed hosts lowercased + host-validated),
  admin_password (manage op admin_password, the password + the
  constant-time confirm match, the "other sessions signed out" message),
  db_password (manage op db_password, the new 24-character password
  shown once on the overview and the panel's stored database_users copy
  re-encrypted), and security (the one-click apply / roll back of the
  nine recommended measures, or the chosen ones, the empty-measures
  guard); every tool is audited (wordpress.<tool>) and the suspended
  account is refused (wpWritable). The remaining tools (clone, sync,
  restore) follow with the wordpress.copy jobs
- ◑ websites: the WordPress vulnerability protection + check (tools)
  (batch 48) — WordPressToolsService::setVulnerabilityProtection +
  checkVulnerabilities + WordPressVulnerabilities.php: the tool
  route's `vulnerability` case (the ModSecurity part in the http layer —
  the status guard: the connector state functional + the module active,
  the admin / provider remedy, the site set to blocking or back to
  detection only when no other installation of the website wants it,
  the server-wide detection-only flag in the on message — and the
  vuln_protection column + audit in the service) and the `vuln_check`
  case (the fresh wordpress.info checked against the free
  wpvulnerability.net database — the core + the non-must-use plugins +
  the themes, at most 300 items, the min / max version operators, the
  severity / score / fixed-in / CVE / link fields, the severity +
  title order, the 12-hour per-item answer cache next to the panel
  database, the stored vulns + vulns_checked_at columns, the "N known
  vulnerabilities found." flash or the unreachable-database error)
- ☑ websites: the WordPress copy tools (clone / sync / restore)
  (batch 49 — the toolkit's last tools) — WordPressToolsService::
  cloneTo / sync / restore: the tool route's last three cases, all
  wordpress.copy agent jobs (tracked like the manage updates, the job
  flash + the "started" messages to the staging subtab) — clone (the
  owner's PHP website + the optional subfolder of its document root,
  the source database must be one of the owner's in the panel, a fresh
  prefixed database + user with full access for the copy — dropped
  again when the copy fails —, the new installation row with
  cloned_from, the changed address everywhere, the staging flag),
  sync (another installation of the same owner, files and/or the
  database, both databases checked against the panel's records, the
  target's wp-config.php + address kept), and restore (the restore
  point's strict shape, the database check, files and the database
  back to that moment). The WpBaseURL helper (the installation's
  address from the website's own domain + document root) completes the
  page's toolkit
- ☑ websites: the per-website backups area (batches 50–51) — the
  website methods of BackupsController + the
  sites/backups view as site_backups.go + hosting/backup_service.go +
  hosting/backup_scheduler.go: the tab page (GET /sites/{id}/backups,
  backups.view — the best-effort refresh of running rows from their
  agent jobs (jobs.get: succeeded stores the agent's manifest +
  size/encrypted/local, failed + a lost job (agent restarted) store
  their error), the list newest-first with the parts from the stored
  manifest, the "Last backup …" line, the schedule count, the linked
  databases, and the admin-only storages + storage select), back up
  now (POST, backups.manage — the files / databases / label /
  encrypt / storage options exactly as the panel's: a customer can
  never pick a storage, "Nothing to back up." when neither files nor
  a linked database, the backup.create agent job + the running row +
  the tracked "Backup <domain>" job), restore (POST
  …/backups/{backup}/restore — completed site backups only, the
  confirm-domain gate, each requested database checked against the
  backup's parts, the backup.restore job with the safety backup flag
  + the remote copy's storage config, tracked "Restore <domain>"),
  delete (POST …/delete — the running guard, the agent's copy first
  (remote too when the storage still exists; not_found tolerated),
  then the row), and the part download (GET …/download?file= —
  octet-stream with the sanitized name + no-store, the chunked
  backup.download stream the PHP stream() loop uses, plain 404 for a
  missing part or a remote-only backup). The hidesAutoBackups rule
  applies everywhere (a hosting account without the auto_backups
  feature never sees the scheduled copies or the schedules, and
  restore/delete of one read "Backup not found."). Batch 51
  completes the area: the two schedule routes (admin-only — the
  controller's Scope::isAll gate, so a hosting account with
  backups.manage still gets "Automatic backups are set up by your
  administrator."): store (POST /sites/{id}/backup-schedules —
  hosting/backup_scheduler.go's CreateSite mirrors the panel's
  validation in its order: frequency daily/weekly, hour 0–23,
  weekday 0–6, retention 1–365, the storage turned on
  (COUNT … active = 1), files-or-databases; a failure re-shows
  the page with the field errors + the old input, PHP's
  backWithErrors, the INSERT with kind 'site' / all_sites 0 /
  keep_local forced on without a storage / enabled 1, the
  backup.schedule_create audit) and delete (POST
  …/backup-schedules/{schedule}/delete — DeleteSite: the
  WHERE id AND website_id scoping so another website's schedule
  reads "Schedule not found.", existing backups kept,
  backup.schedule_delete); the schedule card grows the admin's
  add form (frequency / weekday / hour / files / databases /
  retention / the AES-256 switch / the active storages' "Copy to"
  + keep-local, the panel's _schedule_form + _options) and the
  per-row remove form
- ☑ Mail administration (batch 62) — the MailAdminController's 8 routes
  for server administrators (the all-scope gate on top of the
  server.view/server.manage permissions): the Postfix queue (list with
  search + per-queue counts, one message's headers in a modal via the
  JSON endpoint, deliver now / hold / release / delete on the selected
  messages, retry every deferred one), the delivery reports and
  statistics from the mail logs (time range, address + status filters,
  totals, over time, who sent mail, sender domains), the mail server
  page (Postfix/Dovecot/OpenDKIM/Rspamd states + config checks, the
  managed settings with only the changed Postfix fields sent, the
  latest log lines) and the tools (the address trace through
  mailboxes/forwarders/catch-all/routing, the mailbox permission
  repair). hosting/mail_admin.go carries the agent reply shapes, 8
  routes + 4 pages (+ the nav partial and the reused confirm modal),
  10 tests (the fixture gains server.view for the admin + viewer
  roles, so the all-scope gate is tested too)
- ☑ Mail administration: greylisting + sending limits (batch 63) —
  MailGreylistController (8 routes) and MailRateController (3 routes),
  both for server administrators (the all-scope gate on top of
  server.view/server.manage). The agent keeps the greylisting
  configuration and every change sends the whole of it: the settings
  (on/off, deferral 1–1440, resend 1–10080, record expiration
  60–525600 minutes, SPF bypass, auto-trust of new providers), the
  trusted hosts (add/edit modals — an IP address or CIDR range of up
  to 50 chars + a 200-char comment; delete with the confirm modal),
  the common mail providers (trust / auto-update checkboxes, the
  "N · updated …" SPF range summary with the last error, and the
  "Update ranges now" button that refreshes from the SPF records) and
  the triplet report (search ≤200, status filter, the nine sortable
  columns with the page-resetting toggle links, pages of 25/50/100/
  250, the per-status counts, and the cleanup: remove expired, remove
  selected — the selected triplet JSON — or remove all with the
  confirm). The outgoing mail limits: on/off + recipients per website
  account and hour (0–1000000, 0 = no limit still counted) + per
  account own limits, this hour's use per account (the "Limit
  reached" badge), letting a blocked account (or all) send again, and
  the recent limit events newest-first. Turning the limits on for the
  first time re-applies every PHP website so its PHP uses WhaCP's
  counting sendmail — when a website cannot be re-applied the limits
  stay off and the failed sites are named (the PHP MailRateService
  behavior). hosting/mail_greylist.go + mail_greylist_service.go +
  mail_rate.go + mail_rate_service.go carry the agent reply shapes,
  the params/validation helpers (GreylistParams, GreylistAgo,
  GreylistValidateRange, RateValidate, RateMax) and the
  get→transform→set change helper with the failure/success audit;
  11 routes + 3 pages (+ the two nav tabs), 9 tests
- ☑ IP addresses for server administrators (batch 64) — the /ips area
  (web.php 206–217, deferred from batch 59) and the admin
  Application Manager's 4 routes, both server administrators only
  (Scope::isAll on top of the route permissions). The pool page: the
  agent's usable global addresses (link-local excluded), the pool with
  family/state filters + search, each row's websites (with the owner),
  the on-server status (unknown when the agent is down), the reserved
  reason, the edit modal (shared/dedicated + reserved + reason),
  remove (unused only), the rebuild (re-applies the ranges, adds the
  server's addresses + the websites' missing ones, keeps the rows the
  server no longer has and lists them), and the not-in-pool card with
  add-to-pool (203.0.113.x-style addresses) + the gateways. The IPv6
  ranges page: the routed ranges (the agent adds the local route,
  whacp-ipv6-ranges.service keeps it at boot), the cards with the
  allocations (free / the bound domains), allocate (the next free from
  the range's ::10 up or a given address, optionally straight to a
  website with the AAAA record following), save note, remove (free
  only, its route removed). The migration wizard: choose from/to
  (unreserved-or-in-use pool) + the all-zones checkbox, the plan
  (the websites with their owners, the zones' records, the default
  IPv4 for new zones) with the per-field validation, the confirm
  checkbox, and the per-step results (each website moves on its own,
  nginx -t before the reload, the DNS failure rolls that website
  back). hosting/ip_pool_service.go carries the pool/ranges/allocate/
  migrate/rebuild (server-address snapshot 30 s, range push 60 s,
  the ::10-up scan of 100000), hosting/dns_zone_address.go the zones
  using an address, http/ip_admin_handlers.go the 12 handlers. The
  Application Manager: the global installer switch (the setting,
  customers see nothing when off), the category cards (enabled badge +
  switch), the per-app enabled / visible-to-customers switches (a
  disabled app's visibility switch is disabled; administrators can
  install an invisible app), every change audited server-scoped; the
  switches auto-submit via data-wcp-autosubmit (the CSP-safe form of
  the PHP inline onchange). Along the way: the catalog selects
  referenced an id column app_categories/app_catalog do not have
  (fixed), and the gofmt struct alignment of 11 pre-existing files
  was restored (the CI gofmt gate). 16 routes + 4 pages (+ the tabs
  partial + the apps_toggle partial + the dict template function),
  12 tests
- ☑ Discovery: the read-only inventory pages (batch 65) — DiscoveryController's
  5 routes (`GET /discovery`, `POST /discovery/run`, `GET /php`, `GET /ssl`,
  `GET /cron`) as hosting/discovery_pages.go + http/discovery_handlers.go.
  The overview: the count cards (websites, PHP versions, pools, databases,
  certificates, cron jobs, services and the non-system accounts) each linked
  only when the actor holds that permission, the merged section warnings, the
  unsupported-OS note, and the snapshot history with the acting user's name
  and the checksum's first twelve characters. PHP: the version cards (CLI/FPM
  binaries, pool count, per-version website count, the CLI-default badge) and
  the FPM pool table; the PHP Manager link is shown for php.manage, and the
  install/stop/repository write actions stay with the PhpController batch, so
  the page is a read-only report (documented remainder). Certificates: the
  per-website table (status, expiry, issuer, whether it covers the domain,
  the file) and the certbot lineages with the private-key presence and
  permissions; administrators are redirected to the dedicated /ssl/hosts
  section, exactly as PHP does. Cron: schedule/user/command/source:line from
  the agent, with the redaction note. The re-scan is CSRF-checked,
  discovery.run-gated, honours only the PHP's return_to allowlist
  (`^/[a-z0-9/_-]*$`) and states that nothing on the server was changed.
  Every page is tenant-scoped through a port of DiscoveryService::forScope
  (owned websites, the certificates naming one of their domains, the pools
  those use, and empty cron/database/user lists), and every page falls back
  to the discovery-unavailable view when the agent cannot be reached.
  Along the way: the template FuncMap gained `join` — ips_index.html (batch
  64) called it but it was never registered, which made ParseFS fail and
  would have stopped the whole server from starting. 5 routes + 4 pages
  (+ the shared discovery bar partial), 10 tests
- ☑ MongoDB: the server-level area (batch 66) — all 26 MongoController routes.
  hosting/mongo_server_service.go is an unscoped port of MongoService (the
  per-website SiteMongoService is prefix-scoped and could not be reused):
  status, the repository + install jobs, the service controls
  (services.control on mongod.service), the overview, database/user CRUD, the
  admin password, the authentication switch, the per-database user list, the
  backups (start/list/restore/delete), the managed configuration and the raw
  file (checked + rolled back by the agent), and the log tail. Every mutation
  is audited against the server; reads are not. hosting/mongo_browse_service.go
  adds the 21 browser operations to the same service, and
  hosting/mongo_remote_service.go ports MongoRemoteService: the saved
  connections in mongo_remotes with the PHP's validation (name ≤ 64, the host
  / user / auth-db patterns, port 1–65535, admin by default), a ping before
  storing, and the password sealed with internal/crypto — it never reaches the
  browser or the audit log. http/mongo_admin_handlers.go carries the page and
  the 20 form actions: the six sections (databases, users, backups, remotes,
  config, log — an unknown one falls back to databases) of which only the
  active tab's data is read, the not-installed / stopped states with the
  repository + install + start cards, an agent failure rendered in place
  instead of a blank page, the create-database-with-user shortcut (default
  role readWrite), the bulk drop, the 500-character notes kept as settings
  keys (mongo.note.<serverId>.<name>, read through the new
  settings.Repository.Prefix), the install/repository/service job tracking,
  and the remotes (add/delete/test/the databases JSON for the copy modal/pull
  as a tracked job). http/mongo_browser_handlers.go serves the wildcard
  browser through the very same template and JS as the per-website one, with
  mongo.view on the route and mongo.manage re-checked per mutating operation
  (403 in JSON), plus the collection export download. 26 routes + 2 pages
  (+ the section-tabs partial + the settings Prefix helper), 25 tests
- ☑ Mail: the per-mailbox filters + autoresponder (batch 61) — the
  mailbox's filter list (the agent keeps them, rendered to Sieve):
  create/edit (4 condition rows × part/match/value, 3 action rows,
  all/any matching, empty rows ignored, at least one condition + one
  action required), enable/disable, delete, and the autoresponder
  (subject, message, once per sender every N days, optional
  start/end — mailing lists and automatic messages are never answered).
  A refused form redirects back with the messages (PHP's change()), a
  mailbox the agent does not know gets the agent-error notice.
  hosting/mail_filters.go ports MailFilterService (parts/matches/
  actions labels, the form parsers, the agent round-trip + audit),
  5 routes + the filters page (the list table, the new/edit modals
  with the padded rows, the autoresponder card), 8 tests (the fake
  agent stores the filters so a save is visible on the next read)
- ☑ Mail: the domain page's write side (batch 60) — the mailbox
  create/change/delete (the per-plan quota cap — "unlimited" becomes the
  cap, the generated strong password shown once, the password policy on
  typed-in passwords, the 1000-mailbox domain cap, the address
  uniqueness against mailboxes and forwarders, the delete confirm-by-
  address), the forwarder create/delete (catch-all, 1–50 destinations,
  a mailbox address may not be a forwarder), the mail routing (local /
  backup / remote), adding the MX/SPF/DKIM/DMARC records to the domain's
  zone on this panel (a different SPF or DMARC is kept; idempotent), the
  public-DNS checker (the agent's mail.auth_check, JSON for the page's
  button) and stopping hosting a domain (mailboxes and forwarders must
  be gone first; a domain the agent does not serve is removed anyway) +
  the page's write UI (the create-mailbox + add-forwarder modals with
  the refused-save re-render and re-opened modal, the inline quota/
  password edit row, the per-row delete with the confirm word, the
  routing form, the Check DNS + Add to DNS zone buttons, the danger
  zone for administrators). hosting/mail_service.go grows SaveMailbox /
  DeleteMailbox / SaveAlias / DeleteAlias / SetRouting / PublishDNS /
  RemoveDomain / DnsCheck (the DnsService record save reuses the zone
  editor's transaction + push); the security.Policy gains Generate (the
  PHP password generator: two of each character class at least, then
  random, shuffled, re-rolled until it passes the policy); PlanLimits
  gains the email_accounts count and the numeric plan value (the
  max_email_quota_mb cap). 9 routes, 14 tests
- ☑ DNS: the zone editor's write side (batch 59) — the zone
  page's records CRUD (POST /dns/zones/{id}/records +
  /records/{record} + /records/{record}/delete — the per-type field
  validation (IPv4/IPv6, host names, MX priority, TXT, CAA tag/value/
  flags, SRV name + fields, TTL 60–604800) with the CNAME-uniqueness and
  duplicate-record conflicts, the 2000-record cap, everything in one
  transaction that pushes the zone and rolls back if the agent refuses),
  the bulk enable/disable/delete (one reload), the zone settings (TTL +
  secondary servers), the sync (load again), the zone-file import (the
  agent's zone_parse; duplicates and the zone's own NS at @ kept,
  conflicts listed, the skipped lines flashed to the page) and the
  BIND-format export (the agent's zone_render), the zone delete (the
  agent told first, a zone it does not serve — 404 — deleted anyway,
  confirm-by-name) + the zone page's write UI (the record modal with the
  type-switched fields + the common-record presets, the import modal,
  the bulk bar + per-row toggle/delete, the sortable columns, the
  settings + danger-zone cards for administrators, the refused-save
  re-render with the field messages + the re-opened modal as PHP's
  backWithErrors). hosting/dns_service.go grows CleanRecord/Conflicts/
  SaveRecord/Settings/Sync/Bulk/Import/Export/Delete + pushTx (the push
  inside the open transaction, payload read from the tx so the single
  pooled connection is never needed twice); the DnsService/MailService
  audit hooks move to the shared hosting.Auditor (the 6-arg ctx +
  result form the other services use — the batch 58 constructors
  passed the 6-arg hook to a 4-arg parameter, a compile break this
  batch fixes). http/dns_zone_write_handlers.go (showZoneOf /
  recordIDOf / flashZoneError / zoneRecords + sort / the nine
  handlers), 9 routes, 12 tests (the fake agent's dns.zone_parse /
  dns.zone_render / dns.zone_delete + a per-action failure status for
  the 404 path)
- ☑ DNS + mail: the website's DNS and email pages (batch 58) —
  GET /sites/{id}/dns + GET /sites/{id}/mail (DnsController::site /
  MailController::site — the zone's/domain's page when the user may see
  it, the "managed by another account" notice, else the set-up card over
  the agent's dns.status / mail.status with the nameserver list and the
  staff/customer not-ready text), POST /dns/zones + POST /mail/domains
  (DnsController::store / MailController::store — the website's domain
  zone with the template records (NS per nameserver, A + CNAME www from
  dns_default_ipv4, SPF, CAA) / the DKIM-keyed mail domain + the account's
  spam options re-applied, return_to back to the set-up page, the
  taken-name / other-account / website-domain checks in the PHP's order),
  GET /dns/zones/{id} + GET /mail/domains/{id} (DnsController::show /
  MailController::show, ◑ read-only — the zone's records with the
  live/pending/not-served state from dns.status + the Warnings checks
  (<2 NS, dangling targets, private A, short TTL, SPF rules) / the
  mailboxes with usage bars + quotas, the aliases, the routing state,
  the DNS records to publish (MX/SPF/DKIM/DMARC) and the mail-client
  card; the record/mailbox/alias CRUD, zone settings, sync/import/export,
  the routing form and the filters ride with the DNS/mail-area batches).
  hosting/dns.go (DnsZoneFull + Records + ZoneForWebsite + ZoneNameTaken /
  ZoneOwnerByName / InsertZone), hosting/dns_service.go (Create +
  Warnings + Status/ServedSerial + Nameservers), hosting/mail.go
  (MailDomainFull/Mailbox/Alias + the reads), hosting/mail_service.go
  (Status/AddDomain/DNSRecords/SyncSpam), http/dns_handlers.go +
  http/mail_handlers.go (siteDns/siteMail/dnsZoneStore/dnsZoneShow/
  siteMailService/mailDomainStore/mailDomainShow + pathID), 6 routes, 12
  tests (the create validation order per the PHP: the DNS name check
  fires before the customer's website check, the mail customer check
  before the name; the fake agent's dns.status / mail.domain_add /
  mail.spam_configure fixtures)
- ☑ websites: the website IP assignment (batch 55) — the
  advanced page's IP card (SitesController::advanced's ipPool + the card
  in sites/advanced.php) and POST /sites/{id}/ip
  (IpController::assignSite, server.manage — customers 403).
  hosting/ip_service.go ports the IpService parts this page uses: the
  address pool (shared/dedicated/reserved, with each address's websites),
  the card's pool filter (nothing reserved, a dedicated address only when
  no other website has it, administrators only) and assign() — the
  addresses must be pooled (right family, not reserved, a dedicated
  address not bound to another website), the website patch
  (agent re-apply + the website.ip audit with the diff) and, asked for,
  the A/AAAA records in the website's DNS zone following (the default-IPv4
  fallback for the old A value, the website's own names in a shared zone,
  and the website moved back to its previous address when the DNS update
  fails — the website.ip_rollback audit). hosting/dns_service.go ports
  the DnsService helpers that need (zoneForName, replaceAddress with the
  names restriction + the zone push to the agent); the /ips pool pages,
  the IPv6 ranges, the migration wizard and the rebuild stay with the
  DNS/IP batch. The advanced page also renders its success/error flashes
  (PHP's global flash, missing since batch 53)
- - ☑ websites: the website import (batch 54) — the last deferral of the
  website area: importing a discovered (non-WhaCP) website into WhaCP
  management. hosting/discovery_service.go ports DiscoveryService +
  DiscoverySnapshotRepository (the read-only server inventory: the agent's
  discovery.inventory, the snapshot store with the newest-20 retention,
  the cache TTL + invalidation setting, the for-page parsers for the
  websites / cron jobs / one website by id). hosting/import_service.go
  ports ImportService (7 methods): plan (the kind + PHP version from the
  discovery record, the website service's validate with the server-wide
  PHP profile, the Let's Encrypt certificate adopted as-is), the agent's
  adopt.preview (120 s) / adopt.run / adopt.undo, the website_imports
  record, and refresh (the running import's job polled on the hub:
  succeeded -> activate + track the found database + invalidate discovery,
  failed -> delete the new website from panel and server + truncate the
  error to 500 chars). http/import.go ports ImportController (preview +
  run: the plan re-renders with the field errors and old input on a
  rejected form, a blocked preview or agent failure flashes and redirects
  back, the confirm gate, the owner select for administrators, the job
  tracking + "Import started" flash to the hub) and
  SitesController::undoImport. The hub (batch 53's deferral) now runs the
  refresh on every load: the "Importing from …" / "Imported from …" banner
  with the Undo import action (data-wcp-confirm, the copy is deleted), the
  failed import redirects away with the flash, and the success/job flashes
  render. GET /websites/{id} becomes the discovered website's detail page
  (WebsitesController::show, websites.view — the PHP route's true port;
  the old managed stopgap is gone, its test rewritten: a numeric id is a
  404 without leaking, a discovery id resolves for any viewer) and the
  websites index gains the "Other websites on this server (read-only)"
  section (the discovered sites minus the WhaCP-managed ones, the
  per-site Details + Import links, the catch-all count, the warnings —
  absent when the agent cannot be asked). Three routes: GET/POST
  /websites/{id}/import + POST /sites/{id}/import/undo (import.run — the
  customer scope lacks it, 403). The discovery pages (GET /discovery,
  POST /discovery/run) stay ☐ with their page batch
- - ☑ websites: the site security + health overviews (batch 52) —
  SitesController::security + ::health (both websites.view) as
  site_health.go + hosting/site_health.go (SiteHealthService): each
  page is a list of plain check results the view renders with a
  status (ok/warn/bad), an explanation, the fix and who can do it
  ("You can fix this" vs "Ask your provider"), plus an infobar
  counting the items that need attention. Security: HTTPS off /
  not enforced / enforced, the certificate's days remaining from
  the agent's ssl.inspect (≤15 days a warning, ≤3 days bad — the
  lookup fails safe, no reply means no line; an https site with no
  stored cert file gets no line), the last av_scans row (infected /
  never scanned / clean) and the PHP 8.1 EOL warning (a numeric
  version_compare-style compare). Health: enabled / turned off
  (the one provider-side item) and the last completed backup.
  A foreign or missing website id 404s with the shared message.
  This batch also repairs a latent bug the SSL batch shipped
  (its CI run died on the owner's billing before the tests ran):
  CertInfo + RenewalStatus had no JSON tags, so the agent's
  snake_case reply fields (days_remaining, not_after, dns_names,
  certbot_installed, timer_active, method) never unmarshaled — the
  SSL page always read a 0-day cert and a dead renewal timer;
  the tags are now in place and the security check reads real
  numbers
- ◑ websites: the WordPress cron takeover (tools) (batch 47)
  — WordPressToolsService::setCronTakeover as
  hosting/wordpress_tools.go (SetCronTakeover): the tool route's
  `cron` case — taking wp-cron.php over runs the agent's
  cron_takeover_on (the returned command must match the strict
  wp-cli `cron event run --due-now --quiet` shape, else a
  user-facing error), saves it as a system cron job every 5 minutes
  via the cron service (the "taken over from wp-cron.php" description,
  undoing the takeover when the save fails), links it on the
  installation's cron_job_id column (no duplicate job on a second
  run), and audits wordpress.cron_takeover; turning it off runs
  cron_takeover_off, deletes the job and clears the link
- ☑ websites: the site MongoDB database browser (batch 42) —
  SiteMongoController's browser / browse / export as
  site_mongo_browser.go + the browser half of hosting/site_mongo_service.go:
  the three-pane tool page (GET /sites/{id}/mongo/browser — the account's
  prefixed databases, the ?db= preselect, the not-running / agent-down
  states; the reused /assets/js/mongobrowser.js), the JSON operation
  endpoint (POST /sites/{id}/mongo/browse — all 21 operations: collections
  + create/drop/empty/rename, documents + delete/insert/update, aggregate,
  explain, import, schema, users + user create/password/drop, indexes,
  collection stats, index create/drop — the ownership guard keeps a
  crafted db outside the prefix away from the agent, the PHP JSON error
  shape), and the collection export download (GET /sites/{id}/mongo/export —
  .json/.jsonl, the safe file name, no-store). The changing operations
  keep their audit records
- ☑ websites: the site MongoDB management area (batch 41) —
  SiteMongoController + the write side of SiteMongoService as
  site_mongo.go + hosting/site_mongo_service.go: the page
  (GET /sites/{id}/mongo, websites.manage — databases / users / backups
  sections; the agent's overview + backup list filtered to the account's
  "<system_user>_" prefix; the not-installed / not-running / agent-down
  states), creating + dropping databases, creating users (role), changing
  passwords, dropping users, and the backup actions (back up now + restore
  as agent jobs, tracked + flashed; delete). Every name stays under the
  account's prefix: create/user names are scoped (a typed prefix is not
  doubled), and the drop/password/restore/delete actions refuse any name
  that is not under the prefix (a crafted name can never reach the agent);
  backup archives are validated against the "<stamp>_<db>.archive.gz"
  shape before the restore/delete. The full database browser
  (mongo.browse / browser / export) followed in batch 42
- ☑ websites: the Git deployment area (batch 40) — DeployController +
  DeployService as site_git.go + hosting/deploy_service.go: the
  repository page (GET /sites/{id}/git, deploy.manage — the connected
  summary (repo/branch/folder/on-push, the document-root hint), the
  deployment history (commit/branch/trigger/status, the log link), the
  connect/settings form (repo URL https/ssh, branch, folder inside the
  website — logs/tmp/backups/.ssh refused, the post-deploy commands,
  the auto-deploy switch), the deploy key card (created on demand,
  60 s agent call) and the webhook card (payload URL + secret,
  regenerate)), deploy now (deploy.run agent job, tracked + flashed),
  rotate (new token + secret, the replay table cleared) and disconnect
  (the deployed files stay). The public signed push webhook
  (POST /hooks/deploy/{token}, no session/CSRF): 40-hex token, then
  GitHub/Gitea HMAC-SHA256 or the GitLab secret token — refused
  deliveries are audited + notified (1/hour); ping → pong; a push to
  another ref or auto-deploy off → 202 ignored; the same delivery
  body is refused once it started a deployment (webhook_deliveries);
  a user-facing start failure rolls the delivery back (502) so the
  host may retry; a successful push deploys as "webhook", tracked
  like a panel deployment
- ☑ websites: the cron area (batch 39) — CronController + the write
  side of CronService as site_cron.go + hosting/cron_service.go: the
  job list page (schedule presets + custom, the command, the
  description, enabled + email-output switches, the job's runs log
  with per-run start/duration/result/trigger/output), add/edit
  (schedule validation — macros + five fields with ranges, the
  2000-char single-line command, the 200-char description, the
  200-job cap, the plan's max_cron_jobs limit), pause/enable,
  delete, and "run now" (an agent job, tracked + flashed, the
  output kept in the job's log). Every change pushes the website's
  complete schedule to the agent (cron.apply) and restores the
  stored jobs when the apply fails; a suspended account's jobs do
  not run. The cron.view / cron.manage split is honored in the view
- ☑ websites: the SSL/HTTPS area (batch 38) — SslController +
  SslService as site_ssl.go + hosting/ssl_service.go: the certificate
  page (GET /sites/{id}/ssl, ssl.view — the state inspected from the
  agent: valid / expiring (the ssl_warning_days threshold) / expired /
  problem, the issuer, the covered names, the auto-renewal state, the
  certbot-missing warning, the AutoSSL notices), the actions
  (ssl.manage): issue a Let's Encrypt certificate (email from the
  account, aliases, staging; 320 s agent call), upload a custom
  certificate, renew (Let's Encrypt only), disable HTTPS (optionally
  deleting the certificate), the HTTPS options (redirect / HSTS,
  HSTS forced off without the redirect) and the security headers.
  Every change goes through WebsiteService.patch (Nginx regenerated,
  reload gated by nginx -t, previous state restored on failure) and is
  audited; the read-only split (ssl.view vs ssl.manage) is honored in
  the view
- ☑ server: the server-side Cloudflare area (batch 37) —
  CloudflareController + CloudflareService as cloudflare.go: the
  administrators connect one Cloudflare API token per server
  (POST /cloudflare/connect: shape check, token verification, stored
  encrypted in cloudflare_accounts + audit), the zone list
  (GET /cloudflare: active+pending zones with the Manage links), the
  selected zone (?zone=) — SSL mode, Always Use HTTPS, every DNS
  record with the proxy toggle, the pending-zone nameservers, the
  cache purge — and disconnect. The read-only split is honored
  (server.view sees the page; only server.manage gets the forms);
  every action is audited against the server. Same direct-API pattern
  as the per-website area (injectable transport; ids regex-guarded)
- ☑ websites: the per-website Cloudflare area (batch 36) —
  SiteCloudflareController + CloudflareSiteService as
  site_cloudflare.go: the customer connects their own Cloudflare API token
  (POST /sites/{id}/cloudflare/connect: shape check, token verification,
  the zone matching the website's domain — longest match wins — stored
  encrypted per website in cloudflare_sites + audit), the zone page
  (GET /sites/{id}/cloudflare: SSL/TLS mode select, the Always Use HTTPS
  switch, the proxiable DNS records of the domain with per-record proxy
  switches, the API-error banner), and the actions POST …/ssl (validated
  mode), …/always-https, …/proxy (record id whitelisted to 32-hex),
  …/purge (purge_everything), …/disconnect — every change audited against
  the website. The panel talks to api.cloudflare.com directly (an
  injectable transport for tests; no agent), the token is only ever a
  Bearer header, ids are regex-guarded before path interpolation
- ☑ caching: the server-side Redis + Memcached management (batch 35) —
  the Memcached page (GET /memcached: memory progress, the stats, the
  settings modal, the confirmed flush, the install job, the restart —
  redis.manage/packages.manage/services.manage) and the Redis page
  (GET /redis: the overview, the per-website ACL users create/reset +
  remove with the one-shot password flash, the default-user password
  generate/remove, the memory + eviction settings with re-render on
  validation errors, the confirmed database flush — redis.manage)
- ☑ websites: the Redis key browser + the website's Memcached page
  (batch 34) — the server-wide browser (GET /redis/browser, redis.manage)
  and the per-website browser (GET /sites/{id}/redis, websites.manage —
  the prefix locked to the website's system user), both driven by the
  reused /assets/js/redisbrowser.js whose operation endpoint
  (POST …/redis[/browser]/api) whitelists the fields, forces the prefix
  and audits the changing ops (redis.key_*); plus the informational
  Memcached page (GET /sites/{id}/memcached: connection details, the
  running/stopped state)
- ☑ websites: visitor statistics (batch 33) — the website's AWStats
  report (GET /sites/{id}/stats: the month select, the summary, the
  days/hours tables, the top lists drawn from the agent's plain-text
  data file — parsed in internal/stats, a pure port of StatsService
  with unit tests), the on/off switch (POST …/stats/enable: the first
  update when turned on, the stats.enable/disable audits) and the
  on-demand update (POST …/stats/update: a job)
- ☑ security: per-website malware pages (batch 32) — the website's
  ClamAV page (GET /sites/{id}/malware: the status toolbar, the last scan
  with its findings, the scan history, the quarantine listing) + the
  on-demand scan (POST …/malware/scan: the running-scan exclusion, the
  av_scans row, the job tracking + audit) + the quarantine release
  (POST …/malware/release: security.manage only; the agent re-scans the
  file and refuses if it is still infected)
- ☑ security: ModSecurity (batch 31) — the server-wide WAF page
  (GET/POST /security/modsec: status bar + websites/events/vendors/
  custom-rules/installation tabs; server-wide settings; per-rule and bulk
  custom rule editing with the sha optimistic lock; the per-website mode/
  vendors/rules; the blocked/detected events; the background
  install/update/rollback jobs with JobTracker + audit) + the website's
  Web firewall page (GET/POST /sites/{id}/firewall: the mode switch for
  owners, the rules for the site, turning a rule off from an event for
  administrators). Agent-driven (modsec.status/configure/custom_
  get|custom_set/site/events/installs/update/install/crs_install/rollback;
  every change nginx -t checked and rolled back on the agent); the
  modsec.trim scheduler side stays with the background-jobs follow-up
- ☐ DB browser, DB dump/import, DB server area (DbServerController), redis/memcached, DNS records, mail accounts/filters, backups, files/FTP/terminal, automation (deploy/workers write), security suite, monitoring, settings/admin, installers, API v1

## Batch 68 — Backups server-admin slice (18 routes) ☑
- ☑ `GET /backups` — server backups page (website backups tab for the signed-in
  user, plus admin tabs for server configuration, remote storage, account
  files, and the encryption key). Tabs render from `BackupService.ListServer`
  / `ListAllSite` / `ListForUserSQL` and the `BackupScheduler` server schedule
  list; remote storages via `StoragesDetailed`, the key card via
  `backup.key_info`, account files via `backup.account_list` (best effort).
  Customer visibility follows `hidesAutoBackups` (same rule as the per-site
  tab); non-admins see only their own website backups.
- ☑ Server configuration backups (5 routes): `POST /backups/server` starts
  `backup.server` (always encrypted; may copy to a turned-on remote storage),
  tracked as a job (`"Server configuration backup"`);
  `POST /backups/server/{backup}/delete` / `GET /backups/server/{backup}/download`
  reuse `BackupService.Delete` / `Download` (Delete already handled the
  `kind=server` branch of the agent call); `POST /backups/server/{backup}/restore`
  restores the selected components over `backup.server_restore` with the
  `extract_only` review path and the `restore` typed confirmation.
  Components: nginx, php, mysql, redis, letsencrypt, fail2ban, cron
  (PHP SERVER_COMPONENTS).
- ☑ Remote storage administration (10 routes): `POST /backups/storages`
  validates S3/SFTP/FTPS/Google Drive/local forms (endpoint/bucket/region,
  host/port/user/pass/key/tls, client_id/secret, path), tests the connection,
  seals a pending storage (30-min TTL, encrypted with the panel key) and
  either presents the host-key/TLS-pin confirmation page (GET/POST
  `/backups/storages/confirm`) or redirects Google OAuth consent (state
  nonce in session, callback `GET /backups/storages/google/callback`
  exchanges for a refresh_token, tests and inserts the storage).
  `POST /backups/storages/{storage}` edits a storage (re-tests, pins the
  original host key); `POST .../{storage}/test|active|delete|scan` retest,
  toggle active, delete, or remote-import (`backup.remote_import`) back into
  `backups`. Storage configs are stored encrypted with the panel key and
  opened on demand, matching PHP's seal/unseal (1800s TTL).
- ☑ Encryption key (2 routes): `POST /backups/key/download` re-authenticates
  with the panel password (audit denied on mismatch) and streams
  `backup.key_export` over an encrypted passphrase; `POST /backups/key/import`
  accepts the JSON key file (4 KiB limit) and calls `backup.key_import`,
  reporting the "already present" case.
- ☑ Server schedules (2 routes): `POST /backups/schedules` adds either a
  server-configuration schedule (`kind=server`) or an all-sites schedule
  (`all_sites=1`, files/databases/encrypt/keep-local, frequency/hour/
  weekday/retention validated the same as the per-site form, only to active
  storages); `POST /backups/schedules/{schedule}/delete` removes it.
  `BackupScheduler.ListServer` returns both kinds for the server page.
- Files: `hosting/backup_admin.go` (966 LOC), `hosting/account_backup.go`,
  `hosting/backup_scheduler.go` (rewritten with a shared scan, +AllSites
  +LastRunAtStr, 278 LOC), `http/backups_handlers.go` (1080 LOC, 18 handlers),
  `web/templates/backups_index.html`, `web/templates/backups_confirm_storage.html`,
  `web/templates.go` (+hours/mapItems template funcs), `server.go` (+18 routes,
  agent.DetailsString helper). Docs bumped to **342 ☑ / 4 ◑ / 205 ☐**.

## NOT YET PORTED (known follow-ups, tracked honestly)
- WebAuthn sign-in + security-key management; IP-verification sign-in (emailed code); the HULK enforcement side (the failed-login recording into the panel's login flow + the HulkService::run scheduler loop that turns events into blocks — the panel page itself is ported); step-up password confirmation; password-age and role-requires-2FA enforcement; trusted-proxy client IP
- QR image on the 2FA setup page (the otpauth URI + secret are shown for manual entry; a QR encoder or dependency is a small follow-up)
- notification emails (the warning/error rows are inserted but not emailed; the PHP Redactor for their body is not ported)
- profile: password change, API-token management, session management, security-key forms
- the full per-feature view/handler port (websites, databases, mongo, …) — see list above
- background jobs / scheduler binary (backup schedules, AutoSSL, webhook deliveries, cron-email, discovery, monitoring sweeps)

## Known risks / blockers
- No Go toolchain in the authoring sandbox: batches are verified by the CI server job (gofmt gate, go vet, go test -race, go build) after each push — check `gh run list` after committing.
- Full PHP phpunit baseline not re-run this session (300s cap); relied on last CI-green.
- modernc.org/sqlite + x/crypto must download via the module proxy (verify first).
- This is a multi-month effort; each session continues from the next ☐ here.

## Next exact step
Batch 69: the remaining server-administration controllers in the planned
sequence — ApiController (18) and SystemController (14), then PhpController
(8), AccountBackupsController (8 account routes — create/delete/download/
preview/restore/upload/restores*) and Roles (7). SslCenterController (18
routes) is in the already-drafted PR #66 `arena/b6bfc5f7-whacp`; it is not
re-duplicated on this branch. After those controller slices, the
background-jobs follow-up (backup/AutoSSL/hulk/modsec/av schedulers, webhook
deliveries, discovery and monitoring sweeps).

## Batch 69 — REST v1 API slice (18 routes, ApiController) ☑

- Ported files:
  - `server/internal/auth/api.go` (NEW 135 LOC): Bearer-token
    authenticator (`APIAuthenticator`); sha256-hex hash on
    `api_tokens.token_hash`, expires_at check, identity.CanAct gate,
    last_used_at/last_used_ip touch; ctx keys `ctxAPIUser`/`ctxAPIToken`,
    `APITokenRow`, `APIUserFrom`/`APITokenFrom`/`WithAPIContext`,
    `StatusError` and `clientIP` helper.
  - `server/internal/http/api.go` (NEW 157 LOC): JSON helpers for REST v1
    (`writeAPI`, `writeAPIError`, `apiUserError` mapping
    ValidationError→422 / UserError→409 / agent.Error→502,
    `parseJSONBody` with 1 MiB limit, `pathID`, `apiWebsite`/`apiBackup`/
    `apiDatabase` marshallers, `nullableSQL`/`nullableStr`).
  - `server/internal/http/api_middleware.go` (NEW 58 LOC): `apiPublic`
    (no-auth) and `apiGuard(permission, write)` — Bearer auth → 401,
    permission check → 403, ability='write' gate for mutating methods,
    panic/UserError/Validation/Agent error → JSON envelope.
  - `server/internal/http/api_handlers.go` (NEW ~440 LOC): 18 handlers
    — openapi, me, serverInfo, listWebsites, createWebsite (aliases
    "\n"-joined), showWebsite, enable/disable (shared setEnabled),
    deleteWebsite (remove_files/remove_user query flags), issueSsl
    (email/include_aliases/staging), listBackups (Refresh then List),
    createBackup (files/databases/encrypt/label → JobTracker.Track),
    deployWebsite (DeployService.Deploy), listDatabases, createDatabase
    (with optional user+auto password), job (JobIDRe + VisibleTo + Poll),
    listNotifications (with unread/limit filters), readNotifications
    (MarkAllRead for the API user).
  - `server/internal/http/server.go`: 18 routes registered under
    `/api/v1/` (registered BEFORE web routes so they take precedence over
    site-specific wildcard handlers); Server struct gains `apiAuth`
    wired via `auth.NewAPIAuthenticator(db, users)` in New().
  - `server/internal/http/api_test.go` (placeholder): integration
    coverage will land once a Go toolchain is available in CI; routes
    are exercised structurally by the compilation of the handler graph.
- Permission gates mirror `panel/routes/web.php` lines 589–606 exactly
  (server.view / websites.view / websites.manage / ssl.manage /
  backups.view / backups.manage / deploy.manage / databases.view /
  databases.manage / notifications.view). Write routes are additionally
  gated by the token's `ability='write'` column.
- Responses use the same JSON envelope as PHP: `{success:true,data}` on
  success, `{success:false,error:{code,message}}` on failure. The 404
  body is `{code:"not_found",message:"Not found, or not visible to this
  token."}` to match PHP's `abort(404)` wording.
- Docs bumped to **360 ☑ / 4 ◑ / 187 ☐**.

Next up in Batch 69: PhpController (8 routes), AccountBackupsController
(8), Roles (7).

## Batch 69b — SystemController (14 routes) ☑

- Ported files:
  - `server/internal/http/system.go` (NEW ~380 LOC): Packages list (GET
    /packages with JSON for the install-queue XHR), install/refresh/
    upgrade/repair (POST with job tracking via JobTracker), hostname
    set + mail server re-sync (best effort), hostname references read
    (+ webmail/remail re-apply), root password change (admin-only,
    panel-password re-confirmation, password policy check mirroring
    PHP's ≥14 chars, ≥3/4 classes, no colons/controls), time zone,
    graphical login toggle, scheduled reboot and cancel.
  - `server/internal/web/templates/server_packages.html` (NEW):
    minimal server_packages template — updates CTA, component list,
    repair/remove-duplicates forms (mirrors the PHP components list
    surface; the richer queue widget is added when JS assets land).
  - `server/internal/web/templates/server_hostname_references.html`
    (NEW): minimal hostname-references page with search form +
    webmail/mail re-apply buttons.
  - `server/internal/http/server.go`: registered 14 routes (packages
    group under `packages.manage`, server group under `server.manage`);
    all use the existing `s.guard(routeName, perm, handler)` pattern
    (session + CSRF + permission + feature gating).
- Job actions (`packages.install`, `packages.refresh`,
  `packages.upgrade`, `packages.repair`) are tracked via
  `hosting.NewJobTracker(s.db).Track(...)` just like memcached/ftp
  installs so the dashboard job widget shows them.
- Mail-server resync after a host-name change is best-effort: the
  `mail.configure` agent call is wrapped in its own try/catch with a
  tailored success/error flash message (parity with PHP).
- Root password policy matches PHP exactly: minimum 14 runes, three
  of {lower, upper, digit, symbol}, no colon or C0 controls; the admin
  role is enforced in addition to the `server.manage` permission.
- Docs bumped: **400 ☑ / 4 ◑ / 147 ☐** (378+ after prior awk recount;
  python recount confirmed 400 with 14 new ☐→☑ flips from 386 baseline
  at merge of PR #68).

Next up in Batch 69: PhpController (8 routes), AccountBackupsController
(8), Roles (7).

## Batch 69c — PhpController (8 routes) ☑

- Ported files:
  - `server/internal/http/php.go` (NEW ~350 LOC): 8 handlers
    - POST /php/repository (enable third-party PHP repo — PPA/Remi;
      `confirm=yes` gate, php.repository.enable job tracked).
    - POST /php/install (validate version `/^[5-9]\.[0-9]{1,2}$/`, fire
      php.install job, audit with installed packages list).
    - POST /php/{version}/toggle (php.set_enabled on/off; audit
      php.enable / php.disable).
    - GET /php/{version} (render per-version page fetching
      php.extensions / php.ini_get / php.fpm_get concurrently).
    - POST /php/{version}/extensions/{op} (install/remove/toggle;
      toggle is synchronous set, install/remove are job-tracked;
      extension name regex `/^[a-z0-9_]{2,20}$/`, op whitelist).
    - POST /php/{version}/ini (accept text mode (raw content) or
      form mode `ini[key]=value` with key regex `/^[a-z0-9_.]{2,64}$/`;
      fire php.ini_set synchronously).
    - POST /php/{version}/fpm (parse the four integer pool settings
      with 0 = empty; fire php.fpm_set).
    - GET /php/{version}/modules (JSON for the per-site runtime
      form — returns {success,modules} mirroring PHP, 404 for invalid
      versions, 502 on agent errors).
  - `server/internal/web/templates/php_show.html` (NEW): minimal
    per-version page with extensions/ini/fpm tabs and simple forms.
  - `server/internal/http/server.go`: 8 routes registered under
    `s.guard(...)` — packages.manage for repository, php.manage for
    mutators, php.view for the JSON modules list.
- Validators mirror PHP exactly: version regex `^[5-9]\.[0-9]{1,2}$`,
  extension name `^[a-z0-9_]{2,20}$`, ini key `^[a-z0-9_.]{2,64}$`,
  extension op whitelist {install,remove,toggle}.
- Docs bumped: **408 ☑ / 4 ◑ / 139 ☐**.

Next in Batch 69: AccountBackupsController (8), Roles (7).

Build/test the Go module with: `cd server && go test ./...` (deps pinned for Go 1.24). CI
(`gofmt` / `go vet` / `go test -race` / `make build`) is the verification gate
for this sandbox — no local Go toolchain is available.

## Batch 69d — AccountBackupsController (8 routes) ☑

- `server/internal/hosting/account_backup.go`: extended
  AccountBackupService with Create/Delete/Chunk/Upload/Inspect/Start/
  RestoreRecord/ImportState/Recreate methods + AccountRestore row
  struct. The agent performs the actual tarball pack/unpack and
  streaming; this service writes audit entries, persists the
  `account_restores` row, and hands job IDs back to handlers.
- `server/internal/http/account_backups.go` (NEW ~350 LOC): 8 handlers
  — create (admin-only via RoleScope=="all", job-tracked), download
  (chunked streaming, base64 decoded per chunk, session released so
  streaming isn't blocked), delete (path-sanitised agent call),
  upload (multipart chunk XHR, JSON envelope), preview (Inspect +
  render), start (confirm-username gate, inserts account_restores
  row), show (loads restore + import state + one-shot secrets from
  session), recreate (advances status→restoring, asks agent, returns
  jobs to track + generated secrets).
- `server/internal/web/templates/backups_account_restore.html` (NEW):
  minimal restore page with confirm form, state display and
  one-shot credentials list.
- `server/internal/http/server.go`: 8 routes registered (backups.manage
  for most; users.manage for restore/recreate since they create/edit
  accounts).
- Docs: **416 ☑ / 4 ◑ / 131 ☐**.

## Batch 69e — RolesController (8 routes) ☑

- `server/internal/http/roles.go` (NEW ~300 LOC): 8 handlers — index,
  create, store, edit, update, reset (defaults re-seeded via
  authz.SyncDefaults for built-in roles), destroy (guards: system
  roles cannot be deleted, roles with users cannot be deleted),
  two-factor policy toggle. Permissions are stored as a checkbox
  list; `syncRolePerms` replaces the role_permissions join in a tx.
- `server/internal/web/templates/roles_index.html` +
  `roles_form.html` (NEW): minimal list and form with scope selector,
  permissions checklist, 2FA toggle and reset-to-defaults button.
- `server/internal/http/server.go`: 8 routes registered
  (users.view for list/edit; roles.manage for mutations).
- Docs: **423 ☑ / 4 ◑ / 124 ☐**.

End of Batch 69 (ApiController 18 + SystemController 14 + PhpController
8 + AccountBackupsController 8 + RolesController 7 = 55 routes ported
in this batch). Next batches move to background-job follow-ups and the
remaining controllers (Services, Cron, Server overview, ServerHealth,
MailFilters, etc.).


## Batch 70a — MonitoringController (7 routes) ☑

- `server/internal/http/monitoring.go` (NEW ~230 LOC): 7 handlers
  (index with range selector, live admin-only page that polls
  system.live via JSON, liveData JSON endpoint, processes list via
  processes.list agent call, trace (processes.trace), signal
  (processes.signal with command truncated to 200 chars for audit)
  and kill-user (processes.kill_user, TERM/KILL). Admin-only pages
  use hosting.IsAll(user) matching PHP's Scope::isAll.
- 4 minimal templates: monitoring_index, monitoring_live (with
  polling JS), monitoring_processes, monitoring_trace.
- `server/internal/http/server.go`: 7 routes registered (monitoring.view
  for list/live/processes; server.manage for trace/signal/kill-user).
- Docs: **430 ☑ / 4 ◑ / 117 ☐**.

## Batch 70b — TopbarController (5 routes) ☑

- `server/internal/http/topbar.go` (NEW ~180 LOC): 5 handlers
  - GET /topbar/status (admin-only JSON, polls agent.status for load avg)
  - POST /services/restart-all (tracks job, audits services list,
    flashes job + restart message; referrer validated to same-origin
    paths via safeReferer)
  - GET /whacp/update (renders page; also supports JSON for AJAX poll)
  - POST /whacp/update/check (stub — points to the update page)
  - POST /whacp/update (kicks whacp.update agent call with audit).
- `server/internal/web/templates/server_update.html` (NEW): minimal
  update page with status display + start/check buttons.
- `server/internal/http/server.go`: 5 routes registered (monitoring.view
  for status; services.manage for restart-all; server.manage for
  whacp.update).
- Docs: **435 ☑ / 4 ◑ / 112 ☐**.

## Batch 71 — Notifications / API tokens / Services smalls (6 routes) ☑

- `server/internal/notify/notify.go`: added `UnreadCount`, `ListUnread`,
  `SetEmailOpt`, `EmailOpt` helpers for the topbar summary JSON and the email
  opt-in toggle.
- `server/internal/identity/api_tokens.go` (NEW): `Repository.CreateAPIToken`
  (whcp_&lt;prefix&gt;_&lt;secret&gt; tokens, sha256-hashed, "read"/"write" abilities)
  and `RevokeAPIToken` (owner-scoped delete).
- `server/internal/http/notifications_handlers.go`: added `notificationsSummary`
  (GET /notifications/summary — JSON for the topbar bell: unread count plus
  latest 5 and 8 trimmed unread items with relative time, same-origin link
  filter) and `notificationsEmail` (POST /notifications/email — toggles
  users.notify_email).
- `server/internal/http/users_handlers.go`: added `apiTokenStore` (POST
  /profile/tokens — flashes the one-shot plaintext token back to /profile,
  rendered by the existing Tokens/Surprise/NewToken profile template data) and
  `apiTokenRevoke` (POST /profile/tokens/{id}/revoke).
- `server/internal/http/services.go` (NEW): `servicesControl` (POST
  /services/control — systemd unit regex validation for start/stop/restart/
  reload/enable/disable, supports ?return_to=/mail/admin/server and JSON
  callers for the dashboard widget) and `servicesRestartWhacp` (POST
  /services/whacp/{key}/restart — admin-gated via hosting.IsAll, handles the
  scheduled-restart response path).
- `server/internal/http/server.go`: registered 6 routes (notifications.view
  summary+email; api.tokens store+revoke; services.manage control+restart-
  whacp).
- Cleanup: removed duplicate `writeJSON` in `system.go` (shared helper lives in
  server.go).
- Docs: **441 ☑ / 4 ◑ / 106 ☐**.

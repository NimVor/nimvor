# WhaCP PHP → Go Migration — Status (machine-readable-ish)

Baseline commit: 46313561 (branch claude/sleepy-ptolemy-bdv6ib)
PHP app: 230 files / 38,926 lines · 84 controllers · 76 services · 8 repos
Views: 179 · Tests: 119 files / 15,461 lines · Routes: 551 · Migrations: 57
Agent: Go, unchanged, full suite GREEN at baseline.

Route porting count (see ROUTE-MATRIX.md, verified against
`server/internal/http/server.go`): **68 ☑ done+tested, 3 ◑ partial, 480 ☐** of 551.

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
  pages carry the "Job <id> started" flash alert meanwhile)
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
Batch: database browser (DbBrowserController) — GET `/databases/{id}/browse`
+ POST `/databases/{id}/browse/api` (databases.browser, databases.manage):
the table list / column browse / run-SQL page per database, plus the server
browser variant GET `/databases/browser` (databases.browser.server,
staff) that the unmanaged section's Browse links point at. The agent's
`db.browse*` actions are already implemented (agent/internal/dbmgr); check
the exact action names + the PHP DbBrowserController for the request
contract before porting.

Build/test the Go module with: `cd server && go test ./...` (deps pinned for Go 1.24).

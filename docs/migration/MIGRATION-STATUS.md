# WhaCP PHP → Go Migration — Status (machine-readable-ish)

Baseline commit: 46313561 (branch claude/sleepy-ptolemy-bdv6ib)
PHP app: 230 files / 38,926 lines · 84 controllers · 76 services · 8 repos
Views: 179 · Tests: 119 files / 15,461 lines · Routes: 551 · Migrations: 57
Agent: Go, unchanged, full suite GREEN at baseline.

Route porting count (see ROUTE-MATRIX.md, verified against
`server/internal/http/server.go`): **25 ☑ done+tested, 3 ◑ partial, 523 ☐** of 551.

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
- GET `/websites`, GET `/websites/{id}` (tenant-scoped; foreign id → 404)
- GET `/sites/{id}/cron` · GET `/sites/{id}/workers` · GET `/sites/{id}/subdomains` (feature-gated)
- GET/POST `/databases` + POST `/databases/{id}/drop` (plan-limit enforced before
  the agent call; confirm-word + CSRF negative tests)
- GET `/dns` (zone list, tenant-scoped) · GET `/mail` (domains, tenant-scoped)
- GET `/audit` (staff) · GET `/users` (reseller-scoped) · GET `/roles` · GET `/plans` (reseller-own) · GET `/servers` (staff)
- GET+POST /notifications (read-marking scoped) · GET `/profile` (◑: 2FA + API
  tokens done; password change, token management, sessions, security-key
  management forms are a later batch)

## Phase 2+ — Feature areas (one batch each; see ROUTE-MATRIX "Ported" column)
Order by risk/leverage: identity+RBAC+feature-gating → dashboard → websites →
databases(MySQL) → mongo → redis/memcached → files → dns → ssl → mail → backups
→ cron/workers → monitoring/security → settings/admin → installers → API.
- ☑ identity completion (TOTP 2FA) — done this session
- ◐ password reset by email (forgot/reset flows + SMTP mailer + settings repo) — next batch
- ☐ WebAuthn security keys (registration + login assertion) — 2FA users with both
  factors can use TOTP; key-only users are refused (safe) until this lands
- ☐ websites write path (create/enable/disable/delete, settings, subdomains/addons write, PHP/runtime profiles)
- ☐ database users, DB browser, mongo, redis/memcached, DNS records, mail accounts/filters, SSL/ACME, backups, files/FTP/terminal, automation (cron/deploy/workers write), security suite, monitoring, settings/admin, installers, API v1

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
Batch: password reset by email — internal/settings (settings repo over the
`settings` table), internal/mail (SMTP via net/smtp with STARTTLS, parity with
app/Mail/Mailer), Security\PasswordReset port (token hashing, 30-min TTL,
per-user/per-IP limits, same-answer-for-unknown-accounts), routes
GET+POST /password/forgot, GET+POST /password/reset/{token}, minimal-layout
views, negative tests (expired/used token, throttling, wrong confirmation).
Then: websites write path.

Build/test the Go module with: `cd server && go test ./...` (deps pinned for Go 1.24).

# NimVor PHP → Go Migration — Status (machine-readable-ish)

Baseline commit: 46313561 (branch claude/sleepy-ptolemy-bdv6ib)
PHP app: 230 files / 38,926 lines · 84 controllers · 76 services · 8 repos
Views: 179 · Tests: 119 files / 15,461 lines · Routes: 551 · Migrations: 57
Agent: Go, unchanged, full suite GREEN at baseline.

## Legend
☐ not started · ◐ in progress · ☑ done+tested · ⊘ retired (documented)

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
- ☑ internal/auth: login throttle + login_attempts store (verbatim from LoginThrottle.php/LoginAttemptRepository.php); AuthService password path — `Attempt` (throttle, dummy-hash timing, suspended/inactive handling, opportunistic rehash), `User` (idle+absolute timeouts, password-hash+session-epoch fingerprint invalidation), `Logout`. A user with a second factor configured is REFUSED a password-only sign-in (never bypassed)
- ☑ internal/web: html/template engine with embedded, CSP-safe login + dashboard templates (no inline script/style; reuse /assets)
- ☑ internal/http: per-request session middleware (load→resolve user→save cookie before write), `requireAuth`, and the login/logout/dashboard handlers with CSRF verification (`_token`/X-CSRF-Token) and token rotation on sign-in
- ☑ VERIFIED: `go test ./...` green; live binary signs in end-to-end against a real SQLite DB using a PHP-generated argon2id hash (GET /login→CSRF, POST→303 /, dashboard shows the user), and rejects bad CSRF / wrong password / anonymous dashboard access; logout clears the session
- ☑ internal/authz: role/permission bootstrap seeding (`SyncDefaults`) — the 46-permission catalogue + 5 system roles ported from Security\Permissions, byte-identical to the PHP definitions (diffed), idempotent, keeps plugin.* perms and administrator-customised roles; wired into cmd/nimvor startup after migrate (verified seeding a fresh DB)
- ☐ NOT YET PORTED (known follow-ups, tracked honestly): the 2FA/WebAuthn/IP-verification/HULK sign-in flows (currently refused, not bypassed); step-up password confirmation; password-age and role-requires-2FA enforcement; trusted-proxy client IP; the permission+feature route middleware wired across ported routes; and the full per-feature view/handler port (websites, databases, mongo, …)
- ◐ static asset serving ☑ (reused assets dir) · html/template layout pending
- ◐ stdlib mux + request logging ☑ · auth/permission/feature middleware pending
- ◐ /healthz ☑ (parity: {"status":"ok"}/503) · login/logout/dashboard pending
- ☑ cmd/nimvor (serves, graceful shutdown, migrate-on-start) + cmd/nimvor-migrate (status/up) — binaries build and run; verified /healthz and a 57-migration fresh DB

## Phase 2+ — Feature areas (one batch each; see ROUTE-MATRIX "Ported" column)
Order by risk/leverage: identity+RBAC+feature-gating → dashboard → websites →
databases(MySQL) → mongo → redis/memcached → files → dns → ssl → mail → backups
→ cron/workers → monitoring/security → settings/admin → installers → API.

## Known risks / blockers
- Full PHP phpunit baseline not re-run this session (300s cap); relied on last CI-green.
- modernc.org/sqlite + x/crypto must download via the module proxy (verify first).
- This is a multi-month effort; each session continues from the next ☐ here.

## Next exact step
Implement internal/config + internal/crypto (nacl secretbox reading /etc/nimvor/panel.key),
then internal/session + CSRF + security-headers middleware, then internal/views
(html/template + embed of the existing login layout) and the login/logout/dashboard
handlers with httptest tests (valid/invalid/CSRF/locked).

Build/test the Go module with: `cd server && go test ./...` (deps pinned for Go 1.24).

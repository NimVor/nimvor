# WhaCP PHP → Go Migration — Status (machine-readable-ish)

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
- ☐ NOT YET PORTED (known follow-ups, tracked honestly): role/permission bootstrap seeding (PHP RoleRepository::ensure*; migrations do not seed these — the Go panel will need an equivalent on a fresh DB); AuthService state machine (attempt/user()/timeouts/fingerprint), login throttle, 2FA/WebAuthn/IP-verification/HULK brute force, auth+permission+feature HTTP middleware, login/logout/dashboard handlers + html/template views
- ◐ static asset serving ☑ (reused assets dir) · html/template layout pending
- ◐ stdlib mux + request logging ☑ · auth/permission/feature middleware pending
- ◐ /healthz ☑ (parity: {"status":"ok"}/503) · login/logout/dashboard pending
- ☑ cmd/whacp (serves, graceful shutdown, migrate-on-start) + cmd/whacp-migrate (status/up) — binaries build and run; verified /healthz and a 57-migration fresh DB

## Phase 2+ — Feature areas (one batch each; see ROUTE-MATRIX "Ported" column)
Order by risk/leverage: identity+RBAC+feature-gating → dashboard → websites →
databases(MySQL) → mongo → redis/memcached → files → dns → ssl → mail → backups
→ cron/workers → monitoring/security → settings/admin → installers → API.

## Known risks / blockers
- Full PHP phpunit baseline not re-run this session (300s cap); relied on last CI-green.
- modernc.org/sqlite + x/crypto must download via the module proxy (verify first).
- This is a multi-month effort; each session continues from the next ☐ here.

## Next exact step
Implement internal/config + internal/crypto (nacl secretbox reading /etc/whacp/panel.key),
then internal/session + CSRF + security-headers middleware, then internal/views
(html/template + embed of the existing login layout) and the login/logout/dashboard
handlers with httptest tests (valid/invalid/CSRF/locked).

Build/test the Go module with: `cd server && go test ./...` (deps pinned for Go 1.24).

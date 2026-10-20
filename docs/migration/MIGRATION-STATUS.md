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
- ☐ server/ module, config, logging (slog)
- ☐ database: open existing sqlite (same PRAGMAs), migrator (name-compatible, embedded SQL)
- ☐ identity: argon2id + bcrypt verify (PHP cross-compat test)
- ☐ agent: unix-socket JSON client
- ☐ session (secretbox cookie) + CSRF + security headers
- ☐ html/template engine + base layout + asset embedding
- ☐ stdlib router covering {param} routes; auth/permission/feature middleware
- ☐ handlers: /healthz, login, logout, dashboard
- ☐ cmd/whacp, cmd/whacp-migrate

## Phase 2+ — Feature areas (one batch each; see ROUTE-MATRIX "Ported" column)
Order by risk/leverage: identity+RBAC+feature-gating → dashboard → websites →
databases(MySQL) → mongo → redis/memcached → files → dns → ssl → mail → backups
→ cron/workers → monitoring/security → settings/admin → installers → API.

## Known risks / blockers
- Full PHP phpunit baseline not re-run this session (300s cap); relied on last CI-green.
- modernc.org/sqlite + x/crypto must download via the module proxy (verify first).
- This is a multi-month effort; each session continues from the next ☐ here.

## Next exact step
Create `server/go.mod`, verify `modernc.org/sqlite` + `golang.org/x/crypto`
download, then implement internal/database (open + migrator) with tests.

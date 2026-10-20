# WhaCP PHP → Go — Target Architecture & Compatibility Decisions

The Go control panel replaces `panel/` (PHP). It keeps the **existing Go agent**
(`agent/`, its own module, unix-socket protocol), the **existing SQLite DB and
its 57 migrations**, the **Bootstrap 5 frontend + the 12 JS files unchanged**,
and the **authorization / tenant-isolation / feature-gating / audit** model.

## Module layout
New Go module `github.com/mchkhawarshahzad/whacp/server` under `server/`.
It does NOT import the agent's `internal/` packages (unimportable); it speaks the
agent's wire protocol through its own small client.

```
server/
  cmd/whacp/            main HTTP server
  cmd/whacp-migrate/    run/inspect DB migrations
  internal/config/      paths, env, secrets locations
  internal/database/    sqlite open (same PRAGMAs) + migrator (name-compatible)
  internal/identity/    password verify (argon2id + bcrypt), users
  internal/authorization/ RBAC, feature gating, tenant scope
  internal/session/     secretbox cookie sessions (+ session_epoch)
  internal/agent/       unix-socket JSON client (POST /v1/actions/<name>)
  internal/http/        server, router (stdlib net/http 1.22 patterns)
  internal/http/middleware/ auth, csrf, security headers, rate limit, audit
  internal/http/handlers/ one file per feature area
  internal/services/    business rules (ports of app/Services)
  internal/repositories/ SQL (ports of app/Repositories)
  internal/views/       html/template + embed; ports of resources/views
  internal/audit/       audit logging
  web/static/           symlink/copy of panel public assets (bootstrap, JS, css)
```

## Hard compatibility decisions (verified against the code)
1. **SQLite driver:** `modernc.org/sqlite` (pure Go) — no cgo, single static
   binary, easy cross-compile. Validated against schema/transactions in tests.
   Open with the SAME pragmas the PHP `Connection` sets: `foreign_keys=ON`,
   `busy_timeout=5000`, `journal_mode=WAL`, `synchronous=NORMAL`.
2. **Migrations:** the PHP `Migrator` records applied migrations by **name**
   (basename, e.g. `0057_mongo_remotes`) in a `migrations(name, applied_at)`
   table. The Go migrator uses the SAME table and names. The 57 PHP migrations'
   SQL is extracted into identically-named embedded `.sql` files, so:
   - **Upgrade** (existing DB): all 57 names already present → skipped; only new
     Go migrations (0058+) run.
   - **Fresh install:** all 57 run in order → identical schema.
   A single-writer advisory lock prevents concurrent migrators.
3. **Passwords:** PHP `password_hash` → **argon2id** PHC string
   (`$argon2id$v=19$m=65536,t=4,p=1$salt$hash`), bcrypt fallback. Go verifies the
   exact PHC format with `golang.org/x/crypto/argon2` / `bcrypt`. No re-hashing
   of existing users; new hashes match PHP defaults. Cross-compat proven by a
   test that verifies a hash produced by PHP `password_hash`.
4. **Sessions:** PHP used native file sessions (not DB). Go uses authenticated-
   encryption cookies via `nacl/secretbox` keyed from the existing
   `/etc/whacp/panel.key` (libsodium secretbox == nacl secretbox, wire
   compatible). `session_epoch` on the user still invalidates all sessions.
   Cutover invalidates in-flight PHP sessions (users re-login) — documented.
5. **CSRF:** per-session token in the session cookie, required on state-changing
   browser requests except routes flagged `csrf=false` (e.g. HMAC webhooks).
6. **Agent boundary:** unchanged. Go client dials the unix socket, sends
   `Authorization: Bearer <token-file>`, `POST /v1/actions/<name>` + JSON, parses
   `{success,data,error{code,message,details},request_id}`. The panel never runs
   privileged commands itself; authorization is enforced in Go BEFORE every
   agent call (an authenticated agent request does not imply the customer is
   authorized).
7. **Router:** stdlib `net/http` ServeMux (Go 1.22+ method+`{param}` patterns)
   covers all 551 routes incl. `{id}` and `{token}`. No third-party router.
8. **Frontend:** `resources/views/*.php` → `html/template`; the 12 JS files and
   all CSS/vendor assets are reused unchanged and embedded. CSP stays
   `script-src 'self'` (no inline scripts).

## Dependencies (minimal)
- `modernc.org/sqlite` (DB driver) — unavoidable.
- `golang.org/x/crypto` (argon2id, bcrypt, nacl/secretbox).
Everything else is standard library.

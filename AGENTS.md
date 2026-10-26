# AGENTS.md — Permanent instructions for AI agents working on WhaCP

This file is for any AI agent (or developer) picking up work on this
repository. It captures the durable facts, conventions and commands that do
not change between tasks. For the **current, task-specific status** (what was
just done, what is unfinished), read `AGENT_HANDOFF.md` instead — that file is
refreshed per handoff; this one is stable.

Authoritative sources, in order of precedence when they disagree:
`CONTRIBUTING.md` → `docs/development/README.md` → `README.md` →
`CHANGELOG.md`. This file summarises them; it does not replace them.

---

## 1. What WhaCP is

WhaCP ("WHA Control Panel") is a free, MIT-licensed Linux server & hosting
control panel for VPS/dedicated servers (a cPanel/Plesk-class product). Core
rule: **a control panel has root-level consequences, so every privileged
operation must be explicit, validated, audited and reversible.**

Two components:

- **`server/`** — Go web control panel (module
  `github.com/mchkhawarshahzad/whacp/server`, Go 1.24). Single binary
  `whacp` at `/usr/local/bin/whacp`; serves HTML templates + embedded
  static assets over HTTP, opens the SQLite database, auto-runs pending
  SQL migrations and calls `authz.SyncDefaults` on boot, and talks to the
  agent over an authenticated Unix socket (`POST /v1/actions/<name>`).
  Never runs shell commands itself; privileged operations are dispatched
  to the agent.
- **`agent/`** — Go service (module `github.com/mchkhawarshahzad/whacp/agent`,
  Go 1.24). Runs on the managed server. Exposes **named actions only**; no
  shell, only allowlisted binaries (`execx`). Unix socket (token + peer UID)
  locally, pinned TLS 1.3 for remote servers. A write gate
  (`allow_write_actions`) guards all mutating actions.

Supporting dirs: `installer/` (install/upgrade/uninstall shell scripts),
`docs/`, `plugins/` (example plugin in PHP — the plugin SDK is intentionally
PHP for third-party extensions), `scripts/` (incl. `dev.sh`), `tests/`
(integration, e2e, VM/real-host), `dist/` (built agent + panel binaries).

## 2. Repository / branch facts

- Remote: `https://github.com/mchkhawarshahzad/WhaCP`.
- **There is no `main` branch.** The remote's default branch is
  `claude/sleepy-ptolemy-bdv6ib`, which is where all development lives. Do not
  assume `origin/main` exists.
- Current version: see `VERSION` (at time of writing `1.0.0-rc1`).
- Commit on the designated working branch; push with
  `git push -u origin <branch>`. Do **not** open a pull request unless the
  user explicitly asks.
- **Commit messages:** imperative subject ≤ 72 chars, body explaining *why*.
  End each commit with the attribution trailer your session provides
  (`Co-Authored-By:` + `Claude-Session:` lines). Never put a model
  identifier anywhere in repo artifacts except that trailer.

## 3. Build, test, lint — the exact commands

Run from the repo root unless noted. `make` targets mirror CI:

```bash
make lint          # gofmt check + go vet + PHP -l sweep + shell -n (CI gate)
make test          # test-agent + test-panel
make build         # builds dist/whacp-agent
make smoke         # tests/integration/smoke.sh
scripts/dev.sh     # run agent (against fixture) + panel on http://127.0.0.1:8090
```

**Agent (Go):**
```bash
cd agent && gofmt -l .        # MUST be empty (CI fails otherwise)
cd agent && go vet ./...
cd agent && go test -race ./... # ~194 test files; the realhost suite is tagged `realhost` and excluded by default
```

**Panel (PHP):**
```bash
cd panel && composer install              # first time (writes vendor/)
cd panel && vendor/bin/phpunit --no-coverage [files…]
```
- The full panel suite can hit a 300s `max_execution_time` cap; when iterating,
  **run specific test files** (e.g.
  `vendor/bin/phpunit --no-coverage tests/Http/AppsTest.php`) rather than the
  whole suite.
- Panel tests live under `panel/tests/{Unit,Http,Integration,Security,Performance}`;
  HTTP tests use `tests/Support/HttpTestCase` with a **fake agent**
  (`$this->agent->succeed(...)`, `$this->lastCall(...)`, `$this->post(...)`,
  `$this->flash(...)`).

**CI** (`.github/workflows/ci.yml`): gofmt gate on `agent/`, `go vet`,
`go test -race`, `make lint`, `phpunit`, plus an opt-in cross-OS real-host VM
matrix (Ubuntu 22.04/24.04, AlmaLinux 9, Rocky 9, …).

## 4. Engineering rules (mandatory — from CONTRIBUTING.md)

1. **Never break existing websites.** Host changes follow *Detect → Backup →
   Validate → Apply → Verify*, with rollback on failure.
2. **No arbitrary command execution.** Panel never shells out. The agent runs
   only `execx`-allowlisted binaries with fixed argument shapes, no shell. New
   capability = new **named agent action**.
3. **Never trust paths.** Use `filesystem.Root` in the agent; validate and
   confine every user-supplied path.
4. **Never reload an invalid config.** Generate → validate (`nginx -t`,
   `named-checkzone`, …) → reload, keeping the previous working file.
5. **Idempotency.** Running an action twice must not corrupt anything.
6. **Never expose secrets** in responses, logs or audit records
   (`logging.Redact`).
7. **Audit** every privileged/destructive action.
8. **Mark modifying agent actions `Mutating: true`** so they stay behind the
   write gate.
9. **Friendly user errors** (what/why/what-to-do); technicals go to logs.
10. **Original work only** — no copied code/UI/assets/wording from other panels.

## 5. Code style

- **Go:** `gofmt`; small packages under `internal/<domain>`; `context.Context`
  on anything that can block; `log/slog`; stdlib first. Thin HTTP handlers
  that call into domain services/repositories; **repositories own SQL**,
  services own behaviour. Templates live under `server/internal/web/templates/`
  and are parsed once at server start; static assets are `//go:embed`-ed from
  `server/internal/web/assets/`.
- **Plugins (PHP):** PSR-12, `declare(strict_types=1)`, thin entrypoint, output
  escaped. The plugin SDK is intentionally PHP; plugin code runs under
  PHP-FPM on hosted sites, not as part of the panel process.
- **No inline `<script>` or `<style>`.** The panel sends a strict
  Content-Security-Policy (`script-src 'self'`; see
  `server/internal/http/server.go` CSP header setup); inline scripts are
  blocked by the browser. Put page behaviour in the embedded JS assets
  (`server/internal/web/assets/js/whacp.js` — global delegated handlers
  keyed off `data-*` attributes) or a page-specific asset. Keep templates
  free of inline `<script>`/`<style>` blocks.

## 6. Per-PR expectations

- One logical change per commit/PR, with tests (parsers → fixture tests; HTTP
  pages → an `httptest` test against the `Server.Handler()`).
- Document new features in `docs/` **and add a `CHANGELOG.md` line** (under
  `## [Unreleased]`). This is frequently the step that gets missed — check it.
- Describe any host-touching operation and how it validates/rolls back.
- `make lint test smoke` must pass (gofmt/vet, `go test -race ./...`, the
  pure-Go integration smoke test).

## 7. Key paths to know

| Concern | Path |
|---|---|
| Server binary / subcommands | `server/cmd/whacp/` (`serve`, `version`, `bootstrap-admin`, `set-admin-password`, `has-admin`) |
| Agent binary | `agent/cmd/whacp-agent/` |
| HTTP routing & middleware | `server/internal/http/server.go` |
| Auth / roles / permissions | `server/internal/auth/`, `server/internal/authz/`, `server/internal/identity/` |
| SQL migrations | `server/internal/database/migrations/*.sql` (auto-applied in a transaction on boot) |
| Embedded web assets | `server/internal/web/assets/` + `server/internal/web/assets.go` (`//go:embed`) |
| HTML templates | `server/internal/web/templates/*.html` |
| Plan limits & enforcement | `server/internal/plans/` |
| Website create/apply (agent side) | `agent/internal/sites/` |
| CSP / security headers | `server/internal/http/server.go` (`Content-Security-Policy` header) |
| Agent actions registry | `agent/internal/actions/`, per-domain `agent/internal/<domain>/` |
| Release/audit docs | `docs/release/` |

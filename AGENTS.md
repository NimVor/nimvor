# AGENTS.md — Permanent instructions for AI agents working on NimVor

This file is for any AI agent (or developer) picking up work on this
repository. It captures the durable facts, conventions and commands that do
not change between tasks. For the **current, task-specific status** (what was
just done, what is unfinished), read `AGENT_HANDOFF.md` instead — that file is
refreshed per handoff; this one is stable.

Authoritative sources, in order of precedence when they disagree:
`CONTRIBUTING.md` → `docs/development/README.md` → `README.md` →
`CHANGELOG.md`. This file summarises them; it does not replace them.

---

## 1. What NimVor is

NimVor is a free, MIT-licensed Linux server & hosting
control panel for VPS/dedicated servers (a cPanel/Plesk-class product). Core
rule: **a control panel has root-level consequences, so every privileged
operation must be explicit, validated, audited and reversible.**

Two components:

- **`panel/`** — PHP 8.3+ web app (custom MVC, SQLite). Never runs shell
  commands. Talks to the agent over an authenticated channel
  (`POST /v1/actions/<name>`).
- **`agent/`** — Go service (module `github.com/NimVor/nimvor/agent`,
  Go 1.24). Runs on the managed server. Exposes **named actions only**; no
  shell, only allowlisted binaries (`execx`). Unix socket (token + peer UID)
  locally, pinned TLS 1.3 for remote servers. A write gate
  (`allow_write_actions`) guards all mutating actions.

Supporting dirs: `installer/` (install/upgrade/uninstall shell scripts),
`docs/`, `plugins/`, `scripts/` (incl. `dev.sh`), `tests/` (integration, e2e,
VM/real-host), `dist/` (built agent).

## 2. Repository / branch facts

- Remote: `https://github.com/NimVor/nimvor` (public, MIT). The default branch is `main`;
  work happens on the designated feature branch (e.g. `claude/sleepy-ptolemy-bdv6ib`) and is merged
  to `main` only when the user asks.
- Current version: see `VERSION` (at time of writing `1.0.0`).
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
make build         # builds dist/nimvor-agent
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
  on anything that can block; `log/slog`; stdlib first.
- **PHP:** PSR-12, `declare(strict_types=1)`, constructor injection, thin
  controllers, **repositories for SQL**, **services for behaviour**. Escape all
  output with `e()`.
- **No inline `<script>` or `<style>`.** The panel sends a strict
  Content-Security-Policy (`script-src 'self'`,
  `app/Http/Middleware/SecurityHeaders.php`); inline scripts are blocked by the
  browser. Put page behaviour in `panel/public/assets/js/nimvor.js` (global,
  delegated handlers keyed off `data-*` attributes) or a page-specific asset
  loaded via `$this->asset(...)`. A regression guard,
  `tests/Unit/ViewTest.php::testNoViewUsesInlineScripts`, fails the build on any
  inline `<script>` in a template — keep it green.

## 6. Per-PR expectations

- One logical change per commit/PR, with tests (parsers → fixture tests; panel
  pages → an HTTP test).
- Document new features in `docs/` **and add a `CHANGELOG.md` line** (under
  `## [Unreleased]`). This is frequently the step that gets missed — check it.
- Describe any host-touching operation and how it validates/rolls back.
- `make lint test smoke` must pass.

## 7. Key paths to know

| Concern | Path |
|---|---|
| Plan limits & enforcement | `panel/app/Services/PlanLimits.php` |
| Website create/child/apply | `panel/app/Services/WebsiteService.php` |
| App installer | `panel/app/Services/AppInstallerService.php`, `app/Http/Controllers/AppsController.php`, `resources/views/sites/apps.php` |
| Global panel JS | `panel/public/assets/js/nimvor.js` |
| CSP / security headers | `panel/app/Http/Middleware/SecurityHeaders.php` |
| Agent actions registry | `agent/internal/actions/`, per-domain `agent/internal/<domain>/` |
| Release/audit docs | `docs/release/` |

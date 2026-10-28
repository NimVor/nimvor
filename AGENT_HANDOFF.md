# AGENT_HANDOFF.md — Verified development status

> **Historical.** This is the handoff written for 1.0.0-rc1 (2026-10-19) and is
> kept for reference; the branch and version facts below are out of date.
> Current status: `CHANGELOG.md`, `docs/release/1.0.0-ga-report.md`, `AGENTS.md`.

**Prepared:** 2026-10-19 · **Branch:** `claude/sleepy-ptolemy-bdv6ib` (the
remote's default branch; there is no `main`) · **HEAD:** `2ad3bc3` · **Version
(`VERSION`):** `1.0.0-rc1`

This is the handoff from a previous Claude account whose usage limit was
reached. Everything below was **verified against the repository on the date
above**, not taken from the prior conversation (which is not available). Where
something is inferred rather than observed, it says so.

---

## 1. Repository state (verified)

- **Working tree:** clean. `git status` reports nothing to commit — all work is
  committed and pushed.
- **Branches:** only one exists locally and on the remote —
  `claude/sleepy-ptolemy-bdv6ib`. `git ls-remote --heads origin` shows just that
  ref; the remote's `HEAD` points at it. **No `main`/`master`.**
- **Local HEAD == remote HEAD** (`2ad3bc3…`), so nothing is unpushed.
- **Pull requests:** none (open or closed) found via the GitHub API.
- **Untracked/uncommitted changes:** none.

## 2. Most recent work (last 5 commits, newest first)

| Commit | Summary |
|---|---|
| `2ad3bc3` | **App installer: choose main domain or a subdomain as the install target.** Adds an "Install on" selector to the app-install modal; install runs against the chosen site's own document root. |
| `03d5c32` | **Move inline page scripts to `nimvor.js` (fixes CSP block).** Inline `<script>` on the Apps page (and the Website-Settings PHP-profile toggle and MongoDB password modal) was blocked by `script-src 'self'`; moved to delegated handlers. Added `ViewTest::testNoViewUsesInlineScripts` guard. |
| `e714a2c` | **Count addon domains against the website plan limit.** Addon domains + primaries now count toward the `websites` cap; subdomains keep their own separate limit. |
| `9d59d81` | runtimes: offer and resolve the system's Python versions. |
| `41ea349` | docs: changelog for the customer-portal feature program. |

The trajectory of recent work is a **reactive bug-fix / small-feature loop on
the customer portal** (plan-limit correctness, the CSP regression, the
app-installer target selector), driven by user reports — not a single large
in-progress feature. No evidence of a half-written feature left mid-change
(tree is clean, builds pass).

## 3. Build / test verification (run on 2026-10-19)

| Check | Result |
|---|---|
| `php -l` sweep over `panel/{app,resources,routes}` | ✅ clean |
| `phpunit` — `PlanResourcesTest`, `AppsTest`, `ViewTest`, `SiteMongoTest`, `SitePhpProfileTest` | ✅ 22 tests / 88 assertions pass |
| `go build ./...` (agent) | ✅ builds |
| `go test ./internal/runtimemgr/...` | ✅ pass |
| `gofmt -l agent` | ❌ **one file dirty** (see 4.1) |

The full suites (whole `phpunit`, `go test -race ./...`) and `make smoke` were
**not** re-run end-to-end in this handoff (time/scope); the targeted suites
above — the ones the recent commits touched — are green.

## 4. Known issues / unfinished work (verified)

### 4.1 `agent/internal/egress/egress.go` gofmt — ✅ RESOLVED
Was committed (since `7b5af04`) with imports out of order (`sort`/`regexp`
swapped), which failed the CI gofmt gate (`.github/workflows/ci.yml` lines
42–43) and `make lint`. Fixed in this session with `gofmt -w`; `gofmt -l agent`
is now empty and the lint gate passes again.

### 4.2 CHANGELOG behind the last three commits — ✅ RESOLVED
`## [Unreleased]` now has entries for the addon-domain plan-limit fix
(`e714a2c`) and the CSP inline-script fix (`03d5c32`) under **Fixed**, and the
app-installer target selector (`2ad3bc3`) under **Added**.

### 4.3 Documented remaining GA gate: cross-OS real-host matrix
Per `CHANGELOG.md` (GA security program) and `docs/release/1.0.0-ga-readiness.md`,
the GA security work is "implemented and passing locally; the cross-OS
real-host matrix (Ubuntu 22.04/24.04, AlmaLinux 9, Rocky 9) is the remaining GA
gate." This is the main outstanding release task — it needs the VM/real-host CI
jobs run green, not more feature code. (This matches the one still-open item in
the prior task list: "GA P3–P8: matrix, negative tests, scorecard, report".)

## 5. Suggested next steps (in order)

1. ~~Fix 4.1 (gofmt) and 4.2 (CHANGELOG)~~ — done in this session.
2. Continue the customer-portal fix/feature loop as the user directs, or drive
   the GA real-host matrix (4.3) toward green.
3. Before any PR: `make lint test smoke` must pass.

## 6. How to confirm this handoff yourself

```bash
git -C . status --short                     # clean
git -C . log --oneline -5                   # matches §2
git ls-remote --heads origin                # one branch, no main
cd agent && gofmt -l .                       # reproduces §4.1
cd panel && vendor/bin/phpunit --no-coverage tests/Http/AppsTest.php
```

See `AGENTS.md` for durable project conventions, commands and architecture.

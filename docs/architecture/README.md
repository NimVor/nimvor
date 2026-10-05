# Architecture

WhaCP has two components with a hard trust boundary between them.

```text
┌──────────────────────────── unprivileged ────────────────────────────┐
│  WhaCP Panel  (PHP 8.3+, user "whacp", 127.0.0.1:8090)               │
│   Http/ middleware: SecurityHeaders → VerifyCsrfToken → Authenticate │
│   Controllers → Services → Repositories → SQLite (/var/lib/whacp)    │
│   AgentClient ── the only way to touch the host                      │
└──────────────────────────────┬───────────────────────────────────────┘
                               │ HTTP over Unix socket
                               │ Authorization: Bearer <token>
                               │ POST /v1/actions/<name>  {params}
┌──────────────────────────────▼──────────── privileged (sandboxed) ───┐
│  WhaCP Agent  (Go, root with CAP_DAC_READ_SEARCH + CAP_CHOWN only)   │
│   server/   peer UID check, token check, body limits, timeouts       │
│   actions/  fixed registry; Mutating actions behind a write gate     │
│   platform/ OS abstraction (paths, service names per distribution)   │
│   nginx/ php/ ssl/ database/ redis/ cron/ users/ firewall/ systemd/  │
│   monitoring/ background collector (procfs) with in-memory history   │
│   filesystem/ confined, size-limited reads   execx/ binary allowlist │
└──────────────────────────────────────────────────────────────────────┘
```

## Why two components

The panel handles untrusted input from browsers. It must never hold the
power to change the server directly. The agent holds that power but exposes
it only as a small catalogue of named, validated actions. Compromising the
panel therefore yields, at most, the ability to call those actions — never a
root shell.

## Panel

- **MVC without a framework.** A small, auditable core (`app/Core`,
  `app/Http`) keeps dependencies at zero at runtime.
- **Composition root:** `Core\Application` wires services in a DI container;
  classes are autowired by constructor type.
- **Request pipeline:** security headers → session → CSRF → authentication &
  per-route permission → controller. Controllers are thin; business rules
  live in `Services`, SQL in `Repositories`.
- **Storage:** SQLite (WAL) via `Database\Connection`; all queries use bound
  parameters. Migrations in `database/migrations` are ordered and recorded.
  Moving to MySQL/PostgreSQL later only touches repositories.
- **Views:** plain PHP templates, escaped with `e()`. No inline scripts or
  styles, enforced by a strict CSP.

## Agent

- **One entry point:** `POST /v1/actions/{name}`. Handlers decode parameters
  strictly (unknown fields rejected) and return data in a uniform envelope.
- **Read-only by construction in this release:** every action is non-mutating,
  the write gate is off, and systemd mounts the whole filesystem read-only
  for the agent (`ProtectSystem=strict`).
- **OS abstraction:** `platform.Detect` reads `/etc/os-release` and returns a
  `Platform` that knows paths and unit names. Nothing else hard-codes
  distribution details.
- **Testability:** all host access goes through `filesystem.Root`, whose base
  can be a fixture tree, and through the `execx.Runner` interface.

## Discovery data flow

```text
agent: nginx.Discover ─┐
       php.Discover  ──┼─ discovery.Correlate → websites ─┐
       ssl.Inspect   ──┘                                  ├─ discovery.inventory
       database, redis, cron, users, firewall, services ──┘
panel: DiscoveryService → snapshot (JSON + sha256) in SQLite → pages
```

Snapshots are cached for `discovery_cache_seconds` and kept (last 20) so an
operator can see *when* the server's configuration changed.

## Design rules

The mandatory rules (Detect → Backup → Validate → Apply → Verify; no arbitrary
commands; never reload invalid config; idempotency; secrets never exposed;
audit everything) are listed in [CONTRIBUTING.md](../../CONTRIBUTING.md).

- [Package management and component installation](package-management.md): how packages are installed, diagnosed and recovered on APT and DNF systems.

# Development

## Environment

```bash
scripts/dev.sh              # agent on the fixture server + panel on :8090
DEV_ROOT=/ scripts/dev.sh   # inspect this machine (read-only)
make lint test smoke        # what CI runs
```

Dev state lives in `.dev/` (git-ignored). Log in as `admin` /
`dev-password-change-me`.

## Layout

```text
agent/
  cmd/whacp-agent/        main: config, wiring, socket, graceful shutdown
  internal/
    actions/              action registry, write gate, built-in read-only actions
    config/               JSON config with validation
    cron/ database/ firewall/ nginx/ php/ redis/ ssl/ users/   discovery by domain
    discovery/            correlation into websites + full inventory
    execx/                allowlisted command runner (+ FakeRunner for tests)
    filesystem/           confined, size-limited reads (Root)
    logging/              slog setup and secret redaction
    monitoring/           procfs parsers and background collector
    platform/             OS abstraction
    server/               HTTP API, auth, peer credentials, socket listener
    systemd/              unit status
  api/openapi.yaml
  tests/fixtures/ubuntu-host/   fake server used by tests and scripts/dev.sh
panel/
  app/{Core,Http,Security,Services,Repositories,Agent,Database,View,Support}
  config/app.php          defaults (override: /etc/whacp/panel.php or $WHACP_CONFIG)
  database/migrations/    ordered SQL migrations
  public/                 front controller, router for php -S, assets (vendored Bootstrap)
  resources/views/        templates
  routes/web.php          routes with per-route permissions
  tests/{Unit,Integration,Http}
```

## Adding an agent action

1. Implement the logic in the relevant `internal/<domain>` package, reading
   only through `filesystem.Root` and running binaries only through
   `execx.Runner`.
2. Register it in `actions/builtin.go` (or a new file) with a dotted name,
   a description, strict parameter decoding and `Mutating: true` if it
   changes anything.
3. Add fixture-based tests, update `agent/api/openapi.yaml` and
   `docs/agent/README.md`.
4. Call it from the panel via `AgentClient::call()` inside a service.

## Adding a panel page

1. Add a permission in `Security/Permissions.php` if needed (and to the
   default roles).
2. Add the route in `routes/web.php` with `permission`.
3. Keep the controller thin; put behaviour in a service and SQL in a
   repository; audit state changes with `AuditLogger`.
4. Template: escape with `e()`, no inline JS/CSS, use `data-*` hooks in
   `public/assets/js/whacp.js`.
5. Tests: service tests in `tests/Integration`, an HTTP test in `tests/Http`.

## Fixture server

`agent/tests/fixtures/ubuntu-host` is a miniature Ubuntu 24.04 filesystem:
Nginx with an HTTPS site + redirect, a PHP 8.4 site on TCP FPM, a reverse
proxy with a self-signed certificate, a disabled site, a `conf.d` vhost and a
default server; PHP 8.3/8.4 with pools; MariaDB data directory; Redis with a
password; certbot; cron with embedded secrets; UFW. The certificates are
test-only and no private keys are stored. The smoke test verifies the tree
is unchanged after a full discovery run.

See the [roadmap](roadmap.md) for upcoming phases.

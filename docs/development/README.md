# Development

## Environment

```bash
scripts/dev.sh              # agent on the fixture server + panel on :8090
DEV_ROOT=/ scripts/dev.sh   # inspect this machine (read-only)
make lint test smoke        # what CI runs
```

CI runs the quick jobs on every push and pull request: unit, HTTP, smoke,
upgrade, browser, and real-host on the Ubuntu 24.04 runner. A newer push
to a branch cancels the run it replaces. The virtual machines (AlmaLinux
9/10, Rocky Linux 9/10, Ubuntu 22.04/26.04: real-host, install and
post-install tests) use most of the Actions minutes, so they run only:

- when started by hand: *Actions → CI → Run workflow*, pick the branch
  (GitHub offers this once the workflow is on the default branch);
- for a push whose commit message contains `[full ci]`;
- before every release: pushing a `v*` tag runs everything first.

Start the full run before merging changes to the installer, upgrade,
system services, SELinux, or anything run as a website account.

Dev state lives in `.dev/` (git-ignored). Log in as `admin` /
`dev-password-change-me`.

## Layout

```text
agent/
  cmd/nimvor-agent/        main: config, wiring, socket, graceful shutdown
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
  config/app.php          defaults (override: /etc/nimvor/panel.php or $NIMVOR_CONFIG)
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
   `public/assets/js/nimvor.js`.
5. Tests: service tests in `tests/Integration`, an HTTP test in `tests/Http`.

## Forms: background submits and windows

Controllers keep the plain pattern: validate, act, flash a message and
redirect (or flash errors and redirect back).

**Background submits.** `nimvor.js` submits POST forms in the page and its
header actions in the background, with the header `X-NimVor-Ajax: 1` and
`Accept: text/html`, exactly as a normal post.
- **Server side:** the `AjaxForms` middleware turns the final redirect into
  `{"success": true, "redirect": "…"}`. CSRF, authorization and validation
  are unchanged.
- **Page swap:** the page loads that address and swaps in the new content
  (`.nim-shell`), keeping the scroll position. The flash message is shown,
  as a notification when it is out of view.
- **History:** each submit adds a history step, and Back loads the previous
  page in full.
- **Other responses:** an HTML answer (an error page) is shown; a file is
  downloaded.

A form is submitted normally when:
- it has a `target`, is a GET form, or has `data-nim-noajax` on itself or a
  parent;
- the page has its own scripts (`section('scripts')`), or declares
  `<?php $this->start(); ?>off<?php $this->end('ajax'); ?>`;
- it is outside the page layout, such as the file manager or the sign-in
  pages.

Every `DOMContentLoaded` handler in `nimvor.js` runs again after a swap, so it
must be safe to run twice. Downloads stay GET links.

**Form windows.** Create and edit forms open in a Bootstrap modal:
```php
<button type="button" data-bs-toggle="modal" data-bs-target="#new-thing">Add thing</button>
<?= $this->modalStart('new-thing', 'Add thing', $errors !== []) ?>
    <form method="post" action="…" data-nim-busy>…</form>
<?= $this->modalEnd() ?>
```
- The third argument reopens the window after a refused save, with the
  errors.
- A link to `#new-thing` opens the window, and its first field gets focus.
- Windows for table rows are collected in the loop and printed after the
  table (see `ips/index.php`).
- Destructive forms keep `data-nim-confirm` (and
  `data-nim-confirm-word` for high-risk ones).

## Fixture server

`agent/tests/fixtures/ubuntu-host` is a miniature Ubuntu 24.04 filesystem:
Nginx with an HTTPS site + redirect, a PHP 8.4 site on TCP FPM, a reverse
proxy with a self-signed certificate, a disabled site, a `conf.d` vhost and a
default server; PHP 8.3/8.4 with pools; MariaDB data directory; Redis with a
password; certbot; cron with embedded secrets; UFW. The certificates are
test-only and no private keys are stored. The smoke test verifies the tree
is unchanged after a full discovery run.

See the [roadmap](roadmap.md) for upcoming phases.

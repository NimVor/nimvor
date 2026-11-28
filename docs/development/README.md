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

## Windows instead of pages

A link marked `data-nim-site-modal` loads a window into the one shared `#nim-site-modal` (in `partials/job_modal.php`, so it is on every
page) from its `href`, or from `data-nim-modal-href` when the plain `href` is the full page (so Ctrl-click and no-JS still work);
`data-nim-modal-xl` asks for a wider window. The server answers with the window's HTML only (`modal-header`, `modal-body`); its form
carries `data-nim-site-modal-form` and `data-nim-modal-error`, is sent in the background with `Accept: application/json`, and the
controller answers `{success, message, redirect?}` or a 422 with `error.errors` (the fields are marked in the window). Scripted parts
start on the `nim:modal-loaded` event. Used by Create website (`sites.create.modal`), Nginx (`nginx.modal`, Start/Stop at the top), the
database server settings and the website quick settings.

## The new-website form

`sites/form.php` is a four-step wizard for a new website (`data-nim-wizard`, `data-nim-step` 1–4: Type, Domain and owner, Settings, Review; `initWizard` in `nimvor.js` validates a step on *Next*, fills the review summary, and only the last step shows the submit button); a type-card click advances. Edit mode shares the file without the wizard. Fields that depend on the type carry `data-nim-kind`, the rarely used
ones are not asked at all: the system user is derived from the domain, the PHP limits come from the selected version (`WebsiteService::phpVersionDefaults()`: `php.ini_get` effective memory/upload/execution values and `php.fpm_get` process cap, standard defaults when unreadable) and the security profile from the panel default (edit mode shows the system user and request size). The window version is `GET /websites/new/modal`; its form is sent in the background and answered as JSON
(`SitesController::store`). With `wizard=1` an administrator must send `owner_mode` (`existing` + `owner_id`, `new` + `new_email`
[+ `new_username`], or `none`); `new` creates the customer through `AccountService::createCustomerWebsite()` and flashes the generated
password once (`secret`). Customers and resellers own what they create (`WebsiteService::resolveOwner`); API callers send no `wizard`.

## Service logos

`View::logo($id, $size, $class)` returns the logo of a service as an `<img>` (or `''`, so `?:` keeps the icon): `$this->logo('nginx', 16) ?: '<i class="bi bi-hdd-rack"></i>'`.
The files are `public/assets/img/services/NAME.png` (sources and licences in the README next to them) and the ids the panel uses
for them are in `View::LOGOS`.

## Field hints (tooltips)

A field's explanation is not printed under the input. Put it in a hint right after the label:

```php
<label class="form-label" for="f-host">Host name</label><?= $this->hint('A full name such as server1.example.com.', 'Host name') ?>
```

`View::hint($text, $about)` returns a small info button (`.nim-hint`) and the text in a hidden span that the button points to with
`aria-describedby`, so screen readers read it. `assets/js/nimvor.js` shows it in one tooltip on hover, on keyboard focus and when the
button is clicked or tapped (it then stays until Escape, a click elsewhere or scrolling); the tooltip is placed against the window, so
tables and windows do not cut it off. The shared `partials/field` takes the text from its `help` option. The text is escaped; use plain
text (no markup).

Keep as ordinary visible text: security warnings, notices about destructive actions, consequences the user must see before saving (a
restart, a changed address), short section notes, and the technical variable name, shown muted (`.nim-var`) next to the label.
`tests/e2e/fieldhints.js` covers hover, focus, click, Escape and the phone layout.

## Fixture server

`agent/tests/fixtures/ubuntu-host` is a miniature Ubuntu 24.04 filesystem:
Nginx with an HTTPS site + redirect, a PHP 8.4 site on TCP FPM, a reverse
proxy with a self-signed certificate, a disabled site, a `conf.d` vhost and a
default server; PHP 8.3/8.4 with pools; MariaDB data directory; Redis with a
password; certbot; cron with embedded secrets; UFW. The certificates are
test-only and no private keys are stored. The smoke test verifies the tree
is unchanged after a full discovery run.

See the [roadmap](roadmap.md) for upcoming phases.

# Writing NimVor plugins

A plugin adds pages, sidebar entries and event listeners to the panel. It
is PHP code that runs **inside the panel process**, so:

- plugins are installed on disk by an administrator (as root), in
  `/opt/nimvor/plugins/<name>/` — they cannot be uploaded through the web
  interface;
- they run only after an administrator enables them under
  **Administration → Plugins**;
- files that are group- or world-writable, and symbolic links, are refused.

Plugins never get more power over the *server* than the panel has: they can
call the same named agent actions as the panel (see `agent/api/openapi.yaml`)
and nothing else. **Within the panel, however, an enabled plugin is fully
trusted:** it runs with the panel's rights, can read the panel database and
the agent token, and is not limited by roles and permissions. Treat enabling
a plugin like giving its author administrator access. Install only plugins
whose code you have reviewed.

## Layout

```
/opt/nimvor/plugins/my-plugin/
├── plugin.json      manifest
├── plugin.php       returns function (PluginContext $ctx): void
└── views/*.php      templates rendered inside the panel layout
```

`plugin.json`:

```json
{
  "name": "my-plugin",
  "title": "My plugin",
  "version": "1.0.0",
  "description": "What it does.",
  "author": "You",
  "license": "MIT",
  "permissions": {
    "plugin.my-plugin.view": "View my plugin"
  }
}
```

`name` must equal the folder name (2–40 lowercase letters, digits or
dashes). Permissions must be named `plugin.<name>.<something>`; they appear
in role management and are granted to administrators when the plugin is
enabled.

## The context

```php
return function (NimVor\Plugins\PluginContext $ctx): void {
    // A page at /plugins/my-plugin (GET or POST). Every route needs a
    // permission (the plugin's own or a core one) and gets the same
    // authentication and CSRF protection as core pages.
    $ctx->route('GET', '/', fn ($request) => $ctx->render('index', ['x' => 1]), 'plugin.my-plugin.view');

    // Sidebar entry under "Plugins".
    $ctx->nav('My plugin', 'puzzle', '/', 'plugin.my-plugin.view');

    // Events: website.created, website.updated, website.deleting,
    // website.deleted, backup.completed, backup.failed.
    $ctx->on('website.created', function (array $payload) use ($ctx): void { /* … */ });
};
```

| Method | Purpose |
|---|---|
| `route($method, $path, $handler, $permission)` | Register `GET`/`POST` `/plugins/<name><path>`; `$handler($request, ...$params)` returns a `Response` or HTML string. |
| `render($template, $data)` | Render `views/<template>.php` inside the layout (same helpers as core views: `e()`, `$this->csrfField()`, `$this->can()`, …). |
| `nav($label, $icon, $path, $permission)` | Sidebar entry ([Bootstrap Icons](https://icons.getbootstrap.com/) name). |
| `on($event, $listener)` | Event listener; exceptions are logged, never break the panel. |
| `agent()` | The agent client (`->call('system.info')`). |
| `db()` | The panel database connection. Create your own tables with a `plugin_<name>_` prefix. |
| `setting($key, $default)` / `setSetting($key, $value)` | Plugin-scoped settings. |
| `audit($action, $details)` | Write to the audit log as `plugin.<name>.<action>`. |

If `plugin.php` throws while loading, the plugin's routes are removed for
that request and the error is shown on the Plugins page; the panel keeps
working.

See `plugins/example-status` in the source tree for a complete example.

# Writing WhaCP plugins

A plugin adds pages, sidebar entries and event listeners to the panel. It
is a **separate program** — in any language — that listens on a Unix socket.
WhaCP never runs plugin code: it reads the plugin's manifest, signs the user
in, checks the route's permission and the CSRF token, and forwards the
request to the plugin's socket. The plugin's HTML is shown inside the panel
layout.

- Plugins are installed on disk by an administrator (as root), in
  `/opt/whacp/plugins/<name>/` (`WHACP_PLUGINS_DIR`) — they cannot be
  uploaded through the web interface. The administrator also starts the
  program (for example from a systemd unit running as the panel's user).
- They take effect only after an administrator enables them under
  **Administration → Plugins**.
- Folders and files that are group- or world-writable, and symbolic links,
  make a plugin unusable.

**Trust.** A plugin does not get the panel's database, its secrets or the
agent token, and it cannot call the agent through the panel. It does act for
the users who open its pages (the panel tells it who they are), and its HTML
is shown on the panel's origin (the content security policy still blocks
inline scripts). Install only plugins whose code you have reviewed. The
socket must be reachable by the panel's user only (mode 0600): the identity
headers are trusted because nobody else can send them.

## Layout

```
/opt/whacp/plugins/my-plugin/
├── plugin.json      manifest
└── …                the plugin's program (started by you, not by WhaCP)
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
  },
  "socket": "/run/whacp/plugins/my-plugin.sock",
  "routes": [
    {"method": "GET",  "path": "/",         "permission": "plugin.my-plugin.view"},
    {"method": "POST", "path": "/save",     "permission": "plugin.my-plugin.view"},
    {"method": "GET",  "path": "/item/{id}", "permission": "plugin.my-plugin.view"}
  ],
  "nav": [
    {"label": "My plugin", "icon": "puzzle", "path": "/", "permission": "plugin.my-plugin.view"}
  ],
  "events": ["website.created"]
}
```

- `name` must equal the folder name (2–40 lowercase letters, digits or
  dashes).
- `permissions` must be named `plugin.<name>.<something>`; they appear in
  role management and are granted to administrators when the plugin is
  enabled (and removed when it is disabled).
- `socket` is the absolute path of the plugin's Unix socket.
- `routes` are the only paths forwarded, under `/plugins/<name>` (GET or
  POST; `{x}` matches one path segment). The permission is a plugin's own or
  a core one. Anything else is a 404 without reaching the plugin.
- `nav` entries appear in the sidebar under "Plugins" for users holding the
  permission ([Bootstrap Icons](https://icons.getbootstrap.com/) name).
- `events` are the events the plugin wants: `website.created`,
  `website.updated`, `website.deleting`, `website.deleted`.

## What the plugin receives

A request to `/plugins/my-plugin/item/42?x=1` arrives as `GET /item/42?x=1`
on the socket with these headers (set by the panel, never by the browser):

| Header | Value |
|---|---|
| `X-WhaCP-User-ID`, `X-WhaCP-User` | the signed-in user's id and name |
| `X-WhaCP-Role` | the user's role name |
| `X-WhaCP-CSRF` | the CSRF token to put in forms (`_token`) |
| `X-WhaCP-Server` | the id of the server the user is working on |

A `POST` is forwarded only with a valid CSRF token (form field `_token` or
the `X-CSRF-Token` header); the body is at most 1 MiB. Escape everything you
print.

## What the plugin answers

- `text/html` — an HTML fragment shown inside the panel layout, with the
  status you send; set `X-WhaCP-Title` for the page title.
- `3xx` with `Location: /path` — a redirect to a page of the panel (a path
  starting with `/`; anything else is refused).
- `application/json` — passed through (for scripts of your own).

If the plugin is not running, users see "Plugin unavailable".

## Events

For each subscribed event the panel sends `POST /_events/<event>` with a JSON
body (`{"id":…, "domain":"…", "owner_id":…, "kind":"php"}` for websites),
waiting at most five seconds. A failing plugin is logged and never affects
the panel.

See `plugins/example-status` in the source tree for a complete example (a
Python program using only the standard library).

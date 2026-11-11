# Customer panel themes

Hosting customers can choose how their panel looks. A theme changes the **frame** (header, menus, how a website's
tools are arranged) and the look (colours, density, shapes). It never changes what the panel can do: every theme is
rendered by the same controllers, services, routes, permission checks, CSRF protection and audit logging, and every
theme lists every tool the customer's role and plan allow.

| Key | Name | Idea |
|---|---|---|
| `modern` | NimVor Modern | The standard panel (default for new installations). Not a "theme" in the code: it is the panel's own layout. |
| `classic` | Classic | Search box and a launcher grid of tools, grouped as Files, Databases, Domains, Email, Security, Software, Advanced. |
| `workspace` | Workspace | Light sidebar listing the websites, a card per website, tab strip for a website's tools. |
| `simple` | Simple | Large shortcuts, "All tools" drawer, bottom tab bar on phones. |
| `compact` | Compact | Dense lists, category menu, the account's limits always visible. |

The names and layouts are original. They are inspired by the *navigation ideas* of well-known hosting panels (tools
grouped by category, a websites-first dashboard, a minimal shared-hosting layout, a dense list layout), not by their
branding or exact designs.

## Who sees a theme

Themes apply to **hosting customers**, the accounts that get the simple layout (`View::simpleLayout()`). Server
administrators and resellers keep the server-management layout; the *light/dark mode* and *accent colour* apply to
everyone. Full-screen tools (file manager and editor, database browsers) keep their own window layout and take the
mode and accent.

## How it works

* `app/View/Themes.php` — the list of themes (key, name, description) and the accent colours, plus the
  administrator's setting definitions.
* `app/Services/ThemeService.php` — decides the theme, mode and accent for a viewer (their choice, else the
  administrator's default, else NimVor Modern), saves choices, and provides the account's websites and plan usage for
  the frame.
* `app/View/View.php` — `render()` swaps the panel layout (`layouts/app`) for the viewer's theme layout
  (`themes/<key>/layout`) and, when the theme has one, a page for its own version of a page
  (`themes/<key>/pages/<template>`, for example `themes/classic/pages/websites/index`). Everything else is the same
  template as for every other theme.
* `app/View/ThemeContext.php` — one place that works out the websites, the current website and its tools for the
  frame. `app/View/ThemeNav.php` — groups a website's tools into categories (a tool no category names lands in
  *Advanced*, so a theme can regroup tools but never hide one) and picks shortcuts.
* `app/Http/Controllers/AppearanceController.php` — the customer's *Appearance* page (`/appearance`), previews, and
  the administrator's page (`/settings/themes`).
* Preferences are stored on the user (`users.ui_theme`, `ui_mode`, `ui_accent`; empty = the administrator's default).
  Administrator controls are ordinary settings (`theme_default`, `theme_enabled_<key>`, `theme_customers_choose`,
  `theme_default_mode`, `theme_default_accent`, `theme_accent_<colour>`).

Only the chosen theme's assets are loaded: `assets/css/themes/base.css` (shared), `assets/css/themes/<key>.css`,
`assets/css/themes/accents.css` only when an accent colour was chosen, and `assets/js/themes/common.js`.

## Adding a theme

1. Register it in `Themes::all()` (key, label, tagline, description, the accent it looks best in). Keys are lower-case
   letters.
2. Create `panel/resources/views/themes/<key>/layout.php`. Start from an existing layout. It must:
   * open with `themes/components/head` and close with `themes/components/foot` (they load the right assets,
     the dark-mode script, the confirmation and background-task dialogs and the toast area);
   * call `ThemeContext::build($this)` for the websites, the current website, its tools, the account pages and the
     list for the finder;
   * show **all** of `$nt['items']` (the website's tools) and `$nt['accountPages']`, using `ThemeNav::grouped()` if it
     groups them. Never filter them: permissions and plan limits are already applied;
   * render `partials/flash`, `partials/theme_preview_bar`, `themes/components/page_head` (or the page's own
     `title`/`subtitle`/`actions` sections) and `$content`;
   * keep the *skip link* and landmarks (`<header>`, `<nav aria-label>`, `<main id="nt-main">`).
3. Create `public/assets/css/themes/<key>.css`. Set the theme's variables under `html[data-nim-theme="<key>"]`
   (`--nim-accent`, `--nt-bg`, `--nt-radius`, …) **and give the dark values** under
   `html[data-nim-theme="<key>"][data-bs-theme="dark"]` for every colour variable you set. Prefix class names with a short
   theme prefix (`ncl-`, `nws-`, `nsi-`, `nco-` for the existing ones). Use `base.css`'s `.nt-*` components where you can.
   Make it responsive: the menu of a sidebar layout becomes a slide-in menu (`data-nt-nav="toggle"`) below 992 px.
4. Optionally add a replacement for the websites list at `themes/<key>/pages/websites/index.php`. It receives the same
   variables as the shared page.
5. Add the theme to the thumbnails in `nimvor.css` (`.nim-thumb-<key>`) so the Appearance page can draw it.
6. `tests/Http/ThemesTest.php` runs every registered theme through the same checks (renders, shows every tool, opens
   every tool page, keeps the choice); a new theme is covered by adding it to `Themes::all()`.

Rules that keep themes safe: no inline `<script>` or `<style>` (the Content-Security-Policy blocks them), behaviour
only through `data-nt-*` attributes handled in `assets/js/themes/common.js` or the panel's `nimvor.js`; escape all
output with `e()`; do not add controllers, queries or routes for a theme.

## Previewing

`POST /appearance/preview` keeps a theme in the session only (a bar at the top offers *Keep this theme* and *Go
back*); nothing is saved or changed on the server until the customer keeps it.

# NimVor website

The public website for NimVor: home, features, resources (documentation, blog,
community and source, build services, sponsors, about), support (contact, privacy
policy) and a sign-in area with a **client dashboard** and an **administrator
dashboard with reports**.

It is separate from the control panel (`panel/`) and shares no code with it.

**Stack:** PHP 8.3, MySQL or MariaDB, and Redis or Memcached (or a file cache on a
server with neither). No framework, no build step, no external fonts or scripts.

## What is in it

| Area | What it does |
|------|--------------|
| Public pages | Server-rendered, original editorial design (cream and sage paper, a deep-green section, one orange accent, oversized headlines, hairline rules), works on phones. Fonts (Inter, Inter Tight, IBM Plex Mono, SIL OFL) are served from `public/assets/fonts`, with their licences. Pages that do not depend on who you are are cacheable and set no cookie. |
| Blog | Posts in the database, written under Admin, drafts, a small safe text format (headings, lists, bold, code, https links). |
| Contact and build services | Forms stored in the database, with a hidden field for bots and a per-address limit. |
| Sponsors | Added and removed by an administrator. |
| Accounts | Register, sign in, sign out, change name and password. Argon2id, sign-in throttling, session id renewed at sign-in. |
| Client dashboard | Only the signed-in client's own build requests and messages. |
| Admin dashboard | Overview, reports (sign-ups, visits, status counts, most visited pages), users (deactivate and reactivate), messages and build requests (set status), blog, sponsors, CSV exports. |

## Security

- Sessions, rate limits and cached pages live in the cache (Redis, Memcached or files); the session id is 256 bits, `HttpOnly`, `SameSite=Lax` and `Secure` over HTTPS. A session is only created when something needs one.
- Every `POST` needs a CSRF token. Every query is a prepared statement. Every printed value is escaped.
- A strict Content-Security-Policy (`script-src 'self'`, `style-src 'self'`, no inline script or style) plus `X-Frame-Options`, `nosniff`, a referrer and permissions policy and HSTS over HTTPS. A test fails the build if a template contains an inline script, style or event handler.
- Admin routes need the `admin` role (checked in the route table, not in each page). Client pages are scoped to the signed-in user's id.
- CSV exports prefix cells that a spreadsheet could read as a formula.
- Visits are counted per page and day only. No address, cookie or other identifier is stored.
- No secret is in the repository: settings come from environment variables or a file outside the web root.

## Run it locally

```bash
cd website
composer install                      # only needed for the tests
export DB_NAME=nimvor_site DB_USER=nimvor_site DB_PASS=...   # an empty database you created
export CACHE_DRIVER=file              # or redis / memcached
php bin/site migrate                  # creates the tables
php bin/site seed                     # adds the first blog post (once)
php bin/site user:create you@example.com "Your Name" --role=admin
php -S 127.0.0.1:8099 -t public public/index.php
```

## Settings

| Variable | Default | Meaning |
|----------|---------|---------|
| `SITE_URL` | `https://nimvor.com` | Public address (canonical links, sitemap) |
| `DB_HOST`, `DB_PORT`, `DB_NAME`, `DB_USER`, `DB_PASS` | `127.0.0.1`, `3306`, `nimvor_site`, `nimvor_site`, empty | MySQL or MariaDB |
| `CACHE_DRIVER` | `redis` | `redis`, `memcached` or `file` |
| `REDIS_HOST`, `REDIS_PORT`, `REDIS_PASSWORD` | `127.0.0.1`, `6379`, none | Redis (needs `ext-redis`) |
| `MEMCACHED_HOST`, `MEMCACHED_PORT` | `127.0.0.1`, `11211` | Memcached (needs `ext-memcached`) |
| `CACHE_DIR` | `storage/cache` | Where the file driver writes |
| `CACHE_PREFIX` | `nimsite:` | Prefix for every key, so several sites can share one Redis |
| `SESSION_TTL` | `7200` | Seconds a session lasts |
| `TRUSTED_PROXY` | `127.0.0.1` | The only address whose `X-Forwarded-For` and `X-Forwarded-Proto` are believed |
| `SITE_CONFIG` | none | A PHP file (outside the web root) returning an array that overrides the settings |

## Documentation, problems, sponsors and statistics

These pages read from GitHub on the server (visitors' browsers never contact GitHub):

- **Documentation** (`/docs`): the repository's `docs/**/*.md` plus `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md` and `CHANGELOG.md` are fetched and shown in the site's design, with a sidebar, an "On this page" list, search and links rewritten to stay on the site. Which files are published, and how they are grouped, is `app/Services/DocsCatalog.php`. The first visit fetches them; after that they are refreshed after the answer is sent when they are older than six hours (`DOCS_AUTO_SYNC=0` turns that off), or at once with **Admin → Docs and stats → Sync documentation now**. If GitHub cannot be reached the stored pages keep being served.
- **Problems** (`/resources/problems`): the repository's issues (not pull requests), read at most every ten minutes. Closed = resolved; open with an assignee or a label such as "in progress"/"confirmed" = in progress; other open = reported. If GitHub fails, the last good list is shown marked as old. Reporting happens on GitHub.
- **Sponsors** (`/resources/sponsors`): sponsors you add under Admin → Sponsors, plus the public ones from GitHub Sponsors copied in by **Sync from GitHub now**. GitHub only shows sponsors to a signed-in maintainer, so this needs `GITHUB_TOKEN` (a token that can read the organisation's sponsors; `GITHUB_SPONSORABLE` names the organisation or user, default `NimVor`). Tiers follow the monthly amount (silver from $25, gold from $100). The token is only ever sent to `api.github.com`.
- **Install page** (`/get-started`): the one-line installer, an **install command builder** (profile, PHP versions, host name, administrator, ports, options; also read-only and agent-only) and every option, with where things are installed. The builder is `public/assets/js/install-builder.js` (no inline script: the Content-Security-Policy only allows files). The command is pasted into a root shell, so nothing the visitor types is copied into it unless it matches the pattern the installer itself accepts; `tests/js/install-builder.test.js` checks that (run `node --test 'tests/js/*.test.js'`), and `PagesTest` checks the options and folders on the page against the installer scripts. Without JavaScript the page shows the full recommended command.
- **Statistics** (`/resources/stats`): counts from the anonymous reports installers send to `POST /api/v1/installs` (a random id, the version, the system's name and version, the processor type; no address is stored, and sending is rate limited per address). The counts are indicative: anyone can send a report.

| Variable | Default | Meaning |
|---|---|---|
| `GITHUB_REPO` / `GITHUB_BRANCH` | `NimVor/nimvor` / `main` | Where the documentation and issues come from |
| `GITHUB_TOKEN` | none | Needed for sponsors; also lifts GitHub's anonymous rate limit |
| `GITHUB_SPONSORABLE` | `NimVor` | The organisation or user whose sponsors are shown |
| `DOCS_AUTO_SYNC` | `1` | Refresh the documentation when a visitor finds it older than six hours |

## Deploy without a terminal

You do not need a command line, and you do not need Composer: the site loads its own
classes, and `composer.json` is only used to run the tests.

1. Upload the site's files (a zip of this folder, without `vendor/` and `tests/`) with your host's file manager and extract them, for example into `/var/www/nimvor-site`. Make **only the `public/` folder** the web root. The folders `config/` and `storage/` must be writable by the web server user.
2. Create an empty MySQL or MariaDB database and a database user for it.
3. In the file manager, create the file `storage/setup.key` and put one secret line in it (at least 16 random characters).
4. Open `https://your-site/` in a browser. It sends you to the setup page: enter the setup key, the database details, the cache (choose "Files" if you have neither Redis nor Memcached), the public address and your administrator account.
5. When it says "The site is set up", the tables exist, the key file is deleted, your settings are in `config/local.php` (private, mode 0600) and the setup page is gone. Log in at `/login`.

If the site is configured with environment variables or `SITE_CONFIG` instead, the setup page never appears.

**Apache.** The files include the Apache rules: `public/.htaccess` sends every request that is not a real file to `index.php`, and `.htaccess` in the site's own folder does the same if you cannot change the web root, while answering 403 or 404 for `config/`, `storage/`, `app/` and the other private folders. They need `mod_rewrite` and `AllowOverride All` for the site's folder (see `deploy/apache.conf`). If you see Apache's own "Not Found" page for `/setup`, those rules are not being applied: the web root is probably not `public/` or `.htaccess` files are ignored. On Nginx use `deploy/nginx.conf`.

## Deploy

1. A web server user with PHP 8.3 (`pdo_mysql`, `mbstring`, `sodium`, and `redis` or `memcached`), a database and a database user with rights on that database only.
2. Put `website/` somewhere such as `/var/www/nimvor-site` and make **only `public/`** the web root. See `deploy/nginx.conf`.
3. Put the settings in `/etc/nimvor-site/config.php` (readable by the PHP user only) and set `SITE_CONFIG` in the PHP-FPM pool.
4. Run `php bin/site migrate`, then `php bin/site seed`, then create the first administrator with `php bin/site user:create`.
5. Check `https://your-site/health` (`ok`, or `503` if the database or cache is down).

Updating: pull the new code and run `php bin/site migrate`. **Without a terminal**, upload the new files, then import the SQL file for each migration the site has not had yet (in order) with phpMyAdmin or your host's database tool: `database/sql/0002_docs_problems_stats.sql` upgrades a site set up before the documentation, sponsors and statistics pages. Each file runs once, records itself in the `migrations` table, and says so at its top; `tests/Unit/MigrationSqlTest.php` keeps it identical to the PHP migration. To load the documentation without waiting for a first visit: `php bin/site docs:sync`; to copy the sponsors: `php bin/site sponsors:sync`.

## Tests

```bash
cd website
composer install
DB_NAME=nimvor_site_test DB_USER=... DB_PASS=... vendor/bin/phpunit --no-coverage
```

The tests run the whole application in-process against a **real database and a real
cache** and **empty the tables of the database they are given**: never point them at
real data. `TEST_CACHE=file` runs them without Redis. The Memcached driver is only
exercised on a machine that has `ext-memcached` and a Memcached server (the test says
so when it skips).

## Not built yet

- Password reset by email (there is no mail sending in the site). An administrator can reset a password on the server with `php bin/site user:password EMAIL`.
- Email notifications for new messages and requests: they appear in the admin area.
- Two-factor sign-in.

# Per-website `php.ini` and `.user.ini`

Customers can put either file in their website and NimVor applies it to that
website's own PHP-FPM pool, with no Apply button and without touching the
server's PHP configuration or any other website.

```text
/home/nimvor/example.com/            website folder (not served)
├── php.ini                          optional: applies below the document root's php.ini
└── public_html/                     document root (the real one, if it differs)
    ├── php.ini                      applied by NimVor
    └── .user.ini                    applied by PHP itself
```

## Architecture

NimVor already gave every website its own pool (`nimvor-<domain>.conf`) with
the plan's limits, the confinement (`open_basedir`, temporary folders,
`disable_functions`, error log, sendmail) and the validated directives set in
the panel (`php.ini` map of the website). This feature extends that layer; it
adds no second mechanism.

* **`.user.ini`** is read by PHP-FPM itself, from the script's folder up to the
  document root, cached for `user_ini.cache_ttl` seconds (PHP's default is
  300). NimVor does nothing at request time. What it does: makes sure the plan
  values are plain `php_value` entries (not `php_admin_value`), because PHP
  never lets a `.user.ini` change an admin value, and lists the file in the
  panel.
* **`php.ini`** is the server's file; PHP-FPM never loads one from a website
  folder. NimVor therefore reads the customer's file (as text, never
  executing it), checks each line, and writes the accepted directives into the
  website's pool file in place of the website-level values. It is the only
  per-website PHP configuration PHP-FPM offers.
* **Detection** (agent, `sites/phpconfig.go`): the agent keeps the applied
  specification of each website (root-only, `<state>/sitespec/`). Every ten
  seconds it compares one cheap signature (modification time, size, inode,
  mode) of the three watched paths per website: no directory scanning. A
  change (created, edited, renamed, deleted) triggers one read, one render of
  the pool, and only when the pool differs: `php-fpm -t`, an atomic write with
  rollback, and a graceful reload of that PHP version's FPM master (or the
  account's own master). Nginx is not touched. An unchanged website costs
  three `lstat` calls.
* **Panel** (`sites/php`, card "PHP configuration files"): shows the files,
  what each directive does, the effective values with their source, the status
  and the time of the last detected change. "Check now" asks the agent to look
  immediately; "Reset website overrides" clears the website-level directives
  set in the panel and never touches the customer's files.

Agent actions: `sites.phpconfig` (read-only) and `sites.phpconfig_scan`
(mutating, behind the write gate).

## Precedence (lowest to highest)

1. The server's PHP configuration (`php.ini` and `conf.d` of the version).
2. NimVor website settings: the plan's limits and the directives set in the panel.
3. `php.ini` of the website folder, then `php.ini` of the document root (nearer wins).
4. `.user.ini` (PHP applies it itself, so it wins, but only for settings PHP lets a `.user.ini` change).

Inside one file the last assignment wins (as in PHP); the earlier lines are
marked *Replaced*. Nothing is silently ignored: every line of both files is
shown with what happened to it (*In effect*, *Locked*, *Server only*, *Not
supported*, *Invalid*, *Replaced*, *Not checked*).

**Locked** settings are written with `php_admin_value` by NimVor and cannot be
changed by either file: `open_basedir`, `upload_tmp_dir`, `sys_temp_dir`,
`session.save_path`, `error_log`, `sendmail_path`, `disable_functions`. An
administrator can also lock the plan's memory, upload, execution-time and
error settings per website ("Lock the memory, upload and time limits" on the
PHP page; `lock_ini`). Settings that are system-level in PHP (`file_uploads`,
`allow_url_fopen`, `allow_url_include`, `expose_php`, `opcache.*`, …) and
extensions cannot be set from a website's files at all.

## Security

* Files are untrusted text. They are never executed or included; `${VAR}`,
  `$`, backslashes, quotes inside values, braces, array keys, `[HOST=]` /
  `[PATH=]` sections and `extension=` are refused.
* Only listed directives, each with a type check (numbers in range, sizes,
  On/Off, enumerations, the time zone). Paths must be inside the website's
  folder (no `..`, no links out).
* The agent runs as root, so it reads customer files carefully: the folder
  must be an ordinary folder inside the website (no symbolic link anywhere in
  it), the file must be a regular file owned by the website's account (or
  root), is opened with `O_NOFOLLOW`, and is at most 64 KiB and 1000 lines.
* Each website's values go only into its own pool; the stored specification
  of one website is never used for another.
* A pool that was edited by hand, or written by an older NimVor, is not
  replaced by the watcher (the status says so); saving the website brings it
  up to date.
* Nginx answers 403 for `php.ini` and for every dot file (`.user.ini`).
* Everything is logged by the agent (`website php.ini applied`, failures with
  the reason) and the panel shows the same message.

## Behaviour and compatibility

* Websites saved before this version have no stored specification yet. Their
  `.user.ini` works at once; for `php.ini`, "Check now" (or any save of the
  website) records the website and from then on changes are picked up on
  their own. Until then the page says "Pending".
* The plan's memory, upload, time and error settings are now plain values: a
  `.user.ini` or `ini_set()` can change them. Set "Lock the memory, upload and
  time limits" to keep the old behaviour. The real ceilings stay: the
  pool's `request_terminate_timeout`, `pm.max_children`, the website's resource
  limits and Nginx's `client_max_body_size`.
* The configuration lives in the pool file, so it survives a PHP-FPM restart,
  a reboot and a PHP version change (the new version's pool is rendered from
  the same files). A change of `php.ini` needs a graceful reload of that PHP
  version's FPM master (PHP-FPM cannot reload a single pool); running requests
  finish, and no pool is restarted.

## Limits imposed by PHP

* PHP-FPM cannot load a per-website `php.ini`; NimVor's pool layer is the
  supported substitute. Directives PHP only reads at start-up (extensions,
  `opcache.*`, …) cannot be set per website.
* A `.user.ini` is applied by PHP, not by NimVor: NimVor cannot veto a line
  in it, only lock the values that matter with admin values. Lines it does not
  know are shown as *Not checked*; PHP applies them only if an extension
  defines them.
* `.user.ini` changes take up to `user_ini.cache_ttl` seconds (default 300).
  Only the document root's `.user.ini` is listed; PHP also reads one in
  subfolders, for scripts in them.
* PHP-FPM keeps the first of a repeated pool entry, so NimVor writes each
  directive once.

## Verified with a real PHP-FPM

`agent/internal/sites/userini_fpm_test.go` starts a real PHP-FPM with the
pools NimVor renders (skipped where none is installed) and reads
`ini_get('memory_limit')`, `upload_max_filesize`, `post_max_size`,
`max_execution_time` and `open_basedir` from the running workers: no custom
file; only `.user.ini` (created, changed, deleted); only `php.ini`; both
files; two websites with different settings; locked limits; a PHP-FPM
restart. `phpinfo()` shows the local value of a `.user.ini` setting different
from the pool's master value, and equal values for locked settings.

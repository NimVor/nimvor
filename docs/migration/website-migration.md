# Migrating websites from another server

*Migrations* (administrators only) copies websites and MySQL/MariaDB databases
from another server into websites and databases that already exist in NimVor.
It reads the source over SSH and **never changes or deletes anything on it**.

This page says what has been built and what has been proven. It does not claim
more than that.

## Status: Phase 1 (read this first)

The full specification is a large product (many panels, email, DNS, SSL,
cut-over). It is being built in phases and **this release is the
first**. A panel is labelled *Fully supported* only after it has been proven
against real servers; today nothing is.

| Source | Level | What is proven |
|---|---|---|
| Custom Linux server (no panel) | **Experimental** | Discovery of Nginx/Apache sites and MySQL/MariaDB databases on synthetic data. File copy and database import proven against a real `sshd` and MariaDB (see "How it was tested"). |
| cPanel / WHM | **Experimental** | Discovery written from cPanel's documented file layout and tested on **synthetic data only**. Not tried on a real cPanel server, and the layout differs between versions. Treat the discovery as a starting point and check it. |
| Plesk (Linux) | **Experimental** | Read from Plesk's own database with read-only queries. Written from the documented schema, tested on synthetic data only. |
| DirectAdmin | **Experimental** | Read from DirectAdmin's files. Written from the documented layout, tested on synthetic data only. |
| FASTPANEL | **Experimental** | Sites read through FASTPANEL's `mogwai sites list` table (found by header names); databases listed for the whole server. Written from the documentation's description of the CLI; the exact output format is unverified. |
| VestaCP, HestiaCP | **Experimental** | Read from the panel's own files (`web.conf`, `db.conf`, `mail/*.conf`). Written from the documented layout, synthetic data only. |
| CyberPanel | **Experimental** | Websites, child domains, databases and mailboxes (`e_users`) from the `cyberpanel` database; a mailbox is listed only if its Maildir exists under `/home/vmail`. Table names unverified on a real server. |
| ISPConfig | **Experimental** | Websites, databases and mailboxes from the `dbispconfig` database. Table names unverified on a real server. |
| NimVor | **Experimental** | Another NimVor server, read from its panel database (`/var/lib/nimvor/nimvor.sqlite`, needs `sqlite3` on the source): websites with their system user and document root, the databases tied to each site (databases without a site are listed separately), mailboxes (`/var/vmail/<domain>/<user>`), aliases, DNS records and cron jobs. FTP accounts are not read. |
| Froxlor | **Experimental** | Customers, domains, databases and mailboxes from Froxlor's database; a mailbox is only listed if its Maildir exists. Table names unverified on a real server. |
| CloudPanel, aaPanel | **Experimental** | Sites from the panel's Nginx/Apache vhost files; databases listed for the whole server. |
| InterWorx | **Experimental** | SiteWorx accounts and domains from the documented `/home/<user>/<domain>/html` folders; databases by `<user>_` prefix. |
| Enhance (limited) | **Experimental** | Only lists `/var/www/<website-id>/public_html` folders, named by website id (domain names are not read), and the server's databases. |
| Virtualmin/Webmin | **Not available** | Listed in the wizard so nobody assumes it works. It cannot be chosen. |

### What it moves

| | This version |
|---|---|
| Website files (rsync over SSH, changed files only, checksum-verified) | ✅ |
| MySQL / MariaDB databases: schema, data, character set, views, triggers, stored procedures, functions, events | ✅ (dump and import; table counts compared) |
| Several websites at once, excludes, compression | ✅ |
| Pause, cancel, continue after a failure or an agent restart | ✅ |
| Synchronise before the switch: copy again only files changed on the source, keeping files that are newer here (files only) | ✅ |
| Synchronising databases | ❌ by design (see "Before you switch DNS") |
| Email messages (Maildir: every folder, flags kept) into mailboxes you already made in NimVor, with the same address | ✅ (rsync, no delete, never into a mailbox that has messages unless overwriting is allowed, checksum-verified, included in sync, rollback and validation) |
| Email forwarders and aliases (plain address-to-address rules; cPanel, Vesta/Hestia, DirectAdmin, ISPConfig, Froxlor, CyberPanel) | ✅ created as NimVor aliases on request; existing aliases and mailboxes are never overwritten |
| Mailbox passwords and quotas | ❌ (set them on the NimVor mailboxes) |
| DNS records of the source's zone files (MX, TXT, CNAME, SRV, CAA) added to the NimVor zones of the same names (zone files are read for cPanel, DirectAdmin, Vesta/Hestia, ISPConfig) | ✅ on request; address records (A, AAAA) are never copied; only NimVor's own zones change |
| FTP accounts (cPanel `/etc/proftpd/<user>`) | ✅ recreated as NimVor FTP accounts inside the matching website, with a **new** password shown once (the old one cannot be read); other panels are not read |
| The site each database belongs to (FASTPANEL, aaPanel, CloudPanel) | ✅ read where the panel can say; otherwise the databases are listed for the whole server and you choose |
| DNS cut-over **check** (read only): where each migrated name points now, as seen by four resolvers, and whether that is this server | ✅ |
| Changing DNS records or nameservers, TTL changes, DNS provider integration | ❌ by design: NimVor never changes DNS for a migration |
| SSL certificates | ❌ |
| FTP accounts, cron jobs (cron jobs are *listed*) | ❌ |
| PHP settings per domain, rewrite rules, application configuration | ❌ (Apache `.htaccess` is not translated to Nginx) |
| PostgreSQL | ❌ |
| Database users and grants | ❌ (NimVor creates the database user for the NimVor database) |
| Creating the destination website/account for you | ❌ (create the NimVor website and database first) |
| Validation pass: document root, created files present and owned by the account, database reachable and table count, HTTP request to the site by name | ✅ |
| Validation of PHP execution and extensions, application configuration, SSL, DNS | ❌ |
| Rollback of destination changes | ✅ files and (on confirmation) databases, never newer data (see "Rollback") |
| Final report | ✅ JSON download and a printable page (print or save as PDF from the browser); no server-generated PDF |
| API connections (WHM API, Plesk REST…) | ❌ (SSH only) |

## Before you start

1. On NimVor, **create the website** that will receive each site (Websites) and
   the **database** that will receive each database (Databases). A migration
   fills existing ones and refuses to start on a website that already has files
   or a database that already has tables, unless you tick *Allow overwriting*.
2. On the source you need **SSH access as root**, or a user that can read every
   website folder and run `mysqldump`/`mysql` on every database you move.
   `rsync` (3.0 or newer, so that folder names with spaces work on old servers) and (for databases) `mysqldump` must be installed on the source.
3. Check that this server has room: the check in step 4 does it, and NimVor
   always keeps 512 MiB free.

## Procedure

1. **Source.** Migrations → New migration → choose the source.
2. **Connect.** Enter host, SSH port and user and choose *Check the server*.
   NimVor asks the server for its **SSH host key** and shows it. Compare it with
   the source (`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`) and only then
   enter the password or a private key. The login is sent only to a server with
   exactly that key.
3. **Discover.** Reads accounts, websites with their folders and sizes,
   databases, mailboxes and cron jobs. Read only.
4. **Choose and map.** Tick websites and databases and pick the NimVor website
   or database each goes into. Set how many websites to copy at once, what to
   leave out, compression, and whether overwriting is allowed.
5. **Check.** A readiness report in three levels: *Critical* (cannot proceed),
   *Warning* (look at it), *Info*. It covers disk space, temporary space, PHP
   versions, database engine and version, the tools on the source, and lists
   what this version does not move. Critical problems disable *Start*.
6. **Migrate.** Runs in the agent, not in your browser: close the page if you
   like. Each website and database is a task with its own status, attempts and
   result. Files are copied first, several at a time, then databases one after
   the other.
7. **Validate.** *Validate* (any time after a run) checks what is on this server:
   the document root exists; every file the copy created is present, and owned by
   the website's account; files that differed from the source when they were
   copied are flagged; each database answers and has the number of tables that was
   imported; and the site is requested **by name from this server's own web
   server**, so it works before DNS is switched. Results are *Pass*, *Look* (a
   warning, for example the site answers HTTP 500, or is not enabled yet) or
   *Fail*. The report lists what this pass does **not** check (PHP execution and
   extensions, application configuration, SSL, DNS).
8. **DNS cut-over.** *Check DNS* reads where each migrated website's name points
   now, as seen by this server's own resolver and by Cloudflare, Google and
   Quad9, and compares it with this server's **public** addresses. A name is
   *Points to this server* only when every resolver that answered returns only
   this server's addresses; *Still spreading* when some do and some do not;
   *Points elsewhere* when none does. It lists advice for each case and reminds
   you to lower the TTL a day before switching (the TTL cannot be read from here).
   **NimVor never changes DNS records or nameservers**: you change them at your DNS
   provider and check again. There is no button and no route that does. Mail (MX)
   is not compared; do not move MX before your mail has been moved.
9. **Report.** *Report (JSON)* lists what was copied, what failed, what differs
   from the source, what is left to do and what was not moved.

### Before you switch DNS

This version does not touch DNS. After the copy: open the site on NimVor (for
example with a hosts-file entry) and check it, then use **Synchronise before
switching DNS** right before the switch.

*Synchronise* asks for the login again (a finished migration no longer holds it;
the SSH key you confirmed stays pinned) and copies again **only the files that
changed on the source**. A file that is **newer on NimVor** (edited since the
migration) is **kept**, not replaced (`rsync --update`), and nothing is deleted.
Files it adds are recorded, so a rollback still removes them.

**Databases are not synchronised.** A dump can only be imported into an empty
database, so a second import would drop what was written on NimVor since. If a
site's database changes between the copy and the switch, plan a short freeze:
stop writes on the source, move the database again into a new empty database by
a new migration, and switch. This limit is deliberate: a "sync" that silently
replaced newer data would break the rule that data is never overwritten.

Then change DNS yourself (see *DNS cut-over* below).

## Permissions and what runs where

* The panel pages need the `server.manage` permission and an administrator
  scope. Customers cannot see or call any of it.
* Everything privileged is done by the agent: SSH, rsync, `mysqldump`, the
  import (the agent's own database client, with the client's shell commands
  turned off), and writing into websites. The panel never runs a command.
* `ssh` and `rsync` were added to the agent's command allowlist for this.

## Security

* **Host key pinning.** `StrictHostKeyChecking=yes` against a private
  `known_hosts` holding only the key you confirmed. A server with another key is
  refused.
* **The login.** A key goes to a `0600` file; a password goes to ssh through a
  one-shot `SSH_ASKPASS` helper that prints it from a `0600` file. Neither is on
  a command line. The folder is overwritten with zeros and removed after every
  command. In the panel the login is encrypted (`Crypto`), kept only while the
  migration is unfinished, never shown again and never put in the audit log or
  the agent's state. It is erased when the migration completes or is cancelled,
  or with *Erase the stored login*. The agent's state files hold no credential:
  continuing a stopped migration sends the login again.
* **No free-form remote commands.** Only the agent builds commands for the
  source, from fixed text and quoted, validated values (paths, database names,
  account names). Multi-line scripts are sent base64-encoded because the agent's
  runner refuses newlines in arguments.
* **Destinations are confined.** A destination must be an existing website
  folder under the sites root, not a symlink, owned by the account named in the
  plan. Files are given to that account (`--chown`); symlinks pointing outside
  the site are not copied (`--safe-links`).
* **Never deletes.** No `--delete`, no removal on the source. A migration only
  fills what is empty: it refuses a website that has files other than NimVor's
  placeholder page, and a database that has tables, unless *Allow overwriting*
  is ticked (the start button then says so).
* **Disk.** Each task refuses to start if the disk would fall below the
  512 MiB reserve; dumps are checked to be complete before they are imported.
* **Audit.** Creating, connecting, discovering, starting, pausing, resuming,
  cancelling, forgetting the login and deleting are recorded without any
  credential.

## Reliability

State is a file per migration in the agent's state folder (written atomically,
mode `0600`). A task is identified by what it fills, so a finished task is never
repeated. A failed task is retried up to three times with a pause; an
interrupted rsync resumes (`--partial`); a finished dump is reused after a
restart if it is complete. When the agent restarts, running migrations become
*Interrupted* and can be continued (give the login again; the panel does this
from its encrypted copy). A migration locks its destinations: no other
unfinished migration may use the same website or database. A failed migration
lists what remains ("What is left to do").

## Rollback

*Roll back* (on a completed, failed, cancelled, paused or interrupted migration)
undoes what the migration did **in this server**, and nothing else. It needs no
login and never touches the source.

* **Files.** For every copy the agent records the files and folders it created,
  with the size and modification time the copy left. A rollback removes a file
  only if it is **still exactly that**. A file you edited, replaced (even with a
  link) or that is gone is left alone and listed under "Kept". Files you added
  are never in the record. A created folder is removed only when it is empty.
* **Databases** are left as they are unless you tick *Also empty the databases it
  filled*. Even then a database is emptied only if it had **no table before the
  import** (otherwise your tables cannot be told from the imported ones) and **no
  table was written to after the import**; otherwise it is kept and reported.
  Emptying drops and recreates the database with the same character set and
  collation; its grants stay.
* **Overwritten files cannot be restored.** They exist only when *Allow
  overwriting* was ticked; no copy of them was kept, and the report says how many.
* A rollback asks for confirmation, is recorded in the audit log and is final: a
  rolled back migration cannot be continued, but its destinations are free for a
  new migration.

Not available: restoring overwritten files, rolling back DNS/SSL (they are
not moved), and snapshots taken before the migration. Nothing on the source is
ever changed, so there is nothing to roll back there.

## Email

Create each mailbox under **Email** first, then tick it in the wizard: its messages are
copied into the NimVor mailbox with the same address. The copy is a pull with `rsync`
(never deletes), owned by the `vmail` account, leaves out Dovecot's own caches (they are
rebuilt) and is checked afterwards. A mailbox that already has messages is refused
unless overwriting is allowed. Reading or flagging a message in Maildir renames it, so a
rollback keeps messages that were read since and any that arrived later; validation
reports them as changed, not lost. Passwords and quotas are not
copied. Forwarders (cPanel `/etc/valiases`) are listed under the selection step and, when you
tick them, created straight away as NimVor aliases; rules that pipe to a program, bounce or
discard are not read. Do not move MX records before the mail has been moved and synchronised.

## Plesk (Linux, experimental)

Choose **Plesk (Linux)** as the source and log in as root. NimVor reads Plesk's own database (`plesk db`
with read-only SELECT queries) for subscriptions and their domains, document roots, subdomains, MySQL/MariaDB
databases and mailboxes, then continues exactly as for any source: files, databases and mail are copied
into the NimVor websites, databases and mailboxes you chose. A Plesk subscription is one account. Not
read from Plesk: PHP versions per domain, forwarders, DNS zones, FTP and cron. This adapter was written from
Plesk's documented database layout and has only run on synthetic data, so test it on a non-critical
subscription first.

## DirectAdmin (experimental)

Choose **DirectAdmin** as the source and log in as root. NimVor reads DirectAdmin's own files: the users
under `/usr/local/directadmin/data/users`, each user's `domains.list` (the first domain is the main one),
`domains/<domain>.subdomains`, the mailboxes in `/etc/virtual/<domain>/passwd` (Maildirs under
`/home/<user>/imap/`), and databases by DirectAdmin's `<user>_<name>` naming. Files, databases and mail
then migrate like any other source. Not read: PHP versions per domain, forwarders, DNS zones, FTP and
cron. Written from DirectAdmin's documented layout and run on synthetic data only: test it on a
non-critical user first.

## FASTPANEL (experimental)

Choose **FASTPANEL** as the source and log in as root. NimVor runs `mogwai sites list` on the source and
reads the SERVER_NAME, OWNER and DOCUMENT_ROOT columns by their header names (a plain aligned table or a
`|`/`│` drawn one). Each site owner is an account and each site's document root is copied, usually
`/var/www/<owner>/data/www/<domain>`. Databases are shown as one list for the whole server under the
pseudo-account "databases", and you choose each destination yourself, because FASTPANEL's
database-to-site link is not read. Mailboxes, forwarders, DNS, FTP and cron are not read. If the table
cannot be recognised the wizard says so instead of guessing. FASTPANEL's own `transfer` tool is the
better choice between two FASTPANEL servers; this reader is for moving a FASTPANEL server's sites
into NimVor.

## More panels (experimental)

VestaCP, HestiaCP, CyberPanel, ISPConfig, Froxlor, CloudPanel, aaPanel and InterWorx are read-only
adapters in the same style as Plesk and DirectAdmin: you log in as root, NimVor reads the panel's own files
or database (read-only SELECTs through the mysql client where the panel keeps its accounts in a database),
and everything then migrates like any other source. What each reads is listed in the table above; none
reads forwarders, DNS, FTP or cron. When a query fails because a table or column has another name in your
version, the discovery says so in its warnings instead of guessing. **All of them were written from each
project's documentation or source layout and have only run on synthetic data**, so try each on a
non-critical account first and send the output of any failing step so the reader can be fixed.

## Creating the destination databases

For each source database you can pick an existing empty NimVor database, or **Create a new NimVor database
and user**. Nothing is created when you choose it. When you press **Start**, NimVor creates the database
(named like the source, with a number added if that name is taken) and a user with full access to it that
has the same name, both linked to the NimVor website that receives that account's files (when exactly one
website of the account was chosen). The new database and user credentials are shown **once**, on the page
after Start, and are not stored. The source application's own configuration (for example `wp-config.php`)
still holds the old database name, user and password, so update it to the new values after the copy. A
database created this way is not removed by a rollback; delete it under Databases if you do not want it.

## Known limitations

* MySQL 8 → MariaDB: databases using `utf8mb4_0900_*` collations or MySQL-8-only
  features fail to import; the check warns when it sees this combination.
* The database dump needs the SSH user to run `mysqldump`/`mysql` without a
  password prompt (root over the socket, or a `~/.my.cnf`).
* A site that changes while it is copied shows *N files differ*; run it again.
* Sizes use `du`; databases listed under a cPanel account are those named
  `<account>_…`.
* Files in dumps are not size-limited; very large databases need the disk
  space the check reports.

## Troubleshooting

| Message | Meaning / fix |
|---|---|
| "the source server's SSH host key must be confirmed first" | Choose *Check the server* and confirm the key. |
| `Permission denied` | Wrong password/key, or the user may not log in over SSH (`PermitRootLogin`). |
| "already has files" / "already has N table(s)" | The destination is not empty. Use an empty website/database, or tick *Allow overwriting* if it may be overwritten. |
| "create it in NimVor first" | The database does not exist on this server. |
| "rsync is not installed on the source" | Install `rsync` on the source and discover again. |
| "the database … could not be dumped" | The SSH user cannot run `mysqldump` (see limitations). |
| "disk … keeps … free" | Free space, or choose a temporary folder on a bigger disk. |

## How it was tested

Unit tests (synthetic data) cover panel detection, the cPanel and custom-Linux
discovery, the assessment, plan validation, the SSH session's secrets handling,
retries, resume, pause, cancel, agent restart, refusal to overwrite, incomplete
and truncated dumps and table-count mismatches. **Real-host tests** (CI's
`realhost` jobs) run the transport against a real `sshd` and MariaDB: password
login through askpass, key login, a wrong password, a wrong pinned key, an rsync
pull with exclusions and a symlink that must not be followed, ownership,
continuing after a change, and a database with utf8mb4 data, a view and a
stored procedure; and rolling back a real copy and a real database (a changed file and a table written to later are kept). The panel's HTTP tests check access control, key
confirmation, that the login is encrypted and erased, and the plan the agent
receives. **Not tested:** a real cPanel (or any other panel) server, large
sites and databases, and a browser walkthrough of the wizard against a live
source.

## API

Agent actions (all under `/v1/actions/<name>`; the ones that change anything are
behind the write gate):

| Action | Purpose |
|---|---|
| `migrate.panels` | Supported sources and their level |
| `migrate.host_key` | The source's SSH host key to confirm (no login sent) |
| `migrate.test` | Connect and read the server's facts and panel (read only) |
| `migrate.discover` | Discovery as a job; its result is the discovery |
| `migrate.assess` | Compatibility findings for a plan; contacts no server |
| `migrate.create` | Store a validated plan |
| `migrate.start` / `migrate.resume` | Run / continue (job); the login is given for that run |
| `migrate.pause` / `migrate.cancel` | Stop at the next safe point / cancel |
| `migrate.cutover_check` | Read DNS for the migrated names (this server's resolver, Cloudflare, Google, Quad9); changes nothing |
| `migrate.validate` | Check what the migration left here (reads only; contacts no source) |
| `migrate.sync` | Copy again only changed files (job); keeps files newer here; databases excluded |
| `migrate.rollback` | Remove what the migration created (confirmation required); databases only when asked |
| `migrate.get` / `migrate.list` | State of one / all migrations |

Panel routes are under `/migrations` (see `routes/web.php`).

## Adding a source panel

A source is an `Adapter` in `agent/internal/migrate`:

```go
type Adapter interface {
    ID() string
    Name() string
    Support() Support // must stay Experimental until proven on real servers
    Detect(ctx context.Context, t Transport) (Detection, bool, error)
    Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error)
}
```

1. Write the adapter (`linux.go` and `cpanel.go` are examples). It only calls
   `t.Exec` with fixed text; quote every dynamic value with `Shq`, and validate
   paths with `ValidRemotePath` and names with a strict regexp.
2. Register it in `adapters()` (specific panels before the generic one).
3. Test it with a fake `Transport` and synthetic output of the real panel's
   files, and write a real-host test when a real panel can be run.
4. Keep it `Experimental` until its discovery and migration have run against real
   servers, then say so in this page's table.

The engine, assessment, state, retries and security do not change: they work from
the `Discovery` an adapter returns.

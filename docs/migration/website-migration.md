# Migrating websites from another server

*Migrations* (administrators only) copies websites and MySQL/MariaDB databases
from another server into websites and databases that already exist in NimVor.
It reads the source over SSH and **never changes or deletes anything on it**.

This page says what has been built and what has been proven. It does not claim
more than that.

## Status: Phase 1 (read this first)

The full specification is a large product (many panels, email, DNS, SSL,
cut-over, rollback). It is being built in phases and **this release is the
first**. A panel is labelled *Fully supported* only after it has been proven
against real servers; today nothing is.

| Source | Level | What is proven |
|---|---|---|
| Custom Linux server (no panel) | **Experimental** | Discovery of Nginx/Apache sites and MySQL/MariaDB databases on synthetic data. File copy and database import proven against a real `sshd` and MariaDB (see "How it was tested"). |
| cPanel / WHM | **Experimental** | Discovery written from cPanel's documented file layout and tested on **synthetic data only**. Not tried on a real cPanel server, and the layout differs between versions. Treat the discovery as a starting point and check it. |
| Plesk, CyberPanel, DirectAdmin, Virtualmin/Webmin, HestiaCP, aaPanel, ISPConfig, Enhance, InterWorx, CloudPanel, VestaCP, Froxlor | **Not available** | Listed in the wizard so nobody assumes they work. They cannot be chosen. |

### What it moves

| | This version |
|---|---|
| Website files (rsync over SSH, changed files only, checksum-verified) | ✅ |
| MySQL / MariaDB databases: schema, data, character set, views, triggers, stored procedures, functions, events | ✅ (dump and import; table counts compared) |
| Several websites at once, excludes, compression | ✅ |
| Pause, cancel, continue after a failure or an agent restart | ✅ |
| Email accounts, messages, aliases, forwarders | ❌ (mailbox names are *listed* from cPanel) |
| DNS zones and records, DNS cut-over assistant | ❌ |
| SSL certificates | ❌ |
| FTP accounts, cron jobs (cron jobs are *listed*) | ❌ |
| PHP settings per domain, rewrite rules, application configuration | ❌ (Apache `.htaccess` is not translated to Nginx) |
| PostgreSQL | ❌ |
| Database users and grants | ❌ (NimVor creates the database user for the NimVor database) |
| Creating the destination website/account for you | ❌ (create the NimVor website and database first) |
| Validation: HTTP, PHP, SSL, DNS checks | ❌ (files by checksum, databases by table count only) |
| Rollback of destination changes | ❌ (see "Rollback") |
| Final report as PDF | ❌ (JSON only) |
| API connections (WHM API, Plesk REST…) | ❌ (SSH only) |

## Before you start

1. On NimVor, **create the website** that will receive each site (Websites) and
   the **database** that will receive each database (Databases). A migration
   fills existing ones and refuses to start on a website that already has files
   or a database that already has tables, unless you tick *Allow overwriting*.
2. On the source you need **SSH access as root**, or a user that can read every
   website folder and run `mysqldump`/`mysql` on every database you move.
   `rsync` and (for databases) `mysqldump` must be installed on the source.
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
7. **Report.** *Report (JSON)* lists what was copied, what failed, what differs
   from the source, what is left to do and what was not moved.

### Before you switch DNS

This version does not touch DNS. After the copy: open the site on NimVor (for
example with a hosts-file entry), check pages, forms and logins, and run the
migration again to bring over files that changed on the source meanwhile (only
changed files are copied). Then change DNS yourself.

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

**There is no automatic rollback in this version.** Because a migration only
fills empty destinations, undoing it means deleting the NimVor website's files
or database (or the website itself) and starting again. Cancelling stops the
migration and keeps what was copied; it removes nothing. Nothing on the source
is ever changed. Rollback with snapshots and restoration metadata is planned.

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
stored procedure. The panel's HTTP tests check access control, key
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

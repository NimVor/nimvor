# Package management and component installation

How the NimVor agent installs packages and components on Ubuntu/Debian
(APT) and AlmaLinux/Rocky Linux/RHEL (DNF), what it does when that fails,
and why it works this way.

## The failures this design fixes

| Platform | Symptom | Root cause |
|---|---|---|
| Ubuntu 24.04 | `E: seteuid 42 failed - seteuid (1: Operation not permitted)`, `Method http has died`, `Sub-process http returned an error code (112)` | APT downloads as its `_apt` user. On systemd 255 the agent's process starts without `CAP_SETUID` in its permitted capability set, and with `NoNewPrivileges=yes` it can never regain it (the same mechanism as F-16). `apt-get` ran as a direct child of the agent and inherited that. Ubuntu 22.04 (systemd 249) keeps the capability, so it worked there. Reproduced with `setpriv --no-new-privs --bounding-set=-setuid apt-get update`: identical output. |
| Rocky Linux 9 (SELinux enforcing) | `Error in PREIN scriptlet in rpm package dovecot`, the same for `redis`, Remi's PHP; `sudo dnf install dovecot` works | RPM runs every `%pre`/`%post` shell scriptlet in the `rpm_script_t` SELinux domain through an explicit exec context. Under `NoNewPrivileges=yes` the kernel refuses such an explicit transition (`security/selinux/hooks.c`, `check_nnp_nosuid`), so the scriptlet never starts. Packages without shell scriptlets installed fine, which hid the pattern. |
| Rocky Linux 9 | PHP 8.0, 8.1, 8.2 and EPEL packages fail | The agent assumed EPEL, CRB and Remi were configured. The installer only enabled EPEL and Remi when its own dependencies were missing, and never CRB. CI's VM setup enabled all three by hand, so CI never saw it. |
| All | Two installs at once fail or corrupt each other | `max_jobs` is 2 and PHP, ModSecurity, quota and webmail jobs use other job targets than `packages.install`, so two package transactions could run at once. APT fails at once on a held lock. |
| All | `dnf install failed; see the log above` | Every failure was reported the same way; the package manager's own message was only in the job log. |
| All | A component "installed" that does not run | Success meant "the package manager exited 0". |

All three platform failures have one cause: package managers ran inside the
agent's systemd sandbox, which is built for the agent, not for package
maintainer scripts. CI did not catch it because no test installed a package
through the *installed* agent: every package was installed by the VM setup
script before NimVor.

## Architecture

```
Panel ──(UNIX socket / TLS)──▶ Agent (NoNewPrivileges, narrow capabilities)
                                 │
                                 │ pkgs.InstallNative / RemoveNative / Refresh / UpgradeAll
                                 │   1. availability check (apt-cache policy / dnf list)
                                 │   2. RHEL: prepare repositories for what is missing
                                 │   3. transact(): run, diagnose, recover, retry (≤ 3)
                                 ▼
                     execx.RunWith ── PackageTransaction(id, args)?
                                 │ yes: one at a time (package slot, also across restarts)
                                 ▼
               systemd-run (PID 1): nimvor-pkg-<id>.service, as root,
               the environment of `sudo apt-get` / dnf-automatic.service
                                 ▼
                           apt-get / dpkg / dnf / rpm
                                 ▼
               sysmgr.verifyComponents: start the service, check it works
```

### Privileged package path (`agent/internal/execx/packages.go`)

`PackageTransaction(id, args)` decides which commands are package
transactions:

- `apt-get`: anything except simulations (`-s`), so updates, installs,
  removals and source downloads;
- `dpkg`: `-i`, `--configure`, `-r`, `-P`, `--unpack`;
- `dnf`: `install`, `remove`, `upgrade`, `reinstall`, `downgrade`, `module`,
  `makecache`, `download`, etc. Not `list`, `repoquery`, `repolist`,
  `--assumeno` or `--cacheonly`;
- `rpm`: installs, erases (without `--test`), `--rebuilddb`, `--import`.
  Not queries.

Those run in a transient unit started by PID 1 (`nimvor-pkg-*.service`),
as root, with the default capabilities and without `NoNewPrivileges`: the
environment an administrator's `sudo apt-get` or RHEL's own
`dnf-automatic.service` has. Output comes back through the same named-pipe
mechanism as commands run as website accounts (F-16, F-19).

**Security trade-off (deliberate):** this is a narrow exception to the
agent's sandbox. Only the four allowlisted package managers take this path,
only for transaction verbs, and only with arguments the callers have already
validated (package names matching `validate.Package`, fixed repository URLs).
The agent itself keeps `NoNewPrivileges=yes` and its capability set. A
package manager runs the signed packages' maintainer scripts as root in any
case: confining it protects nothing, it only breaks installs. NimVor never
disables SELinux, AppArmor or signature checks, and never uses `--nogpgcheck`,
`--allow-unauthenticated`, `--force-all` or `--nodeps`.

Guarantees:

- **One transaction at a time.** A process-wide slot serialises every package
  transaction, whichever job starts it. Units of an earlier agent instance
  (`nimvor-pkg-*`) are waited for, with backoff and a progress line in the
  job log. APT is also told to wait for the dpkg lock
  (`DPkg::Lock::Timeout`); DNF waits for its own lock.
- **Never stopped half-way.** If the agent stops waiting (job timeout,
  shutdown, restart), the unit finishes on its own and its output is still
  drained. A broken dpkg or RPM state is worse than a late one.
- **No surprise restarts.** `needrestart` only lists services
  (`NEEDRESTART_MODE=l`).

Without systemd (development containers) the command runs directly; the
agent is not sandboxed there.

### Diagnosis and recovery (`agent/internal/pkgs/diagnose.go`, `transact.go`)

Every failed run is classified from the package manager's output:

| Category | Recognised by | Automatic recovery |
|---|---|---|
| `PRIVILEGE_TRANSITION` | `seteuid`, `setresuid`, `Failed to set new user ids` | none (fixed by the path above; the hint says to update) |
| `LOCKED` | `Could not get lock`, `Waiting for process with pid` | wait, try again |
| `REPOSITORY` | `Failed to fetch`, `Failed to download metadata`, `Curl error`, `Method http has died`, … | wait, refresh the index, try again |
| `INTERRUPTED` | `dpkg was interrupted` | `dpkg --configure -a`, try again (once) |
| `RPM_DATABASE` | `rpmdb open failed`, `DB_RUNRECOVERY`, … | `rpm --rebuilddb`, try again (once) |
| `PACKAGE_SCRIPT` | `Error in PREIN scriptlet in rpm package X`, `%prein(X) scriptlet failed, exit status N`, `X package post-installation script subprocess returned error exit status N` | none; package, stage and exit status reported. Retried only when the script says a lock was busy ("try again later") |
| `NOT_AVAILABLE` | `Unable to locate package`, `No match for argument` (or the pre-check) | RHEL: prepare repositories, re-check |
| `DEPENDENCY` | `Unmet dependencies`, `nothing provides`, `conflicting requests` | RHEL, `nothing provides`: enable EPEL and CRB (EPEL's packages need CRB's libraries, e.g. opendkim needs libmilter), try again (once). Otherwise none; `dnf check` / `apt-get check` output added, and the configured repositories that provide each missing library are named (read-only `dnf repoquery --enablerepo=*`) |
| `SIGNATURE` | `NO_PUBKEY`, `GPG check FAILED` | none (never bypassed) |
| `DISK_FULL` | `No space left on device`, … | none |

At most three attempts, with backoff (10 s, then 30 s). Permanent errors are
never retried. The job log records every attempt, every recovery step, the
diagnosis, the relevant output lines and what to do. The job's error is one
specific sentence, for example:

```
install dovecot failed: dovecot: its prein script failed (exit status 1) [PACKAGE_SCRIPT].
The package's script output above shows why; on SELinux systems also check: ausearch -m AVC -ts recent.
```

### Repositories on RHEL

Before installing, the agent checks which packages the enabled repositories
offer (`dnf list --available --installed`; `apt-cache policy` on Debian).
Only when something is missing does it prepare repositories, and only these:

- PHP versions (`phpNN-php-*`): Remi's repository (`remi-release`, from
  `rpms.remirepo.net`), and `remi-safe` enabled;
- Rspamd: the Rspamd project's repository
  (`https://rspamd.com/rpm-stable/centos-N/rspamd.repo`, signed; EPEL does
  not carry it for EL9 and later);
- anything else: EPEL (`epel-release`; on RHEL itself the Fedora project's
  `epel-release-latest-N`) and CRB, which EPEL's packages need
  (`dnf config-manager --set-enabled crb`; on RHEL a note to enable
  CodeReady Builder with `subscription-manager`).

Then it refreshes the index and checks again. A package that is still
missing is reported as `NOT_AVAILABLE`, naming it and the enabled
repositories. For example, PHP 8.0 on a server whose repositories do not
carry it says exactly that, not "installation failed".

### Health checks (`agent/internal/sysmgr/health.go`)

A component counts as installed only when it works:

| Component | Service started | Check |
|---|---|---|
| nginx | `nginx.service` | `nginx -t` |
| redis-server | `redis-server.service` / `redis.service` / `valkey.service` | `redis-cli ping` → `PONG` (or `NOAUTH`) |
| mariadb-server | `mariadb.service` | `mysqladmin ping` |
| memcached | `memcached.service` | service active |
| postfix | (started when email is set up) | `postfix check` |
| dovecot | (started when email is set up) | `doveconf -n` |
| bind9 | (started when DNS is set up) | `named-checkconf` |
| PHP versions | (PHP page) | `phpX.Y -v`, `php-fpm -t` |

When a service does not start, its last 100 journal lines are collected and
the cause classified (`PORT_CONFLICT`, `SELINUX_DENIAL`, `PERMISSION_ERROR`,
`USER_GROUP_ERROR`, `MISSING_FILE`, `CONFIGURATION_ERROR`). It is not
restarted again and again.

### Repairing broken package state (`agent/internal/pkgs/repair.go`)

A transaction that failed or was interrupted can leave packages that are
installed but not set up. `packages.health` finds them, without changing
anything:

| Problem | Found by | Repair |
|---|---|---|
| An install script failed (RPM) | `Error in POSTIN … scriptlet in rpm package X` in `/var/log/dnf.rpm.log*`, for packages not installed again since (install time ≤ failure) | `dnf reinstall X`, once per failure |
| Package not fully installed (dpkg) | `dpkg --audit` | `dpkg --configure -a` |
| Missing dependency | `dnf check --dependencies`, `apt-get -s check` | installs it (the exact version an `=` dependency names); on Debian `apt-get install -f` only when its simulation removes nothing |
| Duplicate versions (RPM) | `dnf check --duplicates` | `dnf remove --duplicates`, **only** on the administrator's confirmation (it removes packages) |

`packages.repair` runs the repair as a job; Server → Packages shows the
problems with **Repair** and, for duplicates, **Remove duplicates** (its own
confirmation). When the agent starts (after an installation or update of
NimVor), it checks by itself and starts the repair job when something can be
repaired without asking. The same set of problems starts it only once, and a
package whose script fails again after its reinstall is reported ("repair
tried") instead of being reinstalled at every start
(`/var/lib/nimvor-agent/pkgs-repair.json`). Removal scripts that failed
(PREUN/POSTUN) are not repaired by installing again and are not reported.

### Idempotency and interruption

- Installing an installed package is a no-op for APT and DNF; the health
  check runs again. Configuration files are kept (`--force-confold`; RPM
  writes `.rpmnew`).
- A job interrupted by an agent restart is reported as interrupted. The
  package transaction it started finishes in its own unit. Running the
  action again verifies the result.
- The installer detects an unfinished earlier run and continues it with
  `--resume` (or `--reinstall`), keeping configuration, token and database.

## Installer self-healing (`installer/install.sh`)

- Prints the detected environment: family, architecture, package manager,
  init system, SELinux mode, firewall.
- Go and Composer are found in their usual places, not only in sudo's `PATH`
  (`/usr/local/go/bin/go`, `/usr/lib/golang/bin/go`, `/usr/lib/go-*/bin/go`;
  `/usr/local/bin/composer`, `/usr/bin/composer`, `~/.local/bin/composer`).
  Each is reported with its version and path. With `--install-deps` they are
  installed from the distribution when missing.
- A `dist/nimvor-agent` older than the agent's sources (after `git pull`) is
  rebuilt. When Go is missing the installer stops and says so (use
  `--install-deps`); it no longer installs the old build. The agent's version
  names the commit it was built from (`1.0.0-rc1+abc1234`).
- On SELinux systems the installer loads a small policy module
  (`installer/selinux/nimvor_postfix_sockets.cil`): systemd may create the
  greylisting and mail-limit sockets in Postfix's private folder. That is
  all it allows. SELinux stays enforcing.
- APT waits for the dpkg lock instead of failing.
- Rocky/Alma: CRB is enabled together with EPEL.

## Manual checks

```bash
# Package transactions run outside the agent's sandbox:
journalctl -o cat -u 'nimvor-pkg-*' --since -1h
systemctl list-units 'nimvor-pkg-*'        # one at a time

# The agent's own sandbox is unchanged:
PID=$(systemctl show -p MainPID --value nimvor-agent)
grep -E 'NoNewPrivs|CapEff' /proc/$PID/status   # NoNewPrivs: 1

# A component's job log (Server → Packages → queue window), or:
cat /var/lib/nimvor-agent/jobs/<job id>.log

# SELinux stays enforcing; denials, if any:
getenforce; ausearch -m AVC -ts recent
```

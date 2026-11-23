# Changing the SSH port

Security → SSH can move the SSH server to another port. A wrong port or a
closed firewall means no one can log in, so the change is applied in a fixed
order and **is only reported as applied after SSH has answered on the new
port**. Anything that fails undoes everything before it.

## What happens, in order

1. **Refusals before anything is touched.**
   - the port is already listening for another program;
   - UFW and firewalld are not in use, but a plain nftables or iptables
     ruleset drops incoming connections by default and has no rule for the
     new port. NimVor does not manage that firewall, so it says so and asks
     you to allow the port there first. (NimVor's own tables are ignored:
     they accept by default.)
2. **The firewall is opened first.** UFW or firewalld get the rule, and the
   firewall's own list is read again: a rule that is not in it stops the
   change. UFW rules keep the action of the old SSH rule (`allow` or
   `limit`). The old port stays open for now: an undo needs it.
3. SELinux gets the port label (RHEL family).
4. The sshd drop-in `/etc/ssh/sshd_config.d/00-nimvor.conf` is written, checked
   with `sshd -t`, and `sshd -T` must show the wanted values (another file
   that sets them earlier is detected).
5. **The daemon is told.**
   - *Plain service:* `systemctl reload`. If SSH does not answer afterwards,
     the service is restarted once.
   - *Socket activation* (`ssh.socket`, Ubuntu 22.10 and later): `Port` in
     `sshd_config` is **ignored** there, systemd listens. NimVor writes
     `/etc/systemd/system/ssh.socket.d/nimvor-port.conf` with one listener
     per address family (`0.0.0.0:PORT`, and `[::]:PORT` with
     `BindIPv6Only=ipv6-only` when the kernel has IPv6), reloads systemd and
     restarts the socket (open sessions stay). A socket that is enabled but
     stopped gets the same file without a restart, so the next boot comes up
     on the same port.
6. **Verification.** The new port must be a listening socket
   (`/proc/net/tcp`, `tcp6`) and an SSH greeting (`SSH-2.0-…`) must arrive
   from `127.0.0.1`, `::1` (when IPv6 exists) and each of the server's own
   global addresses. Nothing else counts: not the firewall rule, not
   `sshd -T`, not `systemctl show ssh.socket`.
7. **Brute force protection follows the port.** fail2ban's sshd jail bans on the
   jail's own port (a rule `tcp dport 22 … reject`), so after a move the new port
   would be unprotected and an address banned on 22 would still be refused
   there. The jail file NimVor manages gets `port = <ports SSH really listens on>`
   (checked with `fail2ban-client --test`, then reloaded). This is best effort:
   if it fails the SSH change still stands. A jail you defined in your own file is not
   touched. An unconfirmed change puts the old jail port back too.
8. Only then the change is handed to the confirmation window. When you press
   **Keep**, the firewall rules that opened exactly the old port from anywhere
   (UFW `allow`/`limit`, firewalld port or the `ssh` service for port 22) are
   removed, unless SSH still listens on that port. Rules limited to an address,
   `deny` rules and rules for other ports are never touched. It is best effort and
   logged: the change stays whatever happens. Unconfirmed, nothing is removed and
   the new port's rule goes instead.

If step 4, 5 or 6 fails, the drop-in and the socket file are put back, systemd
is reloaded, the socket and service are restarted or reloaded again, and the
firewall rule is removed. If the old port does not answer after that either,
the message says to use the provider's console.

Every listed step but the last two is undone on failure; the jail file is part of the
unconfirmed-change rollback.

## What the server cannot prove

The agent connects to the server **from the server**. Those packets enter on
the loopback interface, which almost every firewall accepts, so this check
cannot see a firewall in front of the server (the hosting provider's panel,
a security group) or a packet filter applied by something other than UFW,
firewalld, nftables or iptables on this machine. That is why the change still
waits for confirmation: **open a new SSH connection from your own computer to
the new port, and only then press Keep.** Unconfirmed after two minutes, the
change is undone, firewall included.

## Why "success" used to be possible with "Connection refused"

Reproduced with OpenSSH 9.6 on Ubuntu 24.04 (a socket handed to sshd the way
`ssh.socket` does it; `ss` then shows the same two owners as in the report, the
sshd process and its parent, both with the listening descriptor):

| Finding | Evidence |
|---|---|
| **Confirmed on the reported server.** Ubuntu's own `ssh.socket` lists `ListenStream=0.0.0.0:22`, `ListenStream=[::]:22` and `BindIPv6Only=ipv6-only`. The old NimVor drop-in replaced the two lines with a single `ListenStream=2020` but kept the unit's `BindIPv6Only=ipv6-only`, so systemd bound an IPv6-only socket: `ss` showed only `[::]:2020` and IPv4 clients were refused. | `systemctl cat ssh.socket` and `ss -ltnp` from the server. The `bindv6only` sysctl was `0` and is overridden by the unit. |
| Under socket activation `Port` in `sshd_config` is ignored, and so is a changed `Port` after `SIGHUP` (what `systemctl reload` sends): sshd keeps the socket it was given. | socket on 2021, config changed to `Port 2300`, `kill -HUP`: 2021 still answers, 2300 never listens. |
| `sshd -T` (the check the old code used to confirm "the settings take effect") prints the **configured** port, so it said 2300 although nothing listened there. | same test; `sshd -T` is read from the files, not from sockets. |
| The old code verified neither sockets nor connections: after `systemctl reload` it reported success. | `ssh.go` `ConfigureSSH`, last step. |
| The old socket file had one `ListenStream=PORT`: IPv4 and IPv6 depended on the kernel default `net.ipv6.bindv6only`. With `1`, `ss` shows only `[::]:PORT` (as in the report) and IPv4 clients are refused. | `ss` cannot tell a dual-stack from a v6-only socket; each family is now listed explicitly. |
| nftables or iptables without UFW/firewalld were never looked at: the port was not opened and nothing said so. The agent could not read those rulesets at all (`nft` and `iptables` were not allowed to list rules). | `fw.Backend`, `execx` allow-list. |
| UFW `limit` rules answer further connections of an address with a *reject* (ICMP port unreachable) once six connections arrive within 30 seconds: clients print "Connection refused" although the port listens. | UFW's `ufw-user-limit` chain; an administrator retrying a failing login hits it quickly. |

The first row was confirmed on the reported server and the next two were reproduced; the others come from reading the code
and from the documented behavior of UFW and the kernel (this test machine has
no IPv6, so the v6-only case could not be run).

The first row is what refused the connection on the reported server. The commands
that showed it, for other servers:

```bash
systemctl cat ssh.socket                       # ListenStream lines and drop-ins
sysctl net.ipv6.bindv6only
ss -ltnp | grep -E 'sshd|systemd'
ufw status verbose; nft list ruleset | grep -B2 -A8 'hook input'
iptables -S INPUT; ip6tables -S INPUT
journalctl -u ssh -u ssh.socket --since -1h
```

A refusal that arrives immediately (RST) with a listening port points at a
reject rule or a v4/v6 mismatch; a timeout points at a dropping firewall.

## Where the code is

| Concern | File / function |
|---|---|
| Whole flow, order and rollback | `agent/internal/security/ssh.go` `ConfigureSSH` |
| Socket drop-in, restart, listing check | `ssh.go` `applySSHSocket`, `sshport.go` `socketDropIn` |
| Mode detection (active/enabled socket, IPv6) | `sshport.go` `detectSSHMode`, `ipv6Usable` |
| Listening sockets and greeting probe | `sshport.go` `readListeners`, `SSHProbe`, `verifyListening` |
| Unmanaged nftables / iptables | `sshport.go` `unmanagedFilterBlocks`, `nftBlocks`, `iptablesBlocks` |
| Firewall rule really present | `sshport.go` `firewallOpens` |
| fail2ban sshd jail follows the port | `fail2ban.go` `syncFail2banSSHPort`, `withSSHJailPort` |
| Ports SSH answers on (for firewall status, brute force protection) | `sshport.go` `SSHPorts` |
| Read-only `nft list ruleset`, `iptables -S INPUT` | `agent/internal/execx/execx.go` |
| Panel message | `panel/app/Services/SecurityService.php` `configureSsh` |

## Tests

`agent/internal/security/sshport_test.go` models the server's SSH, socket and UFW
state from the files the agent writes and answers the probe only where a
listener exists, so a change that "applies" but does not listen fails the
test:

- 22 → 2020 → 3030 → 22, with the plain service and with socket activation
- socket activation with IPv4+IPv6 and IPv4-only; socket enabled but stopped
- the incident: the socket does not bind the new port although every listing says it does
- `sshd -t` failure, reload failure, a service that does not come up (restarted once), a rollback that does not recover
- port already in use, rule missing after the firewall accepted it, closed and open nftables/iptables rulesets
- real sockets (`SSHProbe`: SSH greeting passes, HTTP and closed ports fail) and a real `sshd` (`TestVerifyListeningRealSSHD`, needs root)

The real-host VM matrix in CI (Ubuntu 22.04, 24.04, 26.04, AlmaLinux 9/10,
Rocky 9/10) runs the existing `ConfigureSSH` real-host test; systemd socket
activation itself is only exercised there on distributions that enable it.

## The panel's port is protected

The panel's own HTTPS port (8443 by default, read from its web server site) is
treated like SSH is not: the rule that opens exactly that port for everyone
cannot be deleted (Security → Firewall shows "Panel" and "Protected" instead of
Delete, and the agent refuses the call too), and a `deny` or `reject` rule for
everyone that covers it (the port itself or a range around it) is refused. Rules
for single source addresses are fine, so a hostile address can still be blocked.
If the panel moves to another port, the protection moves with it. `agent/internal/security/panelport.go`.

A background check (every minute, in the agent) also **restores the panel's rule if it
disappears**, for instance after someone ran `ufw delete` by hand or a firewall was reset.
The rule is put first in the list (so an earlier `deny` cannot hide it) with the remark
"nimvor: panel", and a warning is logged. It does nothing while the firewall is off, while a
security change awaits confirmation (that change may be rolled back), or when some allow
rule already opens the port, so a panel an administrator opened to their own address only is not
widened.

package security

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/changeset"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/selinux"
)

// SSHEffective returns `sshd -T` as key → value (keys lower-case).
func SSHEffective(ctx context.Context, env *host.Env) (map[string]string, error) {
	out, err := env.Runner.Run(ctx, "sshd", "-T")
	if err != nil {
		return nil, fmt.Errorf("sshd -T failed: %s", host.Output(out))
	}
	cfg := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		if ok {
			if _, dup := cfg[k]; !dup {
				cfg[k] = v
			}
		}
	}
	return cfg, nil
}

// SSHSettings are the options NimVor manages.
type SSHSettings struct {
	Port                   int    `json:"port"`
	PermitRootLogin        string `json:"permit_root_login"` // yes, prohibit-password, no
	PasswordAuthentication bool   `json:"password_authentication"`
	MaxAuthTries           int    `json:"max_auth_tries"`
	X11Forwarding          bool   `json:"x11_forwarding"`
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// Render produces the drop-in file.
func (s SSHSettings) Render() string {
	var b strings.Builder
	b.WriteString("# Managed by NimVor (Security → SSH). Do not edit: changes are overwritten.\n")
	b.WriteString("# sshd uses the first value it reads, so this file is named 00-… to take precedence.\n")
	fmt.Fprintf(&b, "Port %d\nPermitRootLogin %s\nPasswordAuthentication %s\n", s.Port, s.PermitRootLogin, yesNo(s.PasswordAuthentication))
	if !s.PasswordAuthentication {
		b.WriteString("KbdInteractiveAuthentication no\n")
	}
	fmt.Fprintf(&b, "PubkeyAuthentication yes\nMaxAuthTries %d\nX11Forwarding %s\n", s.MaxAuthTries, yesNo(s.X11Forwarding))
	return b.String()
}

func (s SSHSettings) validate() error {
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	switch s.PermitRootLogin {
	case "yes", "prohibit-password", "no":
	default:
		return fmt.Errorf("permit_root_login must be yes, prohibit-password or no")
	}
	if s.MaxAuthTries < 1 || s.MaxAuthTries > 10 {
		return fmt.Errorf("max_auth_tries must be 1–10")
	}
	return nil
}

// KeyAccess reports which accounts can log in with an SSH key: root and
// members of the sudo/admin/wheel groups with a non-empty authorized_keys.
func KeyAccess(env *host.Env) (root bool, admins []string) {
	hasKeys := func(home string) bool {
		b, err := env.Root.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
		if err != nil {
			return false
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				return true
			}
		}
		return false
	}
	homes := map[string]string{}
	if b, err := env.Root.ReadFile("/etc/passwd"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Split(l, ":")
			if len(f) >= 7 {
				homes[f[0]] = f[5]
			}
		}
	}
	root = hasKeys(homes["root"])
	if b, err := env.Root.ReadFile("/etc/group"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Split(l, ":")
			if len(f) == 4 && (f[0] == "sudo" || f[0] == "admin" || f[0] == "wheel") && f[3] != "" {
				for _, u := range strings.Split(f[3], ",") {
					if h, ok := homes[u]; ok && hasKeys(h) {
						admins = append(admins, u)
					}
				}
			}
		}
	}
	return root, admins
}

func sshUnit(env *host.Env) string {
	for _, dir := range []string{"/lib/systemd/system", "/usr/lib/systemd/system", "/etc/systemd/system"} {
		if env.Root.Exists(dir + "/ssh.service") {
			return "ssh.service"
		}
	}
	return "sshd.service"
}

// ConfigureSSH writes the drop-in, validates it with sshd -t, applies it and
// then checks that SSH really answers on the port, under the guard.
//
// A port change runs in this order, and every step that fails undoes the
// steps before it (the previous drop-in and socket drop-in are restored, the
// firewall rule is removed again, the daemon is reloaded):
//
//  1. refuse early: the port is in use, or a packet filter NimVor does not
//     manage (nftables, iptables) would drop it
//  2. open the port in the firewall (UFW, firewalld) and check the rule is there
//  3. SELinux port label
//  4. write the sshd drop-in, sshd -t, check the effective settings
//  5. socket activation (ssh.socket) or the daemon: apply the port
//  6. verify listening sockets and an SSH greeting on every address
//
// Only then is the change handed to the guard, which still undoes it unless
// the administrator confirms from a new connection on the new port.
func (s *Service) ConfigureSSH(ctx context.Context, in SSHSettings) (*Pending, error) {
	env := s.env
	if err := in.validate(); err != nil {
		return nil, actions.InvalidParams(err.Error())
	}
	if err := s.guard.Busy(); err != nil {
		return nil, err
	}
	rootKeys, admins := KeyAccess(env)
	if !in.PasswordAuthentication {
		canRoot := rootKeys && in.PermitRootLogin != "no"
		if !canRoot && len(admins) == 0 {
			return nil, actions.Conflict("No administrator account has an SSH key (authorized_keys). Add a key before turning off password logins, or you would be locked out.")
		}
	}
	if in.PermitRootLogin == "no" && len(admins) == 0 && !in.PasswordAuthentication {
		return nil, actions.Conflict("Root login would be disabled but no sudo user can log in with a key.")
	}
	current, err := SSHEffective(ctx, env)
	if err != nil {
		return nil, actions.Failed("The SSH configuration could not be read.", err.Error())
	}
	oldPort, _ := strconv.Atoi(strings.Fields(current["port"] + " 22")[0])
	portChange := in.Port != oldPort
	mode := detectSSHMode(ctx, env)

	pending := Pending{Kind: "ssh", Summary: fmt.Sprintf("SSH: port %d, root login %s, passwords %s", in.Port, in.PermitRootLogin, yesNo(in.PasswordAuthentication))}

	if portChange {
		// 1. Refuse before anything is touched.
		if l := readListeners(env); l.v4[in.Port] || l.v6[in.Port] {
			return nil, actions.Conflict(fmt.Sprintf("Port %d is already used by another program. Choose a free port.", in.Port))
		}
		if fw, err := Firewall(ctx, env); err != nil || !fw.Active {
			if why := unmanagedFilterBlocks(ctx, env, in.Port); why != "" {
				return nil, actions.Conflict(fmt.Sprintf("SSH was not moved to port %d: %s. NimVor does not manage this firewall, so it cannot open the port for you. Allow TCP port %d there first (keep port %d open until the new one works), then try again.", in.Port, why, in.Port, oldPort))
			}
		}
		// 2. Open the new port before SSH moves, and check the rule exists.
		if fw, err := Firewall(ctx, env); err == nil && fw.Active && fw.Backend == "firewalld" {
			item := "--port=" + strconv.Itoa(in.Port) + "/tcp"
			if err := runFirewalld(ctx, env, firewalldCmds(true, addOrRemove(item, true))); err != nil {
				return nil, err
			}
			pending.Commands = append(pending.Commands, firewalldCmds(true, addOrRemove(item, false))...)
		} else if err == nil && fw.Active {
			action := "allow"
			for _, r := range fw.Rules {
				if r.SSH && r.Action == "limit" {
					action = "limit" // keep the administrator's choice of rate limiting
				}
			}
			args := []string{action, "proto", "tcp", "from", "any", "to", "any", "port", strconv.Itoa(in.Port), "comment", "nimvor: SSH"}
			if err := s.ufw(ctx, args...); err != nil {
				return nil, err
			}
			pending.UFW = append(pending.UFW, append([]string{"--force", "delete"}, withoutComment(args)...))
		}
		if fw, err := Firewall(ctx, env); err == nil && fw.Active && !firewallOpens(ctx, env, in.Port) {
			undoFirewallNow(ctx, env, pending)
			return nil, actions.Failed(fmt.Sprintf("The firewall did not take a rule for SSH port %d, so SSH was not moved.", in.Port), "the rule is missing from the firewall's list after it was added")
		}
	}
	// 3. SELinux lets sshd listen only on ports labelled for it.
	if err := selinux.Port(ctx, env, "ssh_port_t", in.Port); err != nil {
		undoFirewallNow(ctx, env, pending)
		return nil, actions.Failed("SELinux does not allow SSH on port "+strconv.Itoa(in.Port)+".", err.Error())
	}
	pending.Files = []FileRestore{snapshot(env, env.M.SSHDropIn)}
	unit := sshUnit(env)
	pending.ReloadUnits = []string{unit}

	var settle func() // undoes the daemon side after the drop-in was committed
	undo := func(a *changeset.Applied) {
		if a != nil {
			_ = a.Rollback()
		}
		if settle != nil {
			settle()
		}
		undoFirewallNow(ctx, env, pending)
	}

	// 4. The sshd drop-in.
	set := env.Changes("ssh")
	set.Write(env.M.SSHDropIn, []byte(in.Render()), 0o644, agentfs.RootOwner)
	a, err := set.Commit(func() error {
		out, err := env.Runner.Run(ctx, "sshd", "-t")
		if err != nil {
			return fmt.Errorf("%s", host.Output(out))
		}
		return nil
	})
	if err != nil {
		undo(nil)
		return nil, host.FailWithValidation("The SSH settings were not applied", err)
	}
	after, err := SSHEffective(ctx, env)
	if err == nil {
		want := map[string]string{"port": strconv.Itoa(in.Port), "passwordauthentication": yesNo(in.PasswordAuthentication), "maxauthtries": strconv.Itoa(in.MaxAuthTries)}
		root := in.PermitRootLogin
		if root == "prohibit-password" {
			root = "without-password" // sshd -T prints the older alias
		}
		if got := after["permitrootlogin"]; got != root && got != in.PermitRootLogin {
			err = fmt.Errorf("permitrootlogin is %s", got)
		}
		for k, v := range want {
			if after[k] != v {
				err = fmt.Errorf("%s is %s (another file sets it earlier)", k, after[k])
			}
		}
	}
	if err != nil {
		undo(a)
		return nil, actions.Failed("The SSH settings would not take effect, so nothing was changed.", err.Error())
	}

	// 5. Apply: the socket decides the port under socket activation (sshd_config's
	// Port is ignored there), otherwise the daemon re-reads its configuration.
	settle = func() {
		for _, f := range pending.Files[1:] {
			_ = restoreFile(env, f)
		}
		if len(pending.Restart) > 0 {
			_ = env.DaemonReload(ctx)
			for _, u := range pending.Restart {
				_, _ = env.Runner.Run(ctx, "systemctl", "restart", "--", u)
			}
		}
		_ = env.Systemctl(ctx, "try-reload-or-restart", unit)
	}
	if mode.socket() {
		if err := applySSHSocket(ctx, env, in.Port, mode, &pending); err != nil {
			undo(a)
			return nil, actions.Failed("The SSH port could not be changed, so nothing was changed.", err.Error())
		}
	}
	verb := "reload"
	if mode.SocketActive {
		verb = "try-reload-or-restart" // the service may be stopped: the socket starts it on demand
	}
	if err := env.Systemctl(ctx, verb, unit); err != nil {
		undo(a)
		return nil, actions.Failed("The SSH server could not be reloaded; the previous settings were restored.", err.Error())
	}

	// 6. Prove it: sockets are listening and SSH answers on every address.
	verified, err := verifyListening(ctx, env, in.Port, mode.IPv6)
	if err != nil && !mode.socket() && portChange {
		// A reload keeps the old listener on some builds: restart once.
		if rerr := env.Systemctl(ctx, "restart", unit); rerr == nil {
			verified, err = verifyListening(ctx, env, in.Port, mode.IPv6)
		}
	}
	if err != nil {
		undo(a)
		note := ""
		if _, perr := verifyListening(ctx, env, oldPort, mode.IPv6); perr != nil && portChange {
			note = fmt.Sprintf(" The previous port %d does not answer either; use the provider's console to check the SSH service.", oldPort)
		}
		return nil, actions.Failed(fmt.Sprintf("SSH did not answer on port %d, so the previous settings were restored.%s", in.Port, note), err.Error())
	}
	pending.Verified = verified
	if portChange {
		syncFail2banSSHPort(ctx, env, SSHPorts(ctx, env), &pending)
	}
	return s.guard.Start(pending)
}

// undoFirewallNow reverts firewall rules added for a change that stopped
// before it was applied.
func undoFirewallNow(ctx context.Context, env *host.Env, p Pending) {
	for _, a := range p.UFW {
		_, _ = env.Runner.Run(ctx, "ufw", a...)
	}
	_ = runFirewalld(ctx, env, p.Commands)
}

// sshSocketUnit is how Ubuntu 22.10 and later start sshd: systemd listens on the port and starts sshd
// when someone connects, so the "Port" line of sshd_config is not what decides where SSH answers.
const sshSocketUnit = "ssh.socket"

func sshSocketActive(ctx context.Context, env *host.Env) bool {
	return env.UnitActive(ctx, sshSocketUnit)
}

func sshSocketDropIn(env *host.Env) string {
	return filepath.Join(env.M.UnitDir, "ssh.socket.d", "nimvor-port.conf")
}

// applySSHSocket makes the socket listen on port, for IPv4 and (when the kernel has it) IPv6.
// An active socket is restarted (open sessions stay) and checked; a socket that is enabled but stopped
// (sshd runs on its own for now) only gets its setting, so the next boot comes up on the same port.
// What it changed is recorded in pending so an unconfirmed change puts the old port back.
func applySSHSocket(ctx context.Context, env *host.Env, port int, mode sshMode, pending *Pending) error {
	dropIn := sshSocketDropIn(env)
	snap := snapshot(env, dropIn)
	set := env.Changes("ssh-socket")
	if port == 22 {
		if !snap.Existed {
			return nil // already on the default
		}
		set.Remove(dropIn)
	} else {
		set.Mkdir(filepath.Dir(dropIn), 0o755, agentfs.RootOwner)
		set.Write(dropIn, []byte(socketDropIn(port, mode.IPv6)), 0o644, agentfs.RootOwner)
	}
	if _, err := set.Apply(); err != nil {
		return err
	}
	pending.Files = append(pending.Files, snap)
	if mode.SocketActive {
		pending.Restart = append(pending.Restart, sshSocketUnit)
	}
	revert := func() {
		_ = restoreFile(env, snap)
		_ = env.DaemonReload(ctx)
		if mode.SocketActive {
			_, _ = env.Runner.Run(ctx, "systemctl", "restart", "--", sshSocketUnit)
			pending.Restart = pending.Restart[:len(pending.Restart)-1]
		}
		pending.Files = pending.Files[:len(pending.Files)-1]
	}
	if err := env.DaemonReload(ctx); err != nil {
		revert()
		return err
	}
	if !mode.SocketActive {
		return nil
	}
	if out, err := env.Runner.Run(ctx, "systemctl", "restart", "--", sshSocketUnit); err != nil {
		revert()
		return fmt.Errorf("%s", host.Output(out))
	}
	out, err := env.Runner.Run(ctx, "systemctl", "show", "--property=Listen", "--value", "--", sshSocketUnit)
	if err == nil && strings.TrimSpace(string(out)) != "" && !strings.Contains(string(out), ":"+strconv.Itoa(port)+" ") {
		revert()
		return fmt.Errorf("%s still listens on %s", sshSocketUnit, strings.Join(strings.Fields(strings.ReplaceAll(string(out), "\n", " ")), " "))
	}
	return nil
}

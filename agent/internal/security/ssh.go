package security

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/selinux"
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

// SSHSettings are the options WhaCP manages.
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
	b.WriteString("# Managed by WhaCP (Security → SSH). Do not edit: changes are overwritten.\n")
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

// ConfigureSSH writes the drop-in, validates it with sshd -t, reloads the
// SSH server and verifies the effective settings, under the guard.
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
	oldPort, _ := strconv.Atoi(current["port"])

	pending := Pending{Kind: "ssh", Summary: fmt.Sprintf("SSH: port %d, root login %s, passwords %s", in.Port, in.PermitRootLogin, yesNo(in.PasswordAuthentication))}
	// Open the new port first if the firewall is active.
	if in.Port != oldPort {
		if fw, err := Firewall(ctx, env); err == nil && fw.Active && fw.Backend == "firewalld" {
			item := "--port=" + strconv.Itoa(in.Port) + "/tcp"
			if err := runFirewalld(ctx, env, firewalldCmds(true, addOrRemove(item, true))); err != nil {
				return nil, err
			}
			pending.Commands = append(pending.Commands, firewalldCmds(true, addOrRemove(item, false))...)
		} else if err == nil && fw.Active {
			args := []string{"limit", "proto", "tcp", "from", "any", "to", "any", "port", strconv.Itoa(in.Port), "comment", "whacp: SSH"}
			if err := s.ufw(ctx, args...); err != nil {
				return nil, err
			}
			pending.UFW = append(pending.UFW, append([]string{"--force", "delete"}, withoutComment(args)...))
		}
	}
	// SELinux lets sshd listen only on ports labelled for it.
	if err := selinux.Port(ctx, env, "ssh_port_t", in.Port); err != nil {
		undoFirewallNow(ctx, env, pending)
		return nil, actions.Failed("SELinux does not allow SSH on port "+strconv.Itoa(in.Port)+".", err.Error())
	}
	pending.Files = []FileRestore{snapshot(env, env.M.SSHDropIn)}
	unit := sshUnit(env)
	pending.ReloadUnits = []string{unit}

	undoFirewall := func() {
		for _, a := range pending.UFW {
			_, _ = env.Runner.Run(ctx, "ufw", a...)
		}
		_ = runFirewalld(ctx, env, pending.Commands)
	}
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
		undoFirewall()
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
		_ = a.Rollback()
		undoFirewall()
		return nil, actions.Failed("The SSH settings would not take effect, so nothing was changed.", err.Error())
	}
	if err := env.Systemctl(ctx, "reload", unit); err != nil {
		_ = a.Rollback()
		undoFirewall()
		return nil, actions.Failed("The SSH server could not be reloaded; the previous settings were restored.", err.Error())
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

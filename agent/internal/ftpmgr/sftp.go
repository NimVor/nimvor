package ftpmgr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/shacrypt"
)

// SFTP access for a website's own system user. OpenSSH confines it with
// ChrootDirectory (the sites root, which must be root-owned and is made
// 0711 so the other websites' folders cannot be listed) and internal-sftp
// starting in the website folder; no shell, forwarding or TTY. Keys live in
// root-owned files outside the website so they cannot be changed from it.

// SFTPUser is one website account's SFTP settings.
type SFTPUser struct {
	User          string   `json:"user"`
	Domain        string   `json:"domain"`
	PasswordLogin bool     `json:"password_login"`
	Keys          []string `json:"keys"`
}

type sftpState struct {
	Users map[string]SFTPUser `json:"users"`
	// Shells holds each account's shell mode (see shell.go); it applies
	// while the account's SSH/SFTP login is on.
	Shells map[string]string `json:"shells,omitempty"`
}

func statePath(env *host.Env) string { return filepath.Join(env.Cfg.StateDir, "sftp.json") }

func loadState(env *host.Env) sftpState {
	st := sftpState{Users: map[string]SFTPUser{}}
	if b, err := env.Root.ReadFile(statePath(env)); err == nil {
		json.Unmarshal(b, &st)
	}
	if st.Users == nil {
		st.Users = map[string]SFTPUser{}
	}
	if st.Shells == nil {
		st.Shells = map[string]string{}
	}
	return st
}

var keyTypes = map[string]bool{"ssh-ed25519": true, "ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true, "ecdsa-sha2-nistp521": true, "ssh-rsa": true, "sk-ssh-ed25519@openssh.com": true, "sk-ecdsa-sha2-nistp256@openssh.com": true}
var commentRe = regexp.MustCompile(`^[A-Za-z0-9 ._@+=:/()-]{0,100}$`)

// NormalizeKey checks an authorized_keys line: a plain key (no options
// such as command= or from=), with an optional short comment.
func NormalizeKey(line string) (string, error) {
	f := strings.Fields(strings.TrimSpace(line))
	if len(f) < 2 || !keyTypes[f[0]] {
		return "", fmt.Errorf("not an SSH public key (expected e.g. ssh-ed25519 AAAA… comment)")
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil || len(blob) < 20 || len(f[1]) > 2000 {
		return "", fmt.Errorf("the key data is not valid base64")
	}
	// The blob starts with its own type name.
	if len(blob) < 4+len(f[0]) || string(blob[4:4+len(f[0])]) != f[0] {
		return "", fmt.Errorf("the key data does not match its type %s", f[0])
	}
	comment := strings.Join(f[2:], " ")
	if !commentRe.MatchString(comment) {
		comment = ""
	}
	return strings.TrimSpace(f[0] + " " + f[1] + " " + comment), nil
}

// renderDropIn produces the Match blocks for every enabled user.
func renderDropIn(env *host.Env, st sftpState) string {
	var b strings.Builder
	b.WriteString("# Managed by WhaCP (website SFTP access). Do not edit: changes are overwritten.\n")
	names := make([]string, 0, len(st.Users))
	for n := range st.Users {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		u := st.Users[n]
		mode := shellMode(st, n)
		fmt.Fprintf(&b, "\nMatch User %s\n", u.User)
		switch mode {
		case ShellJailed:
			// Everything (SFTP too) goes through the sandboxing login shell.
			fmt.Fprintf(&b, "    ForceCommand %s\n", JailShell)
		case ShellNormal:
		default:
			fmt.Fprintf(&b, "    ChrootDirectory %s\n", filepath.Clean(env.Cfg.SitesRoot))
			fmt.Fprintf(&b, "    ForceCommand internal-sftp -d /%s -u 0027\n", u.Domain)
		}
		fmt.Fprintf(&b, "    AuthorizedKeysFile %s/%%u\n", env.M.SFTPKeysDir)
		fmt.Fprintf(&b, "    PasswordAuthentication %s\n", yesNo(u.PasswordLogin))
		b.WriteString("    PubkeyAuthentication yes\n    AllowTcpForwarding no\n    AllowAgentForwarding no\n    AllowStreamLocalForwarding no\n    X11Forwarding no\n    PermitTunnel no\n    PermitUserRC no\n")
		fmt.Fprintf(&b, "    PermitTTY %s\n", yesNo(mode != ShellDisabled))
	}
	return b.String()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// chrootReady checks the sites root and its parents are owned by root and
// not writable by group or others (OpenSSH refuses the chroot otherwise).
func chrootReady(env *host.Env) error {
	if os.Geteuid() != 0 {
		return nil // development agent: ownership cannot be checked
	}
	p := filepath.Clean(env.Cfg.SitesRoot)
	for {
		real, err := env.Root.Path(p)
		if err != nil {
			return err
		}
		info, err := os.Lstat(real)
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || info.Mode().Perm()&0o022 != 0 || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s must belong to root and not be writable by group or others for SFTP confinement", p)
		}
		if p == "/" {
			return nil
		}
		p = filepath.Dir(p)
	}
}

// effective returns sshd's settings for a connection by user.
func effective(ctx context.Context, env *host.Env, user string) (map[string]string, error) {
	out, err := env.Runner.Run(ctx, "sshd", "-T", "-C", "user="+user+",host=localhost,addr=127.0.0.1")
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

// Settings that must not change for anybody else when the SFTP file is
// written (an older OpenSSH could let a Match block in an included file
// capture the rest of sshd_config).
var unaffected = []string{"chrootdirectory", "forcecommand", "passwordauthentication", "permitrootlogin", "x11forwarding", "allowtcpforwarding", "authorizedkeysfile", "permittty", "subsystem", "port"}

func sshUnit(env *host.Env) string {
	for _, dir := range []string{"/lib/systemd/system", "/usr/lib/systemd/system", "/etc/systemd/system"} {
		if env.Root.Exists(dir + "/ssh.service") {
			return "ssh.service"
		}
	}
	return "sshd.service"
}

// applySFTP applies the SSH settings after one account's SFTP change.
func applySFTP(ctx context.Context, env *host.Env, st sftpState, changedUser string, password string) error {
	return applySSH(ctx, env, st, []string{changedUser}, changedUser, password)
}

// applySSH writes the state, drop-in and keys, validates with sshd -t,
// verifies the effective settings of the changed accounts, reloads sshd
// and sets their login shells; everything is rolled back on failure.
// Access is taken away before the configuration changes and a shell is
// given only once sshd runs the configuration that pins its key file.
func applySSH(ctx context.Context, env *host.Env, st sftpState, changed []string, pwUser string, password string) error {
	before, err := effective(ctx, env, "root")
	if err != nil {
		return actions.Failed("The SSH server configuration could not be read.", err.Error())
	}
	current := map[string]string{}
	if all, err := env.Accounts(); err == nil {
		for _, u := range all {
			current[u.Name] = u.Shell
		}
	}
	jailed := false
	for _, name := range changed {
		mode := shellMode(st, name)
		jailed = jailed || mode == ShellJailed
		if mode == ShellDisabled && current[name] != NoLogin {
			if err := setShell(ctx, env, name, ShellDisabled); err != nil {
				return err
			}
			current[name] = NoLogin
		}
	}
	set := env.Changes("sftp")
	if jailed {
		if err := prepareJail(ctx, env, set); err != nil {
			return err
		}
	}
	set.Mkdir(env.M.SFTPKeysDir, 0o755, agentfs.RootOwner)
	if len(st.Users) > 0 {
		// Hide the list of websites from confined users.
		set.Mkdir(env.Cfg.SitesRoot, 0o711, agentfs.RootOwner)
	}
	for name, u := range st.Users {
		set.Write(filepath.Join(env.M.SFTPKeysDir, name), []byte(strings.Join(u.Keys, "\n")+"\n"), 0o644, agentfs.RootOwner)
	}
	for _, name := range changed {
		if _, on := st.Users[name]; !on {
			keyFile := filepath.Join(env.M.SFTPKeysDir, name)
			if env.Root.Exists(keyFile) {
				set.Remove(keyFile)
			}
		}
	}
	set.Write(env.M.SFTPDropIn, []byte(renderDropIn(env, st)), 0o644, agentfs.RootOwner)
	stateJSON, _ := json.MarshalIndent(st, "", "  ")
	set.Write(statePath(env), stateJSON, 0o600, agentfs.RootOwner)
	applied, err := set.Commit(func() error {
		out, err := env.Runner.Run(ctx, "sshd", "-t")
		if err != nil {
			return fmt.Errorf("%s", host.Output(out))
		}
		after, err := effective(ctx, env, "root")
		if err != nil {
			return err
		}
		for _, k := range unaffected {
			if before[k] != after[k] {
				return fmt.Errorf("the SFTP settings would change %s for other users (%q → %q); this OpenSSH version does not scope Match blocks in included files", k, before[k], after[k])
			}
		}
		for _, name := range changed {
			if _, on := st.Users[name]; !on {
				continue
			}
			mine, err := effective(ctx, env, name)
			if err != nil {
				return err
			}
			if err := verifyMode(env, name, shellMode(st, name), mine); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return host.FailWithValidation("The SSH settings were not applied", err)
	}
	// Password: set (hashed) or lock password logins for the account.
	if pwUser != "" {
		if u, on := st.Users[pwUser]; on && u.PasswordLogin && password != "" {
			out, perr := env.Runner.RunWith(ctx, "chpasswd", execx.Opts{Stdin: strings.NewReader(pwUser + ":" + shacrypt.HashPassword(password) + "\n")}, "-e")
			if perr != nil {
				applied.Rollback()
				return actions.Failed("The SFTP password could not be set.", host.Output(out))
			}
		} else if !on || !u.PasswordLogin {
			env.Runner.Run(ctx, "usermod", "--lock", "--", pwUser)
		}
	}
	if env.UnitActive(ctx, sshUnit(env)) {
		if err := env.Systemctl(ctx, "reload", sshUnit(env)); err != nil {
			applied.Rollback()
			env.Systemctl(ctx, "reload", sshUnit(env))
			return actions.Failed("The SSH server did not reload; the previous settings were restored.", err.Error())
		}
	}
	for _, name := range changed {
		mode := shellMode(st, name)
		if mode == ShellDisabled || current[name] == loginShell(mode) && mode != ShellJailed {
			continue
		}
		if err := setShell(ctx, env, name, mode); err != nil {
			setShell(ctx, env, name, ShellDisabled)
			return err
		}
	}
	return nil
}

// verifyMode checks sshd applies an account's mode (sshd -T for it).
func verifyMode(env *host.Env, user, mode string, eff map[string]string) error {
	chroot, force := eff["chrootdirectory"], eff["forcecommand"]
	switch mode {
	case ShellJailed:
		if force != JailShell || (chroot != "" && chroot != "none") {
			return fmt.Errorf("sshd does not run %s's sessions through the jail (chroot %q, command %q); another configuration file may take precedence", user, chroot, force)
		}
	case ShellNormal:
		if (force != "" && force != "none") || (chroot != "" && chroot != "none") {
			return fmt.Errorf("sshd does not give %s a shell (chroot %q, command %q); another configuration file may take precedence", user, chroot, force)
		}
	default:
		if chroot != filepath.Clean(env.Cfg.SitesRoot) || !strings.Contains(force, "internal-sftp") {
			return fmt.Errorf("sshd does not apply the SFTP confinement to %s (chroot %q, command %q); another configuration file may take precedence", user, chroot, force)
		}
	}
	return nil
}

// setShell sets an account's login shell and jail group membership.
func setShell(ctx context.Context, env *host.Env, user, mode string) error {
	if out, err := env.Runner.Run(ctx, "usermod", "--shell", loginShell(mode), "--", user); err != nil {
		return actions.Failed("The login shell of "+user+" could not be changed.", host.Output(out))
	}
	if _, ok := groupGID(env, JailGroup); !ok {
		return nil
	}
	if mode == ShellJailed {
		if out, err := env.Runner.Run(ctx, "usermod", "--append", "--groups", JailGroup, "--", user); err != nil {
			return actions.Failed(user+" could not be added to the "+JailGroup+" group.", host.Output(out))
		}
		return nil
	}
	// Not a member: gpasswd fails, which is fine.
	env.Runner.Run(ctx, "gpasswd", "--delete", user, JailGroup)
	return nil
}

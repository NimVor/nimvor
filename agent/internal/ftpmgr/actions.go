package ftpmgr

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/fw"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/selinux"
	"github.com/mchkhawarshahzad/whacp/agent/internal/shacrypt"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

type configureReq struct {
	Settings
	ConfirmReplaceService bool `json:"confirm_replace_service"`
	// ReplaceUnusedStock lets the server setup replace the pure-ftpd
	// package's own default service when it is the only other FTP server and
	// has no virtual accounts: installing the package starts it, so it is not
	// a server somebody configured.
	ReplaceUnusedStock bool `json:"replace_unused_stock"`
}

type accountReq struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Dir      string `json:"dir"`
	Password string `json:"password"`
	QuotaMB  int64  `json:"quota_mb"`
}

type sftpReq struct {
	User          string   `json:"user"`
	Enabled       bool     `json:"enabled"`
	PasswordLogin bool     `json:"password_login"`
	Password      string   `json:"password"`
	Keys          []string `json:"keys"`
}

var domainRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

func certInfo(env *host.Env, p paths) map[string]any {
	b, err := env.Root.ReadFile(p.cert)
	if err != nil {
		return nil
	}
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			return nil
		}
		if blk.Type == "CERTIFICATE" {
			c, err := x509.ParseCertificate(blk.Bytes)
			if err != nil {
				return nil
			}
			sum := sha256.Sum256(c.Raw)
			return map[string]any{"subject": c.Subject.String(), "issuer": c.Issuer.String(), "not_after": c.NotAfter, "sha256": strings.ToUpper(hex.EncodeToString(sum[:]))}
		}
	}
}

func loadAccounts(env *host.Env, p paths) []Account {
	b, _ := env.Root.ReadFile(p.passwd)
	all, _ := env.Accounts()
	return parseAccounts(string(b), all)
}

// saveAccounts writes the account list and rebuilds pure-ftpd's database
// (the old list comes back if that fails).
func saveAccounts(ctx context.Context, env *host.Env, p paths, list []Account) error {
	set := env.Changes("ftp-accounts")
	set.Write(p.passwd, renderAccounts(list), 0o600, agentfs.RootOwner)
	_, err := set.Commit(func() error { return mkdb(ctx, env, p) })
	if err != nil {
		return host.FailWithValidation("The FTP accounts were not saved", err)
	}
	return nil
}

func mkdb(ctx context.Context, env *host.Env, p paths) error {
	out, err := env.Runner.Run(ctx, "pure-pw", "mkdb", p.pdb, "-f", p.passwd)
	if err != nil {
		return fmt.Errorf("pure-pw mkdb: %s", host.Output(out))
	}
	return nil
}

// RemoveSite deletes the FTP accounts inside a website's folder and its
// SFTP access (called when the website is deleted).
func RemoveSite(ctx context.Context, env *host.Env, domain string) error {
	mu.Lock()
	defer mu.Unlock()
	p := ftpPaths(env)
	root := filepath.Join(filepath.Clean(env.Cfg.SitesRoot), domain)
	if env.Root.Exists(p.passwd) {
		all := loadAccounts(env, p)
		keep := make([]Account, 0, len(all))
		for _, a := range all {
			if a.Dir != root && !strings.HasPrefix(a.Dir, root+"/") {
				keep = append(keep, a)
			}
		}
		if len(keep) != len(all) {
			if err := saveAccounts(ctx, env, p, keep); err != nil {
				return err
			}
		}
	}
	st := loadState(env)
	for name, u := range st.Users {
		if u.Domain == domain {
			delete(st.Users, name)
			return applySFTP(ctx, env, st, name, "")
		}
	}
	return nil
}

// Register adds the FTP and SFTP actions.
func Register(r *actions.Registry, env *host.Env) {
	p := ftpPaths(env)

	r.Register(actions.Action{Name: "ftp.status", Description: "FTP server state, settings, certificate and accounts (optionally of one website user); SFTP-enabled website users.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			User string `json:"user"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		out := map[string]any{"installed": env.Root.Exists("/usr/sbin/pure-ftpd"), "configured": env.Root.Exists(p.conf), "active": env.UnitActive(ctx, Unit)}
		settings := Defaults()
		if b, err := env.Root.ReadFile(p.conf); err == nil {
			settings = Parse(string(b))
		}
		out["settings"] = settings
		out["certificate"] = certInfo(env, p)
		var others []string
		for _, u := range otherFTPUnits {
			if env.UnitActive(ctx, u) {
				others = append(others, u)
			}
		}
		out["other_ftp_servers"] = others
		accounts := []Account{}
		for _, a := range loadAccounts(env, p) {
			if in.User == "" || a.User == in.User {
				accounts = append(accounts, a)
			}
		}
		out["accounts"] = accounts
		st := loadState(env)
		sftp := []map[string]any{}
		for _, u := range st.Users {
			if in.User != "" && u.User != in.User {
				continue
			}
			keys := []map[string]string{}
			for _, k := range u.Keys {
				keys = append(keys, map[string]string{"key": k, "fingerprint": fingerprint(k)})
			}
			sftp = append(sftp, map[string]any{"user": u.User, "domain": u.Domain, "password_login": u.PasswordLogin, "keys": keys, "shell": shellMode(st, u.User)})
		}
		out["sftp"] = sftp
		out["sftp_root"] = filepath.Clean(env.Cfg.SitesRoot)
		return out, nil
	}})

	r.Register(actions.Action{Name: "ftp.configure", Mutating: true, Description: "Set up or change WhaCP's FTP server (its own pure-ftpd unit and configuration; TLS required by default). Stopping another running FTP server needs confirm_replace_service. Restarts, verifies and rolls back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		in := configureReq{Settings: Defaults()}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.Settings.validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		mu.Lock()
		defer mu.Unlock()
		if !env.Root.Exists("/usr/sbin/pure-ftpd") {
			return nil, actions.Conflict("pure-ftpd is not installed. Install it under Server → Packages first.")
		}
		var others []string
		for _, u := range otherFTPUnits {
			if env.UnitActive(ctx, u) {
				others = append(others, u)
			}
		}
		if len(others) > 0 && !in.ConfirmReplaceService && !(in.ReplaceUnusedStock && unusedStockOnly(env, others)) {
			return nil, actions.Conflict(strings.Join(others, ", ") + " is running and uses the FTP port. Confirm to stop and disable it; WhaCP then runs its own FTP service. Accounts configured in it are not carried over.")
		}
		old := Defaults()
		if b, err := env.Root.ReadFile(p.conf); err == nil {
			old = Parse(string(b))
		}
		wasActive := env.UnitActive(ctx, Unit)
		set := env.Changes("ftp")
		set.Mkdir(p.dir, 0o700, agentfs.RootOwner)
		set.Write(p.conf, []byte(in.Settings.Render(p)), 0o644, agentfs.RootOwner)
		set.Write(p.unit, []byte(renderUnit(p)), 0o644, agentfs.RootOwner)
		if !env.Root.Exists(p.cert) {
			hn, _ := os.Hostname()
			if hn == "" {
				hn = "localhost"
			}
			pemData, err := selfSigned(hn)
			if err != nil {
				return nil, actions.Failed("Could not create the FTP certificate.", err.Error())
			}
			set.Write(p.cert, pemData, 0o600, agentfs.RootOwner)
		}
		if !env.Root.Exists(p.passwd) {
			set.Write(p.passwd, nil, 0o600, agentfs.RootOwner)
		}
		applied, err := set.Commit(func() error { return mkdb(ctx, env, p) })
		if err != nil {
			return nil, host.FailWithValidation("The FTP server was not configured", err)
		}
		fail := func(msg string, cause error) (any, error) {
			applied.Rollback()
			env.DaemonReload(ctx)
			if wasActive {
				env.Systemctl(ctx, "restart", Unit)
			} else {
				env.Systemctl(ctx, "stop", Unit)
				env.Systemctl(ctx, "disable", Unit)
			}
			for _, u := range others {
				env.Systemctl(ctx, "enable", u)
				env.Systemctl(ctx, "start", u)
			}
			return nil, actions.Failed(msg+" The previous state was restored.", cause.Error())
		}
		if err := env.DaemonReload(ctx); err != nil {
			return fail("systemd could not load the FTP service.", err)
		}
		for _, u := range others {
			env.Systemctl(ctx, "stop", u)
			env.Systemctl(ctx, "disable", u)
		}
		// SELinux: the FTP server works in websites' folders.
		if err := selinux.Boolean(ctx, env, "ftpd_full_access"); err != nil {
			return fail("SELinux does not let the FTP server use websites' files.", err)
		}
		if err := env.Systemctl(ctx, "enable", Unit); err != nil {
			return fail("The FTP service could not be enabled.", err)
		}
		if err := env.Systemctl(ctx, "restart", Unit); err != nil {
			return fail("The FTP service did not start.", err)
		}
		if !env.Cfg.SimulateCommands {
			if err := probe(ctx, "127.0.0.1:21"); err != nil {
				return fail("The FTP service does not answer on port 21.", err)
			}
		}
		notes := []string{}
		if old.PassiveMin != in.PassiveMin || old.PassiveMax != in.PassiveMax {
			fw.Close(ctx, env, fw.Port(fmt.Sprintf("%d:%d/tcp", old.PassiveMin, old.PassiveMax)))
		}
		notes = append(notes, fw.Open(ctx, env, "FTP", "21/tcp", fw.Port(fmt.Sprintf("%d:%d/tcp", in.PassiveMin, in.PassiveMax))))
		return map[string]any{"settings": in.Settings, "stopped": others, "notes": notes, "certificate": certInfo(env, p), "changed": applied.Changed}, nil
	}})

	r.Register(actions.Action{Name: "ftp.disable", Mutating: true, Description: "Stop and disable WhaCP's FTP service (accounts are kept).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		mu.Lock()
		defer mu.Unlock()
		env.Systemctl(ctx, "stop", Unit)
		env.Systemctl(ctx, "disable", Unit)
		return map[string]any{"active": env.UnitActive(ctx, Unit)}, nil
	}})

	r.Register(actions.Action{Name: "ftp.account_save", Mutating: true, Description: "Create or change an FTP account of a website: chrooted to the website folder (or a sub-folder), files owned by the website's user. The password is stored as a SHA-512 crypt hash.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in accountReq
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		in.Name = strings.ToLower(strings.TrimSpace(in.Name))
		if !accountRe.MatchString(in.Name) {
			return nil, actions.InvalidParams("name may contain lower-case letters, digits and . _ @ - (up to 64)")
		}
		if in.QuotaMB < 0 || in.QuotaMB > 10<<20 {
			return nil, actions.InvalidParams("quota_mb must be 0 (none) to 10485760")
		}
		mu.Lock()
		defer mu.Unlock()
		if !env.Root.Exists(p.passwd) {
			return nil, actions.Conflict("Set up the FTP server first (Server → FTP).")
		}
		u, err := siteUser(env, in.User)
		if err != nil {
			return nil, err
		}
		dir, err := siteDir(env, u, in.Dir)
		if err != nil {
			return nil, err
		}
		list := loadAccounts(env, p)
		idx := -1
		for i, a := range list {
			if a.Name == in.Name {
				idx = i
			}
		}
		acct := Account{Name: in.Name, User: u.Name, UID: u.UID, gid: u.GID, Dir: dir, QuotaMB: in.QuotaMB}
		switch {
		case in.Password != "":
			if err := validPassword(in.Password); err != nil {
				return nil, actions.InvalidParams(err.Error())
			}
			acct.hash = lockHash(shacrypt.HashPassword(in.Password), accountSuspended(env, u.Name))
		case idx >= 0:
			if list[idx].UID != u.UID {
				return nil, actions.Conflict("an FTP account named " + in.Name + " belongs to another website")
			}
			acct.hash = list[idx].hash
		default:
			return nil, actions.InvalidParams("password is required for a new account")
		}
		created := idx < 0
		if created {
			list = append(list, acct)
		} else {
			list[idx] = acct
		}
		if err := saveAccounts(ctx, env, p, list); err != nil {
			return nil, err
		}
		return map[string]any{"account": acct, "created": created}, nil
	}})

	r.Register(actions.Action{Name: "ftp.move_site", Mutating: true, Description: "Give a website's FTP accounts (those inside its folder) to the website's current account, keeping their passwords (after the website moved to another account).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			User   string `json:"user"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, host.Invalid(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if !env.Root.Exists(p.passwd) {
			return map[string]any{"moved": 0}, nil
		}
		u, err := siteUser(env, in.User)
		if err != nil {
			return nil, err
		}
		// The website folder must already be the account's (the website was
		// moved to it): accounts are never given to an unrelated account.
		root := filepath.Join(filepath.Clean(env.Cfg.SitesRoot), in.Domain)
		real, err := env.Root.Path(root)
		if err != nil {
			return nil, host.Invalid(err)
		}
		fi, err := os.Lstat(real)
		if err != nil || !fi.IsDir() || fi.Sys().(*syscall.Stat_t).Uid != uint32(u.UID) {
			return nil, actions.Conflict("The folder of " + in.Domain + " does not belong to " + in.User + "; its FTP accounts were not moved.")
		}
		list := loadAccounts(env, p)
		moved := 0
		for i, a := range list {
			if (a.Dir == root || strings.HasPrefix(a.Dir, root+"/")) && a.UID != u.UID {
				list[i].User, list[i].UID, list[i].gid = u.Name, u.UID, u.GID
				moved++
			}
		}
		if moved > 0 {
			if err := saveAccounts(ctx, env, p, list); err != nil {
				return nil, err
			}
		}
		return map[string]any{"moved": moved}, nil
	}})

	r.Register(actions.Action{Name: "ftp.account_delete", Mutating: true, Description: "Delete an FTP account (files are kept).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in accountReq
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		list := loadAccounts(env, p)
		keep := list[:0]
		found := false
		for _, a := range list {
			if a.Name == in.Name && (in.User == "" || a.User == in.User) {
				found = true
				continue
			}
			keep = append(keep, a)
		}
		if !found {
			return nil, actions.NotFound("no FTP account " + in.Name)
		}
		if err := saveAccounts(ctx, env, p, keep); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": in.Name}, nil
	}})

	registerSuspend(r, env)
	registerShell(r, env)

	r.Register(actions.Action{Name: "sftp.configure", Mutating: true, Description: "Turn SFTP access on or off for a website's system user: confined to the websites root (starting in its folder), SFTP only, with keys and/or a password. Validated with sshd -t, verified with sshd -T, rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in sftpReq
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		u, err := siteUser(env, in.User)
		if err != nil {
			return nil, err
		}
		domain := filepath.Base(filepath.Clean(u.Home))
		if !domainRe.MatchString(domain) {
			return nil, actions.InvalidParams("the website folder name is not supported for SFTP")
		}
		st := loadState(env)
		prev, had := st.Users[u.Name]
		if !in.Enabled {
			if !had {
				return map[string]any{"enabled": false}, nil
			}
			delete(st.Users, u.Name)
			if err := applySFTP(ctx, env, st, u.Name, ""); err != nil {
				return nil, err
			}
			return map[string]any{"enabled": false}, nil
		}
		if in.Password != "" {
			if err := validPassword(in.Password); err != nil {
				return nil, actions.InvalidParams(err.Error())
			}
		}
		if in.PasswordLogin && in.Password == "" && !(had && prev.PasswordLogin) {
			return nil, actions.InvalidParams("set a password to allow password logins")
		}
		if len(in.Keys) > 20 {
			return nil, actions.InvalidParams("at most 20 keys")
		}
		keys := []string{}
		seen := map[string]bool{}
		for i, k := range in.Keys {
			if strings.TrimSpace(k) == "" {
				continue
			}
			nk, err := NormalizeKey(k)
			if err != nil {
				return nil, actions.InvalidParams("key " + strconv.Itoa(i+1) + ": " + err.Error())
			}
			if f := strings.Fields(nk)[1]; !seen[f] {
				seen[f] = true
				keys = append(keys, nk)
			}
		}
		if !in.PasswordLogin && len(keys) == 0 {
			return nil, actions.InvalidParams("add an SSH key or allow password logins")
		}
		if err := chrootReady(env); err != nil {
			return nil, actions.Conflict(err.Error())
		}
		st.Users[u.Name] = SFTPUser{User: u.Name, Domain: domain, PasswordLogin: in.PasswordLogin, Keys: keys}
		if err := applySFTP(ctx, env, st, u.Name, in.Password); err != nil {
			return nil, err
		}
		return map[string]any{"enabled": true, "user": u.Name, "start_dir": "/" + domain, "keys": len(keys), "password_login": in.PasswordLogin}, nil
	}})
}

// fingerprint returns the OpenSSH SHA256 fingerprint of a public key line.
func fingerprint(key string) string {
	f := strings.Fields(key)
	if len(f) < 2 {
		return ""
	}
	blob, err := base64Decode(f[1])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(blob)
	return "SHA256:" + strings.TrimRight(base64Encode(sum[:]), "=")
}

func base64Decode(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }
func base64Encode(b []byte) string          { return base64.StdEncoding.EncodeToString(b) }

// unusedStockOnly reports whether the running FTP servers are only the
// pure-ftpd package's own service and it has no virtual accounts (no or an
// empty /etc/pure-ftpd/pureftpd.passwd): the state right after the package is
// installed, which nobody configured.
func unusedStockOnly(env *host.Env, others []string) bool {
	if len(others) != 1 || others[0] != "pure-ftpd.service" {
		return false
	}
	b, err := env.Root.ReadFile("/etc/pure-ftpd/pureftpd.passwd")
	return err != nil || strings.TrimSpace(string(b)) == ""
}

package ftpmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

type fixture struct {
	h *testhost.Host
	r *actions.Registry
}

func setup(t *testing.T) *fixture {
	t.Helper()
	h := testhost.New(t)
	// A website account: home is <sites_root>/<domain>.
	f, _ := os.OpenFile(filepath.Join(h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "shop:x:%d:%d::/home/whacp/shop.test:/usr/sbin/nologin\n", h.UID, h.GID)
	f.Close()
	for _, d := range []string{"home/whacp/shop.test/public_html/uploads", "home/whacp/blog.test/public_html", "usr/sbin"} {
		os.MkdirAll(filepath.Join(h.Base, d), 0o755)
	}
	os.WriteFile(filepath.Join(h.Base, "usr/sbin/pure-ftpd"), []byte("#!/bin/true\n"), 0o755)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	probe = func(context.Context, string) error { return nil }
	t.Cleanup(func() { probe = func(context.Context, string) error { return nil } })
	return &fixture{h: h, r: r}
}

func (f *fixture) call(t *testing.T, name string, params any) (map[string]any, error) {
	t.Helper()
	a, err := f.r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(params)
	res, err := a.Handler(context.Background(), b)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(out, &m)
	return m, nil
}

func (f *fixture) must(t *testing.T, name string, params any) map[string]any {
	t.Helper()
	m, err := f.call(t, name, params)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

func code(err error) string {
	var ae *actions.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func called(f *fixture, prefix string) bool {
	for _, c := range f.h.Runner.Calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func TestConfigureNeedsConfirmationToReplaceAnotherServer(t *testing.T) {
	f := setup(t)
	// The test host reports every unit as active, so pure-ftpd/vsftpd look busy.
	if _, err := f.call(t, "ftp.configure", map[string]any{}); code(err) != "conflict" {
		t.Fatalf("err = %v", err)
	}
	if f.h.Exists("etc/whacp-ftp/pure-ftpd.conf") {
		t.Fatal("configuration written without confirmation")
	}
	res := f.must(t, "ftp.configure", map[string]any{"confirm_replace_service": true, "passive_min": 50000, "passive_max": 50100})
	conf := f.h.Read("etc/whacp-ftp/pure-ftpd.conf")
	for _, want := range []string{"ChrootEveryone         yes", "TLS                    2", "PassivePortRange       50000 50100", "PureDB                 /etc/whacp-ftp/accounts.pdb", "NoAnonymous            yes"} {
		if !strings.Contains(conf, want) {
			t.Errorf("config lacks %q:\n%s", want, conf)
		}
	}
	if !strings.Contains(f.h.Read("etc/systemd/system/whacp-ftpd.service"), "ExecStart=/usr/sbin/pure-ftpd /etc/whacp-ftp/pure-ftpd.conf") {
		t.Fatal("unit not written")
	}
	info, err := os.Stat(filepath.Join(f.h.Base, "etc/whacp-ftp/pure-ftpd.pem"))
	if err != nil || info.Mode().Perm() != 0o600 || !strings.Contains(f.h.Read("etc/whacp-ftp/pure-ftpd.pem"), "BEGIN CERTIFICATE") {
		t.Fatalf("certificate: %v %v", info, err)
	}
	for _, c := range []string{"pure-pw mkdb /etc/whacp-ftp/accounts.pdb -f /etc/whacp-ftp/accounts.passwd", "systemctl stop -- pure-ftpd.service", "systemctl disable -- vsftpd.service", "systemctl enable -- whacp-ftpd.service", "systemctl restart -- whacp-ftpd.service"} {
		if !called(f, c) {
			t.Errorf("missing call %q in %v", c, f.h.Runner.Calls)
		}
	}
	if res["certificate"] == nil || Parse(conf).PassiveMin != 50000 {
		t.Fatalf("result = %+v", res)
	}
	// The certificate is kept on later changes.
	cert := f.h.Read("etc/whacp-ftp/pure-ftpd.pem")
	f.must(t, "ftp.configure", map[string]any{"confirm_replace_service": true, "tls": "optional"})
	if f.h.Read("etc/whacp-ftp/pure-ftpd.pem") != cert || !strings.Contains(f.h.Read("etc/whacp-ftp/pure-ftpd.conf"), "TLS                    1") {
		t.Fatal("certificate replaced or TLS not changed")
	}
	for _, bad := range []map[string]any{{"passive_min": 80, "passive_max": 90}, {"tls": "none"}, {"passive_ip": "not-an-ip"}, {"max_clients": 0}} {
		bad["confirm_replace_service"] = true
		if _, err := f.call(t, "ftp.configure", bad); code(err) != "invalid_params" {
			t.Errorf("%v accepted: %v", bad, err)
		}
	}
}

func TestConfigureRollsBackWhenTheServerDoesNotAnswer(t *testing.T) {
	f := setup(t)
	probe = func(context.Context, string) error { return errors.New("connection refused") }
	_, err := f.call(t, "ftp.configure", map[string]any{"confirm_replace_service": true})
	if code(err) != "failed" || f.h.Exists("etc/whacp-ftp/pure-ftpd.conf") || f.h.Exists("etc/systemd/system/whacp-ftpd.service") {
		t.Fatalf("err = %v; files kept", err)
	}
	if !called(f, "systemctl start -- pure-ftpd.service") {
		t.Fatal("the replaced FTP server was not started again")
	}
}

func TestAccounts(t *testing.T) {
	f := setup(t)
	if _, err := f.call(t, "ftp.account_save", map[string]any{"name": "a", "user": "shop", "password": "long-enough-pw"}); code(err) != "conflict" {
		t.Fatalf("before setup: %v", err)
	}
	f.must(t, "ftp.configure", map[string]any{"confirm_replace_service": true})
	f.must(t, "ftp.account_save", map[string]any{"name": "Dev@Shop.test", "user": "shop", "dir": "public_html", "password": "long-enough-pw", "quota_mb": 500})
	line := strings.TrimSpace(f.h.Read("etc/whacp-ftp/accounts.passwd"))
	fields := strings.Split(line, ":")
	if len(fields) != 18 || fields[0] != "dev@shop.test" || !strings.HasPrefix(fields[1], "$6$rounds=100000$") || fields[5] != "/home/whacp/shop.test/public_html/./" || fields[12] != "524288000" {
		t.Fatalf("passwd line = %q", line)
	}
	if strings.Contains(line, "long-enough-pw") {
		t.Fatal("clear-text password stored")
	}
	hash := fields[1]
	// Update without a password keeps the hash.
	f.must(t, "ftp.account_save", map[string]any{"name": "dev@shop.test", "user": "shop", "dir": "public_html/uploads"})
	fields = strings.Split(strings.TrimSpace(f.h.Read("etc/whacp-ftp/accounts.passwd")), ":")
	if fields[1] != hash || fields[5] != "/home/whacp/shop.test/public_html/uploads/./" || fields[12] != "" {
		t.Fatalf("update = %v", fields)
	}
	os.Symlink("/etc", filepath.Join(f.h.Base, "home/whacp/shop.test/etc"))
	for _, bad := range []map[string]any{
		{"name": "new", "user": "shop"},                                                    // no password
		{"name": "x y", "user": "shop", "password": "long-enough-pw"},                      // bad name
		{"name": "x", "user": "shop", "password": "short"},                                 // short password
		{"name": "x", "user": "shop", "dir": "../blog.test", "password": "long-enough-pw"}, // outside (becomes shop.test/blog.test)
		{"name": "x", "user": "shop", "dir": "etc", "password": "long-enough-pw"},          // symlink
		{"name": "x", "user": "root", "password": "long-enough-pw"},                        // not a website user
		{"name": "x", "user": "example", "password": "long-enough-pw"},                     // home outside the sites root
	} {
		if _, err := f.call(t, "ftp.account_save", bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	st := f.must(t, "ftp.status", map[string]any{"user": "shop"})
	if accts := st["accounts"].([]any); len(accts) != 1 || accts[0].(map[string]any)["dir"] != "/home/whacp/shop.test/public_html/uploads" {
		t.Fatalf("status accounts = %+v", st["accounts"])
	}
	if strings.Contains(fmt.Sprint(st), "$6$") {
		t.Fatal("status exposes password hashes")
	}
	f.must(t, "ftp.account_save", map[string]any{"name": "second", "user": "shop", "password": "long-enough-pw"})
	f.must(t, "ftp.account_delete", map[string]any{"name": "second"})
	if _, err := f.call(t, "ftp.account_delete", map[string]any{"name": "second"}); code(err) != "not_found" {
		t.Fatalf("second delete: %v", err)
	}
	// A failing database rebuild restores the previous list.
	f.h.Fail["pure-pw mkdb"] = "cannot write"
	before := f.h.Read("etc/whacp-ftp/accounts.passwd")
	if _, err := f.call(t, "ftp.account_save", map[string]any{"name": "third", "user": "shop", "password": "long-enough-pw"}); err == nil {
		t.Fatal("expected failure")
	}
	if f.h.Read("etc/whacp-ftp/accounts.passwd") != before {
		t.Fatal("accounts not restored")
	}
	delete(f.h.Fail, "pure-pw mkdb")
	// Deleting the website removes its accounts.
	if err := RemoveSite(context.Background(), f.h.Env, "shop.test"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(f.h.Read("etc/whacp-ftp/accounts.passwd")) != "" {
		t.Fatalf("accounts left: %q", f.h.Read("etc/whacp-ftp/accounts.passwd"))
	}
}

const testKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl laptop"

func sshd(f *fixture, rootAfter map[string]string) {
	calls := 0
	f.h.Respond = func(id string, args []string) (string, error, bool) {
		if id != "sshd" || len(args) < 3 || args[0] != "-T" {
			return "", nil, false
		}
		if strings.Contains(args[2], "user=root,") {
			calls++
			cfg := map[string]string{"chrootdirectory": "none", "forcecommand": "none", "x11forwarding": "no", "passwordauthentication": "no"}
			if calls > 1 {
				for k, v := range rootAfter {
					cfg[k] = v
				}
			}
			var b strings.Builder
			for k, v := range cfg {
				b.WriteString(k + " " + v + "\n")
			}
			return b.String(), nil, true
		}
		return "chrootdirectory /home/whacp\nforcecommand internal-sftp -d /shop.test -u 0027\n", nil, true
	}
}

func TestSFTP(t *testing.T) {
	f := setup(t)
	sshd(f, nil)
	for _, bad := range []map[string]any{
		{"user": "shop", "enabled": true},                                              // no key, no password
		{"user": "shop", "enabled": true, "password_login": true},                      // password login without password
		{"user": "shop", "enabled": true, "keys": []string{`command="sh" ` + testKey}}, // options
		{"user": "shop", "enabled": true, "keys": []string{"ssh-ed25519 notbase64!"}},
		{"user": "shop", "enabled": true, "keys": []string{"ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"}}, // type mismatch
		{"user": "root", "enabled": true, "keys": []string{testKey}},
	} {
		if _, err := f.call(t, "sftp.configure", bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	res := f.must(t, "sftp.configure", map[string]any{"user": "shop", "enabled": true, "keys": []string{testKey, testKey}})
	if res["start_dir"] != "/shop.test" || res["keys"].(float64) != 1 {
		t.Fatalf("result = %+v", res)
	}
	drop := f.h.Read("etc/ssh/sshd_config.d/50-whacp-sftp.conf")
	for _, want := range []string{"Match User shop\n", "ChrootDirectory /home/whacp\n", "ForceCommand internal-sftp -d /shop.test -u 0027\n", "AuthorizedKeysFile /etc/ssh/whacp-keys/%u\n", "PasswordAuthentication no\n", "AllowTcpForwarding no\n", "PermitTTY no\n"} {
		if !strings.Contains(drop, want) {
			t.Errorf("drop-in lacks %q:\n%s", want, drop)
		}
	}
	if strings.TrimSpace(f.h.Read("etc/ssh/whacp-keys/shop")) != testKey {
		t.Fatalf("keys = %q", f.h.Read("etc/ssh/whacp-keys/shop"))
	}
	if !called(f, "sshd -t") || !called(f, "systemctl reload -- ssh") || !called(f, "usermod --lock -- shop") {
		t.Fatalf("calls = %v", f.h.Runner.Calls)
	}
	st := f.must(t, "ftp.status", map[string]any{"user": "shop"})
	if s := st["sftp"].([]any); len(s) != 1 || s[0].(map[string]any)["keys"].([]any)[0].(map[string]any)["fingerprint"] == "" {
		t.Fatalf("status sftp = %+v", st["sftp"])
	}
	// A password is set hashed through chpasswd.
	f.must(t, "sftp.configure", map[string]any{"user": "shop", "enabled": true, "password_login": true, "password": "another-long-pw"})
	if !called(f, "chpasswd -e") || !strings.Contains(f.h.Read("etc/ssh/sshd_config.d/50-whacp-sftp.conf"), "PasswordAuthentication yes") {
		t.Fatal("password login not enabled")
	}
	// Disabling removes the block and the key file.
	f.must(t, "sftp.configure", map[string]any{"user": "shop", "enabled": false})
	if strings.Contains(f.h.Read("etc/ssh/sshd_config.d/50-whacp-sftp.conf"), "Match") || f.h.Exists("etc/ssh/whacp-keys/shop") {
		t.Fatal("SFTP not removed")
	}
}

func TestSFTPRollsBackWhenOtherUsersWouldBeAffected(t *testing.T) {
	f := setup(t)
	sshd(f, map[string]string{"x11forwarding": "yes"})
	_, err := f.call(t, "sftp.configure", map[string]any{"user": "shop", "enabled": true, "keys": []string{testKey}})
	var ae *actions.Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Message+ae.Details, "x11forwarding") {
		t.Fatalf("err = %+v", err)
	}
	if f.h.Exists("etc/ssh/sshd_config.d/50-whacp-sftp.conf") || f.h.Exists("etc/ssh/whacp-keys/shop") || f.h.Exists("var/lib/whacp-agent/sftp.json") {
		t.Fatal("changes not rolled back")
	}
	if called(f, "systemctl reload") {
		t.Fatal("sshd reloaded after a failed validation")
	}
}

func TestAccountSuspension(t *testing.T) {
	f := setup(t)
	os.MkdirAll(filepath.Join(f.h.Base, "etc/whacp-ftp"), 0o755)
	os.WriteFile(filepath.Join(f.h.Base, "etc/whacp-ftp/accounts.passwd"), nil, 0o600)
	f.must(t, "ftp.account_save", map[string]any{"name": "shopftp", "user": "shop", "password": "long-ftp-password"})
	hash := func() string {
		line := strings.TrimSpace(f.h.Read("etc/whacp-ftp/accounts.passwd"))
		return strings.Split(line, ":")[1]
	}
	if strings.HasPrefix(hash(), "!") {
		t.Fatal("locked before suspension")
	}
	res := f.must(t, "account.suspend", map[string]any{"user": "shop", "suspended": true})
	if res["ftp_accounts"].(float64) != 1 || !called(f, "usermod --expiredate 1 -- shop") || !strings.HasPrefix(hash(), "!$6$") {
		t.Fatalf("suspend: %v, hash %q, calls %v", res, hash(), f.h.Runner.Calls)
	}
	// A new password set while suspended stays locked.
	os.WriteFile(filepath.Join(f.h.Base, "etc/shadow"), []byte("shop:!:20000:0:99999:7::1:\n"), 0o600)
	f.must(t, "ftp.account_save", map[string]any{"name": "shopftp", "user": "shop", "password": "another-ftp-password"})
	if !strings.HasPrefix(hash(), "!$6$") {
		t.Fatalf("password change unlocked the account: %q", hash())
	}
	f.must(t, "account.suspend", map[string]any{"user": "shop", "suspended": false})
	if strings.HasPrefix(hash(), "!") || !called(f, "usermod --expiredate  -- shop") {
		t.Fatalf("resume: hash %q, calls %v", hash(), f.h.Runner.Calls)
	}
	for _, bad := range []string{"root", "www-data", "nobody-here"} {
		if _, err := f.call(t, "account.suspend", map[string]any{"user": bad, "suspended": true}); err == nil {
			t.Errorf("suspended %s", bad)
		}
	}
}

func TestShellModes(t *testing.T) {
	f := setup(t)
	f.h.Respond = func(id string, args []string) (string, error, bool) {
		if id != "sshd" || len(args) < 3 || args[0] != "-T" {
			return "", nil, false
		}
		if strings.Contains(args[2], "user=root,") {
			return "chrootdirectory none\nforcecommand none\nx11forwarding no\npasswordauthentication no\n", nil, true
		}
		drop := f.h.Read("etc/ssh/sshd_config.d/50-whacp-sftp.conf")
		switch {
		case strings.Contains(drop, "ForceCommand "+JailShell):
			return "chrootdirectory none\nforcecommand " + JailShell + "\n", nil, true
		case strings.Contains(drop, "ChrootDirectory"):
			return "chrootdirectory /home/whacp\nforcecommand internal-sftp -d /shop.test -u 0027\n", nil, true
		}
		return "chrootdirectory none\nforcecommand none\n", nil, true
	}
	os.MkdirAll(filepath.Join(f.h.Base, "usr/bin"), 0o755)
	os.MkdirAll(filepath.Join(f.h.Base, "usr/local/sbin"), 0o755)

	// A mode is kept for later but has no effect without SSH/SFTP login.
	res := f.must(t, "shell.set", map[string]any{"users": []string{"shop"}, "mode": "normal"})
	if res["accounts"].([]any)[0].(map[string]any)["effective"] != "disabled" || called(f, "usermod --shell /bin/bash") {
		t.Fatalf("shell without SSH login: %v %v", res, f.h.Runner.Calls)
	}
	f.must(t, "sftp.configure", map[string]any{"user": "shop", "enabled": true, "keys": []string{testKey}})
	if !called(f, "usermod --shell /bin/bash -- shop") {
		t.Fatalf("normal shell not given with SSH login: %v", f.h.Runner.Calls)
	}
	drop := f.h.Read("etc/ssh/sshd_config.d/50-whacp-sftp.conf")
	if strings.Contains(drop, "ChrootDirectory") || !strings.Contains(drop, "PermitTTY yes") || !strings.Contains(drop, "AuthorizedKeysFile /etc/ssh/whacp-keys/%u") || !strings.Contains(drop, "AllowTcpForwarding no") {
		t.Fatalf("normal drop-in:\n%s", drop)
	}

	// Jailed needs bubblewrap.
	if _, err := f.call(t, "shell.set", map[string]any{"users": []string{"shop"}, "mode": "jailed"}); code(err) != "conflict" {
		t.Fatalf("jailed without bubblewrap: %v", err)
	}
	os.WriteFile(filepath.Join(f.h.Base, "usr/bin/bwrap"), []byte("ELF"), 0o755)
	f.must(t, "shell.set", map[string]any{"users": []string{"shop"}, "mode": "jailed"})
	drop = f.h.Read("etc/ssh/sshd_config.d/50-whacp-sftp.conf")
	if !strings.Contains(drop, "ForceCommand "+JailShell+"\n") || strings.Contains(drop, "ChrootDirectory") || !strings.Contains(drop, "PermitUserRC no") {
		t.Fatalf("jailed drop-in:\n%s", drop)
	}
	script := f.h.Read("usr/local/sbin/whacp-jailsh")
	for _, want := range []string{"SITES_ROOT='/home/whacp'", "--unshare-all", "--ro-bind /usr /usr", `--bind "$home" "$home"`, "--clearenv", "--ro-bind-try /etc/php /etc/php"} {
		if !strings.Contains(script, want) {
			t.Errorf("jail script lacks %q", want)
		}
	}
	if strings.Contains(script, "/etc/shadow") || strings.Contains(script, "--bind /etc") {
		t.Error("the jail exposes /etc")
	}
	if !called(f, "usermod --shell "+JailShell+" -- shop") || !called(f, "groupadd --system -- "+JailGroup) {
		t.Fatalf("jailed shell not set: %v", f.h.Runner.Calls)
	}

	// Turning SFTP off takes the shell away first. (The fake usermod does
	// not edit passwd; write what the real one would have.)
	pw := f.h.Read("etc/passwd")
	os.WriteFile(filepath.Join(f.h.Base, "etc/passwd"), []byte(strings.Replace(pw, "/home/whacp/shop.test:/usr/sbin/nologin", "/home/whacp/shop.test:"+JailShell, 1)), 0o644)
	f.h.Runner.Calls = nil
	f.must(t, "sftp.configure", map[string]any{"user": "shop", "enabled": false})
	if !called(f, "usermod --shell /usr/sbin/nologin -- shop") {
		t.Fatalf("shell kept after SSH login was turned off: %v", f.h.Runner.Calls)
	}
	for _, bad := range []map[string]any{{"users": []string{"root"}, "mode": "normal"}, {"users": []string{"shop"}, "mode": "root"}, {"users": []string{}, "mode": "jailed"}} {
		if _, err := f.call(t, "shell.set", bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

// A website moved to a new account takes its FTP accounts along, with
// their passwords; other websites' accounts stay as they are.
func TestMoveSiteKeepsPasswords(t *testing.T) {
	f := setup(t)
	fp, _ := os.OpenFile(filepath.Join(f.h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintf(fp, "blog:x:%d:%d::/home/whacp/blog.test:/usr/sbin/nologin\n", f.h.UID+1, f.h.GID+1)
	fmt.Fprintf(fp, "newshop:x:%d:%d::/home/whacp/shop.test:/usr/sbin/nologin\n", f.h.UID+2, f.h.GID+2)
	fp.Close()
	f.must(t, "ftp.configure", map[string]any{"confirm_replace_service": true})
	f.must(t, "ftp.account_save", map[string]any{"name": "shopdev", "user": "shop", "dir": "public_html", "password": "long-enough-pw"})
	f.must(t, "ftp.account_save", map[string]any{"name": "blogdev", "user": "blog", "password": "long-enough-pw"})
	lines := func() map[string][]string {
		m := map[string][]string{}
		for _, l := range strings.Split(strings.TrimSpace(f.h.Read("etc/whacp-ftp/accounts.passwd")), "\n") {
			fs := strings.Split(l, ":")
			m[fs[0]] = fs
		}
		return m
	}
	before := lines()
	// Only the account that now owns the website folder receives them.
	if _, err := f.call(t, "ftp.move_site", map[string]any{"domain": "shop.test", "user": "newshop"}); err == nil {
		t.Fatal("moved before the folder belongs to the new account")
	}
	if err := os.Chown(filepath.Join(f.h.Base, "home/whacp/shop.test"), f.h.UID+2, f.h.GID+2); err != nil { // what sites.apply does
		t.Skip("handing a folder to another account needs root")
	}
	if _, err := f.call(t, "ftp.move_site", map[string]any{"domain": "shop.test", "user": "blog"}); err == nil {
		t.Fatal("moved to another website's account")
	}
	if r := f.must(t, "ftp.move_site", map[string]any{"domain": "shop.test", "user": "newshop"}); r["moved"] != float64(1) {
		t.Fatalf("%v", r)
	}
	after := lines()
	if after["shopdev"][2] != fmt.Sprint(f.h.UID+2) || after["shopdev"][3] != fmt.Sprint(f.h.GID+2) || after["shopdev"][1] != before["shopdev"][1] {
		t.Fatalf("shopdev %v (before %v)", after["shopdev"], before["shopdev"])
	}
	if strings.Join(after["blogdev"], ":") != strings.Join(before["blogdev"], ":") {
		t.Fatal("another website's account changed")
	}
}

// A website account that has (or was given) administrator rights never gets
// SSH/SFTP keys, a password or a shell through WhaCP.
func TestAdministratorAccountsGetNoAccess(t *testing.T) {
	f := setup(t)
	g, _ := os.OpenFile(filepath.Join(f.h.Base, "etc/group"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	g.WriteString("sudo:x:27:shop\n")
	g.Close()
	for _, c := range []struct {
		action string
		params map[string]any
	}{
		{"sftp.configure", map[string]any{"user": "shop", "enabled": true, "keys": []string{testKey}}},
		{"shell.set", map[string]any{"users": []string{"shop"}, "mode": "normal"}},
	} {
		if _, err := f.call(t, c.action, c.params); code(err) != "invalid_params" || !strings.Contains(err.Error(), "administrator rights") {
			t.Fatalf("%s for a sudo member: %v", c.action, err)
		}
	}
	if called(f, "usermod") {
		t.Fatalf("account changed: %v", f.h.Runner.Calls)
	}
}

// The server setup installs pure-ftpd, which starts the package's own service;
// with no virtual accounts of its own that is not "another FTP server".
func TestUnusedStockPureFTPdMayBeReplacedBySetup(t *testing.T) {
	f := setup(t)
	only := []string{"pure-ftpd.service"}
	if !unusedStockOnly(f.h.Env, only) {
		t.Error("a fresh stock service was treated as somebody's server")
	}
	// Accounts in it: somebody uses it.
	os.MkdirAll(filepath.Join(f.h.Base, "etc/pure-ftpd"), 0o755)
	os.WriteFile(filepath.Join(f.h.Base, "etc/pure-ftpd/pureftpd.passwd"), []byte("bob:$1$x:1000:1000::/home/bob/./::::::::::::\n"), 0o600)
	if unusedStockOnly(f.h.Env, only) {
		t.Error("a pure-ftpd with virtual accounts was replaced")
	}
	os.WriteFile(filepath.Join(f.h.Base, "etc/pure-ftpd/pureftpd.passwd"), nil, 0o600)
	if !unusedStockOnly(f.h.Env, only) {
		t.Error("an empty account file counted as in use")
	}
	// Another server running: never replaced without confirmation.
	if unusedStockOnly(f.h.Env, []string{"pure-ftpd.service", "vsftpd.service"}) || unusedStockOnly(f.h.Env, []string{"vsftpd.service"}) {
		t.Error("another FTP server was treated as replaceable")
	}
	// The test host reports every unit active, so the action still asks.
	if _, err := f.call(t, "ftp.configure", map[string]any{"replace_unused_stock": true}); code(err) != "conflict" {
		t.Fatalf("err = %v", err)
	}
}

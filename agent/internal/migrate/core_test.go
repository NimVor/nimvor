package migrate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// fakeT answers commands from a table: the first key contained in the command wins.
type fakeT struct {
	answers []struct{ key, out string }
	err     map[string]error
	cmds    []string
}

func (f *fakeT) on(key, out string) *fakeT {
	f.answers = append(f.answers, struct{ key, out string }{key, out})
	return f
}
func (f *fakeT) Describe() string { return "fake" }
func (f *fakeT) Exec(ctx context.Context, cmd string, stdout io.Writer) (string, error) {
	f.cmds = append(f.cmds, cmd)
	for k, e := range f.err {
		if strings.Contains(cmd, k) {
			return "", e
		}
	}
	for _, a := range f.answers {
		if strings.Contains(cmd, a.key) {
			if stdout != nil {
				io.WriteString(stdout, a.out)
				return "", nil
			}
			return a.out, nil
		}
	}
	return "", nil
}

const infoOut = `##os
PRETTY_NAME="AlmaLinux 8.9 (Midnight Oncilla)"
##id
0
##host
src1.example.net
##php
/usr/bin/php 8.1
/opt/cpanel/ea-php74/root/usr/bin/php 7.4
/opt/cpanel/ea-php81/root/usr/bin/php 8.1
##mysql
mysql  Ver 15.1 Distrib 10.6.16-MariaDB, for Linux (x86_64) using readline 5.1
##df
Filesystem     1024-blocks      Used Available Capacity Mounted on
/dev/vda1         41152812  30000000  11152812      74% /
##tools
/usr/bin/rsync
/usr/bin/mysqldump
##svc
nginx inactive
httpd active
mariadb active
named active
##end
`

func TestParseInfoReadsTheFactsOfAServer(t *testing.T) {
	i := parseInfo(infoOut)
	if i.OS != "AlmaLinux 8.9 (Midnight Oncilla)" || !i.Root || i.Hostname != "src1.example.net" {
		t.Fatalf("info = %+v", i)
	}
	if strings.Join(i.PHP, ",") != "7.4,8.1" {
		t.Errorf("php = %v", i.PHP)
	}
	if i.Engine != "mariadb" || i.MySQL != "10.6.16" {
		t.Errorf("db = %s %s", i.Engine, i.MySQL)
	}
	if len(i.Disk) != 1 || i.Disk[0].FreeKB != 11152812 || !i.Rsync || !i.Mysqldump {
		t.Errorf("disk/tools = %+v", i)
	}
	if i.Services["httpd"] != "active" || i.Services["nginx"] != "inactive" {
		t.Errorf("services = %v", i.Services)
	}
	for in, want := range map[string][2]string{
		"mysql  Ver 8.0.33 for Linux on x86_64 (MySQL Community Server - GPL)":   {"mysql", "8.0.33"},
		"mariadb from 11.4.2-MariaDB, client 15.2 for debian-linux-gnu (x86_64)": {"mariadb", "11.4.2"},
		"mysql  Ver 14.14 Distrib 5.7.44, for Linux (x86_64)":                    {"mysql", "5.7.44"},
	} {
		got := parseInfo("##mysql\n" + in + "\n")
		if got.Engine != want[0] || got.MySQL != want[1] {
			t.Errorf("%q → %s %s", in, got.Engine, got.MySQL)
		}
	}
}

const cpMain = `---
addon_domains:
  shop.net: shop.example.com
cp_php_magic_include_path.conf: ~
main_domain: example.com
parked_domains:
- alias.org
sub_domains:
- blog.example.com
- shop.example.com
`

func cpAccountOut() string {
	return "##file /var/cpanel/userdata/alice/main\n" + cpMain +
		"##file /var/cpanel/userdata/alice/example.com\ndocumentroot: /home/alice/public_html\nphpversion: ea-php81\nserveralias: www.example.com\n" +
		"##file /var/cpanel/userdata/alice/blog.example.com\ndocumentroot: /home/alice/public_html/blog\nphpversion: ea-php81\n" +
		"##file /var/cpanel/userdata/alice/shop.example.com\ndocumentroot: /home/alice/shop.net\nphpversion: ea-php74\n" +
		"##home\n/home/alice\n" +
		"##mail\n##dom example.com\ninfo\nsales\n##dom shop.net\norders\n" +
		"##cron\n# comment\nMAILTO=a@example.com\n*/5 * * * * /usr/bin/php /home/alice/public_html/cron.php\n@daily /home/alice/backup.sh\n"
}

func cpTransport() *fakeT {
	f := &fakeT{}
	f.on("/usr/local/cpanel/version", "110.0.12\n")
	f.on("/etc/trueuserdomains", "example.com: alice\nbob.org: bob\nnobody.test: nobody\nbad domain: x\n")
	f.on("information_schema.tables", "alice_wp\t2048\nalice_shop\t512\nbob_db\t10\nother\t5\n")
	f.on("userdata/alice", cpAccountOut())
	f.on("userdata/bob", "##file /var/cpanel/userdata/bob/main\nmain_domain: bob.org\n##file /var/cpanel/userdata/bob/bob.org\ndocumentroot: /home/bob/public_html\n##home\n/home/bob\n##mail\n##cron\n")
	f.on("du -sk", "1500\t/home/alice/public_html\n300\t/home/alice/public_html/blog\n700\t/home/alice/shop.net\n40\t/home/bob/public_html\n")
	f.on("sh -c", infoOut)
	return f
}

func TestCpanelDiscoveryFromItsFileLayout(t *testing.T) {
	f := cpTransport()
	d, err := DiscoverAll(context.Background(), f, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Panel.Panel != "cpanel" || d.Panel.Version != "110.0.12" || d.Panel.Support != Experimental {
		t.Fatalf("panel = %+v", d.Panel)
	}
	if len(d.Accounts) != 2 {
		t.Fatalf("accounts = %+v", d.Accounts)
	}
	a := d.Accounts[0]
	if a.Name != "alice" || a.Home != "/home/alice" || a.PHP != "8.1" {
		t.Errorf("alice = %+v", a)
	}
	kinds := map[string]string{}
	for _, x := range a.Domains {
		kinds[x.Name] = x.Kind
	}
	want := map[string]string{"example.com": "main", "blog.example.com": "sub", "shop.example.com": "sub"}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("%s kind = %q, want %q (%v)", k, kinds[k], v, kinds)
		}
	}
	if a.Domains[0].Name != "example.com" || a.Domains[0].DocRoot != "/home/alice/public_html" || a.Domains[0].SizeKB != 1500 {
		t.Errorf("main domain = %+v", a.Domains[0])
	}
	var dbs []string
	for _, db := range a.Databases {
		dbs = append(dbs, db.Name)
	}
	if strings.Join(dbs, ",") != "alice_shop,alice_wp" {
		t.Errorf("databases = %v (a database of another account or without the prefix must not be listed)", dbs)
	}
	if a.Databases[1].SizeKB != 2048 || a.Databases[1].Engine != "mariadb" {
		t.Errorf("db = %+v", a.Databases[1])
	}
	var mail []string
	for _, m := range a.Email {
		mail = append(mail, m.Address)
	}
	if strings.Join(mail, ",") != "info@example.com,sales@example.com,orders@shop.net" || a.Email[0].Maildir != "/home/alice/mail/example.com/info" {
		t.Errorf("mail = %+v", a.Email)
	}
	if len(a.Cron) != 2 || a.Cron[0].Schedule != "*/5 * * * *" || a.Cron[1].Schedule != "@daily" {
		t.Errorf("cron = %+v", a.Cron)
	}
	if d.Accounts[1].Name != "bob" || len(d.Accounts[1].Domains) != 1 {
		t.Errorf("bob = %+v", d.Accounts[1])
	}
	if len(d.NotMigrated) == 0 || !strings.Contains(strings.Join(d.NotMigrated, " "), "DNS") {
		t.Errorf("not migrated = %v", d.NotMigrated)
	}
	// Every command is built from fixed text; account names went through quoting and a strict name check.
	for _, c := range f.cmds {
		if strings.Contains(c, "nobody") || strings.Contains(c, "bad domain") {
			t.Errorf("a refused account reached a command: %s", c)
		}
	}
}

func TestADifferentPanelIsNotTakenForCpanel(t *testing.T) {
	f := &fakeT{}
	f.on("/usr/local/cpanel/version", "")
	f.on("os-release", "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nLinux 6.1\n")
	a, det, err := DetectPanel(context.Background(), f, "")
	if err != nil || a.ID() != "linux" || det.Version != "Debian GNU/Linux 12 (bookworm)" {
		t.Fatalf("a=%v det=%+v err=%v", a, det, err)
	}
	if _, _, err := DetectPanel(context.Background(), f, "cpanel"); err == nil {
		t.Error("cPanel was assumed on a server without it")
	}
	if _, _, err := DetectPanel(context.Background(), f, "plesk"); err == nil || !strings.Contains(err.Error(), "does not look like Plesk") {
		t.Errorf("plesk = %v", err)
	}
	if _, _, err := DetectPanel(context.Background(), f, "hestiacp"); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("hestiacp = %v", err)
	}
}

const webConf = `##file /etc/nginx/sites-enabled/shop
server {
    listen 80;
    server_name shop.example.com www.shop.example.com;
    root /var/www/shop/public;
    location / { try_files $uri /index.php; }
    location ~ \.php$ { fastcgi_pass unix:/run/php.sock; }
}
server {
    listen 80 default_server;
    server_name _;
    root /var/www/html;
}
##file /etc/apache2/sites-enabled/blog.conf
<VirtualHost *:80>
    ServerName blog.example.org
    DocumentRoot "/var/www/blog"
    <Directory "/var/www/blog">
        AllowOverride All
    </Directory>
</VirtualHost>
##file /etc/nginx/conf.d/evil.conf
server {
    server_name evil.example.net;
    root /var/www/../../etc;
}
`

func TestCustomLinuxDiscoveryReadsNginxAndApacheAndSchemas(t *testing.T) {
	f := &fakeT{}
	f.on("/etc/nginx/sites-enabled/*", webConf)
	f.on("information_schema.tables", "shopdb\t4096\nblog\t100\n")
	f.on("du -sk", "9000\t/var/www/shop/public\n1000\t/var/www/blog\n")
	f.on("os-release", "PRETTY_NAME=\"Ubuntu 22.04.4 LTS\"\n")
	f.on("sh -c", infoOut)
	d, err := DiscoverAll(context.Background(), f, "linux", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := d.Accounts[0]
	got := map[string]string{}
	for _, x := range a.Domains {
		got[x.Name] = x.DocRoot
	}
	want := map[string]string{"shop.example.com": "/var/www/shop/public", "www.shop.example.com": "/var/www/shop/public", "blog.example.org": "/var/www/blog"}
	if len(got) != len(want) {
		t.Fatalf("sites = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, bad := got["evil.example.net"]; bad {
		t.Error("a document root with .. was accepted")
	}
	if _, bad := got["_"]; bad {
		t.Error("the catch-all server was listed as a site")
	}
	if len(a.Databases) != 2 || a.Databases[0].Name != "blog" || a.Databases[1].SizeKB != 4096 {
		t.Errorf("databases = %+v", a.Databases)
	}
	if a.Domains[0].SizeKB <= 0 && a.Domains[1].SizeKB <= 0 {
		t.Errorf("sizes = %+v", a.Domains)
	}
}

func TestAConnectionFailureIsReportedNotHidden(t *testing.T) {
	f := &fakeT{err: map[string]error{"id -u": errors.New("Permission denied (publickey).")}}
	if _, err := DiscoverAll(context.Background(), f, "", nil); err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("err = %v", err)
	}
}

func dest() DestInfo {
	return DestInfo{PHP: []string{"8.2", "8.3"}, MySQL: "10.11.6", Engine: "mariadb", FreeKB: 50 << 20, TempKB: 5 << 20, Reserve: 512 << 10}
}

func findCodes(f []Finding) map[string]string {
	m := map[string]string{}
	for _, x := range f {
		m[x.Code] = x.Severity
	}
	return m
}

func TestAssessmentFindsWhatBlocksAndWhatNeedsAttention(t *testing.T) {
	d := &Discovery{Server: ServerInfo{Root: true, Rsync: true, Mysqldump: true, Engine: "mysql", MySQL: "8.0.33"},
		Accounts: []Account{{Name: "alice", PHP: "7.4"}}, NotMigrated: []string{"DNS zones and records"}}
	p := &Plan{Files: []FileTask{{Domain: "a.com", SizeKB: 1 << 20}}, Databases: []DBTask{{Source: "a", Dest: "a", SizeKB: 1 << 20}}}
	f := Assess(d, p, dest())
	c := findCodes(f)
	if Blocking(f) {
		t.Errorf("a healthy plan is blocked: %+v", f)
	}
	for code, sev := range map[string]string{"mysql8_to_mariadb": Warning, "php_version": Warning, "htaccess": Info, "not_migrated": Info} {
		if c[code] != sev {
			t.Errorf("%s = %q, want %q", code, c[code], sev)
		}
	}
	if f[0].Severity == Info && len(f) > 1 && f[len(f)-1].Severity != Info {
		t.Error("findings are not ordered critical, warning, info")
	}

	// Critical: no space, no rsync, no mysqldump, no database here.
	d.Server.Rsync, d.Server.Mysqldump = false, false
	p.Files[0].SizeKB = 60 << 20
	small := dest()
	small.TempKB = 1024
	small.Engine = ""
	f = Assess(d, p, small)
	c = findCodes(f)
	for _, code := range []string{"disk_space", "no_rsync_source", "no_mysqldump_source", "no_database_here", "temp_space"} {
		if c[code] != Critical {
			t.Errorf("%s = %q, want critical", code, c[code])
		}
	}
	if !Blocking(f) || f[0].Severity != Critical || f[0].Fix == "" {
		t.Errorf("first = %+v", f[0])
	}

	// Same engine, older destination.
	d = &Discovery{Server: ServerInfo{Root: false, Rsync: true, Mysqldump: true, Engine: "mariadb", MySQL: "11.4.2"}}
	f = Assess(d, &Plan{Databases: []DBTask{{Source: "a", Dest: "a"}}}, dest())
	c = findCodes(f)
	if c["database_downgrade"] != Warning || c["not_root"] != Warning {
		t.Errorf("codes = %v", c)
	}
	// Unknown sizes are said, not counted as zero.
	f = Assess(d, &Plan{Files: []FileTask{{Domain: "x", SizeKB: -1}}}, dest())
	if findCodes(f)["size_unknown"] != Warning {
		t.Errorf("codes = %v", findCodes(f))
	}
}

func TestPlanValidationKeepsDestinationsInsideNimVorWebsites(t *testing.T) {
	h := testhost.New(t)
	site := filepath.Join(h.Env.Cfg.SitesRoot, "example.com")
	real, _ := h.Env.Root.Path(site)
	os.MkdirAll(real, 0o755)
	user := "example"
	if u, err := h.Env.LookupUser(user); err == nil && os.Geteuid() == 0 {
		os.Chown(real, u.UID, u.GID)
	}
	ok := func() *Plan {
		return &Plan{Files: []FileTask{{Domain: "example.com", SourceRoot: "/home/alice/public_html", DestRoot: site, DestUser: user}}}
	}
	if err := ok().Validate(h.Env); err != nil {
		t.Fatalf("a good plan refused: %v", err)
	}
	cases := map[string]func(p *Plan){
		"outside the sites root": func(p *Plan) { p.Files[0].DestRoot = "/etc" },
		"parent of the root":     func(p *Plan) { p.Files[0].DestRoot = filepath.Dir(site) },
		"dot dot":                func(p *Plan) { p.Files[0].DestRoot = site + "/../other" },
		"missing website":        func(p *Plan) { p.Files[0].DestRoot = filepath.Join(h.Env.Cfg.SitesRoot, "missing.com") },
		"root as account":        func(p *Plan) { p.Files[0].DestUser = "root" },
		"source is /":            func(p *Plan) { p.Files[0].SourceRoot = "/" },
		"source one level":       func(p *Plan) { p.Files[0].SourceRoot = "/home" },
		"source with dot dot":    func(p *Plan) { p.Files[0].SourceRoot = "/home/alice/../root" },
		"source with a quote":    func(p *Plan) { p.Files[0].SourceRoot = "/home/alice/'; rm -rf /" },
		"bad exclude":            func(p *Plan) { p.Config.Excludes = []string{"$(reboot)"} },
		"exclude dot dot":        func(p *Plan) { p.Config.Excludes = []string{"../etc"} },
		"workers":                func(p *Plan) { p.Config.Workers = 99 },
		"two items one place":    func(p *Plan) { p.Files = append(p.Files, p.Files[0]) },
		"bad database name":      func(p *Plan) { p.Databases = []DBTask{{Source: "a;b", Dest: "x"}} },
		"nothing":                func(p *Plan) { p.Files, p.Databases = nil, nil },
	}
	for name, mutate := range cases {
		p := ok()
		mutate(p)
		if err := p.Validate(h.Env); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A website folder of another account is refused.
	if os.Geteuid() == 0 {
		other := filepath.Join(h.Env.Cfg.SitesRoot, "other.com")
		oreal, _ := h.Env.Root.Path(other)
		os.MkdirAll(oreal, 0o755)
		p := ok()
		p.Files[0].DestRoot = other
		if err := p.Validate(h.Env); err == nil {
			t.Error("a folder that belongs to someone else was accepted")
		}
	}
	// A symlink as the destination is refused.
	link := filepath.Join(h.Env.Cfg.SitesRoot, "link.com")
	lreal, _ := h.Env.Root.Path(link)
	os.Symlink("/tmp", lreal)
	p := ok()
	p.Files[0].DestRoot = link
	if err := p.Validate(h.Env); err == nil {
		t.Error("a symlink destination was accepted")
	}
}

func TestSourceValidation(t *testing.T) {
	good := Source{Host: "src.example.com", User: "root", Password: "pw", HostKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"}
	if err := good.Validate(true); err != nil || good.Port != 22 {
		t.Fatalf("good = %v (port %d)", err, good.Port)
	}
	bad := map[string]func(s *Source){
		"host with space":  func(s *Source) { s.Host = "a b" },
		"host with option": func(s *Source) { s.Host = "-oProxyCommand=x" },
		"port":             func(s *Source) { s.Port = 70000 },
		"user":             func(s *Source) { s.User = "root;id" },
		"no login":         func(s *Source) { s.Password, s.PrivateKey = "", "" },
		"newline password": func(s *Source) { s.Password = "a\nb" },
		"encrypted key": func(s *Source) {
			s.PrivateKey = "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\n-----END RSA PRIVATE KEY-----"
		},
		"not a key":        func(s *Source) { s.Password, s.PrivateKey = "", "hello" },
		"bad host key":     func(s *Source) { s.HostKey = "SHA256:abc" },
		"missing host key": func(s *Source) { s.HostKey = "" },
	}
	for name, mutate := range bad {
		s := good
		mutate(&s)
		if err := s.Validate(true); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	s := good
	s.HostKey = ""
	if err := s.Validate(false); err != nil {
		t.Errorf("the first contact needs no pinned key: %v", err)
	}
}

func TestShqQuotesEverything(t *testing.T) {
	for in, want := range map[string]string{"plain": "'plain'", "a b": "'a b'", "it's": `'it'\''s'`, "$(x);`y`": "'$(x);`y`'", "": "''"} {
		if got := Shq(in); got != want {
			t.Errorf("Shq(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestPathsForCommandsOnTheSource(t *testing.T) {
	for _, p := range []string{"/home/alice/public_html", "/var/www/my site", "/srv/a.b/c-d_e"} {
		if !ValidRemotePath(p) {
			t.Errorf("%q refused", p)
		}
	}
	for _, p := range []string{"", "relative/path", "/a/../b", "/a/./b", "/a//b", "/a;b", "/a$b", "/a`b`", "/a\nb", "/a'b", "/a|b"} {
		if ValidRemotePath(p) {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestACommandWithLineBreaksIsSentAsOneEncodedLine(t *testing.T) {
	if got := oneLine("echo hi"); got != "echo hi" {
		t.Errorf("a one-line command changed: %q", got)
	}
	got := oneLine("sh -c 'a\nb'")
	if strings.ContainsAny(got, "\n\r") || !strings.HasPrefix(got, "echo '") || !strings.HasSuffix(got, "' | base64 -d | sh") {
		t.Errorf("encoded = %q", got)
	}
	// Every script the adapters send goes through it.
	for _, c := range []string{"sh -c " + Shq(infoScript), "sh -c " + Shq(webConfigScript)} {
		if enc := oneLine(c); strings.ContainsAny(enc, "\n\r") {
			t.Error("a script still has line breaks")
		}
	}
}

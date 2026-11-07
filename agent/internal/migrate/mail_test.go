package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mailRig adds a vmail account and an empty NimVor mailbox to the standard rig.
func mailRig(t *testing.T) (*rig, string) {
	r := newRig(t)
	r.plan.Files, r.plan.Databases = nil, nil
	pw := filepath.Join(r.h.Base, "etc/passwd")
	f, _ := os.OpenFile(pw, os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "vmail:x:%d:%d::/var/vmail:/usr/sbin/nologin\n", r.h.UID, r.h.GID)
	f.Close()
	dir, err := r.h.Env.Root.Path(filepath.Join(r.h.Env.M.VmailDir, "example.com", "bob", "Maildir"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"cur", "new", "tmp"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
	}
	// The vmail account owns its mailboxes.
	if os.Geteuid() == 0 {
		os.Chown(dir, r.h.UID, r.h.GID)
		for _, d := range []string{"cur", "new", "tmp"} {
			os.Chown(filepath.Join(dir, d), r.h.UID, r.h.GID)
		}
	}
	r.plan.Mail = []MailTask{{Address: "bob@example.com", SourceMaildir: "/home/alice/mail/example.com/bob", SizeKB: 4}}
	r.src.rsyncFn = func(n int, args []string, local string, out io.Writer) error {
		for _, a := range args {
			if a == "-n" {
				return nil
			}
		}
		os.WriteFile(filepath.Join(local, "cur", "1700000000.M1.srv:2,S"), []byte("Subject: hi\n\nhello"), 0o600)
		os.WriteFile(filepath.Join(local, "new", "1700000001.M2.srv"), []byte("Subject: new\n\nworld"), 0o600)
		io.WriteString(out, ">f+++++++++ cur/1700000000.M1.srv:2,S\n>f+++++++++ new/1700000001.M2.srv\nNumber of regular files transferred: 2\nTotal transferred file size: 36 bytes\n")
		return nil
	}
	return r, dir
}

func TestAMailboxIsCopiedAndVerified(t *testing.T) {
	r, dir := mailRig(t)
	m := runDone(t, r)
	s := r.state(m.ID)
	if s.Status != Completed || s.Tasks[0].ID != "mail:bob@example.com" || s.Tasks[0].Files != 2 {
		t.Fatalf("state = %s %+v", s.Status, s.Tasks)
	}
	if _, err := os.Stat(filepath.Join(dir, "new", "1700000001.M2.srv")); err != nil {
		t.Error("message not copied")
	}
	args := strings.Join(r.src.rsyncs[0], " ")
	for _, want := range []string{"--chown=", "--exclude=dovecot.index*", "/home/alice/mail/example.com/bob/"} {
		if !strings.Contains(args, want) {
			t.Errorf("rsync args lack %q: %s", want, args)
		}
	}
	if strings.Contains(args, "--delete") {
		t.Error("a mail copy must never delete")
	}
}

func TestAMailboxWithMessagesIsNotFilledUnlessAllowed(t *testing.T) {
	r, dir := mailRig(t)
	os.WriteFile(filepath.Join(dir, "cur", "mine:2,S"), []byte("x"), 0o600)
	m := r.create()
	if err, _ := r.run(m.ID); err == nil {
		t.Fatal("a mailbox with messages was filled")
	}
	if r.src.rsyncCount() != 0 {
		t.Error("rsync ran")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "cur", "mine:2,S")); string(b) != "x" {
		t.Error("the existing message was touched")
	}
}

func TestPlanRefusesMailboxesThatDoNotExistOrAreBadlyNamed(t *testing.T) {
	r, _ := mailRig(t)
	for _, mt := range []MailTask{
		{Address: "ghost@example.com", SourceMaildir: "/home/alice/mail/example.com/ghost"},
		{Address: "not an address", SourceMaildir: "/home/alice/mail/example.com/bob"},
		{Address: "bob@example.com", SourceMaildir: "/mail"},
		{Address: "bob@example.com", SourceMaildir: "relative/path/x"},
	} {
		r.plan.Mail = []MailTask{mt}
		if err := r.plan.Validate(r.h.Env); err == nil {
			t.Errorf("%+v was accepted", mt)
		}
	}
}

func TestRollbackRemovesCopiedMessagesButKeepsReadOnes(t *testing.T) {
	r, dir := mailRig(t)
	m := runDone(t, r)
	// The user read the first message: Maildir renames it to add the flag.
	os.Rename(filepath.Join(dir, "cur", "1700000000.M1.srv:2,S"), filepath.Join(dir, "cur", "1700000000.M1.srv:2,SR"))
	os.WriteFile(filepath.Join(dir, "cur", "later:2,S"), []byte("arrived later"), 0o600)
	rep, err := r.e.Rollback(context.Background(), m.ID, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new", "1700000001.M2.srv")); err == nil {
		t.Error("an untouched copied message was not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "cur", "1700000000.M1.srv:2,SR")); err != nil {
		t.Error("a message that was read since was removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "cur", "later:2,S")); err != nil {
		t.Error("a message that arrived later was removed")
	}
	if len(rep.Kept) == 0 || !strings.Contains(strings.Join(rep.Kept, "\n"), "flags") {
		t.Errorf("kept = %v", rep.Kept)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("the mailbox itself was removed")
	}
}

func TestSyncCopiesMailAgainWithUpdate(t *testing.T) {
	r, _ := mailRig(t)
	m := runDone(t, r)
	if err := r.e.Sync(context.Background(), m.ID, r.source(), io.Discard); err != nil {
		t.Fatal(err)
	}
	var last string
	r.src.mu.Lock()
	for _, c := range r.src.rsyncs {
		if !strings.Contains(strings.Join(c, " "), " -n ") {
			last = strings.Join(c, " ")
		}
	}
	r.src.mu.Unlock()
	if !strings.Contains(last, "--update") {
		t.Errorf("a sync must not replace newer files: %s", last)
	}
}

func TestValidationFindsMissingAndChangedMessages(t *testing.T) {
	r, dir := mailRig(t)
	m := runDone(t, r)
	os.Rename(filepath.Join(dir, "cur", "1700000000.M1.srv:2,S"), filepath.Join(dir, "cur", "1700000000.M1.srv:2,SR"))
	os.Remove(filepath.Join(dir, "new", "1700000001.M2.srv"))
	rep, err := r.e.Validate(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	var txt strings.Builder
	for _, c := range rep.Checks {
		fmt.Fprintf(&txt, "%s|%s|%s|%s\n", c.Item, c.Name, c.Status, c.Detail)
	}
	if !strings.Contains(txt.String(), "bob@example.com") || !strings.Contains(strings.ToLower(txt.String()), "missing") {
		t.Errorf("checks = %s", txt.String())
	}
}

func TestCpanelMailboxSizesAreRead(t *testing.T) {
	a := Account{Home: "/home/alice"}
	parseCpanelAccount(&a, "\n##home\n/home/alice\n##mail\n##dom example.com\ninfo\nsales\n##msize\n120\t/home/alice/mail/example.com/info/\n##cron\n")
	if len(a.Email) != 2 || a.Email[0].SizeKB != 120 || a.Email[1].SizeKB != -1 {
		t.Errorf("email = %+v", a.Email)
	}
}

func TestAssessmentBlocksMailThatDoesNotFitAndSaysWhatIsNotMoved(t *testing.T) {
	p := &Plan{Mail: []MailTask{{Address: "a@example.com", SourceMaildir: "/h/a/mail/example.com/a", SizeKB: 500000}}}
	d := &Discovery{}
	d.Server.Rsync = true
	f := Assess(d, p, DestInfo{MailKB: 1000, FreeKB: 1 << 30, Reserve: 1})
	var space, scope bool
	for _, x := range f {
		space = space || x.Code == "mail_space" && x.Severity == Critical
		scope = scope || x.Code == "mail_scope"
	}
	if !space || !scope {
		t.Errorf("findings = %+v", f)
	}
	if !Blocking(f) {
		t.Error("not blocking")
	}
}

func TestCpanelForwardersAreReadAndOnlyPlainOnesKept(t *testing.T) {
	a := Account{Home: "/home/alice"}
	parseCpanelAccount(&a, "\n##home\n/home/alice\n##mail\n##fwd\n##vdom example.com\n# comment\nsales: Boss@Gmail.com, ops@example.org\n*: :fail: No Such User Here\nhelp: |/usr/bin/script\nlocal: bob\nbad name: x@y.com\n*: all@example.org\n##cron\n")
	if len(a.Forwarders) != 2 {
		t.Fatalf("forwarders = %+v", a.Forwarders)
	}
	if f := a.Forwarders[0]; f.Source != "sales@example.com" || strings.Join(f.Destinations, ",") != "boss@gmail.com,ops@example.org" {
		t.Errorf("first = %+v", f)
	}
	if a.Forwarders[1].Source != "*@example.com" {
		t.Errorf("catch-all = %+v", a.Forwarders[1])
	}
}

func TestReadingAZoneIsReadOnlyConfinedToTheZoneFileAndLimited(t *testing.T) {
	src := &fakeSrc{}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) {
		io.WriteString(stdout, "$TTL 14400\r\n@ IN MX 10 mail.example.com.\r\n")
		return "", nil
	}
	text, err := ReadZone(context.Background(), src, "cpanel", "example.com")
	if err != nil || strings.Contains(text, "\r") || !strings.Contains(text, "MX 10") {
		t.Fatalf("text = %q, %v", text, err)
	}
	cmd := src.execs[0]
	if !strings.Contains(cmd, "/var/named/$d.db") || !strings.Contains(cmd, "example.com") || strings.Contains(cmd, "rm ") || strings.Contains(cmd, ">") {
		t.Errorf("command = %s", cmd)
	}
	for _, d := range []string{"../etc/passwd", "a b", "x;rm -rf /", ""} {
		if _, err := ReadZone(context.Background(), src, "cpanel", d); err == nil {
			t.Errorf("%q was accepted", d)
		}
	}
	if _, err := ReadZone(context.Background(), src, "linux", "example.com"); err == nil {
		t.Error("a non-cPanel source was read")
	}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "", nil }
	if _, err := ReadZone(context.Background(), src, "cpanel", "example.com"); err == nil {
		t.Error("an empty result was accepted")
	}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) {
		stdout.Write(make([]byte, maxZoneBytes+1))
		return "", nil
	}
	if _, err := ReadZone(context.Background(), src, "cpanel", "example.com"); err == nil {
		t.Error("an oversized zone was accepted")
	}
}

const pleskOut = "##dom\n" +
	"1\texample.com\t0\t/var/www/vhosts/example.com/httpdocs\texample_user\t/var/www/vhosts/example.com\n" +
	"2\tblog.example.net\t1\t/var/www/vhosts/example.com/blog.example.net\texample_user\t/var/www/vhosts/example.com\n" +
	"3\tother.org\t0\t/var/www/vhosts/other.org/httpdocs\tother_user\t/var/www/vhosts/other.org\n" +
	"4\tparked.org\t0\t\t\t\n" +
	"5\tbad..name\t0\t/var/www/x\tx\t/var/www\n" +
	"ERROR: something odd\n" +
	"##sub\nshop\texample.com\t1\t/var/www/vhosts/example.com/shop.example.com\nbad label\texample.com\t1\t/x/y\n" +
	"##db\nexample_wp\t1\nother_db\t3\nbad name\t1\nsqlite\tx\n" +
	"##mail\ninfo\texample.com\t1\nSales\tother.org\t3\n$(x)\texample.com\t1\n" +
	"##end\n"

func TestPleskIsReadIntoAccountsOfSubscriptions(t *testing.T) {
	accts, warns := parsePlesk(pleskOut, map[string]int64{"example_wp": 900})
	if len(accts) != 3 {
		t.Fatalf("accounts = %d", len(accts))
	}
	a := accts[0]
	var names []string
	for _, d := range a.Domains {
		names = append(names, d.Name+":"+d.Kind)
	}
	if a.ID != "example.com" || a.Home != "/var/www/vhosts/example.com" || strings.Join(names, ",") != "example.com:main,blog.example.net:addon,shop.example.com:sub" {
		t.Errorf("account = %+v %v", a, names)
	}
	if len(a.Databases) != 1 || a.Databases[0].SizeKB != 900 || a.Databases[0].Name != "example_wp" {
		t.Errorf("databases = %+v", a.Databases)
	}
	if len(a.Email) != 1 || a.Email[0].Address != "info@example.com" || a.Email[0].Maildir != "/var/qmail/mailnames/example.com/info/Maildir" {
		t.Errorf("mail = %+v", a.Email)
	}
	b := accts[1]
	if b.ID != "other.org" || len(b.Databases) != 1 || b.Databases[0].SizeKB != -1 || b.Email[0].Address != "sales@other.org" {
		t.Errorf("other = %+v", b)
	}
	if len(accts[2].Domains) != 0 { // a domain without web hosting has no files to move
		t.Errorf("parked = %+v", accts[2])
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "not understood") {
		t.Errorf("warnings = %v", warns)
	}
}

func TestPleskIsDetectedByItsVersionFile(t *testing.T) {
	src := &fakeSrc{}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "18.0.57 Ubuntu 22.04\n", nil }
	det, ok, err := pleskAdapter{}.Detect(context.Background(), src)
	if err != nil || !ok || det.Version != "18.0.57" || det.Support != Experimental {
		t.Errorf("det = %+v %v %v", det, ok, err)
	}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "", nil }
	if _, ok, _ := (pleskAdapter{}).Detect(context.Background(), src); ok {
		t.Error("detected without a version file")
	}
	if _, err := Lookup("plesk"); err != nil {
		t.Error(err)
	}
}

func TestDirectAdminUserIsReadFromItsFiles(t *testing.T) {
	out := "##dom\nExample.com\nsecond.org\nbad..name\n##sub\n@@d example.com\nblog\nbad label\n@@d unknown.net\nx\n" +
		"##mail\n@@d example.com\ninfo\nSales\n$(x)\n@@d other.net\nz\n##end\n"
	a := parseDirectAdminUser("alice", out)
	var names []string
	for _, d := range a.Domains {
		names = append(names, d.Name+":"+d.Kind+":"+d.DocRoot)
	}
	want := "example.com:main:/home/alice/domains/example.com/public_html,second.org:addon:/home/alice/domains/second.org/public_html,blog.example.com:sub:/home/alice/domains/example.com/public_html/blog"
	if strings.Join(names, ",") != want {
		t.Errorf("domains = %v", names)
	}
	if len(a.Email) != 2 || a.Email[0].Address != "info@example.com" || a.Email[0].Maildir != "/home/alice/imap/example.com/info/Maildir" || a.Email[1].Address != "sales@example.com" {
		t.Errorf("mail = %+v", a.Email)
	}
}

func TestDirectAdminIsDetectedByItsVersion(t *testing.T) {
	src := &fakeSrc{}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "Version: 1.681\n", nil }
	det, ok, err := directAdminAdapter{}.Detect(context.Background(), src)
	if err != nil || !ok || det.Version != "1.681" || det.Support != Experimental {
		t.Errorf("det = %+v %v %v", det, ok, err)
	}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "", nil }
	if _, ok, _ := (directAdminAdapter{}).Detect(context.Background(), src); ok {
		t.Error("detected without DirectAdmin")
	}
	if _, err := Lookup("directadmin"); err != nil {
		t.Error(err)
	}
}

const fastPanelPlain = "ID  SERVER_NAME    ALIASES          OWNER         MODE    PHP_VERSION  IPS        DOCUMENT_ROOT\n" +
	"1   example.com    www.example.com  example_usr   mpm_itk 8.2          1.2.3.4    /var/www/example_usr/data/www/example.com\n" +
	"2   shop.test                       shop_usr      mpm_itk 8.1          1.2.3.4    /var/www/shop_usr/data/www/shop.test\n" +
	"3   bad..name                       x_usr         mpm_itk 8.1          1.2.3.4    /var/www/x_usr/data/www/bad\n" +
	"4   evil.test                       e_usr         mpm_itk 8.1          1.2.3.4    /var/www/../etc\n"

const fastPanelBoxed = "+----+-------------+---------+-------------+---------------------------------------+\n" +
	"| ID | SERVER_NAME | ALIASES | OWNER       | DOCUMENT_ROOT                         |\n" +
	"+----+-------------+---------+-------------+---------------------------------------+\n" +
	"| 1  | Example.com |         | example_usr | /var/www/example_usr/data/www/example.com |\n" +
	"| 2  | blog.example.com |    | example_usr | /var/www/example_usr/data/www/blog.example.com |\n" +
	"+----+-------------+---------+-------------+---------------------------------------+\n"

func TestFastPanelSitesAreReadFromTheTableByHeaderName(t *testing.T) {
	accts, warns := parseFastPanelSites(fastPanelPlain)
	if len(accts) != 2 {
		t.Fatalf("accounts = %+v", accts)
	}
	if accts[0].ID != "example_usr" || accts[0].Domains[0].Name != "example.com" || accts[0].Domains[0].DocRoot != "/var/www/example_usr/data/www/example.com" || accts[0].Home != "/var/www/example_usr" {
		t.Errorf("first = %+v", accts[0])
	}
	if accts[1].ID != "shop_usr" || accts[1].Domains[0].DocRoot != "/var/www/shop_usr/data/www/shop.test" {
		t.Errorf("second (empty aliases) = %+v", accts[1])
	}
	if len(warns) != 2 {
		t.Errorf("a bad name and a path with .. must be skipped with a note: %v", warns)
	}
	boxed, _ := parseFastPanelSites(fastPanelBoxed)
	if len(boxed) != 1 || len(boxed[0].Domains) != 2 || boxed[0].Domains[0].Name != "blog.example.com" {
		t.Errorf("boxed = %+v", boxed)
	}
	if a, w := parseFastPanelSites("command not found: mogwai"); len(a) != 0 || len(w) != 1 || !strings.Contains(w[0], "could not be read") {
		t.Errorf("garbage = %v %v", a, w)
	}
}

func TestFastPanelIsDetectedByItsCLI(t *testing.T) {
	src := &fakeSrc{}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "fastpanel\n", nil }
	det, ok, err := fastPanelAdapter{}.Detect(context.Background(), src)
	if err != nil || !ok || det.Panel != "fastpanel" || det.Support != Experimental {
		t.Errorf("det = %+v %v %v", det, ok, err)
	}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "", nil }
	if _, ok, _ := (fastPanelAdapter{}).Detect(context.Background(), src); ok {
		t.Error("detected without FASTPANEL")
	}
}

func TestVestaAndHestiaUsersAreReadFromTheirFiles(t *testing.T) {
	out := "##web\nDOMAIN='Example.com' IP='1.2.3.4' ALIAS='www.example.com' SSL='yes'\nDOMAIN='blog.example.com' IP='1.2.3.4'\nDOMAIN='bad..x' IP='1'\nDOMAIN='example.com' IP='dup'\n" +
		"##db\nDB='alice_wp' DBUSER='alice_wp' HOST='localhost' TYPE='mysql' CHARSET='UTF8'\nDB='alice_pg' TYPE='pgsql'\nDB='bad name' TYPE='mysql'\n" +
		"##mail\n@@d example.com\nACCOUNT='info' ALIAS='' QUOTA='unlimited'\nACCOUNT='Sales' QUOTA='1'\nACCOUNT='$(x)'\n@@d bad..dom\nACCOUNT='z'\n##end\n"
	a := parseVestaUser("alice", out)
	if len(a.Domains) != 2 || a.Domains[0].Name != "example.com" || a.Domains[0].DocRoot != "/home/alice/web/example.com/public_html" || a.Domains[1].DocRoot != "/home/alice/web/blog.example.com/public_html" {
		t.Errorf("domains = %+v", a.Domains)
	}
	if len(a.Databases) != 1 || a.Databases[0].Name != "alice_wp" || a.Databases[0].Engine != "mysql" {
		t.Errorf("databases = %+v", a.Databases)
	}
	if len(a.Email) != 2 || a.Email[0].Address != "info@example.com" || a.Email[0].Maildir != "/home/alice/mail/example.com/info" || a.Email[1].Address != "sales@example.com" {
		t.Errorf("mail = %+v", a.Email)
	}
}

func TestVestaAndHestiaAreDetectedByTheirOwnFolder(t *testing.T) {
	src := &fakeSrc{}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) {
		if strings.Contains(cmd, "/usr/local/hestia") {
			return "present\nVERSION='1.8.10'\n", nil
		}
		return "", nil
	}
	h := vestaFamily{id: "hestiacp", name: "HestiaCP", root: "/usr/local/hestia"}
	det, ok, _ := h.Detect(context.Background(), src)
	if !ok || det.Panel != "hestiacp" || det.Version != "1.8.10" {
		t.Errorf("hestia = %+v %v", det, ok)
	}
	v := vestaFamily{id: "vestacp", name: "VestaCP", root: "/usr/local/vesta"}
	if _, ok, _ := v.Detect(context.Background(), src); ok {
		t.Error("vesta detected on a hestia server")
	}
	for _, id := range []string{"vestacp", "hestiacp"} {
		if _, err := Lookup(id); err != nil {
			t.Error(err)
		}
	}
}

func TestCyberPanelAccountsAreBuiltFromItsTables(t *testing.T) {
	sites := [][]string{{"Example.com"}, {"bad..x"}, {"shop.test"}}
	children := [][]string{{"blog.example.com", "/home/example.com/public_html/blog", "example.com"}, {"x.example.com", "../etc", "example.com"}, {"y.other.org", "/home/o/public_html", "unknown.org"}}
	dbs := [][]string{{"example_wp", "example.com"}, {"orphan", "ghost.org"}}
	a := buildCyberPanel(sites, children, dbs)
	if len(a) != 2 || a[0].ID != "example.com" || len(a[0].Domains) != 2 || a[0].Domains[0].DocRoot != "/home/example.com/public_html" || a[0].Domains[1].Name != "blog.example.com" || a[0].Domains[1].Kind != "sub" {
		t.Fatalf("accounts = %+v", a)
	}
	if len(a[0].Databases) != 1 || a[0].Databases[0].Name != "example_wp" || len(a[1].Databases) != 0 {
		t.Errorf("databases = %+v %+v", a[0].Databases, a[1].Databases)
	}
}

func TestISPConfigSitesDatabasesAndMailboxesAreRead(t *testing.T) {
	sites := [][]string{
		{"1", "Example.com", "/var/www/clients/client1/web1", "web1", "vhost"},
		{"2", "sub.example.com", "/var/www/clients/client1/web2", "web2", "vhostsubdomain"},
		{"3", "bad..x", "/var/www/clients/client1/web3", "web3", "vhost"},
		{"4", "evil.test", "/var/www/../etc", "web4", "vhost"},
	}
	dbs := [][]string{{"c1db", "1"}, {"lost", "99"}, {"bad name", "1"}}
	mail := [][]string{{"Info@Example.com", "/var/vmail/example.com/info"}, {"nodomain", "/x"}, {"z@example.com", "/var/vmail/../x"}}
	a := buildISPConfig(sites, dbs, mail)
	if len(a) != 3 {
		t.Fatalf("accounts = %+v", a)
	}
	if a[0].ID != "web1" || a[0].Domains[0].DocRoot != "/var/www/clients/client1/web1/web" || a[0].Domains[0].Kind != "main" || len(a[0].Databases) != 1 || a[0].Databases[0].Name != "c1db" {
		t.Errorf("web1 = %+v", a[0])
	}
	if a[1].ID != "web2" || a[1].Domains[0].Kind != "sub" {
		t.Errorf("web2 = %+v", a[1])
	}
	if a[2].ID != "mail" || len(a[2].Email) != 1 || a[2].Email[0].Address != "info@example.com" || a[2].Email[0].Maildir != "/var/vmail/example.com/info/Maildir" {
		t.Errorf("mail = %+v", a[2])
	}
}

func TestFroxlorCustomersDomainsDatabasesAndMailAreRead(t *testing.T) {
	cust := [][]string{{"1", "web1", "/var/customers/webs/web1/"}, {"2", "bad name", "/x"}}
	doms := [][]string{
		{"Example.com", "/var/customers/webs/web1/example.com/", "1", "0"},
		{"blog.example.com", "/var/customers/webs/web1/blog/", "1", "5"},
		{"x.org", "/var/customers/webs/web1/../x", "1", "0"},
		{"o.org", "/var/customers/webs/o/", "9", "0"},
	}
	dbs := [][]string{{"web1sql1", "1"}, {"web9sql", "9"}}
	mail := [][]string{{"info@example.com", "/var/customers/mail/web1/example.com/info/", "1"}, {"x@example.com", "/var/customers/mail/web9/", "9"}}
	a := buildFroxlor(cust, doms, dbs, mail)
	if len(a) != 1 || a[0].ID != "web1" || a[0].Home != "/var/customers/webs/web1" {
		t.Fatalf("accounts = %+v", a)
	}
	if len(a[0].Domains) != 2 || a[0].Domains[0].DocRoot != "/var/customers/webs/web1/example.com" || a[0].Domains[1].Kind != "sub" {
		t.Errorf("domains = %+v", a[0].Domains)
	}
	if len(a[0].Databases) != 1 || len(a[0].Email) != 1 || a[0].Email[0].Maildir != "/var/customers/mail/web1/example.com/info" {
		t.Errorf("db/mail = %+v %+v", a[0].Databases, a[0].Email)
	}
}

func TestSQLPanelsReportAFailedQueryAndAreDetected(t *testing.T) {
	src := &fakeSrc{}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) {
		return "ERROR 1146 (42S02): Table 'cyberpanel.websiteFunctions_websites' doesn't exist\n", nil
	}
	if _, err := sqlRows(context.Background(), src, "cyberpanel", "SELECT 1"); err == nil || !strings.Contains(err.Error(), "doesn't exist") {
		t.Errorf("err = %v", err)
	}
	if _, err := sqlRows(context.Background(), src, "bad name; rm", "SELECT 1"); err == nil {
		t.Error("a non-plain database name was accepted")
	}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "a\tb\n\nc\td\n", nil }
	rows, err := sqlRows(context.Background(), src, "x", "SELECT 1")
	if err != nil || len(rows) != 2 || rows[1][1] != "d" {
		t.Errorf("rows = %v %v", rows, err)
	}
	for _, c := range []struct {
		a   Adapter
		out string
		id  string
	}{
		{cyberPanelAdapter{}, "cyberpanel\n", "cyberpanel"},
		{ispConfigAdapter{}, "ispconfig\ndefine('ISPC_APP_VERSION', '3.2.11');\n", "ispconfig"},
		{froxlorAdapter{}, "froxlor\n$sql['db'] = 'froxlor';\n", "froxlor"},
	} {
		src.execFn = func(cmd string, stdout io.Writer) (string, error) { return c.out, nil }
		det, ok, _ := c.a.Detect(context.Background(), src)
		if !ok || det.Panel != c.id {
			t.Errorf("%s = %+v %v", c.id, det, ok)
		}
		src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "", nil }
		if _, ok, _ := c.a.Detect(context.Background(), src); ok {
			t.Errorf("%s detected on an empty server", c.id)
		}
		if _, err := Lookup(c.id); err != nil {
			t.Error(err)
		}
	}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) {
		return "ispconfig\ndefine('ISPC_APP_VERSION', '3.2.11');\n", nil
	}
	if det, _, _ := (ispConfigAdapter{}).Detect(context.Background(), src); det.Version != "3.2.11" {
		t.Errorf("version = %q", det.Version)
	}
}

const cloudPanelConf = "##file /etc/nginx/sites-enabled/example.com.conf\nserver {\n  listen 443 ssl;\n  server_name example.com www.example.com;\n  root /home/example/htdocs/example.com;\n}\n" +
	"##file /etc/nginx/sites-enabled/app.conf\nserver {\n  server_name _;\n  root /home/clp/htdocs/app/files/public;\n}\n" +
	"##file /etc/nginx/sites-enabled/shop.conf\nserver {\n  server_name shop.test;\n  root /home/shopuser/htdocs/shop.test/public;\n}\n"

func TestCloudPanelSitesAreGroupedByTheirSiteUserAndThePanelItselfIsLeftOut(t *testing.T) {
	accts := groupSites(parseWebConfig(cloudPanelConf), cloudPanelGroup)
	if len(accts) != 2 || accts[0].ID != "example" || accts[0].Home != "/home/example" || accts[0].Domains[0].DocRoot != "/home/example/htdocs/example.com" {
		t.Fatalf("accounts = %+v", accts)
	}
	if accts[1].ID != "shopuser" || accts[1].Domains[0].DocRoot != "/home/shopuser/htdocs/shop.test/public" {
		t.Errorf("shop = %+v", accts[1])
	}
}

func TestAaPanelOnlyKeepsSitesUnderWwwroot(t *testing.T) {
	conf := "##file /www/server/panel/vhost/nginx/example.com.conf\nserver {\n  server_name example.com www.example.com;\n  root /www/wwwroot/example.com;\n}\n" +
		"##file /www/server/panel/vhost/nginx/phpmyadmin.conf\nserver {\n  server_name phpmyadmin.local;\n  root /www/server/phpmyadmin;\n}\n"
	accts := groupSites(parseWebConfig(conf), aaPanelGroup)
	if len(accts) != 1 || accts[0].ID != "aapanel" || len(accts[0].Domains) != 2 || accts[0].Domains[0].DocRoot != "/www/wwwroot/example.com" {
		t.Errorf("accounts = %+v", accts)
	}
}

func TestInterWorxAccountsAreReadFromTheirFolders(t *testing.T) {
	out := "alice example.com\nalice second.org\nbob shop.test\nbad name x\nalice ..\n"
	a := parseInterWorx(out)
	if len(a) != 2 || a[0].ID != "alice" || len(a[0].Domains) != 2 || a[0].Domains[0].Kind != "main" || a[0].Domains[1].Kind != "addon" || a[0].Domains[0].DocRoot != "/home/alice/example.com/html" {
		t.Fatalf("accounts = %+v", a)
	}
	if a[1].ID != "bob" || a[1].Domains[0].DocRoot != "/home/bob/shop.test/html" {
		t.Errorf("bob = %+v", a[1])
	}
}

func TestCloudPanelAaPanelAndInterWorxAreDetected(t *testing.T) {
	src := &fakeSrc{}
	for _, c := range []struct {
		a   Adapter
		out string
	}{{cloudPanel(), "cloudpanel\n"}, {aaPanel(), "aapanel\n"}, {interWorxAdapter{}, "interworx\n"}} {
		src.execFn = func(cmd string, stdout io.Writer) (string, error) { return c.out, nil }
		det, ok, _ := c.a.Detect(context.Background(), src)
		if !ok || det.Panel != c.a.ID() || det.Support != Experimental {
			t.Errorf("%s = %+v %v", c.a.ID(), det, ok)
		}
		src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "", nil }
		if _, ok, _ := c.a.Detect(context.Background(), src); ok {
			t.Errorf("%s detected on an empty server", c.a.ID())
		}
		if _, err := Lookup(c.a.ID()); err != nil {
			t.Error(err)
		}
	}
}

func TestEnhanceWebsitesAreListedByIdAndNothingElseIsGuessed(t *testing.T) {
	out := "3f2b1c9e-8a4d-4e6f-9b1a-0c2d3e4f5a6b\nnot-a-uuid\n../etc\nA1B2C3D4-0000-1111-2222-333344445555\n"
	a := parseEnhance(out)
	if len(a) != 2 || a[0].ID != "3f2b1c9e-8a4d-4e6f-9b1a-0c2d3e4f5a6b" || a[0].Domains[0].DocRoot != "/var/www/3f2b1c9e-8a4d-4e6f-9b1a-0c2d3e4f5a6b/public_html" || a[1].ID != "a1b2c3d4-0000-1111-2222-333344445555" {
		t.Fatalf("accounts = %+v", a)
	}
	src := &fakeSrc{}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "enhance\n", nil }
	det, ok, _ := enhanceAdapter{}.Detect(context.Background(), src)
	if !ok || det.Panel != "enhance" || det.Support != Experimental {
		t.Errorf("det = %+v %v", det, ok)
	}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) { return "", nil }
	if _, ok, _ := (enhanceAdapter{}).Detect(context.Background(), src); ok {
		t.Error("detected without Enhance")
	}
	if _, err := Lookup("enhance"); err != nil {
		t.Error(err)
	}
}

func TestCyberPanelDiscoveryAddsOnlyMailboxesWhoseMaildirExists(t *testing.T) {
	src := &fakeSrc{}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) {
		switch {
		case strings.Contains(cmd, "FROM e_users"):
			return "info@example.com\texample.com\nsales@example.com\texample.com\nghost@example.com\texample.com\nlone@nosite.org\tnosite.org\nbad@other.net\texample.com\n", nil
		case strings.Contains(cmd, "FROM websiteFunctions_websites") && !strings.Contains(cmd, "JOIN"):
			return "example.com\n", nil
		case strings.Contains(cmd, "JOIN"):
			return "", nil
		case strings.Contains(cmd, "/home/vmail/example.com/info/Maildir/cur"):
			return "yes\n", nil
		case strings.Contains(cmd, "/home/vmail/example.com/sales/cur"):
			return "yes\n", nil
		case strings.Contains(cmd, "/home/vmail/nosite.org/lone/Maildir/cur"):
			return "yes\n", nil
		}
		return "", nil
	}
	d, err := cyberPanelAdapter{}.Discover(context.Background(), src, ServerInfo{}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	var web, other *Account
	for i := range d.Accounts {
		switch d.Accounts[i].ID {
		case "example.com":
			web = &d.Accounts[i]
		case "mail":
			other = &d.Accounts[i]
		}
	}
	if web == nil || len(web.Email) != 2 || web.Email[0].Maildir != "/home/vmail/example.com/info/Maildir" || web.Email[1].Maildir != "/home/vmail/example.com/sales" {
		t.Fatalf("web = %+v", web)
	}
	if other == nil || len(other.Email) != 1 || other.Email[0].Address != "lone@nosite.org" {
		t.Errorf("mail account = %+v", other)
	}
	joined := strings.Join(d.Warnings, "\n")
	if !strings.Contains(joined, "1 mailbox(es) were left out") {
		t.Errorf("warnings = %v", d.Warnings)
	}
}

func TestForwardersAreCleanedAndAttachedToTheAccountThatOwnsTheDomain(t *testing.T) {
	f, ok := makeForwarder("Sales@Example.com", splitDests("Boss@Gmail.com, ops@example.org;boss@gmail.com\\nsales@example.com"))
	if !ok || f.Source != "sales@example.com" || strings.Join(f.Destinations, ",") != "boss@gmail.com,ops@example.org" {
		t.Errorf("f = %+v %v", f, ok)
	}
	if c, ok := makeForwarder("@example.com", []string{"all@example.org"}); !ok || c.Source != "*@example.com" {
		t.Errorf("catch-all = %+v %v", c, ok)
	}
	for _, bad := range [][]string{{"|/usr/bin/x"}, {"localuser"}, {}, {"a@b.com", "bob"}} {
		if _, ok := makeForwarder("x@example.com", bad); ok {
			t.Errorf("%v was accepted", bad)
		}
	}
	if _, ok := makeForwarder("bad name@example.com", []string{"a@b.com"}); ok {
		t.Error("a bad source was accepted")
	}
	a := newAccount("web1", "/x")
	a.Domains = []Domain{{Name: "example.com"}}
	got := attachForwarders([]*Account{a}, []Forwarder{{Source: "info@example.com", Destinations: []string{"a@b.com"}}, {Source: "x@other.org", Destinations: []string{"c@d.com"}}})
	if len(got) != 2 || len(a.Forwarders) != 1 || got[1].ID != "mail" || got[1].Forwarders[0].Source != "x@other.org" {
		t.Errorf("attached = %+v", got)
	}
}

func TestVestaAndDirectAdminForwardersAndAliasesAreRead(t *testing.T) {
	v := parseVestaUser("alice", "##web\nDOMAIN='example.com'\n##mail\n@@d example.com\nACCOUNT='info' ALIAS='office,desk' FWD='boss@gmail.com' FWD_ONLY='no'\nACCOUNT='plain' ALIAS='' FWD=''\n##end\n")
	if len(v.Forwarders) != 3 || v.Forwarders[0].Source != "info@example.com" || v.Forwarders[0].Destinations[0] != "boss@gmail.com" || v.Forwarders[1].Source != "office@example.com" || v.Forwarders[1].Destinations[0] != "info@example.com" {
		t.Errorf("vesta = %+v", v.Forwarders)
	}
	da := parseDirectAdminUser("alice", "##dom\nexample.com\n##fwd\n@@d example.com\nsales: boss@gmail.com,ops@example.org\nhelp: |/usr/bin/x\n@@d unknown.net\nz: a@b.com\n##end\n")
	if len(da.Forwarders) != 1 || da.Forwarders[0].Source != "sales@example.com" || len(da.Forwarders[0].Destinations) != 2 {
		t.Errorf("directadmin = %+v", da.Forwarders)
	}
}

func TestZoneFilesAreReadFromTheRightPlaceForEachPanel(t *testing.T) {
	want := map[string]string{"directadmin": "/var/named/$d.db", "vestacp": "/home/*/conf/dns/$d.db", "hestiacp": "/home/*/conf/dns/$d.db", "ispconfig": "/etc/bind/pri.$d"}
	for panel, path := range want {
		src := &fakeSrc{}
		src.execFn = func(cmd string, stdout io.Writer) (string, error) {
			io.WriteString(stdout, "@ IN MX 10 mail.example.com.\n")
			return "", nil
		}
		if _, err := ReadZone(context.Background(), src, panel, "example.com"); err != nil || !strings.Contains(src.execs[0], path) {
			t.Errorf("%s: %v %v", panel, err, src.execs)
		}
		if !HasZoneFiles(panel) {
			t.Errorf("%s has no zone support", panel)
		}
	}
	for _, p := range []string{"linux", "plesk", "froxlor", "nosuch"} {
		if HasZoneFiles(p) {
			t.Errorf("%s claims zone support", p)
		}
		if _, err := ReadZone(context.Background(), &fakeSrc{}, p, "example.com"); err == nil {
			t.Errorf("%s was read", p)
		}
	}
}

func TestDatabasesAreLinkedToTheirSiteWhenThePanelSaysSoAndNeverGuessedOtherwise(t *testing.T) {
	a := newAccount("example", "/home/example")
	a.Domains = []Domain{{Name: "example.com"}}
	dbs := []Database{{Name: "wp1"}, {Name: "other"}, {Name: "orphan"}}
	rest := placeDatabases([]*Account{a}, dbs, dbSiteMap([][]string{{"wp1", "Example.com"}, {"other", "gone.org"}, {"bad name", "example.com"}}))
	if len(a.Databases) != 1 || a.Databases[0].Name != "wp1" || len(rest) != 2 {
		t.Errorf("a = %+v rest = %+v", a.Databases, rest)
	}
	fp := parseFastPanelDatabases("ID  NAME      SERVER  SITE\n1   shop_db   1       shop.test\n2   other_db  1\n")
	if fp["shop_db"] != "shop.test" || len(fp) != 1 {
		t.Errorf("fastpanel = %v", fp)
	}
	boxed := parseFastPanelDatabases("| ID | NAME | SERVER_NAME |\n| 1 | blog_db | blog.test |\n")
	if boxed["blog_db"] != "blog.test" {
		t.Errorf("boxed = %v", boxed)
	}
	if len(parseFastPanelDatabases("command not found")) != 0 {
		t.Error("garbage produced links")
	}
}

func TestConfPanelDiscoveryLinksDatabasesAndFallsBackWhenTheLinkCannotBeRead(t *testing.T) {
	run := func(link bool) *Discovery {
		src := &fakeSrc{}
		src.execFn = func(cmd string, stdout io.Writer) (string, error) {
			switch {
			case strings.Contains(cmd, "sites-enabled"):
				return cloudPanelConf, nil
			case strings.Contains(cmd, "information_schema"):
				return "wp1\t100\nloose\t50\n", nil
			case strings.Contains(cmd, "sqlite3"):
				if link {
					return "wp1|example.com\n", nil
				}
				return "sqlite3: command not found\n", nil
			}
			return "", nil
		}
		d, err := cloudPanel().Discover(context.Background(), src, ServerInfo{}, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	d := run(true)
	var ex, pool *Account
	for i := range d.Accounts {
		switch d.Accounts[i].ID {
		case "example":
			ex = &d.Accounts[i]
		case "databases":
			pool = &d.Accounts[i]
		}
	}
	if ex == nil || len(ex.Databases) != 1 || ex.Databases[0].Name != "wp1" || ex.Databases[0].SizeKB != 100 || pool == nil || len(pool.Databases) != 1 || pool.Databases[0].Name != "loose" {
		t.Fatalf("linked: ex=%+v pool=%+v", ex, pool)
	}
	d = run(false)
	if !strings.Contains(strings.Join(d.Warnings, "\n"), "could not be read, so the databases are listed for the whole server") {
		t.Errorf("warnings = %v", d.Warnings)
	}
	for _, a := range d.Accounts {
		if a.ID == "databases" && len(a.Databases) != 2 {
			t.Errorf("fallback pool = %+v", a.Databases)
		}
	}
}

func TestCpanelFtpAccountsAreReadWithoutTheirPasswordHash(t *testing.T) {
	a := Account{ID: "alice", Home: "/home/alice"}
	parseCpanelAccount(&a, "\n##home\n/home/alice\n##mail\n##ftp\nalice:$6$secrethash:1000:1000::/home/alice:/bin/ftpsh\nstaging@example.com:$1$h2:1000:1000::/home/alice/public_html/staging:/bin/ftpsh\nftp:*:99:99::/var/ftp:/bin/false\nbad name:x:1:1::/h:/s\nrel:x:1:1::relative/path:/s\n##cron\n")
	if len(a.FTP) != 1 || a.FTP[0].Login != "staging@example.com" || a.FTP[0].Home != "/home/alice/public_html/staging" {
		t.Fatalf("ftp = %+v", a.FTP)
	}
	b, _ := json.Marshal(a.FTP)
	if strings.Contains(string(b), "secrethash") || strings.Contains(string(b), "$1$") {
		t.Error("a password hash was kept")
	}
}

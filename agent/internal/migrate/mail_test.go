package migrate

import (
	"context"
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
	if !strings.Contains(cmd, "/var/named/example.com.db") || strings.Contains(cmd, "rm ") || strings.Contains(cmd, ">") {
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

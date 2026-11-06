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

package mailmgr

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestTrace(t *testing.T) {
	d := newData()
	d.Domains["shop.test"] = Domain{}
	d.Domains["old.test"] = Domain{}
	d.Domains["backup.test"] = Domain{}
	d.Routing = map[string]string{"old.test": RouteRemote, "backup.test": RouteBackup}
	d.Mailboxes["me@shop.test"] = Mailbox{QuotaMB: 500, Filters: []Filter{{Enabled: true}, {Enabled: false}}, Responder: &Responder{Enabled: true}}
	d.Aliases["sales@shop.test"] = []string{"me@shop.test", "boss@gmail.test"}
	d.Aliases["a@shop.test"] = []string{"b@shop.test"}
	d.Aliases["b@shop.test"] = []string{"a@shop.test"}
	d.Aliases["@shop.test"] = []string{"me@shop.test"}
	d.Suspended = []string{"shop.test"}
	mx := func(_ context.Context, dom string) ([]*net.MX, error) {
		if dom == "gmail.test" {
			return []*net.MX{{Host: "alt.gmail.test.", Pref: 20}, {Host: "mx.gmail.test.", Pref: 5}}, nil
		}
		return nil, errors.New("no")
	}
	join := func(s []TraceStep) string {
		var b strings.Builder
		for _, x := range s {
			b.WriteString(strings.Repeat(" ", x.Depth) + x.Result + " " + x.Address + ": " + x.Detail + "\n")
		}
		return b.String()
	}
	got := join(Trace(context.Background(), d, "Sales@Shop.test", "", mx))
	for _, want := range []string{"forward sales@shop.test: Forwarded to me@shop.test, boss@gmail.test.", " mailbox me@shop.test: Delivered to the mailbox me@shop.test (quota 500 MB). Also: 1 email filter(s) run first; the autoresponder replies; the account is suspended", " external boss@gmail.test: gmail.test is not hosted here; mail is sent to its mail exchanger mx.gmail.test (5), alt.gmail.test (20)."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if got := join(Trace(context.Background(), d, "a@shop.test", "", mx)); !strings.Contains(got, "loop a@shop.test") {
		t.Errorf("loop:\n%s", got)
	}
	if got := join(Trace(context.Background(), d, "nobody@shop.test", "", mx)); !strings.Contains(got, "catchall nobody@shop.test") || !strings.Contains(got, " mailbox me@shop.test") {
		t.Errorf("catch-all:\n%s", got)
	}
	delete(d.Aliases, "@shop.test")
	if got := join(Trace(context.Background(), d, "nobody@shop.test", "", mx)); !strings.Contains(got, "rejected nobody@shop.test") || !strings.Contains(got, "550 5.1.1") {
		t.Errorf("unknown:\n%s", got)
	}
	if got := join(Trace(context.Background(), d, "x@old.test", "[smtp.relay.test]:587", mx)); !strings.Contains(got, "remote x@old.test") || !strings.Contains(got, "through the smart host [smtp.relay.test]:587") {
		t.Errorf("remote:\n%s", got)
	}
	if got := join(Trace(context.Background(), d, "x@backup.test", "", mx)); !strings.Contains(got, "backup x@backup.test") {
		t.Errorf("backup:\n%s", got)
	}
}

func TestRepairTree(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (chown)")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("x"), 0o640)
	box := filepath.Join(root, "shop.test", "me", "Maildir", "cur")
	os.MkdirAll(box, 0o777)
	os.WriteFile(filepath.Join(box, "1.msg"), []byte("hi"), 0o666)
	os.Symlink(outside, filepath.Join(box, "evil"))
	os.Symlink(t.TempDir(), filepath.Join(root, "shop.test", "me", "evildir"))
	syscall.Mkfifo(filepath.Join(box, "fifo"), 0o666)
	os.Chmod(box, 0o777)
	os.Chmod(filepath.Join(box, "1.msg"), 0o666)
	filepath.Walk(filepath.Join(root, "shop.test"), func(p string, _ os.FileInfo, _ error) error { return os.Lchown(p, 1234, 1234) })

	fd, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	st := &RepairStats{}
	if err := repairTree(fd, "shop.test", "shop.test", 5000, 5000, st, 0); err != nil {
		t.Fatal(err)
	}
	if st.Dirs != 4 || st.Files != 1 || len(st.Skipped) != 3 {
		t.Fatalf("stats: %+v", st)
	}
	var s syscall.Stat_t
	syscall.Stat(filepath.Join(box, "1.msg"), &s)
	if s.Uid != 5000 || s.Mode&0o777 != 0o660 {
		t.Fatalf("file: uid %d mode %o", s.Uid, s.Mode&0o777)
	}
	syscall.Stat(box, &s)
	if s.Uid != 5000 || s.Mode&0o777 != 0o770 {
		t.Fatalf("dir: uid %d mode %o", s.Uid, s.Mode&0o777)
	}
	syscall.Stat(outside, &s)
	if s.Uid != 0 || s.Mode&0o777 != 0o640 {
		t.Fatalf("a link's target was changed: uid %d mode %o", s.Uid, s.Mode&0o777)
	}
	// A second run changes nothing.
	st2 := &RepairStats{}
	repairTree(fd, "shop.test", "shop.test", 5000, 5000, st2, 0)
	if st2.Fixed != 0 {
		t.Fatalf("second run fixed %d", st2.Fixed)
	}
}

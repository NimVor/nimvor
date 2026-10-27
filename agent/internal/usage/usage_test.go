package usage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestUsage(t *testing.T) {
	h := testhost.New(t)
	root := filepath.Join(h.Base, "home/whacp/shop.test")
	os.MkdirAll(filepath.Join(root, "public_html/a"), 0o755)
	os.WriteFile(filepath.Join(root, "public_html/index.php"), make([]byte, 1000), 0o644)
	os.WriteFile(filepath.Join(root, "public_html/a/b.txt"), make([]byte, 24), 0o644)
	os.WriteFile(filepath.Join(h.Base, "big"), make([]byte, 1<<20), 0o644)
	os.Symlink(filepath.Join(h.Base, "big"), filepath.Join(root, "public_html/link")) // not followed
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("sites.usage")
	res, err := a.Handler(context.Background(), json.RawMessage(`{"domains":["shop.test","missing.test"]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := res.([]Result)
	if got[0].Bytes != 1024 || got[0].Files != 2 || !got[1].NotFound {
		t.Fatalf("usage = %+v", got)
	}
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"domains":["../etc"]}`)); err == nil {
		t.Error("invalid domain accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, _, partial, _ := Measure(ctx, root); !partial {
		t.Error("expected a partial result after the deadline")
	}
}

func TestBandwidthFromAccessLogs(t *testing.T) {
	line := func(date string, bytes string) string {
		return `203.0.113.5 - - [` + date + ` +0000] "GET /a?x=\x22y\x22 HTTP/1.1" 200 ` + bytes + ` "-" "Mozilla/5.0 (X11)"` + "\n"
	}
	if m, n, ok := LineBytes(line("27/Sep/2026:10:00:00", "512")); !ok || m != "2026-09" || n != 512 {
		t.Fatalf("parse: %s %d %v", m, n, ok)
	}
	if _, n, ok := LineBytes(line("27/Sep/2026:10:00:00", "-")); !ok || n != 0 {
		t.Fatal("no body")
	}
	for _, bad := range []string{"", "garbage\n", "x [27/Foo/2026:10:00:00 +0000] \"GET / HTTP/1.1\" 200 5\n"} {
		if _, _, ok := LineBytes(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}

	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	call := func(params string) []Traffic {
		a, _ := r.Lookup("sites.bandwidth")
		res, err := a.Handler(context.Background(), json.RawMessage(params))
		if err != nil {
			t.Fatal(err)
		}
		return res.([]Traffic)
	}
	logs := filepath.Join(h.Base, "home/whacp/a.test/logs")
	os.MkdirAll(logs, 0o755)
	log := filepath.Join(logs, "access.log")
	os.WriteFile(log, []byte(line("31/Aug/2026:23:59:59", "100")+line("01/Sep/2026:00:00:01", "1000")+line("02/Sep/2026:00:00:01", "24")+"partial line without end"), 0o644)
	got := call(`{"sites":[{"domain":"a.test","root":"/home/whacp/a.test"}]}`)
	if len(got) != 1 || got[0].Months["2026-08"] != 100 || got[0].Months["2026-09"] != 1024 {
		t.Fatalf("first read: %+v", got)
	}
	// Continues where it stopped (the partial line is read once complete).
	f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("\n" + line("03/Sep/2026:00:00:01", "5"))
	f.Close()
	pos, _ := json.Marshal(map[string]any{"sites": []Position{{Domain: "a.test", Root: "/home/whacp/a.test", Inode: got[0].Inode, Offset: got[0].Offset}}})
	got2 := call(string(pos))
	if got2[0].Months["2026-09"] != 5 || got2[0].Months["2026-08"] != 0 {
		t.Fatalf("second read: %+v", got2)
	}
	// A rotated log (new file) is read from the start.
	os.Remove(log)
	os.WriteFile(log, []byte(line("04/Sep/2026:00:00:01", "7")), 0o644)
	pos, _ = json.Marshal(map[string]any{"sites": []Position{{Domain: "a.test", Root: "/home/whacp/a.test", Inode: got2[0].Inode, Offset: got2[0].Offset}}})
	if got3 := call(string(pos)); got3[0].Months["2026-09"] != 7 {
		t.Fatalf("after rotation: %+v", got3)
	}
	// Never through a symlink out of the website.
	os.Remove(log)
	os.WriteFile(filepath.Join(h.Base, "etc/secret.log"), []byte(line("04/Sep/2026:00:00:01", "999")), 0o600)
	os.Symlink(filepath.Join(h.Base, "etc/secret.log"), log)
	if got4 := call(`{"sites":[{"domain":"a.test","root":"/home/whacp/a.test"}]}`); !got4[0].NotFound {
		t.Fatalf("followed a symlink: %+v", got4)
	}
	for _, bad := range []string{`{"sites":[{"domain":"a.test","root":"/etc"}]}`, `{"sites":[{"domain":"../x","root":""}]}`} {
		a, _ := r.Lookup("sites.bandwidth")
		if _, err := a.Handler(context.Background(), json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

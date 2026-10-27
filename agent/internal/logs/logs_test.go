package logs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestResolveRefusesArbitraryPaths(t *testing.T) {
	h := testhost.New(t)
	for _, id := range []string{"site:../../etc:passwd.log", "site:a.test:../../x.log", "site:a.test:shadow", "file:/etc/shadow", "system:shadow", "journal:sshd;id.service", "journal:dbus.service", "php:8.3/../../x"} {
		if _, err := Resolve(h.Env, id); err == nil {
			t.Errorf("%s accepted", id)
		}
	}
	if _, err := Resolve(h.Env, "journal:nginx.service"); err != nil {
		t.Error(err)
	}
}

func TestTailFollowAndFilter(t *testing.T) {
	h := testhost.New(t)
	dir := filepath.Join(h.Base, "home/whacp/a.test/logs")
	os.MkdirAll(dir, 0o755)
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "line %d %s\n", i, map[bool]string{true: "ERROR", false: "ok"}[i%100 == 0])
	}
	os.WriteFile(filepath.Join(dir, "error.log"), []byte(b.String()), 0o644)
	s, err := Resolve(h.Env, "site:a.test:error.log")
	if err != nil {
		t.Fatal(err)
	}
	res, _ := Tail(context.Background(), h.Env, s, 5, "", false, 0, "")
	if len(res.Lines) != 5 || res.Lines[4] != "line 3000 ERROR" {
		t.Fatalf("tail = %v", res.Lines)
	}
	res, _ = Tail(context.Background(), h.Env, s, 10, "error", false, 0, "")
	if len(res.Lines) != 10 || !strings.HasPrefix(res.Lines[0], "line 2100") {
		t.Fatalf("filtered tail = %v", res.Lines)
	}
	f, _ := os.OpenFile(filepath.Join(dir, "error.log"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("new 1\nnew 2\npartial")
	f.Close()
	next, _ := Tail(context.Background(), h.Env, s, 0, "", true, res.Offset, "")
	if strings.Join(next.Lines, "|") != "new 1|new 2" {
		t.Fatalf("follow = %v", next.Lines)
	}
	srcs := Sources(context.Background(), h.Env, "a.test")
	if len(srcs) != 1 || srcs[0].ID != "site:a.test:error.log" {
		t.Fatalf("sources = %+v", srcs)
	}
}

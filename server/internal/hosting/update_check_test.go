package hosting

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

const shaA, shaB = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestGithubRepoOnlyForGithubRemotes(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/acme/WhaCP.git": "acme/WhaCP", "git@github.com:acme/whacp": "acme/whacp", "https://github.com/acme/whacp/": "acme/whacp",
		"https://gitlab.com/acme/whacp": "", "https://github.com.evil.test/a/b": "", "": "",
	} {
		if got := GithubRepo(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestUpdateCheckStatesAndCaching(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	svc := NewUpdateCheckService(h.agent, settings.NewRepository(db))
	ctx := context.Background()
	if !svc.Due(ctx) {
		t.Error("never checked: due")
	}
	h.agent.canned = map[string]json.RawMessage{"whacp.update_status": json.RawMessage(`{"git":true,"commit":"` + shaA + `","branch":"main","remote":"https://github.com/acme/whacp.git","updatable":true}`)}
	var asked string
	reply := func(code int, body string) {
		svc.Fetch = func(_ context.Context, u string) (int, string) { asked = u; return code, body }
	}
	reply(200, shaB+"\n")
	if c := svc.Check(ctx); !c.Available || c.State != "available" || c.Latest != shaB || !strings.HasSuffix(asked, "/repos/acme/whacp/commits/main") {
		t.Fatalf("available: %+v (%s)", c, asked)
	}
	reply(200, shaA)
	if c := svc.Check(ctx); c.Available || c.State != "current" {
		t.Fatalf("current: %+v", c)
	}
	if svc.Due(ctx) || svc.Cached(ctx) == nil {
		t.Error("a fresh check is cached")
	}
	reply(404, "")
	if c := svc.Check(ctx); c.State != "unknown" || !strings.Contains(c.Reason, "private repository") {
		t.Fatalf("404: %+v", c)
	}
	reply(500, "")
	if c := svc.Check(ctx); c.State != "unknown" || c.Available || !strings.Contains(c.Reason, "HTTP 500") {
		t.Fatalf("500 is never 'up to date': %+v", c)
	}
	h.agent.canned["whacp.update_status"] = json.RawMessage(`{"git":false,"commit":"","remote":""}`)
	if c := svc.Check(ctx); c.State != "unknown" || !strings.Contains(c.Reason, "GitHub repository") {
		t.Fatalf("archive install: %+v", c)
	}
}

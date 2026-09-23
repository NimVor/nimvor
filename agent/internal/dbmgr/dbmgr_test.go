package dbmgr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestQuote(t *testing.T) {
	cases := map[string]string{`a'b`: `'a\'b'`, `a\b`: `'a\\b'`, "a\nb": `'a\nb'`}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
	if Ident("x`y") != "`x``y`" {
		t.Error("Ident escaping")
	}
}

func call(t *testing.T, r *actions.Registry, name, params string) (any, error) {
	a, err := r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a.Handler(context.Background(), json.RawMessage(params))
}

func TestSQLIsSentOnStdinWithValidatedIdentifiers(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	h.Runner.Outputs = map[string]string{}
	if _, err := call(t, r, "db.user_create", `{"user":"shop","password":"S3cure'Pass!word","host":"localhost"}`); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(h.Runner.Stdins, "\n")
	if !strings.Contains(all, `CREATE USER 'shop'@'localhost' IDENTIFIED BY 'S3cure\'Pass!word';`) {
		t.Errorf("stdin = %s", all)
	}
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "S3cure") {
			t.Errorf("password leaked into argv: %s", c)
		}
	}
	for _, bad := range []string{
		`{"name":"shop; DROP DATABASE mysql"}`, `{"name":"mysql"}`, `{"name":"a` + "`" + `b"}`,
	} {
		if _, err := call(t, r, "db.create", bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := call(t, r, "db.drop", `{"name":"shop","confirm":"other"}`); err == nil {
		t.Error("drop without matching confirmation accepted")
	}
	if _, err := call(t, r, "db.grant", `{"user":"shop","database":"shop","privileges":"superuser"}`); err == nil {
		t.Error("unknown privilege set accepted")
	}
}

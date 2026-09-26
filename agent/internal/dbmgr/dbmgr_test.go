package dbmgr

import (
	"context"
	"encoding/json"
	"io"
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

// SQL imports run the client with its own commands (shell, files) turned
// off, and are refused when the client cannot do that.
func TestImportTurnsOffClientCommands(t *testing.T) {
	for _, c := range []struct{ help, want string }{
		{"  --sandbox           Disallow commands that access the file system\n", "--sandbox"},
		{"  --commands          Enable or disable processing of local mysql commands.\n", "--commands=OFF"},
		{"  --sandbox-ish-option  not the real one\n  --batch\n", ""},
	} {
		guards.Delete("mysql")
		h := testhost.New(t)
		h.Respond = func(id string, args []string) (string, error, bool) {
			if id == "mysql" && len(args) == 1 && args[0] == "--help" {
				return c.help, nil, true
			}
			return "", nil, false
		}
		err := Client{Env: h.Env}.ImportFrom(context.Background(), "shop", strings.NewReader("\\! touch /tmp/pwned\n"), io.Discard)
		var imported []string
		for _, k := range h.Runner.Calls {
			if strings.HasPrefix(k, "mysql ") && k != "mysql --help" {
				imported = append(imported, k)
			}
		}
		if c.want == "" {
			if err == nil || len(imported) != 0 {
				t.Fatalf("client without the option: err %v, calls %v", err, imported)
			}
			continue
		}
		if err != nil || len(imported) != 1 || !strings.Contains(imported[0], " "+c.want+" -- shop") {
			t.Fatalf("%s: err %v, calls %v", c.want, err, imported)
		}
	}
	guards.Delete("mysql")
}

package dbmgr

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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

// A website's SQL file is opened confined to the website: a symlink to a
// file elsewhere (e.g. /etc/shadow, which mysql errors would quote into the
// job log) is refused by db.import_site and by db.import alike.
func TestImportFromAWebsiteIsConfinedToIt(t *testing.T) {
	guards.Delete("mysql")
	defer guards.Delete("mysql")
	h := testhost.New(t)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "mysql" && len(args) == 1 && args[0] == "--help" {
			return "  --sandbox           Disallow commands that access the file system\n", nil, true
		}
		return "", nil, false
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	site := filepath.Join(h.Base, h.Env.Cfg.SitesRoot, "shop.test")
	os.MkdirAll(filepath.Join(site, "tmp"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "etc"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/shadow"), []byte("root:$6$secret-hash:19000:0:99999:7:::\n"), 0o600)
	os.WriteFile(filepath.Join(site, "tmp/dump.sql"), []byte("CREATE TABLE t (a INT);\n"), 0o644)
	os.Symlink(filepath.Join(h.Env.Cfg.SitesRoot, "../../etc/shadow"), filepath.Join(site, "tmp/evil.sql"))
	os.Symlink("../../../../etc/shadow", filepath.Join(site, "tmp/evil2.sql"))

	for _, p := range []string{"tmp/evil.sql", "tmp/evil2.sql", "../other.test/x.sql", "tmp/dump.txt"} {
		if _, err := call(t, r, "db.import_site", `{"name":"shop","domain":"shop.test","path":"`+p+`"}`); err == nil {
			t.Errorf("db.import_site accepted %s", p)
		}
	}
	res, err := call(t, r, "db.import_site", `{"name":"shop","domain":"shop.test","path":"tmp/dump.sql","remove_after":true}`)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	if j.Status != "succeeded" || !strings.Contains(strings.Join(h.Runner.Stdins, ""), "CREATE TABLE t") {
		t.Fatalf("import: %s, stdin %q", j.Status, h.Runner.Stdins)
	}
	if _, err := os.Stat(filepath.Join(site, "tmp/dump.sql")); !os.IsNotExist(err) {
		t.Error("uploaded file not removed")
	}

	// The older db.import takes absolute paths: inside a website the same confinement applies.
	res, err = call(t, r, "db.import", `{"name":"shop","source":"`+filepath.Join(h.Env.Cfg.SitesRoot, "shop.test/tmp/evil2.sql")+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	j, _ = h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	if j.Status != "failed" || strings.Contains(strings.Join(h.Runner.Stdins, ""), "secret-hash") {
		t.Fatalf("symlink followed: %s, stdin %q", j.Status, h.Runner.Stdins)
	}
}

func TestUserLock(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	h.Runner.Outputs = map[string]string{}
	if _, err := call(t, r, "db.user_lock", `{"user":"shop_u","locked":true}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, r, "db.user_lock", `{"user":"shop_u","host":"%","locked":false}`); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(h.Runner.Stdins, "\n")
	for _, want := range []string{"ALTER USER IF EXISTS 'shop_u'@'localhost' ACCOUNT LOCK;", "ALTER USER IF EXISTS 'shop_u'@'%' ACCOUNT UNLOCK;"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %s", want, all)
		}
	}
	for _, bad := range []string{`{"user":"root","locked":true}`, `{"user":"a'b","locked":true}`} {
		if _, err := call(t, r, "db.user_lock", bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

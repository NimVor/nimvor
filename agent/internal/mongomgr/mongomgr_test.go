package mongomgr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func setup(t *testing.T) (*testhost.Host, *actions.Registry) {
	t.Helper()
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	return h, r
}

func call(t *testing.T, r *actions.Registry, name, params string) (any, error) {
	t.Helper()
	a, lerr := r.Lookup(name)
	if lerr != nil {
		t.Fatalf("no action %s: %v", name, lerr)
	}
	return a.Handler(context.Background(), json.RawMessage(params))
}

func TestStatus(t *testing.T) {
	h, r := setup(t)
	h.Runner.Unavailable["mongosh"] = true
	res, _ := call(t, r, "mongo.status", `{}`)
	if res.(Status).Installed {
		t.Fatal("status should report not installed")
	}
	delete(h.Runner.Unavailable, "mongosh")
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "systemctl" && len(args) >= 2 && args[0] == "is-active" {
			return "active\n", nil
		}
		if id == "mongosh" {
			return "7.0.14\n", nil
		}
		return "", nil
	}
	res, err := call(t, r, "mongo.status", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	st := res.(Status)
	if !st.Installed || !st.Running || st.Version != "7.0.14" {
		t.Fatalf("status: %+v", st)
	}
}

func TestOverviewParsesDatabasesAndUsers(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "mongosh" {
			return "", nil
		}
		script := args[len(args)-1]
		switch {
		case strings.Contains(script, "listDatabases"):
			return `[{"name":"admin","sizeOnDisk":40960,"empty":false},{"name":"shop","sizeOnDisk":8192,"empty":false},{"name":"local","sizeOnDisk":1,"empty":false}]`, nil
		case strings.Contains(script, "usersInfo"):
			return `[{"user":"root","db":"admin","roles":[{"role":"root","db":"admin"}]},{"user":"shopuser","db":"shop","roles":[{"role":"readWrite","db":"shop"}]}]`, nil
		}
		return "", nil
	}
	res, err := call(t, r, "mongo.overview", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	dbs := m["databases"].([]Database)
	if len(dbs) != 1 || dbs[0].Name != "shop" || dbs[0].Size != 8192 {
		t.Fatalf("databases (internal not filtered?): %+v", dbs)
	}
	users := m["users"].([]User)
	if len(users) != 1 || users[0].User != "shopuser" || users[0].DB != "shop" || users[0].Roles[0] != "readWrite" {
		t.Fatalf("users: %+v", users)
	}
}

// A failure listing users must not hide the databases: the overview still
// returns the databases and reports the users error.
func TestOverviewReturnsDatabasesWhenUsersFail(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "mongosh" {
			return "", nil
		}
		script := args[len(args)-1]
		if strings.Contains(script, "listDatabases") {
			return `[{"name":"shop","sizeOnDisk":8192,"empty":false}]`, nil
		}
		if strings.Contains(script, "usersInfo") {
			return "not authorized", fmt.Errorf("exit status 1")
		}
		return "", nil
	}
	res, err := call(t, r, "mongo.overview", `{}`)
	if err != nil {
		t.Fatalf("overview should not fail when only users fail: %v", err)
	}
	m := res.(map[string]any)
	if dbs := m["databases"].([]Database); len(dbs) != 1 || dbs[0].Name != "shop" {
		t.Fatalf("databases: %+v", dbs)
	}
	if m["users_error"] == nil {
		t.Fatal("a users error should be reported")
	}
}

func TestDatabaseCreateAndDrop(t *testing.T) {
	h, r := setup(t)
	var scripts []string
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "mongosh" {
			scripts = append(scripts, args[len(args)-1])
		}
		return "", nil
	}
	if _, err := call(t, r, "mongo.database_create", `{"name":"shop"}`); err != nil {
		t.Fatal(err)
	}
	if len(scripts) != 1 || !strings.Contains(scripts[0], "getSiblingDB('shop')") || !strings.Contains(scripts[0], "createCollection('_whacp')") {
		t.Fatalf("create script: %v", scripts)
	}
	// Dangerous or malformed names are refused before mongosh runs.
	for _, bad := range []string{`{"name":"a'b"}`, `{"name":"a.b"}`, `{"name":"a/b"}`, `{"name":""}`, `{"name":"a b"}`} {
		if _, err := call(t, r, "mongo.database_create", bad); err == nil {
			t.Errorf("accepted bad name: %s", bad)
		}
	}
	// Drop requires the confirmation to match.
	if _, err := call(t, r, "mongo.database_drop", `{"name":"shop","confirm":"nope"}`); err == nil {
		t.Error("drop without a matching confirm should fail")
	}
	if _, err := call(t, r, "mongo.database_drop", `{"name":"shop","confirm":"shop"}`); err != nil {
		t.Fatal(err)
	}
	if last := scripts[len(scripts)-1]; !strings.Contains(last, "dropDatabase()") {
		t.Fatalf("drop script: %s", last)
	}
}

func TestUserCreateValidationAndScript(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) { return "", nil }
	if _, err := call(t, r, "mongo.user_create", `{"user":"shopuser","password":"longenough1","db":"shop","role":"readWrite"}`); err != nil {
		t.Fatal(err)
	}
	// The password-bearing script is fed on stdin, so it (and the password)
	// never reach the command line / process list.
	if len(h.Runner.Stdins) != 1 {
		t.Fatalf("the user script was not fed on stdin: %v", h.Runner.Stdins)
	}
	script := h.Runner.Stdins[0]
	for _, want := range []string{"getSiblingDB('shop')", "createUser(", "'shopuser'", "role:'readWrite'", "JSON.parse("} {
		if !strings.Contains(script, want) {
			t.Errorf("user script lacks %q:\n%s", want, script)
		}
	}
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "longenough1") || strings.Contains(c, "--eval") {
			t.Errorf("the password/script must not appear on the command line: %s", c)
		}
	}
	for _, bad := range []string{
		`{"user":"ok","password":"short","db":"shop","role":"readWrite"}`,
		`{"user":"ok","password":"longenough1","db":"shop","role":"superuser"}`,
		`{"user":"bad'user","password":"longenough1","db":"shop"}`,
		`{"user":"ok","password":"longenough1","db":"a b"}`,
	} {
		if _, err := call(t, r, "mongo.user_create", bad); err == nil {
			t.Errorf("accepted bad user input: %s", bad)
		}
	}
}

func TestActionsNeedMongoInstalled(t *testing.T) {
	h, r := setup(t)
	h.Runner.Unavailable["mongosh"] = true
	for _, a := range []string{"mongo.overview", "mongo.database_create", "mongo.user_create"} {
		params := `{}`
		if a == "mongo.database_create" {
			params = `{"name":"shop"}`
		}
		if a == "mongo.user_create" {
			params = `{"user":"u","password":"longenough1","db":"shop"}`
		}
		if _, err := call(t, r, a, params); err == nil {
			t.Errorf("%s should require MongoDB installed", a)
		}
	}
}

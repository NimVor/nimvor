package mongomgr

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/execx"
)

// TestRemotePingAndDatabases checks that a remote connection is reached through
// a mongosh script on stdin (never the command line) and that the credentials
// do not leak into the process arguments.
func TestRemotePingAndDatabases(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id != "mongosh" {
			return "", nil
		}
		script := ""
		if n := len(h.Runner.Stdins); n > 0 {
			script = h.Runner.Stdins[n-1]
		}
		if strings.Contains(script, "buildInfo") {
			return `{"ok":1,"version":"7.0.5","databases":3}`, nil
		}
		if strings.Contains(script, "listDatabases") {
			return `[{"name":"shop","size":8192},{"name":"blog","size":4096}]`, nil
		}
		return "", nil
	}

	conn := `{"host":"db.example.com","port":27017,"username":"root","password":"p@ss:w/rd","auth_db":"admin","tls":true}`
	ping := mustCall(t, r, "mongo.remote_ping", conn)
	if ping["ok"] != true || ping["version"].(string) != "7.0.5" {
		t.Fatalf("ping: %+v", ping)
	}
	// mongosh must be started with --nodb and no connection string argument;
	// the password must never appear in the arguments.
	lastCall := h.Runner.Calls[len(h.Runner.Calls)-1]
	if !strings.Contains(lastCall, "--nodb") {
		t.Errorf("mongosh not started with --nodb: %s", lastCall)
	}
	if strings.Contains(strings.Join(h.Runner.Calls, " "), "p@ss") {
		t.Error("the password leaked into the command line")
	}
	// The connection string (with escaped credentials) is in the script on stdin.
	joinedStdin := strings.Join(h.Runner.Stdins, "\n")
	if !strings.Contains(joinedStdin, "db.example.com:27017") || !strings.Contains(joinedStdin, "tls=true") {
		t.Errorf("the connection string was not built into the script: %s", joinedStdin)
	}

	dbs := mustCall(t, r, "mongo.remote_databases", conn)
	raw := string(dbs["databases"].(json.RawMessage))
	if !strings.Contains(raw, "shop") || !strings.Contains(raw, "blog") {
		t.Errorf("remote databases: %s", raw)
	}

	// Bad input is refused.
	for _, bad := range []string{
		`{"host":"","port":27017}`,
		`{"host":"ok","port":70000}`,
		`{"host":"ok","port":27017,"username":"bad user"}`,
	} {
		if _, err := call(t, r, "mongo.remote_ping", bad); err == nil {
			t.Errorf("remote_ping accepted bad input: %s", bad)
		}
	}
}

// TestRemotePull copies a remote database into the local server and checks the
// mongodump/mongorestore flags, that the password reaches only a --config file
// (not the command line), and that the file is cleaned up.
func TestRemotePull(t *testing.T) {
	h, r := setup(t)
	var confSeen, confPath string
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "mongodump" {
			for _, a := range args {
				if strings.HasPrefix(a, "--config=") {
					confPath = strings.TrimPrefix(a, "--config=")
					if b, err := os.ReadFile(filepath.Join(h.Base, confPath)); err == nil {
						confSeen = string(b)
					}
				}
			}
			return "dumped", nil
		}
		return "", nil
	}

	res := mustCall(t, r, "mongo.remote_pull", `{"host":"db.example.com","port":27017,"username":"root","password":"s3cr:t","auth_db":"admin","remote_db":"shop","target_db":"shop_copy","drop":true}`)
	if res["job_id"] == nil {
		t.Fatalf("no job id: %+v", res)
	}
	h.Env.Jobs.Drain()

	all := strings.Join(h.Runner.Calls, "\n")
	if strings.Contains(all, "s3cr:t") {
		t.Errorf("the remote password leaked into the command line: %s", all)
	}
	// mongodump targets the remote and the right database.
	if !strings.Contains(all, "mongodump ") || !strings.Contains(all, "--host=db.example.com") || !strings.Contains(all, "--db=shop") || !strings.Contains(all, "--config=") {
		t.Errorf("mongodump flags wrong: %s", all)
	}
	// mongorestore renames into the target database.
	if !strings.Contains(all, "mongorestore ") || !strings.Contains(all, "--nsFrom=shop.*") || !strings.Contains(all, "--nsTo=shop_copy.*") || !strings.Contains(all, "--drop") {
		t.Errorf("mongorestore flags wrong: %s", all)
	}
	// The password did reach the --config file during the run…
	if !strings.Contains(confSeen, "s3cr:t") {
		t.Errorf("the password was not written to the config file: %q", confSeen)
	}
	// …and the file is removed afterwards.
	if confPath != "" {
		if _, err := os.Stat(filepath.Join(h.Base, confPath)); !os.IsNotExist(err) {
			t.Errorf("the credentials file was not cleaned up: %v", err)
		}
	}

	// A bad target database name is refused before any work.
	if _, err := call(t, r, "mongo.remote_pull", `{"host":"ok","remote_db":"shop","target_db":"a b"}`); err == nil {
		t.Error("remote_pull accepted a bad target name")
	}
}

package mongomgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/execx"
)

// TestBackupListRestoreDelete drives a full round trip: back up a database
// (which writes a gzip archive), list it, restore from it, then delete it. The
// fake mongodump writes a stand-in archive file so the listing and restore can
// find it; mongorestore is only checked for the right flags.
func TestBackupListRestoreDelete(t *testing.T) {
	h, r := setup(t)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "mongodump" {
			for _, a := range args {
				if strings.HasPrefix(a, "--archive=") {
					p := filepath.Join(h.Base, strings.TrimPrefix(a, "--archive="))
					os.MkdirAll(filepath.Dir(p), 0o755)
					os.WriteFile(p, []byte("ARCHIVE"), 0o644)
				}
			}
			return "done dumping shop", nil
		}
		return "", nil
	}

	res := mustCall(t, r, "mongo.backup", `{"db":"shop"}`)
	file := res["file"].(string)
	if !strings.HasSuffix(file, "_shop.archive.gz") {
		t.Fatalf("backup file name: %q", file)
	}
	h.Env.Jobs.Drain()
	if !h.Runner.Called("mongodump --db=shop --archive=" + filepath.Join("/var/backups/whacp/mongo", file) + " --gzip") {
		t.Errorf("mongodump was not called as expected; calls: %v", h.Runner.Calls)
	}

	list := mustCall(t, r, "mongo.backups", `{"db":"shop"}`)
	backups := list["backups"].([]map[string]any)
	if len(backups) != 1 || backups[0]["file"].(string) != file || backups[0]["db"].(string) != "shop" {
		t.Fatalf("backups: %+v", backups)
	}
	if backups[0]["size"].(int64) != int64(len("ARCHIVE")) {
		t.Errorf("backup size: %+v", backups[0]["size"])
	}

	// Another database's backups are not listed for this one.
	empty := mustCall(t, r, "mongo.backups", `{"db":"other"}`)
	if len(empty["backups"].([]map[string]any)) != 0 {
		t.Errorf("another database's filter returned backups: %+v", empty)
	}

	mustCall(t, r, "mongo.restore", `{"file":"`+file+`","db":"shop","drop":true}`)
	h.Env.Jobs.Drain()
	restored := false
	for _, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "mongorestore ") && strings.Contains(c, "--nsInclude=shop.*") && strings.Contains(c, "--drop") {
			restored = true
		}
	}
	if !restored {
		t.Errorf("mongorestore was not called with the expected flags; calls: %v", h.Runner.Calls)
	}

	mustCall(t, r, "mongo.backup_delete", `{"file":"`+file+`"}`)
	after := mustCall(t, r, "mongo.backups", `{}`)
	if len(after["backups"].([]map[string]any)) != 0 {
		t.Errorf("the backup was not deleted: %+v", after)
	}

	// Path traversal and junk file names are refused before any work.
	for _, bad := range []string{
		`{"file":"../../etc/passwd","db":"shop"}`,
		`{"file":"shop.gz","db":"shop"}`,
		`{"file":"` + file + `","db":"a b"}`,
	} {
		if _, err := call(t, r, "mongo.restore", bad); err == nil {
			t.Errorf("restore accepted a bad request: %s", bad)
		}
	}
	if _, err := call(t, r, "mongo.backup_delete", `{"file":"../../etc/passwd"}`); err == nil {
		t.Error("delete accepted a traversal path")
	}
}

package migrate

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func syncRun(r *rig, id string) (error, string) {
	var log bytes.Buffer
	err := r.e.Sync(context.Background(), id, r.source(), &log)
	return err, log.String()
}

func TestSyncCopiesAgainWithoutReplacingNewerFilesAndLeavesDatabasesAlone(t *testing.T) {
	r := newRig(t)
	m := runDone(t, r)
	copies := r.src.rsyncCount()
	imports := len(r.db.imports)
	dumps := 0
	for _, c := range r.src.execs {
		if strings.Contains(c, "mysqldump") {
			dumps++
		}
	}
	if err, log := syncRun(r, m.ID); err != nil {
		t.Fatalf("sync: %v\n%s", err, log)
	}
	s := r.state(m.ID)
	if s.Status != Completed || s.Tasks[0].Status != Done || s.Tasks[1].Status != Done {
		t.Fatalf("state = %+v", s)
	}
	if r.src.rsyncCount() != copies+2 {
		t.Errorf("rsync calls %d → %d (expected a copy and a verification)", copies, r.src.rsyncCount())
	}
	last := strings.Join(r.src.rsyncs[copies], " ")
	if !strings.Contains(last, "--update") || strings.Contains(last, "--delete") {
		t.Errorf("sync args = %s", last)
	}
	if !strings.Contains(strings.Join(r.src.rsyncs[0], " "), "-a") || strings.Contains(strings.Join(r.src.rsyncs[0], " "), "--update") {
		t.Errorf("the first copy must not use --update: %v", r.src.rsyncs[0])
	}
	// The database was neither dumped nor imported again.
	after := 0
	for _, c := range r.src.execs {
		if strings.Contains(c, "mysqldump") {
			after++
		}
	}
	if len(r.db.imports) != imports || after != dumps {
		t.Errorf("the database was synchronised: %d imports, %d dumps", len(r.db.imports), after)
	}
	// Results of earlier checks are cleared: they described the old copy.
	if s.Validation != nil || s.Cutover != nil {
		t.Errorf("stale results kept: %+v %+v", s.Validation, s.Cutover)
	}
}

func TestSyncKeepsWhatWasCreatedSoARollbackStillWorks(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = nil
	m := runDone(t, r)
	// The source gained a file; the next copy brings it.
	ok := r.src.rsyncFn
	r.src.rsyncFn = func(n int, args []string, local string, out io.Writer) error {
		for _, a := range args {
			if a == "-n" {
				return nil
			}
		}
		os.WriteFile(filepath.Join(local, "new-on-source.txt"), []byte("n"), 0o644)
		io.WriteString(out, ">f+++++++++ new-on-source.txt\nNumber of regular files transferred: 1\nTotal transferred file size: 1 bytes\n")
		return nil
	}
	_ = ok
	if err, log := syncRun(r, m.ID); err != nil {
		t.Fatalf("sync: %v\n%s", err, log)
	}
	rep, err := r.e.Rollback(context.Background(), m.ID, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FilesRemoved != 3 {
		t.Errorf("a rollback after a sync removed %d files (the first copy's two and the synchronised one)", rep.FilesRemoved)
	}
	if _, err := os.Stat(filepath.Join(r.real, "new-on-source.txt")); err == nil {
		t.Error("the synchronised file was not rolled back")
	}
}

func TestSyncIsRefusedWhenItDoesNotApply(t *testing.T) {
	r := newRig(t)
	m := r.create()
	if err, _ := syncRun(r, m.ID); err == nil || !strings.Contains(err.Error(), "cannot be synchronised") {
		t.Errorf("a migration that never ran was synchronised: %v", err)
	}
	r2 := newRig(t)
	r2.plan.Files = nil
	m2 := runDone(t, r2)
	if err, _ := syncRun(r2, m2.ID); err == nil || !strings.Contains(err.Error(), "no website files") {
		t.Errorf("a databases-only migration was synchronised: %v", err)
	}
	r3 := newRig(t)
	r3.plan.Databases = nil
	m3 := runDone(t, r3)
	r3.e.Rollback(context.Background(), m3.ID, false, io.Discard)
	if err, _ := syncRun(r3, m3.ID); err == nil {
		t.Error("a rolled back migration was synchronised")
	}
	a, err := newRegistry(r.h).Lookup("migrate.sync")
	if err != nil || !a.Mutating {
		t.Errorf("action = %+v, %v", a, err)
	}
	// A login without a pinned key is refused before anything runs.
	if _, err := a.Handler(context.Background(), []byte(`{"id":"0123456789abcdef","source":{"host":"h.example.com","user":"root","password":"x"}}`)); err == nil {
		t.Error("a sync without a pinned key was accepted")
	}
}

func TestSyncFailureIsReportedAndCanBeContinued(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = nil
	m := runDone(t, r)
	r.src.rsyncFn = func(int, []string, string, io.Writer) error { return io.ErrUnexpectedEOF }
	err, _ := syncRun(r, m.ID)
	if err == nil || r.state(m.ID).Status != Failed {
		t.Fatalf("err = %v, status = %s", err, r.state(m.ID).Status)
	}
	r.src.rsyncFn = nil
	if err, log := r.run(m.ID); err != nil {
		t.Fatalf("continue: %v\n%s", err, log)
	}
	if r.state(m.ID).Status != Completed {
		t.Error("not completed after continuing")
	}
}

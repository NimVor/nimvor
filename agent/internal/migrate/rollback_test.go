package migrate

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestItemizedReadsNewFilesNewFoldersAndOverwrites(t *testing.T) {
	out := `cd+++++++++ ./
cd+++++++++ sub dir/
>f+++++++++ index.php
>f+++++++++ sub dir/a b.txt
>f.st...... changed.php
cL+++++++++ link -> /etc/passwd
>f+++++++++ bad\#012name
>f+++++++++ ../escape
>f+++++++++ /abs
.d..t...... .
Number of regular files transferred: 4
`
	files, dirs, over := itemized(out)
	if strings.Join(files, "|") != "index.php|sub dir/a b.txt" {
		t.Errorf("files = %v", files)
	}
	if strings.Join(dirs, "|") != "sub dir" {
		t.Errorf("dirs = %v", dirs)
	}
	if over != 1 {
		t.Errorf("overwritten = %d", over)
	}
}

func runDone(t *testing.T, r *rig) *Migration {
	m := r.create()
	if err, log := r.run(m.ID); err != nil {
		t.Fatalf("run: %v\n%s", err, log)
	}
	return m
}

func TestRollbackRemovesOnlyWhatTheMigrationCreatedAndLeavesThePlaceholder(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = nil
	m := runDone(t, r)
	if _, err := os.Stat(filepath.Join(r.real, "index.php")); err != nil {
		t.Fatal("the copy did not happen")
	}
	rep, err := r.e.Rollback(context.Background(), m.ID, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FilesRemoved != 2 || rep.DirsRemoved != 1 || len(rep.Kept) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(r.real, "index.php")); err == nil {
		t.Error("index.php was not removed")
	}
	if _, err := os.Stat(filepath.Join(r.real, "sub dir")); err == nil {
		t.Error("the folder the migration created was not removed")
	}
	// What was there before the migration is untouched.
	if b, _ := os.ReadFile(filepath.Join(r.real, "index.html")); !strings.Contains(string(b), "example.com") {
		t.Error("the placeholder page was removed")
	}
	s := r.state(m.ID)
	if s.Status != RolledBack || s.Rollback == nil || s.Finished == nil {
		t.Errorf("state = %+v", s)
	}
	// A rolled back migration cannot run or be rolled back again, and its destination is free.
	if err, _ := r.run(m.ID); err == nil {
		t.Error("a rolled back migration was run")
	}
	if _, err := r.e.Rollback(context.Background(), m.ID, false, io.Discard); err == nil || !strings.Contains(err.Error(), "already") {
		t.Errorf("second rollback = %v", err)
	}
	if _, err := r.e.Create(r.plan, "linux", "x"); err != nil {
		t.Errorf("the destination was not freed: %v", err)
	}
}

func TestRollbackNeverRemovesNewerDataOrFollowsALink(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = nil
	m := runDone(t, r)
	// The site's owner edited index.php after the migration, and replaced the other file with a link.
	os.WriteFile(filepath.Join(r.real, "index.php"), []byte("<?php echo 'my new work and more';"), 0o644)
	other := filepath.Join(r.real, "sub dir", "a b.txt")
	os.Remove(other)
	secret := filepath.Join(t.TempDir(), "precious")
	os.WriteFile(secret, []byte("do not delete"), 0o644)
	os.Symlink(secret, other)
	// ...and added a file of their own.
	os.WriteFile(filepath.Join(r.real, "mine.php"), []byte("mine"), 0o644)

	rep, err := r.e.Rollback(context.Background(), m.ID, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FilesRemoved != 0 || len(rep.Kept) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	for _, k := range rep.Kept {
		if !strings.Contains(k, "changed since the migration") {
			t.Errorf("kept = %q", k)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(r.real, "index.php")); !strings.Contains(string(b), "my new work") {
		t.Error("newer data was removed")
	}
	if b, _ := os.ReadFile(secret); string(b) != "do not delete" {
		t.Error("a file behind a link was removed")
	}
	if _, err := os.Stat(filepath.Join(r.real, "mine.php")); err != nil {
		t.Error("a file the migration did not create was removed")
	}
	// A folder that is not empty is kept.
	if _, err := os.Stat(filepath.Join(r.real, "sub dir")); err != nil {
		t.Error("a folder that still holds data was removed")
	}
}

func TestRollbackEmptiesADatabaseOnlyWhenConfirmedAndNotWrittenToSince(t *testing.T) {
	// Not confirmed: left alone, and it says so.
	r := newRig(t)
	r.plan.Files = nil
	m := runDone(t, r)
	rep, err := r.e.Rollback(context.Background(), m.ID, false, io.Discard)
	if err != nil || len(r.db.resets) != 0 || len(rep.DatabasesEmptied) != 0 || !strings.Contains(strings.Join(rep.Notes, " "), "left as it is") {
		t.Fatalf("rep = %+v err = %v resets = %v", rep, err, r.db.resets)
	}
	// Confirmed.
	r = newRig(t)
	r.plan.Files = nil
	m = runDone(t, r)
	if !r.state(m.ID).Tasks[0].StartedEmpty {
		t.Fatal("the database was not recorded as empty at the start")
	}
	rep, err = r.e.Rollback(context.Background(), m.ID, true, io.Discard)
	if err != nil || strings.Join(rep.DatabasesEmptied, ",") != "example_wp" || len(r.db.resets) != 1 {
		t.Fatalf("rep = %+v err = %v", rep, err)
	}
	// Written to after the import: kept, never silently emptied.
	r = newRig(t)
	r.plan.Files = nil
	m = runDone(t, r)
	r.db.newer = true
	rep, err = r.e.Rollback(context.Background(), m.ID, true, io.Discard)
	if err != nil || len(rep.DatabasesEmptied) != 0 || len(r.db.resets) != 0 || !strings.Contains(strings.Join(rep.Kept, " "), "newer data is never removed") {
		t.Fatalf("rep = %+v err = %v", rep, err)
	}
	// A database that had tables before (overwriting was allowed): its own data cannot be told apart.
	r = newRig(t)
	r.plan.Files = nil
	r.plan.Config.AllowOverwrite = true
	r.db.tables = 4
	m = runDone(t, r)
	rep, err = r.e.Rollback(context.Background(), m.ID, true, io.Discard)
	if err != nil || len(r.db.resets) != 0 || !strings.Contains(strings.Join(rep.Kept, " "), "had tables before") {
		t.Fatalf("rep = %+v err = %v", rep, err)
	}
}

func TestOverwrittenFilesAreReportedAsNotRestorable(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = nil
	r.plan.Config.AllowOverwrite = true
	os.WriteFile(filepath.Join(r.real, "old.php"), []byte("old"), 0o644)
	ok := r.src.rsyncFn
	r.src.rsyncFn = func(n int, args []string, local string, out io.Writer) error {
		err := ok(n, args, local, out)
		for _, a := range args {
			if a == "-n" {
				return err
			}
		}
		io.WriteString(out, ">f.st...... old.php\n")
		return err
	}
	m := runDone(t, r)
	rep, err := r.e.Rollback(context.Background(), m.ID, false, io.Discard)
	if err != nil || !strings.Contains(strings.Join(rep.Notes, " "), "1 existing file(s) were overwritten") {
		t.Fatalf("rep = %+v err = %v", rep, err)
	}
	if _, err := os.Stat(filepath.Join(r.real, "old.php")); err != nil {
		t.Error("an existing file was removed")
	}
}

func TestRollbackNeedsAFinishedStateAndRefusesWhileRunning(t *testing.T) {
	r := newRig(t)
	m := r.create() // created, never run
	if _, err := r.e.Rollback(context.Background(), m.ID, false, io.Discard); err == nil {
		t.Error("a migration that never ran was rolled back")
	}
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	r.src.rsyncFn = func(int, []string, string, io.Writer) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-block
		return nil
	}
	done := make(chan error, 1)
	go func() { err, _ := r.run(m.ID); done <- err }()
	<-entered
	if _, err := r.e.Rollback(context.Background(), m.ID, false, io.Discard); err == nil {
		t.Error("a running migration was rolled back")
	}
	close(block)
	<-done
}

func TestTheRollbackActionNeedsAnExplicitConfirmation(t *testing.T) {
	h := newRig(t)
	rg := newRegistry(h.h)
	a, err := rg.Lookup("migrate.rollback")
	if err != nil || !a.Mutating {
		t.Fatalf("action = %+v err = %v", a, err)
	}
	if _, err := a.Handler(context.Background(), []byte(`{"id":"0123456789abcdef"}`)); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Errorf("rollback without confirmation = %v", err)
	}
}

package migrate

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/sites"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

const secretPW = "S3cret-Source-Pw"

type fakeSrc struct {
	mu      sync.Mutex
	rsyncs  [][]string
	execs   []string
	rsyncFn func(n int, args []string, local string, out io.Writer) error
	execFn  func(cmd string, stdout io.Writer) (string, error)
}

func (f *fakeSrc) Describe() string { return "fake" }
func (f *fakeSrc) Exec(ctx context.Context, cmd string, stdout io.Writer) (string, error) {
	f.mu.Lock()
	f.execs = append(f.execs, cmd)
	fn := f.execFn
	f.mu.Unlock()
	if fn != nil {
		return fn(cmd, stdout)
	}
	return "", nil
}
func (f *fakeSrc) Rsync(ctx context.Context, extra []string, remote, local string, stdout io.Writer) (string, error) {
	f.mu.Lock()
	f.rsyncs = append(f.rsyncs, append(append([]string{}, extra...), remote, local))
	n := len(f.rsyncs)
	fn := f.rsyncFn
	f.mu.Unlock()
	if fn != nil {
		return "", fn(n, extra, local, stdout)
	}
	return "", nil
}
func (f *fakeSrc) rsyncCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.rsyncs) }

type fakeDB struct {
	mu      sync.Mutex
	exists  bool
	tables  int
	srcTabs int
	imports []string
	failImp int
	newer   bool
	resets  []string
}

func (d *fakeDB) Exists(ctx context.Context, name string) (bool, error) { return d.exists, nil }
func (d *fakeDB) Tables(ctx context.Context, name string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tables, nil
}
func (d *fakeDB) Reset(ctx context.Context, name string, since time.Time) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.newer {
		return true, nil
	}
	d.resets = append(d.resets, name)
	d.tables = 0
	return false, nil
}

func (d *fakeDB) Import(ctx context.Context, name string, r io.Reader, log io.Writer) error {
	b, err := io.ReadAll(r)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		return err
	}
	if d.failImp > 0 {
		d.failImp--
		d.tables = 1 // a partial import leaves tables behind
		return errors.New("import failed")
	}
	d.imports = append(d.imports, string(b))
	d.tables = d.srcTabs
	return nil
}

func gz(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(s))
	w.Close()
	return b.Bytes()
}

const goodDump = "-- MariaDB dump\nCREATE TABLE a (id int);\nINSERT INTO a VALUES (1);\n-- Dump completed on 2026-11-06\n"

type rig struct {
	t    *testing.T
	h    *testhost.Host
	e    *Engine
	src  *fakeSrc
	db   *fakeDB
	site string // NimVor docroot (host path)
	real string
	plan Plan
}

func newRig(t *testing.T) *rig {
	h := testhost.New(t)
	site := filepath.Join(h.Env.Cfg.SitesRoot, "example.com")
	real, _ := h.Env.Root.Path(site)
	os.MkdirAll(real, 0o755)
	if u, err := h.Env.LookupUser("example"); err == nil && os.Geteuid() == 0 {
		os.Chown(real, u.UID, u.GID)
	}
	os.WriteFile(filepath.Join(real, "index.html"), []byte(sites.DefaultIndex("example.com")), 0o644)
	src := &fakeSrc{}
	src.execFn = func(cmd string, stdout io.Writer) (string, error) {
		switch {
		case strings.Contains(cmd, "mysqldump"):
			stdout.Write(gz(goodDump))
			return "", nil
		case strings.Contains(cmd, "information_schema.tables"):
			return "1\n", nil
		}
		return "", nil
	}
	src.rsyncFn = func(n int, args []string, local string, out io.Writer) error {
		for _, a := range args {
			if a == "-n" { // the verification pass
				return nil
			}
		}
		os.MkdirAll(filepath.Join(local, "sub dir"), 0o755)
		os.WriteFile(filepath.Join(local, "index.php"), []byte("<?php echo 1;"), 0o644)
		os.WriteFile(filepath.Join(local, "sub dir", "a b.txt"), []byte("data"), 0o644)
		io.WriteString(out, "cd+++++++++ sub dir/\n>f+++++++++ index.php\n>f+++++++++ sub dir/a b.txt\nNumber of regular files transferred: 2\nTotal transferred file size: 17 bytes\n")
		return nil
	}
	db := &fakeDB{exists: true, srcTabs: 1}
	e := NewEngine(h.Env)
	e.Open = func(Source) (Puller, error) { return src, nil }
	e.DB = db
	e.Sleep = func(time.Duration) {}
	return &rig{t: t, h: h, e: e, src: src, db: db, site: site, real: real, plan: Plan{
		Files:     []FileTask{{Domain: "example.com", SourceRoot: "/home/alice/public_html", DestRoot: site, DestUser: "example", SizeKB: 10}},
		Databases: []DBTask{{Source: "alice_wp", Dest: "example_wp", SizeKB: 10}},
	}}
}

func (r *rig) source() Source {
	return Source{Host: "src.example.com", User: "root", Password: secretPW, HostKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"}
}

func (r *rig) create() *Migration {
	m, err := r.e.Create(r.plan, "cpanel", "ssh://root@src.example.com:22")
	if err != nil {
		r.t.Fatal(err)
	}
	return m
}

func (r *rig) run(id string) (error, string) {
	var log bytes.Buffer
	err := r.e.Run(context.Background(), id, r.source(), &log)
	return err, log.String()
}

func (r *rig) state(id string) *Migration {
	m, err := r.e.Store.Load(id)
	if err != nil {
		r.t.Fatal(err)
	}
	return m
}

func TestAMigrationCopiesFilesAndDatabaseAndIsVerified(t *testing.T) {
	r := newRig(t)
	m := r.create()
	err, log := r.run(m.ID)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, log)
	}
	s := r.state(m.ID)
	if s.Status != Completed || s.Finished == nil {
		t.Fatalf("status = %s (%s)", s.Status, s.Error)
	}
	for _, task := range s.Tasks {
		if task.Status != Done {
			t.Errorf("%s = %s %s", task.ID, task.Status, task.Error)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(r.real, "index.php")); string(b) != "<?php echo 1;" {
		t.Errorf("file not copied: %q", b)
	}
	ft, dt := s.Tasks[0], s.Tasks[1]
	if ft.Files != 2 || ft.Bytes != 17 || ft.Checkpoint != "copied" || ft.Differences != 0 {
		t.Errorf("files task = %+v", ft)
	}
	if dt.Checkpoint != "imported" || len(dt.SHA256) != 64 || !strings.Contains(dt.Detail, "1 tables imported") {
		t.Errorf("db task = %+v", dt)
	}
	if len(r.db.imports) != 1 || !strings.Contains(r.db.imports[0], "Dump completed") {
		t.Errorf("imports = %v", r.db.imports)
	}
	// The copy is a pull that never deletes, keeps symlinks inside the site, and gives the files to the website's account.
	args := strings.Join(r.src.rsyncs[0], " ")
	for _, want := range []string{"-a", "--safe-links", "--chown=", "--partial", "/home/alice/public_html/"} {
		if !strings.Contains(args, want) {
			t.Errorf("rsync args lack %q: %s", want, args)
		}
	}
	for _, bad := range []string{"--delete", "--remove-source-files", secretPW} {
		for _, call := range r.src.rsyncs {
			if strings.Contains(strings.Join(call, " "), bad) {
				t.Errorf("rsync call contains %q", bad)
			}
		}
	}
	if len(r.src.rsyncs) != 2 || !strings.Contains(strings.Join(r.src.rsyncs[1], " "), "-c") {
		t.Errorf("a checksum verification pass was not run: %v", r.src.rsyncs)
	}
	// Nothing was ever run on the source that changes it.
	for _, c := range r.src.execs {
		for _, bad := range []string{"rm ", "drop ", "DROP ", "delete", "mv ", "> /"} {
			if strings.Contains(c, bad) {
				t.Errorf("a command on the source could change it: %s", c)
			}
		}
	}
	// Temporary dumps are gone.
	if _, err := os.Stat(filepath.Join(r.h.Base, "var/lib/nimvor-agent/tmp", "migrate-"+m.ID)); err == nil {
		t.Error("temporary files left after a completed migration")
	}
	// The state file and the log hold no credential, and are private.
	raw, _ := os.ReadFile(filepath.Join(r.e.Store.Dir, m.ID+".json"))
	if bytes.Contains(raw, []byte(secretPW)) || strings.Contains(log, secretPW) {
		t.Error("the password was written to the state or the log")
	}
	if info, _ := os.Stat(filepath.Join(r.e.Store.Dir, m.ID+".json")); info.Mode().Perm() != 0o600 {
		t.Errorf("state mode = %v", info.Mode().Perm())
	}
}

func TestAWebsiteWithFilesIsNeverOverwrittenWithoutPermission(t *testing.T) {
	r := newRig(t)
	os.WriteFile(filepath.Join(r.real, "index.php"), []byte("my own site"), 0o644)
	r.plan.Databases = nil
	m := r.create()
	err, _ := r.run(m.ID)
	s := r.state(m.ID)
	if err == nil || s.Status != Failed || !strings.Contains(s.Tasks[0].Error, "already has files") {
		t.Fatalf("err = %v, state = %+v", err, s)
	}
	if s.Tasks[0].Attempts != 1 {
		t.Errorf("a refusal was retried %d times", s.Tasks[0].Attempts)
	}
	if r.src.rsyncCount() != 0 {
		t.Error("rsync ran on a website that has files")
	}
	if b, _ := os.ReadFile(filepath.Join(r.real, "index.php")); string(b) != "my own site" {
		t.Error("an existing file was changed")
	}
	if len(s.Remaining) == 0 {
		t.Error("a failed migration did not say what is left")
	}
	// With explicit permission it proceeds.
	r2 := newRig(t)
	os.WriteFile(filepath.Join(r2.real, "other.txt"), []byte("x"), 0o644)
	r2.plan.Databases = nil
	r2.plan.Config.AllowOverwrite = true
	m2 := r2.create()
	if err, log := r2.run(m2.ID); err != nil {
		t.Fatalf("allowed overwrite: %v\n%s", err, log)
	}
}

func TestADatabaseWithTablesIsNotImportedIntoAndAMissingOneIsReported(t *testing.T) {
	r := newRig(t)
	r.plan.Files = nil
	r.db.tables = 3
	m := r.create()
	err, _ := r.run(m.ID)
	s := r.state(m.ID)
	if err == nil || !strings.Contains(s.Tasks[0].Error, "already has 3 table") || len(r.db.imports) != 0 {
		t.Fatalf("err=%v task=%+v imports=%d", err, s.Tasks[0], len(r.db.imports))
	}
	r = newRig(t)
	r.plan.Files = nil
	r.db.exists = false
	m = r.create()
	if err, _ := r.run(m.ID); err == nil || !strings.Contains(r.state(m.ID).Tasks[0].Error, "create it in NimVor first") {
		t.Errorf("missing database: %v", err)
	}
}

func TestATransientFailureIsRetriedAndAPermanentOneStopsAndCanBeContinued(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = nil
	calls := 0
	ok := r.src.rsyncFn
	r.src.rsyncFn = func(n int, args []string, local string, out io.Writer) error {
		calls++
		if calls <= 2 {
			return errors.New("ssh: connection lost")
		}
		return ok(n, args, local, out)
	}
	m := r.create()
	if err, log := r.run(m.ID); err != nil {
		t.Fatalf("two disconnections were not survived: %v\n%s", err, log)
	}
	if got := r.state(m.ID).Tasks[0]; got.Attempts != 3 || got.Status != Done {
		t.Errorf("task = %+v", got)
	}

	// The connection never comes back: the task fails after three tries and says so.
	r = newRig(t)
	r.plan.Databases = nil
	r.src.rsyncFn = func(int, []string, string, io.Writer) error {
		return errors.New("ssh: connect to host: Connection refused")
	}
	m = r.create()
	err, _ := r.run(m.ID)
	s := r.state(m.ID)
	if err == nil || s.Status != Failed || s.Tasks[0].Attempts != 3 || !strings.Contains(s.Tasks[0].Error, "Connection refused") {
		t.Fatalf("err = %v, state = %+v", err, s)
	}
	if len(s.Remaining) == 0 || !strings.Contains(s.Remaining[0], "not complete") {
		t.Errorf("remaining = %v", s.Remaining)
	}
	// The source comes back: continuing finishes the job.
	r.src.rsyncFn = func(n int, args []string, local string, out io.Writer) error {
		io.WriteString(out, "Number of regular files transferred: 2\nTotal transferred file size: 5 bytes\n")
		return nil
	}
	if err, log := r.run(m.ID); err != nil {
		t.Fatalf("resume: %v\n%s", err, log)
	}
	if s := r.state(m.ID); s.Status != Completed || s.Tasks[0].Files != 2 {
		t.Errorf("after resume: %+v", s)
	}
}

func TestResumingDoesNotRepeatWhatIsDone(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = []DBTask{{Source: "alice_wp", Dest: "example_wp"}}
	r.db.failImp = 3 // the database never imports: three attempts fail
	m := r.create()
	err, _ := r.run(m.ID)
	s := r.state(m.ID)
	if err == nil || s.Tasks[0].Status != Done || s.Tasks[1].Status != Failed || s.Tasks[1].Checkpoint != "importing" {
		t.Fatalf("err = %v, tasks = %+v", err, s.Tasks)
	}
	if !strings.Contains(strings.Join(s.Remaining, " "), "may be incomplete") {
		t.Errorf("remaining = %v", s.Remaining)
	}
	filesCalls := r.src.rsyncCount()
	// The destination now has the tables of the failed import: continuing is allowed because this migration began it.
	if err, log := r.run(m.ID); err != nil {
		t.Fatalf("resume: %v\n%s", err, log)
	}
	if r.src.rsyncCount() != filesCalls {
		t.Errorf("the files were copied again: %d → %d rsync calls", filesCalls, r.src.rsyncCount())
	}
	if s := r.state(m.ID); s.Status != Completed || s.Tasks[1].Checkpoint != "imported" {
		t.Errorf("after resume: %+v", s)
	}
}

func TestAnIncompleteDumpIsNeverImported(t *testing.T) {
	r := newRig(t)
	r.plan.Files = nil
	r.src.execFn = func(cmd string, stdout io.Writer) (string, error) {
		if strings.Contains(cmd, "mysqldump") {
			stdout.Write(gz("CREATE TABLE a (id int);\nINSERT INTO a VALUES (1);\n")) // cut off: no completion line
		}
		return "", nil
	}
	m := r.create()
	err, _ := r.run(m.ID)
	if err == nil || len(r.db.imports) != 0 {
		t.Fatalf("err = %v, imports = %d", err, len(r.db.imports))
	}
	if e := r.state(m.ID).Tasks[0].Error; !strings.Contains(e, "incomplete") {
		t.Errorf("error = %q", e)
	}
	dir, _ := r.h.Env.Root.Path(r.e.tempDir(r.state(m.ID)))
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("partial dump left: %v", ents)
	}
	// A dump cut in the middle of the gzip stream is refused too.
	r2 := newRig(t)
	r2.plan.Files = nil
	r2.src.execFn = func(cmd string, stdout io.Writer) (string, error) {
		if strings.Contains(cmd, "mysqldump") {
			b := gz(goodDump)
			stdout.Write(b[:len(b)-6])
		}
		return "", nil
	}
	m2 := r2.create()
	if err, _ := r2.run(m2.ID); err == nil || len(r2.db.imports) != 0 {
		t.Errorf("a truncated gzip was imported: %v", err)
	}
}

func TestTableCountsMustMatchAfterTheImport(t *testing.T) {
	r := newRig(t)
	r.plan.Files = nil
	r.db.srcTabs = 5 // the import yields 5 tables but the source says 1
	m := r.create()
	err, _ := r.run(m.ID)
	if err == nil || !strings.Contains(r.state(m.ID).Tasks[0].Error, "has 5 table(s) after the import but the source has 1") {
		t.Fatalf("err = %v, task = %+v", err, r.state(m.ID).Tasks[0])
	}
}

func TestCancelStopsAtOnceKeepsWhatWasCopiedAndNeverTouchesTheSource(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = nil
	started := make(chan struct{})
	r.src.rsyncFn = func(n int, args []string, local string, out io.Writer) error {
		os.WriteFile(filepath.Join(local, "partial.bin"), []byte("half"), 0o644)
		close(started)
		time.Sleep(5 * time.Second) // a long transfer; the context ends it
		return nil
	}
	// The fake ignores ctx, so cancel by making rsync return when asked to stop.
	stopped := make(chan struct{})
	r.src.rsyncFn = func(n int, args []string, local string, out io.Writer) error {
		os.WriteFile(filepath.Join(local, "partial.bin"), []byte("half"), 0o644)
		select {
		case started <- struct{}{}:
		default:
		}
		<-stopped
		return errors.New("transfer interrupted")
	}
	started = make(chan struct{}, 1)
	m := r.create()
	done := make(chan error, 1)
	go func() { err, _ := r.run(m.ID); done <- err }()
	<-started
	if err := r.e.Stop(m.ID, Cancelled); err != nil {
		t.Fatal(err)
	}
	close(stopped)
	if err := <-done; err != nil {
		t.Fatalf("a cancelled migration returned an error: %v", err)
	}
	s := r.state(m.ID)
	if s.Status != Cancelled || s.Tasks[0].Status != Pending || !strings.Contains(s.Error, "nothing was removed from the source") {
		t.Fatalf("state = %+v", s)
	}
	if _, err := os.Stat(filepath.Join(r.real, "partial.bin")); err != nil {
		t.Error("cancelling removed what was copied")
	}
	// A cancelled migration cannot be run again.
	if err, _ := r.run(m.ID); err == nil {
		t.Error("a cancelled migration was run")
	}
}

func TestPauseThenResume(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = nil
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	first := true
	r.src.rsyncFn = func(n int, args []string, local string, out io.Writer) error {
		if first {
			first = false
			entered <- struct{}{}
			<-release
			return errors.New("interrupted")
		}
		return nil
	}
	m := r.create()
	done := make(chan error, 1)
	go func() { err, _ := r.run(m.ID); done <- err }()
	<-entered
	if err := r.e.Stop(m.ID, Paused); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s := r.state(m.ID); s.Status != Paused || s.Tasks[0].Status != Pending {
		t.Fatalf("paused state = %+v", s)
	}
	if err, log := r.run(m.ID); err != nil {
		t.Fatalf("resume: %v\n%s", err, log)
	}
	if r.state(m.ID).Status != Completed {
		t.Error("not completed after resume")
	}
}

func TestAnAgentRestartLeavesAnInterruptedMigrationThatCanBeContinued(t *testing.T) {
	r := newRig(t)
	r.plan.Databases = nil
	m := r.create()
	// The state as the agent left it when it was killed in the middle of a copy.
	s := r.state(m.ID)
	s.Status = Running
	s.Tasks[0].Status = Running
	s.Tasks[0].Attempts = 1
	s.Tasks[0].Checkpoint = "copying"
	if err := r.e.Store.Save(s); err != nil {
		t.Fatal(err)
	}
	// A new agent process starts.
	fresh := NewEngine(r.h.Env)
	fresh.Open, fresh.DB, fresh.Sleep = r.e.Open, r.db, r.e.Sleep
	fresh.Reconcile()
	got, _ := fresh.Store.Load(m.ID)
	if got.Status != Interrupted || got.Tasks[0].Status != Pending || !strings.Contains(got.Error, "agent stopped") {
		t.Fatalf("after restart = %+v", got)
	}
	var log bytes.Buffer
	if err := fresh.Run(context.Background(), m.ID, r.source(), &log); err != nil {
		t.Fatalf("continue: %v\n%s", err, log.String())
	}
	if got, _ := fresh.Store.Load(m.ID); got.Status != Completed {
		t.Errorf("status = %s", got.Status)
	}
}

func TestTwoMigrationsNeverFillTheSameDestinationAndOneRunsOnce(t *testing.T) {
	r := newRig(t)
	m := r.create()
	if _, err := r.e.Create(r.plan, "cpanel", "x"); err == nil || !strings.Contains(err.Error(), m.ID) {
		t.Errorf("a second migration to the same website was accepted: %v", err)
	}
	other := r.plan
	other.Files = nil
	other.Databases = []DBTask{{Source: "x", Dest: "example_wp"}}
	if _, err := r.e.Create(other, "cpanel", "x"); err == nil {
		t.Error("a second migration into the same database was accepted")
	}
	// Running the same migration twice at once is refused.
	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	r.src.rsyncFn = func(n int, args []string, local string, out io.Writer) error {
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
	if err, _ := r.run(m.ID); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second run = %v", err)
	}
	close(block)
	<-done
}

func TestNothingIsCopiedOrDumpedWhenTheDiskIsBelowTheReserve(t *testing.T) {
	r := newRig(t)
	old := freeKB
	freeKB = func(string) int64 { return 1000 } // about 1 MiB free
	defer func() { freeKB = old }()
	m := r.create()
	err, _ := r.run(m.ID)
	s := r.state(m.ID)
	if err == nil || s.Status != Failed {
		t.Fatalf("err = %v, state = %+v", err, s)
	}
	for _, task := range s.Tasks {
		if task.Status != Failed || !strings.Contains(task.Error, "keeps") || task.Attempts != 1 {
			t.Errorf("%s = %+v", task.ID, task)
		}
	}
	if r.src.rsyncCount() != 0 || len(r.src.execs) != 0 {
		t.Errorf("the source was used although the disk is full: %d rsync, %d commands", r.src.rsyncCount(), len(r.src.execs))
	}
}

func TestTheSSHSessionKeepsSecretsOutOfArgumentsAndRemovesThem(t *testing.T) {
	h := testhost.New(t)
	var seenArgs []string
	var seenEnv []string
	var files map[string]string
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "ssh" {
			seenArgs = args
		}
		return "", nil, false
	}
	src := Source{Host: "src.example.com", Port: 2222, User: "root", Password: secretPW, HostKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"}
	tr, err := NewSSH(h.Env, src)
	if err != nil {
		t.Fatal(err)
	}
	st := tr.(*sshTransport)
	s, err := st.open()
	if err != nil {
		t.Fatal(err)
	}
	seenEnv = s.env
	files = map[string]string{}
	ents, _ := os.ReadDir(s.dir)
	for _, e := range ents {
		info, _ := e.Info()
		files[e.Name()] = info.Mode().Perm().String()
	}
	if files["pw"] != "-rw-------" || files["known_hosts"] != "-rw-------" || files["askpass"] != "-rwx------" {
		t.Errorf("modes = %v", files)
	}
	if kh, _ := os.ReadFile(filepath.Join(s.dir, "known_hosts")); !strings.HasPrefix(string(kh), "[src.example.com]:2222 ssh-ed25519 ") {
		t.Errorf("known_hosts = %q", kh)
	}
	opts := strings.Join(s.opt, " ")
	for _, want := range []string{"StrictHostKeyChecking=yes", "GlobalKnownHostsFile=/dev/null", "-F /dev/null", "IdentitiesOnly=yes", "-p 2222", "PreferredAuthentications=password"} {
		if !strings.Contains(opts, want) {
			t.Errorf("ssh options lack %q: %s", want, opts)
		}
	}
	if strings.Contains(opts, secretPW) || strings.Contains(s.sshCommand(), secretPW) {
		t.Error("the password is in the ssh options")
	}
	haveAskpass := false
	for _, e := range seenEnv {
		if strings.HasPrefix(e, "SSH_ASKPASS=") {
			haveAskpass = true
		}
		if strings.Contains(e, secretPW) {
			t.Errorf("the password itself is in the environment: %s", e)
		}
	}
	if !haveAskpass {
		t.Error("no askpass helper")
	}
	dir := s.dir
	s.close()
	if _, err := os.Stat(dir); err == nil {
		t.Error("the session folder was not removed")
	}
	// A command goes through ssh with the user and host and the command after "--".
	if _, err := tr.Exec(context.Background(), "echo hi", nil); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(seenArgs, " ")
	if strings.Contains(joined, secretPW) || !strings.HasSuffix(joined, "root@src.example.com -- echo hi") {
		t.Errorf("ssh args = %v", seenArgs)
	}
	// An error message never shows the password.
	h.Fail["ssh"] = "Permission denied: " + secretPW
	_, err = tr.Exec(context.Background(), "id", nil)
	if err == nil || strings.Contains(err.Error(), secretPW) {
		t.Errorf("err = %v", err)
	}
	// A private key login uses BatchMode and no password helper.
	key := Source{Host: "src.example.com", User: "root", PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----", HostKey: src.HostKey}
	kt, _ := NewSSH(h.Env, key)
	ks, err := kt.(*sshTransport).open()
	if err != nil {
		t.Fatal(err)
	}
	defer ks.close()
	if !strings.Contains(strings.Join(ks.opt, " "), "BatchMode=yes") {
		t.Errorf("key options = %v", ks.opt)
	}
	if info, err := os.Stat(filepath.Join(ks.dir, "key")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("key file = %v %v", info, err)
	}
	// Without a pinned host key nothing connects.
	if _, err := NewSSH(h.Env, Source{Host: "h.example.com", User: "root", Password: "x"}); err == nil {
		t.Error("a transport without a pinned host key was made")
	}
}

func TestEveryActionIsRegisteredAndTheOnesThatChangeThingsAreGated(t *testing.T) {
	h := testhost.New(t)
	rg := newRegistry(h)
	want := map[string]bool{"migrate.panels": false, "migrate.host_key": false, "migrate.test": false, "migrate.discover": false, "migrate.assess": false,
		"migrate.create": true, "migrate.start": true, "migrate.resume": true, "migrate.pause": true, "migrate.cancel": true, "migrate.get": false, "migrate.list": false}
	for name, mutating := range want {
		a, err := rg.Lookup(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if a.Mutating != mutating {
			t.Errorf("%s mutating = %v, want %v", name, a.Mutating, mutating)
		}
	}
	// A login without a pinned key, or a bad id, is refused before anything runs.
	for name, body := range map[string]string{
		"migrate.start":  `{"id":"0123456789abcdef","source":{"host":"h.example.com","user":"root","password":"x"}}`,
		"migrate.test":   `{"source":{"host":"h.example.com","user":"root","password":"x"}}`,
		"migrate.get":    `{"id":"../../etc/passwd"}`,
		"migrate.cancel": `{"id":"nope"}`,
	} {
		a, _ := rg.Lookup(name)
		if _, err := a.Handler(context.Background(), []byte(body)); err == nil {
			t.Errorf("%s accepted %s", name, body)
		}
	}
	// The plan list is honest about proof: every adapter is experimental.
	for _, d := range Supported() {
		if d.Support != Experimental {
			t.Errorf("%s claims %s support; nothing here has run against a real server", d.Panel, d.Support)
		}
	}
}

func TestStateIsReadBackExactly(t *testing.T) {
	r := newRig(t)
	m := r.create()
	got, err := r.e.Store.Load(m.ID)
	if err != nil || got.ID != m.ID || len(got.Tasks) != 2 || got.Tasks[0].ID != "files:"+r.site || got.Tasks[1].ID != "db:example_wp" {
		t.Fatalf("got = %+v, err = %v", got, err)
	}
	if _, err := r.e.Store.Load("../x"); err == nil {
		t.Error("a path was accepted as a migration id")
	}
	if _, err := r.e.Store.Load("0123456789abcdef"); err == nil {
		t.Error("a missing migration was found")
	}
}

func newRegistry(h *testhost.Host) *actions.Registry {
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	return r
}

package migrate

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/dbmgr"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/sites"
)

// DB is what the engine needs of the destination's database server.
type DB interface {
	Exists(ctx context.Context, name string) (bool, error)
	Tables(ctx context.Context, name string) (int, error)
	Import(ctx context.Context, name string, r io.Reader, log io.Writer) error
	// Reset empties a database the migration filled, unless a table was written to after
	// since (newer=true: nothing is dropped).
	Reset(ctx context.Context, name string, since time.Time) (newer bool, err error)
}

type realDB struct{ c dbmgr.Client }

func (d realDB) Exists(ctx context.Context, name string) (bool, error) {
	rows, err := d.c.Query(ctx, "SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = "+dbmgr.Quote(name)+";")
	return err == nil && len(rows) == 1 && rows[0][0] != "0", err
}

func (d realDB) Tables(ctx context.Context, name string) (int, error) {
	rows, err := d.c.Query(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = "+dbmgr.Quote(name)+";")
	if err != nil || len(rows) != 1 {
		return 0, fmt.Errorf("the tables of %s could not be counted", name)
	}
	return strconv.Atoi(rows[0][0])
}

func (d realDB) Reset(ctx context.Context, name string, since time.Time) (bool, error) {
	if !dbNameRe.MatchString(name) {
		return false, fmt.Errorf("invalid database name")
	}
	rows, err := d.c.Query(ctx, fmt.Sprintf("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = %s AND update_time > FROM_UNIXTIME(%d);", dbmgr.Quote(name), since.Add(5*time.Second).Unix()))
	if err != nil || len(rows) != 1 {
		return false, fmt.Errorf("the tables of %s could not be checked", name)
	}
	if rows[0][0] != "0" {
		return true, nil
	}
	cs, err := d.c.Query(ctx, "SELECT default_character_set_name, default_collation_name FROM information_schema.schemata WHERE schema_name = "+dbmgr.Quote(name)+";")
	if err != nil || len(cs) != 1 || len(cs[0]) < 2 {
		return false, fmt.Errorf("the character set of %s could not be read", name)
	}
	if !regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(cs[0][0]) || !regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(cs[0][1]) {
		return false, fmt.Errorf("unexpected character set")
	}
	// Dropping and creating again with the same character set empties it; the grants on it stay.
	sql := fmt.Sprintf("DROP DATABASE `%[1]s`; CREATE DATABASE `%[1]s` CHARACTER SET %[2]s COLLATE %[3]s;", name, cs[0][0], cs[0][1])
	return false, d.c.Exec(ctx, "empty "+name, sql)
}

func (d realDB) Import(ctx context.Context, name string, r io.Reader, log io.Writer) error {
	return d.c.ImportFrom(ctx, name, r, log)
}

// Engine runs migrations.
type Engine struct {
	Env   *host.Env
	Store *Store
	// Open connects to a source; tests replace it.
	Open  func(src Source) (Puller, error)
	DB    DB
	Sleep func(time.Duration)

	mu      sync.Mutex
	running map[string]context.CancelFunc
	reason  map[string]string
}

// NewEngine builds the engine with the real SSH transport and database client.
func NewEngine(env *host.Env) *Engine {
	dir, err := env.Root.Path(filepath.Join(env.Cfg.StateDir, "migrations"))
	if err != nil {
		dir = filepath.Join(env.Cfg.StateDir, "migrations")
	}
	return &Engine{
		Env:   env,
		Store: &Store{Dir: dir},
		Open: func(src Source) (Puller, error) {
			t, err := NewSSH(env, src)
			if err != nil {
				return nil, err
			}
			return t.(*sshTransport), nil
		},
		DB:      realDB{c: dbmgr.Client{Env: env}},
		Sleep:   time.Sleep,
		running: map[string]context.CancelFunc{},
		reason:  map[string]string{},
	}
}

// fatal marks an error that a retry cannot fix.
type fatal struct{ error }

func (f fatal) Unwrap() error { return f.error }

// Create validates a plan and stores a migration that is ready to run.
func (e *Engine) Create(p Plan, panel, source string) (*Migration, error) {
	if err := p.Validate(e.Env); err != nil {
		return nil, err
	}
	// Two migrations never fill the same destination at once.
	for _, o := range e.Store.List() {
		if !Active(o.Status) {
			continue
		}
		for _, f := range p.Files {
			for _, of := range o.Plan.Files {
				if f.DestRoot == of.DestRoot {
					return nil, fmt.Errorf("%s is already the destination of an unfinished migration (%s)", f.DestRoot, o.ID)
				}
			}
		}
		for _, d := range p.Databases {
			for _, od := range o.Plan.Databases {
				if d.Dest == od.Dest {
					return nil, fmt.Errorf("the database %s is already the destination of an unfinished migration (%s)", d.Dest, o.ID)
				}
			}
		}
	}
	m := &Migration{ID: newID(), Status: Created, Panel: panel, Source: source, Plan: p, Created: time.Now().UTC()}
	for _, f := range p.Files {
		m.Tasks = append(m.Tasks, Task{ID: "files:" + f.DestRoot, Kind: "files", Label: "Files of " + f.Domain, Status: Pending})
	}
	for _, d := range p.Databases {
		m.Tasks = append(m.Tasks, Task{ID: "db:" + d.Dest, Kind: "db", Label: "Database " + d.Source + " → " + d.Dest, Status: Pending})
	}
	return m, e.Store.Save(m)
}

// Reconcile marks the migrations that were running when the agent stopped.
// Their tasks go back to pending; continuing needs the login again.
func (e *Engine) Reconcile() {
	for _, m := range e.Store.List() {
		if m.Status != Running {
			continue
		}
		m.Status = Interrupted
		m.Error = "The agent stopped while this migration was running. Continue it to finish the remaining tasks."
		for i := range m.Tasks {
			if m.Tasks[i].Status == Running {
				m.Tasks[i].Status = Pending
			}
		}
		_ = e.Store.Save(m)
	}
}

// Stop asks a running migration to stop; its unfinished tasks stay pending.
func (e *Engine) Stop(id, why string) error {
	e.mu.Lock()
	cancel, ok := e.running[id]
	if ok {
		e.reason[id] = why
	}
	e.mu.Unlock()
	if ok {
		cancel()
		return nil
	}
	m, err := e.Store.Load(id)
	if err != nil {
		return err
	}
	if why == Cancelled && Active(m.Status) {
		m.Status = Cancelled
		now := time.Now().UTC()
		m.Finished = &now
		e.cleanTemp(id)
		return e.Store.Save(m)
	}
	return fmt.Errorf("the migration is not running")
}

func (e *Engine) tempDir(m *Migration) string {
	base := m.Plan.Config.TempDir
	if base == "" {
		base = filepath.Join(e.Env.Cfg.StateDir, "tmp")
	}
	return filepath.Join(base, "migrate-"+m.ID)
}

func (e *Engine) cleanTemp(id string) {
	if m, err := e.Store.Load(id); err == nil {
		if real, err := e.Env.Root.Path(e.tempDir(m)); err == nil {
			_ = os.RemoveAll(real)
		}
	}
}

// Run executes the tasks that are not done. It returns when they are all
// finished, one failed for good, or the migration was stopped.
func (e *Engine) Run(ctx context.Context, id string, src Source, log io.Writer) error {
	m, err := e.Store.Load(id)
	if err != nil {
		return err
	}
	if m.Status == Completed || m.Status == Cancelled || m.Status == RolledBack {
		return fmt.Errorf("this migration is %s", m.Status)
	}
	if err := src.Validate(true); err != nil {
		return err
	}
	t, err := e.Open(src)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.mu.Lock()
	if _, busy := e.running[id]; busy {
		e.mu.Unlock()
		return fmt.Errorf("this migration is already running")
	}
	e.running[id] = cancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, id)
		delete(e.reason, id)
		e.mu.Unlock()
	}()

	// The plan is checked again: the destination may have changed since it was made.
	if err := m.Plan.Validate(e.Env); err != nil {
		return e.finish(m, Failed, "The plan no longer fits this server: "+err.Error())
	}
	m.Status, m.Error, m.Finished = Running, "", nil
	for i := range m.Tasks {
		if m.Tasks[i].Status == Failed {
			m.Tasks[i].Status = Pending // a failed task is tried again when continued
		}
	}
	_ = e.Store.Save(m)

	var smu sync.Mutex // guards m while tasks run at the same time
	update := func(i int, fn func(t *Task)) {
		smu.Lock()
		fn(&m.Tasks[i])
		_ = e.Store.Save(m)
		smu.Unlock()
	}
	run := func(i int) {
		task := m.Tasks[i]
		if task.Status == Done {
			return
		}
		for attempt := 1; attempt <= 3; attempt++ {
			if ctx.Err() != nil {
				return
			}
			now := time.Now().UTC()
			update(i, func(t *Task) { t.Status, t.Attempts, t.Started, t.Error = Running, t.Attempts+1, &now, "" })
			fmt.Fprintf(log, "[%s] %s (attempt %d)\n", task.ID, task.Label, attempt)
			var err error
			switch task.Kind {
			case "files":
				err = e.runFiles(ctx, m, t, i, update, log)
			case "db":
				err = e.runDB(ctx, m, t, i, update, log)
			}
			if err == nil {
				fin := time.Now().UTC()
				update(i, func(t *Task) { t.Status, t.Finished = Done, &fin })
				return
			}
			if ctx.Err() != nil {
				update(i, func(t *Task) { t.Status = Pending })
				return
			}
			var ft fatal
			update(i, func(t *Task) { t.Status, t.Error = Failed, err.Error() })
			fmt.Fprintf(log, "[%s] failed: %v\n", task.ID, err)
			if errors.As(err, &ft) || attempt == 3 {
				return
			}
			e.Sleep(time.Duration(attempt*2) * time.Second)
		}
	}

	// Files first (several at once), then databases one after the other.
	sem := make(chan struct{}, m.Plan.Config.Workers)
	var wg sync.WaitGroup
	for i := range m.Tasks {
		if m.Tasks[i].Kind != "files" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			run(i)
		}(i)
	}
	wg.Wait()
	for i := range m.Tasks {
		if m.Tasks[i].Kind == "db" && ctx.Err() == nil {
			run(i)
		}
	}

	e.mu.Lock()
	why := e.reason[id]
	e.mu.Unlock()
	smu.Lock()
	defer smu.Unlock()
	failed, pending := 0, 0
	for _, t := range m.Tasks {
		switch t.Status {
		case Failed:
			failed++
		case Pending, Running:
			pending++
		}
	}
	switch {
	case ctx.Err() != nil && why == Cancelled:
		e.cleanTemp(id)
		return e.finish(m, Cancelled, "Cancelled. What was copied so far stays in the destination; nothing was removed from the source.")
	case ctx.Err() != nil:
		return e.finish(m, Paused, "")
	case failed > 0:
		return e.finish(m, Failed, fmt.Sprintf("%d task(s) failed. Fix the cause and continue: tasks that are done are not repeated.", failed))
	case pending > 0:
		return e.finish(m, Failed, "Some tasks did not run.")
	}
	e.cleanTemp(id)
	return e.finish(m, Completed, "")
}

// finish stores the final state. Failed migrations say what is left.
func (e *Engine) finish(m *Migration, status, msg string) error {
	now := time.Now().UTC()
	m.Status, m.Error = status, msg
	if status != Paused {
		m.Finished = &now
	}
	m.Remaining = nil
	if status == Failed || status == Cancelled || status == Paused {
		for _, t := range m.Tasks {
			switch {
			case t.Kind == "files" && t.Status != Done:
				m.Remaining = append(m.Remaining, "Files of "+strings.TrimPrefix(t.ID, "files:")+" are not complete (copied so far stay in place; continuing copies the rest).")
			case t.Kind == "db" && t.Status != Done && t.Checkpoint == "importing":
				m.Remaining = append(m.Remaining, "The database "+strings.TrimPrefix(t.ID, "db:")+" was being imported and may be incomplete: continuing imports it again over its own tables.")
			case t.Kind == "db" && t.Status != Done:
				m.Remaining = append(m.Remaining, "The database "+strings.TrimPrefix(t.ID, "db:")+" was not imported.")
			}
		}
	}
	if err := e.Store.Save(m); err != nil {
		return err
	}
	if status == Failed {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

var (
	rsyncFiles = regexp.MustCompile(`(?m)^Number of regular files transferred: ([\d,]+)`)
	rsyncBytes = regexp.MustCompile(`(?m)^Total transferred file size: ([\d,]+)`)
)

func num(s string) int64 {
	n, _ := strconv.ParseInt(strings.ReplaceAll(s, ",", ""), 10, 64)
	return n
}

// destEmpty reports whether a website folder holds nothing but the
// placeholder page NimVor writes for a new website.
func destEmpty(real, domain string) (bool, error) {
	ents, err := os.ReadDir(real)
	if err != nil {
		return false, err
	}
	for _, e := range ents {
		if e.Name() == "index.html" && !e.IsDir() {
			if b, err := os.ReadFile(filepath.Join(real, "index.html")); err == nil && sites.PlaceholderIndex(b, domain) {
				continue
			}
		}
		return false, nil
	}
	return true, nil
}

func (e *Engine) runFiles(ctx context.Context, m *Migration, t Puller, i int, update func(int, func(*Task)), log io.Writer) error {
	f := m.Plan.Files[fileIndex(m, m.Tasks[i].ID)]
	if err := checkDest(e.Env, &f); err != nil {
		return fatal{err}
	}
	real, _ := e.Env.Root.Path(f.DestRoot)
	_, u, err := e.Env.Credential(f.DestUser)
	if err != nil {
		return fatal{err}
	}
	info, err := os.Stat(real)
	if err != nil {
		return fatal{err}
	}
	gid := u.GID
	if st := sysGID(info); st >= 0 {
		gid = st
	}
	// The first copy only fills an empty website: files already there are never
	// overwritten unless the person said so. A copy that was started by this
	// migration is continued.
	first := m.Tasks[i].Attempts <= 1 && m.Tasks[i].Bytes == 0 && m.Tasks[i].Checkpoint == ""
	if first && !m.Plan.Config.AllowOverwrite {
		empty, err := destEmpty(real, f.Domain)
		if err != nil {
			return fatal{err}
		}
		if !empty {
			return fatal{fmt.Errorf("%s already has files. Migrating into it could overwrite them; choose an empty website, or allow overwriting explicitly", f.DestRoot)}
		}
	}
	if free := freeKB(real); free < reserveKB {
		return fatal{fmt.Errorf("the disk of %s has only %s free; NimVor keeps %s free, so nothing was copied", f.DestRoot, kb(free), kb(reserveKB))}
	}
	update(i, func(t *Task) { t.Checkpoint = "copying" })
	args := []string{"-a", "-i", "-8", "--partial", "--safe-links", "--stats", "--no-specials", "--no-devices",
		fmt.Sprintf("--chown=%d:%d", u.UID, gid)}
	if m.Plan.Config.Compress {
		args = append(args, "-z")
	}
	for _, x := range m.Plan.Config.Excludes {
		args = append(args, "--exclude="+x)
	}
	var out strings.Builder
	if _, err := t.Rsync(ctx, args, strings.TrimSuffix(f.SourceRoot, "/")+"/", strings.TrimSuffix(real, "/")+"/", &out); err != nil {
		return fmt.Errorf("the files could not be copied: %v", err)
	}
	files, bytes := int64(0), int64(0)
	if mm := rsyncFiles.FindStringSubmatch(out.String()); mm != nil {
		files = num(mm[1])
	}
	if mm := rsyncBytes.FindStringSubmatch(out.String()); mm != nil {
		bytes = num(mm[1])
	}
	if err := e.recordCreated(m.ID, m.Tasks[i].ID, real, out.String()); err != nil {
		return fmt.Errorf("what was copied could not be recorded: %v", err)
	}
	// Verification: a second pass that compares by checksum and lists what differs.
	vargs := []string{"-a", "-n", "-c", "--safe-links", "--itemize-changes", "--no-specials", "--no-devices", "--no-owner", "--no-group"}
	for _, x := range m.Plan.Config.Excludes {
		vargs = append(vargs, "--exclude="+x)
	}
	var vout strings.Builder
	if _, err := t.Rsync(ctx, vargs, strings.TrimSuffix(f.SourceRoot, "/")+"/", strings.TrimSuffix(real, "/")+"/", &vout); err != nil {
		return fmt.Errorf("the copy could not be verified: %v", err)
	}
	diff := 0
	for _, l := range strings.Split(vout.String(), "\n") {
		if strings.HasPrefix(l, ">f") {
			diff++
		}
	}
	update(i, func(t *Task) {
		t.Files, t.Bytes, t.Differences, t.Checkpoint = files, bytes, diff, "copied"
		t.Detail = fmt.Sprintf("%d files, %d bytes copied; %d differ after verification", files, bytes, diff)
	})
	if diff > 0 {
		fmt.Fprintf(log, "[%s] %d file(s) differ from the source: the site changed while it was copied. Run the copy again to bring them over.\n", m.Tasks[i].ID, diff)
	}
	return nil
}

func fileIndex(m *Migration, id string) int {
	for i, f := range m.Plan.Files {
		if "files:"+f.DestRoot == id {
			return i
		}
	}
	return 0
}

func dbIndex(m *Migration, id string) int {
	for i, d := range m.Plan.Databases {
		if "db:"+d.Dest == id {
			return i
		}
	}
	return 0
}

func (e *Engine) runDB(ctx context.Context, m *Migration, t Puller, i int, update func(int, func(*Task)), log io.Writer) error {
	d := m.Plan.Databases[dbIndex(m, m.Tasks[i].ID)]
	ok, err := e.DB.Exists(ctx, d.Dest)
	if err != nil {
		return err
	}
	if !ok {
		return fatal{fmt.Errorf("the database %s does not exist here: create it in NimVor first", d.Dest)}
	}
	n, err := e.DB.Tables(ctx, d.Dest)
	if err != nil {
		return err
	}
	cp := m.Tasks[i].Checkpoint
	// Only an empty database is filled. A database this migration already began to
	// fill is continued (the dump drops and recreates its own tables).
	if n > 0 && cp != "importing" && !m.Plan.Config.AllowOverwrite {
		return fatal{fmt.Errorf("the database %s already has %d table(s). Importing could overwrite them; choose an empty database, or allow overwriting explicitly", d.Dest, n)}
	}
	dir, err := e.Env.Root.Path(e.tempDir(m))
	if err != nil {
		return fatal{err}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if free := freeKB(dir); free < reserveKB {
		return fatal{fmt.Errorf("the disk for temporary files has only %s free; NimVor keeps %s free, so nothing was dumped", kb(free), kb(reserveKB))}
	}
	final := filepath.Join(dir, "db-"+d.Dest+".sql.gz")
	if err := dumpVerified(final); err != nil {
		fmt.Fprintf(log, "Dumping %s from the source…\n", d.Source)
		if err := e.dump(ctx, t, d.Source, final); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(log, "The dump of %s is already here and complete.\n", d.Source)
	}
	sum, size, err := sha256File(final)
	if err != nil {
		return err
	}
	update(i, func(t *Task) { t.Checkpoint, t.SHA256, t.Bytes = "dumped", sum, size })

	srcTables, err := t.Exec(ctx, "sh -c "+Shq("mysql -N -e "+Shq("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = '"+d.Source+"'")), nil)
	if err != nil {
		return fmt.Errorf("the tables of %s could not be counted on the source: %v", d.Source, err)
	}
	update(i, func(t *Task) {
		t.Checkpoint = "importing"
		if cp == "" {
			t.StartedEmpty = n == 0
		}
	})
	f, err := os.Open(final)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fatal{fmt.Errorf("the dump is damaged")}
	}
	if err := e.DB.Import(ctx, d.Dest, gz, log); err != nil {
		return err
	}
	have, err := e.DB.Tables(ctx, d.Dest)
	if err != nil {
		return err
	}
	if want := int(num(strings.TrimSpace(srcTables))); want != have {
		return fmt.Errorf("the database %s has %d table(s) after the import but the source has %d", d.Dest, have, want)
	}
	update(i, func(t *Task) {
		t.Checkpoint = "imported"
		t.Tables = have
		t.Detail = fmt.Sprintf("%d tables imported, %d bytes of dump (sha256 %s…)", have, size, sum[:12])
	})
	_ = os.Remove(final)
	return nil
}

// dump streams the source database into a file, complete or not at all.
func (e *Engine) dump(ctx context.Context, t Puller, name, final string) error {
	part := final + ".partial"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cmd := "sh -c " + Shq("mysqldump --single-transaction --quick --routines --triggers --events --hex-blob --skip-lock-tables --default-character-set=utf8mb4 -- "+Shq(name)+" | gzip -c")
	_, err = t.Exec(ctx, cmd, f)
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(part)
		return fmt.Errorf("the database %s could not be dumped: %v", name, err)
	}
	if err := dumpVerified(part); err != nil {
		os.Remove(part)
		return fmt.Errorf("the dump of %s is incomplete: %v", name, err)
	}
	return os.Rename(part, final)
}

// dumpVerified checks that a dump is a whole gzip stream that ends the way
// mysqldump ends a finished dump.
func dumpVerified(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	r := bufio.NewReaderSize(gz, 1<<20)
	tail := ""
	buf := make([]byte, 1<<20)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			tail += string(buf[:n])
			if len(tail) > 4096 {
				tail = tail[len(tail)-4096:]
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if !strings.Contains(tail, "-- Dump completed") {
		return fmt.Errorf("the dump does not end with mysqldump's completion line")
	}
	return nil
}

func sha256File(p string) (string, int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

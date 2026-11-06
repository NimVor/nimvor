//go:build realhost

package migrate

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/backup"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

func realEnv(t *testing.T) *host.Env {
	t.Helper()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.BackupDir = t.TempDir()
	cfg.SitesRoot, _ = os.MkdirTemp("/tmp", "nimvor-mig-")
	os.Chmod(cfg.SitesRoot, 0o755)
	t.Cleanup(func() { os.RemoveAll(cfg.SitesRoot) })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsroot := filesystem.Host()
	return host.New(cfg, fsroot, platform.Detect(fsroot), execx.NewSystemRunner(60*time.Second), jm, log)
}

// The real transport against the local sshd: a password login through the
// askpass helper, a key login, the pinned host key, and an rsync pull into a
// NimVor website folder that belongs to its account.
func TestRealHostMigrateFilesOverSSH(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if c, err := net.DialTimeout("tcp", "127.0.0.1:22", 2*time.Second); err != nil {
		t.Skip("needs sshd on port 22")
	} else {
		c.Close()
	}
	for _, bin := range []string{"rsync", "ssh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("needs " + bin)
		}
	}
	const pw = "Mig-Src-Pa55word-1"
	exec.Command("useradd", "-m", "-s", "/bin/sh", "nimvormsrc").Run()
	t.Cleanup(func() { exec.Command("userdel", "-r", "nimvormsrc").Run() })
	exec.Command("sh", "-c", "echo 'nimvormsrc:"+pw+"' | chpasswd").Run()
	os.MkdirAll("/home/nimvormsrc/www/sub dir", 0o755)
	os.WriteFile("/home/nimvormsrc/www/index.php", []byte("<?php echo 'migrated';"), 0o644)
	os.WriteFile("/home/nimvormsrc/www/sub dir/a b.txt", []byte(strings.Repeat("data", 50000)), 0o644)
	os.Symlink("/etc/passwd", "/home/nimvormsrc/www/escape")
	os.WriteFile("/home/nimvormsrc/www/skip.log", []byte("log"), 0o644)
	exec.Command("chown", "-R", "nimvormsrc:nimvormsrc", "/home/nimvormsrc").Run()

	exec.Command("useradd", "-m", "-s", "/usr/sbin/nologin", "-u", "2894", "nimvormdst").Run()
	t.Cleanup(func() { exec.Command("userdel", "-r", "nimvormdst").Run() })

	env := realEnv(t)
	site := filepath.Join(env.Cfg.SitesRoot, "migrated.test", "public_html")
	os.MkdirAll(site, 0o755)
	exec.Command("chown", "nimvormdst:nimvormdst", site).Run()

	keys, err := backup.ScanHostKeys(context.Background(), env, backup.Remote{Type: "sftp", Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	hostKey := keys[0]
	src := Source{Host: "127.0.0.1", User: "nimvormsrc", Password: pw, HostKey: hostKey}

	// Connection test: facts of the source, over a password login.
	tr, err := NewSSH(env, src)
	if err != nil {
		t.Fatal(err)
	}
	info, err := Inspect(context.Background(), tr)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if info.Root || info.Hostname == "" || !info.Rsync {
		t.Errorf("info = %+v", info)
	}

	// A wrong password and a wrong host key are refused, and say nothing of the password.
	bad := src
	bad.Password = "wrong-password"
	bt, _ := NewSSH(env, bad)
	if _, err := bt.Exec(context.Background(), "id", nil); err == nil || strings.Contains(err.Error(), "wrong-password") {
		t.Errorf("wrong password: %v", err)
	}
	wrong := src
	wrong.HostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
	wt, _ := NewSSH(env, wrong)
	if _, err := wt.Exec(context.Background(), "id", nil); err == nil {
		t.Error("connected to a server whose host key does not match the pinned one")
	}

	// The migration.
	e := NewEngine(env)
	e.Sleep = func(time.Duration) {}
	plan := Plan{Files: []FileTask{{Domain: "migrated.test", SourceRoot: "/home/nimvormsrc/www", DestRoot: site, DestUser: "nimvormdst", SizeKB: 300}},
		Config: Config{Excludes: []string{"*.log"}, Compress: true, Workers: 1}}
	m, err := e.Create(plan, "linux", tr.Describe())
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := e.Run(context.Background(), m.ID, src, &log); err != nil {
		t.Fatalf("run: %v\n%s", err, log.String())
	}
	s, _ := e.Store.Load(m.ID)
	if s.Status != Completed || s.Tasks[0].Differences != 0 || s.Tasks[0].Files < 2 {
		t.Fatalf("state = %+v\n%s", s, log.String())
	}
	if b, _ := os.ReadFile(filepath.Join(site, "index.php")); string(b) != "<?php echo 'migrated';" {
		t.Errorf("index.php = %q", b)
	}
	if b, err := os.ReadFile(filepath.Join(site, "sub dir", "a b.txt")); err != nil || len(b) != 200000 {
		t.Errorf("a file with spaces: %d bytes, %v", len(b), err)
	}
	if _, err := os.Stat(filepath.Join(site, "skip.log")); err == nil {
		t.Error("an excluded file was copied")
	}
	if _, err := os.Lstat(filepath.Join(site, "escape")); err == nil {
		t.Error("a symlink pointing outside the site was copied")
	}
	// The files belong to the website's account, not to the source's user.
	out, _ := exec.Command("stat", "-c", "%U", filepath.Join(site, "index.php")).Output()
	if strings.TrimSpace(string(out)) != "nimvormdst" {
		t.Errorf("owner = %s", out)
	}
	// The source is untouched.
	if _, err := os.Stat("/home/nimvormsrc/www/index.php"); err != nil {
		t.Error("the source file is gone")
	}
	raw, _ := os.ReadFile(filepath.Join(e.Store.Dir, m.ID+".json"))
	if bytes.Contains(raw, []byte(pw)) || strings.Contains(log.String(), pw) {
		t.Error("the password was written to the state or the log")
	}
	// No secret folder is left behind.
	left, _ := filepath.Glob(filepath.Join(env.Cfg.StateDir, "tmp", "ssh-*"))
	if len(left) != 0 {
		t.Errorf("secret folders left: %v", left)
	}

	// An interrupted copy is continued: change the source and run the files again.
	os.WriteFile("/home/nimvormsrc/www/new.txt", []byte("new"), 0o644)
	exec.Command("chown", "nimvormsrc:nimvormsrc", "/home/nimvormsrc/www/new.txt").Run()
	m2 := *s
	m2.Status = Interrupted
	m2.Tasks[0].Status = Pending
	m2.Tasks[0].Bytes = 1 // a copy was started
	e.Store.Save(&m2)
	if err := e.Run(context.Background(), m.ID, src, &log); err != nil {
		t.Fatalf("continue: %v\n%s", err, log.String())
	}
	if b, _ := os.ReadFile(filepath.Join(site, "new.txt")); string(b) != "new" {
		t.Error("the changed source was not synchronised")
	}

	// Synchronise: the site's owner edited index.php here after the migration (newer than the source's copy),
	// and the source gained a file. The sync brings the new file and keeps the edit.
	time.Sleep(1100 * time.Millisecond)
	os.WriteFile(filepath.Join(site, "index.php"), []byte("<?php echo 'edited here, newer';"), 0o644)
	os.WriteFile("/home/nimvormsrc/www/later.txt", []byte("later"), 0o644)
	exec.Command("chown", "nimvormsrc:nimvormsrc", "/home/nimvormsrc/www/later.txt").Run()
	if err := e.Sync(context.Background(), m.ID, src, &log); err != nil {
		t.Fatalf("sync: %v\n%s", err, log.String())
	}
	if b, _ := os.ReadFile(filepath.Join(site, "later.txt")); string(b) != "later" {
		t.Error("the sync did not bring the new source file")
	}
	if b, _ := os.ReadFile(filepath.Join(site, "index.php")); !strings.Contains(string(b), "edited here, newer") {
		t.Errorf("the sync replaced a file that is newer here: %q", b)
	}
	if s2, _ := e.Store.Load(m.ID); s2.Status != Completed {
		t.Errorf("status after the sync = %s", s2.Status)
	}
	// Put the file back as the rest of this test expects it.
	os.WriteFile(filepath.Join(site, "index.php"), []byte("<?php echo 'migrated';"), 0o644)
	exec.Command("chown", "nimvormdst:nimvormdst", filepath.Join(site, "index.php")).Run()

	// Validation: the real ownership from rsync --chown, and the document root, are checked; there is
	// no web server serving this folder here, so the HTTP request is only a warning.
	vrep, err := e.Validate(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	vc := map[string]string{}
	for _, c := range vrep.Checks {
		vc[c.Name] = c.Status
	}
	// (After the sync the edited file differs from the source on purpose: that is a warning, not a failure.)
	if vc["Files match the source"] == Fail {
		t.Errorf("files match the source = %q", vc["Files match the source"])
	}
	for _, n := range []string{"Document root", "Created files present", "File ownership"} {
		if vc[n] != Pass {
			t.Errorf("validation %q = %q (%+v)", n, vc[n], vrep.Checks)
		}
	}
	if vc["HTTP answer"] != Warn && vc["HTTP answer"] != Pass {
		t.Errorf("http check = %q", vc["HTTP answer"])
	}

	// A source folder whose name has a space (rsync splits a remote path on spaces unless told not to).
	os.MkdirAll("/home/nimvormsrc/my site", 0o755)
	os.WriteFile("/home/nimvormsrc/my site/page.html", []byte("<p>space</p>"), 0o644)
	exec.Command("chown", "-R", "nimvormsrc:nimvormsrc", "/home/nimvormsrc/my site").Run()
	site2 := filepath.Join(env.Cfg.SitesRoot, "spaced.test", "public_html")
	os.MkdirAll(site2, 0o755)
	exec.Command("chown", "nimvormdst:nimvormdst", site2).Run()
	m3, err := e.Create(Plan{Files: []FileTask{{Domain: "spaced.test", SourceRoot: "/home/nimvormsrc/my site", DestRoot: site2, DestUser: "nimvormdst", SizeKB: 4}}}, "linux", tr.Describe())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), m3.ID, src, &log); err != nil {
		t.Fatalf("a source folder with a space: %v\n%s", err, log.String())
	}
	if b, _ := os.ReadFile(filepath.Join(site2, "page.html")); string(b) != "<p>space</p>" {
		t.Errorf("a source folder with a space was not copied: %q", b)
	}

	// Rollback: what the migration created goes, what the owner changed or added stays.
	os.WriteFile(filepath.Join(site, "index.php"), []byte("<?php echo 'edited by the owner';"), 0o644)
	os.WriteFile(filepath.Join(site, "mine.txt"), []byte("mine"), 0o644)
	rep, err := e.Rollback(context.Background(), m.ID, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FilesRemoved < 2 || len(rep.Kept) != 1 || !strings.Contains(rep.Kept[0], "index.php") {
		t.Errorf("rollback report = %+v", rep)
	}
	for _, gone := range []string{"new.txt", "sub dir"} {
		if _, err := os.Stat(filepath.Join(site, gone)); err == nil {
			t.Errorf("%s was not rolled back", gone)
		}
	}
	for _, kept := range []string{"index.php", "mine.txt"} {
		if _, err := os.Stat(filepath.Join(site, kept)); err != nil {
			t.Errorf("%s was removed by the rollback", kept)
		}
	}

	// A key login works too.
	dir := t.TempDir()
	exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, "k")).Run()
	pub, _ := os.ReadFile(filepath.Join(dir, "k.pub"))
	priv, _ := os.ReadFile(filepath.Join(dir, "k"))
	os.MkdirAll("/home/nimvormsrc/.ssh", 0o700)
	os.WriteFile("/home/nimvormsrc/.ssh/authorized_keys", pub, 0o600)
	exec.Command("chown", "-R", "nimvormsrc:nimvormsrc", "/home/nimvormsrc/.ssh").Run()
	ksrc := Source{Host: "127.0.0.1", User: "nimvormsrc", PrivateKey: string(priv), HostKey: hostKey}
	kt, _ := NewSSH(env, ksrc)
	if out, err := kt.Exec(context.Background(), "id -un", nil); err != nil || strings.TrimSpace(out) != "nimvormsrc" {
		t.Errorf("key login = %q, %v", out, err)
	}
}

func mysqlRun(t *testing.T, stdin, db string, args ...string) string {
	t.Helper()
	cmd := exec.Command("mysql", append([]string{"-uroot", "-N"}, args...)...)
	if db != "" {
		cmd.Args = append(cmd.Args, db)
	}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mysql: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// A MySQL/MariaDB database is dumped over SSH (as root, key login) and
// imported with the agent's own database client: schema, data in utf8mb4, a
// view and a stored procedure arrive, table counts are compared, and a
// database that already has tables is not touched.
func TestRealHostMigrateDatabaseOverSSH(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if c, err := net.DialTimeout("tcp", "127.0.0.1:22", 2*time.Second); err != nil {
		t.Skip("needs sshd on port 22")
	} else {
		c.Close()
	}
	if out, err := exec.Command("mysql", "-uroot", "-N", "-e", "select 1").CombinedOutput(); err != nil {
		t.Skipf("needs a running MariaDB/MySQL reachable as root: %s", out)
	}
	mysqlRun(t, "DROP DATABASE IF EXISTS migsrc; CREATE DATABASE migsrc CHARACTER SET utf8mb4; DROP DATABASE IF EXISTS migdst; CREATE DATABASE migdst CHARACTER SET utf8mb4; DROP DATABASE IF EXISTS migfull; CREATE DATABASE migfull;", "")
	t.Cleanup(func() {
		mysqlRun(t, "DROP DATABASE IF EXISTS migsrc; DROP DATABASE IF EXISTS migdst; DROP DATABASE IF EXISTS migfull;", "")
	})
	mysqlRun(t, "CREATE TABLE t (id INT PRIMARY KEY, s VARCHAR(50) CHARACTER SET utf8mb4); INSERT INTO t VALUES (1,'héllo 日本'),(2,'x'); CREATE VIEW v AS SELECT id FROM t; CREATE TABLE u (a INT);\nDELIMITER //\nCREATE PROCEDURE p() BEGIN SELECT 1; END //\nDELIMITER ;\n", "migsrc")
	mysqlRun(t, "CREATE TABLE keep (x INT); INSERT INTO keep VALUES (7);", "migfull")

	dir := t.TempDir()
	exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, "k")).Run()
	pub, _ := os.ReadFile(filepath.Join(dir, "k.pub"))
	priv, _ := os.ReadFile(filepath.Join(dir, "k"))
	old, _ := os.ReadFile("/root/.ssh/authorized_keys")
	os.MkdirAll("/root/.ssh", 0o700)
	os.WriteFile("/root/.ssh/authorized_keys", append(old, pub...), 0o600)
	t.Cleanup(func() { os.WriteFile("/root/.ssh/authorized_keys", old, 0o600) })

	env := realEnv(t)
	keys, err := backup.ScanHostKeys(context.Background(), env, backup.Remote{Type: "sftp", Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	src := Source{Host: "127.0.0.1", User: "root", PrivateKey: string(priv), HostKey: keys[0]}
	e := NewEngine(env)
	e.Sleep = func(time.Duration) {}

	m, err := e.Create(Plan{Databases: []DBTask{{Source: "migsrc", Dest: "migdst"}}}, "linux", "ssh://root@127.0.0.1:22")
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := e.Run(context.Background(), m.ID, src, &log); err != nil {
		t.Fatalf("run: %v\n%s", err, log.String())
	}
	s, _ := e.Store.Load(m.ID)
	if s.Status != Completed || s.Tasks[0].Checkpoint != "imported" || len(s.Tasks[0].SHA256) != 64 {
		t.Fatalf("state = %+v\n%s", s, log.String())
	}
	if got := mysqlRun(t, "SELECT s FROM t WHERE id = 1;", "migdst"); got != "héllo 日本" {
		t.Errorf("unicode data = %q", got)
	}
	if got := mysqlRun(t, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='migdst';", ""); got != "3" {
		t.Errorf("tables = %s", got)
	}
	if got := mysqlRun(t, "SELECT COUNT(*) FROM information_schema.views WHERE table_schema='migdst';", ""); got != "1" {
		t.Errorf("views = %s", got)
	}
	if got := mysqlRun(t, "SELECT COUNT(*) FROM information_schema.routines WHERE routine_schema='migdst' AND routine_name='p';", ""); got != "1" {
		t.Errorf("procedures = %s", got)
	}
	if got := mysqlRun(t, "SELECT default_character_set_name FROM information_schema.schemata WHERE schema_name='migdst';", ""); got != "utf8mb4" {
		t.Errorf("charset = %s", got)
	}
	// The source database is unchanged.
	if got := mysqlRun(t, "SELECT COUNT(*) FROM t;", "migsrc"); got != "2" {
		t.Errorf("source rows = %s", got)
	}
	// The temporary dump is gone.
	if left, _ := filepath.Glob(filepath.Join(env.Cfg.StateDir, "tmp", "migrate-*", "*")); len(left) != 0 {
		t.Errorf("dump files left: %v", left)
	}
	dv, err := e.Validate(context.Background(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	dc := map[string]string{}
	for _, c := range dv.Checks {
		dc[c.Name] = c.Status
	}
	if dc["Database reachable"] != Pass || dc["Table count"] != Pass {
		t.Errorf("database validation = %+v", dv.Checks)
	}
	// Rollback: written to after the import, the database is kept; untouched, it is emptied and keeps its character set.
	time.Sleep(7 * time.Second)
	mysqlRun(t, "INSERT INTO t VALUES (3,'written later');", "migdst")
	rep, err := e.Rollback(context.Background(), m.ID, true, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DatabasesEmptied) != 0 || len(rep.Kept) != 1 || !strings.Contains(rep.Kept[0], "newer data is never removed") {
		t.Errorf("rollback with newer data = %+v", rep)
	}
	if got := mysqlRun(t, "SELECT COUNT(*) FROM t;", "migdst"); got != "3" {
		t.Errorf("newer data was removed: %s rows", got)
	}
	mq, err := e.Create(Plan{Databases: []DBTask{{Source: "migsrc", Dest: "migdst2"}}}, "linux", "ssh://root@127.0.0.1:22")
	_ = mq
	if err == nil {
		mysqlRun(t, "DROP DATABASE IF EXISTS migdst2; CREATE DATABASE migdst2 CHARACTER SET utf8mb4;", "")
		t.Cleanup(func() { mysqlRun(t, "DROP DATABASE IF EXISTS migdst2;", "") })
		if err := e.Run(context.Background(), mq.ID, src, &bytes.Buffer{}); err != nil {
			t.Fatalf("second import: %v", err)
		}
		rep, err := e.Rollback(context.Background(), mq.ID, true, io.Discard)
		if err != nil || len(rep.DatabasesEmptied) != 1 {
			t.Fatalf("rollback = %+v, %v", rep, err)
		}
		if got := mysqlRun(t, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='migdst2';", ""); got != "0" {
			t.Errorf("tables left after the rollback: %s", got)
		}
		if got := mysqlRun(t, "SELECT default_character_set_name FROM information_schema.schemata WHERE schema_name='migdst2';", ""); got != "utf8mb4" {
			t.Errorf("charset after the rollback = %s", got)
		}
	}
	// A database that already has tables is refused and left as it was.
	m2, err := e.Create(Plan{Databases: []DBTask{{Source: "migsrc", Dest: "migfull"}}}, "linux", "ssh://root@127.0.0.1:22")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background(), m2.ID, src, &bytes.Buffer{}); err == nil {
		t.Error("imported into a database that has tables")
	}
	if got := mysqlRun(t, "SELECT x FROM keep;", "migfull"); got != "7" {
		t.Errorf("the existing data changed: %q", got)
	}
	s2, _ := e.Store.Load(m2.ID)
	if !strings.Contains(s2.Tasks[0].Error, "already has") {
		t.Errorf("error = %q", s2.Tasks[0].Error)
	}
}

// Messages pulled with the real rsync over the real sshd into a mailbox owned by the vmail account:
// Dovecot's caches stay behind, flags in file names survive, a mailbox with messages is refused.
func TestRealHostMigrateMailOverSSH(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if c, err := net.DialTimeout("tcp", "127.0.0.1:22", 2*time.Second); err != nil {
		t.Skip("needs sshd on port 22")
	} else {
		c.Close()
	}
	for _, bin := range []string{"rsync", "ssh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("needs " + bin)
		}
	}
	const pw = "Mig-Src-Pa55word-2"
	exec.Command("useradd", "-m", "-s", "/bin/sh", "nimvormail").Run()
	t.Cleanup(func() { exec.Command("userdel", "-r", "nimvormail").Run() })
	exec.Command("sh", "-c", "echo 'nimvormail:"+pw+"' | chpasswd").Run()
	box := "/home/nimvormail/mail/example.test/info"
	for _, d := range []string{"cur", "new", "tmp", ".Sent/cur"} {
		os.MkdirAll(filepath.Join(box, d), 0o755)
	}
	os.WriteFile(filepath.Join(box, "cur", "1700000000.M1.src:2,S"), []byte("Subject: one\n\nbody"), 0o600)
	os.WriteFile(filepath.Join(box, "new", "1700000001.M2.src"), []byte("Subject: two\n\nbody"), 0o600)
	os.WriteFile(filepath.Join(box, ".Sent", "cur", "1700000002.M3.src:2,S"), []byte("Subject: sent\n\nbody"), 0o600)
	os.WriteFile(filepath.Join(box, "dovecot.index.cache"), []byte("cache"), 0o600)
	os.WriteFile(filepath.Join(box, "maildirsize"), []byte("0S"), 0o600)
	exec.Command("chown", "-R", "nimvormail:nimvormail", "/home/nimvormail").Run()

	if _, err := user.Lookup("vmail"); err != nil {
		exec.Command("useradd", "-r", "-s", "/usr/sbin/nologin", "vmail").Run()
		t.Cleanup(func() { exec.Command("userdel", "vmail").Run() })
	}
	env := realEnv(t)
	vm, err := env.LookupUser("vmail")
	if err != nil {
		t.Skip("cannot make a vmail account here")
	}
	env.M.VmailDir, _ = os.MkdirTemp("/tmp", "nimvor-vmail-")
	t.Cleanup(func() { os.RemoveAll(env.M.VmailDir) })
	os.Chmod(env.M.VmailDir, 0o755)
	dest := filepath.Join(env.M.VmailDir, "example.test", "info", "Maildir")
	for _, d := range []string{"cur", "new", "tmp"} {
		os.MkdirAll(filepath.Join(dest, d), 0o700)
	}
	exec.Command("chown", "-R", strconv.Itoa(vm.UID)+":"+strconv.Itoa(vm.GID), env.M.VmailDir).Run()

	keys, err := backup.ScanHostKeys(context.Background(), env, backup.Remote{Type: "sftp", Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	src := Source{Host: "127.0.0.1", User: "nimvormail", Password: pw, HostKey: keys[0]}
	tr, err := NewSSH(env, src)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(env)
	e.Sleep = func(time.Duration) {}
	plan := Plan{Mail: []MailTask{{Address: "info@example.test", SourceMaildir: box, SizeKB: 20}}, Config: Config{Workers: 1}}
	m, err := e.Create(plan, "cpanel", tr.Describe())
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := e.Run(context.Background(), m.ID, src, &log); err != nil {
		t.Fatalf("run: %v\n%s", err, log.String())
	}
	s, _ := e.Store.Load(m.ID)
	if s.Status != Completed || s.Tasks[0].Differences != 0 || s.Tasks[0].Files != 3 {
		t.Fatalf("state = %+v\n%s", s, log.String())
	}
	for _, p := range []string{"cur/1700000000.M1.src:2,S", "new/1700000001.M2.src", ".Sent/cur/1700000002.M3.src:2,S"} {
		info, err := os.Stat(filepath.Join(dest, p))
		if err != nil {
			t.Errorf("%s not copied: %v", p, err)
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != vm.UID {
			t.Errorf("%s is not owned by vmail", p)
		}
	}
	for _, p := range []string{"dovecot.index.cache", "maildirsize"} {
		if _, err := os.Stat(filepath.Join(dest, p)); err == nil {
			t.Errorf("%s was copied", p)
		}
	}
	// The mailbox now has messages: a second migration into it is refused.
	m2, err := e.Create(plan, "cpanel", tr.Describe())
	if err == nil {
		if err := e.Run(context.Background(), m2.ID, src, io.Discard); err == nil {
			t.Error("a mailbox with messages was filled again")
		}
	}
}

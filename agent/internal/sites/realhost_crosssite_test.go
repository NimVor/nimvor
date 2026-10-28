//go:build realhost

// Real-host cross-site PHP isolation (security item 50). Run as root on a
// disposable machine with nginx, the PHP-FPM service and the PHP CLI
// installed:
//
//	go test -tags realhost ./internal/sites -run RealHostCrossSitePHPIsolation -v
//
// It creates two websites owned by two unprivileged accounts (nimvor-a and
// nimvor-b), puts private files, a configuration file, logs and a backup in
// website B, starts B's real PHP-FPM master (so a worker runs as nimvor-b and
// B's socket exists), and then runs an attacker PHP script *as the real
// nimvor-a account* that tries every cross-site operation against B. Every
// unauthorized operation must be denied by the operating system — the test
// deliberately removes PHP's own open_basedir so that what is proven is the
// kernel's user/permission isolation, not a PHP setting an attacker could
// turn off.
package sites

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// phpCLI finds the PHP command-line binary for a version.
func phpCLI(v string) string {
	compact := strings.ReplaceAll(v, ".", "")
	for _, c := range []string{"php" + v, "php", "php" + compact, "/opt/remi/php" + compact + "/root/usr/bin/php"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

func TestRealHostCrossSitePHPIsolation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	v := testhost.PHPVersion()
	cli := phpCLI(v)
	if cli == "" {
		t.Skipf("no PHP %s CLI", v)
	}
	fpmBin := "/usr/sbin/php-fpm" + v
	if _, err := os.Stat(fpmBin); err != nil {
		if alt := "/opt/remi/php" + strings.ReplaceAll(v, ".", "") + "/root/usr/sbin/php-fpm"; func() bool { _, e := os.Stat(alt); return e == nil }() {
			fpmBin = alt
		} else {
			t.Skipf("no php-fpm %s binary", v)
		}
	}

	root := filesystem.Host()
	cfg := config.Default()
	cfg.SitesRoot = "/home/nimvor-xsite"
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	r := &hybrid{real: execx.NewSystemRunner(30 * time.Second)}
	env := host.New(cfg, root, platform.Detect(root), r, jm, log)
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, cfg.StateDir+"/**")
	m := &Manager{Env: env}
	ctx := context.Background()

	a := Spec{Domain: "a.xsite.test", User: "nimvor-a", CreateUser: true, Kind: "php", PHPVersion: v, Enabled: true, PHP: PHPSettings{Harden: true}}
	// B runs a static pool (one worker) so a nimvor-b process exists in /proc
	// as soon as the master starts.
	b := Spec{Domain: "b.xsite.test", User: "nimvor-b", CreateUser: true, Kind: "php", PHPVersion: v, Enabled: true,
		PHP: PHPSettings{Harden: true, PM: "static", MaxChildren: 1}}
	t.Cleanup(func() {
		for _, s := range []Spec{a, b} {
			m.handleDelete(ctx, []byte(`{"domain":"`+s.Domain+`","user":"`+s.User+`","remove_files":true,"remove_user":true}`))
		}
		os.RemoveAll(cfg.SitesRoot)
	})
	for _, s := range []Spec{a, b} {
		if _, err := m.Apply(ctx, s); err != nil {
			t.Fatalf("apply %s: %v", s.Domain, err)
		}
	}

	aRoot := filepath.Join(cfg.SitesRoot, a.Domain)
	bRoot := filepath.Join(cfg.SitesRoot, b.Domain)
	// B's private material, owned by nimvor-b with realistic modes.
	bSecret := filepath.Join(bRoot, "public_html", "secret.txt")
	bConfig := filepath.Join(bRoot, "config.php")
	bLog := filepath.Join(bRoot, "logs", "private.log")
	bBackup := filepath.Join(bRoot, "backups", "backup.tar")
	write := func(p, content string, mode os.FileMode) {
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatalf("seed %s: %v", p, err)
		}
		if out, err := exec.Command("chown", "nimvor-b:nimvor-b", p).CombinedOutput(); err != nil {
			t.Fatalf("chown %s: %s", p, out)
		}
		os.Chmod(p, mode)
	}
	write(bSecret, "SECRET-OF-B-site", 0o640)
	write(bConfig, "<?php $db_password = 'SECRET-OF-B-db';", 0o600)
	write(bLog, "SECRET-OF-B-log", 0o640)
	write(bBackup, "SECRET-OF-B-backup", 0o600)

	// A's own file (the control: A may read its own data).
	aSecret := filepath.Join(aRoot, "public_html", "mine.txt")
	if err := os.WriteFile(aSecret, []byte("A-own-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	exec.Command("chown", "nimvor-a:nimvor-a", aSecret).Run()

	// Start B's PHP-FPM master directly (no systemd needed): it reads B's
	// pool and listens on B's socket; its worker runs as nimvor-b.
	bLayout := m.layout(b)
	bPool, err := env.Root.Path(bLayout.PoolFile)
	if err != nil {
		t.Fatalf("resolve B pool: %v", err)
	}
	// A world-traversable scratch directory: the attacker script and the
	// FPM configuration live here so that nimvor-a can read the script (a
	// t.TempDir() is mode 0700 and root-owned).
	work, err := os.MkdirTemp("/tmp", "nimvor-xsite-")
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(work, 0o755)
	t.Cleanup(func() { os.RemoveAll(work) })
	masterConf := filepath.Join(work, "b-master.conf")
	fpmLog := filepath.Join(work, "b-fpm.log")
	os.WriteFile(masterConf, []byte("[global]\nerror_log = "+fpmLog+"\ndaemonize = no\ninclude = "+bPool+"\n"), 0o644)
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { devnull.Close() })
	fpm := exec.Command(fpmBin, "-F", "-y", masterConf, "-p", work)
	// Give the master its own process group, and send output to /dev/null
	// through a real fd (so no copy goroutine keeps Wait blocked), then kill
	// the whole group in cleanup — the master alone leaves the worker behind.
	fpm.Stdout, fpm.Stderr, fpm.Stdin = devnull, devnull, devnull
	fpm.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := fpm.Start(); err != nil {
		t.Fatalf("start B fpm: %v", err)
	}
	t.Cleanup(func() {
		syscall.Kill(-fpm.Process.Pid, syscall.SIGKILL)
		fpm.Wait()
	})

	// Wait for B's socket and worker.
	var bWorker string
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(bLayout.Socket); err != nil {
			continue
		}
		// A worker owned by nimvor-b (the master is root).
		out, _ := exec.Command("pgrep", "-u", "nimvor-b", "-f", "php-fpm").Output()
		if f := strings.Fields(string(out)); len(f) > 0 {
			bWorker = f[0]
			break
		}
	}
	if _, err := os.Stat(bLayout.Socket); err != nil {
		lb, _ := os.ReadFile(fpmLog)
		t.Fatalf("B's socket never appeared: %v\n%s", err, lb)
	}
	if bWorker == "" {
		t.Log("no nimvor-b worker found; /proc worker checks will target the master instead")
		out, _ := exec.Command("pgrep", "-f", "php-fpm.*"+filepath.Base(masterConf)).Output()
		if f := strings.Fields(string(out)); len(f) > 0 {
			bWorker = f[0]
		}
	}

	// The attacker PHP, run as nimvor-a with no open_basedir, so only the OS
	// stands between it and B. It prints one "OP <name> <ALLOWED|DENIED>"
	// line per attempt.
	targets := map[string]string{
		"b_secret": bSecret, "b_config": bConfig, "b_log": bLog, "b_backup": bBackup,
		"b_docroot": filepath.Join(bRoot, "public_html"), "b_root": bRoot,
		"b_pool_conf": bPool, "b_socket": bLayout.Socket,
		"a_root": aRoot, "a_secret": aSecret,
		"shadow": "/etc/shadow", "panel_cfg": "/etc/nimvor/panel.php",
		"b_worker_environ": "/proc/" + bWorker + "/environ",
		"b_worker_cmdline": "/proc/" + bWorker + "/cmdline",
	}
	tj, _ := json.Marshal(targets)
	script := filepath.Join(work, "attack.php")
	os.WriteFile(script, []byte(crossSiteAttacker), 0o644)

	cmd := exec.CommandContext(ctx, "runuser", "-u", "nimvor-a", "--",
		cli, "-d", "open_basedir=", "-d", "disable_functions=", script, string(tj))
	outb, err := cmd.CombinedOutput()
	out := string(outb)
	t.Logf("attacker output (uid should be nimvor-a):\n%s", out)
	if err != nil && !strings.Contains(out, "OP ") {
		t.Fatalf("attacker did not run: %v\n%s", err, out)
	}

	results := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 3 && f[0] == "OP" {
			results[f[1]] = f[2]
		}
	}
	// Prove the script really ran as nimvor-a, not root.
	if !strings.Contains(out, "WHOAMI nimvor-a") {
		t.Fatalf("attacker did not run as nimvor-a:\n%s", out)
	}
	if strings.Contains(out, "SECRET-OF-B") {
		t.Errorf("attacker leaked B's data:\n%s", out)
	}

	// Every cross-site and host operation must be DENIED.
	mustDeny := []string{
		"read_b_secret", "read_b_config", "read_b_log", "read_b_backup",
		"list_b_docroot", "list_b_root",
		"write_b_create", "write_b_overwrite", "delete_b_secret", "rename_b_secret",
		"symlink_read_b", "read_b_pool_conf",
		"fpm_connect_b", "read_shadow", "read_panel_cfg",
	}
	if bWorker != "" {
		mustDeny = append(mustDeny, "read_b_worker_environ")
	}
	for _, op := range mustDeny {
		got, ok := results[op]
		if !ok {
			t.Errorf("%s: no result in attacker output", op)
			continue
		}
		if got != "DENIED" {
			t.Errorf("%s = %s, must be DENIED (cross-site access or host access succeeded)", op, got)
		}
	}
	// The control: A reads its own file.
	if results["read_a_own"] != "ALLOWED" {
		t.Errorf("read_a_own = %s, A must be able to read its own file (test harness broken)", results["read_a_own"])
	}
}

// crossSiteAttacker is the PHP run as nimvor-a. argv[1] is a JSON object of
// named paths. It reports DENIED unless an operation actually succeeded.
const crossSiteAttacker = `<?php
$t = json_decode($argv[1], true);
echo "WHOAMI " . trim(shell_exec('id -un')) . "\n";
function emit($op, $allowed) { echo "OP $op " . ($allowed ? "ALLOWED" : "DENIED") . "\n"; }
function readable($p) { $d = @file_get_contents($p); return $d !== false && strlen($d) > 0 ? $d : false; }

// Reads of B's private files.
emit('read_b_secret',  readable($t['b_secret'])  !== false);
emit('read_b_config',  readable($t['b_config'])  !== false);
emit('read_b_log',     readable($t['b_log'])     !== false);
emit('read_b_backup',  readable($t['b_backup'])  !== false);
emit('read_b_pool_conf', readable($t['b_pool_conf']) !== false);

// Directory listings of B.
$ls = @scandir($t['b_docroot']); emit('list_b_docroot', is_array($ls) && count(array_diff($ls, ['.','..'])) > 0);
$lr = @scandir($t['b_root']);    emit('list_b_root',    is_array($lr) && count(array_diff($lr, ['.','..'])) > 0);

// Writes into B.
$new = $t['b_docroot'] . '/evil-' . getmypid() . '.txt';
$created = @file_put_contents($new, 'x'); if ($created !== false) { @unlink($new); }
emit('write_b_create', $created !== false);
emit('write_b_overwrite', @file_put_contents($t['b_secret'], 'tampered') !== false);
emit('delete_b_secret', @unlink($t['b_secret']));
emit('rename_b_secret', @rename($t['b_secret'], $t['b_secret'] . '.moved'));

// A symlink in A's own tree pointing at B's secret: following it must still
// be stopped by the kernel.
$link = $t['a_root'] . '/public_html/link-' . getmypid();
@symlink($t['b_secret'], $link);
emit('symlink_read_b', readable($link) !== false);
@unlink($link);

// B's PHP-FPM socket.
$fp = @stream_socket_client('unix://' . $t['b_socket'], $e, $es, 2);
emit('fpm_connect_b', $fp !== false); if ($fp) { fclose($fp); }

// Host secrets.
emit('read_shadow', readable($t['shadow']) !== false);
emit('read_panel_cfg', readable($t['panel_cfg']) !== false);

// B's worker environment via /proc (the sensitive one).
if (!empty($t['b_worker_environ']) && strpos($t['b_worker_environ'], '/proc//') === false) {
    emit('read_b_worker_environ', readable($t['b_worker_environ']) !== false);
}

// Control: A reads its own file.
emit('read_a_own', readable($t['a_secret']) !== false);
`

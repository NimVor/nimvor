package sites

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests start a real PHP-FPM (php-fpm8.3 or any php-fpm on the PATH,
// as root, with user_ini.cache_ttl=1) with the pools NimVor renders and ask
// the running PHP for ini_get(): what a customer's .user.ini and php.ini
// really do. They are skipped where no PHP-FPM is installed.

func findFPM(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root to start PHP-FPM workers")
	}
	for _, n := range []string{"php-fpm8.4", "php-fpm8.3", "php-fpm8.2", "php-fpm8.1", "php-fpm"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	t.Skip("no PHP-FPM installed")
	return ""
}

// fcgiGet runs a script through a PHP-FPM socket and returns the body.
func fcgiGet(t *testing.T, sock, script string) string {
	t.Helper()
	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	rec := func(typ byte, body []byte) []byte {
		h := make([]byte, 8)
		h[0], h[1] = 1, typ
		binary.BigEndian.PutUint16(h[2:], 1)
		binary.BigEndian.PutUint16(h[4:], uint16(len(body)))
		return append(h, body...)
	}
	nv := func(k, v string) []byte {
		return append(append([]byte{byte(len(k)), byte(len(v))}, k...), v...)
	}
	var params []byte
	for _, kv := range [][2]string{{"SCRIPT_FILENAME", script}, {"REQUEST_METHOD", "GET"}, {"SERVER_PROTOCOL", "HTTP/1.1"}, {"DOCUMENT_ROOT", filepath.Dir(script)}} {
		params = append(params, nv(kv[0], kv[1])...)
	}
	req := rec(1, []byte{0, 1, 0, 0, 0, 0, 0, 0})
	req = append(req, rec(4, params)...)
	req = append(req, rec(4, nil)...)
	req = append(req, rec(5, nil)...)
	if _, err := c.Write(req); err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(c)
	var body bytes.Buffer
	for len(out) >= 8 {
		n := int(binary.BigEndian.Uint16(out[4:6]))
		pad := int(out[6])
		if len(out) < 8+n+pad {
			break
		}
		if out[1] == 6 {
			body.Write(out[8 : 8+n])
		}
		out = out[8+n+pad:]
	}
	s := body.String()
	if i := strings.Index(s, "\r\n\r\n"); i >= 0 {
		s = s[i+4:]
	}
	return s
}

type fpmLab struct {
	t    *testing.T
	dir  string
	bin  string
	pool map[string]string // domain -> pool file
}

func newFPMLab(t *testing.T) *fpmLab {
	bin := findFPM(t)
	dir, err := os.MkdirTemp("", "nimvor-fpm-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.Chmod(dir, 0o755)
	// A pre-built /tmp parent may be private; the workers run as "nobody".
	return &fpmLab{t: t, dir: dir, bin: bin, pool: map[string]string{}}
}

// site creates a website folder owned by the unprivileged account and
// writes its pool exactly as NimVor renders it (the head is the minimum
// the lab needs; the PHP values are NimVor's own PoolLines).
func (l *fpmLab) site(domain string, mod func(*Spec)) (Spec, string) {
	t := l.t
	root := filepath.Join(l.dir, domain)
	doc := filepath.Join(root, "public_html")
	for _, d := range []string{doc, filepath.Join(root, "tmp/sessions"), filepath.Join(root, "logs")} {
		os.MkdirAll(d, 0o755)
	}
	filepath.Walk(root, func(p string, _ os.FileInfo, _ error) error { os.Chown(p, 65534, 65534); return nil })
	s := Spec{Domain: domain, User: "nobody", Root: root, DocumentRoot: doc, Kind: "php", PHPVersion: "8.3", Enabled: true,
		PHP: PHPSettings{MemoryLimitMB: 128, UploadMaxMB: 2, MaxExecSeconds: 30, MaxChildren: 3, PM: "ondemand", MaxRequests: 100, IdleTimeoutSec: 10, SlowlogSec: 10}}
	if mod != nil {
		mod(&s)
	}
	_ = t
	return s, doc
}

func (l *fpmLab) layout(s Spec) Layout {
	return Layout{Socket: filepath.Join(l.dir, s.Domain+".sock"), LogDir: filepath.Join(s.Root, "logs"), TmpDir: filepath.Join(s.Root, "tmp"), WebUser: "nobody"}
}

// write renders the website's pool with the customer's current php.ini.
func (l *fpmLab) write(s Spec) {
	lay := l.layout(s)
	lay.Customer = BuildCustomerLayer(s, lockedKeys(PoolLines(s, lay)), 65534, func(p string) string { return p }).Values
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\nuser = nobody\ngroup = nogroup\nlisten = %s\nlisten.owner = nobody\nlisten.mode = 0666\npm = ondemand\npm.max_children = 3\n", strings.ReplaceAll(s.Domain, ".", "_"), lay.Socket)
	b.WriteString(renderINI(PoolLines(s, lay)))
	if l.pool[s.Domain] == "" {
		l.pool[s.Domain] = filepath.Join(l.dir, "pool.d", s.Domain+".conf")
		os.MkdirAll(filepath.Dir(l.pool[s.Domain]), 0o755)
	}
	os.WriteFile(l.pool[s.Domain], []byte(b.String()), 0o644)
}

func (l *fpmLab) start() *exec.Cmd {
	t := l.t
	conf := filepath.Join(l.dir, "fpm.conf")
	os.WriteFile(conf, []byte(fmt.Sprintf("[global]\npid = %s/fpm.pid\nerror_log = %s/fpm.log\ndaemonize = no\ninclude = %s/pool.d/*.conf\n", l.dir, l.dir, l.dir)), 0o644)
	// A parent of the lab that only root can enter would hide the files from "nobody".
	for p := l.dir; p != "/"; p = filepath.Dir(p) {
		if st, err := os.Stat(p); err == nil && st.Mode().Perm()&0o001 == 0 {
			os.Chmod(p, st.Mode().Perm()|0o001)
		}
	}
	cmd := exec.Command(l.bin, "-y", conf, "-d", "user_ini.cache_ttl=1", "--allow-to-run-as-root")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Stderr stays /dev/null: a pipe would be held open by the workers after the master is killed.
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); cmd.Process.Kill(); cmd.Wait() })
	for i := 0; i < 50; i++ {
		ok := true
		for d := range l.pool {
			if _, err := os.Stat(filepath.Join(l.dir, d+".sock")); err != nil {
				ok = false
			}
		}
		if ok {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return cmd
}

// reload is PHP-FPM's graceful reload (what the agent's systemctl reload does).
func (l *fpmLab) reload(cmd *exec.Cmd) {
	cmd.Process.Signal(syscall.SIGUSR2)
	time.Sleep(700 * time.Millisecond)
}

const probe = `<?php foreach (['memory_limit','upload_max_filesize','post_max_size','max_execution_time','open_basedir','display_errors','date.timezone','max_input_vars'] as $k) echo $k,'=',ini_get($k),"\n";`

// values runs the probe script of a site and parses its output.
func (l *fpmLab) values(s Spec, doc string) map[string]string {
	script := filepath.Join(doc, "probe.php")
	os.WriteFile(script, []byte(probe), 0o644)
	out := fcgiGet(l.t, l.layout(s).Socket, script)
	m := map[string]string{}
	for _, ln := range strings.Split(strings.TrimSpace(out), "\n") {
		if k, v, ok := strings.Cut(ln, "="); ok {
			m[k] = v
		}
	}
	return m
}

func waitTTL() { time.Sleep(2200 * time.Millisecond) }

func TestRealPHPFPMAppliesCustomerConfiguration(t *testing.T) {
	lab := newFPMLab(t)
	a, docA := lab.site("a.test", nil)
	b, docB := lab.site("b.test", func(s *Spec) { s.PHP.INI = map[string]string{"date.timezone": "Asia/Karachi"} })
	locked, docL := lab.site("locked.test", func(s *Spec) { s.PHP.LockINI = true })
	for _, s := range []Spec{a, b, locked} {
		lab.write(s)
	}
	cmd := lab.start()

	// 1. No custom file: NimVor's own values.
	v := lab.values(a, docA)
	if v["memory_limit"] != "128M" || v["upload_max_filesize"] != "2M" || v["post_max_size"] != "2M" || v["max_execution_time"] != "30" {
		t.Fatalf("defaults: %v", v)
	}
	if !strings.HasSuffix(v["open_basedir"], "a.test/:/usr/share/php/:/dev/urandom") {
		t.Fatalf("open_basedir: %v", v)
	}

	// 2. Only a .user.ini: PHP itself reads it (after user_ini.cache_ttl).
	os.WriteFile(filepath.Join(docA, ".user.ini"), []byte("memory_limit = 300M\nupload_max_filesize = 40M\npost_max_size = 50M\nmax_execution_time = 77\nopen_basedir = /\n"), 0o644)
	waitTTL()
	v = lab.values(a, docA)
	if v["memory_limit"] != "300M" || v["upload_max_filesize"] != "40M" || v["post_max_size"] != "50M" || v["max_execution_time"] != "77" {
		t.Fatalf(".user.ini: %v", v)
	}
	if !strings.HasSuffix(v["open_basedir"], "a.test/:/usr/share/php/:/dev/urandom") {
		t.Fatalf("a .user.ini changed open_basedir: %v", v)
	}

	// 3. Modified, then deleted.
	os.WriteFile(filepath.Join(docA, ".user.ini"), []byte("memory_limit = 200M\n"), 0o644)
	waitTTL()
	if v = lab.values(a, docA); v["memory_limit"] != "200M" || v["upload_max_filesize"] != "2M" {
		t.Fatalf("modified .user.ini: %v", v)
	}
	os.Remove(filepath.Join(docA, ".user.ini"))
	waitTTL()
	if v = lab.values(a, docA); v["memory_limit"] != "128M" {
		t.Fatalf("deleted .user.ini: %v", v)
	}

	// 4. Only a php.ini: NimVor puts it in the pool; one graceful reload.
	os.WriteFile(filepath.Join(docA, "php.ini"), []byte("memory_limit = 256M\nupload_max_filesize = 20M\npost_max_size = 25M\nmax_execution_time = 90\nopen_basedir = /\n"), 0o644)
	lab.write(a)
	lab.reload(cmd)
	if v = lab.values(a, docA); v["memory_limit"] != "256M" || v["upload_max_filesize"] != "20M" || v["post_max_size"] != "25M" || v["max_execution_time"] != "90" {
		t.Fatalf("php.ini: %v", v)
	}
	if !strings.HasSuffix(v["open_basedir"], "a.test/:/usr/share/php/:/dev/urandom") {
		t.Fatalf("a php.ini changed open_basedir: %v", v)
	}

	// 5. Both: the .user.ini is the nearest and wins; php.ini's other values stay.
	os.WriteFile(filepath.Join(docA, ".user.ini"), []byte("memory_limit = 512M\n"), 0o644)
	waitTTL()
	if v = lab.values(a, docA); v["memory_limit"] != "512M" || v["upload_max_filesize"] != "20M" {
		t.Fatalf("both files: %v", v)
	}

	// 6. Other websites are untouched; the panel's website value is below php.ini.
	if v = lab.values(b, docB); v["memory_limit"] != "128M" || v["upload_max_filesize"] != "2M" || v["date.timezone"] != "Asia/Karachi" {
		t.Fatalf("isolation: %v", v)
	}
	os.WriteFile(filepath.Join(docB, "php.ini"), []byte("date.timezone = Europe/Paris\nmax_input_vars = 3000\n"), 0o644)
	lab.write(b)
	lab.reload(cmd)
	if v = lab.values(b, docB); v["date.timezone"] != "Europe/Paris" || v["max_input_vars"] != "3000" {
		t.Fatalf("php.ini over the website value: %v", v)
	}
	if v = lab.values(a, docA); v["date.timezone"] == "Europe/Paris" || v["max_input_vars"] == "3000" {
		t.Fatalf("a website's php.ini leaked into another: %v", v)
	}

	// 7. With the plan's limits locked neither file changes them.
	os.WriteFile(filepath.Join(docL, "php.ini"), []byte("memory_limit = 900M\nmax_input_vars = 4000\n"), 0o644)
	os.WriteFile(filepath.Join(docL, ".user.ini"), []byte("memory_limit = 800M\nmax_input_vars = 4500\n"), 0o644)
	lab.write(locked)
	lab.reload(cmd)
	waitTTL()
	if v = lab.values(locked, docL); v["memory_limit"] != "128M" || v["max_input_vars"] != "4500" {
		t.Fatalf("locked limits: %v", v)
	}

	// 8. A PHP-FPM restart keeps everything: the values are in the pool file.
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	cmd.Wait()
	os.Remove(filepath.Join(lab.dir, "a.test.sock"))
	cmd2 := lab.start()
	_ = cmd2
	if v = lab.values(a, docA); v["memory_limit"] != "512M" || v["upload_max_filesize"] != "20M" {
		t.Fatalf("after a restart: %v", v)
	}
}

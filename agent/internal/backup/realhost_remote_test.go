//go:build realhost

package backup

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/shacrypt"
)

func remoteEnv(t *testing.T) (*host.Env, *actions.Registry, Service) {
	t.Helper()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.BackupDir = t.TempDir()
	cfg.SitesRoot, _ = os.MkdirTemp("/tmp", "nimvor-rsites-")
	os.Chmod(cfg.SitesRoot, 0o755)
	t.Cleanup(func() { os.RemoveAll(cfg.SitesRoot) })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsroot := filesystem.Host()
	env := host.New(cfg, fsroot, platform.Detect(fsroot), execx.NewSystemRunner(60*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	return env, r, Service{Env: env}
}

// backupCycle tests a destination, then backs up a website to it, removes
// the local copy, restores from the remote and deletes the remote copy.
func backupCycle(t *testing.T, r *actions.Registry, s Service, remote Remote, check func(map[string]any) Remote) {
	t.Helper()
	a, _ := r.Lookup("backup.remote_test")
	b, _ := json.Marshal(map[string]any{"remote": remote})
	res, err := a.Handler(context.Background(), b)
	if err != nil {
		var ae *actions.Error
		if e, ok := err.(*actions.Error); ok {
			ae = e
		}
		t.Fatalf("remote_test: %#v", ae)
	}
	remote = check(res.(map[string]any))
	// Once pinned, the same settings pass without discovering again.
	b, _ = json.Marshal(map[string]any{"remote": remote})
	if _, err := a.Handler(context.Background(), b); err != nil {
		t.Fatalf("remote_test with the pinned key: %+v", err)
	}

	if out, err := exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", "-u", "1779", "nimvorrmt").CombinedOutput(); err != nil && !strings.Contains(string(out), "already exists") {
		ids, _ := exec.Command("getent", "passwd", "1779").CombinedOutput()
		gids, _ := exec.Command("getent", "group", "1779", "nimvorrmt").CombinedOutput()
		t.Fatalf("useradd nimvorrmt: %v %s\nuid 1779: %s\ngroup: %s", err, out, ids, gids)
	}
	t.Cleanup(func() { exec.Command("userdel", "nimvorrmt").Run() })
	root := filepath.Join(s.Env.Cfg.SitesRoot, "rmt.test")
	os.MkdirAll(filepath.Join(root, "public_html"), 0o750)
	os.WriteFile(filepath.Join(root, "public_html/index.html"), []byte("remote backup"), 0o640)
	exec.Command("chown", "-R", "nimvorrmt:nimvorrmt", root).Run()
	cred, _, err := s.Env.Credential("nimvorrmt")
	if err != nil {
		t.Fatal(err)
	}
	keep := false
	var log strings.Builder
	m, err := s.CreateSite(context.Background(), SiteRequest{Domain: "rmt.test", User: "nimvorrmt", Root: root, Files: true, Encrypt: true, Remote: &remote, KeepLocal: &keep}, cred, &log)
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, log.String())
	}
	os.WriteFile(filepath.Join(root, "public_html/index.html"), []byte("changed"), 0o640)
	// As on a new server: this server forgets the backup, finds it again in
	// the storage (listing it over the real protocol) and restores it.
	local, _ := s.real(s.dir("rmt.test", m.ID))
	if err := os.RemoveAll(local); err != nil {
		t.Fatal(err)
	}
	found, err := s.ImportRemote(context.Background(), &remote)
	if err != nil {
		t.Fatalf("search the storage: %v", err)
	}
	imported := false
	for _, f := range found {
		imported = imported || (f.ID == m.ID && f.Skipped == "" && f.Manifest != nil)
	}
	if !imported {
		t.Fatalf("backup not found in the storage: %+v", found)
	}
	if _, err := s.Restore(context.Background(), RestoreRequest{Domain: "rmt.test", User: "nimvorrmt", Root: root, ID: m.ID, Files: true, Remote: &remote}, cred, &log); err != nil {
		t.Fatalf("restore: %v\n%s", err, log.String())
	}
	if b, _ := os.ReadFile(filepath.Join(root, "public_html/index.html")); string(b) != "remote backup" {
		t.Fatalf("restored content = %q", b)
	}
	if err := s.Delete(context.Background(), "rmt.test", m.ID, &remote); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if keys, _ := remote.Open(s.Env).List(context.Background(), m.Remote.Key+"/"); len(keys) != 0 {
		t.Errorf("remote files left: %v", keys)
	}
}

// SFTP with key authentication against the local sshd.
func TestRealHostSFTPBackup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if c, err := net.DialTimeout("tcp", "127.0.0.1:22", 2*time.Second); err != nil {
		t.Skip("needs sshd on port 22")
	} else {
		c.Close()
	}
	exec.Command("useradd", "-m", "-s", "/bin/sh", "nimvorsftp").Run()
	t.Cleanup(func() { exec.Command("userdel", "-r", "nimvorsftp").Run() })
	dir := t.TempDir()
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, "k")).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	pub, _ := os.ReadFile(filepath.Join(dir, "k.pub"))
	priv, _ := os.ReadFile(filepath.Join(dir, "k"))
	os.MkdirAll("/home/nimvorsftp/.ssh", 0o700)
	os.WriteFile("/home/nimvorsftp/.ssh/authorized_keys", pub, 0o600)
	exec.Command("chown", "-R", "nimvorsftp:nimvorsftp", "/home/nimvorsftp").Run()

	_, r, s := remoteEnv(t)
	remote := Remote{Type: "sftp", Host: "127.0.0.1", Username: "nimvorsftp", PrivateKey: string(priv), Prefix: "nimvor"}
	backupCycle(t, r, s, remote, func(res map[string]any) Remote {
		if !strings.HasPrefix(res["fingerprint"].(string), "SHA256:") {
			t.Fatalf("result = %v", res)
		}
		remote.HostKey = res["host_key"].(string)
		return remote
	})
	// A wrong pinned key is refused.
	other := remote
	other.HostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
	if _, err := other.Open(s.Env).List(context.Background(), ""); err == nil {
		t.Error("connected with a wrong host key")
	}
}

// FTPS (explicit TLS) against a pure-ftpd started for the test with a
// self-signed certificate, which is pinned.
func TestRealHostFTPSBackup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("pure-ftpd"); err != nil {
		t.Skip("needs pure-ftpd")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ftp.nimvor.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	kder, _ := x509.MarshalECPrivateKey(key)
	pemData := append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	os.WriteFile(filepath.Join(dir, "ftp.pem"), pemData, 0o600)

	exec.Command("useradd", "-m", "-u", "2892", "-s", "/usr/sbin/nologin", "nimvorftp").Run()
	t.Cleanup(func() { exec.Command("userdel", "-r", "nimvorftp").Run() })
	line := fmt.Sprintf("nimvorftp:%s:2892:2892::/home/nimvorftp/./::::::::::::\n", shacrypt.HashPassword("Ftp-Pa55word-123"))
	os.WriteFile(filepath.Join(dir, "users.passwd"), []byte(line), 0o600)
	if out, err := exec.Command("pure-pw", "mkdb", filepath.Join(dir, "users.pdb"), "-f", filepath.Join(dir, "users.passwd")).CombinedOutput(); err != nil {
		t.Fatalf("pure-pw: %s", out)
	}
	conf := fmt.Sprintf(`ChrootEveryone yes
Daemonize no
NoAnonymous yes
DontResolve yes
PureDB %s/users.pdb
Bind 127.0.0.1,2121
PassivePortRange 40100 40150
MinUID 1000
TLS 2
CertFile %s/ftp.pem
`, dir, dir)
	os.WriteFile(filepath.Join(dir, "pure-ftpd.conf"), []byte(conf), 0o600)
	srv := exec.Command("pure-ftpd", filepath.Join(dir, "pure-ftpd.conf"))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Process.Kill(); srv.Wait() })
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:2121"); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	_, r, s := remoteEnv(t)
	remote := Remote{Type: "ftp", Host: "127.0.0.1", Port: 2121, Username: "nimvorftp", Password: "Ftp-Pa55word-123", Prefix: "nimvor"}
	backupCycle(t, r, s, remote, func(res map[string]any) Remote {
		if !strings.HasPrefix(res["tls_pin"].(string), "sha256//") || !strings.Contains(fmt.Sprint(res["tls_subject"]), "ftp.nimvor.test") {
			t.Fatalf("result = %v", res)
		}
		remote.TLSPin = res["tls_pin"].(string)
		return remote
	})
	// A different pin is refused, and so is plain FTP without the pin.
	other := remote
	other.TLSPin = "sha256//" + strings.Repeat("A", 43) + "="
	if _, err := other.Open(s.Env).List(context.Background(), ""); err == nil {
		t.Error("connected with a wrong certificate pin")
	}
	other.TLSPin = ""
	if _, err := other.Open(s.Env).List(context.Background(), ""); err == nil {
		t.Error("connected to a self-signed server without a pin")
	}
}

// fetchViaActions runs the three fetch actions the way the panel does: the
// server's key is asked for first, then the file is fetched in a job with
// that key pinned, and finally deleted.
func fetchViaActions(t *testing.T, env *host.Env, r *actions.Registry, remote Remote, probe map[string]any, remotePath, want string) {
	t.Helper()
	ctx := context.Background()
	call := func(name string, params any) any {
		t.Helper()
		a, err := r.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(params)
		res, err := a.Handler(ctx, b)
		if err != nil {
			t.Fatalf("%s: %+v", name, err)
		}
		return res
	}
	pr := call("backup.fetch_probe", probe).(map[string]any)
	if remote.Type == "sftp" {
		if !strings.HasPrefix(pr["fingerprint"].(string), "SHA256:") {
			t.Fatalf("probe = %v", pr)
		}
		remote.HostKey = pr["host_key"].(string)
	} else {
		if !strings.HasPrefix(pr["tls_pin"].(string), "sha256//") {
			t.Fatalf("probe = %v", pr)
		}
		remote.TLSPin = pr["tls_pin"].(string)
	}
	started := call("backup.fetch_remote", map[string]any{"remote": remote, "path": remotePath}).(map[string]any)
	job, err := env.Jobs.Wait(ctx, started["job_id"].(string))
	if err != nil || job.Status != "succeeded" {
		log, _, _ := env.Jobs.Log(started["job_id"].(string), 0)
		t.Fatalf("job = %+v, err = %v\n%s", job, err, log)
	}
	res := job.Result.(map[string]any)
	name := res["name"].(string)
	got, err := os.ReadFile(filepath.Join(env.Cfg.BackupDir, "fetched", name))
	if err != nil || string(got) != want {
		t.Fatalf("fetched file = %q, %v", got, err)
	}
	if res["size"] != int64(len(want)) {
		t.Errorf("size = %v", res["size"])
	}
	// A wrong pin is refused and leaves nothing behind.
	bad := remote
	if remote.Type == "sftp" {
		bad.HostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
	} else {
		bad.TLSPin = "sha256//" + strings.Repeat("A", 43) + "="
	}
	s := Service{Env: env}
	before, _ := os.ReadDir(filepath.Join(env.Cfg.BackupDir, "fetched"))
	if _, err := s.FetchRemote(ctx, FetchRequest{Remote: bad, Path: remotePath}, io.Discard); err == nil {
		t.Error("fetched from a server whose key does not match")
	}
	if after, _ := os.ReadDir(filepath.Join(env.Cfg.BackupDir, "fetched")); len(after) != len(before) {
		t.Errorf("files left after a refused fetch: %v", after)
	}
	del := call("backup.fetched_delete", map[string]any{"names": []string{name}}).(map[string]any)
	if rs := del["results"].([]map[string]any); len(rs) != 1 || rs[0]["deleted"] != true {
		t.Fatalf("delete = %v", del)
	}
	if _, err := os.Stat(filepath.Join(env.Cfg.BackupDir, "fetched", name)); err == nil {
		t.Error("file left after delete")
	}
}

// Fetching a file over SFTP with key authentication: by absolute path and
// by a path relative to the login folder.
func TestRealHostFetchSFTP(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if c, err := net.DialTimeout("tcp", "127.0.0.1:22", 2*time.Second); err != nil {
		t.Skip("needs sshd on port 22")
	} else {
		c.Close()
	}
	exec.Command("useradd", "-m", "-s", "/bin/sh", "nimvorfetch").Run()
	t.Cleanup(func() { exec.Command("userdel", "-r", "nimvorfetch").Run() })
	dir := t.TempDir()
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, "k")).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	pub, _ := os.ReadFile(filepath.Join(dir, "k.pub"))
	priv, _ := os.ReadFile(filepath.Join(dir, "k"))
	os.MkdirAll("/home/nimvorfetch/.ssh", 0o700)
	os.WriteFile("/home/nimvorfetch/.ssh/authorized_keys", pub, 0o600)
	os.MkdirAll("/home/nimvorfetch/out dir", 0o755)
	body := strings.Repeat("fetched-data-", 5000)
	os.WriteFile("/home/nimvorfetch/out dir/my backup.tar.gz", []byte(body), 0o644)
	exec.Command("chown", "-R", "nimvorfetch:nimvorfetch", "/home/nimvorfetch").Run()

	env, r, _ := remoteEnv(t)
	remote := Remote{Type: "sftp", Host: "127.0.0.1", Username: "nimvorfetch", PrivateKey: string(priv)}
	probe := map[string]any{"type": "sftp", "host": "127.0.0.1"}
	fetchViaActions(t, env, r, remote, probe, "/home/nimvorfetch/out dir/my backup.tar.gz", body)
	fetchViaActions(t, env, r, remote, probe, "out dir/my backup.tar.gz", body)
	// A file that does not exist, or a folder, is an error and leaves nothing.
	s := Service{Env: env}
	remote.HostKey = func() string { k, _ := ScanHostKeys(context.Background(), env, remote); return k[0] }()
	for _, p := range []string{"/home/nimvorfetch/missing.tar", "/home/nimvorfetch/out dir"} {
		if _, err := s.FetchRemote(context.Background(), FetchRequest{Remote: remote, Path: p}, io.Discard); err == nil {
			t.Errorf("%q fetched", p)
		}
	}
	if left, _ := os.ReadDir(filepath.Join(env.Cfg.BackupDir, "fetched")); len(left) != 0 {
		t.Errorf("left after failures: %v", left)
	}
}

// Fetching over FTPS (explicit TLS, self-signed certificate that is pinned).
func TestRealHostFetchFTPS(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("pure-ftpd"); err != nil {
		t.Skip("needs pure-ftpd")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ftp.nimvor.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	kder, _ := x509.MarshalECPrivateKey(key)
	pemData := append(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	os.WriteFile(filepath.Join(dir, "ftp.pem"), pemData, 0o600)

	exec.Command("useradd", "-m", "-u", "2893", "-s", "/usr/sbin/nologin", "nimvorftpf").Run()
	t.Cleanup(func() { exec.Command("userdel", "-r", "nimvorftpf").Run() })
	line := fmt.Sprintf("nimvorftpf:%s:2893:2893::/home/nimvorftpf/./::::::::::::\n", shacrypt.HashPassword("Ftp-Pa55word-123"))
	os.WriteFile(filepath.Join(dir, "users.passwd"), []byte(line), 0o600)
	if out, err := exec.Command("pure-pw", "mkdb", filepath.Join(dir, "users.pdb"), "-f", filepath.Join(dir, "users.passwd")).CombinedOutput(); err != nil {
		t.Fatalf("pure-pw: %s", out)
	}
	conf := fmt.Sprintf(`ChrootEveryone yes
Daemonize no
NoAnonymous yes
DontResolve yes
PureDB %s/users.pdb
Bind 127.0.0.1,2122
PassivePortRange 40160 40200
MinUID 1000
TLS 2
CertFile %s/ftp.pem
`, dir, dir)
	os.WriteFile(filepath.Join(dir, "pure-ftpd.conf"), []byte(conf), 0o600)
	srv := exec.Command("pure-ftpd", filepath.Join(dir, "pure-ftpd.conf"))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Process.Kill(); srv.Wait() })
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:2122"); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	body := strings.Repeat("ftps-data-", 4000)
	os.MkdirAll("/home/nimvorftpf/in", 0o755)
	os.WriteFile("/home/nimvorftpf/in/site.tar", []byte(body), 0o644)
	exec.Command("chown", "-R", "nimvorftpf:nimvorftpf", "/home/nimvorftpf").Run()

	env, r, _ := remoteEnv(t)
	remote := Remote{Type: "ftp", Host: "127.0.0.1", Port: 2122, Username: "nimvorftpf", Password: "Ftp-Pa55word-123"}
	probe := map[string]any{"type": "ftp", "host": "127.0.0.1", "port": 2122, "tls": "explicit"}
	fetchViaActions(t, env, r, remote, probe, "in/site.tar", body)
	fetchViaActions(t, env, r, remote, probe, "/in/site.tar", body)
}

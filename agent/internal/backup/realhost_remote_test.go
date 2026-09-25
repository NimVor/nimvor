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

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

func remoteEnv(t *testing.T) (*host.Env, *actions.Registry, Service) {
	t.Helper()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.BackupDir = t.TempDir()
	cfg.SitesRoot, _ = os.MkdirTemp("/tmp", "whacp-rsites-")
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

	exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", "-u", "1779", "whacprmt").Run()
	t.Cleanup(func() { exec.Command("userdel", "whacprmt").Run() })
	root := filepath.Join(s.Env.Cfg.SitesRoot, "rmt.test")
	os.MkdirAll(filepath.Join(root, "public_html"), 0o750)
	os.WriteFile(filepath.Join(root, "public_html/index.html"), []byte("remote backup"), 0o640)
	exec.Command("chown", "-R", "whacprmt:whacprmt", root).Run()
	cred, _, err := s.Env.Credential("whacprmt")
	if err != nil {
		t.Fatal(err)
	}
	keep := false
	var log strings.Builder
	m, err := s.CreateSite(context.Background(), SiteRequest{Domain: "rmt.test", User: "whacprmt", Root: root, Files: true, Encrypt: true, Remote: &remote, KeepLocal: &keep}, cred, &log)
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, log.String())
	}
	os.WriteFile(filepath.Join(root, "public_html/index.html"), []byte("changed"), 0o640)
	if _, err := s.Restore(context.Background(), RestoreRequest{Domain: "rmt.test", User: "whacprmt", Root: root, ID: m.ID, Files: true, Remote: &remote}, cred, &log); err != nil {
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
	exec.Command("useradd", "-m", "-s", "/bin/sh", "whacpsftp").Run()
	t.Cleanup(func() { exec.Command("userdel", "-r", "whacpsftp").Run() })
	dir := t.TempDir()
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, "k")).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	pub, _ := os.ReadFile(filepath.Join(dir, "k.pub"))
	priv, _ := os.ReadFile(filepath.Join(dir, "k"))
	os.MkdirAll("/home/whacpsftp/.ssh", 0o700)
	os.WriteFile("/home/whacpsftp/.ssh/authorized_keys", pub, 0o600)
	exec.Command("chown", "-R", "whacpsftp:whacpsftp", "/home/whacpsftp").Run()

	_, r, s := remoteEnv(t)
	remote := Remote{Type: "sftp", Host: "127.0.0.1", Username: "whacpsftp", PrivateKey: string(priv), Prefix: "whacp"}
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

// FTPS (explicit TLS) against a vsftpd started for the test with a
// self-signed certificate, which is pinned.
func TestRealHostFTPSBackup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("vsftpd"); err != nil {
		t.Skip("needs vsftpd")
	}
	dir := t.TempDir()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ftp.whacp.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	kder, _ := x509.MarshalECPrivateKey(key)
	os.WriteFile(filepath.Join(dir, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	os.WriteFile(filepath.Join(dir, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600)
	os.MkdirAll("/var/run/vsftpd/empty", 0o755)
	conf := fmt.Sprintf(`listen=YES
listen_port=2121
listen_address=127.0.0.1
background=NO
anonymous_enable=NO
local_enable=YES
write_enable=YES
local_umask=077
pasv_enable=YES
pasv_min_port=40100
pasv_max_port=40150
ssl_enable=YES
force_local_logins_ssl=YES
force_local_data_ssl=YES
require_ssl_reuse=NO
rsa_cert_file=%s/cert.pem
rsa_private_key_file=%s/key.pem
secure_chroot_dir=/var/run/vsftpd/empty
pam_service_name=vsftpd
seccomp_sandbox=NO
`, dir, dir)
	os.WriteFile(filepath.Join(dir, "vsftpd.conf"), []byte(conf), 0o600)
	srv := exec.Command("vsftpd", filepath.Join(dir, "vsftpd.conf"))
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
	exec.Command("useradd", "-m", "-s", "/bin/sh", "whacpftp").Run()
	t.Cleanup(func() { exec.Command("userdel", "-r", "whacpftp").Run() })
	cmd := exec.Command("chpasswd")
	cmd.Stdin = strings.NewReader("whacpftp:Ftp-Pa55word-123\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}

	_, r, s := remoteEnv(t)
	remote := Remote{Type: "ftp", Host: "127.0.0.1", Port: 2121, Username: "whacpftp", Password: "Ftp-Pa55word-123", Prefix: "whacp"}
	backupCycle(t, r, s, remote, func(res map[string]any) Remote {
		if !strings.HasPrefix(res["tls_pin"].(string), "sha256//") || !strings.Contains(fmt.Sprint(res["tls_subject"]), "ftp.whacp.test") {
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

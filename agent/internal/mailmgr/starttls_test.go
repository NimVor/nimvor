package mailmgr

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	cryptotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// smtpServer answers like Postfix: STARTTLS either works or is refused
// with 454 (as Postfix does when it cannot load TLS).
func smtpServer(t *testing.T, tlsWorks bool) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mail.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	cfg := &cryptotls.Config{Certificates: []cryptotls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				c.Write([]byte("220 mail.test ESMTP\r\n"))
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						c.Write([]byte("250-mail.test\r\n250 STARTTLS\r\n"))
					case strings.HasPrefix(line, "STARTTLS") && !tlsWorks:
						c.Write([]byte("454 4.7.0 TLS not available due to local problem\r\n"))
					case strings.HasPrefix(line, "STARTTLS"):
						c.Write([]byte("220 2.0.0 Ready to start TLS\r\n"))
						cryptotls.Server(c, cfg).Handshake()
						return
					}
				}
			}(c)
		}
	}()
	return l.Addr().String()
}

func TestStartTLSCheck(t *testing.T) {
	if err := startTLS(context.Background(), smtpServer(t, true)); err != nil {
		t.Fatalf("working STARTTLS: %v", err)
	}
	err := startTLS(context.Background(), smtpServer(t, false))
	if err == nil || !strings.Contains(err.Error(), "454") {
		t.Fatalf("refused STARTTLS not reported: %v", err)
	}
}

// Postfix before 3.6 rejects ">=TLSv1.2" and then disables TLS entirely.
func TestPostfixProtocolsWorkOnPostfix35(t *testing.T) {
	for _, kv := range postfixSettings("/etc/whacp-mail", "mail.test", tls{}, false, true) {
		if strings.Contains(kv[1], ">=") {
			t.Errorf("%s = %s needs Postfix 3.6", kv[0], kv[1])
		}
	}
}

// RHEL's Roundcube hard-codes its configuration folder; WhaCP then defines
// it first (auto_prepend_file) instead of editing the package's files.
func TestRoundcubeConfigDirOnHardcodedPackages(t *testing.T) {
	h, _ := setup(t)
	e := env{h.Env}
	iniset := filepath.Join(h.Base, e.M.RoundcubeDir, "program/include/iniset.php")
	os.MkdirAll(filepath.Dir(iniset), 0o755)

	os.WriteFile(iniset, []byte("<?php\ndefine('RCUBE_CONFIG_DIR', getenv('ROUNDCUBE_CONFIG_DIR') ?: INSTALL_PATH . 'config/');\n"), 0o644)
	if e.needsConfigDirFile() || strings.Contains(e.renderWebmailPool("8.3"), "auto_prepend_file") {
		t.Error("a package that reads ROUNDCUBE_CONFIG_DIR got a prepend file")
	}

	os.WriteFile(iniset, []byte("<?php\nif (!defined('RCMAIL_CONFIG_DIR')) {\n    define('RCMAIL_CONFIG_DIR', '/etc/roundcubemail');\n}\ndefine('RCUBE_CONFIG_DIR', '/etc/roundcubemail/');\n"), 0o644)
	if !e.needsConfigDirFile() {
		t.Fatal("hard-coded configuration folder not detected")
	}
	if !strings.Contains(e.renderWebmailPool("8.3"), "php_admin_value[auto_prepend_file] = "+e.configDirFile()+"\n") {
		t.Error("pool does not prepend the configuration folder")
	}
	f := e.renderConfigDirFile()
	if !strings.Contains(f, "define('RCUBE_CONFIG_DIR', '"+e.M.WebmailDir+"/')") || !strings.Contains(f, "define('RCMAIL_CONFIG_DIR', '"+e.M.WebmailDir+"')") {
		t.Errorf("prepend file:\n%s", f)
	}
}

// RHEL's defaults.inc.php is root:apache 0640: WhaCP copies it rather than
// including a file the webmail account cannot read.
func TestRoundcubeDefaultsReadableByWebmail(t *testing.T) {
	h, _ := setup(t)
	e := env{h.Env}
	src := filepath.Join(h.Base, e.roundcubeDefaults())
	os.MkdirAll(filepath.Dir(src), 0o755)
	os.WriteFile(src, []byte("<?php\n$config['product_name'] = 'Roundcube';\n"), 0o644)
	data, mode, err := e.renderDefaults()
	if err != nil || mode != 0o644 || !strings.Contains(string(data), "include '"+e.roundcubeDefaults()+"';") {
		t.Errorf("world-readable defaults: %v %o\n%s", err, mode, data)
	}
	os.Chmod(src, 0o640)
	data, mode, err = e.renderDefaults()
	if err != nil || mode != 0o640 || strings.Contains(string(data), "include") || !strings.Contains(string(data), "$config['product_name'] = 'Roundcube';") || !strings.HasPrefix(string(data), "<?php\n") {
		t.Errorf("restricted defaults: %v %o\n%s", err, mode, data)
	}
}

// Roundcube 1.5 (RHEL 9) reads default_host and smtp_server; 1.6 reads
// imap_host and smtp_host.
func TestRoundcubeServerSettingsPerVersion(t *testing.T) {
	h, _ := setup(t)
	e := env{h.Env}
	iniset := filepath.Join(h.Base, e.M.RoundcubeDir, "program/include/iniset.php")
	os.MkdirAll(filepath.Dir(iniset), 0o755)
	for _, c := range []struct {
		version string
		legacy  bool
	}{{"1.5.15", true}, {"1.6.5", false}, {"1.10.0", false}} {
		os.WriteFile(iniset, []byte("<?php\ndefine('RCMAIL_VERSION', '"+c.version+"');\n"), 0o644)
		if got := e.roundcubeLegacy(); got != c.legacy {
			t.Errorf("%s: legacy = %v", c.version, got)
		}
		cfg := renderRoundcube("/var/lib/whacp-webmail", "k", e.roundcubeLegacy())
		if !strings.Contains(cfg, "$config['imap_host'] = 'ssl://127.0.0.1:993';") {
			t.Errorf("%s: imap_host missing", c.version)
		}
		if has := strings.Contains(cfg, "$config['default_host'] = 'ssl://127.0.0.1';") && strings.Contains(cfg, "$config['smtp_port'] = 587;"); has != c.legacy {
			t.Errorf("%s: legacy settings present = %v", c.version, has)
		}
	}
}

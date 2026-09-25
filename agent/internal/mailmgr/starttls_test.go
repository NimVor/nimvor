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

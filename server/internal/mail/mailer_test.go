package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// transcript is what the fake server saw.
type transcript struct {
	ehlo        string
	authMode    string // "plain" or "login"
	authUser    string
	authPass    string
	mailFrom    string
	rcpts       []string
	data        string // DATA content, excluding the terminating dot
	tlsUpgraded bool
}

// fakeSMTP is one in-process server session.
type fakeSMTP struct {
	port int
	tr   chan transcript
}

// runFakeSMTP accepts exactly one connection and plays a minimal server:
// 220, EHLO (caps), optional STARTTLS, optional AUTH, MAIL/RCPT/DATA, QUIT.
func runFakeSMTP(t *testing.T, caps string, cert *tls.Certificate) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	tr := make(chan transcript, 1)
	f := &fakeSMTP{port: ln.Addr().(*net.TCPAddr).Port, tr: tr}
	go func() {
		defer close(tr)
		conn, err := ln.Accept()
		if err != nil {
			tr <- transcript{}
			return
		}
		defer conn.Close()
		in := bufio.NewReader(conn)
		out := transcript{}
		write := func(s string) { _, _ = conn.Write([]byte(s)) }
		read := func() string {
			line, err := in.ReadString('\n')
			if err != nil {
				return ""
			}
			return strings.TrimRight(line, "\r\n")
		}
		write("220 fake ESMTP ready\r\n")
		for {
			line := read()
			if line == "" {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				out.ehlo = line
				capsLine := "HELP"
				if caps != "" {
					capsLine = caps
				}
				write("250-fake\r\n250 " + capsLine + "\r\n")
			case line == "STARTTLS":
				write("220 Go ahead\r\n")
				if cert != nil {
					tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*cert}})
					if err := tc.Handshake(); err != nil {
						tr <- out
						return
					}
					out.tlsUpgraded = true
					conn = tc
					in = bufio.NewReader(tc)
					write("250-fake\r\n250 " + capsLine + "\r\n")
				}
			case strings.HasPrefix(line, "AUTH PLAIN "):
				raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
				if parts := strings.SplitN(string(raw), "\x00", 3); len(parts) == 3 {
					out.authMode = "plain"
					out.authUser = parts[1]
					out.authPass = parts[2]
				}
				write("235 2.7.0 Authentication successful\r\n")
			case line == "AUTH LOGIN":
				out.authMode = "login"
				write("334 VXNlcm5hbWU6\r\n")
				if u, err := base64.StdEncoding.DecodeString(read()); err == nil {
					out.authUser = string(u)
				}
				write("334 UGFzc3dvcmQ6\r\n")
				if p, err := base64.StdEncoding.DecodeString(read()); err == nil {
					out.authPass = string(p)
				}
				write("235 2.7.0 Authentication successful\r\n")
			case strings.HasPrefix(line, "MAIL FROM:"):
				out.mailFrom = strings.TrimSpace(strings.TrimPrefix(line, "MAIL FROM:"))
				write("250 2.1.0 Ok\r\n")
			case strings.HasPrefix(line, "RCPT TO:"):
				out.rcpts = append(out.rcpts, strings.TrimSpace(strings.TrimPrefix(line, "RCPT TO:")))
				write("250 2.1.5 Ok\r\n")
			case line == "DATA":
				write("354 End data with <CR><LF>.<CR><LF>\r\n")
				var body []string
				for {
					d := read()
					if d == "." || d == "" {
						break
					}
					body = append(body, d)
				}
				out.data = strings.Join(body, "\n")
				write("250 2.0.0 Ok: queued\r\n")
			case line == "QUIT":
				write("221 2.0.0 Bye\r\n")
				tr <- out
				return
			default:
				write("250 Ok\r\n")
			}
		}
	}()
	return f
}

func (f *fakeSMTP) wait(t *testing.T) transcript {
	t.Helper()
	select {
	case out := <-f.tr:
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the fake SMTP transcript")
		return transcript{}
	}
}

// mailTestDB opens a migrated DB with key/value settings pre-set.
func mailTestDB(t *testing.T, kv ...string) (*sql.DB, *crypto.Crypto) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	var key [32]byte
	c := crypto.NewWithKey(key)
	for i := 0; i+1 < len(kv); i += 2 {
		if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, '')`, kv[i], kv[i+1]); err != nil {
			t.Fatal(err)
		}
	}
	return db, c
}

func sealPassword(t *testing.T, c *crypto.Crypto, db *sql.DB, plain string) {
	t.Helper()
	enc, err := c.Encrypt([]byte(plain))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('mail.password', ?, '')`, enc); err != nil {
		t.Fatal(err)
	}
}

func newTestMailer(t *testing.T, db *sql.DB, c *crypto.Crypto) *Mailer {
	t.Helper()
	m := New(settings.NewRepository(db), c, slog.New(slog.DiscardHandler))
	m.timeout = 5 * time.Second
	return m
}

// selfSignedCert builds a certificate valid for 127.0.0.1 plus the pool that
// trusts it, so a test can exercise verified STARTTLS.
func selfSignedCert(t *testing.T) (*tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

func deliveryMessage(t *testing.T, err error) string {
	t.Helper()
	var de *DeliveryError
	if !errors.As(err, &de) {
		t.Fatalf("error is %T (%v), want *DeliveryError", err, err)
	}
	return de.Message
}

func TestMessageFormat(t *testing.T) {
	msg, err := Message("panel@example.com", []string{"a@example.com", "b@example.com"}, "WhaCP: reset your password", "line one\n.leading dot\r\nlast")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"From: WhaCP <panel@example.com>",
		"To: a@example.com, b@example.com",
		"Subject: WhaCP: reset your password",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
		"Auto-Submitted: auto-generated",
		"Date: ",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
	if !regexp.MustCompile(`Message-ID: <[0-9a-f]{24}@example\.com>`).MatchString(msg) {
		t.Errorf("bad Message-ID:\n%s", msg)
	}
	if !strings.Contains(msg, "\r\n\r\nline one\r\n..leading dot\r\nlast") {
		t.Errorf("body not normalized/dot-stuffed:\n%q", msg)
	}
	msg2, err := Message("panel@example.com", []string{"a@example.com"}, "a\nb\tc", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg2, "Subject: a b c") {
		t.Errorf("subject not collapsed:\n%s", msg2)
	}
}

func TestValidAddress(t *testing.T) {
	for _, ok := range []string{"a@b.co", "user+tag@ex-ample.test", "A_B-c.d@x.y.z"} {
		if !ValidAddress(ok) {
			t.Errorf("ValidAddress(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "a@", "@b.co", "a b@c.co", "a<b@c.co", "a\r\nb@c.co", "a@b", "a@@b.co", strings.Repeat("x", 250) + "@b.co"} {
		if ValidAddress(bad) {
			t.Errorf("ValidAddress(%q) = true, want false", bad)
		}
	}
}

func TestSendUnconfigured(t *testing.T) {
	db, c := mailTestDB(t)
	m := newTestMailer(t, db, c)
	if ok, err := m.Configured(context.Background()); err != nil || ok {
		t.Fatalf("Configured = %v, %v; want false, nil", ok, err)
	}
	if got := deliveryMessage(t, m.Send(context.Background(), []string{"a@example.com"}, "s", "b")); got != "Outgoing email is not set up." {
		t.Fatalf("Send message = %q", got)
	}
}

func TestSendInvalidRecipient(t *testing.T) {
	db, c := mailTestDB(t,
		"mail.host", `"127.0.0.1"`, "mail.port", "2525",
		"mail.encryption", `"none"`, "mail.from", `"panel@example.com"`,
	)
	m := newTestMailer(t, db, c)
	if got := deliveryMessage(t, m.Send(context.Background(), []string{"bad address"}, "s", "b")); got != "Invalid recipient address." {
		t.Fatalf("Send message = %q", got)
	}
	if err := m.Send(context.Background(), nil, "s", "b"); err != nil {
		t.Fatalf("Send(no recipients) = %v; want nil", err)
	}
}

func TestSendRefusesUnencryptedAuth(t *testing.T) {
	db, c := mailTestDB(t,
		"mail.host", `"mail.example.com"`, "mail.encryption", `"none"`,
		"mail.username", `"user"`, "mail.from", `"panel@example.com"`,
	)
	sealPassword(t, c, db, "secret123")
	m := newTestMailer(t, db, c)
	if got := deliveryMessage(t, m.Send(context.Background(), []string{"a@example.com"}, "s", "b")); got != "Refusing to send the password over an unencrypted connection." {
		t.Fatalf("Send message = %q", got)
	}
}

func TestSendPlainDeliveryDedupesRecipients(t *testing.T) {
	f := runFakeSMTP(t, "", nil)
	db, c := mailTestDB(t,
		"mail.host", `"127.0.0.1"`, "mail.port", fmt.Sprint(f.port),
		"mail.encryption", `"none"`, "mail.from", `"panel@example.com"`,
	)
	m := newTestMailer(t, db, c)
	body := "Someone (hopefully you) asked.\r\nChoose a new password here within 30 minutes:\nhttps://panel.example.test/password/reset/abc"
	if err := m.Send(context.Background(), []string{"a@example.com", "a@example.com", "b@example.com"}, "WhaCP: reset your password", body); err != nil {
		t.Fatalf("Send = %v", err)
	}
	out := f.wait(t)
	if out.mailFrom != "<panel@example.com>" {
		t.Errorf("MAIL FROM = %q", out.mailFrom)
	}
	if len(out.rcpts) != 2 || out.rcpts[0] != "<a@example.com>" || out.rcpts[1] != "<b@example.com>" {
		t.Errorf("RCPTs = %v, want a then b (deduped)", out.rcpts)
	}
	for _, want := range []string{
		"From: WhaCP <panel@example.com>",
		"To: a@example.com, b@example.com",
		"Subject: WhaCP: reset your password",
		"Someone (hopefully you) asked.",
		"Choose a new password here within 30 minutes:",
		"https://panel.example.test/password/reset/abc",
	} {
		if !strings.Contains(out.data, want) {
			t.Errorf("DATA missing %q:\n%s", want, out.data)
		}
	}
	if out.authMode != "" {
		t.Errorf("unexpected auth mode %q", out.authMode)
	}
}

func TestSendAuthPlain(t *testing.T) {
	f := runFakeSMTP(t, "AUTH PLAIN LOGIN", nil)
	db, c := mailTestDB(t,
		"mail.host", `"127.0.0.1"`, "mail.port", fmt.Sprint(f.port),
		"mail.encryption", `"none"`, "mail.username", `"mailer-user"`,
		"mail.from", `"panel@example.com"`,
	)
	sealPassword(t, c, db, "secret123")
	m := newTestMailer(t, db, c)
	if err := m.Send(context.Background(), []string{"a@example.com"}, "s", "b"); err != nil {
		t.Fatalf("Send = %v", err)
	}
	out := f.wait(t)
	if out.authMode != "plain" || out.authUser != "mailer-user" || out.authPass != "secret123" {
		t.Errorf("AUTH PLAIN = mode %q user %q pass %q", out.authMode, out.authUser, out.authPass)
	}
}

func TestSendAuthLogin(t *testing.T) {
	f := runFakeSMTP(t, "AUTH LOGIN", nil)
	db, c := mailTestDB(t,
		"mail.host", `"127.0.0.1"`, "mail.port", fmt.Sprint(f.port),
		"mail.encryption", `"none"`, "mail.username", `"mailer-user"`,
		"mail.from", `"panel@example.com"`,
	)
	sealPassword(t, c, db, "secret123")
	m := newTestMailer(t, db, c)
	if err := m.Send(context.Background(), []string{"a@example.com"}, "s", "b"); err != nil {
		t.Fatalf("Send = %v", err)
	}
	out := f.wait(t)
	if out.authMode != "login" || out.authUser != "mailer-user" || out.authPass != "secret123" {
		t.Errorf("AUTH LOGIN = mode %q user %q pass %q", out.authMode, out.authUser, out.authPass)
	}
}

func TestSendStartTLS(t *testing.T) {
	cert, pool := selfSignedCert(t)
	f := runFakeSMTP(t, "STARTTLS", cert)
	db, c := mailTestDB(t,
		"mail.host", `"127.0.0.1"`, "mail.port", fmt.Sprint(f.port),
		"mail.encryption", `"starttls"`, "mail.from", `"panel@example.com"`,
	)
	m := newTestMailer(t, db, c)
	m.roots = pool
	if err := m.Send(context.Background(), []string{"a@example.com"}, "s", "b"); err != nil {
		t.Fatalf("Send = %v", err)
	}
	out := f.wait(t)
	if !out.tlsUpgraded {
		t.Fatal("the connection was not upgraded to TLS")
	}
	if out.mailFrom != "<panel@example.com>" {
		t.Errorf("MAIL FROM = %q", out.mailFrom)
	}
	if !strings.Contains(out.data, "To: a@example.com") {
		t.Errorf("DATA = %q", out.data)
	}
}

func TestSendStartTLSNotOffered(t *testing.T) {
	f := runFakeSMTP(t, "", nil)
	db, c := mailTestDB(t,
		"mail.host", `"127.0.0.1"`, "mail.port", fmt.Sprint(f.port),
		"mail.encryption", `"starttls"`, "mail.from", `"panel@example.com"`,
	)
	m := newTestMailer(t, db, c)
	if got := deliveryMessage(t, m.Send(context.Background(), []string{"a@example.com"}, "s", "b")); got != "The mail server does not offer STARTTLS." {
		t.Fatalf("Send message = %q", got)
	}
}

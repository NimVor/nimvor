// Package mail sends the panel's plain-text email (password resets, alerts).
// It is the Go port of Mail\SmtpMailer: a minimal RFC 5321 client with
// implicit TLS or STARTTLS (certificate verified), AUTH PLAIN/LOGIN and
// quoted-printable plain-text bodies. The SMTP credentials live in the
// shared `settings` table (mail.* keys) with the password sealed under the
// panel key, exactly as on the PHP side. Credentials are never sent over an
// unencrypted connection except to this machine.
package mail

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

const (
	timeout         = 15 * time.Second
	replyLimitBytes = 1024
)

// Encryption modes (MailConfig::ENCRYPTION keys).
const (
	EncryptionTLS      = "tls"      // implicit TLS, port 465
	EncryptionSTARTTLS = "starttls" // STARTTLS, port 587
	EncryptionNone     = "none"     // local relay only
)

// Config is one outgoing-mail configuration (MailConfig::get).
type Config struct {
	Host       string
	Port       int
	Encryption string
	Username   string
	Password   string // decrypted; never logged
	From       string
}

// DeliveryError is a user-facing delivery failure: Message is what the UI
// shows, Why is the protocol detail (logs only) and Reason suggests the fix
// (MailException's three parts).
type DeliveryError struct {
	Message string
	Why     string
	Reason  string
}

func (e *DeliveryError) Error() string { return e.Message + " " + e.Why }

// Mailer sends plain-text email. One instance is safe for serial use; the
// panel sends at most a few messages per request.
type Mailer struct {
	settings *settings.Repository
	crypto   *crypto.Crypto
	log      *slog.Logger
	timeout  time.Duration
	roots    *x509.CertPool // nil = the system root certificates
}

func New(s *settings.Repository, c *crypto.Crypto, log *slog.Logger) *Mailer {
	return &Mailer{settings: s, crypto: c, log: log, timeout: timeout}
}

// Configured reports whether outgoing mail has been set up
// (MailConfig::configured).
func (m *Mailer) Configured(ctx context.Context) (bool, error) {
	cfg, err := m.config(ctx)
	if err != nil {
		return false, err
	}
	return cfg.Host != "" && cfg.From != "", nil
}

// Send delivers a plain-text UTF-8 message to the recipients. An empty (or
// fully-duplicate) recipient list is a no-op, mirroring the PHP client.
func (m *Mailer) Send(ctx context.Context, to []string, subject, text string) error {
	cfg, err := m.config(ctx)
	if err != nil {
		return err
	}
	if cfg.Host == "" || cfg.From == "" {
		return &DeliveryError{
			Message: "Outgoing email is not set up.",
			Reason:  "Enter an SMTP server under Settings → Email.",
		}
	}
	to = dedupe(to)
	for _, a := range to {
		if !ValidAddress(a) {
			return &DeliveryError{Message: "Invalid recipient address."}
		}
	}
	if len(to) == 0 {
		return nil
	}
	msg, err := Message(cfg.From, to, subject, text)
	if err != nil {
		return err
	}
	if cfg.Username != "" {
		local := cfg.Host == "localhost" || cfg.Host == "127.0.0.1" || cfg.Host == "::1"
		if cfg.Encryption == EncryptionNone && !local {
			return &DeliveryError{
				Message: "Refusing to send the password over an unencrypted connection.",
				Reason:  "Choose TLS or STARTTLS, or use a relay on this server.",
			}
		}
	}
	sess, err := m.dial(ctx, cfg, m.timeout)
	if err != nil {
		m.log.Warn("mail delivery failed", "host", cfg.Host, "error", err.Error())
		return err
	}
	defer sess.quit()
	if err := sess.deliver(cfg, to, msg); err != nil {
		var de *DeliveryError
		if errors.As(err, &de) {
			m.log.Warn("mail delivery failed", "host", cfg.Host, "error", err.Error(), "reason", de.Reason)
		} else {
			m.log.Warn("mail delivery failed", "host", cfg.Host, "error", err.Error())
		}
		return err
	}
	return nil
}

// config reads the mail.* settings (MailConfig::get + password()).
func (m *Mailer) config(ctx context.Context) (Config, error) {
	s := m.settings
	host, err := s.String(ctx, "mail.host", "")
	if err != nil {
		return Config{}, err
	}
	port, err := s.Int(ctx, "mail.port", 587)
	if err != nil {
		return Config{}, err
	}
	enc, err := s.String(ctx, "mail.encryption", EncryptionSTARTTLS)
	if err != nil {
		return Config{}, err
	}
	username, err := s.String(ctx, "mail.username", "")
	if err != nil {
		return Config{}, err
	}
	from, err := s.String(ctx, "mail.from", "")
	if err != nil {
		return Config{}, err
	}
	var password string
	if encPW, err := s.String(ctx, "mail.password", ""); err != nil {
		return Config{}, err
	} else if encPW != "" {
		plain, err := m.crypto.Decrypt(encPW)
		if err != nil {
			return Config{}, fmt.Errorf("mail: decrypt password: %w", err)
		}
		password = string(plain)
	}
	return Config{Host: host, Port: port, Encryption: enc, Username: username, Password: password, From: from}, nil
}

func dedupe(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, a := range in {
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}

// ValidAddress is MailConfig::validAddress: a sensible email address, no
// header-injection characters.
func ValidAddress(a string) bool {
	if len(a) > 254 {
		return false
	}
	if strings.ContainsAny(a, "\r\n<>") {
		return false
	}
	at := strings.Index(a, "@")
	if at < 1 || at != strings.LastIndex(a, "@") {
		return false
	}
	local, domain := a[:at], a[at+1:]
	if local == "" || domain == "" || len(domain) < 3 {
		return false
	}
	for i := 0; i < len(local); i++ {
		c := local[i]
		if !(c == '.' || c == '_' || c == '-' || c == '+' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	for i := 0; i < len(domain); i++ {
		c := domain[i]
		if !(c == '.' || c == '-' ||
			(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// sess is one SMTP conversation (SmtpMailer's socket + caps).
type sess struct {
	conn net.Conn
	in   *bufio.Reader
	caps string
	to   time.Duration
}

func (s *sess) quit() {
	_, _ = s.conn.Write([]byte("QUIT\r\n"))
	_ = s.conn.Close()
}

func (s *sess) deadline() {
	_ = s.conn.SetDeadline(time.Now().Add(s.to))
}

func (s *sess) write(data string) error {
	s.deadline()
	if _, err := s.conn.Write([]byte(data)); err != nil {
		return &DeliveryError{Message: "The connection to the mail server was lost.", Why: err.Error()}
	}
	return nil
}

// expect reads a (multi-line) reply until the "NNN " terminator and checks
// its code against ok. It returns the reply with the status prefixes
// stripped, as the PHP client does before capability inspection.
func (s *sess) expect(ok ...int) (string, error) {
	s.deadline()
	var reply strings.Builder
	for {
		line, readErr := s.in.ReadString('\n')
		if len(line) > replyLimitBytes {
			line = line[:replyLimitBytes]
		}
		if line != "" {
			reply.WriteString(line)
			if len(line) < 4 || line[3] == ' ' {
				break
			}
		}
		if readErr != nil {
			msg := "The connection to the mail server was lost."
			if errors.Is(readErr, os.ErrDeadlineExceeded) {
				msg = "The mail server did not answer in time."
			}
			return "", &DeliveryError{Message: msg, Why: readErr.Error()}
		}
	}
	code, err := strconv.Atoi(reply.String()[:3])
	if err != nil {
		return "", &DeliveryError{Message: "The mail server sent a malformed reply.", Why: reply.String()}
	}
	for _, want := range ok {
		if code == want {
			return stripReplyPrefix(reply.String()), nil
		}
	}
	why := strings.TrimSpace(reply.String())
	if len(why) > 300 {
		why = why[:300]
	}
	return "", &DeliveryError{Message: "The mail server refused the message.", Why: why}
}

func (s *sess) command(line string, ok ...int) (string, error) {
	if err := s.write(line + "\r\n"); err != nil {
		return "", err
	}
	return s.expect(ok...)
}

var replyPrefixRe = regexp.MustCompile(`(?m)^\d{3}[ -]`)

func stripReplyPrefix(reply string) string {
	return replyPrefixRe.ReplaceAllString(reply, "")
}

var ehloNameRe = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

func (m *Mailer) dial(ctx context.Context, cfg Config, to time.Duration) (*sess, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	d := &net.Dialer{Timeout: to}
	if cfg.Encryption == EncryptionTLS {
		conn, err := tls.DialWithDialer(d, "tcp", addr, m.tlsConfig(cfg.Host))
		if err != nil {
			return nil, &DeliveryError{
				Message: "Cannot connect to the mail server.",
				Why:     fmt.Sprintf("%s:%d: %v", cfg.Host, cfg.Port, err),
				Reason:  "Check the host, port and encryption mode, and that outgoing connections are allowed.",
			}
		}
		s := &sess{conn: conn, in: bufio.NewReader(conn), to: to}
		if _, err := s.expect(220); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if err := s.ehlo(); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return s, nil
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, &DeliveryError{
			Message: "Cannot connect to the mail server.",
			Why:     fmt.Sprintf("%s:%d: %v", cfg.Host, cfg.Port, err),
			Reason:  "Check the host, port and encryption mode, and that outgoing connections are allowed.",
		}
	}
	s := &sess{conn: conn, in: bufio.NewReader(conn), to: to}
	if _, err := s.expect(220); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := s.ehlo(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if cfg.Encryption == EncryptionSTARTTLS {
		if !hasStartTLS(s.caps) {
			_ = conn.Close()
			return nil, &DeliveryError{
				Message: "The mail server does not offer STARTTLS.",
				Reason:  "Choose TLS (port 465), or a server that supports STARTTLS.",
			}
		}
		if _, err := s.command("STARTTLS", 220); err != nil {
			_ = conn.Close()
			return nil, err
		}
		tc := tls.Client(conn, m.tlsConfig(cfg.Host))
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, &DeliveryError{
				Message: "The secure connection to the mail server failed.",
				Why:     "TLS handshake or certificate verification failed.",
				Reason:  "Check that the host name matches the server certificate.",
			}
		}
		s.conn = tc
		s.in = bufio.NewReader(tc)
		if err := s.ehlo(); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *sess) ehlo() error {
	name, err := os.Hostname()
	if err != nil || name == "" || !ehloNameRe.MatchString(name) {
		name = "localhost"
	}
	caps, err := s.command("EHLO "+name, 250)
	if err != nil {
		return err
	}
	s.caps = caps
	return nil
}

// tlsConfig is the verified-CA, SNI-bearing TLS config shared by implicit
// TLS and STARTTLS (MinVersion TLS 1.2, matching PHP's
// STREAM_CRYPTO_METHOD_TLSv1_2_CLIENT | TLSv1_3_CLIENT).
func (m *Mailer) tlsConfig(host string) *tls.Config {
	return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: m.roots}
}

func hasStartTLS(caps string) bool {
	return regexp.MustCompile(`(?mi)^STARTTLS\b`).MatchString(caps)
}

func hasAuthCaps(caps string) (bool, string) {
	m := regexp.MustCompile(`(?mi)^AUTH\b(.*)$`).FindStringSubmatch(caps)
	if m == nil {
		return false, ""
	}
	return true, m[1]
}

// deliver authenticates (when configured) and sends the message
// (SmtpMailer::deliver).
func (s *sess) deliver(cfg Config, to []string, msg string) error {
	if cfg.Username != "" {
		rejected := "The mail server rejected the user name or password."
		hasAuth, authCaps := hasAuthCaps(s.caps)
		hasPlain := regexp.MustCompile(`\bPLAIN\b`).MatchString(authCaps)
		hasLogin := regexp.MustCompile(`\bLOGIN\b`).MatchString(authCaps)
		if hasAuth && !hasPlain && hasLogin {
			if _, err := s.command("AUTH LOGIN", 334); err != nil {
				return err
			}
			if _, err := s.command(base64.StdEncoding.EncodeToString([]byte(cfg.Username)), 334); err != nil {
				return err
			}
			if _, err := s.command(base64.StdEncoding.EncodeToString([]byte(cfg.Password)), 235); err != nil {
				var de *DeliveryError
				if !errors.As(err, &de) || de.Message == "The mail server refused the message." {
					return &DeliveryError{Message: rejected, Why: err.Error()}
				}
				return err
			}
		} else {
			plain := base64.StdEncoding.EncodeToString([]byte("\x00" + cfg.Username + "\x00" + cfg.Password))
			if _, err := s.command("AUTH PLAIN "+plain, 235); err != nil {
				var de *DeliveryError
				if !errors.As(err, &de) || de.Message == "The mail server refused the message." {
					return &DeliveryError{Message: rejected, Why: err.Error()}
				}
				return err
			}
		}
	}
	if _, err := s.command("MAIL FROM: <"+cfg.From+">", 250); err != nil {
		return err
	}
	for _, a := range to {
		if _, err := s.command("RCPT TO: <"+a+">", 250, 251); err != nil {
			return err
		}
	}
	if _, err := s.command("DATA", 354); err != nil {
		return err
	}
	if err := s.write(msg + "\r\n.\r\n"); err != nil {
		return err
	}
	_, err := s.expect(250)
	return err
}

// Message builds the wire message (SmtpMailer::message): the fixed headers,
// then a quoted-printable UTF-8 body with dot-stuffing (RFC 5321 §4.5.2).
func Message(from string, to []string, subject, text string) (string, error) {
	subject = strings.Join(regexp.MustCompile(`[\r\n\t]+`).Split(subject, -1), " ")
	subject = strings.TrimSpace(subject)
	domain := "localhost"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		if d := from[i+1:]; d != "" {
			domain = d
		}
	}
	id := make([]byte, 12)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	headers := []string{
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"From: WhaCP <" + from + ">",
		"To: " + strings.Join(to, ", "),
		"Subject: " + mimeQ(subject),
		"Message-ID: <" + hex.EncodeToString(id) + "@" + domain + ">",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: quoted-printable",
		"Auto-Submitted: auto-generated",
	}
	body := qpEncode(normalizeNewlines(text))
	body = regexp.MustCompile(`(?m)^\.`).ReplaceAllString(body, "..")
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body, nil
}

// normalizeNewlines maps any line ending to \r\n (PHP: \r\n -> \n, lone \r
// -> \n, then \n -> \r\n).
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// qpEncode is quoted-printable (RFC 2045) for the UTF-8 body: CRLF stay hard
// line breaks, long lines get soft breaks at 76 columns and "=" and
// non-ASCII bytes are escaped. It uses the standard library's encoder, which
// matches PHP's quoted_printable_encode up to equivalent encodings (checked
// differentially against PHP 8.3: only tab / trailing-space escaping differ).
func qpEncode(s string) string {
	var buf bytes.Buffer
	w := quotedprintable.NewWriter(&buf)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return buf.String()
}

// mimeQ is mb_encode_mimeheader($s, 'UTF-8', 'Q', "\r\n"): a subject with
// non-ASCII or control bytes becomes an RFC 2047 Q encoded-word
// ("=?UTF-8?q?...?="); a plain ASCII subject passes through unchanged.
func mimeQ(s string) string {
	return mime.QEncoding.Encode("UTF-8", s)
}

package tlsprofile

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/changeset"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// The mail server's TLS (Postfix and Dovecot) and the certificates of
// WhaCP's own services (the panel, the mail server). They are kept here so
// the mail setup (mailmgr) and the SSL/TLS pages (sslmgr) agree: a mail
// reconfiguration keeps what the administrator chose.

// Mail is the mail server's TLS configuration.
type Mail struct {
	// Protocols: "tls12" (TLS 1.2 and 1.3) or "tls13" (TLS 1.3 only) for
	// IMAP and mail submission. Port 25 accepts what sending servers
	// offer (refusing them makes them deliver without encryption).
	Protocols string `json:"protocols"`
	// Ciphers: "default" (the programs' own lists) or "intermediate".
	Ciphers string `json:"ciphers"`
}

// MailDefault is what WhaCP set up before the choice existed.
var MailDefault = Mail{Protocols: "tls12", Ciphers: "default"}

// Validate checks a mail profile.
func (m Mail) Validate() error {
	return Profile{Protocols: m.Protocols, Ciphers: m.Ciphers}.Validate()
}

func mailPath(e *host.Env) string { return filepath.Join(e.Cfg.StateDir, "mail-tls.json") }

// LoadMail returns the saved mail profile (MailDefault when none).
func LoadMail(e *host.Env) Mail {
	m := MailDefault
	if b, err := e.Root.ReadFile(mailPath(e)); err == nil {
		var q Mail
		if json.Unmarshal(b, &q) == nil && q.Validate() == nil {
			m = q
		}
	}
	return m
}

// SaveMail adds the mail profile to set.
func SaveMail(e *host.Env, set *changeset.Set, m Mail) {
	b, _ := json.MarshalIndent(m, "", "  ")
	set.Write(mailPath(e), b, 0o600, agentfs.RootOwner)
}

// PostfixMandatoryProtocols is smtpd_tls_mandatory_protocols (written in
// the form Postfix 3.5, on RHEL 9, understands).
func (m Mail) PostfixMandatoryProtocols() string {
	if m.Protocols == "tls13" {
		return "!SSLv2, !SSLv3, !TLSv1, !TLSv1.1, !TLSv1.2"
	}
	return "!SSLv2, !SSLv3, !TLSv1, !TLSv1.1"
}

// PostfixCiphers are the cipher settings for postconf -e.
func (m Mail) PostfixCiphers() [][2]string {
	if m.Ciphers == "intermediate" {
		return [][2]string{{"smtpd_tls_mandatory_ciphers", "high"}, {"tls_high_cipherlist", intermediateCiphers}}
	}
	return [][2]string{{"smtpd_tls_mandatory_ciphers", "medium"}}
}

// DovecotLines are the TLS lines of WhaCP's Dovecot drop-in.
func (m Mail) DovecotLines() string {
	s := "ssl_min_protocol = TLSv1.2\n"
	if m.Protocols == "tls13" {
		s = "ssl_min_protocol = TLSv1.3\n"
	}
	if m.Ciphers == "intermediate" && m.Protocols != "tls13" {
		s += "ssl_cipher_list = " + intermediateCiphers + "\n"
	}
	return s
}

// ReplaceDovecotLines swaps the TLS lines in a rendered drop-in.
func ReplaceDovecotLines(conf string, m Mail) (string, error) {
	var out []string
	done := false
	for _, line := range strings.Split(conf, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "ssl_cipher_list"):
			continue
		case strings.HasPrefix(t, "ssl_min_protocol"):
			out = append(out, strings.TrimSuffix(m.DovecotLines(), "\n"))
			done = true
			continue
		}
		out = append(out, line)
	}
	if !done {
		return "", fmt.Errorf("the Dovecot configuration has no ssl_min_protocol line")
	}
	return strings.Join(out, "\n"), nil
}

// ServiceCert is a certificate chosen for one of WhaCP's services.
type ServiceCert struct {
	Cert string `json:"cert"`
	Key  string `json:"key"`
	// Previous is what the service used before (restored on "default").
	PrevCert string `json:"prev_cert,omitempty"`
	PrevKey  string `json:"prev_key,omitempty"`
}

func serviceCertsPath(e *host.Env) string { return filepath.Join(e.Cfg.StateDir, "service-certs.json") }

// LoadServiceCerts returns the chosen certificates by service ("panel", "mail").
func LoadServiceCerts(e *host.Env) map[string]ServiceCert {
	out := map[string]ServiceCert{}
	if b, err := e.Root.ReadFile(serviceCertsPath(e)); err == nil {
		json.Unmarshal(b, &out)
	}
	return out
}

// SaveServiceCerts adds the choices to set.
func SaveServiceCerts(e *host.Env, set *changeset.Set, certs map[string]ServiceCert) {
	b, _ := json.MarshalIndent(certs, "", "  ")
	set.Write(serviceCertsPath(e), b, 0o600, agentfs.RootOwner)
}

// MailCert is the administrator's certificate for the mail server, when
// one was chosen and its files exist.
func MailCert(e *host.Env) (cert, key string, ok bool) {
	c, found := LoadServiceCerts(e)["mail"]
	if !found || c.Cert == "" || !e.Root.Exists(c.Cert) || !e.Root.Exists(c.Key) {
		return "", "", false
	}
	return c.Cert, c.Key, true
}

// Package tlsaudit probes a server's own TLS endpoints and reports weak
// configuration (security items 70–71): the obsolete protocols TLS 1.0 and
// 1.1 still being accepted, weak (CBC-SHA / 3DES / export) cipher suites
// being accepted, and certificate problems — a SHA-1 signature, an expired
// or not-yet-valid certificate, an untrusted chain or a host-name mismatch.
//
// It only connects and reports; it never changes a server's configuration,
// so an audit can never break a customer's website. Applying a fix is a
// separate, explicit action (the server-wide TLS profile).
package tlsaudit

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Endpoint is one TLS listener to probe.
type Endpoint struct {
	Host       string `json:"host"`        // dial host; defaults to 127.0.0.1
	Port       int    `json:"port"`        // required
	ServerName string `json:"server_name"` // SNI and the name verified against the certificate
	Kind       string `json:"kind"`        // website, mail-smtps, mail-submission, mail-imaps, panel …
	StartTLS   string `json:"starttls"`    // "" (implicit TLS) or "smtp"
}

func (e Endpoint) dialHost() string {
	if e.Host != "" {
		return e.Host
	}
	return "127.0.0.1"
}

func (e Endpoint) addr() string { return net.JoinHostPort(e.dialHost(), fmt.Sprint(e.Port)) }

func (e Endpoint) label() string {
	if e.ServerName != "" {
		return fmt.Sprintf("%s (%s)", e.ServerName, e.addr())
	}
	return e.addr()
}

// Finding is one problem at an endpoint.
type Finding struct {
	Endpoint string `json:"endpoint"`
	Kind     string `json:"kind"`
	Issue    string `json:"issue"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
}

// Result is the outcome for one endpoint.
type Result struct {
	Endpoint    string    `json:"endpoint"`
	Kind        string    `json:"kind"`
	Reachable   bool      `json:"reachable"`
	MinVersion  string    `json:"min_version,omitempty"`  // the lowest protocol it accepts
	BestVersion string    `json:"best_version,omitempty"` // negotiated with a modern client
	Findings    []Finding `json:"findings,omitempty"`
}

const dialTimeout = 8 * time.Second

// Probe audits one endpoint.
func Probe(ctx context.Context, ep Endpoint) Result {
	r := Result{Endpoint: ep.label(), Kind: ep.Kind}
	add := func(issue, sev, detail string) {
		r.Findings = append(r.Findings, Finding{Endpoint: ep.label(), Kind: ep.Kind, Issue: issue, Severity: sev, Detail: detail})
	}

	// First, an insecure handshake offering every protocol and cipher: this
	// always connects to any TLS server and gives us the certificate chain
	// and the best protocol it will negotiate, independent of trust.
	state, _, connected := handshake(ctx, ep, &tls.Config{
		ServerName: ep.ServerName, InsecureSkipVerify: true,
		MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS13, CipherSuites: allCipherIDs(),
	})
	if !connected || len(state.PeerCertificates) == 0 {
		return r
	}
	r.Reachable = true
	r.BestVersion = versionName(bestVersion(ctx, ep))

	// A separate verifying pass for trust and host-name problems.
	if _, verifyErr, _ := handshake(ctx, ep, &tls.Config{ServerName: ep.ServerName, MinVersion: tls.VersionTLS12}); verifyErr != nil {
		switch classifyVerifyError(verifyErr) {
		case "hostname":
			add("hostname", "high", "the certificate is not valid for "+ep.ServerName+": "+verifyErr.Error())
		case "expired":
			add("cert-expired", "high", "the certificate is expired or not yet valid: "+verifyErr.Error())
		case "untrusted":
			add("chain", "medium", "the certificate chain is not trusted (missing intermediate or self-signed): "+verifyErr.Error())
		default:
			add("chain", "medium", "certificate verification failed: "+verifyErr.Error())
		}
	}
	// Certificate details (independent of trust): SHA-1 signature, expiry.
	for i, c := range state.PeerCertificates {
		if isSHA1(c.SignatureAlgorithm) && !bytesEqualIssuerSubject(c) {
			add("cert-sha1", "high", fmt.Sprintf("certificate %d (%s) is signed with SHA-1 (%s)", i, c.Subject.CommonName, c.SignatureAlgorithm))
		}
	}
	if leaf := leafOf(state); leaf != nil {
		now := time.Now()
		if now.After(leaf.NotAfter) {
			add("cert-expired", "high", "the leaf certificate expired on "+leaf.NotAfter.UTC().Format("2006-01-02"))
		} else if now.Before(leaf.NotBefore) {
			add("cert-expired", "high", "the leaf certificate is not valid until "+leaf.NotBefore.UTC().Format("2006-01-02"))
		}
	}

	// Obsolete protocols: TLS 1.0 and 1.1 must be refused. Offer every cipher
	// so the handshake can complete if the server allows the old protocol.
	lowest := state.Version
	for _, v := range []struct {
		ver  uint16
		name string
	}{{tls.VersionTLS10, "TLS 1.0"}, {tls.VersionTLS11, "TLS 1.1"}} {
		if st, err, ok := handshake(ctx, ep, &tls.Config{ServerName: ep.ServerName, InsecureSkipVerify: true, MinVersion: v.ver, MaxVersion: v.ver, CipherSuites: allCipherIDs()}); ok && err == nil {
			add("protocol-weak", "high", v.name+" is accepted; allow only TLS 1.2 and 1.3")
			if st.Version < lowest {
				lowest = st.Version
			}
		}
	}
	r.MinVersion = versionName(lowest)

	// Weak cipher suites: offer only the insecure ones at TLS 1.2 or below
	// and see whether the server picks one.
	if _, err, ok := handshake(ctx, ep, &tls.Config{
		ServerName: ep.ServerName, InsecureSkipVerify: true,
		MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS12, CipherSuites: weakCipherIDs(),
	}); ok && err == nil {
		add("cipher-weak", "medium", "a weak cipher suite (CBC-SHA / 3DES / export) is accepted at TLS 1.2 or below")
	}
	return r
}

// bestVersion reports the highest protocol the server negotiates with a
// modern client (no cipher restriction).
func bestVersion(ctx context.Context, ep Endpoint) uint16 {
	if st, err, ok := handshake(ctx, ep, &tls.Config{ServerName: ep.ServerName, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS13}); ok && err == nil {
		return st.Version
	}
	return tls.VersionTLS10
}

// allCipherIDs returns every cipher suite Go knows, secure and insecure, for
// probes that must complete a handshake against any server.
func allCipherIDs() []uint16 {
	var ids []uint16
	for _, c := range tls.CipherSuites() {
		ids = append(ids, c.ID)
	}
	return append(ids, weakCipherIDs()...)
}

// handshake dials the endpoint (doing SMTP STARTTLS first when asked) and
// attempts a TLS handshake with cfg. connected is true once the TCP and
// STARTTLS stages succeed, regardless of the handshake outcome; err is the
// handshake error (nil on success, or the certificate verification error
// when cfg verifies), and the returned state is the connection state (it
// carries the peer certificates even when verification failed).
func handshake(ctx context.Context, ep Endpoint, cfg *tls.Config) (tls.ConnectionState, error, bool) {
	d := net.Dialer{Timeout: dialTimeout}
	raw, err := d.DialContext(ctx, "tcp", ep.addr())
	if err != nil {
		return tls.ConnectionState{}, err, false
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(dialTimeout))
	if ep.StartTLS == "smtp" {
		if err := smtpStartTLS(raw, ep.ServerName); err != nil {
			return tls.ConnectionState{}, err, false
		}
	}
	conn := tls.Client(raw, cfg)
	hsErr := conn.HandshakeContext(ctx)
	return conn.ConnectionState(), hsErr, true
}

// smtpStartTLS drives the minimal SMTP dialogue up to STARTTLS.
func smtpStartTLS(conn net.Conn, name string) error {
	br := bufio.NewReader(conn)
	read := func() error {
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return err
			}
			if len(line) >= 4 && (line[3] == ' ') {
				if line[0] != '2' && line[0] != '3' {
					return errors.New("smtp: " + strings.TrimSpace(line))
				}
				return nil
			}
		}
	}
	if err := read(); err != nil { // greeting
		return err
	}
	host := name
	if host == "" {
		host = "localhost"
	}
	if _, err := fmt.Fprintf(conn, "EHLO %s\r\n", host); err != nil {
		return err
	}
	if err := read(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(conn, "STARTTLS\r\n"); err != nil {
		return err
	}
	return read()
}

func leafOf(s tls.ConnectionState) *x509.Certificate {
	if len(s.PeerCertificates) > 0 {
		return s.PeerCertificates[0]
	}
	return nil
}

func bytesEqualIssuerSubject(c *x509.Certificate) bool {
	// A self-signed root legitimately signs itself; browsers never see it
	// over the wire for a public CA, but a private root with SHA-1 is less
	// interesting than a SHA-1 leaf. Treat only root self-signatures as
	// exempt from the SHA-1 finding.
	return string(c.RawIssuer) == string(c.RawSubject)
}

func isSHA1(a x509.SignatureAlgorithm) bool {
	switch a {
	case x509.SHA1WithRSA, x509.DSAWithSHA1, x509.ECDSAWithSHA1:
		return true
	}
	return false
}

func classifyVerifyError(err error) string {
	var he x509.HostnameError
	if errors.As(err, &he) {
		return "hostname"
	}
	var ie x509.CertificateInvalidError
	if errors.As(err, &ie) {
		if ie.Reason == x509.Expired {
			return "expired"
		}
	}
	var ae x509.UnknownAuthorityError
	if errors.As(err, &ae) {
		return "untrusted"
	}
	return "other"
}

func versionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	}
	return fmt.Sprintf("0x%04x", v)
}

// weakCipherIDs returns the cipher suites Go considers insecure (CBC-SHA,
// 3DES, RC4 where still present) for the weak-cipher probe.
func weakCipherIDs() []uint16 {
	var ids []uint16
	for _, c := range tls.InsecureCipherSuites() {
		ids = append(ids, c.ID)
	}
	return ids
}

// Audit probes several endpoints concurrently (bounded) and returns one
// result each, in the order given.
func Audit(ctx context.Context, eps []Endpoint) []Result {
	results := make([]Result, len(eps))
	sem := make(chan struct{}, 6)
	done := make(chan int, len(eps))
	for i, ep := range eps {
		go func(i int, ep Endpoint) {
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = Probe(ctx, ep)
			done <- i
		}(i, ep)
	}
	for range eps {
		<-done
	}
	return results
}

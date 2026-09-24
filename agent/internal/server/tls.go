package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LoadOrCreateCert loads the agent's TLS certificate, creating a
// self-signed ECDSA P-256 certificate (valid 10 years) when missing. The
// panel pins the public key, so no CA is involved.
func LoadOrCreateCert(certPath, keyPath string) (tls.Certificate, error) {
	if _, err := os.Stat(certPath); errors.Is(err, fs.ErrNotExist) {
		if err := createCert(certPath, keyPath); err != nil {
			return tls.Certificate{}, err
		}
	}
	return tls.LoadX509KeyPair(certPath, keyPath)
}

func createCert(certPath, keyPath string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "whacp-agent " + host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"whacp-agent"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	kder, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

// Pin returns the certificate's public-key pin in the form curl expects
// for CURLOPT_PINNEDPUBLICKEY: "sha256//" + base64(SHA-256(SPKI)).
func Pin(cert tls.Certificate) (string, error) {
	if len(cert.Certificate) == 0 {
		return "", errors.New("empty certificate")
	}
	c, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return "sha256//" + base64.StdEncoding.EncodeToString(sum[:]), nil
}

// ListenTLS listens on addr with TLS 1.3 only.
func ListenTLS(addr string, cert tls.Certificate) (net.Listener, error) {
	return tls.Listen("tcp", addr, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	})
}

// ClientPolicy limits remote callers by address and locks out addresses
// that present wrong tokens repeatedly.
type ClientPolicy struct {
	Allowed []*net.IPNet
	// MaxFailures within Window lock an address out for Window.
	MaxFailures int
	Window      time.Duration
	Now         func() time.Time

	mu       sync.Mutex
	failures map[string][]time.Time
}

// ParseCIDRs parses addresses or networks ("203.0.113.4", "10.0.0.0/8").
func ParseCIDRs(list []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, s := range list {
		s = strings.TrimSpace(s)
		if !strings.Contains(s, "/") {
			if ip := net.ParseIP(s); ip != nil {
				if ip.To4() != nil {
					s += "/32"
				} else {
					s += "/128"
				}
			}
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("invalid client address %q", s)
		}
		out = append(out, n)
	}
	return out, nil
}

func (p *ClientPolicy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Allows reports whether a remote address may connect at all.
func (p *ClientPolicy) Allows(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	ok := false
	for _, n := range p.Allowed {
		if n.Contains(ip) {
			ok = true
			break
		}
	}
	if !ok {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.recent(host)) < p.MaxFailures
}

// Failed records an authentication failure.
func (p *ClientPolicy) Failed(remote string) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failures == nil {
		p.failures = map[string][]time.Time{}
	}
	p.failures[host] = append(p.recent(host), p.now())
}

// recent returns failures within the window (caller holds mu).
func (p *ClientPolicy) recent(host string) []time.Time {
	var keep []time.Time
	for _, t := range p.failures[host] {
		if p.now().Sub(t) < p.Window {
			keep = append(keep, t)
		}
	}
	if p.failures != nil {
		p.failures[host] = keep
	}
	return keep
}

// Guard wraps a handler for the TCP listener: callers outside the
// allowlist, or locked out, get 403 before authentication.
func (p *ClientPolicy) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.Allows(r.RemoteAddr) {
			http.Error(w, `{"success":false,"error":{"code":"forbidden","message":"This address may not use the agent."}}`, http.StatusForbidden)
			return
		}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusUnauthorized {
			p.Failed(r.RemoteAddr)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

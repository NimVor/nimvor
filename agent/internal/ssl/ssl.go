// Package ssl inspects TLS certificates referenced by the server (read-only).
//
// Private keys are never read: only their existence and permissions are
// reported.
package ssl

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/filesystem"
)

// Certificate describes a leaf certificate.
type Certificate struct {
	File              string    `json:"file"`
	KeyFile           string    `json:"key_file,omitempty"`
	KeyExists         bool      `json:"key_exists"`
	KeyWorldReadable  bool      `json:"key_world_readable"`
	Subject           string    `json:"subject"`
	CommonName        string    `json:"common_name"`
	DNSNames          []string  `json:"dns_names"`
	Issuer            string    `json:"issuer"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
	DaysRemaining     int       `json:"days_remaining"`
	Expired           bool      `json:"expired"`
	SelfSigned        bool      `json:"self_signed"`
	LetsEncrypt       bool      `json:"lets_encrypt"`
	FingerprintSHA256 string    `json:"fingerprint_sha256"`
	ChainLength       int       `json:"chain_length"`
	Error             string    `json:"error,omitempty"`
}

// Status returns a coarse state used by the UI.
func (c Certificate) Status() string {
	switch {
	case c.Error != "":
		return "error"
	case c.Expired:
		return "expired"
	case c.DaysRemaining <= 14:
		return "expiring"
	default:
		return "valid"
	}
}

// Covers reports whether the certificate is valid for host (wildcards
// included).
func (c Certificate) Covers(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	names := c.DNSNames
	if len(names) == 0 && c.CommonName != "" {
		names = []string{c.CommonName}
	}
	for _, n := range names {
		n = strings.ToLower(n)
		if n == host {
			return true
		}
		if strings.HasPrefix(n, "*.") {
			suffix := n[1:]
			if strings.HasSuffix(host, suffix) && !strings.Contains(strings.TrimSuffix(host, suffix), ".") {
				return true
			}
		}
	}
	return false
}

// Inspect parses the certificate at certPath. keyPath may be empty.
func Inspect(root filesystem.Root, certPath, keyPath string, now time.Time) Certificate {
	c := Certificate{File: certPath, KeyFile: keyPath, DNSNames: []string{}}
	if keyPath != "" {
		if info, err := root.Stat(keyPath); err == nil {
			c.KeyExists = true
			c.KeyWorldReadable = info.Mode().Perm()&0o004 != 0
		}
	}
	data, err := root.ReadFile(certPath)
	if err != nil {
		c.Error = "certificate file cannot be read"
		return c
	}
	certs, err := parsePEMCerts(data)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	leaf := certs[0]
	c.ChainLength = len(certs)
	c.Subject = leaf.Subject.String()
	c.CommonName = leaf.Subject.CommonName
	c.DNSNames = append(c.DNSNames, leaf.DNSNames...)
	c.Issuer = leaf.Issuer.String()
	c.NotBefore = leaf.NotBefore.UTC()
	c.NotAfter = leaf.NotAfter.UTC()
	c.Expired = now.After(leaf.NotAfter)
	c.DaysRemaining = int(leaf.NotAfter.Sub(now).Hours() / 24)
	c.SelfSigned = bytes.Equal(leaf.RawIssuer, leaf.RawSubject)
	issuer := strings.ToLower(leaf.Issuer.String())
	c.LetsEncrypt = strings.Contains(issuer, "let's encrypt") || strings.Contains(strings.ToLower(strings.Join(leaf.Issuer.Organization, " ")), "let's encrypt")
	sum := sha256.Sum256(leaf.Raw)
	c.FingerprintSHA256 = strings.ToUpper(hex.EncodeToString(sum[:]))
	return c
}

func parsePEMCerts(data []byte) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue // never keep private key material, even if bundled
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("certificate is malformed")
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("no certificate found in file")
	}
	return certs, nil
}

// LECertificate is a Let's Encrypt lineage managed by certbot.
type LECertificate struct {
	Name          string      `json:"name"`
	Certificate   Certificate `json:"certificate"`
	RenewalConfig string      `json:"renewal_config,omitempty"`
	Authenticator string      `json:"authenticator,omitempty"`
	Installer     string      `json:"installer,omitempty"`
	Webroot       string      `json:"webroot,omitempty"`
}

// DiscoverLetsEncrypt lists certbot lineages under leDir (e.g. /etc/letsencrypt).
func DiscoverLetsEncrypt(root filesystem.Root, leDir string, now time.Time) []LECertificate {
	out := []LECertificate{}
	live := filepath.Join(leDir, "live")
	entries, err := root.ReadDir(live)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "README" {
			continue
		}
		dir := filepath.Join(live, name)
		if !root.IsDir(dir) {
			continue
		}
		le := LECertificate{
			Name:        name,
			Certificate: Inspect(root, filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem"), now),
		}
		renewal := filepath.Join(leDir, "renewal", name+".conf")
		if data, err := root.ReadFile(renewal); err == nil {
			le.RenewalConfig = renewal
			kv := parseRenewal(data)
			le.Authenticator = kv["authenticator"]
			le.Installer = kv["installer"]
			le.Webroot = kv["webroot_path"]
		}
		out = append(out, le)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func parseRenewal(data []byte) map[string]string {
	kv := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		// Only keep non-sensitive, descriptive keys.
		switch k {
		case "authenticator", "installer", "webroot_path", "server":
			if _, exists := kv[k]; !exists {
				kv[k] = strings.TrimRight(strings.TrimSpace(v), ",")
			}
		}
	}
	return kv
}

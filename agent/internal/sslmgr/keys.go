package sslmgr

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Private keys and certificate signing requests made on the server. The
// key is generated here, stored root-only (0600) and never returned: the
// CSR goes to a certificate authority, and the certificate it sends back is
// installed against the stored key. A self-signed certificate can be made
// from the same key (for testing or internal names).

// KeyTypes are the keys that can be generated.
var KeyTypes = []string{"rsa2048", "rsa3072", "rsa4096", "ecdsa256", "ecdsa384"}

// KeyMeta describes a stored key.
type KeyMeta struct {
	ID          string    `json:"id"`
	Names       []string  `json:"names"`
	Subject     string    `json:"subject"`
	KeyType     string    `json:"key_type"`
	Created     time.Time `json:"created"`
	PublicKey   string    `json:"public_key_sha256"` // fingerprint of the public key
	HasCSR      bool      `json:"has_csr"`
	SelfSigned  bool      `json:"self_signed"`
	CSR         string    `json:"csr,omitempty"`
	Certificate string    `json:"self_signed_certificate,omitempty"`
}

var keyIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,80}-[0-9]{14}$`)

func keysDir(env *host.Env) string { return filepath.Join(env.Cfg.StateDir, "keys") }

func keyDir(env *host.Env, id string) (string, error) {
	if !keyIDRe.MatchString(id) || strings.Contains(id, "..") {
		return "", actions.InvalidParams("key_id is not valid")
	}
	return filepath.Join(keysDir(env), id), nil
}

func generateKey(kind string) (crypto.Signer, error) {
	switch kind {
	case "rsa2048":
		return rsa.GenerateKey(rand.Reader, 2048)
	case "rsa3072":
		return rsa.GenerateKey(rand.Reader, 3072)
	case "rsa4096":
		return rsa.GenerateKey(rand.Reader, 4096)
	case "ecdsa256":
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "ecdsa384":
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	}
	return nil, actions.InvalidParams("key_type: one of " + strings.Join(KeyTypes, ", "))
}

// PublicKeyFingerprint is the SHA-256 of the DER public key (uppercase hex).
func PublicKeyFingerprint(pub any) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// CSRRequest is what a CSR (and optionally a self-signed certificate) is
// made from.
type CSRRequest struct {
	Names            []string `json:"names"` // first is the common name
	Organization     string   `json:"organization"`
	OrganizationUnit string   `json:"organizational_unit"`
	Locality         string   `json:"locality"`
	Province         string   `json:"province"`
	Country          string   `json:"country"`
	Email            string   `json:"email"`
	KeyType          string   `json:"key_type"`
	SelfSignedDays   int      `json:"self_signed_days"` // 0: CSR only
}

var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)

func (r CSRRequest) check() error {
	if len(r.Names) == 0 || len(r.Names) > 100 {
		return actions.InvalidParams("names: 1–100 domain names")
	}
	for _, n := range r.Names {
		// Wildcards are allowed in requests (the CA decides).
		if err := validate.Domain("names", strings.TrimPrefix(n, "*.")); err != nil {
			return host.Invalid(err)
		}
	}
	for field, v := range map[string]string{"organization": r.Organization, "organizational_unit": r.OrganizationUnit, "locality": r.Locality, "province": r.Province} {
		if !utf8.ValidString(v) || len(v) > 64 || strings.ContainsAny(v, "\x00\r\n") {
			return actions.InvalidParams(field + ": up to 64 characters")
		}
	}
	if r.Country != "" && !countryRe.MatchString(r.Country) {
		return actions.InvalidParams("country: two capital letters, e.g. DE")
	}
	if r.Email != "" && !emailRe.MatchString(r.Email) {
		return actions.InvalidParams("email: not a valid address")
	}
	if r.SelfSignedDays < 0 || r.SelfSignedDays > 825 {
		return actions.InvalidParams("self_signed_days: 0 (CSR only) to 825")
	}
	return nil
}

func (r CSRRequest) subject() pkix.Name {
	n := pkix.Name{CommonName: r.Names[0]}
	add := func(dst *[]string, v string) {
		if v = strings.TrimSpace(v); v != "" {
			*dst = []string{v}
		}
	}
	add(&n.Organization, r.Organization)
	add(&n.OrganizationalUnit, r.OrganizationUnit)
	add(&n.Locality, r.Locality)
	add(&n.Province, r.Province)
	add(&n.Country, r.Country)
	return n
}

// MakeCSR creates the key, the CSR and, if asked, a self-signed certificate.
func MakeCSR(r CSRRequest, now time.Time) (keyPEM, csrPEM, certPEM []byte, pub any, err error) {
	if err = r.check(); err != nil {
		return
	}
	if r.KeyType == "" {
		r.KeyType = "ecdsa256"
	}
	key, err := generateKey(r.KeyType)
	if err != nil {
		return
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	tpl := &x509.CertificateRequest{Subject: r.subject(), DNSNames: r.Names}
	if r.Email != "" {
		tpl.EmailAddresses = []string{r.Email}
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, tpl, key)
	if err != nil {
		return
	}
	csrPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	pub = key.Public()
	if r.SelfSignedDays > 0 {
		serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
		ct := &x509.Certificate{
			SerialNumber: serial, Subject: r.subject(), DNSNames: r.Names,
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Duration(r.SelfSignedDays) * 24 * time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
		}
		if _, ok := key.(*rsa.PrivateKey); ok {
			ct.KeyUsage |= x509.KeyUsageKeyEncipherment
		}
		var certDER []byte
		if certDER, err = x509.CreateCertificate(rand.Reader, ct, ct, key.Public(), key); err != nil {
			return
		}
		certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	}
	return
}

func readKeyMeta(env *host.Env, id string, withPEM bool) (KeyMeta, error) {
	dir, err := keyDir(env, id)
	if err != nil {
		return KeyMeta{}, err
	}
	b, err := env.Root.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return KeyMeta{}, actions.NotFound("That key does not exist.")
	}
	var m KeyMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return KeyMeta{}, actions.Failed("The key's details cannot be read.", err.Error())
	}
	m.ID = id
	if csr, err := env.Root.ReadFile(filepath.Join(dir, "csr.pem")); err == nil {
		m.HasCSR = true
		if withPEM {
			m.CSR = string(csr)
		}
	}
	if cert, err := env.Root.ReadFile(filepath.Join(dir, "selfsigned.pem")); err == nil {
		m.SelfSigned = true
		if withPEM {
			m.Certificate = string(cert)
		}
	}
	return m, nil
}

func readStoredKey(env *host.Env, id string) (string, error) {
	dir, err := keyDir(env, id)
	if err != nil {
		return "", err
	}
	b, err := env.Root.ReadFile(filepath.Join(dir, "privkey.pem"))
	if err != nil {
		return "", actions.NotFound("That key does not exist.")
	}
	return string(b), nil
}

// ---- certificate checks -------------------------------------------------------

// checkChain verifies that each certificate is signed by the next one.
func checkChain(fullPEM []byte) error {
	var certs []*x509.Certificate
	rest := fullPEM
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return errors.New("a certificate in the chain is malformed")
		}
		certs = append(certs, c)
	}
	for i := 0; i+1 < len(certs); i++ {
		if err := certs[i].CheckSignatureFrom(certs[i+1]); err != nil {
			return fmt.Errorf("certificate %d (%s) is not signed by the next one (%s): put the chain in order, from the issuer of the certificate upwards", i+1, certs[i].Subject.CommonName, certs[i+1].Subject.CommonName)
		}
	}
	return nil
}

// ---- actions ---------------------------------------------------------------------

func registerKeys(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "ssl.csr_create", Mutating: true, Timeout: 2 * time.Minute, Description: "Generate a private key (kept on the server, never returned) and a certificate signing request for it; optionally a self-signed certificate from the same key.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in CSRRequest
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		for i, n := range in.Names {
			in.Names[i] = strings.ToLower(strings.TrimSpace(n))
		}
		now := env.Now()
		keyPEM, csrPEM, certPEM, pub, err := MakeCSR(in, now)
		if err != nil {
			return nil, err
		}
		id := strings.TrimPrefix(in.Names[0], "*.") + "-" + now.UTC().Format("20060102150405")
		dir, err := keyDir(env, id)
		if err != nil {
			return nil, err
		}
		if env.Root.Exists(dir) {
			return nil, actions.Conflict("A key was created for this name a moment ago; try again in a second.")
		}
		kt := in.KeyType
		if kt == "" {
			kt = "ecdsa256"
		}
		meta := KeyMeta{Names: in.Names, Subject: in.subject().String(), KeyType: kt, Created: now.UTC(), PublicKey: PublicKeyFingerprint(pub)}
		mb, _ := json.MarshalIndent(meta, "", "  ")
		set := env.Changes("key-" + id)
		set.Mkdir(keysDir(env), 0o700, agentfs.RootOwner)
		set.Mkdir(dir, 0o700, agentfs.RootOwner)
		set.Write(filepath.Join(dir, "privkey.pem"), keyPEM, 0o600, agentfs.RootOwner)
		set.Write(filepath.Join(dir, "csr.pem"), csrPEM, 0o644, agentfs.RootOwner)
		set.Write(filepath.Join(dir, "meta.json"), mb, 0o600, agentfs.RootOwner)
		if certPEM != nil {
			set.Write(filepath.Join(dir, "selfsigned.pem"), certPEM, 0o644, agentfs.RootOwner)
		}
		if _, err := set.Apply(); err != nil {
			return nil, host.FailWithValidation("The key was not stored", err)
		}
		return readKeyMeta(env, id, true)
	}})

	r.Register(actions.Action{Name: "ssl.keys", Description: "Private keys made on the server, with whether each has a CSR and a self-signed certificate; the keys themselves are never returned.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		out := []KeyMeta{}
		entries, _ := env.Root.ReadDir(keysDir(env))
		for _, e := range entries {
			if !e.IsDir() || !keyIDRe.MatchString(e.Name()) {
				continue
			}
			if m, err := readKeyMeta(env, e.Name(), false); err == nil {
				out = append(out, m)
			}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
		return map[string]any{"keys": out, "key_types": KeyTypes}, nil
	}})

	r.Register(actions.Action{Name: "ssl.key", Description: "One key made on the server: its CSR and self-signed certificate (PEM) and details; never the private key.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		return readKeyMeta(env, in.ID, true)
	}})

	r.Register(actions.Action{Name: "ssl.key_delete", Mutating: true, Description: "Delete a stored key with its CSR and self-signed certificate. Certificates already installed keep their own copy of the key.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		dir, err := keyDir(env, in.ID)
		if err != nil {
			return nil, err
		}
		if !env.Root.Exists(filepath.Join(dir, "meta.json")) {
			return nil, actions.NotFound("That key does not exist.")
		}
		// Removed directly: a deleted private key is not kept in configuration backups.
		for _, f := range []string{"privkey.pem", "csr.pem", "selfsigned.pem", "meta.json"} {
			if _, err := env.Writer.Remove(filepath.Join(dir, f)); err != nil {
				return nil, actions.Failed("The key was not deleted completely.", err.Error())
			}
		}
		if real, err := env.Root.Path(dir); err == nil {
			os.Remove(real) // the now empty folder
		}
		return map[string]any{"deleted": in.ID}, nil
	}})
}

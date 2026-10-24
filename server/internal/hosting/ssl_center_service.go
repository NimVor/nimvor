package hosting

// SslCenterService — SSL/TLS for server administrators (SslCenterController +
// the read/act side of SslService/sslmgr): the certificate store, the hosts
// Nginx serves, the private keys and CSRs made on this server, the panel's
// and the mail server's certificates, the ACME account and the server-wide
// TLS configuration.
//
// This is an administrator area: the handler refuses a scoped account
// outright (the PHP's denied()), and every mutation is audited against the
// server. Nothing here ever returns a private key — the agent keeps them, and
// the only material that travels back is a CSR (which is public by design).

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// sslCertTimeout is the agent timeout for the slow certbot-backed actions
// (the PHP uses 60 s for reads and 120 s for the writes that call certbot).
const (
	sslCenterTimeout = 60 * time.Second
	sslCenterSlow    = 120 * time.Second
)

// SslCertDetail is one certificate as the agent inspects it (ssl.Certificate
// in the agent; no private key).
type SslCertDetail struct {
	File              string    `json:"file"`
	KeyFile           string    `json:"key_file"`
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
	Error             string    `json:"error"`
}

// IssuerName is the O= or CN= part of an issuer DN, the way the panel shows
// it.
func (c SslCertDetail) IssuerName() string {
	if m := sslIssuerORe.FindStringSubmatch(c.Issuer); m != nil {
		return m[1]
	}
	if m := sslIssuerCNRe.FindStringSubmatch(c.Issuer); m != nil {
		return m[1]
	}
	return c.Issuer
}

// SslHost is one host name Nginx serves (ssl.store's hosts).
type SslHost struct {
	Name             string         `json:"name"`
	File             string         `json:"file"`
	Enabled          bool           `json:"enabled"`
	HTTPS            bool           `json:"https"`
	RedirectsToHTTPS bool           `json:"redirects_to_https"`
	WhaCP            bool           `json:"whacp"`
	Covered          bool           `json:"covered"`
	Certificate      *SslCertDetail `json:"certificate"`
}

// SslCertEntry is one certificate in the store (ssl.store's certificates).
type SslCertEntry struct {
	Source      string        `json:"source"` // letsencrypt, whacp, other
	Name        string        `json:"name"`
	Certificate SslCertDetail `json:"certificate"`
	UsedBy      []string      `json:"used_by"`
	Deletable   bool          `json:"deletable"`
}

// SslStore is the agent's ssl.store reply.
type SslStore struct {
	Hosts        []SslHost      `json:"hosts"`
	Certificates []SslCertEntry `json:"certificates"`
}

// SslKeyMeta describes a private key made on this server (ssl.keys).
type SslKeyMeta struct {
	ID              string    `json:"id"`
	Names           []string  `json:"names"`
	Subject         string    `json:"subject"`
	KeyType         string    `json:"key_type"`
	Created         time.Time `json:"created"`
	PublicKeySHA256 string    `json:"public_key_sha256"`
	HasCSR          bool      `json:"has_csr"`
	SelfSigned      bool      `json:"self_signed"`
	CSR             string    `json:"csr"`
	SelfSignedCert  string    `json:"self_signed_certificate"`
}

// SslKeyList is the ssl.keys reply.
type SslKeyList struct {
	Keys     []SslKeyMeta `json:"keys"`
	KeyTypes []string     `json:"key_types"`
}

// SslCSRInput is the CSR form (ssl.csr_create).
type SslCSRInput struct {
	Names            []string `json:"names"`
	Organization     string   `json:"organization"`
	OrganizationUnit string   `json:"organizational_unit"`
	Locality         string   `json:"locality"`
	Province         string   `json:"province"`
	Country          string   `json:"country"`
	Email            string   `json:"email"`
	KeyType          string   `json:"key_type"`
	SelfSignedDays   int      `json:"self_signed_days"`
}

// SslServiceCert is one of WhaCP's own services with its certificate.
type SslServiceCert struct {
	Certificate       SslCertDetail `json:"certificate"`
	FollowsTLSProfile bool          `json:"follows_tls_profile"`
}

// SslLECert is one Let's Encrypt certificate a service could use.
type SslLECert struct {
	Name          string   `json:"name"`
	Names         []string `json:"names"`
	DaysRemaining int      `json:"days_remaining"`
	// CoversHostname is set by the services page so the picker preselects
	// the certificate that already answers for the server's host name.
	CoversHostname bool `json:"-"`
}

// SslMailTLS is the mail server's TLS profile.
type SslMailTLS struct {
	Protocols string `json:"protocols"`
	Ciphers   string `json:"ciphers"`
}

// SslServices is the agent's ssl.services reply.
type SslServices struct {
	Hostname    string          `json:"hostname"`
	Panel       *SslServiceCert `json:"panel"`
	Mail        *SslServiceCert `json:"mail"`
	LetsEncrypt []SslLECert     `json:"lets_encrypt"`
	MailTLS     *SslMailTLS     `json:"mail_tls"`
	// CertbotInstalled says whether the agent has certbot, which decides
	// whether issuing anything is possible.
	CertbotInstalled bool `json:"certbot_installed"`
}

// SslProvider is one ACME certificate authority.
type SslProvider struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	RequiresEAB bool   `json:"requires_eab"`
	TermsURL    string `json:"terms_url"`
}

// SslAccount is the agent's ssl.account reply for one provider.
type SslAccount struct {
	Providers        []SslProvider `json:"providers"`
	Provider         string        `json:"provider"`
	Label            string        `json:"label"`
	CertbotInstalled bool          `json:"certbot_installed"`
	Registered       bool          `json:"registered"`
	Email            string        `json:"email"`
	AccountURI       string        `json:"account_uri"`
	RequiresEAB      bool          `json:"requires_eab"`
	TermsURL         string        `json:"terms_url"`
}

// SslAccountInput is the registration form (ssl.account_register).
type SslAccountInput struct {
	Provider   string `json:"provider"`
	Email      string `json:"email"`
	AgreeTOS   bool   `json:"agree_tos"`
	Staging    bool   `json:"staging"`
	EABKID     string `json:"eab_kid"`
	EABHMACKey string `json:"eab_hmac_key"`
}

// SslAccountResult is what registering returns.
type SslAccountResult struct {
	Updated bool `json:"updated"`
}

// SslTLSProfile is the server-wide TLS profile (tlsprofile.Profile).
type SslTLSProfile struct {
	Protocols      string `json:"protocols"`
	Ciphers        string `json:"ciphers"`
	SessionTickets bool   `json:"session_tickets"`
}

// SslTLS is the agent's ssl.tls_get reply.
type SslTLS struct {
	Profile      SslTLSProfile `json:"profile"`
	Snippet      string        `json:"snippet"`
	Path         string        `json:"path"`
	Vhosts       int           `json:"vhosts"`
	LegacyVhosts int           `json:"legacy_vhosts"`
}

// SslHostnameIssue is the reply to asking for the host name's certificate.
type SslHostnameIssue struct {
	JobID    string `json:"job_id"`
	Hostname string `json:"hostname"`
}

var (
	sslIssuerORe  = regexp.MustCompile(`O=([^,]+)`)
	sslIssuerCNRe = regexp.MustCompile(`CN=([^,]+)`)
)

// SslCenterService is the administrator-facing SSL/TLS service for one server.
type SslCenterService struct {
	agent    AgentCaller
	audit    Auditor
	serverID int64
}

// NewSslCenterService builds the service.
func NewSslCenterService(ag AgentCaller, audit Auditor, serverID int64) *SslCenterService {
	return &SslCenterService{agent: ag, audit: audit, serverID: serverID}
}

// SslCenterAuditTarget is the server id as the audit target.
func (s *SslCenterService) target() string { return fmt.Sprintf("%d", s.serverID) }

func (s *SslCenterService) record(ctx context.Context, action, target, result string, details map[string]any) {
	if s.audit == nil {
		return
	}
	s.audit(ctx, action, "server", target, result, details)
}

// recordOK is the success path; failures are recorded by the handlers, which
// are the only place that knows the failure's target name.
func (s *SslCenterService) recordOK(ctx context.Context, action, target string, details map[string]any) {
	s.record(ctx, action, target, "success", details)
}

// Store reads every certificate and host name (ssl.store).
func (s *SslCenterService) Store(ctx context.Context) (*SslStore, error) {
	res, err := s.agent.Call(ctx, "ssl.store", map[string]any{}, sslCenterTimeout)
	if err != nil {
		return nil, err
	}
	out := &SslStore{Hosts: []SslHost{}, Certificates: []SslCertEntry{}}
	if err := json.Unmarshal(res, out); err != nil {
		return nil, err
	}
	if out.Hosts == nil {
		out.Hosts = []SslHost{}
	}
	if out.Certificates == nil {
		out.Certificates = []SslCertEntry{}
	}
	return out, nil
}

// RenewalStatus reports whether certbot's renewal timer is running
// (ssl.renewal_status).
func (s *SslCenterService) RenewalStatus(ctx context.Context) (*RenewalStatus, error) {
	res, err := s.agent.Call(ctx, "ssl.renewal_status", map[string]any{}, sslCenterTimeout)
	if err != nil {
		return nil, err
	}
	st := &RenewalStatus{}
	if err := json.Unmarshal(res, st); err != nil {
		return nil, err
	}
	return st, nil
}

// EnableRenewal turns on certbot's renewal timer (ssl.renewal_enable) and
// returns the method that did it.
func (s *SslCenterService) EnableRenewal(ctx context.Context) (string, error) {
	res, err := s.agent.Call(ctx, "ssl.renewal_enable", map[string]any{}, sslCenterTimeout)
	if err != nil {
		return "", err
	}
	st := &RenewalStatus{}
	if err := json.Unmarshal(res, st); err != nil {
		return "", err
	}
	s.recordOK(ctx, "ssl.renewal_enable", "certbot", map[string]any{"method": st.Method})
	return st.Method, nil
}

// RenewalLog is certbot's recent renewal output (ssl.renewal_log).
func (s *SslCenterService) RenewalLog(ctx context.Context) ([]string, error) {
	res, err := s.agent.Call(ctx, "ssl.renewal_log", map[string]any{}, sslCenterTimeout)
	if err != nil {
		return nil, err
	}
	var o struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Lines == nil {
		o.Lines = []string{}
	}
	return o.Lines, nil
}

// DeleteCertificate deletes a Let's Encrypt or WhaCP certificate whose name
// no Nginx configuration uses (ssl.delete / ssl.custom_delete).
func (s *SslCenterService) DeleteCertificate(ctx context.Context, source, name string) error {
	switch source {
	case "letsencrypt":
		if _, err := s.agent.Call(ctx, "ssl.delete",
			map[string]any{"cert_name": name}, sslCenterTimeout); err != nil {
			return err
		}
	case "whacp":
		if _, err := s.agent.Call(ctx, "ssl.custom_delete",
			map[string]any{"name": name}, sslCenterTimeout); err != nil {
			return err
		}
	default:
		return &UserError{What: "Only Let's Encrypt and WhaCP certificates can be deleted here."}
	}
	s.recordOK(ctx, "ssl.certificate_delete", "ssl",
		map[string]any{"source": source, "name": name})
	return nil
}

// Keys lists the private keys made on this server (ssl.keys).
func (s *SslCenterService) Keys(ctx context.Context) (*SslKeyList, error) {
	res, err := s.agent.Call(ctx, "ssl.keys", map[string]any{}, sslCenterTimeout)
	if err != nil {
		return nil, err
	}
	out := &SslKeyList{Keys: []SslKeyMeta{}, KeyTypes: []string{}}
	if err := json.Unmarshal(res, out); err != nil {
		return nil, err
	}
	if out.Keys == nil {
		out.Keys = []SslKeyMeta{}
	}
	if out.KeyTypes == nil {
		out.KeyTypes = []string{}
	}
	return out, nil
}

// Key reads one key with its CSR (ssl.key). A key the agent does not know
// yields a nil key rather than an error, like the PHP's empty lookup.
func (s *SslCenterService) Key(ctx context.Context, id string) (*SslKeyMeta, error) {
	res, err := s.agent.Call(ctx, "ssl.key", map[string]any{"id": id}, sslCenterTimeout)
	if err != nil {
		return nil, err
	}
	var k SslKeyMeta
	if err := json.Unmarshal(res, &k); err != nil {
		return nil, err
	}
	if k.ID == "" {
		return nil, nil
	}
	return &k, nil
}

// CreateCSR makes a key and a CSR (ssl.csr_create), returning its id.
func (s *SslCenterService) CreateCSR(ctx context.Context, in SslCSRInput) (string, error) {
	res, err := s.agent.Call(ctx, "ssl.csr_create", in, sslCenterSlow)
	if err != nil {
		return "", err
	}
	var o struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return "", err
	}
	s.recordOK(ctx, "ssl.csr_create", "ssl", map[string]any{
		"id": o.ID, "names": in.Names, "key_type": in.KeyType,
		"self_signed": in.SelfSignedDays > 0,
	})
	return o.ID, nil
}

// DeleteKey removes a stored key and its CSR (ssl.key_delete).
func (s *SslCenterService) DeleteKey(ctx context.Context, id string) error {
	if _, err := s.agent.Call(ctx, "ssl.key_delete", map[string]any{"id": id}, sslCenterTimeout); err != nil {
		return err
	}
	s.recordOK(ctx, "ssl.key_delete", "ssl", map[string]any{"id": id})
	return nil
}

// Services reads the panel's, the mail server's and the host name's
// certificates (ssl.services).
func (s *SslCenterService) Services(ctx context.Context) (*SslServices, error) {
	res, err := s.agent.Call(ctx, "ssl.services", map[string]any{}, sslCenterTimeout)
	if err != nil {
		return nil, err
	}
	out := &SslServices{LetsEncrypt: []SslLECert{}}
	if err := json.Unmarshal(res, out); err != nil {
		return nil, err
	}
	if out.LetsEncrypt == nil {
		out.LetsEncrypt = []SslLECert{}
	}
	return out, nil
}

// Account reads the ACME account for a provider (ssl.account).
func (s *SslCenterService) Account(ctx context.Context, provider string) (*SslAccount, error) {
	res, err := s.agent.Call(ctx, "ssl.account",
		map[string]any{"provider": provider}, sslCenterTimeout)
	if err != nil {
		return nil, err
	}
	out := &SslAccount{Providers: []SslProvider{}}
	if err := json.Unmarshal(res, out); err != nil {
		return nil, err
	}
	if out.Providers == nil {
		out.Providers = []SslProvider{}
	}
	return out, nil
}

// RegisterAccount registers the ACME account for a provider, or updates its
// contact email (ssl.account_register). The EAB HMAC key is a secret: it is
// never audited.
func (s *SslCenterService) RegisterAccount(ctx context.Context, in SslAccountInput) (*SslAccountResult, error) {
	res, err := s.agent.Call(ctx, "ssl.account_register", in, sslCenterSlow)
	if err != nil {
		s.record(ctx, "ssl.account_register", in.Provider, "failure",
			map[string]any{"provider": in.Provider, "email": in.Email, "error": agentMessage(err)})
		return nil, err
	}
	out := &SslAccountResult{}
	if err := json.Unmarshal(res, out); err != nil {
		return nil, err
	}
	s.recordOK(ctx, "ssl.account_register", in.Provider,
		map[string]any{"provider": in.Provider, "email": in.Email, "updated": out.Updated})
	return out, nil
}

// ProviderLabel is the display name the panel uses for an ACME provider.
func ProviderLabel(id string) string {
	switch id {
	case "letsencrypt":
		return "Let's Encrypt"
	case "zerossl":
		return "ZeroSSL"
	case "buypass":
		return "Buypass Go"
	}
	return id
}

// SetServiceCert points the panel or the mail server at one of the Let's
// Encrypt certificates, or (empty name) returns it to the previous one
// (ssl.service_cert).
func (s *SslCenterService) SetServiceCert(ctx context.Context, service, certName string) error {
	if _, err := s.agent.Call(ctx, "ssl.service_cert",
		map[string]any{"service": service, "cert_name": certName}, sslCenterSlow); err != nil {
		s.record(ctx, "ssl.service_cert", service, "failure",
			map[string]any{"cert_name": certName, "error": agentMessage(err)})
		return err
	}
	s.recordOK(ctx, "ssl.service_cert", service, map[string]any{"cert_name": certName})
	return nil
}

// SetMailTLS applies the mail server's TLS profile (ssl.mail_tls).
func (s *SslCenterService) SetMailTLS(ctx context.Context, protocols, ciphers string) error {
	profile := map[string]any{"protocols": protocols, "ciphers": ciphers}
	if _, err := s.agent.Call(ctx, "ssl.mail_tls", profile, sslCenterSlow); err != nil {
		details := map[string]any{"protocols": protocols, "ciphers": ciphers, "error": agentMessage(err)}
		s.record(ctx, "ssl.mail_tls", "mail", "failure", details)
		return err
	}
	s.recordOK(ctx, "ssl.mail_tls", "mail", profile)
	return nil
}

// IssueHostname asks Let's Encrypt for the server's host name as a job
// (ssl.hostname_issue).
func (s *SslCenterService) IssueHostname(ctx context.Context, email, provider string) (*SslHostnameIssue, error) {
	res, err := s.agent.Call(ctx, "ssl.hostname_issue",
		map[string]any{"email": email, "provider": provider}, sslCenterTimeout)
	if err != nil {
		s.record(ctx, "ssl.hostname_issue", "hostname", "failure",
			map[string]any{"error": agentMessage(err)})
		return nil, err
	}
	out := &SslHostnameIssue{}
	if err := json.Unmarshal(res, out); err != nil {
		return nil, err
	}
	s.recordOK(ctx, "ssl.hostname_issue", "hostname", map[string]any{"hostname": out.Hostname})
	return out, nil
}

// TLS reads the server-wide TLS configuration (ssl.tls_get).
func (s *SslCenterService) TLS(ctx context.Context) (*SslTLS, error) {
	res, err := s.agent.Call(ctx, "ssl.tls_get", map[string]any{}, sslCenterTimeout)
	if err != nil {
		return nil, err
	}
	out := &SslTLS{}
	if err := json.Unmarshal(res, out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetTLS applies a TLS profile to every WhaCP virtual host (ssl.tls_set) and
// returns how many files were migrated to the shared snippet.
func (s *SslCenterService) SetTLS(ctx context.Context, p SslTLSProfile) (int, error) {
	res, err := s.agent.Call(ctx, "ssl.tls_set", p, sslCenterSlow)
	if err != nil {
		s.record(ctx, "ssl.tls", "nginx", "failure", map[string]any{
			"protocols": p.Protocols, "ciphers": p.Ciphers,
			"session_tickets": p.SessionTickets, "error": agentMessage(err),
		})
		return 0, err
	}
	s.recordOK(ctx, "ssl.tls", "nginx", map[string]any{
		"protocols": p.Protocols, "ciphers": p.Ciphers, "session_tickets": p.SessionTickets,
	})
	var o struct {
		Migrated int `json:"migrated_vhosts"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return 0, err
	}
	return o.Migrated, nil
}

// SslTLSOptionLabel is the label the panel shows for a protocol or cipher
// choice.
func SslTLSOptionLabel(kind, value string) string {
	if kind == "protocols" {
		switch value {
		case "tls12":
			return "TLS 1.2 and 1.3"
		case "tls13":
			return "TLS 1.3 only"
		}
		return value
	}
	switch value {
	case "default":
		return "Nginx default"
	case "intermediate":
		return "Modern ciphers only (Mozilla \"intermediate\")"
	}
	return value
}

// SslTrimmedNames lowercases and de-duplicates a comma/space separated list
// of names (the CSR form).
func SslTrimmedNames(raw string) []string {
	out := []string{}
	for _, n := range regexp.MustCompile(`[\s,]+`).Split(strings.ToLower(raw), -1) {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

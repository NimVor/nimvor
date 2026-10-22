package hosting

// SslService — HTTPS for websites (SslService.php). The certificate is
// obtained/validated on the agent, then the website is switched through
// WebsiteService.patch (which regenerates the Nginx config and reloads only
// after nginx -t, and restores the previous state on failure).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// sslACMETimeout is the agent timeout for Let's Encrypt calls (certbot can
// take a while), like the PHP's 320 s.
const sslACMETimeout = 320 * time.Second

// CertInfo is the agent's ssl.inspect reply (the leaf certificate).
type CertInfo struct {
	DNSNames      []string  `json:"dns_names"`
	Issuer        string    `json:"issuer"`
	NotAfter      time.Time `json:"not_after"`
	DaysRemaining int       `json:"days_remaining"`
	Expired       bool      `json:"expired"`
	Error         string    `json:"error"`
}

// RenewalStatus is the agent's ssl.renewal_status reply.
type RenewalStatus struct {
	CertbotInstalled bool   `json:"certbot_installed"`
	TimerActive      bool   `json:"timer_active"`
	Method           string `json:"method"`
}

// sslFilePair is the {cert_file, key_file} pair the ssl.issue/ssl.custom
// replies carry.
type sslFilePair struct {
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
}

// SslService wraps the ssl.* agent actions around WebsiteService.patch.
type SslService struct {
	agent    AgentCaller
	wsvc     *WebsiteService
	cfg      *settings.Repository
	audit    Auditor
	serverID int64
}

// NewSslService builds the service.
func NewSslService(agent AgentCaller, wsvc *WebsiteService, cfg *settings.Repository, audit Auditor, serverID int64) *SslService {
	return &SslService{agent: agent, wsvc: wsvc, cfg: cfg, audit: audit, serverID: serverID}
}

func (s *SslService) recordAudit(ctx context.Context, action, result string, siteID int64, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, "website", fmt.Sprintf("%d", siteID), result, details)
	}
}

// provider is the configured ACME certificate authority (letsencrypt by
// default).
func (s *SslService) provider(ctx context.Context) (string, error) {
	v, err := s.cfg.String(ctx, "acme_provider", "")
	if err != nil {
		return "", err
	}
	if v == "" {
		v = "letsencrypt"
	}
	return v, nil
}

// Issue obtains a Let's Encrypt certificate and switches the website to
// HTTPS (SslService::issue).
func (s *SslService) Issue(ctx context.Context, site *Website, email string, includeAliases, staging bool) error {
	if !site.Enabled {
		return &UserError{What: "Enable the website first.", Why: "Let's Encrypt must reach it over HTTP to verify the domain."}
	}
	if email != "" && !emailRe.MatchString(email) {
		return &UserError{What: "Enter a valid email address for expiry notices, or leave it empty."}
	}
	provider, err := s.provider(ctx)
	if err != nil {
		return err
	}
	names := []string{site.Domain}
	if includeAliases {
		names = append(names, site.Aliases...)
	}
	res, err := s.agent.Call(ctx, "ssl.issue", map[string]any{
		"cert_name": site.Domain, "names": names, "email": email, "staging": staging, "provider": provider,
	}, sslACMETimeout)
	if err != nil {
		s.recordAudit(ctx, "ssl.issue", "failure", site.ID, map[string]any{
			"domain": site.Domain, "error": agentMessage(err),
		})
		return err
	}
	files, err := sslFiles(res)
	if err != nil {
		return err
	}
	if err := s.wsvc.patch(ctx, site, map[string]any{
		"https": true, "force_https": true, "ssl_mode": "letsencrypt",
		"cert_file": files.CertFile, "key_file": files.KeyFile,
	}, "ssl.enable"); err != nil {
		return err
	}
	s.recordAudit(ctx, "ssl.issue", "success", site.ID, map[string]any{
		"domain": site.Domain, "names": names, "staging": staging,
	})
	return nil
}

// InstallCustom validates and installs an uploaded certificate (SslService
// ::installCustom).
func (s *SslService) InstallCustom(ctx context.Context, site *Website, cert, key, chain string) error {
	if strings.TrimSpace(cert) == "" || strings.TrimSpace(key) == "" {
		return &UserError{What: "Paste both the certificate and its private key."}
	}
	res, err := s.agent.Call(ctx, "ssl.custom", map[string]any{
		"name": site.Domain, "certificate": cert, "private_key": key, "chain": chain, "domain": site.Domain,
	})
	if err != nil {
		return err
	}
	files, err := sslFiles(res)
	if err != nil {
		return err
	}
	return s.wsvc.patch(ctx, site, map[string]any{
		"https": true, "ssl_mode": "custom",
		"cert_file": files.CertFile, "key_file": files.KeyFile,
	}, "ssl.custom")
}

// Renew forces a Let's Encrypt renewal (SslService::renew).
func (s *SslService) Renew(ctx context.Context, site *Website) error {
	if site.SSLMode != "letsencrypt" {
		return &UserError{What: "Only Let's Encrypt certificates can be renewed automatically.", Why: "Upload a new certificate instead."}
	}
	if _, err := s.agent.Call(ctx, "ssl.renew", map[string]any{"cert_name": site.Domain}, sslACMETimeout); err != nil {
		return err
	}
	s.recordAudit(ctx, "ssl.renew", "success", site.ID, map[string]any{"domain": site.Domain})
	return nil
}

// Disable turns HTTPS off (SslService::disable); optionally the Let's
// Encrypt certificate is deleted from the server too.
func (s *SslService) Disable(ctx context.Context, site *Website, deleteCertificate bool) error {
	mode := site.SSLMode
	if err := s.wsvc.patch(ctx, site, map[string]any{
		"https": false, "force_https": false, "hsts": false,
		"ssl_mode": "none", "cert_file": "", "key_file": "",
	}, "ssl.disable"); err != nil {
		return err
	}
	if deleteCertificate && mode == "letsencrypt" {
		if _, err := s.agent.Call(ctx, "ssl.delete", map[string]any{"cert_name": site.Domain}); err != nil {
			return err
		}
	}
	return nil
}

// SecurityHeaders toggles the recommended security headers (SslService
// ::securityHeaders).
func (s *SslService) SecurityHeaders(ctx context.Context, site *Website, on bool) error {
	return s.wsvc.patch(ctx, site, map[string]any{"security_headers": on}, "website.security_headers")
}

// Options saves the HTTPS redirect/HSTS options (SslService::options).
func (s *SslService) Options(ctx context.Context, site *Website, force, hsts bool) error {
	if !site.HTTPS {
		return &UserError{What: "Enable HTTPS first."}
	}
	if hsts && !force {
		hsts = false
	}
	return s.wsvc.patch(ctx, site, map[string]any{"force_https": force, "hsts": hsts}, "ssl.options")
}

// Certificate inspects the installed certificate (nil when the website has
// none, or the agent cannot read it) (SslService::certificate).
func (s *SslService) Certificate(ctx context.Context, site *Website) *CertInfo {
	if !site.HTTPS || site.CertFile == "" {
		return nil
	}
	res, err := s.agent.Call(ctx, "ssl.inspect", map[string]any{
		"cert_file": site.CertFile, "key_file": site.KeyFile,
	})
	if err != nil {
		return nil
	}
	var c CertInfo
	if err := json.Unmarshal(res, &c); err != nil {
		return nil
	}
	return &c
}

// RenewalStatus reports whether certbot renews on its own (nil when the
// agent is unreachable) (SslService::renewalStatus).
func (s *SslService) RenewalStatus(ctx context.Context) *RenewalStatus {
	res, err := s.agent.Call(ctx, "ssl.renewal_status", nil)
	if err != nil {
		return nil
	}
	var st RenewalStatus
	if err := json.Unmarshal(res, &st); err != nil {
		return nil
	}
	return &st
}

// sslFiles decodes the {cert_file, key_file} pair of an ssl.issue /
// ssl.custom reply.
func sslFiles(res json.RawMessage) (sslFilePair, error) {
	var out sslFilePair
	if err := json.Unmarshal(res, &out); err != nil {
		return out, &UserError{What: "The agent replied with an unexpected certificate location."}
	}
	return out, nil
}

// agentMessage is the user-facing part of an agent failure (the agent's own
// message when it carries one, the raw error otherwise).
func agentMessage(err error) string {
	if err == nil {
		return ""
	}
	var ae *agent.Error
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	return err.Error()
}

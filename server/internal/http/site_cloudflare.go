package http

// This file ports the per-website Cloudflare area (SiteCloudflareController +
// CloudflareSiteService): a customer connects their own Cloudflare API token
// (scoped to their own zone) and manages the free-plan settings of just
// their website's zone. The panel talks to Cloudflare's API directly (never
// the agent); the token is stored encrypted, per website, and is used only
// as a Bearer header. All routes are websites.manage:
//   GET  /sites/{id}/cloudflare
//   POST /sites/{id}/cloudflare/connect | disconnect | ssl |
//        always-https | proxy | purge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

const cfAPI = "https://api.cloudflare.com/client/v4"

// cfSslModes mirrors CloudflareService::SSL_MODES.
var cfSslModes = [][2]string{
	{"off", "Off"},
	{"flexible", "Flexible"},
	{"full", "Full"},
	{"strict", "Full (strict)"},
}

var cfTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{20,120}$`)
var cfIDRe = regexp.MustCompile(`^[a-f0-9]{32}$`)

// cfTransport is the Cloudflare HTTP hop (injectable for tests — the PHP's
// setTransport).
type cfTransport func(ctx context.Context, method, u string, body any, token string) (int, []byte, error)

// defaultCFTransport is the real hop (20 s total, JSON in/out).
func defaultCFTransport(ctx context.Context, method, u string, body any, token string) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return 0, nil, err
	}
	return res.StatusCode, raw, nil
}

// cfUserFacing is a Cloudflare error the customer can act on.
type cfUserFacing struct{ msg string }

func (e *cfUserFacing) Error() string { return e.msg }

// cfCall performs one API call and returns the "result" payload (raw JSON —
// an object or a list; CloudflareSiteService::call).
func (s *Server) cfCall(ctx context.Context, token, method, path string, body any) (json.RawMessage, error) {
	if token == "" {
		return nil, &cfUserFacing{msg: "Cloudflare is not connected for this website."}
	}
	do := s.cfDo
	if do == nil {
		do = defaultCFTransport
	}
	status, raw, err := do(ctx, method, cfAPI+path, body, token)
	if err != nil {
		return nil, &cfUserFacing{msg: "Cloudflare: request failed (the network call could not be made)."}
	}
	var payload struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || status < 200 || status >= 300 || !payload.Success {
		if len(payload.Errors) > 0 && payload.Errors[0].Message != "" {
			return nil, &cfUserFacing{msg: "Cloudflare: " + payload.Errors[0].Message}
		}
		return nil, &cfUserFacing{msg: fmt.Sprintf("Cloudflare: request failed (HTTP %d).", status)}
	}
	return payload.Result, nil
}

// cfTokenOf returns the decrypted stored token ("" when absent).
func (s *Server) cfTokenOf(ctx context.Context, websiteID int64) (string, error) {
	row, err := hosting.FindCloudflareSite(ctx, s.db, websiteID)
	if err != nil || row == nil {
		return "", err
	}
	tok, err := s.crypto.Decrypt(row.TokenEnc)
	if err != nil {
		return "", err
	}
	return string(tok), nil
}

// cfStatusView is the view's $status (computed).
type cfStatusView struct {
	ZoneName    string
	VerifiedAt  string
	SSL         string
	AlwaysHTTPS bool
	NextAH      int // 1 when the switch would turn the setting ON
	Records     []cfRecordView
	Error       string
}

// cfRecordView is one proxiable DNS record row.
type cfRecordView struct {
	ID      string
	Type    string
	Name    string
	Content string
	Proxied bool
	NextOn  int // 1 when the switch would turn the proxy ON
}

// cfStatus is CloudflareSiteService::status (nil when not connected).
func (s *Server) cfStatus(ctx context.Context, site *hosting.Website) (*cfStatusView, error) {
	row, err := hosting.FindCloudflareSite(ctx, s.db, site.ID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	v := &cfStatusView{ZoneName: row.ZoneName, VerifiedAt: row.VerifiedAt}
	token, err := s.cfTokenOf(ctx, site.ID)
	if err != nil {
		v.Error = "The stored Cloudflare token could not be read."
		return v, nil
	}
	fail := func(err error) (*cfStatusView, error) {
		if ufe, ok := err.(*cfUserFacing); ok {
			v.Error = ufe.msg
		} else {
			v.Error = "Cloudflare: request failed."
		}
		return v, nil
	}
	res, err := s.cfCall(ctx, token, "GET", "/zones/"+row.ZoneID+"/settings/ssl", nil)
	if err != nil {
		return fail(err)
	}
	var set struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(res, &set); err == nil {
		v.SSL = set.Value
	}
	res, err = s.cfCall(ctx, token, "GET", "/zones/"+row.ZoneID+"/settings/always_use_https", nil)
	if err != nil {
		return fail(err)
	}
	if err := json.Unmarshal(res, &set); err == nil {
		v.AlwaysHTTPS = set.Value == "on"
	}
	domain := strings.ToLower(site.Domain)
	res, err = s.cfCall(ctx, token, "GET", "/zones/"+row.ZoneID+"/dns_records?per_page=100", nil)
	if err != nil {
		return fail(err)
	}
	var items []struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Name      string `json:"name"`
		Content   string `json:"content"`
		Proxiable bool   `json:"proxiable"`
		Proxied   bool   `json:"proxied"`
	}
	if err := json.Unmarshal(res, &items); err == nil {
		for _, it := range items {
			name := strings.ToLower(it.Name)
			if (name == domain || name == "www."+domain) && it.Proxiable {
				rec := cfRecordView{ID: it.ID, Type: it.Type, Name: it.Name, Content: it.Content, Proxied: it.Proxied}
				if !rec.Proxied {
					rec.NextOn = 1
				}
				v.Records = append(v.Records, rec)
			}
		}
	}
	if v.AlwaysHTTPS {
		v.NextAH = 0
	} else {
		v.NextAH = 1
	}
	return v, nil
}

// cfConnect is CloudflareSiteService::connect: verify the token, find the
// zone matching the website's domain (the longest match wins), store both.
// It returns the stored zone name (for the audit).
func (s *Server) cfConnect(ctx context.Context, site *hosting.Website, token string) (string, error) {
	token = strings.TrimSpace(token)
	if !cfTokenRe.MatchString(token) {
		return "", &cfUserFacing{msg: "That does not look like a Cloudflare API token."}
	}
	if _, err := s.cfCall(ctx, token, "GET", "/user/tokens/verify", nil); err != nil {
		return "", err
	}
	domain := strings.ToLower(site.Domain)
	var bestID, bestName string
	bestLen := -1
	res, err := s.cfCall(ctx, token, "GET", "/zones?per_page=50", nil)
	if err != nil {
		return "", err
	}
	var zones []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(res, &zones); err == nil {
		for _, z := range zones {
			name := strings.ToLower(z.Name)
			if name != "" && (domain == name || strings.HasSuffix(domain, "."+name)) && len(name) > bestLen {
				bestID, bestName, bestLen = z.ID, name, len(name)
			}
		}
	}
	if bestLen < 0 {
		return "", &cfUserFacing{msg: "This token has no Cloudflare zone that matches " + domain + ". Add the domain to Cloudflare first, and give the token access to that zone."}
	}
	enc, err := s.crypto.Encrypt([]byte(token))
	if err != nil {
		return "", fmt.Errorf("encrypt token: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := hosting.SaveCloudflareSite(ctx, s.db, &hosting.CloudflareSite{
		WebsiteID: site.ID, TokenEnc: enc, ZoneID: bestID, ZoneName: bestName, VerifiedAt: now,
	}, now); err != nil {
		return "", err
	}
	return bestName, nil
}

// cfDisconnect is CloudflareSiteService::disconnect.
func (s *Server) cfDisconnect(ctx context.Context, site *hosting.Website) error {
	return hosting.DeleteCloudflareSite(ctx, s.db, site.ID)
}

// cfZoneIDOf is the zone id of the stored connection (CloudflareSiteService
// ::zoneId).
func (s *Server) cfZoneIDOf(ctx context.Context, site *hosting.Website) (string, error) {
	row, err := hosting.FindCloudflareSite(ctx, s.db, site.ID)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", &cfUserFacing{msg: "Cloudflare is not connected for this website."}
	}
	if !cfIDRe.MatchString(row.ZoneID) {
		return "", &cfUserFacing{msg: "Invalid Cloudflare identifier."}
	}
	return row.ZoneID, nil
}

// cfSetSetting is the two zone-setting PATCHs (SSL mode, Always HTTPS).
func (s *Server) cfSetSetting(ctx context.Context, site *hosting.Website, setting, value string) error {
	zone, err := s.cfZoneIDOf(ctx, site)
	if err != nil {
		return err
	}
	token, err := s.cfTokenOf(ctx, site.ID)
	if err != nil {
		return err
	}
	_, err = s.cfCall(ctx, token, "PATCH", "/zones/"+zone+"/settings/"+setting, map[string]any{"value": value})
	return err
}

func (s *Server) cfSetSsl(ctx context.Context, site *hosting.Website, mode string) error {
	valid := false
	for _, m := range cfSslModes {
		if m[0] == mode {
			valid = true
			break
		}
	}
	if !valid {
		return &cfUserFacing{msg: "Unknown SSL mode."}
	}
	return s.cfSetSetting(ctx, site, "ssl", mode)
}

func (s *Server) cfSetAlwaysHttps(ctx context.Context, site *hosting.Website, on bool) error {
	v := "off"
	if on {
		v = "on"
	}
	return s.cfSetSetting(ctx, site, "always_use_https", v)
}

func (s *Server) cfSetProxied(ctx context.Context, site *hosting.Website, recordID string, on bool) error {
	if !cfIDRe.MatchString(recordID) {
		return &cfUserFacing{msg: "Invalid Cloudflare identifier."}
	}
	zone, err := s.cfZoneIDOf(ctx, site)
	if err != nil {
		return err
	}
	token, err := s.cfTokenOf(ctx, site.ID)
	if err != nil {
		return err
	}
	_, err = s.cfCall(ctx, token, "PATCH", "/zones/"+zone+"/dns_records/"+recordID, map[string]any{"proxied": on})
	return err
}

func (s *Server) cfPurge(ctx context.Context, site *hosting.Website) error {
	zone, err := s.cfZoneIDOf(ctx, site)
	if err != nil {
		return err
	}
	token, err := s.cfTokenOf(ctx, site.ID)
	if err != nil {
		return err
	}
	_, err = s.cfCall(ctx, token, "POST", "/zones/"+zone+"/purge_cache", map[string]any{"purge_everything": true})
	return err
}

// cfAudit records a Cloudflare change (target: the website).
func (s *Server) cfAudit(r *http.Request, user *identity.User, action string, websiteID int64, details map[string]any) {
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &uid, Username: user.Username,
		Action: action, TargetType: "website", TargetID: strconv.FormatInt(websiteID, 10),
		Result: audit.Success, IP: clientIP(r), UserAgent: r.UserAgent(),
		Details: details,
	})
}

// ---------------------------------------------------------------- handlers

// cfSiteOf resolves the website (nil = not found / no access).
func (s *Server) cfSiteOf(r *http.Request, id string) *hosting.Website {
	user := userFrom(r.Context())
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), n, user)
	if err != nil || site == nil {
		return nil
	}
	return site
}

// siteCloudflare renders the site's Cloudflare page (index —
// websites.manage).
func (s *Server) siteCloudflare(w http.ResponseWriter, r *http.Request) {
	site := s.cfSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.notFound(w, r)
		return
	}
	sess := sessionFrom(r.Context())
	status, err := s.cfStatus(r.Context(), site)
	if err != nil {
		s.log.Error("cloudflare status", "error", err)
		status = &cfStatusView{Error: "Cloudflare could not be read."}
	}
	data := map[string]any{
		"User":     userFrom(r.Context()),
		"CSRF":     session.CSRFToken(sess),
		"Site":     site,
		"Status":   (*cfStatusView)(nil),
		"SSLModes": cfSslModes,
		"Success":  "",
		"Error":    "",
	}
	if status != nil {
		data["Status"] = status
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "sites_cloudflare", data)
}

// cfFlashErr redirects with the error flash (the PHP's
// redirectWithFailure).
func (s *Server) cfFlashErr(w http.ResponseWriter, r *http.Request, err error) {
	sess := sessionFrom(r.Context())
	msg := "Cloudflare: request failed."
	var ufe *cfUserFacing
	if errors.As(err, &ufe) {
		msg = ufe.msg
	} else {
		s.log.Error("cloudflare action", "error", err)
	}
	sess.Flash("error", msg)
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/cloudflare", http.StatusSeeOther)
}

func (s *Server) cfFlashNotFound(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", "Website not found.")
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

func (s *Server) cfRedirectWith(w http.ResponseWriter, r *http.Request, msg string) {
	sess := sessionFrom(r.Context())
	sess.Flash("success", msg)
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/cloudflare", http.StatusSeeOther)
}

// siteCloudflareConnect (connect — websites.manage).
func (s *Server) siteCloudflareConnect(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.cfFlashErr(w, r, errors.New("bad request"))
		return
	}
	user := userFrom(r.Context())
	site := s.cfSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cfFlashNotFound(w, r)
		return
	}
	zoneName, err := s.cfConnect(r.Context(), site, r.FormValue("token"))
	if err != nil {
		s.cfFlashErr(w, r, err)
		return
	}
	s.cfAudit(r, user, "cloudflare.site_connect", site.ID, map[string]any{"zone": zoneName})
	s.cfRedirectWith(w, r, "Cloudflare connected for "+site.Domain+".")
}

// siteCloudflareDisconnect (disconnect — websites.manage).
func (s *Server) siteCloudflareDisconnect(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site := s.cfSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cfFlashNotFound(w, r)
		return
	}
	if err := s.cfDisconnect(r.Context(), site); err != nil {
		s.cfFlashErr(w, r, err)
		return
	}
	s.cfAudit(r, user, "cloudflare.site_disconnect", site.ID, nil)
	s.cfRedirectWith(w, r, "Cloudflare disconnected.")
}

// siteCloudflareSsl (ssl — websites.manage).
func (s *Server) siteCloudflareSsl(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.cfFlashErr(w, r, errors.New("bad request"))
		return
	}
	user := userFrom(r.Context())
	site := s.cfSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cfFlashNotFound(w, r)
		return
	}
	mode := r.FormValue("mode")
	if err := s.cfSetSsl(r.Context(), site, mode); err != nil {
		s.cfFlashErr(w, r, err)
		return
	}
	s.cfAudit(r, user, "cloudflare.site_ssl", site.ID, map[string]any{"mode": mode})
	s.cfRedirectWith(w, r, "SSL/TLS mode updated.")
}

// siteCloudflareAlwaysHttps (always-https — websites.manage).
func (s *Server) siteCloudflareAlwaysHttps(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.cfFlashErr(w, r, errors.New("bad request"))
		return
	}
	user := userFrom(r.Context())
	site := s.cfSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cfFlashNotFound(w, r)
		return
	}
	on := r.FormValue("on") == "1"
	if err := s.cfSetAlwaysHttps(r.Context(), site, on); err != nil {
		s.cfFlashErr(w, r, err)
		return
	}
	s.cfAudit(r, user, "cloudflare.site_always_https", site.ID, map[string]any{"on": on})
	s.cfRedirectWith(w, r, "Always Use HTTPS updated.")
}

// siteCloudflareProxy (proxy — websites.manage).
func (s *Server) siteCloudflareProxy(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.cfFlashErr(w, r, errors.New("bad request"))
		return
	}
	user := userFrom(r.Context())
	site := s.cfSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cfFlashNotFound(w, r)
		return
	}
	record := r.FormValue("record")
	on := r.FormValue("on") == "1"
	if err := s.cfSetProxied(r.Context(), site, record, on); err != nil {
		s.cfFlashErr(w, r, err)
		return
	}
	s.cfAudit(r, user, "cloudflare.site_proxy", site.ID, map[string]any{"record": record, "proxied": on})
	s.cfRedirectWith(w, r, "Proxy setting updated.")
}

// siteCloudflarePurge (purge — websites.manage).
func (s *Server) siteCloudflarePurge(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site := s.cfSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cfFlashNotFound(w, r)
		return
	}
	if err := s.cfPurge(r.Context(), site); err != nil {
		s.cfFlashErr(w, r, err)
		return
	}
	s.cfAudit(r, user, "cloudflare.site_purge", site.ID, nil)
	s.cfRedirectWith(w, r, "Cloudflare cache purged.")
}

package http

// This file ports the server-side Cloudflare area (CloudflareController +
// CloudflareService): administrators connect one Cloudflare API token per
// server and manage the free-plan CDN/DNS of the account's zones — the zone
// list, the zone's SSL mode / Always Use HTTPS / DNS records (proxy toggle)
// and a cache purge. The panel talks to Cloudflare's API directly (the same
// injectable transport as the per-website area; never the agent); the token
// is stored encrypted and is only ever a Bearer header. Routes:
//   GET  /cloudflare          (server.view)
//   POST /cloudflare/connect  (server.manage)
//   POST /cloudflare/disconnect | proxy | ssl | always-https | purge
//        (server.manage)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// cfSslModeValid reports whether mode is one of the SSL modes.
func cfSslModeValid(mode string) bool {
	for _, m := range cfSslModes {
		if m[0] == mode {
			return true
		}
	}
	return false
}

// cfAccountView is the stored account (without the token).
type cfAccountView struct {
	AccountEmail string
	VerifiedAt   string
}

// cfZoneListItem is one row of the zone list.
type cfZoneListItem struct {
	ID          string
	Name        string
	Status      string
	NameServers []string
}

// cfZoneRecordView is one DNS record of a managed zone. Mode is set by the
// handler: "none" (not proxiable), "manage" (the proxy switch) or "view"
// (the Proxied / DNS-only label for read-only users).
type cfZoneRecordView struct {
	ID        string
	Type      string
	Name      string
	Content   string
	Proxied   bool
	Proxiable bool
	NextOn    int // 1 when the switch would turn the proxy ON
	Mode      string
}

// cfZoneView is a selected zone with its settings and records.
type cfZoneView struct {
	ID          string
	Name        string
	Status      string
	NameServers []string
	SSL         string
	AlwaysHTTPS bool
	NextAH      int // 1 when the switch would turn the setting ON
	Records     []cfZoneRecordView
}

// cfServerAccount is CloudflareService::account (nil when not connected).
func (s *Server) cfServerAccount(ctx context.Context, serverID int64) (*cfAccountView, error) {
	row, err := hosting.FindCloudflareAccount(ctx, s.db, serverID)
	if err != nil || row == nil {
		return nil, err
	}
	return &cfAccountView{AccountEmail: row.AccountEmail, VerifiedAt: row.VerifiedAt}, nil
}

// cfServerToken returns the decrypted server token ("" when absent).
func (s *Server) cfServerToken(ctx context.Context, serverID int64) (string, error) {
	row, err := hosting.FindCloudflareAccount(ctx, s.db, serverID)
	if err != nil || row == nil {
		return "", err
	}
	tok, err := s.crypto.Decrypt(row.TokenEnc)
	if err != nil {
		return "", err
	}
	return string(tok), nil
}

// cfServerZones is CloudflareService::zones (active + pending).
func (s *Server) cfServerZones(ctx context.Context, serverID int64) ([]cfZoneListItem, error) {
	token, err := s.cfServerToken(ctx, serverID)
	if err != nil {
		return nil, err
	}
	res, err := s.cfCall(ctx, token, "GET", "/zones?per_page=50&status=active,pending", nil)
	if err != nil {
		return nil, err
	}
	var items []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Status      string   `json:"status"`
		NameServers []string `json:"name_servers"`
	}
	if err := json.Unmarshal(res, &items); err != nil {
		return nil, &cfUserFacing{msg: "Cloudflare: unexpected response."}
	}
	out := make([]cfZoneListItem, 0, len(items))
	for _, it := range items {
		out = append(out, cfZoneListItem{ID: it.ID, Name: it.Name, Status: it.Status, NameServers: it.NameServers})
	}
	return out, nil
}

// cfServerZone is CloudflareService::zone (+ settings + records).
func (s *Server) cfServerZone(ctx context.Context, serverID int64, zoneID string) (*cfZoneView, error) {
	if !cfIDRe.MatchString(zoneID) {
		return nil, &cfUserFacing{msg: "Invalid Cloudflare identifier."}
	}
	token, err := s.cfServerToken(ctx, serverID)
	if err != nil {
		return nil, err
	}
	v := &cfZoneView{ID: zoneID}
	res, err := s.cfCall(ctx, token, "GET", "/zones/"+zoneID, nil)
	if err != nil {
		return nil, err
	}
	var z struct {
		Name        string   `json:"name"`
		Status      string   `json:"status"`
		NameServers []string `json:"name_servers"`
	}
	if err := json.Unmarshal(res, &z); err == nil {
		v.Name, v.Status, v.NameServers = z.Name, z.Status, z.NameServers
	}
	var set struct {
		Value string `json:"value"`
	}
	sslRes, err := s.cfCall(ctx, token, "GET", "/zones/"+zoneID+"/settings/ssl", nil)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(sslRes, &set); err == nil {
		v.SSL = set.Value
	}
	ahRes, err := s.cfCall(ctx, token, "GET", "/zones/"+zoneID+"/settings/always_use_https", nil)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(ahRes, &set); err == nil {
		v.AlwaysHTTPS = set.Value == "on"
	}
	recRes, err := s.cfCall(ctx, token, "GET", "/zones/"+zoneID+"/dns_records?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	var items []struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Name      string `json:"name"`
		Content   string `json:"content"`
		Proxiable bool   `json:"proxiable"`
		Proxied   bool   `json:"proxied"`
	}
	if err := json.Unmarshal(recRes, &items); err == nil {
		for _, it := range items {
			rec := cfZoneRecordView{ID: it.ID, Type: it.Type, Name: it.Name, Content: it.Content, Proxiable: it.Proxiable, Proxied: it.Proxied}
			if !rec.Proxied {
				rec.NextOn = 1
			}
			v.Records = append(v.Records, rec)
		}
	}
	if v.AlwaysHTTPS {
		v.NextAH = 0
	} else {
		v.NextAH = 1
	}
	return v, nil
}

// cfServerAudit records a Cloudflare change (target: the server).
func (s *Server) cfServerAudit(r *http.Request, user *identity.User, action string, details map[string]any) {
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &uid, Username: user.Username,
		Action: action, TargetType: "server", TargetID: strconv.FormatInt(s.currentServerID(r), 10),
		Result: audit.Success, IP: clientIP(r), UserAgent: r.UserAgent(),
		Details: details,
	})
}

// ---------------------------------------------------------------- handlers

// cloudflareIndex renders the server's Cloudflare page (server.view).
func (s *Server) cloudflareIndex(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	serverID := s.currentServerID(r)
	account, err := s.cfServerAccount(r.Context(), serverID)
	if err != nil {
		s.log.Error("cloudflare account", "error", err)
	}
	var zones []cfZoneListItem
	var zone *cfZoneView
	var cfErr string
	fail := func(err error) {
		var ufe *cfUserFacing
		if errors.As(err, &ufe) {
			cfErr = ufe.msg
		} else {
			cfErr = "Cloudflare: request failed."
		}
	}
	if account != nil {
		zones, err = s.cfServerZones(r.Context(), serverID)
		if err != nil {
			fail(err)
		} else if want := r.URL.Query().Get("zone"); want != "" {
			zone, err = s.cfServerZone(r.Context(), serverID, want)
			if err != nil {
				fail(err)
				zone = nil
			}
		}
	}
	canManage := s.can(r, "server.manage")
	if zone != nil {
		for i := range zone.Records {
			rec := &zone.Records[i]
			switch {
			case !rec.Proxiable:
				rec.Mode = "none"
			case canManage:
				rec.Mode = "manage"
			default:
				rec.Mode = "view"
			}
		}
	}
	data := map[string]any{
		"User":      userFrom(r.Context()),
		"CSRF":      session.CSRFToken(sess),
		"Title":     "Cloudflare",
		"Subtitle":  "CDN and DNS through your Cloudflare account (free plan)",
		"Account":   (*cfAccountView)(nil),
		"Zones":     []cfZoneListItem(nil),
		"Zone":      (*cfZoneView)(nil),
		"CFError":   cfErr,
		"SSLModes":  cfSslModes,
		"CanManage": canManage,
		"Success":   "",
		"Error":     "",
	}
	if account != nil {
		data["Account"] = account
	}
	if zones != nil {
		data["Zones"] = zones
	}
	if zone != nil {
		data["Zone"] = zone
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "cloudflare_index", data)
}

// cfServerFlashErr redirects back with the error flash.
func (s *Server) cfServerFlashErr(w http.ResponseWriter, r *http.Request, to string, err error) {
	var ufe *cfUserFacing
	if !errors.As(err, &ufe) {
		s.log.Error("cloudflare action", "error", err)
	}
	msg := "Cloudflare: request failed."
	if ufe != nil {
		msg = ufe.msg
	}
	sessionFrom(r.Context()).Flash("error", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// cfServerFlashOK redirects back with the success flash.
func (s *Server) cfServerFlashOK(w http.ResponseWriter, r *http.Request, to, msg string) {
	sessionFrom(r.Context()).Flash("success", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// cfServerZoneBack is the redirect target for a zone action (only a valid
// zone id is echoed into the query string).
func cfServerZoneBack(zone string) string {
	if !cfIDRe.MatchString(zone) {
		return "/cloudflare"
	}
	return "/cloudflare?zone=" + zone
}

// cfServerConnect is CloudflareService::connect (verify + store encrypted).
func (s *Server) cfServerConnect(ctx context.Context, serverID int64, token string) error {
	token = strings.TrimSpace(token)
	if !cfTokenRe.MatchString(token) {
		return &cfUserFacing{msg: "That does not look like a Cloudflare API token."}
	}
	if _, err := s.cfCall(ctx, token, "GET", "/user/tokens/verify", nil); err != nil {
		return err
	}
	enc, err := s.crypto.Encrypt([]byte(token))
	if err != nil {
		return fmt.Errorf("encrypt token: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return hosting.SaveCloudflareAccount(ctx, s.db, &hosting.CloudflareAccount{
		ServerID: serverID, TokenEnc: enc, VerifiedAt: now,
	}, now)
}

// cloudflareConnect (cloudflare.connect — server.manage).
func (s *Server) cloudflareConnect(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.cfServerFlashErr(w, r, "/cloudflare", fmt.Errorf("bad request"))
		return
	}
	user := userFrom(r.Context())
	if err := s.cfServerConnect(r.Context(), s.currentServerID(r), r.FormValue("token")); err != nil {
		s.cfServerFlashErr(w, r, "/cloudflare", err)
		return
	}
	s.cfServerAudit(r, user, "cloudflare.connect", nil)
	s.cfServerFlashOK(w, r, "/cloudflare", "Cloudflare connected.")
}

// cloudflareDisconnect (cloudflare.disconnect — server.manage).
func (s *Server) cloudflareDisconnect(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if err := hosting.DeleteCloudflareAccount(r.Context(), s.db, s.currentServerID(r)); err != nil {
		s.cfServerFlashErr(w, r, "/cloudflare", err)
		return
	}
	s.cfServerAudit(r, user, "cloudflare.disconnect", nil)
	s.cfServerFlashOK(w, r, "/cloudflare", "Cloudflare disconnected.")
}

// cloudflareProxy (cloudflare.proxy — server.manage).
func (s *Server) cloudflareProxy(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.cfServerFlashErr(w, r, "/cloudflare", fmt.Errorf("bad request"))
		return
	}
	zone := r.FormValue("zone")
	record := r.FormValue("record")
	on := r.FormValue("proxied") == "1"
	if !cfIDRe.MatchString(zone) || !cfIDRe.MatchString(record) {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), &cfUserFacing{msg: "Invalid Cloudflare identifier."})
		return
	}
	token, err := s.cfServerToken(r.Context(), s.currentServerID(r))
	if err != nil {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), err)
		return
	}
	if _, err := s.cfCall(r.Context(), token, "PATCH", "/zones/"+zone+"/dns_records/"+record, map[string]any{"proxied": on}); err != nil {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), err)
		return
	}
	s.cfServerAudit(r, userFrom(r.Context()), "cloudflare.proxy", map[string]any{"zone": zone, "record": record, "proxied": on})
	s.cfServerFlashOK(w, r, cfServerZoneBack(zone), "DNS record updated.")
}

// cloudflareSsl (cloudflare.ssl — server.manage).
func (s *Server) cloudflareSsl(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.cfServerFlashErr(w, r, "/cloudflare", fmt.Errorf("bad request"))
		return
	}
	zone := r.FormValue("zone")
	mode := r.FormValue("mode")
	if !cfIDRe.MatchString(zone) {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), &cfUserFacing{msg: "Invalid Cloudflare identifier."})
		return
	}
	if !cfSslModeValid(mode) {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), &cfUserFacing{msg: "Unknown SSL mode."})
		return
	}
	token, err := s.cfServerToken(r.Context(), s.currentServerID(r))
	if err != nil {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), err)
		return
	}
	if _, err := s.cfCall(r.Context(), token, "PATCH", "/zones/"+zone+"/settings/ssl", map[string]any{"value": mode}); err != nil {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), err)
		return
	}
	s.cfServerAudit(r, userFrom(r.Context()), "cloudflare.ssl", map[string]any{"zone": zone, "mode": mode})
	s.cfServerFlashOK(w, r, cfServerZoneBack(zone), "SSL mode updated.")
}

// cloudflareAlwaysHttps (cloudflare.always_https — server.manage).
func (s *Server) cloudflareAlwaysHttps(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.cfServerFlashErr(w, r, "/cloudflare", fmt.Errorf("bad request"))
		return
	}
	zone := r.FormValue("zone")
	on := r.FormValue("on") == "1"
	if !cfIDRe.MatchString(zone) {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), &cfUserFacing{msg: "Invalid Cloudflare identifier."})
		return
	}
	token, err := s.cfServerToken(r.Context(), s.currentServerID(r))
	if err != nil {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), err)
		return
	}
	v := "off"
	if on {
		v = "on"
	}
	if _, err := s.cfCall(r.Context(), token, "PATCH", "/zones/"+zone+"/settings/always_use_https", map[string]any{"value": v}); err != nil {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), err)
		return
	}
	s.cfServerAudit(r, userFrom(r.Context()), "cloudflare.always_https", map[string]any{"zone": zone, "on": on})
	s.cfServerFlashOK(w, r, cfServerZoneBack(zone), "Always Use HTTPS updated.")
}

// cloudflarePurge (cloudflare.purge — server.manage).
func (s *Server) cloudflarePurge(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.cfServerFlashErr(w, r, "/cloudflare", fmt.Errorf("bad request"))
		return
	}
	zone := r.FormValue("zone")
	if !cfIDRe.MatchString(zone) {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), &cfUserFacing{msg: "Invalid Cloudflare identifier."})
		return
	}
	token, err := s.cfServerToken(r.Context(), s.currentServerID(r))
	if err != nil {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), err)
		return
	}
	if _, err := s.cfCall(r.Context(), token, "POST", "/zones/"+zone+"/purge_cache", map[string]any{"purge_everything": true}); err != nil {
		s.cfServerFlashErr(w, r, cfServerZoneBack(zone), err)
		return
	}
	s.cfServerAudit(r, userFrom(r.Context()), "cloudflare.purge", map[string]any{"zone": zone})
	s.cfServerFlashOK(w, r, cfServerZoneBack(zone), "Cache purged.")
}

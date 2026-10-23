package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// dnsIndex lists the DNS zones the user may see, tenant-scoped (website-aware).
// Gated by dns.manage, which the dns plan feature withholds when disabled.
// Mirrors DnsController::index (route "dns", dns.manage).
func (s *Server) dnsIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zones, err := hosting.NewDnsRepository(s.db).Zones(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list dns zones", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess := sessionFrom(r.Context())
	s.render(w, "dns", map[string]any{
		"User":    user,
		"CSRF":    session.CSRFToken(sess),
		"Zones":   zones,
		"Success": pullFlashKey(sess, "success"),
		"Error":   pullFlashKey(sess, "error"),
	})
}

// siteDnsService builds the DNS service bound to the current actor.
func (s *Server) siteDnsService(r *http.Request, user *identity.User) *hosting.DnsService {
	return hosting.NewDnsService(s.db, hosting.NewDnsRepository(s.db), s.agent, s.settings,
		hosting.NewWebsiteRepository(s.db), s.websiteAuditHook(r, user))
}

// siteDns renders the website's DNS page (DnsController::site): the zone's
// page when the user may see it, the "managed by another account" notice,
// or the set-up card. Gated by dns.manage.
func (s *Server) siteDns(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	repo := hosting.NewDnsRepository(s.db)
	zone, err := repo.ZoneForWebsite(r.Context(), site.ID)
	if err != nil {
		s.log.Error("site dns zone", "site", site.ID, "error", err)
		s.showNotFound(w)
		return
	}
	if zone != nil {
		if owned, err := repo.Zone(r.Context(), s.currentServerID(r), zone.ID, user); err == nil && owned != nil {
			http.Redirect(w, r, fmt.Sprintf("/dns/zones/%d", zone.ID), http.StatusSeeOther)
			return
		}
		sess.Flash("error", "The DNS zone of this domain is managed by another account.")
		http.Redirect(w, r, fmt.Sprintf("/sites/%d", site.ID), http.StatusSeeOther)
		return
	}
	svc := s.siteDnsService(r, user)
	status, err := svc.Status(r.Context())
	if err != nil {
		status = nil // the agent keeps the truth; best effort, as in the PHP
	}
	nameservers, err := svc.Nameservers(r.Context())
	if err != nil {
		s.log.Error("dns nameservers", "error", err)
	}
	ready := status != nil && status.Configured && status.Running
	s.render(w, "site_dns", map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sess),
		"Site":        site,
		"Status":      status,
		"Nameservers": nameservers,
		"Ready":       ready,
		"Installed":   status != nil && status.Installed,
		"Staff":       s.can(r, "server.manage"),
		"Success":     pullFlashKey(sess, "success"),
		"Error":       pullFlashKey(sess, "error"),
	})
}

// dnsZoneStore hosts a new zone (DnsController::store, route dns.store,
// dns.manage). The website set-up page may ask for errors back there.
func (s *Server) dnsZoneStore(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	back := "/dns"
	if t := r.PostFormValue("return_to"); siteDNSTargetRe.MatchString(t) {
		back = t
	}
	id, err := s.siteDnsService(r, user).Create(r.Context(), s.currentServerID(r), postValues(r), user)
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			sess.Flash("error", firstValidationError(ve))
		case errors.As(err, &ue):
			sess.Flash("error", userFacingMsg(ue))
		default:
			s.log.Error("create dns zone", "error", err)
			sess.Flash("error", "The zone could not be created.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Zone created and loaded.")
	http.Redirect(w, r, fmt.Sprintf("/dns/zones/%d", id), http.StatusSeeOther)
}

// siteDNSTargetRe is the store's return_to allowlist
// (DnsController::store's ^/sites/\d+/dns$).
var siteDNSTargetRe = regexp.MustCompile(`^/sites/\d+/dns$`)

// siteMailTargetRe is the mail store's return_to allowlist.
var siteMailTargetRe = regexp.MustCompile(`^/sites/\d+/mail$`)

// dnsZoneShow renders one zone's records (DnsController::show, route
// dns.zone, dns.manage).
func (s *Server) dnsZoneShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	zone, err := hosting.NewDnsRepository(s.db).Zone(r.Context(), s.currentServerID(r), pathID(r), user)
	if err != nil {
		s.log.Error("show dns zone", "error", err)
		s.showNotFound(w)
		return
	}
	if zone == nil {
		s.showNotFound(w)
		return
	}
	sort := r.URL.Query().Get("sort")
	sess := sessionFrom(r.Context())
	var skipped []hosting.ImportSkipped
	if v, ok := sess.PullFlash("dns_import"); ok {
		_ = json.Unmarshal([]byte(v), &skipped)
	}
	s.renderZone(w, r, zone, s.zoneRecords(r.Context(), zone.ID, sort), nil, nil, skipped, sort)
}

// pathID parses the {id} path value (0 when malformed; the scoped lookup
// then 404s, as in the other pages).
func pathID(r *http.Request) int64 {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

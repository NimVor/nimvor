package http

// This file ports the website's IP assignment (IpController::assignSite,
// POST /sites/{id}/ip, server.manage) and feeds the advanced page's IP card
// (the ipPool data of SitesController::advanced). The /ips pool pages land
// with the DNS/IP batch.

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// ipService builds the IP service with the audit hook bound to the actor.
func (s *Server) ipService(r *http.Request, user *identity.User) *hosting.IpService {
	return hosting.NewIpService(s.db, s.websiteService(r, user), hosting.NewWebsiteRepository(s.db),
		hosting.NewDnsService(s.db, hosting.NewDnsRepository(s.db), s.agent, s.settings,
			hosting.NewWebsiteRepository(s.db), s.websiteAuditHook(r, user)), s.settings,
		s.agent, s.websiteAuditHook(r, user), s.currentServerID(r))
}

// advancedIPPool is the pool the advanced page offers for the website
// (nil for non-administrators, who do not see the card).
func (s *Server) advancedIPPool(r *http.Request, user *identity.User, site *hosting.Website) []hosting.IPAddress {
	pool, err := s.ipService(r, user).PoolForSite(r.Context(), site.ID, user.RoleScope == "all")
	if err != nil {
		s.log.Error("ip pool", "id", site.ID, "error", err)
		return nil
	}
	return pool
}

// ipAssign saves the website's addresses (IpController::assignSite): the
// agent re-applies the website, and, asked for, the website's A/AAAA
// records in its DNS zone follow. A DNS failure moves the website back.
func (s *Server) ipAssign(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	back := fmt.Sprintf("/sites/%d/advanced#ip", site.ID)
	changed, err := s.ipService(r, user).Assign(r.Context(), site,
		r.PostFormValue("ipv4"), r.PostFormValue("ipv6"), r.PostFormValue("dns") == "1")
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			// The page re-renders without the field errors' forms; the
			// first message is the flashed error (PHP's backWithErrors).
			msg := ""
			if m, ok := ve.Fields["ipv4"]; ok {
				msg = m
			} else if m, ok := ve.Fields["ipv6"]; ok {
				msg = m
			}
			sess.Flash("error", msg)
		case errors.As(err, &ue):
			sess.Flash("error", userFacingMsg(ue))
		default:
			s.log.Error("ip assign", "id", site.ID, "error", err)
			sess.Flash("error", "The addresses could not be saved.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	msg := "Addresses saved and the website reloaded"
	if changed > 0 {
		msg += fmt.Sprintf("; %d DNS record(s) updated", changed)
	}
	sess.Flash("success", msg+".")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

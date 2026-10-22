package http

// This file ports the WordPress toolkit's list and scan actions
// (WordPressController::index, scanAll, siteList, scanSite): the
// all-installations list (GET /wordpress, websites.view) with the
// outdated / issues filters, the scan-all (POST /wordpress/scan,
// websites.manage), the per-website list (GET /sites/{id}/wordpress —
// a single installation is opened directly) and the per-website scan
// (POST /sites/{id}/wordpress/scan — the website's folders, a website
// inside another's folder included). The agent reads files only.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

func (s *Server) wordpressService(r *http.Request, user *identity.User) *hosting.WordPressService {
	return hosting.NewWordPressService(s.db, s.agent, s.currentServerID(r), s.wordpressAudit(r, user))
}

// wordpressAudit binds the audit hook for the WordPress actions.
func (s *Server) wordpressAudit(r *http.Request, user *identity.User) hosting.Auditor {
	return func(ctx context.Context, action, targetType, target string, details map[string]any) {
		entry := audit.Entry{
			Action:     action,
			TargetType: targetType,
			TargetID:   target,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    details,
		}
		if user != nil {
			uid := user.ID
			entry.UserID = &uid
			entry.Username = user.Username
		}
		s.audit.Record(ctx, entry)
	}
}

// wpFlashRedirect flashes a message and redirects (the panel's
// redirectWithSuccess / redirectWithError).
func (s *Server) wpFlashRedirect(w http.ResponseWriter, r *http.Request, kind, msg, to string) {
	sessionFrom(r.Context()).Flash(kind, msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// wordpressIndex renders the all-installations list
// (WordPressController::index).
func (s *Server) wordpressIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	filter := r.URL.Query().Get("filter")
	if filter != "outdated" && filter != "issues" {
		filter = ""
	}
	svc := s.wordpressService(r, user)
	installs, err := svc.Installs(r.Context(), user, nil, filter)
	if err != nil {
		s.log.Error("wordpress index", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var all []hosting.WordPressInstall
	if filter != "" {
		all, err = svc.Installs(r.Context(), user, nil, "")
		if err != nil {
			s.log.Error("wordpress index", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	every := installs
	if filter != "" {
		every = all
	}
	outdated, issues := 0, 0
	for _, i := range every {
		if i.Outdated || i.PluginUpdates+i.ThemeUpdates > 0 {
			outdated++
		}
		if i.Issues > 0 {
			issues++
		}
	}
	sess := sessionFrom(r.Context())
	s.render(w, "wordpress_index", map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sess),
		"Installs":      installs,
		"All":           all,
		"Filter":        filter,
		"AllCount":      len(every),
		"OutdatedCount": outdated,
		"IssuesCount":   issues,
		"CanManage":     s.can(r, "websites.manage"),
	})
}

// wordpressScanAll scans every website folder the user may see
// (WordPressController::scanAll).
func (s *Server) wordpressScanAll(w http.ResponseWriter, r *http.Request) {
	if !s.wpFormToken(w, r) {
		return
	}
	user := userFrom(r.Context())
	sites, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("wordpress scan", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	res, err := s.wordpressService(r, user).Scan(r.Context(), sites)
	if err != nil {
		s.wpScanError(w, r, err, "/wordpress")
		return
	}
	s.wpScanned(w, r, res, "/wordpress", "/wordpress")
}

// wpScanned is the scan's reply (WordPressController::scanned): a success
// flash, or the message with the not-scanned websites as an error.
func (s *Server) wpScanned(w http.ResponseWriter, r *http.Request, res *hosting.WordPressScan, to, errTo string) {
	msg := wpScanCount(res.Scanned) + " website folder(s) scanned: " + wpScanCount(res.Found) + " WordPress installation(s) found."
	if len(res.Errors) > 0 {
		n := len(res.Errors)
		if n > 5 {
			n = 5
		}
		s.wpFlashRedirect(w, r, "error", msg+" Not scanned: "+strings.Join(res.Errors[:n], "; "), errTo)
		return
	}
	s.wpFlashRedirect(w, r, "success", msg, to)
}

func wpScanCount(n int) string {
	return strconv.Itoa(n)
}

// wordpressSiteList renders a website's installations (WordPressController::
// siteList); a website with a single installation is opened directly.
func (s *Server) wordpressSiteList(w http.ResponseWriter, r *http.Request) {
	site := s.wpSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.wpFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	id := site.ID
	installs, err := s.wordpressService(r, user).Installs(r.Context(), user, &id, "")
	if err != nil {
		s.log.Error("wordpress site list", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(installs) == 1 && r.URL.Query().Get("list") == "" {
		http.Redirect(w, r, fmt.Sprintf("/sites/%d/wordpress/%d", id, installs[0].ID), http.StatusSeeOther)
		return
	}
	sess := sessionFrom(r.Context())
	s.render(w, "wordpress_site", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Site":      site,
		"Installs":  installs,
		"CanManage": s.can(r, "websites.manage"),
	})
}

// wordpressScanSite scans the website's folders — and the folders of any
// website inside them (WordPressController::scanSite).
func (s *Server) wordpressScanSite(w http.ResponseWriter, r *http.Request) {
	if !s.wpFormToken(w, r) {
		return
	}
	site := s.wpSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.wpFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	id := site.ID
	all, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("wordpress scan", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	base := strings.TrimRight(site.Root, "/") + "/"
	sites := []hosting.Website{}
	for _, st := range all {
		if st.ID == id || strings.HasPrefix(st.Root, base) {
			sites = append(sites, st)
		}
	}
	res, err := s.wordpressService(r, user).Scan(r.Context(), sites)
	if err != nil {
		s.wpScanError(w, r, err, fmt.Sprintf("/sites/%d/wordpress?list=1", id))
		return
	}
	s.wpScanned(w, r, res, fmt.Sprintf("/sites/%d/wordpress", id), fmt.Sprintf("/sites/%d/wordpress?list=1", id))
}

// wpFormToken checks the CSRF token (the panel's forms all carry it).
func (s *Server) wpFormToken(w http.ResponseWriter, r *http.Request) bool {
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return false
	}
	return true
}

// wpSiteOf resolves the website for a /sites/{id} route.
func (s *Server) wpSiteOf(r *http.Request, id string) *hosting.Website {
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

func (s *Server) wpFlashNotFound(w http.ResponseWriter, r *http.Request) {
	sessionFrom(r.Context()).Flash("error", "Website not found.")
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

// wpScanError flashes the scan failure (the agent's message when it
// answered, the generic one when it could not be reached) and redirects.
func (s *Server) wpScanError(w http.ResponseWriter, r *http.Request, err error, to string) {
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		s.wpFlashRedirect(w, r, "error", ue.What, to)
		return
	}
	var ae *agent.Error
	msg := "The agent could not be reached."
	if errors.As(err, &ae) && ae.Message != "" {
		msg = ae.Message
	}
	s.log.Error("wordpress scan", "error", err)
	s.wpFlashRedirect(w, r, "error", msg, to)
}

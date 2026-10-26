package http

// This file ports DiscoveryController (batch 65): the read-only inventory
// pages — the overview, PHP, certificates and cron — plus the explicit
// re-scan. Everything here reads; only POST /discovery/run asks the agent for
// a fresh inventory, and even that changes nothing on the server.
//
// The pages are tenant-scoped: an administrator sees the whole inventory and
// anyone else sees only their own domains, their certificates and the PHP
// pools those use (DiscoveryService::forScope).

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// discoveryReturnToRe is the run action's return_to allowlist
// (the PHP's `#^/[a-z0-9/_-]*$#`).
var discoveryReturnToRe = regexp.MustCompile(`^/[a-z0-9/_-]*$`)

// discoveryScopedInventory loads the latest inventory and narrows it to what
// the actor may see. ok=false means the page was already answered with the
// discovery-unavailable fallback.
func (s *Server) discoveryScopedInventory(w http.ResponseWriter, r *http.Request, user *identity.User) (hosting.InventoryResult, bool) {
	inv, err := s.discoveryService().Inventory(r.Context(), s.currentServerID(r), false, userIDPtr(user))
	if err != nil {
		s.renderDiscoveryUnavailable(w, r, err)
		return hosting.InventoryResult{}, false
	}
	out := *inv
	if !hosting.IsAll(user) {
		domains := []string{}
		if sites, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user); err == nil {
			for _, site := range sites {
				domains = append(domains, site.Domain)
			}
		}
		out.Data = hosting.ForScope(inv.Data, false, domains)
	}
	return out, true
}

// discoveryPageData is the shared page frame: the actor, the CSRF token, the
// flashed messages and the "discovered at" stamp the bar shows.
func (s *Server) discoveryPageData(r *http.Request, inv hosting.InventoryResult) map[string]any {
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"User":          userFrom(r.Context()),
		"CSRF":          session.CSRFToken(sess),
		"DiscoveredAt":  inv.CreatedAt,
		"CanRescan":     s.can(r, "discovery.run"),
		"CanPHP":        s.can(r, "php.view"),
		"CanSSL":        s.can(r, "ssl.view"),
		"CanCron":       s.can(r, "cron.view"),
		"CanSites":      s.can(r, "websites.view"),
		"CanDBs":        s.can(r, "databases.view"),
		"CanServices":   s.can(r, "services.view"),
		"Path":          r.URL.Path,
		"DiscoveredAgo": discoveredAgo(inv.CreatedAt, time.Now().UTC()),
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	return data
}

// discoveryIndex renders GET /discovery (DiscoveryController::index).
func (s *Server) discoveryIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	inv, ok := s.discoveryScopedInventory(w, r, user)
	if !ok {
		return
	}
	history, err := hosting.NewDiscoverySnapshotRepository(s.db).History(r.Context(), 10, s.currentServerID(r))
	if err != nil {
		s.log.Error("discovery history", "error", err)
	}
	data := s.discoveryPageData(r, inv)

	os := hosting.InventoryOSSection(inv.Data)
	prettyName, _ := os["pretty_name"].(string)
	supported, hasSupported := os["supported"].(bool)

	data["Websites"] = len(hosting.InventoryWebsites(inv.Data))
	data["PHPVersions"] = len(hosting.InventorySectionList(inv.Data, "php", "versions"))
	data["PHPPools"] = len(hosting.InventorySectionList(inv.Data, "php", "pools"))
	data["Databases"] = len(hosting.InventorySectionList(inv.Data, "databases", "schemas"))
	data["Certificates"] = len(hosting.InventoryCertificates(inv.Data))
	data["CronJobs"] = len(hosting.InventoryCronEntries(inv.Data))
	data["Services"] = len(hosting.InventoryList(inv.Data, "services"))
	systemUsers := 0
	for _, raw := range hosting.InventoryList(inv.Data, "users") {
		if u, ok := raw.(map[string]any); ok {
			if v, present := u["system"]; present && v != nil {
				if b, isBool := v.(bool); !isBool || b {
					continue
				}
			}
			systemUsers++
		}
	}
	data["SystemUsers"] = systemUsers
	data["Warnings"] = hosting.InventoryWarnings(inv.Data)
	data["OSName"] = prettyName
	data["OSUnsupported"] = hasSupported && !supported
	data["History"] = history
	s.render(w, "discovery_index", data)
}

// discoveryPHP renders GET /php (DiscoveryController::php).
func (s *Server) discoveryPHP(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	inv, ok := s.discoveryScopedInventory(w, r, user)
	if !ok {
		return
	}
	data := s.discoveryPageData(r, inv)
	canManage := s.can(r, "php.manage")

	// The package manager's extra data is best effort: an agent without the
	// action (or an unreachable one) simply leaves those cards out.
	available := []string{}
	services := map[string]any{}
	repository := map[string]any{}
	if canManage {
		if raw, err := s.agent.Call(r.Context(), "php.available", nil); err == nil {
			var list []string
			if json.Unmarshal(raw, &list) == nil {
				available = list
			}
		}
		if raw, err := s.agent.Call(r.Context(), "services.status", nil); err == nil {
			var list []struct {
				Unit        string `json:"unit"`
				ActiveState string `json:"active_state"`
			}
			if json.Unmarshal(raw, &list) == nil {
				for _, svc := range list {
					services[svc.Unit] = map[string]any{"unit": svc.Unit, "active_state": svc.ActiveState}
				}
			}
		}
		if raw, err := s.agent.Call(r.Context(), "php.repository", nil); err == nil {
			_ = json.Unmarshal(raw, &repository)
		}
	}

	// A version counts as installed for websites only with its PHP-FPM.
	installedFPM := map[string]bool{}
	if sec := hosting.InventoryPHPSection(inv.Data); sec != nil {
		if vs, ok := sec["versions"].([]any); ok {
			for _, v := range vs {
				if m, ok := v.(map[string]any); ok && jstr(m, "fpm_binary") != "" {
					installedFPM[jstr(m, "version")] = true
				}
			}
		}
	}
	installable := []string{}
	for i := len(available) - 1; i >= 0; i-- {
		if !installedFPM[available[i]] {
			installable = append(installable, available[i])
		}
	}
	data["Installable"] = installable
	data["CanPackages"] = s.can(r, "packages.manage")
	data["PHPSection"] = hosting.InventoryPHPSection(inv.Data)
	data["Pools"] = hosting.InventorySectionList(inv.Data, "php", "pools")
	data["Usage"] = hosting.InventoryPHPUsage(inv.Data)
	data["Available"] = available
	data["Services"] = services
	data["Repository"] = repository
	data["CanManagePHP"] = canManage
	s.render(w, "discovery_php", data)
}

// discoverySSL renders GET /ssl (DiscoveryController::ssl). Administrators
// manage TLS in the dedicated SSL section, so they are redirected there.
func (s *Server) discoverySSL(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if hosting.IsAll(user) {
		http.Redirect(w, r, "/ssl/hosts", http.StatusSeeOther)
		return
	}
	inv, ok := s.discoveryScopedInventory(w, r, user)
	if !ok {
		return
	}
	data := s.discoveryPageData(r, inv)
	warnDays, _ := s.settings.Int(r.Context(), "ssl_warning_days", 14)
	data["WarnDays"] = warnDays
	data["Certs"] = hosting.InventoryCertificateList(inv.Data)
	// The view's $withSsl: only the sites that reference a certificate,
	// soonest expiry first.
	sites := hosting.InventoryWebsites(inv.Data)
	withSSL := make([]hosting.DiscoveredSite, 0, len(sites))
	for _, site := range sites {
		if site.SSL != nil {
			withSSL = append(withSSL, site)
		}
	}
	sort.SliceStable(withSSL, func(i, j int) bool {
		return withSSL[i].SSL.DaysRemaining < withSSL[j].SSL.DaysRemaining
	})
	data["Sites"] = withSSL
	s.render(w, "discovery_ssl", data)
}

// discoveryCron renders GET /cron (DiscoveryController::cron).
func (s *Server) discoveryCron(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	inv, ok := s.discoveryScopedInventory(w, r, user)
	if !ok {
		return
	}
	data := s.discoveryPageData(r, inv)
	jobs := hosting.InventoryCronEntries(inv.Data)
	warnings := []string{}
	for _, raw := range hosting.InventorySectionList(inv.Data, "cron", "warnings") {
		if str, isStr := raw.(string); isStr && str != "" {
			warnings = append(warnings, str)
		}
	}
	data["Jobs"] = jobs
	data["Warnings"] = warnings
	s.render(w, "discovery_cron", data)
}

// discoveryRun re-scans and redirects (DiscoveryController::run). The agent is
// only ever asked to read; the flash states that nothing was changed.
func (s *Server) discoveryRun(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	back := r.FormValue("return_to")
	if !discoveryReturnToRe.MatchString(back) {
		back = "/discovery"
	}
	inv, err := s.discoveryService().Run(r.Context(), s.currentServerID(r), userIDPtr(user))
	if err != nil {
		msg := "The WhaCP agent could not be reached."
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Message != "" {
			msg = ae.Message
		}
		s.log.Error("discovery run", "error", err)
		sess.Flash("error", msg)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	count := len(hosting.InventoryWebsites(inv.Data))
	msg := "Discovery completed: " + strconv.Itoa(count) + " website(s) found. Nothing on the server was changed."
	sess.Flash("success", msg)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// discoveredAgo renders the discovery bar's "Discovered …" age.
func discoveredAgo(stamp string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return "just now"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " minutes ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " hours ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + " days ago"
	}
}

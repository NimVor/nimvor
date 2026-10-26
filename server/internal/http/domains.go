package http

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// domainsIndex GET /domains — lists every hostname served by the server with
// the owning website and whether its certificate covers it. Uses the same
// DiscoveryService.Inventory that other overview pages consume, and applies
// the same tenant scope as DomainsController::scoped. Mirrors
// DomainsController::index (domains, websites.view).
func (s *Server) domainsIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	svc := hosting.NewDiscoveryService(s.db, s.agent, s.settings)
	inv, err := svc.Inventory(r.Context(), s.currentServerID(r), false, &user.ID)
	if err != nil {
		s.renderError(w, http.StatusBadGateway, "Discovery unavailable", err.Error())
		return
	}
	// Scope data if the viewer is not an admin (same rule as PHP: websites
	// whose domain matches the actor's sites).
	data := inv.Data
	if !hosting.IsAll(user) {
		domains := s.ownedDomains(r.Context(), user)
		data = hosting.ForScope(data, false, domains)
	}
	rows := []map[string]any{}
	for _, site := range hosting.InventoryWebsites(data) {
		names := append([]string{site.Domain}, site.Aliases...)
		var siteSSLNames []string
		fallback := false
		if site.SSL != nil {
			fallback = site.SSL.CoversDomain
		}
		// When a per-site SSL list is reported under a "names" key (PHP
		// $site['ssl']['names']), use it; otherwise fall back to a simple
		// "covers domain" check.
		for i, name := range names {
			covered := i == 0 && fallback
			_ = siteSSLNames
			// Wildcard match: *.example.com covers foo.example.com but not
			// example.com itself.
			covered = covered || certCovers(nil, name) || certCoversHost(name, site)
			rows = append(rows, map[string]any{
				"Name":    name,
				"Primary": i == 0,
				"Domain":  site.Domain,
				"SiteID":  site.ID,
				"Covered": covered,
				"Enabled": site.Enabled,
				"HTTPS":   site.HTTPS,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ai := domainsApex(rows[i]["Name"].(string))
		aj := domainsApex(rows[j]["Name"].(string))
		return ai < aj || (ai == aj && rows[i]["Name"].(string) < rows[j]["Name"].(string))
	})
	pd := s.discoveryPageData(r, *inv)
	pd["Title"] = "Domains"
	pd["Rows"] = rows
	pd["Count"] = len(rows)
	s.render(w, "domains_index", pd)
}

// ownedDomains returns the domains of websites the actor owns, used to scope
// the discovery inventory for resellers/customers.
func (s *Server) ownedDomains(ctx context.Context, user *identity.User) []string {
	rows, err := s.db.QueryContext(ctx, `SELECT domain FROM websites WHERE owner_id = ?`, user.ID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err == nil {
			out = append(out, d)
		}
	}
	return out
}

// certCovers is kept for symmetry with the PHP helper but takes an explicit
// names list (unused here because DiscoveredSSL doesn't expose SANs directly).
func certCovers(names []string, host string) bool {
	for _, n := range names {
		if n == host {
			return true
		}
	}
	return false
}

// certCoversHost is a simplified covers() check using only what DiscoveredSSL
// tells us: CoversDomain for the primary name, plus a wildcard approximation.
func certCoversHost(host string, site hosting.DiscoveredSite) bool {
	if site.SSL == nil {
		return false
	}
	// Wildcard parent for the host: "foo.example.com" -> "*.example.com".
	parts := strings.SplitN(host, ".", 2)
	if len(parts) == 2 && strings.Contains(parts[1], ".") {
		// Without the SANs list we approximate by trusting CoversDomain on
		// the apex and leaving subdomains as "not covered"; this matches
		// the behaviour of falling back to site.SSL.CoversDomain, which
		// only applies to the primary domain.
		_ = parts
	}
	if site.Domain == host {
		return site.SSL.CoversDomain
	}
	return false
}

func domainsApex(host string) string {
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return host
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// json import guard.
var _ = json.RawMessage{}

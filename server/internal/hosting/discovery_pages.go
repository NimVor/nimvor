package hosting

// This file ports the parts of DiscoveryService and
// DiscoverySnapshotRepository the /discovery pages read: the scope filter
// (forScope), the snapshot history with the acting user's name (history) and
// the inventory sections the PHP, SSL, cron and overview pages render.
//
// Everything here is read-only. The inventory arrives as the agent's raw
// JSON, so the helpers decode only the sections a page needs and hand back
// the remaining shape untouched (the PHP views read arrays of mixed values).

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// SnapshotHistoryRow is one row of the overview's snapshot history
// (DiscoverySnapshotRepository::history).
type SnapshotHistoryRow struct {
	ID           int64
	CreatedAt    string
	WebsiteCount int
	Checksum     string
	Username     sql.NullString
}

// History returns the newest snapshots for the server with the acting user's
// username, mirroring DiscoverySnapshotRepository::history.
func (r *DiscoverySnapshotRepository) History(ctx context.Context, limit int, serverID int64) ([]SnapshotHistoryRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT s.id, s.created_at, s.website_count, s.checksum, u.username
		 FROM discovery_snapshots s LEFT JOIN users u ON u.id = s.created_by
		 WHERE s.server_id = ? ORDER BY s.id DESC LIMIT ?`, serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SnapshotHistoryRow{}
	for rows.Next() {
		var h SnapshotHistoryRow
		if err := rows.Scan(&h.ID, &h.CreatedAt, &h.WebsiteCount, &h.Checksum, &h.Username); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// DiscoveredCronEntry is one inventory cron job with its source, as the
// /discovery/cron table renders it.
type DiscoveredCronEntry struct {
	Schedule string `json:"schedule"`
	User     string `json:"user"`
	Command  string `json:"command"`
	Source   string `json:"source"`
	Line     int    `json:"line"`
}

// InventoryCronEntries parses the inventory's cron jobs including their
// source and line (the discovery page shows both; the import page does not).
func InventoryCronEntries(data json.RawMessage) []DiscoveredCronEntry {
	var inv struct {
		Cron struct {
			Jobs []DiscoveredCronEntry `json:"jobs"`
		} `json:"cron"`
	}
	out := []DiscoveredCronEntry{}
	if json.Unmarshal(data, &inv) == nil {
		out = inv.Cron.Jobs
	}
	if out == nil {
		out = []DiscoveredCronEntry{}
	}
	return out
}

// inventorySection decodes one top-level section of the inventory into a
// generic map, so a page can iterate the shape the agent produced without
// this package mirroring every field.
func inventorySection(data json.RawMessage, key string) map[string]any {
	var inv map[string]any
	if json.Unmarshal(data, &inv) != nil {
		return map[string]any{}
	}
	if v, ok := inv[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

// InventoryPHPSection returns the inventory's php section.
func InventoryPHPSection(data json.RawMessage) map[string]any {
	return inventorySection(data, "php")
}

// InventoryDatabasesSection returns the inventory's databases section.
func InventoryDatabasesSection(data json.RawMessage) map[string]any {
	return inventorySection(data, "databases")
}

// InventoryOSSection returns the inventory's os section.
func InventoryOSSection(data json.RawMessage) map[string]any {
	return inventorySection(data, "os")
}

// InventoryList returns a top-level inventory array (services, users,
// certificates, …); never nil.
func InventoryList(data json.RawMessage, key string) []any {
	var inv map[string]any
	if json.Unmarshal(data, &inv) != nil {
		return []any{}
	}
	if v, ok := inv[key].([]any); ok {
		return v
	}
	return []any{}
}

// InventorySectionList returns one array of a section (php.pools,
// databases.schemas, cron.jobs, …); never nil.
func InventorySectionList(data json.RawMessage, section, key string) []any {
	if v, ok := inventorySection(data, section)[key].([]any); ok {
		return v
	}
	return []any{}
}

// InventoryWarnings merges the warnings of the sections the overview page
// shows, in the PHP view's order (websites, php, databases, redis, cron).
func InventoryWarnings(data json.RawMessage) []string {
	out := []string{}
	for _, s := range []string{"websites", "php", "databases", "redis", "cron"} {
		if v, ok := inventorySection(data, s)["warnings"].([]any); ok {
			for _, w := range v {
				if str, ok := w.(string); ok && str != "" {
					out = append(out, str)
				}
			}
		}
	}
	return out
}

// InventoryCertificates returns the inventory's certbot lineages.
func InventoryCertificates(data json.RawMessage) []any {
	return InventoryList(data, "certificates")
}

// DiscoveredCertificate is one certificate of the inventory as the SSL page
// renders it.
type DiscoveredCertificate struct {
	Name          string `json:"name"`
	Domains       []string
	Certificate   DiscoveredCertBody `json:"certificate"`
	Authenticator string             `json:"authenticator"`
}

// DiscoveredCertBody is a lineage's certificate details.
type DiscoveredCertBody struct {
	Status        string   `json:"status"`
	Error         string   `json:"error"`
	NotAfter      string   `json:"not_after"`
	Issuer        string   `json:"issuer"`
	DaysRemaining int      `json:"days_remaining"`
	Expired       bool     `json:"expired"`
	KeyExists     bool     `json:"key_exists"`
	KeyWorldRead  bool     `json:"key_world_readable"`
	Names         []string `json:"names"`
}

// InventoryCertificateList parses the certbot lineages the SSL page lists.
func InventoryCertificateList(data json.RawMessage) []DiscoveredCertificate {
	var inv struct {
		Certificates []DiscoveredCertificate `json:"certificates"`
	}
	out := []DiscoveredCertificate{}
	if json.Unmarshal(data, &inv) == nil {
		out = inv.Certificates
	}
	if out == nil {
		out = []DiscoveredCertificate{}
	}
	return out
}

// ForScope narrows an inventory to the domains a non-administrator may see,
// mirroring DiscoveryService::forScope in the same order: the websites whose
// domain they own, the certificates naming one of those domains, the PHP
// pools those websites use, and empty cron/databases/users lists.
//
// The remaining top-level keys are returned unchanged, exactly as PHP does,
// so a page never loses a section it reads.
func ForScope(data json.RawMessage, all bool, ownDomains []string) json.RawMessage {
	if all {
		return data
	}
	var inv map[string]any
	if err := json.Unmarshal(data, &inv); err != nil {
		return data
	}
	own := make(map[string]bool, len(ownDomains))
	for _, d := range ownDomains {
		own[strings.ToLower(d)] = true
	}

	// The owned websites (DiscoveryService::websites + the domain filter).
	sites := []any{}
	pools := map[string]bool{}
	for _, w := range InventoryWebsites(data) {
		if !own[strings.ToLower(w.Domain)] {
			continue
		}
		sites = append(sites, w)
		if w.PHP != nil && w.PHP.Pool != "" {
			pools[w.PHP.Pool] = true
		}
	}
	inv["websites"] = map[string]any{
		"websites":         sites,
		"catch_all_vhosts": 0,
		"warnings":         []any{},
	}

	// Certificates naming one of the owned domains.
	certs := []any{}
	for _, raw := range InventoryCertificates(data) {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		names := []any{}
		if n, ok := c["name"].(string); ok {
			names = append(names, n)
		}
		if d, ok := c["domains"].([]any); ok {
			names = append(names, d...)
		}
		if cb, ok := c["certificate"].(map[string]any); ok {
			if d, ok := cb["names"].([]any); ok {
				names = append(names, d...)
			}
		}
		mine := false
		for _, n := range names {
			if str, ok := n.(string); ok && own[strings.ToLower(str)] {
				mine = true
				break
			}
		}
		if mine {
			certs = append(certs, c)
		}
	}
	inv["certificates"] = certs

	// The PHP pools of the owned websites; warnings dropped.
	php := InventoryPHPSection(data)
	keptPools := []any{}
	for _, raw := range InventorySectionList(data, "php", "pools") {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := p["name"].(string); ok && pools[name] {
			keptPools = append(keptPools, p)
		}
	}
	php["pools"] = keptPools
	// The version cards count the owned pools only, not the server's.
	perVersion := map[string]int{}
	for _, raw := range keptPools {
		if p, ok := raw.(map[string]any); ok {
			if v, ok := p["version"].(string); ok {
				perVersion[v]++
			}
		}
	}
	if versions, ok := php["versions"].([]any); ok {
		scoped := make([]any, 0, len(versions))
		for _, raw := range versions {
			if v, ok := raw.(map[string]any); ok {
				if ver, ok := v["version"].(string); ok {
					c := make(map[string]any, len(v))
					for k, val := range v {
						c[k] = val
					}
					c["pool_count"] = perVersion[ver]
					raw = c
				}
			}
			scoped = append(scoped, raw)
		}
		php["versions"] = scoped
	}
	php["warnings"] = []any{}
	inv["php"] = php

	inv["cron"] = map[string]any{"jobs": []any{}, "warnings": []any{}}
	installed := false
	if v, ok := InventoryDatabasesSection(data)["installed"].(bool); ok {
		installed = v
	}
	inv["databases"] = map[string]any{"installed": installed, "schemas": []any{}, "warnings": []any{}}
	inv["users"] = []any{}

	out, err := json.Marshal(inv)
	if err != nil {
		return data
	}
	return out
}

// InventoryPHPUsage maps each PHP version the owned websites use to their
// domains (the /discovery/php page's $usage).
func InventoryPHPUsage(data json.RawMessage) map[string][]string {
	out := map[string][]string{}
	for _, w := range InventoryWebsites(data) {
		if w.PHP == nil || w.PHP.Version == "" {
			continue
		}
		out[w.PHP.Version] = append(out[w.PHP.Version], w.Domain)
	}
	return out
}

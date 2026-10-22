package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// This file extends WebsiteRepository with the write path, ported from the
// mutating methods of app/Repositories/WebsiteRepository.php.

// SiteOwner is a website id with its owner, as returned by UsingSystemUser.
type SiteOwner struct {
	ID      int64
	OwnerID *int64
}

// fullSelect is the display join extended to every websites column the write
// path needs (mirrors WebsiteRepository::all/find "SELECT w.*" + the derived
// owner flags).
const fullSelect = `SELECT w.id, w.server_id, w.owner_id, w.domain, w.system_user, w.root, w.document_root,
	w.kind, w.app_runtime, w.php_version, w.php_settings, w.proxy_pass,
	w.https, w.force_https, w.hsts, w.ssl_mode, w.cert_file, w.key_file,
	w.redirects, w.client_max_body_mb, w.enabled, w.status, w.last_error, w.applied_at,
	w.directory_listing, w.ipv4, w.ipv6, w.security_headers, w.parent_id, w.bandwidth_applied,
	w.auto_ssl, w.auto_ssl_note,
	w.created_at, w.updated_at,
	u.username AS owner_name, ` + suspendedUserExpr + ` AS account_suspended, ` + bandwidthBlockedExpr + ` AS bandwidth_blocked
	FROM websites w LEFT JOIN users u ON u.id = w.owner_id`

// FindFull returns the complete row of one website (plus aliases), scoped to
// the user. A nil user means no scoping (internal/system use), as in
// WebsiteRepository::find.
func (r *WebsiteRepository) FindFull(ctx context.Context, serverID, id int64, u *identity.User) (*Website, error) {
	cond, params := "1 = 1", []any(nil)
	if u != nil {
		cond, params = Condition(u, "w.owner_id")
	}
	args := append([]any{serverID, id}, params...)
	w, err := scanFull(r.db.QueryRowContext(ctx, fullSelect+` WHERE w.server_id = ? AND w.id = ? AND `+cond, args...))
	if err != nil {
		return nil, err
	}
	aliases, err := r.aliasesOf(ctx, id)
	if err != nil {
		return nil, err
	}
	w.Aliases = aliases
	return w, nil
}

// unmarshalPhpSettings decodes websites.php_settings. Keys the blob lacks are
// filled with the form defaults, mirroring PHP's
// $existing['php_settings'][$k] ?? $def: a legacy/empty blob ('{}') behaves
// like a freshly created site, while a present value (even an invalid one) is
// kept and validated as stored.
func unmarshalPhpSettings(blob string) PhpSettings {
	var ps PhpSettings
	if json.Unmarshal([]byte(blob), &ps) != nil {
		return ps
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(blob), &m) != nil {
		return ps
	}
	fill := func(key string, def int, cur *int) {
		if _, ok := m[key]; !ok {
			*cur = def
		}
	}
	fill("memory_limit_mb", 256, &ps.MemoryLimitMB)
	fill("upload_max_mb", 64, &ps.UploadMaxMB)
	fill("max_execution_seconds", 60, &ps.MaxExecutionSeconds)
	fill("max_children", 5, &ps.MaxChildren)
	fill("start_servers", 0, &ps.StartServers)
	fill("min_spare_servers", 0, &ps.MinSpareServers)
	fill("max_spare_servers", 0, &ps.MaxSpareServers)
	fill("idle_timeout_seconds", 10, &ps.IdleTimeoutSeconds)
	fill("max_requests", 500, &ps.MaxRequests)
	fill("request_timeout_seconds", 0, &ps.RequestTimeoutSeconds)
	fill("slowlog_seconds", 10, &ps.SlowlogSeconds)
	if _, ok := m["pm"]; !ok {
		ps.PM = "ondemand"
	}
	return ps
}

func (r *WebsiteRepository) aliasesOf(ctx context.Context, websiteID int64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT name FROM domains WHERE website_id = ? AND is_primary = 0 ORDER BY id", websiteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AliasesFor returns the alias names (non-primary domains) of the given
// websites in one query — the ModSecurity page's host → website map (PHP's
// hydrate() asks per website; the result is the same).
func (r *WebsiteRepository) AliasesFor(ctx context.Context, ids []int64) (map[int64][]string, error) {
	out := map[int64][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := r.db.QueryContext(ctx,
		"SELECT website_id, name FROM domains WHERE website_id IN ("+ph+") AND is_primary = 0 ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var websiteID int64
		var name string
		if err := rows.Scan(&websiteID, &name); err != nil {
			return nil, err
		}
		out[websiteID] = append(out[websiteID], name)
	}
	return out, rows.Err()
}

func scanFull(row interface{ Scan(...any) error }) (*Website, error) {
	var w Website
	var https, forceHTTPS, hsts, enabled, dirListing, secHeaders, bwApplied, suspended, bwBlocked int
	var phpSettings, redirects string
	if err := row.Scan(&w.ID, &w.ServerID, &w.OwnerID, &w.Domain, &w.SystemUser, &w.Root, &w.DocumentRoot,
		&w.Kind, &w.AppRuntime, &w.PhpVersion, &phpSettings, &w.ProxyPass,
		&https, &forceHTTPS, &hsts, &w.SSLMode, &w.CertFile, &w.KeyFile,
		&redirects, &w.ClientMaxBodyMB, &enabled, &w.Status, &w.LastError, &w.AppliedAt,
		&dirListing, &w.IPv4, &w.IPv6, &secHeaders, &w.ParentID, &bwApplied,
		&w.AutoSSL, &w.AutoSSLNote,
		&w.CreatedAt, &w.UpdatedAt, &w.OwnerName, &suspended, &bwBlocked); err != nil {
		return nil, err
	}
	w.HTTPS = https == 1
	w.ForceHTTPS = forceHTTPS == 1
	w.HSTS = hsts == 1
	w.Enabled = enabled == 1
	w.DirectoryListing = dirListing == 1
	w.SecurityHeaders = secHeaders == 1
	w.BandwidthApplied = bwApplied == 1
	w.AccountSuspended = suspended == 1
	w.BandwidthBlocked = bwBlocked == 1
	w.PhpSettings = unmarshalPhpSettings(phpSettings)
	var rs []Redirect
	if err := json.Unmarshal([]byte(redirects), &rs); err != nil {
		rs = []Redirect{}
	}
	w.Redirects = rs
	return &w, nil
}

// Insert adds a website row and adopts any unlinked DNS zone or mail domain
// matching its domain (WebsiteRepository::insert + ::adopt).
func (r *WebsiteRepository) Insert(ctx context.Context, serverID int64, row *Website) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	php, err := json.Marshal(row.PhpSettings)
	if err != nil {
		return 0, err
	}
	redirects, err := json.Marshal(row.Redirects)
	if err != nil {
		return 0, err
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO websites (
		server_id, owner_id, domain, system_user, root, document_root, kind, app_runtime,
		php_version, php_settings, proxy_pass, https, force_https, hsts, ssl_mode, cert_file,
		key_file, redirects, client_max_body_mb, enabled, status, directory_listing, ipv4,
		ipv6, security_headers, parent_id, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		serverID, nullOwner(row.OwnerID), row.Domain, row.SystemUser, row.Root, row.DocumentRoot,
		row.Kind, row.AppRuntime, row.PhpVersion, string(php), row.ProxyPass,
		boolInt(row.HTTPS), boolInt(row.ForceHTTPS), boolInt(row.HSTS), row.SSLMode, row.CertFile,
		row.KeyFile, string(redirects), row.ClientMaxBodyMB, boolInt(row.Enabled), row.Status,
		boolInt(row.DirectoryListing), nullStr(row.IPv4), nullStr(row.IPv6),
		boolInt(row.SecurityHeaders), nullParent(row.ParentID), now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := r.adopt(ctx, id); err != nil {
		return 0, err
	}
	return id, nil
}

// adopt links a DNS zone or mail domain created earlier for exactly this
// website's domain (not linked, owned by nobody else) to the website, as if it
// had been chosen when creating it (WebsiteRepository::adopt).
func (r *WebsiteRepository) adopt(ctx context.Context, id int64) error {
	var serverID int64
	var domain string
	var ownerID sql.NullInt64
	if err := r.db.QueryRowContext(ctx,
		"SELECT server_id, domain, owner_id FROM websites WHERE id = ?", id).Scan(&serverID, &domain, &ownerID); err != nil {
		return err
	}
	owner := any(nil)
	if ownerID.Valid {
		owner = ownerID.Int64
	}
	for _, table := range []struct{ table, col string }{
		{"dns_zones", "name"},
		{"mail_domains", "domain"},
	} {
		if _, err := r.db.ExecContext(ctx,
			"UPDATE "+table.table+` SET website_id = ?, owner_id = COALESCE(owner_id, ?)
			 WHERE server_id = ? AND `+table.col+` = ? AND website_id IS NULL AND (owner_id IS NULL OR owner_id IS ?)`,
			id, owner, serverID, domain, owner); err != nil {
			return err
		}
	}
	return nil
}

// UpdateFields sets arbitrary columns (arrays JSON-encoded, bools as 0/1) and
// bumps updated_at (WebsiteRepository::update). "id", "created_at",
// "owner_name" and "aliases" are ignored.
func (r *WebsiteRepository) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	cols := []string{}
	args := []any{}
	for k, v := range fields {
		switch k {
		case "id", "created_at", "owner_name", "aliases":
			continue
		}
		switch t := v.(type) {
		case bool:
			args = append(args, boolInt(t))
		case []byte:
			args = append(args, string(t))
		case string:
			args = append(args, t)
		case int:
			args = append(args, t)
		case int64:
			args = append(args, t)
		case float64:
			args = append(args, t)
		case nil:
			args = append(args, nil)
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return err
			}
			args = append(args, string(b))
		}
		cols = append(cols, k+" = ?")
	}
	if len(cols) == 0 {
		return nil
	}
	args = append(args, id)
	_, err := r.db.ExecContext(ctx,
		"UPDATE websites SET "+strings.Join(cols, ", ")+", updated_at = ? WHERE id = ?",
		append(args, time.Now().UTC().Format(time.RFC3339))...)
	return err
}

// ReplaceDomains rewrites a website's domain rows (primary first)
// (WebsiteRepository::replaceDomains).
func (r *WebsiteRepository) ReplaceDomains(ctx context.Context, serverID, websiteID int64, names []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM domains WHERE website_id = ?", websiteID); err != nil {
		return err
	}
	for i, n := range names {
		primary := 0
		if i == 0 {
			primary = 1
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (?,?,?,?)",
			serverID, websiteID, n, primary); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UsingSystemUser lists the website ids (with owners) run by a system account
// (WebsiteRepository::usingSystemUser).
func (r *WebsiteRepository) UsingSystemUser(ctx context.Context, serverID int64, user string) ([]SiteOwner, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT id, owner_id FROM websites WHERE server_id = ? AND system_user = ?", serverID, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SiteOwner{}
	for rows.Next() {
		var so SiteOwner
		var owner sql.NullInt64
		if err := rows.Scan(&so.ID, &owner); err != nil {
			return nil, err
		}
		if owner.Valid {
			v := owner.Int64
			so.OwnerID = &v
		}
		out = append(out, so)
	}
	return out, rows.Err()
}

// DomainOwner returns the website id owning a hostname, or 0 (WebsiteRepository::domainOwner).
func (r *WebsiteRepository) DomainOwner(ctx context.Context, serverID int64, name string) (int64, error) {
	var id sql.NullInt64
	err := r.db.QueryRowContext(ctx,
		"SELECT website_id FROM domains WHERE server_id = ? AND name = ?", serverID, name).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !id.Valid {
		return 0, nil
	}
	return id.Int64, nil
}

// SetStatus records the applied state; applied_at only advances on success
// (WebsiteRepository::setStatus).
func (r *WebsiteRepository) SetStatus(ctx context.Context, id int64, status, lastError string) error {
	if len(lastError) > 2000 {
		lastError = lastError[:2000]
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`UPDATE websites SET status = ?, last_error = ?,
		 applied_at = CASE WHEN ? = '' THEN ? ELSE applied_at END,
		 updated_at = ? WHERE id = ?`,
		status, lastError, lastError, now, now, id)
	return err
}

// SetBandwidthApplied records whether the server has the website answering 509
// (WebsiteRepository::setBandwidthApplied).
func (r *WebsiteRepository) SetBandwidthApplied(ctx context.Context, id int64, blocked bool) error {
	_, err := r.db.ExecContext(ctx, "UPDATE websites SET bandwidth_applied = ? WHERE id = ?", boolInt(blocked), id)
	return err
}

// DeleteSite removes a website row (its domain rows cascade)
// (WebsiteRepository::delete).
func (r *WebsiteRepository) DeleteSite(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM websites WHERE id = ?", id)
	return err
}

func nullOwner(p sql.NullInt64) any {
	if p.Valid {
		return p.Int64
	}
	return nil
}

func nullParent(p sql.NullInt64) any {
	if p.Valid {
		return p.Int64
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

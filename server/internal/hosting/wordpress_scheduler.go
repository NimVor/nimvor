package hosting

// The two WordPress passes of the scheduler (WordPressToolsService::
// dailyVulnerabilityCheck and ::autoHarden): the daily vulnerability check of
// installations with vulnerability protection, and the recommended security
// measures for WordPress installed with the app installer.

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// WordPressScheduler runs the scheduled WordPress work.
type WordPressScheduler struct {
	db       *sql.DB
	serverID int64
	sites    *WebsiteRepository
	wp       *WordPressService
	tools    *WordPressTools

	// Notify stores a notification for the website's owner.
	Notify func(ctx context.Context, websiteID, ownerID sql.NullInt64, kind, severity, title, body, link, key string)
}

// NewWordPressScheduler builds the scheduled WordPress work.
func NewWordPressScheduler(db *sql.DB, serverID int64, sites *WebsiteRepository, wp *WordPressService, tools *WordPressTools) *WordPressScheduler {
	return &WordPressScheduler{db: db, serverID: serverID, sites: sites, wp: wp, tools: tools}
}

func wpWhere(domain, path string) string {
	if path != "" {
		return domain + "/" + path
	}
	return domain
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// DailyVulnerabilityCheck checks up to 20 installations with vulnerability
// protection that were last checked more than 20 hours ago; vulnerabilities
// not seen before are notified to the website's owner. One line per
// installation.
func (s *WordPressScheduler) DailyVulnerabilityCheck(ctx context.Context) []string {
	cutoff := time.Now().UTC().Add(-20 * time.Hour).Format("2006-01-02T15:04:05Z")
	type due struct{ id, siteID int64 }
	var list []due
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, website_id FROM wordpress_installs
		 WHERE server_id = ? AND vuln_protection = 1 AND (vulns_checked_at IS NULL OR vulns_checked_at < ?)
		 ORDER BY vulns_checked_at LIMIT 20`, s.serverID, cutoff)
	if err != nil {
		return []string{"WordPress vulnerabilities not checked: " + err.Error()}
	}
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.siteID); err != nil {
			rows.Close()
			return []string{"WordPress vulnerabilities not checked: " + err.Error()}
		}
		list = append(list, d)
	}
	rows.Close()

	var out []string
	for _, d := range list {
		site, err := s.sites.Find(ctx, s.serverID, d.siteID, nil)
		if err != nil {
			continue
		}
		install, err := s.wp.Install(ctx, site, d.id)
		if err != nil {
			continue
		}
		where := wpWhere(site.Domain, install.Path)
		info, err := s.wp.Info(ctx, site, install, false)
		if err != nil {
			out = append(out, "WordPress "+where+": not checked ("+err.Error()+")")
			continue
		}
		before := map[string]bool{}
		for _, v := range WpStoredVulnerabilitiesOf(install).Items {
			before[v.Name] = true
		}
		items, reachable, _, err := s.tools.CheckVulnerabilities(install, info)
		if err != nil {
			out = append(out, "WordPress "+where+": not checked ("+err.Error()+")")
			continue
		}
		var fresh []WpVulnItem
		var names []string
		for _, v := range items {
			if !before[v.Name] {
				fresh = append(fresh, v)
				names = append(names, v.Name)
			}
		}
		if len(fresh) > 0 && s.Notify != nil {
			var lines []string
			for i, v := range fresh {
				if i == 10 {
					break
				}
				line := v.Title + ": " + v.Name
				if v.FixedIn != "" {
					line += " (fixed in " + v.FixedIn + ")"
				}
				lines = append(lines, line)
			}
			sum := sha1.Sum([]byte(strings.Join(names, "|")))
			s.Notify(ctx, sql.NullInt64{Int64: site.ID, Valid: true}, site.OwnerID, "wordpress", "warning",
				fmt.Sprintf("%d new %s in WordPress on %s", len(fresh), plural(len(fresh), "vulnerability", "vulnerabilities"), where),
				strings.Join(lines, "\n"),
				"/sites/"+strconv.FormatInt(site.ID, 10)+"/wordpress/"+strconv.FormatInt(install.ID, 10),
				"wp-vulns:"+strconv.FormatInt(install.ID, 10)+":"+hex.EncodeToString(sum[:]))
		}
		line := fmt.Sprintf("WordPress %s: %d known %s", where, len(items), plural(len(items), "vulnerability", "vulnerabilities"))
		if !reachable {
			line += " (database partly unreachable)"
		}
		out = append(out, line)
	}
	return out
}

// AutoHarden gives WordPress installed with the app installer the
// recommended security measures once its installation job has settled (at
// most 10 per pass). Every row is marked hardened either way, so a failed
// installation is not retried; with the setting off nothing is applied.
func (s *WordPressScheduler) AutoHarden(ctx context.Context, enabled bool) []string {
	type pending struct {
		id, siteID, dbID int64
		path, jobStatus  string
	}
	var list []pending
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.website_id, COALESCE(a.database_id, 0), a.path, j.status
		 FROM app_installs a JOIN websites w ON w.id = a.website_id
		 JOIN agent_jobs j ON j.job_id = a.job_id AND j.server_id = w.server_id
		 WHERE w.server_id = ? AND a.app = 'wordpress' AND a.hardened = 0
		   AND j.status IN ('succeeded', 'failed', 'unknown') LIMIT 10`, s.serverID)
	if err != nil {
		return []string{"WordPress not hardened: " + err.Error()}
	}
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.siteID, &p.dbID, &p.path, &p.jobStatus); err != nil {
			rows.Close()
			return []string{"WordPress not hardened: " + err.Error()}
		}
		list = append(list, p)
	}
	rows.Close()

	var out []string
	for _, a := range list {
		if _, err := s.db.ExecContext(ctx, `UPDATE app_installs SET hardened = 1 WHERE id = ?`, a.id); err != nil {
			out = append(out, "WordPress not hardened: "+err.Error())
			continue
		}
		if !enabled || a.jobStatus != "succeeded" {
			continue
		}
		site, err := s.sites.Find(ctx, s.serverID, a.siteID, nil)
		if err != nil {
			continue
		}
		var dbName string
		_ = s.db.QueryRowContext(ctx, `SELECT name FROM databases WHERE id = ?`, a.dbID).Scan(&dbName)
		now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO wordpress_installs (server_id, website_id, path, db_name, scanned_at) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (website_id, path) DO UPDATE SET db_name = excluded.db_name`,
			s.serverID, site.ID, a.path, dbName, now); err != nil {
			out = append(out, "WordPress "+wpWhere(site.Domain, a.path)+": not hardened: "+err.Error())
			continue
		}
		var installID int64
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM wordpress_installs WHERE website_id = ? AND path = ?`, site.ID, a.path).Scan(&installID); err != nil {
			out = append(out, "WordPress "+wpWhere(site.Domain, a.path)+": not hardened: "+err.Error())
			continue
		}
		install, err := s.wp.Install(ctx, site, installID)
		if err != nil {
			out = append(out, "WordPress "+wpWhere(site.Domain, a.path)+": not hardened: "+err.Error())
			continue
		}
		res, err := s.tools.Security(ctx, site, install, "apply", WpRecommendedMeasures)
		where := wpWhere(site.Domain, a.path)
		if err != nil {
			out = append(out, "WordPress "+where+": not hardened: "+err.Error())
			continue
		}
		line := "WordPress " + where + ": hardened"
		if len(res.Problems) > 0 {
			line += " (" + strings.Join(res.Problems, "; ") + ")"
		}
		out = append(out, line)
	}
	return out
}

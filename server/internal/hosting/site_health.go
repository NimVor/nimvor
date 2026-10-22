package hosting

// This file ports the per-website security and health checks
// (SiteHealthService): plain results the site's Security and Health pages
// render with a status, an explanation and how to fix it (and whether the
// customer can fix it themselves or should ask their provider). Everything
// reads the panel's own records; the one agent-backed lookup (the
// certificate) already fails safe.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SiteCheck is one row of the site security/health pages
// (SiteHealthService::c).
type SiteCheck struct {
	Level  string // "ok", "warn" or "bad"
	Title  string
	Detail string
	Fix    string
	Where  string // "you" (the customer) or "admin" (the hosting provider)
}

// SiteHealthService computes the per-website security and health checks.
type SiteHealthService struct {
	db  *sql.DB
	ssl *SslService
}

// NewSiteHealthService builds the service.
func NewSiteHealthService(db *sql.DB, ssl *SslService) *SiteHealthService {
	return &SiteHealthService{db: db, ssl: ssl}
}

// Security lists the website's security checks (SitesController::security).
func (s *SiteHealthService) Security(ctx context.Context, site *Website) ([]SiteCheck, error) {
	checks := []SiteCheck{}
	if !site.HTTPS {
		checks = append(checks, siteCheck("bad", "HTTPS is off",
			"Visitors reach your site over an unencrypted connection.",
			"Issue a free certificate on the SSL / HTTPS page, then turn on Force HTTPS.", "you"))
	} else if !site.ForceHTTPS {
		checks = append(checks, siteCheck("warn", "Visitors can still use plain http://",
			"HTTPS works, but requests are not redirected to it.",
			"Turn on Force HTTPS on the SSL / HTTPS page.", "you"))
	} else {
		checks = append(checks, siteCheck("ok", "HTTPS is on and enforced",
			"All traffic is encrypted and redirected to HTTPS.", "", "you"))
	}

	if site.HTTPS {
		// The agent lookup fails safe: no reply means no certificate line.
		if cert := s.ssl.Certificate(ctx, site); cert != nil {
			days := cert.DaysRemaining
			if days <= 15 {
				level := "warn"
				if days <= 3 {
					level = "bad"
				}
				checks = append(checks, siteCheck(level, "Your certificate expires soon",
					fmt.Sprintf("It is valid for %d more day(s).", max(0, days)),
					"Let's Encrypt certificates renew automatically; if you uploaded your own, upload a fresh one on the SSL / HTTPS page.", "you"))
			} else {
				checks = append(checks, siteCheck("ok", "Certificate is valid",
					fmt.Sprintf("Valid for %d more day(s).", days), "", "you"))
			}
		}
	}

	var infected int
	var scanned bool
	err := s.db.QueryRowContext(ctx,
		`SELECT infected FROM av_scans WHERE website_id = ? ORDER BY id DESC LIMIT 1`, site.ID).Scan(&infected)
	if err == nil {
		scanned = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	switch {
	case scanned && infected > 0:
		checks = append(checks, siteCheck("bad", "Malware was found in your files",
			fmt.Sprintf("%d infected file(s) in the last scan.", infected),
			"Review and remove them on the Malware scan page, then rescan.", "you"))
	case !scanned:
		checks = append(checks, siteCheck("warn", "No malware scan has run yet",
			"Your files have not been scanned for malware.",
			"Run a scan on the Malware scan page.", "you"))
	default:
		checks = append(checks, siteCheck("ok", "No malware found",
			"The last scan found nothing.", "", "you"))
	}

	if site.Kind == "php" && site.PhpVersion != "" && phpVersionOlderThan(site.PhpVersion, "8.1") {
		checks = append(checks, siteCheck("warn", "PHP "+site.PhpVersion+" no longer gets security updates",
			"Old PHP versions stop receiving fixes.",
			"Choose PHP 8.1 or newer under PHP & settings (check your apps support it first).", "you"))
	}
	return checks, nil
}

// Health lists the website's health checks (SitesController::health).
func (s *SiteHealthService) Health(ctx context.Context, site *Website) ([]SiteCheck, error) {
	checks := []SiteCheck{}
	if !site.Enabled {
		checks = append(checks, siteCheck("bad", "Your website is turned off",
			"It is not being served to visitors.",
			"Ask your hosting provider to re-enable it.", "admin"))
	} else {
		checks = append(checks, siteCheck("ok", "Your website is active",
			"It is enabled and served.", "", "you"))
	}

	var createdAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT created_at FROM backups WHERE website_id = ? AND status = 'completed' ORDER BY id DESC LIMIT 1`,
		site.ID).Scan(&createdAt)
	if err == nil {
		checks = append(checks, siteCheck("ok", "You have a recent backup",
			"Last backup: "+createdAt, "", "you"))
	} else if errors.Is(err, sql.ErrNoRows) {
		checks = append(checks, siteCheck("warn", "No successful backup yet",
			"You have no backup to restore from if something goes wrong.",
			"Create a backup on the Backups page.", "you"))
	} else {
		return nil, err
	}
	return checks, nil
}

func siteCheck(level, title, detail, fix, where string) SiteCheck {
	return SiteCheck{Level: level, Title: title, Detail: detail, Fix: fix, Where: where}
}

// phpVersionOlderThan reports whether v sorts strictly before other the way
// PHP's version_compare(v, other, "<") does for the plain numeric versions
// stored per website ("8.0.30", "8.3.1"). Missing parts count as zero, so
// "8.0" is older than "8.1" while "8.1.0" is not older than "8.1".
func phpVersionOlderThan(v, other string) bool {
	a, b := versionParts(v), versionParts(other)
	for i := 0; i < len(a) || i < len(b); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			return av < bv
		}
	}
	return false
}

// versionParts splits a dotted version into its numeric parts, stopping at
// the first non-numeric part (a suffix PHP's version_compare would also
// order as "release" — the panel never stores one).
func versionParts(v string) []int {
	var out []int
	for _, part := range strings.Split(v, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}

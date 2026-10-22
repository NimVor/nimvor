package hosting

// This file ports SiteOverview: the read-only summary of a website for its
// dashboard — counts and states from the panel's own records (no agent
// calls in summary), plus the two agent-backed helpers (the server's
// address and the live database sizes), each scoped like the page it
// links to and failing safe.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// SiteMailSummary is the summary's mail entry (a hosted domain attached to
// the website).
type SiteMailSummary struct {
	ID        int64
	Mailboxes int
	Aliases   int
}

// SiteDnsSummary is the summary's DNS entry (a hosted zone attached to the
// website).
type SiteDnsSummary struct {
	ID      int64
	Records int
}

// SiteGitSummary is the summary's git entry (one repository).
type SiteGitSummary struct {
	Repo   string
	Branch string
}

// SiteSummary is the dashboard summary (SiteOverview::summary).
type SiteSummary struct {
	Databases       int
	Mail            *SiteMailSummary
	Dns             *SiteDnsSummary
	Cron            int
	Workers         int
	Git             *SiteGitSummary
	Backups         int
	LastBackup      string // created_at of the newest completed backup ("" = none)
	BackupSchedules int
	Subdomains      int
	Addons          int
	Apps            []string
}

// SiteOverview reads a website's dashboard data.
type SiteOverview struct {
	db        *sql.DB
	agent     AgentCaller
	cfg       *settings.Repository
	dbService *DatabaseService
}

// NewSiteOverview builds the service.
func NewSiteOverview(db *sql.DB, agent AgentCaller, cfg *settings.Repository, dbService *DatabaseService) *SiteOverview {
	return &SiteOverview{db: db, agent: agent, cfg: cfg, dbService: dbService}
}

// Summary computes the dashboard counts (SiteOverview::summary).
func (s *SiteOverview) Summary(ctx context.Context, serverID int64, site *Website, u *identity.User) (*SiteSummary, error) {
	id := site.ID
	out := &SiteSummary{Apps: []string{}}
	scanInt := func(dest *int, q string, args ...any) error {
		return s.db.QueryRowContext(ctx, q, args...).Scan(dest)
	}

	cond, params := "1 = 1", []any(nil)
	if u != nil {
		cond, params = Condition(u, "owner_id")
	}
	// The website's databases (scoped like the databases page).
	if err := scanInt(&out.Databases,
		`SELECT COUNT(*) FROM databases WHERE server_id = ? AND website_id = ? AND `+cond,
		append([]any{serverID, id}, params...)...); err != nil {
		return nil, err
	}
	if err := scanInt(&out.Cron, `SELECT COUNT(*) FROM cron_jobs WHERE website_id = ?`, id); err != nil {
		return nil, err
	}
	if err := scanInt(&out.Workers, `SELECT COUNT(*) FROM workers WHERE website_id = ?`, id); err != nil {
		return nil, err
	}
	if err := scanInt(&out.Backups, `SELECT COUNT(*) FROM backups WHERE website_id = ?`, id); err != nil {
		return nil, err
	}
	if err := scanInt(&out.BackupSchedules, `SELECT COUNT(*) FROM backup_schedules WHERE website_id = ?`, id); err != nil {
		return nil, err
	}
	if err := scanInt(&out.Subdomains,
		`SELECT COUNT(*) FROM websites WHERE parent_id = ? AND domain LIKE ?`, id, "%."+site.Domain); err != nil {
		return nil, err
	}
	if err := scanInt(&out.Addons,
		`SELECT COUNT(*) FROM websites WHERE parent_id = ? AND domain NOT LIKE ?`, id, "%."+site.Domain); err != nil {
		return nil, err
	}
	var (
		repo   string
		branch string
	)
	if err := s.db.QueryRowContext(ctx,
		`SELECT repo, branch FROM git_repositories WHERE website_id = ?`, id).Scan(&repo, &branch); err == nil {
		out.Git = &SiteGitSummary{Repo: repo, Branch: branch}
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT created_at FROM backups WHERE website_id = ? AND status = 'completed' ORDER BY id DESC LIMIT 1`, id).
		Scan(&out.LastBackup); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT app FROM app_installs WHERE website_id = ? AND status = 'succeeded' ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var app string
		if rows.Scan(&app) != nil {
			continue
		}
		if !seen[app] {
			seen[app] = true
			out.Apps = append(out.Apps, app)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Mail: a hosted domain attached to the website, visible to the user
	// like the mail page (MailService::forWebsite + ::domain).
	var mailID int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM mail_domains WHERE website_id = ?`, id).Scan(&mailID)
	if err == nil {
		cond, params := "1 = 1", []any(nil)
		if u != nil {
			cond, params = WithWebsite(u, "owner_id", "website_id")
		}
		var checked int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT id FROM mail_domains WHERE id = ? AND server_id = ? AND `+cond,
			append([]any{mailID, serverID}, params...)...).Scan(&checked); err == nil {
			var mailboxes, aliases int
			if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_mailboxes WHERE domain_id = ?`, mailID).Scan(&mailboxes) == nil &&
				s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_aliases WHERE domain_id = ?`, mailID).Scan(&aliases) == nil {
				out.Mail = &SiteMailSummary{ID: mailID, Mailboxes: mailboxes, Aliases: aliases}
			}
		} else if err != sql.ErrNoRows {
			return nil, err
		}
	} else if err != sql.ErrNoRows {
		return nil, err
	}

	// DNS: a hosted zone attached to the website, visible like the DNS
	// page (DnsService::forWebsite + ::zone).
	var zoneID int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM dns_zones WHERE website_id = ?`, id).Scan(&zoneID)
	if err == nil {
		cond, params := "1 = 1", []any(nil)
		if u != nil {
			cond, params = WithWebsite(u, "owner_id", "website_id")
		}
		var checked int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT id FROM dns_zones WHERE id = ? AND server_id = ? AND `+cond,
			append([]any{zoneID, serverID}, params...)...).Scan(&checked); err == nil {
			var records int
			if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dns_records WHERE zone_id = ?`, zoneID).Scan(&records) == nil {
				out.Dns = &SiteDnsSummary{ID: zoneID, Records: records}
			}
		} else if err != sql.ErrNoRows {
			return nil, err
		}
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	return out, nil
}

// ServerIP is the address the website answers on (SiteOverview::serverIp):
// the configured default IPv4 when it is one, otherwise the server's first
// global IPv4 address from the agent ("", false when it cannot be asked).
func (s *SiteOverview) ServerIP(ctx context.Context) (string, bool) {
	ip, _ := s.cfg.String(ctx, "dns_default_ipv4", "")
	ip = strings.TrimSpace(ip)
	if isIPv4(ip) {
		return ip, true
	}
	res, err := s.agent.Call(ctx, "system.info", nil)
	if err != nil {
		return "", false
	}
	var info struct {
		System struct {
			Interfaces []struct {
				Addresses []string `json:"addresses"`
			} `json:"interfaces"`
		} `json:"system"`
	}
	if json.Unmarshal(res, &info) != nil {
		return "", false
	}
	for _, iface := range info.System.Interfaces {
		for _, a := range iface.Addresses {
			a = strings.SplitN(a, "/", 2)[0]
			if isIPv4(a) && !strings.HasPrefix(a, "127.") && !reservedIPv4(a) {
				return a, true
			}
		}
	}
	return "", false
}

// isIPv4 reports whether s is an IPv4 dotted address (FILTER_VALIDATE_IP |
// FILTER_FLAG_IPV4).
func isIPv4(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && ip.To4() != nil
}

// reservedIPv4 reports whether s falls in a range the panel will not pick
// automatically (FILTER_FLAG_NO_RES_RANGE, except loopback which the caller
// checks itself).
func reservedIPv4(s string) bool {
	ip4 := net.ParseIP(s).To4()
	if ip4 == nil {
		return true
	}
	o := ip4[0]
	return o == 0 || o >= 224 || (o == 169 && ip4[1] == 254)
}

// DatabaseBytes is the total size of the website's databases, or nil when
// the database server cannot be asked (SiteOverview::databaseBytes). 0
// when the website has no linked databases.
func (s *SiteOverview) DatabaseBytes(ctx context.Context, serverID int64, site *Website, u *identity.User) (*int64, error) {
	cond, params := "1 = 1", []any(nil)
	if u != nil {
		cond, params = Condition(u, "owner_id")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT name FROM databases WHERE server_id = ? AND website_id = ? AND `+cond,
		append([]any{serverID, site.ID}, params...)...)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			names[name] = true
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		zero := int64(0)
		return &zero, nil
	}
	live, err := s.dbService.Live(ctx)
	if err != nil {
		return nil, err
	}
	if live == nil {
		return nil, nil
	}
	total := int64(0)
	for _, d := range live.Databases {
		if names[d.Name] {
			total += d.Bytes
		}
	}
	return &total, nil
}

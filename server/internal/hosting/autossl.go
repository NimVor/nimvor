package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// AutoSSL: a new website waits until its domain points to this server, then
// gets a Let's Encrypt certificate and HTTPS (AutoSslService). The domain
// points here when its name servers are all this panel's or its address (A
// record) is this server's. A website still not pointing here after
// AutoSSLWaitDays, or refused AutoSSLMaxTries times, is left for the SSL
// page. Websites that already use HTTPS are checked too: one whose
// certificate has expired, expires within AutoSSLReplaceDays unrenewed, does
// not cover its domain or is self-signed is queued ('renew'). Every step is
// written to autossl_log.
const (
	AutoSSLWaitDays    = 3
	AutoSSLMaxTries    = 6
	autoSSLPerPass     = 5
	AutoSSLReplaceDays = 7
	autoSSLReplaceEvry = 6 * time.Hour
	// AutoSSLLastReplaceKey records when the scheduler last looked for
	// certificates to replace.
	AutoSSLLastReplaceKey = "_autossl_last_replace_check"
	// SchedulerRunKey records when the scheduler last ran (Doctor::SCHEDULER_KEY).
	SchedulerRunKey = "_scheduler_last_run"
)

// DNSLookup answers the two DNS questions AutoSSL asks.
type DNSLookup interface {
	A(ctx context.Context, name string) []string
	NS(ctx context.Context, name string) []string
}

type netLookup struct{}

func (netLookup) A(ctx context.Context, name string) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		if v4 := a.IP.To4(); v4 != nil {
			out = append(out, v4.String())
		}
	}
	return out
}

func (netLookup) NS(ctx context.Context, name string) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	recs, err := net.DefaultResolver.LookupNS(ctx, name)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range recs {
		out = append(out, strings.TrimRight(strings.ToLower(r.Host), "."))
	}
	return out
}

// AutoSslService is the queue and worker behind the AutoSSL page.
type AutoSslService struct {
	db       *sql.DB
	serverID int64
	cfg      *settings.Repository
	agent    AgentCaller
	ssl      *SslService
	sites    *WebsiteRepository
	dns      *DnsService
	overview *SiteOverview
	lookup   DNSLookup
	now      func() time.Time
}

// NewAutoSslService builds the service.
func NewAutoSslService(db *sql.DB, serverID int64, cfg *settings.Repository, agent AgentCaller, ssl *SslService,
	sites *WebsiteRepository, dns *DnsService, overview *SiteOverview) *AutoSslService {
	return &AutoSslService{db: db, serverID: serverID, cfg: cfg, agent: agent, ssl: ssl, sites: sites, dns: dns, overview: overview,
		lookup: netLookup{}, now: time.Now}
}

func (s *AutoSslService) stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func (s *AutoSslService) bool(ctx context.Context, key string) bool {
	v, err := s.cfg.Values(ctx)
	return err == nil && (v[key] == "1" || v[key] == "true")
}

// Mark queues a new website until its domain points here (AutoSslService::mark).
func (s *AutoSslService) Mark(ctx context.Context, site *Website) {
	if !s.bool(ctx, "auto_ssl") || site.HTTPS || !site.Enabled {
		return
	}
	s.note(ctx, site.ID, "pending", 0, s.stamp(s.now()), "Waiting for the domain to point to this server.")
}

func (s *AutoSslService) note(ctx context.Context, id int64, state string, tries int, next, note string) {
	var n any
	if next != "" {
		n = next
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE websites SET auto_ssl = ?, auto_ssl_tries = ?, auto_ssl_next = ?, auto_ssl_note = ? WHERE id = ?`, state, tries, n, note, id)
}

// Log writes one line of the AutoSSL log (the newest 2000 per server are kept).
func (s *AutoSslService) Log(ctx context.Context, websiteID int64, domain, result, message string) {
	if r := []rune(message); len(r) > 1000 {
		message = string(r[:1000])
	}
	var w any
	if websiteID != 0 {
		w = websiteID
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO autossl_log (server_id, website_id, domain, result, message, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		s.serverID, w, domain, result, message, s.stamp(s.now()))
	_, _ = s.db.ExecContext(ctx, `DELETE FROM autossl_log WHERE server_id = ? AND id <= (SELECT id FROM autossl_log WHERE server_id = ? ORDER BY id DESC LIMIT 1 OFFSET 2000)`, s.serverID, s.serverID)
}

// storeHost is one entry of ssl.store's hosts.
type storeHost struct {
	Name        string `json:"name"`
	HTTPS       bool   `json:"https"`
	Covered     bool   `json:"covered"`
	Certificate *struct {
		Error         string `json:"error"`
		Expired       bool   `json:"expired"`
		SelfSigned    bool   `json:"self_signed"`
		DaysRemaining int    `json:"days_remaining"`
	} `json:"certificate"`
}

// ReplaceReason says why a website's certificate should be replaced ("" when
// it is fine).
func ReplaceReason(h *storeHost) string {
	if h == nil || !h.HTTPS || h.Certificate == nil {
		return ""
	}
	c := h.Certificate
	switch {
	case c.Error != "":
		return "the certificate cannot be read"
	case c.Expired:
		return "the certificate has expired"
	case !h.Covered:
		return "the certificate does not cover " + h.Name
	case c.SelfSigned:
		return "the certificate is self-signed (browsers warn)"
	case c.DaysRemaining <= AutoSSLReplaceDays:
		return fmt.Sprintf("the certificate expires in %d days and was not renewed", c.DaysRemaining)
	}
	return ""
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Queue queues websites for a certificate: those without HTTPS (all) and those
// whose certificate should be replaced. It returns how many were queued.
func (s *AutoSslService) Queue(ctx context.Context, all bool) (int, error) {
	raw, err := s.agent.Call(ctx, "ssl.store", map[string]any{}, 60*time.Second)
	if err != nil {
		return 0, err
	}
	var store struct {
		Hosts []storeHost `json:"hosts"`
	}
	_ = json.Unmarshal(raw, &store)
	hosts := map[string]*storeHost{}
	for i := range store.Hosts {
		hosts[strings.ToLower(store.Hosts[i].Name)] = &store.Hosts[i]
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM websites WHERE server_id = ? ORDER BY id`, s.serverID)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		site, err := s.sites.FindFull(ctx, s.serverID, id, nil)
		if err != nil || site == nil || !site.Enabled || site.AutoSSL == "pending" || site.AutoSSL == "renew" {
			continue
		}
		if !site.HTTPS {
			if all {
				s.note(ctx, id, "pending", 0, s.stamp(s.now()), `Queued by "Run AutoSSL for all websites".`)
				s.Log(ctx, id, site.Domain, "queued", "No HTTPS yet; queued for a Let's Encrypt certificate.")
				n++
			}
			continue
		}
		if why := ReplaceReason(hosts[strings.ToLower(site.Domain)]); why != "" {
			s.note(ctx, id, "renew", 0, s.stamp(s.now()), "Queued for a new certificate: "+why+".")
			s.Log(ctx, id, site.Domain, "queued", upperFirst(why)+"; queued for a new Let's Encrypt certificate.")
			n++
		}
	}
	return n, nil
}

// Run is one scheduler pass: every few hours look for certificates to replace,
// then process what is due (AutoSslService::run).
func (s *AutoSslService) Run(ctx context.Context) []string {
	var out []string
	if s.bool(ctx, "auto_ssl_replace") {
		var raw string
		var last float64
		if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, AutoSSLLastReplaceKey).Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &last)
		}
		if time.Unix(int64(last), 0).Before(s.now().Add(-autoSSLReplaceEvry)) {
			_ = s.cfg.Set(ctx, AutoSSLLastReplaceKey, s.now().Unix(), 0)
			if n, err := s.Queue(ctx, false); err != nil {
				out = append(out, "auto SSL: certificates not checked: "+agentMessage(err))
			} else if n > 0 {
				out = append(out, fmt.Sprintf("auto SSL: %d certificate(s) queued for replacement", n))
			}
		}
	}
	return append(out, s.Process(ctx, autoSSLPerPass)...)
}

// Process issues certificates for up to limit due websites (AutoSslService::process).
func (s *AutoSslService) Process(ctx context.Context, limit int) []string {
	if limit < 1 {
		limit = 1
	}
	type dueRow struct {
		id    int64
		state string
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, auto_ssl FROM websites WHERE server_id = ? AND auto_ssl IN ('pending', 'renew') AND (auto_ssl_next IS NULL OR auto_ssl_next <= ?) ORDER BY auto_ssl_next LIMIT ?`,
		s.serverID, s.stamp(s.now()), limit)
	if err != nil {
		return []string{"auto SSL: the queue could not be read: " + err.Error()}
	}
	var due []dueRow
	for rows.Next() {
		var d dueRow
		if err := rows.Scan(&d.id, &d.state); err != nil {
			rows.Close()
			return nil
		}
		due = append(due, d)
	}
	rows.Close()

	var out []string
	var ip string
	ipKnown := false
	for _, d := range due {
		site, err := s.sites.FindFull(ctx, s.serverID, d.id, nil)
		if err != nil || site == nil {
			continue
		}
		renew := d.state == "renew"
		if (!renew && site.HTTPS) || !site.Enabled {
			note := ""
			if site.HTTPS {
				note = "HTTPS was set up."
			}
			s.note(ctx, d.id, "", 0, "", note)
			continue
		}
		if !ipKnown {
			ip, _ = s.overview.ServerIP(ctx)
			ipKnown = true
		}
		tries := s.tries(ctx, d.id)
		if !s.PointsHere(ctx, site.Domain, ip) {
			switch {
			case renew:
				s.note(ctx, d.id, "gave_up", 0, "", "The domain does not point to this server, so Let's Encrypt cannot check it; the certificate was not replaced.")
				s.Log(ctx, d.id, site.Domain, "skipped", "The domain does not point to this server; the certificate was not replaced.")
				out = append(out, "auto SSL: "+site.Domain+" does not point here; not replaced")
			case s.createdBefore(site.CreatedAt, s.now().Add(-AutoSSLWaitDays*24*time.Hour)):
				s.note(ctx, d.id, "gave_up", tries, "", fmt.Sprintf("The domain did not point to this server within %d days. Issue the certificate on this page once it does.", AutoSSLWaitDays))
				s.Log(ctx, d.id, site.Domain, "skipped", fmt.Sprintf("The domain did not point to this server within %d days; gave up.", AutoSSLWaitDays))
				out = append(out, "auto SSL: "+site.Domain+" does not point here; gave up")
			default:
				s.note(ctx, d.id, "pending", tries, s.stamp(s.now().Add(15*time.Minute)),
					"Waiting for the domain to point to this server (last checked "+s.now().UTC().Format("2006-01-02 15:04")+" UTC).")
			}
			continue
		}
		var aliases []string
		for _, a := range site.Aliases {
			if s.PointsHere(ctx, a, ip) {
				aliases = append(aliases, a)
			}
		}
		site.Aliases = aliases
		if err := s.ssl.Issue(ctx, site, s.email(ctx, site), len(aliases) > 0, false); err != nil {
			tries++
			msg := problemText(err)
			s.Log(ctx, d.id, site.Domain, "failed", fmt.Sprintf("Attempt %d of %d: %s", tries, AutoSSLMaxTries, msg))
			if tries >= AutoSSLMaxTries {
				s.note(ctx, d.id, "failed", tries, "", fmt.Sprintf("Let's Encrypt refused the certificate %d times: %s", tries, msg))
			} else {
				// 30 minutes, then doubling up to 6 hours.
				wait := 30 * time.Minute << (tries - 1)
				if wait > 6*time.Hour {
					wait = 6 * time.Hour
				}
				state := "pending"
				if renew {
					state = "renew"
				}
				s.note(ctx, d.id, state, tries, s.stamp(s.now().Add(wait)), "Let's Encrypt refused the certificate; trying again later: "+msg)
			}
			out = append(out, fmt.Sprintf("auto SSL: %s failed (%d): %s", site.Domain, tries, msg))
			continue
		}
		verb, past := "Certificate issued", "Issued a"
		if renew {
			verb, past = "Certificate replaced", "Replaced with a new"
		}
		s.note(ctx, d.id, "done", 0, "", verb+" automatically on "+s.now().UTC().Format("2006-01-02 15:04")+" UTC.")
		s.Log(ctx, d.id, site.Domain, "issued", past+" Let's Encrypt certificate for "+strings.Join(append([]string{site.Domain}, aliases...), ", ")+".")
		out = append(out, "auto SSL: issued for "+site.Domain)
	}
	return out
}

func problemText(err error) string {
	return problem(err)
}

func (s *AutoSslService) tries(ctx context.Context, id int64) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT auto_ssl_tries FROM websites WHERE id = ?`, id).Scan(&n)
	return n
}

func (s *AutoSslService) createdBefore(created string, cutoff time.Time) bool {
	t, err := time.Parse(time.RFC3339, created)
	return err == nil && t.Before(cutoff)
}

// PointsHere says whether the name resolves to this server: its A record is
// ip, or it is delegated to this panel's name servers (the zone of the name or
// a parent).
func (s *AutoSslService) PointsHere(ctx context.Context, name, ip string) bool {
	name = strings.ToLower(name)
	if ip != "" {
		for _, a := range s.lookup.A(ctx, name) {
			if a == ip {
				return true
			}
		}
	}
	ours, _ := s.dns.Nameservers(ctx)
	if len(ours) == 0 {
		return false
	}
	have := map[string]bool{}
	for _, n := range ours {
		have[strings.TrimRight(strings.ToLower(n), ".")] = true
	}
	parts := strings.Split(name, ".")
	for i := 0; i < len(parts)-1; i++ {
		ns := s.lookup.NS(ctx, strings.Join(parts[i:], "."))
		if len(ns) > 0 {
			for _, n := range ns {
				if !have[n] {
					return false
				}
			}
			return true
		}
	}
	return false
}

// email is the account's, else an administrator's, for expiry notices.
func (s *AutoSslService) email(ctx context.Context, site *Website) string {
	var email sql.NullString
	if site.OwnerID.Valid {
		_ = s.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, site.OwnerID.Int64).Scan(&email)
	}
	if !email.Valid {
		_ = s.db.QueryRowContext(ctx, `SELECT u.email FROM users u JOIN roles r ON r.id = u.role_id WHERE r.name = 'admin' AND u.is_active = 1 ORDER BY u.id LIMIT 1`).Scan(&email)
	}
	if email.Valid && emailRe.MatchString(email.String) {
		return email.String
	}
	return ""
}

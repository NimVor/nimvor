package hosting

// AutoSSL — the automatic Let's Encrypt certificates (AutoSslService.php).
// New websites queue for a certificate once their domain points here; every
// few hours certificates that expire, do not cover the name, are self-signed
// or cannot be read are queued for replacement. The scheduler binary drives
// Run; the SSL/TLS page's "Run AutoSSL for all websites" drives Queue +
// Process directly.
//
// The bookkeeping lives on the websites row (auto_ssl, auto_ssl_tries,
// auto_ssl_next, auto_ssl_note) and in autossl_log, both already migrated.

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

const (
	// AutoSSLWaitDays is how long a new website waits for its domain before
	// AutoSSL gives up.
	AutoSSLWaitDays = 3
	// AutoSSLMaxTries is how many refusals before it stops trying.
	AutoSSLMaxTries = 6
	// AutoSSLPerPass is how many websites one scheduler pass handles.
	AutoSSLPerPass = 5
	// AutoSSLReplaceDays is the remaining validity at which a certificate is
	// queued for replacement.
	AutoSSLReplaceDays = 7
	// autosslReplaceEvery is how often the scheduler looks for certificates
	// to replace.
	autosslReplaceEvery = 6 * 3600
	// autosslLogKeep is how many log lines a server keeps.
	autosslLogKeep = 2000
	// SchedulerLastRunKey is Doctor::SCHEDULER_KEY — the scheduler writes it
	// after every run.
	SchedulerLastRunKey = "_scheduler_last_run"
	// autosslLastReplaceKey is AutoSslService::LAST_REPLACE_KEY.
	autosslLastReplaceKey = "_autossl_last_replace_check"
)

// DnsLookup resolves the names AutoSSL checks. It is an interface so the
// scheduler's tests (and a deployment without resolvers) can supply their
// own answers instead of reaching the network.
type DnsLookup interface {
	A(ctx context.Context, name string) ([]string, error)
	NS(ctx context.Context, name string) ([]string, error)
}

// NetDnsLookup resolves through the system resolver.
type NetDnsLookup struct{ Resolver *net.Resolver }

func (l NetDnsLookup) resolver() *net.Resolver {
	if l.Resolver != nil {
		return l.Resolver
	}
	return net.DefaultResolver
}

// A returns the IPv4 and IPv6 addresses of a name.
func (l NetDnsLookup) A(ctx context.Context, name string) ([]string, error) {
	addrs, err := l.resolver().LookupHost(ctx, name)
	if err != nil {
		return nil, err
	}
	return addrs, nil
}

// NS returns the name servers of a zone.
func (l NetDnsLookup) NS(ctx context.Context, name string) ([]string, error) {
	nss, err := l.resolver().LookupNS(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(nss))
	for _, ns := range nss {
		out = append(out, ns.Host)
	}
	return out, nil
}

// AutoSSLQueued is one website waiting for a certificate (AutoSslService::queued).
type AutoSSLQueued struct {
	ID           int64  `json:"id"`
	Domain       string `json:"domain"`
	AutoSSL      string `json:"auto_ssl"`
	AutoSSLTries int    `json:"auto_ssl_tries"`
	AutoSSLNext  string `json:"auto_ssl_next"`
	AutoSSLNote  string `json:"auto_ssl_note"`
}

// AutoSSLLogLine is one autossl_log row.
type AutoSSLLogLine struct {
	ID        int64  `json:"id"`
	Domain    string `json:"domain"`
	Result    string `json:"result"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

// AutoSSLService drives the automatic certificates for one server.
type AutoSSLService struct {
	db       *sql.DB
	sites    *WebsiteRepository
	ssl      *SslService
	dns      *DnsService
	overview *SiteOverview
	cfg      *settings.Repository
	serverID int64
	lookup   DnsLookup
	agent    AgentCaller
}

// NewAutoSSLService builds the service. lookup may be nil (the system
// resolver is used).
func NewAutoSSLService(db *sql.DB, sites *WebsiteRepository, ssl *SslService, dns *DnsService,
	overview *SiteOverview, cfg *settings.Repository, serverID int64,
	lookup DnsLookup, ag AgentCaller) *AutoSSLService {
	if lookup == nil {
		lookup = NetDnsLookup{}
	}
	return &AutoSSLService{
		db: db, sites: sites, ssl: ssl, dns: dns, overview: overview,
		cfg: cfg, serverID: serverID, lookup: lookup, agent: ag,
	}
}

// note writes the AutoSSL bookkeeping of one website.
func (s *AutoSSLService) note(ctx context.Context, id int64, state string, tries int, next sql.NullString, note string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE websites SET auto_ssl = ?, auto_ssl_tries = ?, auto_ssl_next = ?, auto_ssl_note = ? WHERE id = ?`,
		state, tries, next, note, id)
	return err
}

// noteNow is note with "now" as the next attempt.
func (s *AutoSSLService) noteNow(ctx context.Context, id int64, state string, note string) error {
	return s.note(ctx, id, state, 0, sql.NullString{String: UtcNow(), Valid: true}, note)
}

// Log writes one AutoSSL log line, keeping the newest 2000 per server.
func (s *AutoSSLService) Log(ctx context.Context, websiteID sql.NullInt64, domain, result, message string) error {
	if r := []rune(message); len(r) > 1000 {
		message = string(r[:1000])
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO autossl_log (server_id, website_id, domain, result, message, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		s.serverID, websiteID, domain, result, message, UtcNow()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM autossl_log WHERE server_id = ? AND id <= (
		   SELECT id FROM autossl_log WHERE server_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?)`,
		s.serverID, s.serverID, autosslLogKeep)
	return err
}

// RecentLog is the newest log lines (up to 1000).
func (s *AutoSSLService) RecentLog(ctx context.Context, limit int) ([]AutoSSLLogLine, error) {
	if limit < 1 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, domain, result, message, created_at FROM autossl_log
		 WHERE server_id = ? ORDER BY id DESC LIMIT `+fmt.Sprint(limit), s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutoSSLLogLine{}
	for rows.Next() {
		var l AutoSSLLogLine
		if err := rows.Scan(&l.ID, &l.Domain, &l.Result, &l.Message, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// AutoSSLReplaceReason explains why a website's certificate should be
// replaced ("" when it is fine), mirroring AutoSslService::replaceReason.
func AutoSSLReplaceReason(host *SslHost) string {
	if host == nil || !host.HTTPS || host.Certificate == nil {
		return ""
	}
	c := host.Certificate
	switch {
	case c.Error != "":
		return "the certificate cannot be read"
	case c.Expired:
		return "the certificate has expired"
	case !host.Covered:
		return "the certificate does not cover " + host.Name
	case c.SelfSigned:
		return "the certificate is self-signed (browsers warn)"
	case c.DaysRemaining <= AutoSSLReplaceDays:
		return fmt.Sprintf("the certificate expires in %d days and was not renewed", c.DaysRemaining)
	}
	return ""
}

// Queue queues websites for a certificate: those without HTTPS (when all is
// true) and those whose certificate should be replaced. Returns how many
// were queued.
func (s *AutoSSLService) Queue(ctx context.Context, all bool) (int, error) {
	store, err := s.center().Store(ctx)
	if err != nil {
		return 0, err
	}
	hosts := map[string]*SslHost{}
	for i := range store.Hosts {
		h := &store.Hosts[i]
		hosts[strings.ToLower(h.Name)] = h
	}
	ids, err := s.siteIDs(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		site, err := s.sites.FindFull(ctx, s.serverID, id, nil)
		if err != nil || site == nil || !site.Enabled {
			continue
		}
		if site.AutoSSL == "pending" || site.AutoSSL == "renew" {
			continue
		}
		if !site.HTTPS {
			if !all {
				continue
			}
			if err := s.noteNow(ctx, site.ID, "pending", `Queued by "Run AutoSSL for all websites".`); err != nil {
				return n, err
			}
			_ = s.Log(ctx, sql.NullInt64{Int64: site.ID, Valid: true}, site.Domain, "queued",
				"No HTTPS yet; queued for a Let's Encrypt certificate.")
			n++
			continue
		}
		why := AutoSSLReplaceReason(hosts[strings.ToLower(site.Domain)])
		if why == "" {
			continue
		}
		if err := s.noteNow(ctx, site.ID, "renew", "Queued for a new certificate: "+why+"."); err != nil {
			return n, err
		}
		_ = s.Log(ctx, sql.NullInt64{Int64: site.ID, Valid: true}, site.Domain, "queued",
			upperFirst(why)+"; queued for a new Let's Encrypt certificate.")
		n++
	}
	return n, nil
}

// siteIDs lists the server's website ids.
func (s *AutoSSLService) siteIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM websites WHERE server_id = ? ORDER BY id`, s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Queued lists the websites waiting for AutoSSL.
func (s *AutoSSLService) Queued(ctx context.Context) ([]AutoSSLQueued, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, domain, auto_ssl, auto_ssl_tries,
		        COALESCE(auto_ssl_next, ''), auto_ssl_note
		 FROM websites WHERE server_id = ? AND auto_ssl IN ('pending','renew')
		 ORDER BY auto_ssl_next`, s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AutoSSLQueued{}
	for rows.Next() {
		var q AutoSSLQueued
		if err := rows.Scan(&q.ID, &q.Domain, &q.AutoSSL, &q.AutoSSLTries, &q.AutoSSLNext, &q.AutoSSLNote); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// LastSchedulerRun is when the scheduler last ran, if it ever has.
func (s *AutoSSLService) LastSchedulerRun(ctx context.Context) (time.Time, bool) {
	raw, err := s.cfg.String(ctx, SchedulerLastRunKey, "")
	if err != nil || raw == "" {
		return time.Time{}, false
	}
	var str string
	if err := json.Unmarshal([]byte(raw), &str); err != nil {
		str = strings.Trim(raw, `"`)
	}
	return parseStamp(str)
}

// Run is one scheduler pass: certificates to replace every few hours, then
// the due queue. Returns what happened, for the scheduler's output.
func (s *AutoSSLService) Run(ctx context.Context) []string {
	out := []string{}
	if on, err := s.cfg.Bool(ctx, "auto_ssl_replace", false); err == nil && on {
		lastRaw, _ := s.cfg.Int(ctx, autosslLastReplaceKey, 0)
		if int64(lastRaw) < time.Now().Unix()-autosslReplaceEvery {
			_ = s.cfg.Set(ctx, autosslLastReplaceKey, time.Now().Unix(), 0)
			if n, err := s.Queue(ctx, false); err != nil {
				out = append(out, "auto SSL: certificates not checked: "+agentMessage(err))
			} else if n > 0 {
				out = append(out, fmt.Sprintf("auto SSL: %d certificate(s) queued for replacement", n))
			}
		}
	}
	return append(out, s.Process(ctx, AutoSSLPerPass)...)
}

// Process works through the websites whose next attempt is due.
func (s *AutoSSLService) Process(ctx context.Context, limit int) []string {
	if limit < 1 {
		limit = 1
	}
	now := UtcNow()
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, auto_ssl FROM websites
		 WHERE server_id = ? AND auto_ssl IN ('pending','renew')
		   AND (auto_ssl_next IS NULL OR auto_ssl_next <= ?)
		 ORDER BY auto_ssl_next LIMIT `+fmt.Sprint(limit), s.serverID, now)
	if err != nil {
		return []string{"auto SSL: " + agentMessage(err)}
	}
	type due struct {
		id    int64
		state string
	}
	dueList := []due{}
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.state); err != nil {
			rows.Close()
			return []string{"auto SSL: " + agentMessage(err)}
		}
		dueList = append(dueList, d)
	}
	rows.Close()

	out := []string{}
	ip := ""
	ipKnown := false
	for _, d := range dueList {
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
			_ = s.note(ctx, site.ID, "", 0, sql.NullString{}, note)
			continue
		}
		if !ipKnown {
			if v, ok := s.overview.ServerIP(ctx); ok {
				ip = v
			}
			ipKnown = true
		}
		if !s.PointsHere(ctx, site.Domain, ip) {
			out = append(out, s.notPointingHere(ctx, site, renew)...)
			continue
		}
		aliases := []string{}
		for _, a := range site.Aliases {
			if s.PointsHere(ctx, a, ip) {
				aliases = append(aliases, a)
			}
		}
		if s.ssl == nil {
			continue
		}
		site.Aliases = aliases
		if err := s.ssl.Issue(ctx, site, s.email(ctx, site), len(aliases) > 0, false); err != nil {
			out = append(out, s.failed(ctx, site, renew, err)...)
			continue
		}
		verb := "Certificate issued"
		if renew {
			verb = "Certificate replaced"
		}
		_ = s.note(ctx, site.ID, "done", 0, sql.NullString{}, verb+" automatically on "+time.Now().UTC().Format("2006-01-02 15:04")+" UTC.")
		names := append([]string{site.Domain}, aliases...)
		issued := "Issued a"
		if renew {
			issued = "Replaced with a new"
		}
		_ = s.Log(ctx, sql.NullInt64{Int64: site.ID, Valid: true}, site.Domain, "issued",
			issued+" Let's Encrypt certificate for "+strings.Join(names, ", ")+".")
		out = append(out, "auto SSL: issued for "+site.Domain)
	}
	return out
}

// notPointingHere records a domain that does not resolve to this server: a
// replacement is abandoned at once, a new website gets its WAIT_DAYS first.
func (s *AutoSSLService) notPointingHere(ctx context.Context, site *Website, renew bool) []string {
	if renew {
		_ = s.note(ctx, site.ID, "gave_up", 0, sql.NullString{},
			"The domain does not point to this server, so Let's Encrypt cannot check it; the certificate was not replaced.")
		_ = s.Log(ctx, sql.NullInt64{Int64: site.ID, Valid: true}, site.Domain, "skipped",
			"The domain does not point to this server; the certificate was not replaced.")
		return []string{"auto SSL: " + site.Domain + " does not point here; not replaced"}
	}
	created, ok := parseStamp(site.CreatedAt)
	if ok && created.Before(time.Now().Add(-AutoSSLWaitDays*24*time.Hour)) {
		_ = s.note(ctx, site.ID, "gave_up", s.tries(ctx, site.ID), sql.NullString{},
			fmt.Sprintf("The domain did not point to this server within %d days. Issue the certificate on this page once it does.", AutoSSLWaitDays))
		_ = s.Log(ctx, sql.NullInt64{Int64: site.ID, Valid: true}, site.Domain, "skipped",
			fmt.Sprintf("The domain did not point to this server within %d days; gave up.", AutoSSLWaitDays))
		return []string{"auto SSL: " + site.Domain + " does not point here; gave up"}
	}
	_ = s.note(ctx, site.ID, "pending", s.tries(ctx, site.ID),
		sql.NullString{String: time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339), Valid: true},
		"Waiting for the domain to point to this server (last checked "+time.Now().UTC().Format("2006-01-02 15:04")+" UTC).")
	return nil
}

// failed schedules the next attempt, with the PHP's 30-minute doubling backoff
// capped at six hours.
func (s *AutoSSLService) failed(ctx context.Context, site *Website, renew bool, cause error) []string {
	tries := s.tries(ctx, site.ID) + 1
	msg := agentMessage(cause)
	_ = s.Log(ctx, sql.NullInt64{Int64: site.ID, Valid: true}, site.Domain, "failed",
		fmt.Sprintf("Attempt %d of %d: %s", tries, AutoSSLMaxTries, msg))
	if tries >= AutoSSLMaxTries {
		_ = s.note(ctx, site.ID, "failed", tries, sql.NullString{},
			fmt.Sprintf("Let's Encrypt refused the certificate %d times: %s", tries, msg))
	} else {
		back := 30 * time.Minute << uint(tries-1)
		if back > 6*time.Hour {
			back = 6 * time.Hour
		}
		state := "pending"
		if renew {
			state = "renew"
		}
		_ = s.note(ctx, site.ID, state, tries,
			sql.NullString{String: time.Now().UTC().Add(back).Format(time.RFC3339), Valid: true},
			"Let's Encrypt refused the certificate; trying again later: "+msg)
	}
	return []string{fmt.Sprintf("auto SSL: %s failed (%d): %s", site.Domain, tries, msg)}
}

// tries is the website's stored attempt count.
func (s *AutoSSLService) tries(ctx context.Context, id int64) int {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT auto_ssl_tries FROM websites WHERE id = ?`, id).Scan(&n)
	return n
}

// PointsHere reports whether the name resolves to this server or is delegated
// to the panel's own name servers.
func (s *AutoSSLService) PointsHere(ctx context.Context, name, ip string) bool {
	if ip != "" {
		addrs, err := s.lookup.A(ctx, name)
		if err == nil {
			for _, a := range addrs {
				if a == ip {
					return true
				}
			}
		}
	}
	if s.dns == nil {
		return false
	}
	ours, err := s.dns.Nameservers(ctx)
	if err != nil || len(ours) == 0 {
		return false
	}
	want := map[string]bool{}
	for _, n := range ours {
		want[strings.TrimSuffix(strings.ToLower(n), ".")] = true
	}
	parts := strings.Split(strings.ToLower(name), ".")
	for i := 1; i < len(parts); i++ {
		ns, err := s.lookup.NS(ctx, strings.Join(parts[i:], "."))
		if err != nil || len(ns) == 0 {
			continue
		}
		for _, n := range ns {
			if !want[strings.TrimSuffix(strings.ToLower(n), ".")] {
				return false
			}
		}
		return true
	}
	return false
}

// email is the account's address, else the first active administrator's.
func (s *AutoSSLService) email(ctx context.Context, site *Website) string {
	var email sql.NullString
	if site.OwnerID.Valid {
		_ = s.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, site.OwnerID.Int64).Scan(&email)
	}
	if !email.Valid || email.String == "" {
		_ = s.db.QueryRowContext(ctx,
			`SELECT u.email FROM users u JOIN roles r ON r.id = u.role_id
			 WHERE r.name IN ('admin','administrator') AND u.is_active = 1 ORDER BY u.id LIMIT 1`).Scan(&email)
	}
	if !email.Valid || !strings.Contains(email.String, "@") {
		return ""
	}
	return email.String
}

// center reuses the SSL/TLS service's agent calls for the ssl.store read.
func (s *AutoSSLService) center() *SslCenterService {
	return NewSslCenterService(s.agent, nil, s.serverID)
}

// UtcNow is the timestamp format the panel stores (RFC 3339, UTC).
func UtcNow() string { return time.Now().UTC().Format("2006-01-02T15:04:05+00:00") }

// parseStamp reads the timestamps the panels store (gmdate('c'), which
// formats as +00:00, and the 'Y-m-d H:i:s' form older rows carry).
func parseStamp(raw string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// upperFirst capitalises the first letter (PHP's ucfirst).
func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

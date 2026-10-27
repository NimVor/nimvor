package hosting

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// MonitoringService stores a metrics sample on every scheduler pass (kept 30
// days), evaluates the alert rules (disk, memory, load, service down, agent
// unreachable, certificate expiring, backup failed, quota, bandwidth) and
// notifies by email and an optional webhook when an alert opens or resolves
// (MonitoringService).
type MonitoringService struct {
	db       *sql.DB
	serverID int64
	agent    AgentCaller
	cfg      *settings.Repository
	limits   *PlanLimits
	// Notify records a notification (NotificationService::notify).
	Notify func(ctx context.Context, websiteID, ownerID sql.NullInt64, kind, severity, title, link, key string)
	// Email sends the alert mail; nil or failing is ignored.
	Email func(ctx context.Context, to []string, subject, text string) error
	// Webhook delivers a JSON body to the alert webhook; nil uses http.
	Webhook func(url, body string) error
	Now     func() time.Time
}

const monitoringRetentionDays = 30

// NewMonitoringService builds the service.
func NewMonitoringService(db *sql.DB, serverID int64, agent AgentCaller, cfg *settings.Repository, limits *PlanLimits) *MonitoringService {
	return &MonitoringService{db: db, serverID: serverID, agent: agent, cfg: cfg, limits: limits, Now: time.Now}
}

type firingAlert struct{ rule, subject, severity, message string }

func (a firingAlert) key() string {
	if a.subject != "" {
		return a.rule + ":" + a.subject
	}
	return a.rule
}

func (m *MonitoringService) setting(ctx context.Context, key string) int {
	v, err := m.cfg.Values(ctx)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(v[key]))
	return n
}

func (m *MonitoringService) call(ctx context.Context, action string, params any, out any) error {
	raw, err := m.agent.Call(ctx, action, params, 300*time.Second)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func f(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case json.Number:
		n, _ := t.Float64()
		return n
	}
	return 0
}

// Run is one monitoring pass; it returns log lines.
func (m *MonitoringService) Run(ctx context.Context) []string {
	now := m.Now()
	firing := map[string]firingAlert{}
	add := func(a firingAlert) { firing[a.key()] = a }
	agentErr := func() error {
		var mt map[string]any
		if err := m.call(ctx, "system.metrics", nil, &mt); err != nil {
			return err
		}
		var info struct {
			System struct {
				CPU struct {
					Cores int `json:"cores"`
				} `json:"cpu"`
			} `json:"system"`
		}
		if err := m.call(ctx, "system.info", nil, &info); err != nil {
			return err
		}
		cores := info.System.CPU.Cores
		if cores < 1 {
			cores = 1
		}
		maxDisk := 0.0
		diskAlert := float64(m.setting(ctx, "alert_disk_percent"))
		disks, _ := mt["disks"].([]any)
		for _, d := range disks {
			dm, _ := d.(map[string]any)
			pct := f(dm["used_percent"])
			maxDisk = math.Max(maxDisk, pct)
			if pct >= diskAlert {
				mp, _ := dm["mount_point"].(string)
				if mp == "" {
					mp, _ = dm["mount"].(string)
				}
				if mp == "" {
					mp = "?"
				}
				sev := "warning"
				if pct >= 98 {
					sev = "critical"
				}
				add(firingAlert{"disk", mp, sev, fmt.Sprintf("Disk %s is %.0f%% full.", mp, pct)})
			}
		}
		var rx, tx float64
		nets, _ := mt["network"].([]any)
		for _, n := range nets {
			nm, _ := n.(map[string]any)
			rx += f(nm["rx_bytes_per_sec"])
			tx += f(nm["tx_bytes_per_sec"])
		}
		memM, _ := mt["memory"].(map[string]any)
		loadM, _ := mt["load"].(map[string]any)
		mem, load := f(memM["used_percent"]), f(loadM["load1"])
		var prevMem float64
		_ = m.db.QueryRowContext(ctx, `SELECT mem FROM metric_samples WHERE server_id = ? AND taken_at < ? ORDER BY taken_at DESC LIMIT 1`, m.serverID, now.Unix()).Scan(&prevMem)
		if _, err := m.db.ExecContext(ctx, `INSERT INTO metric_samples (server_id, taken_at, cpu, mem, disk, load1, rx_bps, tx_bps) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			m.serverID, now.Unix(), f(mt["cpu_percent"]), mem, maxDisk, load, rx, tx); err != nil {
			return err
		}
		memAlert := float64(m.setting(ctx, "alert_memory_percent"))
		if mem >= memAlert && prevMem >= memAlert {
			add(firingAlert{"memory", "", "warning", fmt.Sprintf("Memory use is %.0f%%.", mem)})
		}
		if load > float64(cores)*float64(m.setting(ctx, "alert_load_per_core")) {
			add(firingAlert{"load", "", "warning", fmt.Sprintf("Load average %.2f on %d core(s).", load, cores)})
		}
		var services []struct {
			Unit        string `json:"unit"`
			ActiveState string `json:"active_state"`
			UnitFile    string `json:"unit_file_state"`
		}
		if err := m.call(ctx, "services.status", nil, &services); err != nil {
			return err
		}
		for _, s := range services {
			if s.UnitFile == "enabled" && (s.ActiveState == "failed" || s.ActiveState == "inactive") {
				add(firingAlert{"service", s.Unit, "critical", s.Unit + " is " + s.ActiveState + "."})
			}
		}
		for _, q := range m.quotas(ctx, now) {
			add(q)
		}
		warn := m.setting(ctx, "ssl_warning_days")
		var certs []struct {
			Name        string `json:"name"`
			Certificate struct {
				DaysRemaining *int `json:"days_remaining"`
			} `json:"certificate"`
		}
		if err := m.call(ctx, "discovery.certificates", nil, &certs); err != nil {
			return err
		}
		for _, c := range certs {
			days := 999
			if c.Certificate.DaysRemaining != nil {
				days = *c.Certificate.DaysRemaining
			}
			if days <= warn {
				sev, msg := "warning", fmt.Sprintf("Certificate %s expires in %d day(s).", c.Name, days)
				if days <= 3 {
					sev = "critical"
				}
				if days < 0 {
					msg = fmt.Sprintf("Certificate %s has expired.", c.Name)
				}
				add(firingAlert{"certificate", c.Name, sev, msg})
			}
		}
		return nil
	}()
	if agentErr != nil {
		add(firingAlert{"agent", "", "critical", "The WhaCP agent is not reachable: " + agentMessage(agentErr)})
		// Nothing could be measured: keep agent-based alerts open instead of
		// reporting them as resolved.
		if rows, err := m.db.QueryContext(ctx, `SELECT rule, subject, severity, message FROM alerts WHERE server_id = ? AND resolved_at IS NULL AND rule IN ('disk','memory','load','service','certificate','quota','bandwidth')`, m.serverID); err == nil {
			var keep []firingAlert
			for rows.Next() {
				var a firingAlert
				if rows.Scan(&a.rule, &a.subject, &a.severity, &a.message) == nil {
					keep = append(keep, a)
				}
			}
			rows.Close()
			for _, a := range keep {
				add(a)
			}
		}
	}
	if rows, err := m.db.QueryContext(ctx, `SELECT domain, kind, error FROM backups WHERE server_id = ? AND status = 'failed' AND created_at > ?`,
		m.serverID, now.Add(-24*time.Hour).UTC().Format(time.RFC3339)); err == nil {
		var failed []firingAlert
		for rows.Next() {
			var domain, kind, e string
			if rows.Scan(&domain, &kind, &e) == nil {
				what := domain
				if kind == "server" {
					what = "server configuration"
				}
				failed = append(failed, firingAlert{"backup", what, "warning", "Backup of " + what + " failed: " + truncate(e, 150)})
			}
		}
		rows.Close()
		for _, a := range failed {
			add(a)
		}
	}
	log := m.reconcile(ctx, firing, now)
	_, _ = m.db.ExecContext(ctx, `DELETE FROM metric_samples WHERE taken_at < ?`, now.Add(-monitoringRetentionDays*24*time.Hour).Unix())
	_, _ = m.db.ExecContext(ctx, `DELETE FROM alerts WHERE resolved_at IS NOT NULL AND resolved_at < ?`, now.Add(-90*24*time.Hour).UTC().Format(time.RFC3339))
	return log
}

// quotas measures website disk use (at most hourly) and bandwidth (every pass,
// reading on from the last position) and returns quota alerts.
func (m *MonitoringService) quotas(ctx context.Context, now time.Time) []firingAlert {
	byDomain := map[string]int64{}
	var domains []string
	if rows, err := m.db.QueryContext(ctx, `SELECT id, domain FROM websites WHERE server_id = ?`, m.serverID); err == nil {
		for rows.Next() {
			var id int64
			var d string
			if rows.Scan(&id, &d) == nil {
				byDomain[d] = id
				domains = append(domains, d)
			}
		}
		rows.Close()
	}
	var measured int
	var oldest sql.NullString
	_ = m.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(measured_at) FROM website_usage u JOIN websites w ON w.id = u.website_id WHERE w.server_id = ?`, m.serverID).Scan(&measured, &oldest)
	stale := true
	if t, err := time.Parse(time.RFC3339, oldest.String); err == nil {
		stale = t.Before(now.Add(-time.Hour))
	}
	if len(domains) > 0 && (measured < len(domains) || stale) {
		var rows []struct {
			Domain   string `json:"domain"`
			Bytes    int64  `json:"bytes"`
			Files    int64  `json:"files"`
			NotFound bool   `json:"not_found"`
		}
		if err := m.call(ctx, "sites.usage", map[string]any{"domains": domains}, &rows); err == nil {
			for _, r := range rows {
				id, ok := byDomain[r.Domain]
				if r.NotFound || !ok {
					continue
				}
				_, _ = m.db.ExecContext(ctx,
					`INSERT INTO website_usage (website_id, bytes, files, measured_at) VALUES (?, ?, ?, ?) ON CONFLICT (website_id) DO UPDATE SET bytes = excluded.bytes, files = excluded.files, measured_at = excluded.measured_at`,
					id, r.Bytes, r.Files, now.UTC().Format(time.RFC3339))
			}
		}
	}
	m.bandwidth(ctx, now)
	var out []firingAlert
	type acct struct {
		id   int64
		name string
	}
	var accounts []acct
	if rows, err := m.db.QueryContext(ctx, `SELECT id, username FROM users WHERE plan_id IS NOT NULL`); err == nil {
		for rows.Next() {
			var a acct
			if rows.Scan(&a.id, &a.name) == nil {
				accounts = append(accounts, a)
			}
		}
		rows.Close()
	}
	for _, a := range accounts {
		id := a.id
		limit, _ := m.limits.Value(ctx, &id, "bandwidth_mb")
		if limit <= 0 {
			continue
		}
		if used := m.limits.BandwidthUsedMB(ctx, m.limits.Owners(ctx, a.id)); used > limit {
			out = append(out, firingAlert{"bandwidth", a.name, "warning", fmt.Sprintf("%s used %d MB of a %d MB monthly bandwidth.", a.name, used, limit)})
		}
	}
	for _, a := range accounts {
		id := a.id
		limit, _ := m.limits.Value(ctx, &id, "disk_quota_mb")
		if limit <= 0 {
			continue
		}
		owners := []int64{a.id}
		if rows, err := m.db.QueryContext(ctx, `SELECT id FROM users WHERE parent_id = ?`, a.id); err == nil {
			for rows.Next() {
				var o int64
				if rows.Scan(&o) == nil {
					owners = append(owners, o)
				}
			}
			rows.Close()
		}
		if used := m.limits.DiskUsedMB(ctx, owners); used > limit {
			out = append(out, firingAlert{"quota", a.name, "warning", fmt.Sprintf("%s uses %d MB of a %d MB disk quota.", a.name, used, limit)})
		}
	}
	return out
}

// bandwidth reads each website's access log on from where the last read stopped.
func (m *MonitoringService) bandwidth(ctx context.Context, now time.Time) {
	type site struct {
		ID     int64
		Domain string `json:"domain"`
		Root   string `json:"root"`
		Inode  int64  `json:"inode"`
		Offset int64  `json:"offset"`
	}
	var sites []site
	rows, err := m.db.QueryContext(ctx, `SELECT w.id, w.domain, w.root, u.bw_inode, u.bw_offset FROM websites w JOIN website_usage u ON u.website_id = w.id WHERE w.server_id = ?`, m.serverID)
	if err != nil {
		return
	}
	for rows.Next() {
		var s site
		if rows.Scan(&s.ID, &s.Domain, &s.Root, &s.Inode, &s.Offset) == nil {
			sites = append(sites, s)
		}
	}
	rows.Close()
	if len(sites) == 0 {
		return
	}
	byDomain := map[string]int64{}
	req := make([]map[string]any, 0, len(sites))
	for _, s := range sites {
		byDomain[s.Domain] = s.ID
		req = append(req, map[string]any{"domain": s.Domain, "root": s.Root, "inode": s.Inode, "offset": s.Offset})
	}
	var res []struct {
		Domain   string           `json:"domain"`
		NotFound bool             `json:"not_found"`
		Inode    int64            `json:"inode"`
		Offset   int64            `json:"offset"`
		Months   map[string]int64 `json:"months"`
	}
	if err := m.call(ctx, "sites.bandwidth", map[string]any{"sites": req}, &res); err != nil {
		return
	}
	month := now.UTC().Format("2006-01")
	for _, t := range res {
		id, ok := byDomain[t.Domain]
		if !ok || t.NotFound {
			continue
		}
		add := t.Months[month]
		// A new month starts from zero.
		_, _ = m.db.ExecContext(ctx,
			`UPDATE website_usage SET bw_bytes = CASE WHEN bw_month = ? THEN bw_bytes + ? ELSE ? END, bw_month = ?, bw_inode = ?, bw_offset = ? WHERE website_id = ?`,
			month, add, add, month, t.Inode, t.Offset, id)
	}
}

type openAlert struct {
	id                               int64
	rule, subject, severity, message string
}

// reconcile opens new alerts, refreshes firing ones and resolves the rest.
func (m *MonitoringService) reconcile(ctx context.Context, firing map[string]firingAlert, now time.Time) []string {
	ts := now.UTC().Format(time.RFC3339)
	open := map[string]openAlert{}
	if rows, err := m.db.QueryContext(ctx, `SELECT id, rule, subject, severity, message FROM alerts WHERE server_id = ? AND resolved_at IS NULL`, m.serverID); err == nil {
		for rows.Next() {
			var a openAlert
			if rows.Scan(&a.id, &a.rule, &a.subject, &a.severity, &a.message) == nil {
				k := a.rule
				if a.subject != "" {
					k += ":" + a.subject
				}
				open[k] = a
			}
		}
		rows.Close()
	}
	keys := make([]string, 0, len(firing))
	for k := range firing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var log []string
	for _, k := range keys {
		a := firing[k]
		if o, ok := open[k]; ok {
			_, _ = m.db.ExecContext(ctx, `UPDATE alerts SET last_seen = ?, message = ?, severity = ? WHERE id = ?`, ts, a.message, a.severity, o.id)
			continue
		}
		res, err := m.db.ExecContext(ctx, `INSERT INTO alerts (server_id, rule, subject, severity, message, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			m.serverID, a.rule, a.subject, a.severity, a.message, ts, ts)
		if err != nil {
			continue
		}
		id, _ := res.LastInsertId()
		level, mark := "warning", "🟠 "
		if a.severity == "critical" {
			level, mark = "error", "🔴 "
		}
		m.notifyAlert(ctx, id, a.rule, a.subject, level, a.message)
		log = append(log, "Alert: "+a.message)
		m.deliver(ctx, mark+a.message)
	}
	ok := make([]string, 0, len(open))
	for k := range open {
		ok = append(ok, k)
	}
	sort.Strings(ok)
	for _, k := range ok {
		if _, still := firing[k]; still {
			continue
		}
		a := open[k]
		_, _ = m.db.ExecContext(ctx, `UPDATE alerts SET resolved_at = ? WHERE id = ?`, ts, a.id)
		m.notifyAlert(ctx, a.id, a.rule, a.subject, "success", "Resolved: "+a.message)
		log = append(log, "Resolved: "+a.message)
		m.deliver(ctx, "✅ Resolved: "+a.message)
	}
	return log
}

// notifyAlert records an alert (or its resolution) as a notification. Alerts
// about a website (certificate, backup) or a user (quota) are shown to its
// owner as well; the rest to administrators only.
func (m *MonitoringService) notifyAlert(ctx context.Context, alertID int64, rule, subject, severity, message string) {
	if m.Notify == nil {
		return
	}
	link := "/monitoring"
	var site, owner sql.NullInt64
	switch {
	case (rule == "certificate" || rule == "backup") && subject != "":
		var id int64
		if m.db.QueryRowContext(ctx, `SELECT id FROM websites WHERE server_id = ? AND domain = ? COLLATE NOCASE`, m.serverID, subject).Scan(&id) == nil {
			site = sql.NullInt64{Int64: id, Valid: true}
			link = fmt.Sprintf("/sites/%d", id)
			if rule == "backup" {
				link += "/backups"
			}
		}
	case (rule == "quota" || rule == "bandwidth") && subject != "":
		var id int64
		if m.db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ? COLLATE NOCASE`, subject).Scan(&id) == nil {
			owner = sql.NullInt64{Int64: id, Valid: true}
			link = ""
		}
	}
	state := "open"
	if severity == "success" {
		state = "resolved"
	}
	m.Notify(ctx, site, owner, "alert", severity, message, link, fmt.Sprintf("alert:%d:%s", alertID, state))
}

var addrSplit = regexp.MustCompile(`[,;\s]+`)

func hostName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "server"
}

// deliver sends an alert by email (to the alert recipients) and to the webhook.
func (m *MonitoringService) deliver(ctx context.Context, text string) {
	host := hostName()
	if m.Email != nil {
		list, _ := m.cfg.String(ctx, "mail.alert_to", "")
		var to []string
		for _, a := range addrSplit.Split(list, -1) {
			if a = strings.TrimSpace(a); a != "" {
				to = append(to, a)
			}
		}
		if len(to) > 0 {
			subject := "[WhaCP " + host + "] " + truncate(text, 120)
			_ = m.Email(ctx, to, subject, text+"\n\n-- \nWhaCP on "+host+". Change alert settings under Settings.")
		}
	}
	hook, _ := m.cfg.String(ctx, "alert_webhook_url", "")
	if hook == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{"text": "[WhaCP " + host + "] " + text})
	if m.Webhook != nil {
		_ = m.Webhook(hook, string(body))
		return
	}
	u, err := url.Parse(hook)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "WhaCP")
	if resp, err := client.Do(req); err == nil {
		resp.Body.Close()
	}
}

// Alert is one row of the alerts table.
type Alert struct {
	ID         int64
	Rule       string
	Subject    string
	Severity   string
	Message    string
	FirstSeen  string
	LastSeen   string
	ResolvedAt string
}

func (m *MonitoringService) alerts(ctx context.Context, where, order string, args ...any) []Alert {
	rows, err := m.db.QueryContext(ctx, `SELECT id, rule, subject, severity, message, first_seen, last_seen, COALESCE(resolved_at,'') FROM alerts WHERE server_id = ? AND `+where+` ORDER BY `+order, append([]any{m.serverID}, args...)...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		var a Alert
		if rows.Scan(&a.ID, &a.Rule, &a.Subject, &a.Severity, &a.Message, &a.FirstSeen, &a.LastSeen, &a.ResolvedAt) == nil {
			out = append(out, a)
		}
	}
	return out
}

// OpenAlerts are the unresolved alerts, critical first.
func (m *MonitoringService) OpenAlerts(ctx context.Context) []Alert {
	return m.alerts(ctx, `resolved_at IS NULL`, `CASE severity WHEN 'critical' THEN 0 ELSE 1 END, first_seen`)
}

// RecentAlerts are the latest resolved alerts.
func (m *MonitoringService) RecentAlerts(ctx context.Context, limit int) []Alert {
	return m.alerts(ctx, `resolved_at IS NOT NULL`, fmt.Sprintf(`resolved_at DESC LIMIT %d`, limit))
}

// SeriesPoint is one averaged point of a chart.
type SeriesPoint struct {
	T                             int64
	CPU, Mem, Disk, Load1, RX, TX float64
}

// Series averages the samples of the last seconds into at most buckets points.
func (m *MonitoringService) Series(ctx context.Context, seconds, buckets int) []SeriesPoint {
	now := m.Now().Unix()
	from := now - int64(seconds)
	size := int64(seconds / buckets)
	if size < 1 {
		size = 1
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT (taken_at - ?1) / ?2 AS b, MIN(taken_at), AVG(cpu), AVG(mem), MAX(disk), AVG(load1), AVG(rx_bps), AVG(tx_bps)
		 FROM metric_samples WHERE server_id = ?3 AND taken_at >= ?1 GROUP BY b ORDER BY b`, from, size, m.serverID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []SeriesPoint
	for rows.Next() {
		var b int64
		var p SeriesPoint
		if rows.Scan(&b, &p.T, &p.CPU, &p.Mem, &p.Disk, &p.Load1, &p.RX, &p.TX) == nil {
			out = append(out, p)
		}
	}
	return out
}

package security

import (
	"context"
	"database/sql"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// ValidEntry is HulkService::validEntry — an IP address or network.
func ValidEntry(s string) bool {
	i := strings.IndexByte(s, '/')
	if i == -1 {
		return net.ParseIP(s) != nil
	}
	addr, bits := s[:i], s[i+1:]
	if net.ParseIP(addr) == nil {
		return false
	}
	l, err := strconv.Atoi(bits)
	if err != nil || l < 0 {
		return false
	}
	return l <= bitsFor(addr)
}

// bitsFor is strlen(inet_pton($addr)) * 8 (32 for IPv4, 128 for IPv6).
func bitsFor(addr string) int {
	if strings.Contains(addr, ":") {
		return 128
	}
	return 32
}

// InNetwork is HulkService::inNetwork — whether ip falls in the address or
// network.
func InNetwork(ip, network string) bool {
	a := net.ParseIP(ip)
	if a == nil {
		return false
	}
	if i := strings.IndexByte(network, '/'); i == -1 {
		return a.Equal(net.ParseIP(network))
	}
	_, ipnet, err := net.ParseCIDR(network)
	if err != nil {
		return false
	}
	return ipnet.Contains(a)
}

// ReportRow is one row of the failed-logins report (HulkService::report).
type ReportRow struct {
	User        string
	IP          string
	Country     string
	Service     string
	AuthService string
	Time        int64
	Expires     int64
	MinutesLeft int
	CountryName string
}

// BlockRow is one active block (HulkService::blocks).
type BlockRow struct {
	ID          int64
	Kind        string
	IP          string
	User        string
	Reason      string
	Service     string
	AuthService string
	Failures    int
	Firewall    bool
	ExpiresAt   int64
}

// Hulk is the brute force protection store (the hulk_* tables and
// geo_ranges) — the port of app/Services/HulkService.php (the panel side;
// the enforcement loop runs in the agent/scheduler, not here, and the agent
// calls stay in the http layer like the rest of the panel).
type Hulk struct {
	db       *sql.DB
	settings *settings.Repository
}

// NewHulk returns the brute force protection service.
func NewHulk(db *sql.DB, settings *settings.Repository) *Hulk {
	return &Hulk{db: db, settings: settings}
}

// Whitelist is HulkService::whitelist (the hulk_whitelist setting, split).
func (h *Hulk) Whitelist(ctx context.Context) []string {
	s, _ := h.settings.String(ctx, "hulk_whitelist", "")
	entries := []string{}
	for _, p := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ','
	}) {
		if p != "" {
			entries = append(entries, p)
		}
	}
	return entries
}

// Whitelisted is HulkService::whitelisted.
func (h *Hulk) Whitelisted(entries []string, ip string) bool {
	for _, w := range entries {
		if InNetwork(ip, w) {
			return true
		}
	}
	return false
}

// Countries is HulkService::countries (code => status).
func (h *Hulk) Countries(ctx context.Context) (map[string]string, error) {
	marks := map[string]string{}
	rows, err := h.db.QueryContext(ctx, "SELECT code, status FROM hulk_countries")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code, status string
		if err := rows.Scan(&code, &status); err != nil {
			return nil, err
		}
		marks[code] = status
	}
	return marks, rows.Err()
}

// SetCountries is HulkService::setCountries — it upserts or removes the marks
// and returns the number of countries changed. Unknown codes are skipped.
func (h *Hulk) SetCountries(ctx context.Context, marks map[string]string) (int, error) {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	upsert, err := tx.PrepareContext(ctx, "INSERT INTO hulk_countries (code, status) VALUES (?, ?) ON CONFLICT (code) DO UPDATE SET status = excluded.status")
	if err != nil {
		return 0, err
	}
	for code, status := range marks {
		if _, known := Countries[code]; !known {
			continue
		}
		switch status {
		case "whitelist", "blacklist":
			if _, err := upsert.ExecContext(ctx, code, status); err != nil {
				upsert.Close()
				return 0, err
			}
		default:
			if _, err := tx.ExecContext(ctx, "DELETE FROM hulk_countries WHERE code = ?", code); err != nil {
				upsert.Close()
				return 0, err
			}
		}
		n++
	}
	upsert.Close()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// Report is HulkService::report — the failed logins within the retention
// window (newest first, at most 1000).
func (h *Hulk) Report(ctx context.Context) ([]ReportRow, error) {
	retentionMin, _ := h.settings.Int(ctx, "hulk_retention", 360)
	retention := int64(retentionMin) // minutes; widened to match the Unix-time (int64) arithmetic below
	limit := 1000                    // (int) max(1, min(5000, $limit)) with the default limit 1000.
	now := time.Now().Unix()
	rows, err := h.db.QueryContext(ctx,
		"SELECT user, ip, country, service, authservice, `time` FROM hulk_events WHERE ok = 0 AND `time` > ? ORDER BY `time` DESC LIMIT ?",
		now-retention*60, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportRow{}
	for rows.Next() {
		var r ReportRow
		if err := rows.Scan(&r.User, &r.IP, &r.Country, &r.Service, &r.AuthService, &r.Time); err != nil {
			return nil, err
		}
		r.Expires = r.Time + retention*60
		left := (r.Expires - now + 59) / 60 // ceil((expires - now) / 60)
		if left < 0 {
			left = 0
		}
		r.MinutesLeft = int(left)
		if r.Country != "" {
			// Countries::name falls back to the code itself.
			if name, ok := Countries[r.Country]; ok {
				r.CountryName = name
			} else {
				r.CountryName = r.Country
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Blocks is HulkService::blocks (the active blocks, newest first).
func (h *Hulk) Blocks(ctx context.Context) ([]BlockRow, error) {
	now := time.Now().Unix()
	rows, err := h.db.QueryContext(ctx,
		"SELECT id, kind, ip, user, reason, service, authservice, failures, firewall, created_at, expires_at FROM hulk_blocks WHERE expires_at > ? ORDER BY created_at DESC", now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BlockRow{}
	for rows.Next() {
		var b BlockRow
		var ip, user sql.NullString
		var firewall int
		var createdAt int64
		if err := rows.Scan(&b.ID, &b.Kind, &ip, &user, &b.Reason, &b.Service, &b.AuthService, &b.Failures, &firewall, &createdAt, &b.ExpiresAt); err != nil {
			return nil, err
		}
		b.IP, b.User = ip.String, user.String
		b.Firewall = firewall != 0
		out = append(out, b)
	}
	return out, rows.Err()
}

// RemoveBlock is the block half of HulkService::removeBlock (the audit and
// the quiet sync stay with the handler).
func (h *Hulk) RemoveBlock(ctx context.Context, id int64) error {
	_, err := h.db.ExecContext(ctx, "DELETE FROM hulk_blocks WHERE id = ?", id)
	return err
}

// Clear is the data half of HulkService::clear (blocks and events; the audit
// and the quiet sync stay with the handler).
func (h *Hulk) Clear(ctx context.Context) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM hulk_blocks"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM hulk_events"); err != nil {
		return err
	}
	return tx.Commit()
}

// syncBlock is one row of the agent hulk.sync blocks payload.
type syncBlock struct {
	IP    string `json:"ip"`
	Until int64  `json:"until"`
	All   bool   `json:"all"`
}

// SyncPayload is everything HulkService::sync pushes to the agent (the
// enabled flag, the IP blocks, the blacklisted countries' address ranges and
// the whitelist); the http layer performs the hulk.sync call.
type SyncPayload struct {
	Enabled      bool
	Blocks       []syncBlock
	Countries    []CountryRange
	Whitelist    []string
	CountriesAll bool
}

// BuildSync is the data half of HulkService::sync (the agent call stays in
// the http layer, like every other panel agent call).
func (h *Hulk) BuildSync(ctx context.Context) (SyncPayload, error) {
	p := SyncPayload{Blocks: []syncBlock{}, Whitelist: h.Whitelist(ctx)}
	var err error
	p.Enabled, err = h.settings.Bool(ctx, "hulk_enabled", false)
	if err != nil {
		return SyncPayload{}, err
	}
	if !p.Enabled {
		return p, nil
	}
	now := time.Now().Unix()
	rows, err := h.db.QueryContext(ctx,
		"SELECT ip, MAX(expires_at), MAX(firewall) FROM hulk_blocks WHERE ip IS NOT NULL AND kind IN ('ip', 'oneday') AND expires_at > ? GROUP BY ip", now)
	if err != nil {
		return SyncPayload{}, err
	}
	for rows.Next() {
		var b syncBlock
		var firewall int
		if err := rows.Scan(&b.IP, &b.Until, &firewall); err != nil {
			rows.Close()
			return SyncPayload{}, err
		}
		b.All = firewall != 0
		p.Blocks = append(p.Blocks, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return SyncPayload{}, err
	}
	marks, err := h.Countries(ctx)
	if err != nil {
		return SyncPayload{}, err
	}
	black := []string{}
	for code, status := range marks {
		if status == "blacklist" {
			black = append(black, code)
		}
	}
	p.Countries, err = NewGeo(h.db).Ranges(ctx, black)
	if err != nil {
		return SyncPayload{}, err
	}
	p.CountriesAll, err = h.settings.Bool(ctx, "hulk_countries_all", false)
	return p, err
}

package security

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// The enforcement half of HulkService: failed logins (from the panel itself
// and, through the agent, from the logs of SSH, Dovecot, Postfix, pure-ftpd
// and webmail) are stored, counted against the rules, and too many of them
// lock the user name (WhaCP sign-ins) or block the address. Blocks end on
// their own; whitelisted addresses and countries are never blocked.

// HulkEvent is one login attempt reported by a service.
type HulkEvent struct {
	ID          string `json:"id"`
	Time        int64  `json:"time"`
	Service     string `json:"service"`
	AuthService string `json:"authservice"`
	User        string `json:"user"`
	IP          string `json:"ip"`
	OK          bool   `json:"ok"`
	Country     string `json:"-"`
}

// HulkHooks are what enforcement needs from the rest of the panel.
type HulkHooks struct {
	// Notify records a notification ("security" kind).
	Notify func(ctx context.Context, severity, title, body, link, key string)
	// Audit records an audit entry on the server's brute force protection.
	Audit func(ctx context.Context, action, result string, details map[string]any)
	// Now is the clock (tests).
	Now func() time.Time
}

func (h *Hulk) now(hk HulkHooks) int64 {
	if hk.Now != nil {
		return hk.Now().Unix()
	}
	return time.Now().Unix()
}

// Enabled is the hulk_enabled setting.
func (h *Hulk) Enabled(ctx context.Context) bool {
	return h.boolSetting(ctx, "hulk_enabled")
}

// strSetting reads a setting with its definition's default.
func (h *Hulk) strSetting(ctx context.Context, key string) string {
	v, err := h.settings.Values(ctx)
	if err != nil {
		return ""
	}
	return v[key]
}

func (h *Hulk) intSetting(ctx context.Context, key string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(h.strSetting(ctx, key)))
	return n
}

func (h *Hulk) boolSetting(ctx context.Context, key string) bool {
	v := strings.TrimSpace(h.strSetting(ctx, key))
	return v == "1" || v == "true"
}

// isLocal: this server's own addresses and loopback.
func (h *Hulk) isLocal(ctx context.Context, ip string) bool {
	if InNetwork(ip, "127.0.0.0/8") || ip == "::1" {
		return true
	}
	var one int
	return h.db.QueryRowContext(ctx, `SELECT 1 FROM ip_addresses WHERE address = ?`, ip).Scan(&one) == nil
}

// countryStatus is the whitelist/blacklist mark of the address's country ("" none).
func (h *Hulk) countryStatus(ctx context.Context, ip string) string {
	cc, err := NewGeo(h.db).Country(ctx, ip)
	if err != nil || cc == "" {
		return ""
	}
	var st string
	_ = h.db.QueryRowContext(ctx, `SELECT status FROM hulk_countries WHERE code = ?`, cc).Scan(&st)
	return st
}

// LoginRefusal is why a WhaCP sign-in must be refused now ("" when it may go on).
func (h *Hulk) LoginRefusal(ctx context.Context, username, ip string, hk HulkHooks) string {
	if !h.Enabled(ctx) || h.Whitelisted(h.Whitelist(ctx), ip) {
		return ""
	}
	switch h.countryStatus(ctx, ip) {
	case "whitelist":
		return ""
	case "blacklist":
		return "Sign-ins from your country are not allowed on this server."
	}
	now := h.now(hk)
	var one int
	if h.db.QueryRowContext(ctx, `SELECT 1 FROM hulk_blocks WHERE ip = ? AND kind IN ('ip', 'oneday') AND expires_at > ?`, ip, now).Scan(&one) == nil {
		return "Your IP address is blocked by brute force protection. Try again later."
	}
	if username != "" && h.db.QueryRowContext(ctx, `SELECT 1 FROM hulk_blocks WHERE user = ? AND kind = 'user' AND expires_at > ?`, username, now).Scan(&one) == nil {
		return "This account is locked by brute force protection because of too many failed logins. Try again later."
	}
	return ""
}

// RecordPanelFailure counts a failed WhaCP sign-in and acts on it at once. It
// returns the number of new blocks (the caller syncs them to the server).
func (h *Hulk) RecordPanelFailure(ctx context.Context, username, ip string, hk HulkHooks) int {
	if !h.Enabled(ctx) {
		return 0
	}
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	if len(username) > 190 {
		username = username[:190]
	}
	ev := HulkEvent{ID: "p:" + hex.EncodeToString(rnd[:]), Time: h.now(hk), Service: "whacp", AuthService: "whacpd", User: username, IP: ip}
	stored := h.Store(ctx, 1, []HulkEvent{ev})
	return h.Evaluate(ctx, stored, hk)
}

// Store keeps the events not seen before (with their country) and returns the new ones.
func (h *Hulk) Store(ctx context.Context, serverID int64, events []HulkEvent) []HulkEvent {
	var added []HulkEvent
	for _, e := range events {
		if e.ID == "" || net.ParseIP(e.IP) == nil {
			continue
		}
		e.Country, _ = NewGeo(h.db).Country(ctx, e.IP)
		ok := 0
		if e.OK {
			ok = 1
		}
		res, err := h.db.ExecContext(ctx, `INSERT OR IGNORE INTO hulk_events (id, server_id, time, service, authservice, user, ip, country, ok) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, serverID, e.Time, clipRunes(e.Service, 40), clipRunes(e.AuthService, 60), clipRunes(e.User, 190), e.IP, e.Country, ok)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added = append(added, e)
		}
	}
	return added
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func (h *Hulk) count(ctx context.Context, q string, args ...any) int {
	var n int
	_ = h.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n
}

func (h *Hulk) active(ctx context.Context, hk HulkHooks, kind, col, value string) bool {
	var one int
	return h.db.QueryRowContext(ctx, `SELECT 1 FROM hulk_blocks WHERE kind = ? AND `+col+` = ? AND expires_at > ?`, kind, value, h.now(hk)).Scan(&one) == nil
}

// privileged: "root", and WhaCP administrator accounts (they follow the root option).
func (h *Hulk) privileged(ctx context.Context, user string) bool {
	if user == "root" {
		return true
	}
	var one int
	return h.db.QueryRowContext(ctx, `SELECT 1 FROM users u JOIN roles r ON r.id = u.role_id WHERE (u.username = ? OR u.email = ?) AND r.name = 'admin'`, user, user).Scan(&one) == nil
}

// Evaluate applies the rules to new events and returns the number of new blocks.
func (h *Hulk) Evaluate(ctx context.Context, events []HulkEvent, hk HulkHooks) int {
	now := h.now(hk)
	whitelist := h.Whitelist(ctx)
	made := 0
	for _, e := range events {
		if e.OK {
			if e.User == "root" {
				h.rootLogin(ctx, e.IP, e.Time, e.AuthService, hk, whitelist)
			}
			continue
		}
		if h.Whitelisted(whitelist, e.IP) || h.countryStatus(ctx, e.IP) == "whitelist" {
			continue
		}
		local := h.isLocal(ctx, e.IP)
		// IP address-based protection (never this server's own addresses).
		if h.boolSetting(ctx, "hulk_ip_enabled") && !local {
			period := int64(h.intSetting(ctx, "hulk_ip_period")) * 60
			count := h.count(ctx, `SELECT COUNT(*) FROM hulk_events WHERE ip = ? AND ok = 0 AND time > ?`, e.IP, now-period)
			day := h.count(ctx, `SELECT COUNT(*) FROM hulk_events WHERE ip = ? AND ok = 0 AND time > ?`, e.IP, now-86400)
			dayMax, ipMax := h.intSetting(ctx, "hulk_oneday_max"), h.intSetting(ctx, "hulk_ip_max")
			switch {
			case day >= dayMax && !h.active(ctx, hk, "oneday", "ip", e.IP):
				made += h.block(ctx, hk, "oneday", e.IP, "", e, day, dayMax, now+86400, h.boolSetting(ctx, "hulk_oneday_firewall"), "Too many failed logins in one day")
			case count >= ipMax && !h.active(ctx, hk, "ip", "ip", e.IP) && !h.active(ctx, hk, "oneday", "ip", e.IP):
				made += h.block(ctx, hk, "ip", e.IP, "", e, count, ipMax, now+period, h.boolSetting(ctx, "hulk_ip_firewall"), "Too many failed logins from the address")
			}
		}
		// Username-based protection.
		scope := h.strSetting(ctx, "hulk_user_scope")
		if h.boolSetting(ctx, "hulk_user_enabled") && e.User != "" && (scope != "local" || local) {
			if !h.boolSetting(ctx, "hulk_user_lock_root") && h.privileged(ctx, e.User) {
				continue
			}
			period := int64(h.intSetting(ctx, "hulk_user_period")) * 60
			count := h.count(ctx, `SELECT COUNT(*) FROM hulk_events WHERE user = ? AND ok = 0 AND time > ?`, e.User, now-period)
			max := h.intSetting(ctx, "hulk_user_max")
			if count >= max && !h.active(ctx, hk, "user", "user", e.User) {
				made += h.block(ctx, hk, "user", "", e.User, e, count, max, now+period, false, "Too many failed logins for the account")
			}
		}
	}
	return made
}

func (h *Hulk) block(ctx context.Context, hk HulkHooks, kind, ip, user string, e HulkEvent, failures, max int, expires int64, firewall bool, reason string) int {
	var ipV, userV any
	if ip != "" {
		ipV = ip
	}
	if user != "" {
		userV = user
	}
	fw := 0
	if firewall {
		fw = 1
	}
	if _, err := h.db.ExecContext(ctx, `INSERT INTO hulk_blocks (kind, ip, user, reason, service, authservice, failures, firewall, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		kind, ipV, userV, reason, e.Service, e.AuthService, failures, fw, h.now(hk), expires); err != nil {
		return 0
	}
	what := ip
	if user != "" {
		what = "account " + user
	}
	until := time.Unix(expires, 0).UTC()
	if hk.Audit != nil {
		hk.Audit(ctx, "hulk.block", "success", map[string]any{"kind": kind, "ip": ip, "user": user, "failures": failures, "until": until.Format(time.RFC3339), "firewall": firewall})
	}
	if hk.Notify != nil && h.boolSetting(ctx, "hulk_notify_brute") {
		subject := "attack"
		if user != "" {
			subject = "user"
		}
		level := ""
		if firewall {
			level = " at the firewall level"
		}
		hk.Notify(ctx, "warning", "Brute force "+subject+" detected: "+what,
			fmt.Sprintf("%s: %d failed logins (%s). Blocked until %s UTC%s.", reason, failures, e.AuthService, until.Format("2006-01-02 15:04"), level),
			"/security/hulk#report", fmt.Sprintf("hulk:%s:%s:%d", kind, what, expires))
	}
	return 1
}

// rootLogin notifies about a root login from an address not on the whitelist.
func (h *Hulk) rootLogin(ctx context.Context, ip string, at int64, service string, hk HulkHooks, whitelist []string) {
	if h.Whitelisted(whitelist, ip) || hk.Notify == nil {
		return
	}
	netblock := "/24"
	if strings.Contains(ip, ":") {
		netblock = "/64"
	}
	refs := append([]string{}, whitelist...)
	if rows, err := h.db.QueryContext(ctx, `SELECT DISTINCT ip FROM hulk_events WHERE user = 'root' AND ok = 1 AND ip <> ?`, ip); err == nil {
		for rows.Next() {
			var r string
			if rows.Scan(&r) == nil {
				refs = append(refs, r)
			}
		}
		rows.Close()
	}
	known := false
	for _, ref := range refs {
		base, _, _ := strings.Cut(ref, "/")
		if InNetwork(ip, base+netblock) {
			known = true
			break
		}
	}
	if known {
		if !h.boolSetting(ctx, "hulk_notify_root_netblock") {
			return
		}
	} else if !h.boolSetting(ctx, "hulk_notify_root") {
		return
	}
	extra := ""
	if known {
		extra = " (it is in a known netblock)"
	}
	hk.Notify(ctx, "warning", "Root login from "+ip,
		fmt.Sprintf("root signed in with %s from %s at %s UTC. The address is not on the brute force protection whitelist%s.", service, ip, time.Unix(at, 0).UTC().Format("2006-01-02 15:04"), extra),
		"/security/hulk", fmt.Sprintf("hulk:root:%s:%d", ip, at))
}

// Prune drops events past the retention and long-expired blocks.
func (h *Hulk) Prune(ctx context.Context, hk HulkHooks) {
	now := h.now(hk)
	_, _ = h.db.ExecContext(ctx, `DELETE FROM hulk_events WHERE time < ?`, now-int64(h.intSetting(ctx, "hulk_retention"))*60)
	_, _ = h.db.ExecContext(ctx, `DELETE FROM hulk_blocks WHERE expires_at < ?`, now-86400)
}

// FailureSince is the start of the window to ask the agent for failures: just
// before the newest stored service event, but no earlier than the retention.
func (h *Hulk) FailureSince(ctx context.Context, serverID int64, hk HulkHooks) int64 {
	var last sql.NullInt64
	_ = h.db.QueryRowContext(ctx, `SELECT MAX(time) FROM hulk_events WHERE server_id = ? AND id NOT LIKE 'p:%'`, serverID).Scan(&last)
	since := last.Int64 - 60
	if floor := h.now(hk) - int64(h.intSetting(ctx, "hulk_retention"))*60; floor > since {
		since = floor
	}
	return since
}

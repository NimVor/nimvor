package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"
)

// DiskQuotaService enforces the plans' disk space with Linux user quotas
// (agent quota.*). An administrator turns quotas on once per server; then every
// scheduler pass, and after website changes, Reconcile reads each website
// system user's use and sets its hard limit:
//
//   - an account's limit (its plan's disk space, with its overrides) is shared
//     by its system users: each may grow by an equal part of what is left,
//     re-divided every pass, so together they never pass the limit;
//   - a reseller's limit covers its customers' users as well; a user gets the
//     smaller of the two parts;
//   - administrators and accounts without a limit are not limited.
type DiskQuotaService struct {
	db       *sql.DB
	serverID int64
	agent    AgentCaller
	limits   *PlanLimits
	audit    Auditor
}

// NewDiskQuotaService builds the service.
func NewDiskQuotaService(db *sql.DB, serverID int64, agent AgentCaller, limits *PlanLimits, audit Auditor) *DiskQuotaService {
	return &DiskQuotaService{db: db, serverID: serverID, agent: agent, limits: limits, audit: audit}
}

// SplitQuota is the hard limit (KiB, 0 = none) of each system user
// (DiskQuotaService::split). owners: system user => owning account (0 none);
// used: KiB in use; limitKB: account => its limit (limited accounts only);
// parents: account => its reseller (0 none).
func SplitQuota(owners map[string]int64, used map[string]int64, limitKB map[int64]int64, parents map[int64]int64) map[string]int64 {
	members := map[int64][]string{}
	users := make([]string, 0, len(owners))
	for u := range owners {
		users = append(users, u)
	}
	sort.Strings(users)
	for _, u := range users {
		owner := owners[u]
		if owner == 0 {
			continue
		}
		for _, holder := range []int64{owner, parents[owner]} {
			if _, ok := limitKB[holder]; holder != 0 && ok {
				members[holder] = append(members[holder], u)
			}
		}
	}
	share := map[int64]int64{}
	for holder, us := range members {
		left := limitKB[holder]
		for _, u := range us {
			left -= used[u]
		}
		if left < 0 {
			left = 0
		}
		share[holder] = left / int64(len(us))
	}
	out := map[string]int64{}
	for _, u := range users {
		owner := owners[u]
		var parts []int64
		for _, holder := range []int64{owner, parents[owner]} {
			if owner == 0 {
				continue
			}
			if s, ok := share[holder]; holder != 0 && ok {
				parts = append(parts, s)
			}
		}
		if len(parts) == 0 {
			out[u] = 0
			continue
		}
		min := parts[0]
		for _, p := range parts[1:] {
			if p < min {
				min = p
			}
		}
		// 0 would mean "no limit": a full account still gets its use as the limit.
		v := used[u] + min
		if v < 1 {
			v = 1
		}
		out[u] = v
	}
	return out
}

// Reconcile sets each system user's hard limit from the plans and reads the use
// back; it returns what changed. Nothing happens until quotas are on.
func (s *DiskQuotaService) Reconcile(ctx context.Context) ([]string, error) {
	raw, err := s.agent.Call(ctx, "quota.status", map[string]any{}, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var st struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(raw, &st)
	if st.State != "on" {
		return nil, nil
	}
	owners := map[string]int64{}
	rows, err := s.db.QueryContext(ctx,
		`SELECT w.system_user, COALESCE((SELECT w2.owner_id FROM websites w2 WHERE w2.server_id = w.server_id AND w2.system_user = w.system_user ORDER BY w2.id LIMIT 1), 0)
		 FROM websites w WHERE w.server_id = ? AND w.system_user <> '' GROUP BY w.system_user`, s.serverID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var u string
		var o int64
		if rows.Scan(&u, &o) == nil {
			owners[u] = o
		}
	}
	rows.Close()
	known := map[string]int64{}
	if rows, err = s.db.QueryContext(ctx, `SELECT system_user, hard_kb FROM disk_quotas WHERE server_id = ?`, s.serverID); err == nil {
		for rows.Next() {
			var u string
			var h int64
			if rows.Scan(&u, &h) == nil {
				known[u] = h
			}
		}
		rows.Close()
	}
	seen := map[string]bool{}
	var users []string
	for u := range owners {
		seen[u] = true
		users = append(users, u)
	}
	for u := range known {
		if !seen[u] {
			users = append(users, u)
		}
	}
	if len(users) == 0 {
		return nil, nil
	}
	sort.Strings(users)
	rraw, err := s.agent.Call(ctx, "quota.report", map[string]any{"users": users}, 120*time.Second)
	if err != nil {
		return nil, err
	}
	var report struct {
		Users map[string]struct {
			UsedKB int64 `json:"used_kb"`
			HardKB int64 `json:"hard_kb"`
		} `json:"users"`
	}
	_ = json.Unmarshal(rraw, &report)
	used, current := map[string]int64{}, map[string]int64{}
	for u, q := range report.Users {
		used[u], current[u] = q.UsedKB, q.HardKB
	}
	// Limits in effect: plan and overrides; administrators are not limited.
	limitKB, parents := map[int64]int64{}, map[int64]int64{}
	accounts := map[int64]bool{}
	for _, o := range owners {
		if o != 0 {
			accounts[o] = true
		}
	}
	scopeOf := func(id int64) (string, sql.NullInt64, bool) {
		var scope string
		var parent sql.NullInt64
		err := s.db.QueryRowContext(ctx, `SELECT r.scope, u.parent_id FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = ?`, id).Scan(&scope, &parent)
		return scope, parent, err == nil
	}
	for id := range accounts {
		scope, parent, ok := scopeOf(id)
		if !ok {
			continue
		}
		if parent.Valid {
			parents[id] = parent.Int64
		}
		for _, holder := range []int64{id, parent.Int64} {
			if holder == 0 {
				continue
			}
			if _, done := limitKB[holder]; done {
				continue
			}
			hs := scope
			if holder != id {
				hs, _, _ = scopeOf(holder)
			}
			var mb int
			if hs != "all" {
				h := holder
				mb, _ = s.limits.Value(ctx, &h, "disk_quota_mb")
			}
			if mb > 0 {
				limitKB[holder] = int64(mb) * 1024
			}
		}
	}
	want := SplitQuota(owners, used, limitKB, parents)
	// Users without websites any more lose their limit.
	for u := range known {
		if _, ok := want[u]; !ok {
			want[u] = 0
		}
	}
	changes := map[string]int64{}
	for u, kb := range want {
		cur, ok := current[u]
		if !ok {
			continue // no such system user (yet)
		}
		var differs bool
		if kb == 0 || cur == 0 {
			differs = kb != cur
		} else {
			tol := kb / 100
			if tol < 1024 {
				tol = 1024
			}
			differs = int64(math.Abs(float64(kb-cur))) > tol
		}
		if differs {
			changes[u] = kb
		}
	}
	var lines []string
	if len(changes) > 0 {
		set, err := s.agent.Call(ctx, "quota.set", map[string]any{"limits": changes}, 120*time.Second)
		if err != nil {
			return lines, err
		}
		var res struct {
			Failed map[string]string `json:"failed"`
		}
		_ = json.Unmarshal(set, &res)
		names := make([]string, 0, len(changes))
		for u := range changes {
			names = append(names, u)
		}
		sort.Strings(names)
		for _, u := range names {
			kb := changes[u]
			if msg, bad := res.Failed[u]; bad {
				lines = append(lines, u+": disk limit not set: "+msg)
				continue
			}
			current[u] = kb
			if kb == 0 {
				lines = append(lines, u+": disk limit removed")
			} else {
				lines = append(lines, fmt.Sprintf("%s: disk limit set to %d MB", u, int64(math.Round(float64(kb)/1024))))
			}
		}
		if s.audit != nil {
			result := "success"
			if len(res.Failed) > 0 {
				result = "failure"
			}
			failed := make([]string, 0, len(res.Failed))
			for u := range res.Failed {
				failed = append(failed, u)
			}
			sort.Strings(failed)
			s.audit(ctx, "quota.apply", "server", "quota", result, map[string]any{"limits_kb": changes, "failed": failed})
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return lines, err
	}
	defer tx.Rollback()
	for u := range known {
		if _, ok := current[u]; !ok {
			// The system user no longer exists.
			if _, err := tx.ExecContext(ctx, `DELETE FROM disk_quotas WHERE server_id = ? AND system_user = ?`, s.serverID, u); err != nil {
				return lines, err
			}
		}
	}
	for u, hard := range current {
		if _, mine := owners[u]; !mine && hard == 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM disk_quotas WHERE server_id = ? AND system_user = ?`, s.serverID, u); err != nil {
				return lines, err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO disk_quotas (server_id, system_user, used_kb, hard_kb, measured_at) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT (server_id, system_user) DO UPDATE SET used_kb = excluded.used_kb, hard_kb = excluded.hard_kb, measured_at = excluded.measured_at`,
			s.serverID, u, used[u], hard, now); err != nil {
			return lines, err
		}
	}
	return lines, tx.Commit()
}

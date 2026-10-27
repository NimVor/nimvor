package hosting

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// BandwidthLimitService enforces the plans' monthly bandwidth (data transfer).
// An account whose websites (a reseller: with its customers' websites) sent
// more than its plan allows this month is marked for the month; its websites
// then answer "509 Bandwidth limit exceeded" (certificate renewals still work).
// The mark ends when the month ends, the limit is raised or removed, or an
// administrator resets the month's counter. Administrators are never limited.
//
// Reconcile runs after every bandwidth measurement (scheduler) and after plan,
// limit and counter changes. It re-applies exactly the websites whose state on
// this server differs from their account's.
type BandwidthLimitService struct {
	db       *sql.DB
	serverID int64
	limits   *PlanLimits
	sites    *WebsiteRepository
	websites *WebsiteService
	audit    Auditor
	// Notify records a notification for the account (nil: none).
	Notify func(ctx context.Context, ownerID int64, title, body, key string)
	Now    func() time.Time
}

// NewBandwidthLimitService builds the service.
func NewBandwidthLimitService(db *sql.DB, serverID int64, limits *PlanLimits, sites *WebsiteRepository, websites *WebsiteService, audit Auditor) *BandwidthLimitService {
	return &BandwidthLimitService{db: db, serverID: serverID, limits: limits, sites: sites, websites: websites, audit: audit, Now: time.Now}
}

func (s *BandwidthLimitService) record(ctx context.Context, action string, id int64, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, "user", fmt.Sprint(id), "success", details)
	}
}

// Reconcile marks and unmarks accounts and re-applies the websites that differ.
func (s *BandwidthLimitService) Reconcile(ctx context.Context) []string {
	month := s.Now().UTC().Format("2006-01")
	type acct struct {
		id           int64
		name, marked string
		scope        string
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.username, u.bw_blocked_month, r.scope FROM users u JOIN roles r ON r.id = u.role_id WHERE u.plan_id IS NOT NULL OR u.bw_blocked_month <> ''`)
	if err != nil {
		return []string{"bandwidth limits not checked: " + err.Error()}
	}
	var accounts []acct
	for rows.Next() {
		var a acct
		if rows.Scan(&a.id, &a.name, &a.marked, &a.scope) == nil {
			accounts = append(accounts, a)
		}
	}
	rows.Close()
	var lines []string
	for _, a := range accounts {
		id := a.id
		limit := 0
		if a.scope != "all" {
			limit, _ = s.limits.Value(ctx, &id, "bandwidth_mb")
		}
		used := 0
		if limit > 0 {
			used = s.limits.BandwidthUsedMB(ctx, s.limits.Owners(ctx, a.id))
		}
		over := limit > 0 && used > limit
		switch {
		case over && a.marked != month:
			_, _ = s.db.ExecContext(ctx, `UPDATE users SET bw_blocked_month = ? WHERE id = ?`, month, a.id)
			s.record(ctx, "account.bandwidth_block", a.id, map[string]any{"username": a.name, "month": month, "used_mb": used, "limit_mb": limit})
			if s.Notify != nil {
				s.Notify(ctx, a.id, "Monthly bandwidth used up: "+a.name,
					fmt.Sprintf("%s sent %d MB of the %d MB its plan allows in %s. Its websites answer \"509 Bandwidth limit exceeded\" until the month ends, the plan allows more or the counter is reset.", a.name, used, limit, s.Now().UTC().Format("January 2006")),
					fmt.Sprintf("bandwidth-block:%d:%s", a.id, month))
			}
			lines = append(lines, fmt.Sprintf("%s: over its monthly bandwidth (%d of %d MB); websites blocked", a.name, used, limit))
		case !over && a.marked != "":
			_, _ = s.db.ExecContext(ctx, `UPDATE users SET bw_blocked_month = '' WHERE id = ?`, a.id)
			if a.marked == month {
				s.record(ctx, "account.bandwidth_unblock", a.id, map[string]any{"username": a.name, "month": month, "used_mb": used, "limit_mb": limit})
			}
			lines = append(lines, a.name+": within its monthly bandwidth again; websites unblocked")
		}
	}
	// The websites on this server whose state differs.
	stale, err := s.db.QueryContext(ctx,
		`SELECT w.id FROM websites w LEFT JOIN users u ON u.id = w.owner_id WHERE w.server_id = ?
		 AND w.bandwidth_applied <> (CASE WHEN u.id IS NOT NULL AND (u.bw_blocked_month = ? OR EXISTS (SELECT 1 FROM users bp WHERE bp.id = u.parent_id AND bp.bw_blocked_month = ?)) THEN 1 ELSE 0 END)`, s.serverID, month, month)
	if err != nil {
		return append(lines, "bandwidth limits not applied: "+err.Error())
	}
	var ids []int64
	for stale.Next() {
		var id int64
		if stale.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	stale.Close()
	for _, id := range ids {
		site, err := s.sites.FindFull(ctx, s.serverID, id, nil)
		if err != nil || site == nil {
			continue
		}
		if err := s.websites.Reapply(ctx, site); err != nil {
			lines = append(lines, site.Domain+": not applied: "+problem(err))
		}
	}
	return lines
}

package hosting

import (
	"context"
	"database/sql"
	"strings"
)

// PlanFieldLabels are the plan allowances' names (PlanService::FIELDS).
var PlanFieldLabels = map[string]string{
	"disk_quota_mb": "Disk space quota (MB)", "bandwidth_mb": "Monthly bandwidth (MB)", "max_websites": "Websites",
	"max_subdomains": "Subdomains", "max_parked_domains": "Parked domains (aliases)", "max_addon_domains": "Addon domains",
	"max_databases": "SQL databases", "max_ftp_accounts": "FTP accounts", "max_email_accounts": "Email accounts",
	"max_email_quota_mb": "Max quota per email address (MB)", "max_cron_jobs": "Cron jobs", "max_workers": "Workers (applications)",
	"max_customers": "Customers (resellers)",
}

// LimitRow is one line of an account's "Plan and limits": the plan's value,
// the account's own override and the value in effect (nil: no plan / no
// override).
type LimitRow struct {
	Column, Label             string
	Plan, Override, Effective *int
}

// LimitsTable lists the plan values, the account's overrides and the values in
// effect (AccountAdminService::limitsTable). Without a plan only labels and
// overrides are known.
func (p *PlanLimits) LimitsTable(ctx context.Context, userID int64, planID sql.NullInt64) []LimitRow {
	var overrides sql.NullString
	_ = p.db.QueryRowContext(ctx, `SELECT limit_overrides FROM users WHERE id = ?`, userID).Scan(&overrides)
	var planVals []int
	if planID.Valid {
		vals := make([]int, len(PlanFields))
		dest := make([]any, len(PlanFields))
		for i := range vals {
			dest[i] = &vals[i]
		}
		if p.db.QueryRowContext(ctx, `SELECT `+strings.Join(PlanFields, ", ")+` FROM plans WHERE id = ?`, planID.Int64).Scan(dest...) == nil {
			planVals = vals
		}
	}
	rows := make([]LimitRow, 0, len(PlanFields))
	for i, col := range PlanFields {
		r := LimitRow{Column: col, Label: PlanFieldLabels[col]}
		if planVals != nil {
			v := planVals[i]
			r.Plan, r.Effective = &v, &v
		}
		if overrides.Valid {
			if o, has := decodeOverride(overrides.String, col); has {
				r.Override = &o
				if planVals != nil {
					r.Effective = &o
				}
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// UsageRow is a resource's use against the plan's limit (0 = unlimited).
type UsageRow struct {
	Key, Label  string
	Used, Limit int
	Percent     int
	Class       string
}

var usageResources = []struct{ Key, Column, Label string }{
	{"websites", "max_websites", "Websites"}, {"subdomains", "max_subdomains", "Subdomains"},
	{"parked_domains", "max_parked_domains", "Parked domains"}, {"addon_domains", "max_addon_domains", "Addon domains"},
	{"databases", "max_databases", "SQL databases"}, {"email_accounts", "max_email_accounts", "Email accounts"},
	{"cron_jobs", "max_cron_jobs", "Cron jobs"}, {"workers", "max_workers", "Workers"}, {"customers", "max_customers", "Customers"},
}

// Usage is the account's use of each resource, with its customers' for a
// reseller (PlanLimits::usage). FTP accounts live on the server and are not
// counted here.
func (p *PlanLimits) Usage(ctx context.Context, userID int64) []UsageRow {
	owners := p.family(ctx, userID)
	limit := func(col string) int {
		id := userID
		v, _ := p.Value(ctx, &id, col)
		return v
	}
	var out []UsageRow
	add := func(key, label string, used, lim int) {
		r := UsageRow{Key: key, Label: label, Used: used, Limit: lim}
		if lim > 0 {
			r.Percent = used * 100 / lim
			if r.Percent > 100 {
				r.Percent = 100
			}
			switch {
			case used >= lim:
				r.Class = "bg-danger"
			case r.Percent >= 80:
				r.Class = "bg-warning"
			}
		}
		out = append(out, r)
	}
	add("disk", "Disk space (MB)", p.DiskUsedMB(ctx, owners), limit("disk_quota_mb"))
	add("bandwidth", "Bandwidth this month (MB)", p.BandwidthUsedMB(ctx, owners), limit("bandwidth_mb"))
	for _, r := range usageResources {
		n, _ := p.countFor(ctx, r.Key, owners)
		add(r.Key, r.Label, n, limit(r.Column))
	}
	return out
}

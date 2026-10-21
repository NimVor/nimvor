package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// PlanLimits enforces a plan's resource caps, ported from Services\PlanLimits.
// A reseller's customers count against the reseller's plan too. Account-level
// limit_overrides adjust the owner's own plan. A limit of 0 means unlimited.
type PlanLimits struct{ db *sql.DB }

// NewPlanLimits wraps a database handle.
func NewPlanLimits(db *sql.DB) *PlanLimits { return &PlanLimits{db: db} }

// resourceColumn maps a resource key to its plan column and human label
// (PlanLimits::RESOURCES, the subset the ported write paths enforce). The
// counting query in countFor must gain the matching case.
var resourceColumn = map[string][2]string{
	"websites":       {"max_websites", "Websites"},
	"subdomains":     {"max_subdomains", "Subdomains"},
	"parked_domains": {"max_parked_domains", "Parked domains"},
	"addon_domains":  {"max_addon_domains", "Addon domains"},
	"databases":      {"max_databases", "SQL databases"},
}

// AssertCanCreate returns a *UserError if creating `adding` more of `resource`
// would exceed the owner's plan (or the owner's reseller-parent plan). A nil
// ownerId (staff-owned, unassigned) or unknown resource is unlimited. Mirrors
// PlanLimits::assertCanCreate.
func (p *PlanLimits) AssertCanCreate(ctx context.Context, resource string, ownerID *int64, adding int) error {
	col, ok := resourceColumn[resource]
	if !ok || ownerID == nil {
		return nil
	}
	column, label := col[0], col[1]

	// The owner's own plan; a reseller's customers also count against the
	// reseller's plan (checked second).
	checks := [][2]any{{*ownerID, p.family(ctx, *ownerID)}}
	if parent := p.parentOf(ctx, *ownerID); parent != nil && resource != "customers" {
		checks = append(checks, [2]any{*parent, p.family(ctx, *parent)})
	}

	for _, chk := range checks {
		planUser := chk[0].(int64)
		owners := chk[1].([]int64)
		planName, limit, ok, err := p.planLimit(ctx, planUser, column)
		if err != nil {
			return err
		}
		if !ok || limit <= 0 {
			continue
		}
		used, err := p.countFor(ctx, resource, owners)
		if err != nil {
			return err
		}
		if used+adding > limit {
			whose := "Your plan"
			if planUser != *ownerID {
				whose = "Your provider's plan"
			}
			return &UserError{
				What: fmt.Sprintf("%s allows at most %d %s.", whose, limit, limitLabel(label)),
				Why:  fmt.Sprintf("The limit of the %q plan has been reached.", planName),
				Fix:  "Delete an existing one or ask your provider to upgrade the plan.",
			}
		}
	}
	return nil
}

// limitLabel renders the resource label the way the message wants it
// (PlanLimits::assertCanCreate): "SQL databases" keeps its casing (second
// letter capital), everything else is lowercased ("websites", "parked domains").
func limitLabel(label string) string {
	if len(label) >= 2 {
		if c := label[1]; c >= 'A' && c <= 'Z' {
			return label
		}
	}
	return strings.ToLower(label[:1]) + label[1:]
}

// planLimit returns the plan name and the effective limit for a column (plan
// value with the account's limit_overrides applied). ok is false when the user
// has no plan.
func (p *PlanLimits) planLimit(ctx context.Context, userID int64, column string) (name string, limit int, ok bool, err error) {
	var overrides sql.NullString
	var planVal int
	// Only known numeric plan columns are interpolated; column comes from the
	// internal resourceColumn map, never user input.
	q := "SELECT p.name, p." + column + ", u.limit_overrides FROM plans p JOIN users u ON u.plan_id = p.id WHERE u.id = ?"
	err = p.db.QueryRowContext(ctx, q, userID).Scan(&name, &planVal, &overrides)
	if err == sql.ErrNoRows {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	limit = planVal
	if overrides.Valid {
		if v, has := decodeOverride(overrides.String, column); has {
			limit = v
		}
	}
	return name, limit, true, nil
}

// decodeOverride reads one integer override for a column from the JSON blob,
// accepting only non-negative integers (mirrors PlanLimits::decodeOverrides).
func decodeOverride(blob, column string) (int, bool) {
	var m map[string]any
	if json.Unmarshal([]byte(blob), &m) != nil {
		return 0, false
	}
	v, ok := m[column]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		if n >= 0 && n == float64(int(n)) {
			return int(n), true
		}
	case string:
		if i, err := strconv.Atoi(n); err == nil && i >= 0 {
			return i, true
		}
	}
	return 0, false
}

// family returns the user and their direct customers.
func (p *PlanLimits) family(ctx context.Context, userID int64) []int64 {
	ids := []int64{userID}
	rows, err := p.db.QueryContext(ctx, "SELECT id FROM users WHERE parent_id = ?", userID)
	if err != nil {
		return ids
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func (p *PlanLimits) parentOf(ctx context.Context, userID int64) *int64 {
	var parent sql.NullInt64
	if err := p.db.QueryRowContext(ctx, "SELECT parent_id FROM users WHERE id = ?", userID).Scan(&parent); err != nil {
		return nil
	}
	if !parent.Valid {
		return nil
	}
	v := parent.Int64
	return &v
}

// countFor counts existing resources owned by the given users. Mirrors the
// relevant case of PlanLimits::count.
func (p *PlanLimits) countFor(ctx context.Context, resource string, owners []int64) (int, error) {
	if len(owners) == 0 {
		return 0, nil
	}
	in := make([]string, len(owners))
	args := make([]any, len(owners))
	for i, o := range owners {
		in[i] = "?"
		args[i] = o
	}
	var q string
	join := strings.Join(in, ",")
	switch resource {
	// Primary sites and addon domains count toward the website limit (an
	// addon domain is a separate site); subdomains do not — they have their
	// own limit.
	case "websites":
		q = "SELECT COUNT(*) FROM websites c LEFT JOIN websites p ON p.id = c.parent_id WHERE c.owner_id IN (" + join + ") AND (c.parent_id IS NULL OR c.domain NOT LIKE '%.' || p.domain)"
	case "subdomains":
		q = "SELECT COUNT(*) FROM websites c JOIN websites p ON p.id = c.parent_id WHERE c.owner_id IN (" + join + ") AND c.domain LIKE '%.' || p.domain"
	case "addon_domains":
		q = "SELECT COUNT(*) FROM websites c JOIN websites p ON p.id = c.parent_id WHERE c.owner_id IN (" + join + ") AND c.domain NOT LIKE '%.' || p.domain"
	case "parked_domains":
		q = "SELECT COUNT(*) FROM domains d JOIN websites w ON w.id = d.website_id WHERE d.is_primary = 0 AND w.owner_id IN (" + join + ")"
	case "databases":
		q = "SELECT COUNT(*) FROM databases WHERE owner_id IN (" + join + ")"
	default:
		return 0, fmt.Errorf("planlimits: unsupported resource %q", resource)
	}
	var n int
	if err := p.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

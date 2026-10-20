package hosting

import (
	"context"
	"database/sql"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// Plan is a hosting plan for the admin list, with its user count and owner.
type Plan struct {
	ID           int64
	Name         string
	OwnerName    sql.NullString
	Users        int
	MaxWebsites  int
	MaxDatabases int
	DiskQuotaMB  int
	BandwidthMB  int
}

// PlanRepository reads hosting plans.
type PlanRepository struct{ db *sql.DB }

// NewPlanRepository wraps a database handle.
func NewPlanRepository(db *sql.DB) *PlanRepository { return &PlanRepository{db: db} }

// Visible lists the plans an actor may see: staff see every plan (global plans,
// owner_id NULL, plus every reseller's), a reseller sees only their own
// (owner_id = actor). Mirrors PlanService::visible.
func (r *PlanRepository) Visible(ctx context.Context, actor *identity.User) ([]Plan, error) {
	base := `SELECT p.id, p.name, o.username AS owner_name,
	         (SELECT COUNT(*) FROM users u WHERE u.plan_id = p.id) AS users,
	         p.max_websites, p.max_databases, p.disk_quota_mb, p.bandwidth_mb
	         FROM plans p LEFT JOIN users o ON o.id = p.owner_id`
	var (
		q    string
		args []any
	)
	if IsAll(actor) {
		q = base + ` ORDER BY p.owner_id IS NOT NULL, p.name`
	} else {
		var id int64
		if actor != nil {
			id = actor.ID
		}
		q = base + ` WHERE p.owner_id = ? ORDER BY p.name`
		args = append(args, id)
	}
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.Name, &p.OwnerName, &p.Users, &p.MaxWebsites, &p.MaxDatabases, &p.DiskQuotaMB, &p.BandwidthMB); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

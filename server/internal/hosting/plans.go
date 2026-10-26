package hosting

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

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
	Description  string
	Fields       map[string]int // every PlanFields column
	Limits       map[string]int // every PlanLimitColumns column
	Ffmpeg       bool
	FeatureList  sql.NullInt64
}

// PlanRepository reads hosting plans.
type PlanRepository struct{ db *sql.DB }

// NewPlanRepository wraps a database handle.
func NewPlanRepository(db *sql.DB) *PlanRepository { return &PlanRepository{db: db} }

// Visible lists the plans an actor may see: staff see every plan (global plans,
// owner_id NULL, plus every reseller's), a reseller sees only their own
// (owner_id = actor). Mirrors PlanService::visible.
func (r *PlanRepository) Visible(ctx context.Context, actor *identity.User) ([]Plan, error) {
	limitCols := []string{"cpu_percent", "memory_mb", "tasks_max", "io_read_mbps", "io_write_mbps"}
	cols := append(append([]string{}, PlanFields...), limitCols...)
	base := `SELECT p.id, p.name, o.username AS owner_name,
	         (SELECT COUNT(*) FROM users u WHERE u.plan_id = p.id) AS users,
	         p.description, p.ffmpeg, p.feature_list_id, p.` + strings.Join(cols, ", p.") + `
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
		var ffmpeg int
		vals := make([]int, len(cols))
		dest := []any{&p.ID, &p.Name, &p.OwnerName, &p.Users, &p.Description, &ffmpeg, &p.FeatureList}
		for i := range vals {
			dest = append(dest, &vals[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		p.Ffmpeg = ffmpeg == 1
		p.Fields, p.Limits = map[string]int{}, map[string]int{}
		for i, c := range cols {
			if i < len(PlanFields) {
				p.Fields[c] = vals[i]
			} else {
				p.Limits[c] = vals[i]
			}
		}
		p.MaxWebsites, p.MaxDatabases = p.Fields["max_websites"], p.Fields["max_databases"]
		p.DiskQuotaMB, p.BandwidthMB = p.Fields["disk_quota_mb"], p.Fields["bandwidth_mb"]
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlanFields lists the resource-allowance columns for plan form validation
// (PlanService::FIELDS order).
var PlanFields = []string{
	"disk_quota_mb", "bandwidth_mb", "max_websites", "max_subdomains",
	"max_parked_domains", "max_addon_domains", "max_databases", "max_ftp_accounts",
	"max_email_accounts", "max_email_quota_mb", "max_cron_jobs", "max_workers",
	"max_customers",
}

// PlanLimitColumns lists cgroup-style limit columns with (min, max) when set
// (PlanService::LIMITS).
var PlanLimitColumns = map[string][2]int{
	"cpu_percent":   {10, 12800},
	"memory_mb":     {64, 2097152},
	"tasks_max":     {16, 1048576},
	"io_read_mbps":  {1, 100000},
	"io_write_mbps": {1, 100000},
}

// PlanSave is the validated input to PlanRepository.Save.
type PlanSave struct {
	ID          *int64 // nil = create
	Name        string
	Description string
	Fields      map[string]int
	Limits      map[string]int
	FeatureList *int64
	Ffmpeg      bool
}

// Save creates or updates a plan visible to the actor (PlanService::save).
// Reseller plans cannot set max_customers, feature_list_id, or ffmpeg;
// those are forced to 0 / nil for non-staff. It validates name uniqueness
// via the COALESCE(owner_id,0),name index and returns a ValidationError for
// field-level problems.
func (r *PlanRepository) Save(ctx context.Context, actor *identity.User, in PlanSave) (int64, error) {
	name := strings.TrimSpace(in.Name)
	desc := in.Description
	errs := map[string]string{}
	if name == "" || len(name) > 60 {
		errs["name"] = "Enter a name (up to 60 characters)."
	}
	if len(desc) > 300 {
		errs["description"] = "Keep the description under 300 characters."
	}
	fields := map[string]int{}
	for _, f := range PlanFields {
		v, ok := in.Fields[f]
		if !ok {
			v = 0
		}
		if v < 0 || v > 10_000_000 {
			errs[f] = "Enter 0 (unlimited) or a positive number."
			continue
		}
		fields[f] = v
	}
	limits := map[string]int{}
	for f, rng := range PlanLimitColumns {
		v, ok := in.Limits[f]
		if !ok {
			v = 0
		}
		if v != 0 && (v < rng[0] || v > rng[1]) {
			errs[f] = "Enter 0 (no limit) or a value in the allowed range."
			continue
		}
		limits[f] = v
	}
	var ownerID any
	if !IsAll(actor) {
		ownerID = actor.ID
		fields["max_customers"] = 0 // resellers cannot resell
	}
	fl := in.FeatureList
	if !IsAll(actor) {
		fl = nil
	}
	if IsAll(actor) && fl != nil {
		var exists int64
		if err := r.db.QueryRowContext(ctx, "SELECT id FROM feature_lists WHERE id = ?", *fl).Scan(&exists); err != nil {
			errs["feature_list_id"] = "Choose an existing feature list."
		}
	}
	if len(errs) > 0 {
		return 0, &ValidationError{Fields: errs}
	}
	// Uniqueness check via the owner+name index. For updates we first verify
	// the plan exists and is visible to the actor, then exclude it by id.
	var ownerKey any = 0
	if ownerID != nil {
		ownerKey = ownerID
	}
	dupArgs := []any{ownerKey, name}
	dupQ := "SELECT id FROM plans WHERE COALESCE(owner_id, 0) = ? AND name = ?"
	excludeID := int64(0)
	if in.ID != nil {
		excludeID = *in.ID
		var exists int64
		if err := r.db.QueryRowContext(ctx, "SELECT id FROM plans WHERE id = ?", excludeID).Scan(&exists); err != nil {
			return 0, ErrNotFound
		}
		if !IsAll(actor) {
			var rowOwner sql.NullInt64
			_ = r.db.QueryRowContext(ctx, "SELECT owner_id FROM plans WHERE id = ?", excludeID).Scan(&rowOwner)
			if !rowOwner.Valid || rowOwner.Int64 != actor.ID {
				return 0, ErrNotFound
			}
		}
		dupQ += " AND id != ?"
		dupArgs = append(dupArgs, excludeID)
	}
	var dup int64
	if err := r.db.QueryRowContext(ctx, dupQ, dupArgs...).Scan(&dup); err == nil {
		errs["name"] = "You already have a plan with this name."
		return 0, &ValidationError{Fields: errs}
	}
	ffmpeg := 0
	if in.Ffmpeg {
		ffmpeg = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var id int64
	fieldCols := PlanFields
	fieldQ := ""
	for i, c := range fieldCols {
		if i > 0 {
			fieldQ += ", "
		}
		fieldQ += c
	}
	if in.ID == nil {
		place := ""
		vals := []any{ownerID, name, desc}
		for i, c := range fieldCols {
			_ = c
			if i > 0 {
				place += ", "
			}
			place += "?"
			vals = append(vals, fields[fieldCols[i]])
		}
		vals = append(vals, now, now)
		res, err := tx.ExecContext(ctx,
			"INSERT INTO plans (owner_id, name, description, "+fieldQ+", created_at, updated_at) VALUES (?, ?, ?, "+place+", ?, ?)",
			vals...,
		)
		if err != nil {
			return 0, err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return 0, err
		}
	} else {
		id = *in.ID
		sets := "name = ?, description = ?"
		vals := []any{name, desc}
		for _, c := range fieldCols {
			sets += ", " + c + " = ?"
			vals = append(vals, fields[c])
		}
		sets += ", updated_at = ?"
		vals = append(vals, now, id)
		if _, err := tx.ExecContext(ctx, "UPDATE plans SET "+sets+" WHERE id = ?", vals...); err != nil {
			return 0, err
		}
	}
	if IsAll(actor) {
		var flArg any
		if fl != nil {
			flArg = *fl
		}
		if _, err := tx.ExecContext(ctx, "UPDATE plans SET feature_list_id = ? WHERE id = ?", flArg, id); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE plans SET ffmpeg = ? WHERE id = ?", ffmpeg, id); err != nil {
		return 0, err
	}
	limSets := ""
	limVals := []any{}
	first := true
	for c := range PlanLimitColumns {
		if !first {
			limSets += ", "
		}
		limSets += c + " = ?"
		limVals = append(limVals, limits[c])
		first = false
	}
	limVals = append(limVals, id)
	if _, err := tx.ExecContext(ctx, "UPDATE plans SET "+limSets+" WHERE id = ?", limVals...); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// Delete removes a plan if it has no assigned users (PlanService::delete).
func (r *PlanRepository) Delete(ctx context.Context, actor *identity.User, id int64) (string, error) {
	var name string
	var owner sql.NullInt64
	var users int
	row := r.db.QueryRowContext(ctx, "SELECT p.name, p.owner_id, (SELECT COUNT(*) FROM users u WHERE u.plan_id = p.id) FROM plans p WHERE p.id = ?", id)
	if err := row.Scan(&name, &owner, &users); err != nil {
		return "", ErrNotFound
	}
	if !IsAll(actor) {
		// Resellers may only delete their own plans.
		if !owner.Valid || owner.Int64 != actor.ID {
			return "", ErrNotFound
		}
	}
	if users > 0 {
		return "", &UserError{What: "This plan is still assigned to " + strconv.Itoa(users) + " user(s).", Fix: "Move them to another plan first."}
	}
	if _, err := r.db.ExecContext(ctx, "DELETE FROM plans WHERE id = ?", id); err != nil {
		return "", err
	}
	return name, nil
}

// AssertAssignable checks that the plan is one the actor may assign to a
// user (PlanService::assertAssignable): a staff member any plan, a reseller
// only their own. A nil plan id is always fine.
func (r *PlanRepository) AssertAssignable(ctx context.Context, actor *identity.User, planID *int64) error {
	if planID == nil {
		return nil
	}
	visible, err := r.Visible(ctx, actor)
	if err != nil {
		return err
	}
	for _, p := range visible {
		if p.ID == *planID {
			return nil
		}
	}
	return &ValidationError{Fields: map[string]string{"plan_id": "Choose one of your plans."}}
}

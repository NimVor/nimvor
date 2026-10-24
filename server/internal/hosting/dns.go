package hosting

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// Zone is a DNS zone joined to the website it belongs to (if any), with a
// record count. Mirrors the display shape of DnsService::zones.
type Zone struct {
	ID        int64
	Name      string
	WebsiteID sql.NullInt64
	Website   sql.NullString // the attached website's domain, if any
	Records   int
	CreatedAt string
}

// DnsRepository reads DNS zones with tenant scoping applied. Zones use the
// website-aware scope: a zone an administrator created for a customer's
// website is visible to that customer too (Scope.WithWebsite).
type DnsRepository struct{ db *sql.DB }

// NewDnsRepository wraps a database handle.
func NewDnsRepository(db *sql.DB) *DnsRepository { return &DnsRepository{db: db} }

// Zones lists the DNS zones on a server visible to the user. Mirrors
// DnsService::zones (scope on z.owner_id, extended to z.website_id).
func (r *DnsRepository) Zones(ctx context.Context, serverID int64, u *identity.User) ([]Zone, error) {
	cond, params := WithWebsite(u, "z.owner_id", "z.website_id")
	args := append([]any{serverID}, params...)
	rows, err := r.db.QueryContext(ctx, `
		SELECT z.id, z.name, z.website_id, w.domain AS website,
		       (SELECT COUNT(*) FROM dns_records rr WHERE rr.zone_id = z.id) AS records, z.created_at
		FROM dns_zones z LEFT JOIN websites w ON w.id = z.website_id
		WHERE z.server_id = ? AND `+cond+` ORDER BY z.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Zone
	for rows.Next() {
		var z Zone
		if err := rows.Scan(&z.ID, &z.Name, &z.WebsiteID, &z.Website, &z.Records, &z.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

// DnsRecord is one dns_records row (DnsService::records).
type DnsRecord struct {
	ID       int64
	Name     string
	Type     string
	TTL      int
	Value    string
	Priority int
	Weight   int
	Port     int
	Disabled bool
}

// Records lists a zone's records in the canonical display order
// (DnsService::records).
func (r *DnsRepository) Records(ctx context.Context, zoneID int64) ([]DnsRecord, error) {
	rows, err := r.db.QueryContext(ctx, `\
		SELECT id, name, type, ttl, value, priority, weight, port, disabled
		FROM dns_records WHERE zone_id = ?
		ORDER BY CASE name WHEN '@' THEN 0 ELSE 1 END, name,
			CASE type WHEN 'NS' THEN 0 WHEN 'A' THEN 1 WHEN 'AAAA' THEN 2 WHEN 'CNAME' THEN 3 WHEN 'MX' THEN 4 ELSE 5 END, id`, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DnsRecord
	for rows.Next() {
		var rec DnsRecord
		var disabled int
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.Type, &rec.TTL, &rec.Value, &rec.Priority, &rec.Weight, &rec.Port, &disabled); err != nil {
			return nil, err
		}
		rec.Disabled = disabled == 1
		out = append(out, rec)
	}
	return out, rows.Err()
}

// Zone returns one zone scoped to the user (DnsService::zone).
func (r *DnsRepository) Zone(ctx context.Context, serverID, id int64, u *identity.User) (*DnsZoneFull, error) {
	cond, params := WithWebsite(u, "z.owner_id", "z.website_id")
	args := append([]any{id, serverID}, params...)
	var z DnsZoneFull
	err := r.db.QueryRowContext(ctx,
		`SELECT z.id, z.name, z.website_id, z.owner_id, z.ttl, z.primary_ns, z.hostmaster, z.secondaries, z.serial, z.applied_at, w.domain
		 FROM dns_zones z LEFT JOIN websites w ON w.id = z.website_id
		 WHERE z.id = ? AND z.server_id = ? AND `+cond, args...).
		Scan(&z.ID, &z.Name, &z.WebsiteID, &z.OwnerID, &z.TTL, &z.PrimaryNS, &z.Hostmaster, &z.Secondaries, &z.Serial, &z.AppliedAt, &z.Website)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &z, nil
}

// ---- writing (WebsiteService::addSubdomainRecord) -------------------------

// DnsZoneFull is a DNS zone row (the writer needs the push fields; the
// zone pages need the serial, the attached website and the scope columns).
type DnsZoneFull struct {
	ID          int64
	Name        string
	WebsiteID   sql.NullInt64
	OwnerID     sql.NullInt64
	TTL         int
	PrimaryNS   string
	Hostmaster  string
	Secondaries string
	Serial      int
	AppliedAt   sql.NullString
	Website     sql.NullString
}

// ZoneForWebsite returns the hosted zone attached to a website, or nil
// (DnsService::forWebsite).
func (r *DnsRepository) ZoneForWebsite(ctx context.Context, websiteID int64) (*DnsZoneFull, error) {
	var z DnsZoneFull
	err := r.db.QueryRowContext(ctx,
		"SELECT id, name, website_id, owner_id, ttl, primary_ns, hostmaster, secondaries, serial FROM dns_zones WHERE website_id = ?",
		websiteID).Scan(&z.ID, &z.Name, &z.WebsiteID, &z.OwnerID, &z.TTL, &z.PrimaryNS, &z.Hostmaster, &z.Secondaries, &z.Serial)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &z, nil
}

// RecordNameExists reports whether the zone already has a record for a name
// (DnsService::records, name match).
func (r *DnsRepository) RecordNameExists(ctx context.Context, zoneID int64, name string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND name = ?", zoneID, name).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// InsertRecord adds one DNS record (DnsService::insertRecord, minimal shape:
// the subdomain A record the panel writes itself).
func (r *DnsRepository) InsertRecord(ctx context.Context, zoneID int64, name, typ, value string, ttl, priority, weight, port int) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		"INSERT INTO dns_records (zone_id, name, type, ttl, value, priority, weight, port) VALUES (?,?,?,?,?,?,?,?)",
		zoneID, name, typ, ttl, value, priority, weight, port)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteRecord removes one DNS record (used to roll back a failed push).
func (r *DnsRepository) DeleteRecord(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM dns_records WHERE id = ?", id)
	return err
}

// ZonePayload is the zone as the agent takes it, enabled records only
// (DnsService::zonePayload).
func (r *DnsRepository) ZonePayload(ctx context.Context, zoneID int64) (map[string]any, error) {
	var z DnsZoneFull
	if err := r.db.QueryRowContext(ctx,
		"SELECT id, name, ttl, primary_ns, hostmaster, secondaries FROM dns_zones WHERE id = ?",
		zoneID).Scan(&z.ID, &z.Name, &z.TTL, &z.PrimaryNS, &z.Hostmaster, &z.Secondaries); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT name, type, ttl, value, priority, weight, port, disabled
		FROM dns_records WHERE zone_id = ?
		ORDER BY CASE name WHEN '@' THEN 0 ELSE 1 END, name,
			CASE type WHEN 'NS' THEN 0 WHEN 'A' THEN 1 WHEN 'AAAA' THEN 2 WHEN 'CNAME' THEN 3 WHEN 'MX' THEN 4 ELSE 5 END, id`,
		zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []map[string]any{}
	for rows.Next() {
		var name, typ, value string
		var ttl, priority, weight, port, disabled int
		if err := rows.Scan(&name, &typ, &ttl, &value, &priority, &weight, &port, &disabled); err != nil {
			return nil, err
		}
		if disabled == 1 {
			continue
		}
		records = append(records, map[string]any{
			"name": name, "type": typ, "ttl": ttl, "value": value,
			"priority": priority, "weight": weight, "port": port,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	secondaries := []string{}
	if z.Secondaries != "" {
		secondaries = splitCSV(z.Secondaries)
	}
	return map[string]any{
		"zone": z.Name, "ttl": z.TTL,
		"soa":         map[string]any{"primary_ns": z.PrimaryNS + ".", "hostmaster": z.Hostmaster},
		"records":     records,
		"secondaries": secondaries,
	}, nil
}

// UpdateSerial records the zone's new serial and applied time after a push
// (DnsService::push, bookkeeping half).
func (r *DnsRepository) UpdateSerial(ctx context.Context, zoneID int64, serial int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		"UPDATE dns_zones SET serial = ?, applied_at = ?, updated_at = ? WHERE id = ?",
		serial, now, now, zoneID)
	return err
}

// ZoneNameTaken reports whether a zone with this name exists on the server.
func (r *DnsRepository) ZoneNameTaken(ctx context.Context, serverID int64, name string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dns_zones WHERE server_id = ? AND name = ?`, serverID, name).Scan(&n)
	return n > 0, err
}

// ZoneOwnerByName is a zone's owner id by name (nil when absent), for the
// "name belongs to a domain of another account" check (DnsService::create).
func (r *DnsRepository) ZoneOwnerByName(ctx context.Context, serverID int64, name string) (*int64, error) {
	var owner sql.NullInt64
	err := r.db.QueryRowContext(ctx, `SELECT owner_id FROM dns_zones WHERE server_id = ? AND name = ?`, serverID, name).Scan(&owner)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !owner.Valid {
		return nil, nil
	}
	id := owner.Int64
	return &id, nil
}

// InsertZone stores a new zone (DnsService::create, first write).
func (r *DnsRepository) InsertZone(ctx context.Context, serverID int64, name string, websiteID, ownerID *int64, primaryNS, hostmaster string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO dns_zones (server_id, name, website_id, owner_id, primary_ns, hostmaster, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		serverID, name, websiteID, ownerID, primaryNS, hostmaster, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func splitCSV(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

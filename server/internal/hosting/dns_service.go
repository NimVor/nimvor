package hosting

// This file ports the DnsService helpers other areas need beyond the zone
// list page (which lands with the DNS batch): zoneForName (the zone a host
// lives in) and replaceAddress (moving a website's A/AAAA records, as the
// IP assignment does), each followed by pushing the zone to the agent.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

// DnsZoneRef is a host's zone and the host's name within it
// (DnsService::zoneForName's reply).
type DnsZoneRef struct {
	ID   int64
	Name string // "@" or the subdomain labels
}

// DnsService applies DNS changes to the agent.
type DnsService struct {
	db    *sql.DB
	repo  *DnsRepository
	agent AgentCaller
}

// NewDnsService builds the service.
func NewDnsService(db *sql.DB, repo *DnsRepository, agent AgentCaller) *DnsService {
	return &DnsService{db: db, repo: repo, agent: agent}
}

// ZoneForName is the zone containing the host (the longest matching
// zone name), with the host's name inside it; nil when no zone covers it.
// Mirrors DnsService::zoneForName.
func (s *DnsService) ZoneForName(ctx context.Context, host string) (*DnsZoneRef, error) {
	host = strings.ToLower(strings.TrimRight(host, "."))
	labels := strings.Split(host, ".")
	for i := 0; i < len(labels)-1; i++ {
		zoneName := strings.Join(labels[i:], ".")
		var id int64
		err := s.db.QueryRowContext(ctx, `SELECT id FROM dns_zones WHERE name = ?`, zoneName).Scan(&id)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, err
		}
		name := "@"
		if i > 0 {
			name = strings.Join(labels[:i], ".")
		}
		return &DnsZoneRef{ID: id, Name: name}, nil
	}
	return nil, nil
}

// ReplaceAddress moves the zone's A/AAAA records from old to new, restricted
// to the given names when they are given (a website's own names in a shared
// zone); old "" adds missing records, new "" removes them. Returns the
// records changed. Mirrors DnsService::replaceAddress.
func (s *DnsService) ReplaceAddress(ctx context.Context, zoneID int64, typ, old, new string, names []string) (int, error) {
	if typ != "A" && typ != "AAAA" {
		return 0, &UserError{What: "Only A and AAAA records hold addresses."}
	}
	var zoneName string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM dns_zones WHERE id = ?`, zoneID).Scan(&zoneName); err != nil {
		if err == sql.ErrNoRows {
			return 0, &UserError{What: "The zone no longer exists."}
		}
		return 0, err
	}
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	rollback := func() error {
		_ = tx.Rollback()
		return nil
	}
	changed := 0
	if old != "" {
		rows, err := tx.QueryContext(ctx,
			`SELECT id, name FROM dns_records WHERE zone_id = ? AND type = ? AND value = ?`, zoneID, typ, old)
		if err != nil {
			return 0, rollback()
		}
		type rec struct{ id int64 }
		ids := []rec{}
		for rows.Next() {
			var id int64
			var name string
			if rows.Scan(&id, &name) != nil {
				continue
			}
			if len(names) > 0 && !nameSet[name] {
				continue
			}
			ids = append(ids, rec{id})
		}
		rows.Close()
		for _, r := range ids {
			if new == "" {
				if _, err := tx.ExecContext(ctx, `DELETE FROM dns_records WHERE id = ?`, r.id); err != nil {
					return 0, rollback()
				}
			} else {
				if _, err := tx.ExecContext(ctx, `UPDATE dns_records SET value = ? WHERE id = ?`, new, r.id); err != nil {
					return 0, rollback()
				}
			}
		}
		changed = len(ids)
	} else if new != "" {
		for _, name := range names {
			var hasA, has int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND name = ? AND type = 'A' AND disabled = 0`, zoneID, name).Scan(&hasA); err != nil {
				return 0, rollback()
			}
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND name = ? AND type = ?`, zoneID, name, typ).Scan(&has); err != nil {
				return 0, rollback()
			}
			if hasA > 0 && has == 0 {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO dns_records (zone_id, name, type, ttl, value, priority, weight, port) VALUES (?, ?, ?, 0, ?, 0, 0, 0)`,
					zoneID, name, typ, new); err != nil {
					return 0, rollback()
				}
				changed++
			}
		}
	}
	if changed > 0 {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		if err := s.push(ctx, zoneID); err != nil {
			return 0, err
		}
	}
	return changed, nil
}

// push applies the zone to the agent and stores the new serial
// (DnsService::push).
func (s *DnsService) push(ctx context.Context, zoneID int64) error {
	payload, err := s.repo.ZonePayload(ctx, zoneID)
	if err != nil {
		if err == sql.ErrNoRows {
			return &UserError{What: "The zone no longer exists."}
		}
		return err
	}
	res, err := s.agent.Call(ctx, "dns.zone_apply", payload, 60*time.Second)
	if err != nil {
		return err
	}
	var out struct {
		Serial int `json:"serial"`
	}
	serial := 0
	if err := json.Unmarshal(res, &out); err == nil {
		serial = out.Serial
	}
	return s.repo.UpdateSerial(ctx, zoneID, serial)
}

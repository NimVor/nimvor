package hosting

import "context"

// IpZoneHit is a DNS zone with the count of its A/AAAA records pointing at
// one address (DnsService::zonesUsingAddress, used by the IP migration
// wizard).
type IpZoneHit struct {
	ID      int64
	Name    string
	Records int
}

// ZonesUsingAddress is the zones with A/AAAA records on the address,
// ordered by zone name (DnsService::zonesUsingAddress).
func (s *DnsService) ZonesUsingAddress(ctx context.Context, address string) ([]IpZoneHit, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT z.id, z.name, COUNT(*) AS n FROM dns_records r JOIN dns_zones z ON z.id = r.zone_id
		 WHERE r.type IN ('A','AAAA') AND r.value = ? GROUP BY z.id, z.name ORDER BY z.name`,
		address)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IpZoneHit{}
	for rows.Next() {
		var z IpZoneHit
		if err := rows.Scan(&z.ID, &z.Name, &z.Records); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

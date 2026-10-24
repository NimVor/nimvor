package hosting

// This file ports the /ips administration area of IpService (deferred from
// the batch-55 website-side port in ip_service.go): the pool pages
// (add / update / delete / rebuild), the routed IPv6 ranges and their
// allocations, and the migration wizard (plan + run).

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
)

// IpRange is one ipv6_ranges row with its allocation counts.
type IpRange struct {
	ID        int64
	Prefix    string
	Note      string
	Allocated int
	Used      int
}

// IpPoolRow is one ip_addresses row for the /ips pages: the pool row plus
// its range and whether the server currently has the address (nil = the
// agent did not answer).
type IpPoolRow struct {
	ID       int64
	Address  string
	Family   int
	Kind     string
	Reserved int
	Reason   string
	RangeID  *int64
	Sites    []IpSite
	Present  *bool
}

// IpNetAddress is one interface address of the agent's net.addresses.
type IpNetAddress struct {
	Address   string
	Interface string
	Family    int
	PrefixLen int
	Scope     string
}

// IpServer is the agent's view of the server's addresses (net.addresses).
type IpServer struct {
	Addresses []IpNetAddress
	Gateways  []string
}

// IpMigrationPlan is what moving an address changes (IpService::migrationPlan).
type IpMigrationPlan struct {
	Source    string
	Dest      string
	Family    int
	Sites     []IpSite
	Zones     []IpZoneHit
	AllZones  bool
	DefaultV4 bool
}

// IpMigrationResult is one step of a migration run.
type IpMigrationResult struct {
	Item    string
	OK      bool
	Message string
}

// ServerAddresses is the agent's view of the server's addresses
// (IpService::serverAddresses, 30 s).
func (s *IpService) ServerAddresses(ctx context.Context) (*IpServer, error) {
	res, err := s.agent.Call(ctx, "net.addresses", nil, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var out IpServer
	var raw struct {
		Addresses []struct {
			Address   string `json:"address"`
			Interface string `json:"interface"`
			Family    int    `json:"family"`
			PrefixLen int    `json:"prefix_len"`
			Scope     string `json:"scope"`
		} `json:"addresses"`
		Gateways []string `json:"gateways"`
	}
	if err := json.Unmarshal(res, &raw); err != nil {
		return nil, err
	}
	for _, a := range raw.Addresses {
		out.Addresses = append(out.Addresses, IpNetAddress{
			Address: a.Address, Interface: a.Interface, Family: a.Family,
			PrefixLen: a.PrefixLen, Scope: a.Scope,
		})
	}
	out.Gateways = raw.Gateways
	return &out, nil
}

// OnServer is the usable global addresses on the server's interfaces,
// de-duplicated (IpService::onServer).
func (s *IpService) OnServer(ctx context.Context) ([]string, error) {
	srv, err := s.ServerAddresses(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, a := range srv.Addresses {
		ip := Canonical(a.Address)
		if ip == "" || !Usable(ip) || a.Scope == "link" {
			continue
		}
		if !seen[ip] {
			seen[ip] = true
			out = append(out, ip)
		}
	}
	return out, nil
}

// PoolFull is the pool with each address's websites, its range and whether
// the server currently has it (IpService::pool — the /ips view; the
// website card's slimmer Pool is in ip_service.go).
func (s *IpService) PoolFull(ctx context.Context) ([]IpPoolRow, error) {
	ranges, err := s.Ranges(ctx)
	if err != nil {
		return nil, err
	}
	serverKnown := true
	have := map[string]bool{}
	if addrs, err := s.OnServer(ctx); err != nil {
		serverKnown = false
	} else {
		for _, a := range addrs {
			have[a] = true
		}
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, address, family, kind, reserved, reason, range_id FROM ip_addresses WHERE server_id = ? ORDER BY family, address`,
		s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IpPoolRow{}
	for rows.Next() {
		var r IpPoolRow
		var rangeID sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Address, &r.Family, &r.Kind, &r.Reserved, &r.Reason, &rangeID); err != nil {
			return nil, err
		}
		if rangeID.Valid {
			r.RangeID = &rangeID.Int64
		}
		r.Sites = []IpSite{}
		if s, err := s.sitesUsing(ctx, r.Address); err != nil {
			return nil, err
		} else {
			r.Sites = s
		}
		inRange := false
		for _, g := range ranges {
			inRange = inRange || InPrefix(r.Address, g.Prefix)
		}
		if serverKnown {
			p := have[r.Address] || inRange
			r.Present = &p
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FindByID is the pool row for an id (nil when it is not pooled).
func (s *IpService) FindByID(ctx context.Context, id int64) (*IpPoolRow, error) {
	var r IpPoolRow
	err := s.db.QueryRowContext(ctx,
		`SELECT id, address, family, kind, reserved, reason FROM ip_addresses WHERE id = ? AND server_id = ?`,
		id, s.serverID).Scan(&r.ID, &r.Address, &r.Family, &r.Kind, &r.Reserved, &r.Reason)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// available reports whether the server can listen on the address (an
// interface address or a routed range; IpService::available).
func (s *IpService) available(ctx context.Context, ip string) (bool, error) {
	addrs, err := s.OnServer(ctx)
	if err != nil {
		return false, err
	}
	for _, a := range addrs {
		if a == ip {
			return true, nil
		}
	}
	if FamilyOf(ip) == 6 {
		ranges, err := s.Ranges(ctx)
		if err != nil {
			return false, err
		}
		for _, g := range ranges {
			if InPrefix(ip, g.Prefix) {
				return true, nil
			}
		}
	}
	return false, nil
}

// truncateRunes is mb_substr(trim($s), 0, $n) as the pool stores reasons
// and notes.
func truncateRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		return string(r[:n])
	}
	return string(r)
}

// Add pools an address of the server (IpService::add).
func (s *IpService) Add(ctx context.Context, address, kind, reason string) error {
	ip := Canonical(address)
	if ip == "" || !Usable(ip) {
		return &ValidationError{Fields: map[string]string{"address": "Enter a public or private IPv4 or IPv6 address of this server (not loopback, link-local or multicast)."}}
	}
	if kind != "shared" && kind != "dedicated" {
		return &ValidationError{Fields: map[string]string{"kind": "Choose shared or dedicated."}}
	}
	row, err := s.FindAddress(ctx, ip)
	if err != nil {
		return err
	}
	if row != nil {
		return &ValidationError{Fields: map[string]string{"address": ip + " is already in the pool."}}
	}
	ok, err := s.available(ctx, ip)
	if err != nil {
		return err
	}
	if !ok {
		return &ValidationError{Fields: map[string]string{"address": ip + " is not configured on this server. Add it to a network interface (your provider's panel or the system's network configuration) first, or add its IPv6 range."}}
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ip_addresses (server_id, address, family, kind, reserved, reason, created_at) VALUES (?, ?, ?, ?, 0, ?, ?)`,
		s.serverID, ip, FamilyOf(ip), kind, truncateRunes(reason, 300), time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	s.recordAudit(ctx, "ip.add", "server", "ip", "success", map[string]any{"address": ip, "kind": kind})
	return nil
}

// UpdatePoolRow saves a pool row's assignment (IpService::update).
func (s *IpService) UpdatePoolRow(ctx context.Context, id int64, kind string, reserved bool, reason string) error {
	row, err := s.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return &UserError{What: "That address is not in the pool."}
	}
	if kind != "shared" && kind != "dedicated" {
		return &ValidationError{Fields: map[string]string{"kind": "Choose shared or dedicated."}}
	}
	sites, err := s.sitesUsing(ctx, row.Address)
	if err != nil {
		return err
	}
	domains := []string{}
	for _, x := range sites {
		domains = append(domains, x.Domain)
	}
	if reserved && len(sites) > 0 {
		return &UserError{What: row.Address + " is used by " + strings.Join(domains, ", ") + "; move those websites first."}
	}
	if kind == "dedicated" && len(sites) > 1 {
		return &UserError{What: row.Address + " is used by " + strconv.Itoa(len(sites)) + " websites; a dedicated address belongs to one."}
	}
	if reserved && strings.TrimSpace(reason) == "" {
		return &ValidationError{Fields: map[string]string{"reason": "Say why the address is reserved."}}
	}
	b := 0
	if reserved {
		b = 1
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE ip_addresses SET kind = ?, reserved = ?, reason = ? WHERE id = ?`,
		kind, b, truncateRunes(reason, 300), id); err != nil {
		return err
	}
	s.recordAudit(ctx, "ip.update", "server", "ip", "success", map[string]any{
		"address": row.Address, "kind": kind, "reserved": reserved, "reason": reason,
	})
	return nil
}

// DeletePoolRow removes an unused pool row (IpService::delete).
func (s *IpService) DeletePoolRow(ctx context.Context, id int64) error {
	row, err := s.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return &UserError{What: "That address is not in the pool."}
	}
	sites, err := s.sitesUsing(ctx, row.Address)
	if err != nil {
		return err
	}
	if len(sites) > 0 {
		domains := []string{}
		for _, x := range sites {
			domains = append(domains, x.Domain)
		}
		return &UserError{What: row.Address + " is used by " + strings.Join(domains, ", ") + "; it cannot be removed."}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM ip_addresses WHERE id = ?`, id); err != nil {
		return err
	}
	s.recordAudit(ctx, "ip.delete", "server", "ip", "success", map[string]any{"address": row.Address})
	return nil
}

// Ranges is the routed IPv6 ranges with allocation counts (IpService::ranges).
func (s *IpService) Ranges(ctx context.Context) ([]IpRange, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, prefix, note FROM ipv6_ranges WHERE server_id = ? ORDER BY prefix`, s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IpRange{}
	for rows.Next() {
		var g IpRange
		if err := rows.Scan(&g.ID, &g.Prefix, &g.Note); err != nil {
			return nil, err
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ip_addresses WHERE range_id = ?`, g.ID).Scan(&g.Allocated); err != nil {
			return nil, err
		}
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM ip_addresses a WHERE a.range_id = ? AND EXISTS (SELECT 1 FROM websites w WHERE w.ipv6 = a.address)`, g.ID).Scan(&g.Used); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// pushRanges tells the agent which routed IPv6 ranges are local
// (IpService::pushRanges, 60 s).
func (s *IpService) pushRanges(ctx context.Context, prefixes []string) error {
	if prefixes == nil {
		prefixes = []string{}
	}
	_, err := s.agent.Call(ctx, "net.ipv6_ranges", map[string]any{"ranges": prefixes}, 60*time.Second)
	return err
}

// AddRange routes a new IPv6 range and stores it (IpService::addRange).
func (s *IpService) AddRange(ctx context.Context, prefix, note string) error {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	net, lenStr, hasSlash := strings.Cut(prefix, "/")
	if !hasSlash {
		return &ValidationError{Fields: map[string]string{"prefix": "Enter an IPv6 range such as 2001:db8:100::/64."}}
	}
	canon := Canonical(net)
	if canon == "" || FamilyOf(canon) != 6 || !ipDigits(lenStr) {
		return &ValidationError{Fields: map[string]string{"prefix": "Enter an IPv6 range such as 2001:db8:100::/64."}}
	}
	if n, err := strconv.Atoi(lenStr); err == nil {
		lenStr = strconv.Itoa(n)
	}
	prefix = canon + "/" + lenStr
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ipv6_ranges WHERE server_id = ? AND prefix = ?`, s.serverID, prefix).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return &ValidationError{Fields: map[string]string{"prefix": "That range is already added."}}
	}
	all := []string{}
	ranges, err := s.Ranges(ctx)
	if err != nil {
		return err
	}
	for _, g := range ranges {
		all = append(all, g.Prefix)
	}
	all = append(all, prefix)
	// The agent checks the range (routed, not on-link) and adds the route.
	if err := s.pushRanges(ctx, all); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ipv6_ranges (server_id, prefix, note, created_at) VALUES (?, ?, ?, ?)`,
		s.serverID, prefix, truncateRunes(note, 300), time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	s.recordAudit(ctx, "ip.range_add", "server", "ip", "success", map[string]any{"prefix": prefix})
	return nil
}

// ipDigits is ctype_digit ("" is false).
func ipDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// UpdateRange saves a range's note (IpService::updateRange — a wrong id is
// a no-op, as in PHP).
func (s *IpService) UpdateRange(ctx context.Context, id int64, note string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ipv6_ranges SET note = ? WHERE id = ? AND server_id = ?`,
		truncateRunes(note, 300), id, s.serverID)
	return err
}

// RemoveRange deletes a range and its free allocations (IpService::removeRange).
func (s *IpService) RemoveRange(ctx context.Context, id int64) error {
	var prefix string
	err := s.db.QueryRowContext(ctx, `SELECT prefix FROM ipv6_ranges WHERE id = ? AND server_id = ?`, id, s.serverID).Scan(&prefix)
	if err != nil {
		if err == sql.ErrNoRows {
			return &UserError{What: "That range does not exist."}
		}
		return err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT w.domain FROM ip_addresses a JOIN websites w ON w.ipv6 = a.address WHERE a.range_id = ?`, id)
	if err != nil {
		return err
	}
	domains := []string{}
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			domains = append(domains, d)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(domains) > 0 {
		return &UserError{What: "Addresses of this range are used by " + strings.Join(domains, ", ") + "; move those websites first."}
	}
	rest := []string{}
	ranges, err := s.Ranges(ctx)
	if err != nil {
		return err
	}
	for _, g := range ranges {
		if g.Prefix != prefix {
			rest = append(rest, g.Prefix)
		}
	}
	if err := s.pushRanges(ctx, rest); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ip_addresses WHERE range_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ipv6_ranges WHERE id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.recordAudit(ctx, "ip.range_remove", "server", "ip", "success", map[string]any{"prefix": prefix})
	return nil
}

// Allocate takes an address of a range into the pool — the next free one
// from the range's ::10 up, or the one given (IpService::allocate).
func (s *IpService) Allocate(ctx context.Context, rangeID int64, address, kind string) (string, error) {
	var prefix string
	err := s.db.QueryRowContext(ctx, `SELECT prefix FROM ipv6_ranges WHERE id = ? AND server_id = ?`, rangeID, s.serverID).Scan(&prefix)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", &UserError{What: "That range does not exist."}
		}
		return "", err
	}
	var ip string
	if address != "" {
		ip = Canonical(address)
		if ip == "" || FamilyOf(ip) != 6 || !InPrefix(ip, prefix) {
			return "", &ValidationError{Fields: map[string]string{"address": "Enter an address inside " + prefix + "."}}
		}
		row, err := s.FindAddress(ctx, ip)
		if err != nil {
			return "", err
		}
		if row != nil {
			return "", &ValidationError{Fields: map[string]string{"address": ip + " is already in the pool."}}
		}
	} else {
		if ip, err = s.nextFreeIn(ctx, prefix); err != nil {
			return "", err
		}
	}
	if kind != "shared" {
		kind = "dedicated"
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ip_addresses (server_id, address, family, kind, reserved, reason, range_id, created_at) VALUES (?, ?, 6, ?, 0, '', ?, ?)`,
		s.serverID, ip, kind, rangeID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return "", err
	}
	s.recordAudit(ctx, "ip.allocate", "server", "ip", "success", map[string]any{"address": ip, "prefix": prefix})
	return ip, nil
}

// nextFreeIn scans the range from its ::10 up (the first addresses are
// often the router's), 100000 tries, as PHP's allocate does.
func (s *IpService) nextFreeIn(ctx context.Context, prefix string) (string, error) {
	net, _, _ := strings.Cut(prefix, "/")
	a, err := netip.ParseAddr(net)
	if err != nil {
		return "", &UserError{What: "No free address was found in " + prefix + "."}
	}
	b := a.As16()
	base := b[:12]
	n32 := binary.BigEndian.Uint32(b[12:16])
	for n := 0x10; n < 0x10+100000; n++ {
		cand := make([]byte, 0, 16)
		cand = append(cand, base...)
		cand = binary.BigEndian.AppendUint32(cand, n32+uint32(n))
		ip, ok := netip.AddrFromSlice(cand)
		if !ok || !InPrefix(ip.String(), prefix) {
			break
		}
		if row, err := s.FindAddress(ctx, ip.String()); err != nil {
			return "", err
		} else if row == nil {
			return ip.String(), nil
		}
	}
	return "", &UserError{What: "No free address was found in " + prefix + "."}
}

// MigrationPlan is what moving from one address to another changes
// (IpService::migrationPlan).
func (s *IpService) MigrationPlan(ctx context.Context, source, dest string, allZones bool) (*IpMigrationPlan, error) {
	scanon := Canonical(source)
	dcanon := Canonical(dest)
	var row *IPAddress
	var err error
	if dcanon != "" {
		if row, err = s.FindAddress(ctx, dcanon); err != nil {
			return nil, err
		}
	}
	errs := map[string]string{}
	if scanon == "" {
		errs["source"] = "Choose the address to move from (from the pool)."
	} else if srow, err := s.FindAddress(ctx, scanon); err != nil {
		return nil, err
	} else if srow == nil {
		errs["source"] = "Choose the address to move from (from the pool)."
	}
	switch {
	case row == nil:
		errs["dest"] = "Choose the address to move to (from the pool)."
	case row.Reserved == 1:
		errs["dest"] = dcanon + " is reserved."
	case scanon != "" && FamilyOf(scanon) != FamilyOf(dcanon):
		errs["dest"] = "Both addresses must be IPv4, or both IPv6."
	case scanon != "" && scanon == dcanon:
		errs["dest"] = "Choose a different address."
	}
	if len(errs) > 0 {
		return nil, &ValidationError{Fields: errs}
	}
	sites, err := s.sitesUsing(ctx, scanon)
	if err != nil {
		return nil, err
	}
	if row.Kind == "dedicated" {
		sites2, err := s.sitesUsing(ctx, dcanon)
		if err != nil {
			return nil, err
		}
		if len(sites)+len(sites2) > 1 {
			return nil, &ValidationError{Fields: map[string]string{"dest": dcanon + " is dedicated; it can take only one website."}}
		}
	}
	zones := []IpZoneHit{}
	if allZones {
		if zones, err = s.dns.ZonesUsingAddress(ctx, scanon); err != nil {
			return nil, err
		}
	}
	defV4 := false
	if allZones {
		def, _ := s.cfg.String(ctx, "dns_default_ipv4", "")
		defV4 = strings.TrimSpace(def) == scanon
	}
	return &IpMigrationPlan{
		Source: scanon, Dest: dcanon, Family: FamilyOf(scanon),
		Sites: sites, Zones: zones, AllZones: allZones, DefaultV4: defV4,
	}, nil
}

// Migrate moves every website (and, with allZones, every DNS record) from
// one address to another (IpService::migrate); each website moves on its
// own, so one failure does not stop the others.
func (s *IpService) Migrate(ctx context.Context, source, dest string, allZones bool) ([]IpMigrationResult, error) {
	plan, err := s.MigrationPlan(ctx, source, dest, allZones)
	if err != nil {
		return nil, err
	}
	results := []IpMigrationResult{}
	isV4 := plan.Family == 4
	for _, sit := range plan.Sites {
		site, err := s.sites.FindFull(ctx, s.serverID, sit.ID, nil)
		if err != nil {
			return nil, err
		}
		if site == nil {
			continue
		}
		ipv4, ipv6 := site.IPv4.String, site.IPv6.String
		if isV4 {
			ipv4 = plan.Dest
		} else {
			ipv6 = plan.Dest
		}
		n, err := s.Assign(ctx, site, ipv4, ipv6, true)
		if err != nil {
			results = append(results, IpMigrationResult{Item: site.Domain, OK: false, Message: ipErrMessage(err)})
			continue
		}
		results = append(results, IpMigrationResult{Item: site.Domain, OK: true, Message: fmt.Sprintf("Moved; %d DNS record(s) updated.", n)})
	}
	if plan.AllZones {
		typ := "A"
		if !isV4 {
			typ = "AAAA"
		}
		zones, err := s.dns.ZonesUsingAddress(ctx, plan.Source)
		if err != nil {
			return nil, err
		}
		for _, z := range zones {
			n, err := s.dns.ReplaceAddress(ctx, z.ID, typ, plan.Source, plan.Dest, nil)
			if err != nil {
				results = append(results, IpMigrationResult{Item: "DNS zone " + z.Name, OK: false, Message: ipErrMessage(err)})
				continue
			}
			results = append(results, IpMigrationResult{Item: "DNS zone " + z.Name, OK: true, Message: fmt.Sprintf("%d record(s) updated.", n)})
		}
		if plan.DefaultV4 {
			if err := s.cfg.Set(ctx, "dns_default_ipv4", plan.Dest, 0); err != nil {
				return nil, err
			}
			results = append(results, IpMigrationResult{Item: "Default IPv4 for new zones", OK: true, Message: "Now " + plan.Dest + "."})
		}
	}
	failed := false
	for _, r := range results {
		failed = failed || !r.OK
	}
	result := "success"
	if failed {
		result = "failure"
	}
	s.recordAudit(ctx, "ip.migrate", "server", "ip", result, map[string]any{
		"source": plan.Source, "dest": plan.Dest, "results": results,
	})
	return results, nil
}

// ipErrMessage is the PHP getMessage() of a failed migration step.
func ipErrMessage(err error) string {
	var ue *UserError
	var ae *agent.Error
	switch {
	case errors.As(err, &ue):
		return ue.What
	case errors.As(err, &ae) && ae.Message != "":
		return ae.Message
	default:
		return "The step failed."
	}
}

// Rebuild reconciles the pool with the server (IpService::rebuild): it
// re-applies the ranges, adds the server's addresses and the websites'
// missing ones, and reports what the server no longer has (nothing is
// removed).
func (s *IpService) Rebuild(ctx context.Context) (added, missing []string, err error) {
	ranges, err := s.Ranges(ctx)
	if err != nil {
		return nil, nil, err
	}
	if len(ranges) > 0 {
		prefixes := make([]string, 0, len(ranges))
		for _, g := range ranges {
			prefixes = append(prefixes, g.Prefix)
		}
		if err := s.pushRanges(ctx, prefixes); err != nil {
			return nil, nil, err
		}
	}
	want, err := s.OnServer(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT ipv4, ipv6 FROM websites WHERE server_id = ?`, s.serverID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var v4, v6 sql.NullString
		if err := rows.Scan(&v4, &v6); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if v4.Valid {
			want = append(want, v4.String)
		}
		if v6.Valid {
			want = append(want, v6.String)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	added = []string{}
	for _, ip := range want {
		if seen[ip] {
			continue
		}
		seen[ip] = true
		if row, err := s.FindAddress(ctx, ip); err != nil {
			return nil, nil, err
		} else if row != nil {
			continue
		}
		var rangeID *int64
		for _, g := range ranges {
			if FamilyOf(ip) == 6 && InPrefix(ip, g.Prefix) {
				id := g.ID
				rangeID = &id
			}
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO ip_addresses (server_id, address, family, kind, reserved, reason, range_id, created_at) VALUES (?, ?, ?, 'shared', 0, '', ?, ?)`,
			s.serverID, ip, FamilyOf(ip), rangeID, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return nil, nil, err
		}
		added = append(added, ip)
	}
	pool, err := s.PoolFull(ctx)
	if err != nil {
		return nil, nil, err
	}
	missing = []string{}
	for _, r := range pool {
		if r.Present != nil && !*r.Present {
			missing = append(missing, r.Address)
		}
	}
	s.recordAudit(ctx, "ip.rebuild", "server", "ip", "success", map[string]any{"added": added, "missing": missing})
	return added, missing, nil
}

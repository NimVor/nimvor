package hosting

// This file ports the parts of IpService the website's advanced page uses:
// the address pool (which the page's IP card offers) and assign() — binding
// a website to pool addresses, with the DNS records followed (and the
// website rolled back when that fails). The rest of the IP area (the /ips
// pool pages, IPv6 ranges, the migration wizard, rebuild) lands with that
// batch; pool() there additionally reports whether each address is on the
// server, which the card does not show.

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// IpSite is a website bound to a pool address (the pool's sitesUsing).
type IpSite struct {
	ID     int64
	Domain string
}

// IPAddress is one ip_addresses row with the websites bound to it.
type IPAddress struct {
	ID       int64
	Address  string
	Family   int
	Kind     string // shared | dedicated
	Reserved int
	Reason   string
	Sites    []IpSite
}

// IpService binds websites to IP addresses from the pool.
type IpService struct {
	db       *sql.DB
	websites *WebsiteService
	sites    *WebsiteRepository
	dns      *DnsService
	cfg      *settings.Repository
	audit    Auditor
	serverID int64
}

// NewIpService builds the service.
func NewIpService(db *sql.DB, websites *WebsiteService, sites *WebsiteRepository,
	dns *DnsService, cfg *settings.Repository, audit Auditor, serverID int64) *IpService {
	return &IpService{db: db, websites: websites, sites: sites, dns: dns,
		cfg: cfg, audit: audit, serverID: serverID}
}

// recordAudit is the nil-safe audit hook.
func (s *IpService) recordAudit(ctx context.Context, action, targetType, target, result string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, targetType, target, result, details)
	}
}

// Canonical is the address's canonical form ("" when it is not an address).
// Mirrors IpService::canonical.
func Canonical(ip string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ""
	}
	return a.String()
}

// FamilyOf is 4 or 6 (a string without a colon is family 4, as PHP's
// str_contains check does). Mirrors IpService::family.
func FamilyOf(ip string) int {
	if strings.Contains(ip, ":") {
		return 6
	}
	return 4
}

// Usable reports whether the address may be bound to websites: not loopback,
// unspecified, link-local or multicast. Mirrors IpService::usable.
func Usable(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	if !a.IsGlobalUnicast() {
		return false
	}
	if a.Is4() {
		p := a.As4()
		// 224.0.0.0/4 (multicast) + 240.0.0.0/4 (reserved).
		if (p[0] == 22 && p[1] >= 4) || p[0] == 23 {
			return false
		}
	}
	return !a.IsLinkLocalUnicast()
}

// InPrefix reports whether the address lies in the CIDR prefix. Mirrors
// IpService::inPrefix.
func InPrefix(ip, prefix string) bool {
	p, err := netip.ParsePrefix(prefix)
	if err != nil {
		return false
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	return p.Contains(a)
}

// sitesUsing is the websites bound to an address (IpService::sitesUsing).
func (s *IpService) sitesUsing(ctx context.Context, ip string) ([]IpSite, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT w.id, w.domain FROM websites w WHERE w.server_id = ? AND (w.ipv4 = ? OR w.ipv6 = ?) ORDER BY w.domain`,
		s.serverID, ip, ip)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IpSite{}
	for rows.Next() {
		var t IpSite
		if rows.Scan(&t.ID, &t.Domain) == nil {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// Pool is the address pool with each address's websites (IpService::pool,
// without the on-server check that the /ips pages show).
func (s *IpService) Pool(ctx context.Context) ([]IPAddress, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, address, family, kind, reserved, reason FROM ip_addresses WHERE server_id = ? ORDER BY family, address`,
		s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IPAddress{}
	for rows.Next() {
		var r IPAddress
		if err := rows.Scan(&r.ID, &r.Address, &r.Family, &r.Kind, &r.Reserved, &r.Reason); err != nil {
			return nil, err
		}
		r.Sites = []IpSite{}
		if s, err := s.sitesUsing(ctx, r.Address); err != nil {
			return nil, err
		} else {
			r.Sites = s
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PoolForSite is the pool the advanced page offers for one website
// (SitesController::advanced's ipPool): nothing reserved, and a dedicated
// address only when no other website has it. nil when the user is not a
// server administrator (the page hides the card).
func (s *IpService) PoolForSite(ctx context.Context, siteID int64, admin bool) ([]IPAddress, error) {
	if !admin {
		return nil, nil
	}
	all, err := s.Pool(ctx)
	if err != nil {
		return nil, err
	}
	out := []IPAddress{}
	for _, a := range all {
		if a.Reserved == 1 {
			continue
		}
		if a.Kind == "dedicated" && len(a.Sites) > 0 && a.Sites[0].ID != siteID {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// FindAddress is the pool row for an address (nil when it is not pooled).
// Mirrors IpService::findAddress.
func (s *IpService) FindAddress(ctx context.Context, ip string) (*IPAddress, error) {
	var r IPAddress
	err := s.db.QueryRowContext(ctx,
		`SELECT id, address, family, kind, reserved, reason FROM ip_addresses WHERE server_id = ? AND address = ?`,
		s.serverID, ip).Scan(&r.ID, &r.Address, &r.Family, &r.Kind, &r.Reserved, &r.Reason)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// assignable validates one requested address for a website and returns its
// canonical form, or "" for "every address" (IpService::assignable).
func (s *IpService) assignable(ctx context.Context, ip string, family int, siteID int64) (string, error) {
	if ip == "" {
		return "", nil
	}
	fam := strconv.Itoa(family)
	field := "ipv" + fam
	canon := Canonical(ip)
	if canon == "" || FamilyOf(canon) != family {
		return "", &ValidationError{Fields: map[string]string{field: "Choose an IPv" + fam + " address from the pool."}}
	}
	row, err := s.FindAddress(ctx, canon)
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", &ValidationError{Fields: map[string]string{field: canon + " is not in the IP pool."}}
	}
	if row.Reserved == 1 {
		msg := canon + " is reserved"
		if row.Reason != "" {
			msg += " (" + row.Reason + ")"
		}
		return "", &ValidationError{Fields: map[string]string{field: msg + "."}}
	}
	if row.Kind == "dedicated" {
		others, err := s.sitesUsing(ctx, canon)
		if err != nil {
			return "", err
		}
		names := []string{}
		for _, o := range others {
			if o.ID != siteID {
				names = append(names, o.Domain)
			}
		}
		if len(names) > 0 {
			return "", &ValidationError{Fields: map[string]string{field: canon + " is dedicated to " + strings.Join(names, ", ") + "."}}
		}
	}
	return canon, nil
}

// Assign binds the website to the requested addresses and, asked for, keeps
// the website's A/AAAA records in its DNS zone; a DNS failure moves the
// website back (IpService::assign). Returns the number of DNS records
// changed.
func (s *IpService) Assign(ctx context.Context, site *Website, ipv4, ipv6 string, dns bool) (int, error) {
	v4, err := s.assignable(ctx, ipv4, 4, site.ID)
	if err != nil {
		return 0, err
	}
	v6, err := s.assignable(ctx, ipv6, 6, site.ID)
	if err != nil {
		return 0, err
	}
	old4, old6 := site.IPv4.String, site.IPv6.String
	if old4 == v4 && old6 == v6 {
		return 0, nil
	}
	if err := s.websites.patch(ctx, site, map[string]any{"ipv4": nullOr(v4), "ipv6": nullOr(v6)}, "website.ip"); err != nil {
		return 0, err
	}
	changed := 0
	if dns {
		n, err := s.assignDNS(ctx, site, old4, old6, v4, v6)
		if err != nil {
			fresh, ferr := s.sites.FindFull(ctx, site.ServerID, site.ID, nil)
			if ferr != nil {
				fresh = site
			}
			_ = s.websites.patch(ctx, fresh, map[string]any{"ipv4": nullOr(old4), "ipv6": nullOr(old6)}, "website.ip_rollback")
			ue := &UserError{What: "The DNS records could not be updated, so the website was moved back to its previous address."}
			var ae *agent.Error
			if errors.As(err, &ae) && ae.Message != "" {
				ue.Why = ae.Message
			}
			return 0, ue
		}
		changed = n
	}
	s.recordAudit(ctx, "ip.assign", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain": site.Domain, "ipv4": []string{old4, v4}, "ipv6": []string{old6, v6}, "dns_records": changed,
	})
	return changed, nil
}

// assignDNS follows the website's A/AAAA records in its DNS zone (the dns
// half of IpService::assign).
func (s *IpService) assignDNS(ctx context.Context, site *Website, old4, old6, v4, v6 string) (int, error) {
	ref, err := s.dns.ZoneForName(ctx, site.Domain)
	if err != nil {
		return 0, err
	}
	if ref == nil {
		return 0, nil
	}
	names, err := s.namesIn(ctx, site, ref.ID)
	if err != nil {
		return 0, err
	}
	def, _ := s.cfg.String(ctx, "dns_default_ipv4", "")
	def = strings.TrimSpace(def)
	eff4Old, eff4New := old4, v4
	if eff4Old == "" {
		eff4Old = def
	}
	if eff4New == "" {
		eff4New = def
	}
	changed := 0
	if eff4Old != "" && eff4New != "" && eff4Old != eff4New {
		n, err := s.dns.ReplaceAddress(ctx, ref.ID, "A", eff4Old, eff4New, names)
		if err != nil {
			return 0, err
		}
		changed += n
	}
	if old6 != "" && old6 != v6 {
		n, err := s.dns.ReplaceAddress(ctx, ref.ID, "AAAA", old6, v6, names)
		if err != nil {
			return 0, err
		}
		changed += n
	} else if old6 == "" && v6 != "" {
		n, err := s.dns.ReplaceAddress(ctx, ref.ID, "AAAA", "", v6, names)
		if err != nil {
			return 0, err
		}
		changed += n
	}
	return changed, nil
}

// namesIn is the website's own names in its zone (IpService::namesIn).
func (s *IpService) namesIn(ctx context.Context, site *Website, zoneID int64) ([]string, error) {
	out := []string{}
	hosts := append([]string{site.Domain}, site.Aliases...)
	for _, host := range hosts {
		ref, err := s.dns.ZoneForName(ctx, host)
		if err != nil {
			return nil, err
		}
		if ref != nil && ref.ID == zoneID {
			out = append(out, ref.Name)
		}
	}
	return out, nil
}

// nullOr adapts a string to a nullable column value ("" -> NULL, as PHP's
// null-coalesced patch fields store).
func nullOr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

package hosting

// This file ports the DnsService helpers other areas need beyond the zone
// list page (which lands with the DNS batch): zoneForName (the zone a host
// lives in) and replaceAddress (moving a website's A/AAAA records, as the
// IP assignment does), each followed by pushing the zone to the agent.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// DnsZoneRef is a host's zone and the host's name within it
// (DnsService::zoneForName's reply).
type DnsZoneRef struct {
	ID   int64
	Name string // "@" or the subdomain labels
}

// DnsService applies DNS changes to the agent and manages zones for the
// panel (DnsService).
type DnsService struct {
	db       *sql.DB
	repo     *DnsRepository
	agent    AgentCaller
	settings *settings.Repository
	sites    *WebsiteRepository
	auditlog func(action, targetType, target string, details map[string]any)
}

// NewDnsService builds the service.
func NewDnsService(db *sql.DB, repo *DnsRepository, agent AgentCaller, settingsRepo *settings.Repository, sites *WebsiteRepository,
	auditlog func(action, targetType, target string, details map[string]any)) *DnsService {
	return &DnsService{db: db, repo: repo, agent: agent, settings: settingsRepo, sites: sites, auditlog: auditlog}
}

// dnsDomainRe is a fully qualified domain name (DnsService::create / the
// nameservers setting share it).
var dnsDomainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// Nameservers is the configured name server list (DnsService::nameservers):
// the setting split on whitespace/commas, lowercased, trailing dots and
// blank entries dropped, valid domains only, de-duplicated.
func (s *DnsService) Nameservers(ctx context.Context) ([]string, error) {
	raw, err := s.settings.String(ctx, "dns_nameservers", "")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, ns := range strings.FieldsFunc(raw, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' }) {
		ns = strings.ToLower(strings.TrimRight(strings.TrimSpace(ns), "."))
		if ns == "" || !dnsDomainRe.MatchString(ns) || seen[ns] {
			continue
		}
		seen[ns] = true
		out = append(out, ns)
	}
	return out, nil
}

// DnsStatus is the agent's dns.status data (what the zone pages show).
type DnsStatus struct {
	Installed  bool            `json:"installed"`
	Configured bool            `json:"configured"`
	Running    bool            `json:"running"`
	Zones      []DnsServedZone `json:"zones"`
}

// DnsServedZone is one zone the server serves, with the serial it serves.
type DnsServedZone struct {
	Zone   string `json:"zone"`
	Serial int    `json:"serial"`
}

// Status asks the agent for the DNS server state (nil when the agent is
// unreachable, as in the PHP).
func (s *DnsService) Status(ctx context.Context) (*DnsStatus, error) {
	res, err := s.agent.Call(ctx, "dns.status", nil)
	if err != nil {
		return nil, err
	}
	var st DnsStatus
	if err := json.Unmarshal(res, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// ServedSerial is the serial the server serves for a zone name (nil when it
// is not served or the agent is unreachable) — the zone page's Live /
// update-pending state (DnsController::show).
func (st *DnsStatus) ServedSerial(zone string) *int {
	for i := range st.Zones {
		if st.Zones[i].Zone == zone {
			se := st.Zones[i].Serial
			return &se
		}
	}
	return nil
}

// dnsEmailRe is a mail-address shape (the zone's hostmaster check).
var dnsEmailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// Create hosts a zone (optionally with the usual records) and loads it
// (DnsService::create). The agent call happens inside the transaction on the
// single serialised connection: the payload is built from the in-memory rows,
// so no pool connection is needed while the write is open.
func (s *DnsService) Create(ctx context.Context, serverID int64, input map[string]string, u *identity.User) (int64, error) {
	name := strings.ToLower(strings.TrimRight(strings.TrimSpace(input["name"]), "."))
	var site *Website
	if v := input["website_id"]; v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			found, err := s.sites.Find(ctx, serverID, id, u)
			if err != nil && !errors.Is(err, ErrWebsiteNotFound) {
				return 0, err
			}
			if found == nil {
				return 0, &ValidationError{Fields: map[string]string{"website_id": "Choose one of your websites."}}
			}
			site = found
		}
	}
	if name == "" && site != nil {
		name = strings.ToLower(site.Domain)
	}
	if !dnsDomainRe.MatchString(name) || len(name) > 253 {
		return 0, &ValidationError{Fields: map[string]string{"name": "Enter a domain name such as example.com."}}
	}
	// A zone for a website's domain belongs to that website (and so to its
	// owner), whether or not the form named the website.
	if site == nil && IsAll(u) {
		if found, err := s.sites.FindByDomain(ctx, serverID, name); err == nil {
			site = found
		}
	}
	if !IsAll(u) {
		// Customers host zones for their own websites only.
		if site == nil || strings.ToLower(site.Domain) != name {
			return 0, &ValidationError{Fields: map[string]string{"website_id": "Choose the website this zone belongs to; the zone must be its domain."}}
		}
	}
	if taken, err := s.repo.ZoneNameTaken(ctx, serverID, name); err != nil {
		return 0, err
	} else if taken {
		return 0, &ValidationError{Fields: map[string]string{"name": "This zone exists already."}}
	}
	if !IsAll(u) {
		// This server answers for its zones: a zone under another account's
		// zone (x.their-domain.test) would take names from it.
		owner := u.ID
		if site != nil && site.OwnerID.Valid {
			owner = site.OwnerID.Int64
		}
		parts := strings.Split(name, ".")
		for i := 1; i < len(parts)-1; i++ {
			above, err := s.repo.ZoneOwnerByName(ctx, serverID, strings.Join(parts[i:], "."))
			if err != nil {
				return 0, err
			}
			if above != nil && *above != owner {
				return 0, &ValidationError{Fields: map[string]string{"name": "This name belongs to a domain of another account."}}
			}
		}
	}
	ns, err := s.Nameservers(ctx)
	if err != nil {
		return 0, err
	}
	if len(ns) == 0 {
		return 0, &UserError{
			What: "No name servers are configured.",
			Fix:  "An administrator sets them under Settings \u2192 DNS name servers.",
		}
	}
	hostmaster, _ := s.settings.String(ctx, "dns_hostmaster", "")
	if !dnsEmailRe.MatchString(hostmaster) {
		hostmaster = "hostmaster@" + name
	}
	var websiteIDPtr, ownerIDPtr *int64
	if site != nil {
		websiteIDPtr = &site.ID
		if site.OwnerID.Valid {
			o := site.OwnerID.Int64
			ownerIDPtr = &o
		}
	} else if !IsAll(u) {
		id := u.ID
		ownerIDPtr = &id
	}

	// The usual records when the template is checked (DnsService::create).
	records := make([]map[string]any, 0, 5)
	addRec := func(name, typ, value string) {
		records = append(records, map[string]any{"name": name, "type": typ, "ttl": 0, "value": value, "priority": 0, "weight": 0, "port": 0})
	}
	for _, n := range ns {
		addRec("@", "NS", n+".")
	}
	ip, _ := s.settings.String(ctx, "dns_default_ipv4", "")
	hasIP := ip != "" && net.ParseIP(ip) != nil && strings.Count(ip, ".") == 3
	if input["template"] == "1" {
		if hasIP {
			addRec("@", "A", ip)
			addRec("www", "CNAME", "@")
		}
		if input["spf"] == "" || input["spf"] == "1" {
			sp := "v=spf1 +a +mx"
			if hasIP {
				sp += " +ip4:" + ip
			}
			addRec("@", "TXT", sp+" ~all")
		}
		if input["dmarc"] == "1" {
			addRec("_dmarc", "TXT", "v=DMARC1; p=none;")
		}
		addRec("@", "CAA", "issue letsencrypt.org")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx,
		`INSERT INTO dns_zones (server_id, name, website_id, owner_id, primary_ns, hostmaster, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		serverID, name, websiteIDPtr, ownerIDPtr, ns[0], hostmaster, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, rec := range records {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO dns_records (zone_id, name, type, ttl, value, priority, weight, port, disabled)
			 VALUES (?, ?, ?, 0, ?, 0, 0, 0, 0)`,
			id, rec["name"], rec["type"], rec["value"]); err != nil {
			return 0, err
		}
	}
	// Load the zone on the server; the agent keeps it when it accepts it
	// (the payload mirrors DnsRepository.ZonePayload's shape).
	payload := map[string]any{
		"zone":        name,
		"ttl":         3600,
		"soa":         map[string]any{"primary_ns": ns[0] + ".", "hostmaster": hostmaster},
		"records":     records,
		"secondaries": []string{},
	}
	if out, err := s.agent.Call(ctx, "dns.zone_apply", payload, 60*time.Second); err != nil {
		return 0, err
	} else {
		var applied struct {
			Serial int `json:"serial"`
		}
		_ = json.Unmarshal(out, &applied)
		if _, err := tx.ExecContext(ctx,
			"UPDATE dns_zones SET serial = ?, applied_at = ?, updated_at = ? WHERE id = ?",
			applied.Serial, now, now, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if s.auditlog != nil {
		websiteIDStr := ""
		if websiteIDPtr != nil {
			websiteIDStr = fmt.Sprint(*websiteIDPtr)
		}
		s.auditlog("dns.zone_create", "dns_zone", fmt.Sprint(id), map[string]any{"zone": name, "website_id": websiteIDStr})
	}
	return id, nil
}

// Warnings checks a zone's records for the usual problems (DnsService::
// warnings): too few NS at @, MX/NS/CNAME pointing at CNAMEs or at names
// with no record in the zone, private A targets, sub-5-minute TTLs, several
// SPF records, MX without SPF and an open (+all) SPF.
func Warnings(zone string, records []DnsRecord) []string {
	on := make([]DnsRecord, 0, len(records))
	for _, r := range records {
		if !r.Disabled {
			on = append(on, r)
		}
	}
	by := func(name, typ string) []DnsRecord {
		var out []DnsRecord
		for _, r := range on {
			if r.Name == name && r.Type == typ {
				out = append(out, r)
			}
		}
		return out
	}
	// rel is a target's name inside the zone ("@" for the zone itself, the
	// relative name for sub-names, nil for an absolute name outside it).
	rel := func(host string) *string {
		h := strings.ToLower(strings.TrimRight(host, "."))
		if !strings.HasSuffix(host, ".") {
			if host == "@" {
				at := "@"
				return &at
			}
			return &h
		}
		if h == zone {
			at := "@"
			return &at
		}
		if strings.HasSuffix(h, "."+zone) {
			relName := h[:len(h)-len(zone)-1]
			return &relName
		}
		return nil
	}
	seen := map[string]bool{}
	var w []string
	add := func(m string) {
		if !seen[m] {
			seen[m] = true
			w = append(w, m)
		}
	}
	if len(by("@", "NS")) < 2 {
		add("The zone has fewer than two NS records at @; most registries require at least two name servers.")
	}
	for _, r := range on {
		name := r.Name + "." + zone
		if r.Name == "@" {
			name = zone
		}
		t := rel(r.Value)
		if (r.Type == "MX" || r.Type == "NS") && t != nil && len(by(*t, "CNAME")) > 0 {
			add(r.Type + " " + name + " points to " + r.Value + ", which is a CNAME; " + r.Type + " targets must have A/AAAA records.")
		}
		if (r.Type == "MX" || r.Type == "NS" || r.Type == "CNAME") && t != nil && *t != "@" &&
			!strings.Contains(*t, "*") && len(by(*t, "A")) == 0 && len(by(*t, "AAAA")) == 0 && len(by(*t, "CNAME")) == 0 &&
			!strings.HasSuffix(r.Value, ".") {
			add(r.Type + " " + name + " points to " + r.Value + "." + zone + ", which has no record in this zone. For a host elsewhere, end the name with a dot.")
		}
		if r.Type == "A" {
			ip := net.ParseIP(r.Value)
			ok := ip != nil && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
				!ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified()
			if !ok {
				add("A " + name + " is a private or reserved address (" + r.Value + "); visitors on the internet cannot reach it.")
			}
		}
		if r.TTL > 0 && r.TTL < 300 {
			add(r.Type + " " + name + " has a TTL under 5 minutes, which increases DNS traffic; keep short TTLs for planned changes only.")
		}
	}
	var spf []DnsRecord
	for _, r := range by("@", "TXT") {
		if strings.HasPrefix(strings.ToLower(r.Value), "v=spf1") {
			spf = append(spf, r)
		}
	}
	if len(spf) > 1 {
		add("There are " + fmt.Sprint(len(spf)) + " SPF records at @; receivers treat several as an error. Merge them into one.")
	}
	if len(by("@", "MX")) > 0 && len(spf) == 0 {
		add("The domain receives mail (MX) but has no SPF record; mail from it is more likely to be marked as spam.")
	}
	for _, r := range spf {
		if strings.Contains(strings.ToLower(r.Value), "+all") {
			add("The SPF record ends in +all, which lets anyone send mail as this domain.")
		}
	}
	return w
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

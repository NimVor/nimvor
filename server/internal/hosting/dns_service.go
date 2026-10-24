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

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
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
	auditlog Auditor
}

// NewDnsService builds the service.
func NewDnsService(db *sql.DB, repo *DnsRepository, agent AgentCaller, settingsRepo *settings.Repository, sites *WebsiteRepository,
	auditlog Auditor) *DnsService {
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
		s.auditlog(ctx, "dns.zone_create", "dns_zone", fmt.Sprint(id), "success", map[string]any{"zone": name, "website_id": websiteIDStr})
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

// pushTx is push inside an open transaction: the payload is read from the
// transaction (the single pooled connection is held by the write) and the
// serial is stored there too, so a refused zone rolls the write back with it.
func (s *DnsService) pushTx(ctx context.Context, tx *sql.Tx, zoneID int64) error {
	var z DnsZoneFull
	if err := tx.QueryRowContext(ctx,
		"SELECT id, name, ttl, primary_ns, hostmaster, secondaries FROM dns_zones WHERE id = ?", zoneID).
		Scan(&z.ID, &z.Name, &z.TTL, &z.PrimaryNS, &z.Hostmaster, &z.Secondaries); err != nil {
		if err == sql.ErrNoRows {
			return &UserError{What: "The zone no longer exists."}
		}
		return err
	}
	rows, err := tx.QueryContext(ctx, `\
		SELECT name, type, ttl, value, priority, weight, port, disabled
		FROM dns_records WHERE zone_id = ?
		ORDER BY CASE name WHEN '@' THEN 0 ELSE 1 END, name,
			CASE type WHEN 'NS' THEN 0 WHEN 'A' THEN 1 WHEN 'AAAA' THEN 2 WHEN 'CNAME' THEN 3 WHEN 'MX' THEN 4 ELSE 5 END, id`, zoneID)
	if err != nil {
		return err
	}
	records := []map[string]any{}
	for rows.Next() {
		var name, typ, value string
		var ttl, priority, weight, port, disabled int
		if rows.Scan(&name, &typ, &ttl, &value, &priority, &weight, &port, &disabled) != nil {
			continue
		}
		if disabled == 1 {
			continue
		}
		records = append(records, map[string]any{
			"name": name, "type": typ, "ttl": ttl, "value": value,
			"priority": priority, "weight": weight, "port": port,
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	secondaries := []string{}
	if z.Secondaries != "" {
		secondaries = splitCSV(z.Secondaries)
	}
	payload := map[string]any{
		"zone":        z.Name,
		"ttl":         z.TTL,
		"soa":         map[string]any{"primary_ns": z.PrimaryNS + ".", "hostmaster": z.Hostmaster},
		"records":     records,
		"secondaries": secondaries,
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
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = tx.ExecContext(ctx,
		"UPDATE dns_zones SET serial = ?, applied_at = ?, updated_at = ? WHERE id = ?",
		serial, now, now, zoneID)
	return err
}

// ---- the zone editor's write side (the zone batch) ------------------------

// DnsRecordTypes are the record types the zone editor accepts
// (DnsService::TYPES).
var DnsRecordTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA", "PTR"}

var (
	recNameRe   = regexp.MustCompile(`^(\*\.?)([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?\.)*[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)
	recHostRe   = regexp.MustCompile(`^(@|\.|([a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?\.)*[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9])?\.?)$`)
	srvNameRe   = regexp.MustCompile(`^_[a-z0-9-]{1,63}\._(tcp|udp|tls|sctp)(\.|$)`)
	caaIodefRe  = regexp.MustCompile(`^(mailto:[^@\s]+@[^@\s]+|https?://\S+)$`)
	caaDomainRe = regexp.MustCompile(`^(;|[a-z0-9.-]+\.[a-z]{2,}(\s*;.*)?)$`)
	recNumRe    = regexp.MustCompile(`^\d{1,6}$`)
	recCtlRe    = regexp.MustCompile(`[\x00-\x1F]`)
)

// CleanRecord is a validated record (DnsService::cleanRecord's reply).
type CleanRecord struct {
	Name, Type, Value string
	TTL               int
	Priority          int
	Weight            int
	Port              int
}

// isHost is a BIND host name (DnsService's HOST_RE).
func isHost(h string) bool { return h != "" && h != "." && recHostRe.MatchString(h) }

// CleanRecord validates a record from the form: each type has its own fields
// and checks (IPv4, IPv6, host names, MX priority, TXT, CAA, SRV, TTL) and
// normalizes the name relative to the zone (DnsService::cleanRecord).
func (s *DnsService) CleanRecord(in map[string]string, zone string) (*CleanRecord, error) {
	str := func(k string) string { return strings.TrimSpace(in[k]) }
	// num is the typed number or invalid; empty means the default.
	num := func(k string) (int, bool) {
		v := str(k)
		if v == "" {
			return 0, true
		}
		if !recNumRe.MatchString(v) {
			return 0, false
		}
		n, _ := strconv.Atoi(v)
		return n, true
	}
	zoneRe := regexp.MustCompile(`\.` + regexp.QuoteMeta(zone) + `\.?$`)
	recType := strings.ToUpper(str("type"))
	name := strings.ToLower(str("name"))
	// SRV: _service._protocol[.name].
	if recType == "SRV" && (str("srv_service") != "" || str("srv_protocol") != "") {
		svc := strings.TrimLeft(strings.ToLower(str("srv_service")), "_")
		proto := strings.TrimLeft(strings.ToLower(str("srv_protocol")), "_")
		if name != "" && name != "@" {
			name = "_" + svc + "._" + proto + "." + name
		} else {
			name = "_" + svc + "._" + proto
		}
	}
	if name == "" || name == zone || name == zone+"." {
		name = "@"
	} else {
		name = zoneRe.ReplaceAllString(name, "")
	}
	errs := map[string]string{}
	isType := false
	for _, t := range DnsRecordTypes {
		if t == recType {
			isType = true
		}
	}
	if !isType {
		errs["type"] = "Choose a record recType."
	}
	if name != "@" && name != "*" && !recNameRe.MatchString(name) {
		errs["name"] = "Use @ for the domain itself, or a name such as www, mail or *.dev."
	}
	ttl, ok := num("ttl")
	if !ok || (ttl != 0 && (ttl < 60 || ttl > 604800)) {
		errs["ttl"] = "Use 60–604800 seconds, or 0 for the zone default."
		ttl = 0
	}
	value := str("value")
	priority, prioOK := num("priority")
	weight, weightOK := num("weight")
	port, portOK := num("port")
	host := func(h string) bool {
		return len(h) <= 253 && recHostRe.MatchString(strings.ToLower(h))
	}
	switch recType {
	case "A":
		if ip := net.ParseIP(value); ip == nil || ip.To4() == nil {
			errs["value"] = "Enter an IPv4 address such as 192.0.2.10."
		}
	case "AAAA":
		if ip := net.ParseIP(value); ip == nil || ip.To4() != nil {
			errs["value"] = "Enter an IPv6 address such as 2001:db8::10."
		}
	case "CNAME", "NS", "PTR":
		value = strings.ToLower(value)
		if value == "" || value == "." || !host(value) || net.ParseIP(strings.TrimRight(value, ".")) != nil {
			hint := "www.example.com."
			if recType == "NS" {
				hint = "ns1.example.com."
			}
			errs["value"] = "Enter a host name (not an address), e.g. " + hint + " A name ending in a dot is absolute."
		}
		if recType == "PTR" && !strings.HasSuffix(zone, ".arpa") {
			errs["type"] = "PTR records belong in reverse zones (in-addr.arpa or ip6.arpa)."
		}
	case "MX":
		value = strings.ToLower(value)
		if value == "" || !host(value) || net.ParseIP(strings.TrimRight(value, ".")) != nil {
			errs["value"] = "Enter the mail server's host name, not an IP address."
		}
		if !prioOK || priority > 65535 {
			errs["priority"] = "Priority: 0–65535 (lower is preferred)."
		}
	case "SRV":
		value = strings.ToLower(value)
		if value == "" || !host(value) {
			errs["value"] = "Enter the target host name (or . for “no service”)."
		}
		if !prioOK || priority > 65535 {
			errs["priority"] = "Priority: 0–65535."
		}
		if !weightOK || weight > 65535 {
			errs["weight"] = "Weight: 0–65535."
		}
		if !portOK || port > 65535 {
			errs["port"] = "Port: 0–65535."
		}
		if !srvNameRe.MatchString(name) {
			errs["name"] = "An SRV name is _service._protocol, e.g. _sip._tcp (optionally followed by a name)."
		}
	case "TXT":
		// Pasted with surrounding quotes: keep the text only.
		if len(value) >= 2 && value[0] == '"' && strings.HasSuffix(value, `"`) && strings.Count(value, `"`) == 2 {
			value = value[1 : len(value)-1]
		}
		if value == "" || len(value) > 4000 || recCtlRe.MatchString(value) {
			errs["value"] = "Enter the text on one line (up to 4000 characters)."
		}
	case "CAA":
		tag := strings.ToLower(str("caa_tag"))
		var cv string
		if str("caa_tag") != "" {
			cv = str("caa_value")
		} else if sp := strings.Index(value, " "); sp > 0 {
			tag, cv = strings.ToLower(value[:sp]), strings.TrimSpace(value[sp+1:])
		} else {
			tag = strings.ToLower(value)
		}
		cv = strings.Trim(cv, `"`)
		var flags int
		flagsOK := true
		if str("caa_flags") != "" {
			f, ok := num("caa_flags")
			flags, flagsOK = f, ok
		} else {
			flags = priority
		}
		switch tag {
		case "issue", "issuewild", "iodef":
		default:
			errs["caa_tag"] = "Choose issue, issuewild or iodef."
		}
		switch {
		case tag == "iodef":
			if !caaIodefRe.MatchString(cv) {
				errs["caa_value"] = "Enter mailto:address or an https:// URL."
			}
		case tag == "issue" || tag == "issuewild":
			if !caaDomainRe.MatchString(strings.ToLower(cv)) {
				errs["caa_value"] = "Enter the certificate authority's domain, e.g. letsencrypt.org (or ; to allow none)."
			}
		}
		if !flagsOK || (flags != 0 && flags != 128) {
			errs["caa_flags"] = "Flags: 0, or 128 (critical)."
		}
		value = tag + " " + cv
		priority = flags
	}
	if len(errs) > 0 {
		return nil, &ValidationError{Fields: errs}
	}
	return &CleanRecord{
		Name:     name,
		Type:     recType,
		TTL:      ttl,
		Value:    value,
		Priority: priorityOrZero(recType, priority, prioOK),
		Weight:   srvInt(recType, weight, weightOK),
		Port:     srvInt(recType, port, portOK),
	}, nil
}

func priorityOrZero(typ string, n int, ok bool) int {
	if ok && (typ == "MX" || typ == "SRV" || typ == "CAA") {
		return n
	}
	return 0
}

func srvInt(typ string, n int, ok bool) int {
	if ok && typ == "SRV" {
		return n
	}
	return 0
}

// Conflicts reports the zone's other (enabled) records this record clashes
// with, or "" (DnsService::conflicts).
func Conflicts(r CleanRecord, others []DnsRecord) string {
	for _, o := range others {
		if o.Disabled || o.Name != r.Name {
			continue
		}
		if r.Type == "CNAME" || o.Type == "CNAME" {
			other := "other records"
			if o.Type == "CNAME" {
				other = "a CNAME record"
			}
			return r.Name + " already has " + other + "; a CNAME must be the only record of its name."
		}
		if o.Type == r.Type && strings.ToLower(o.Value) == strings.ToLower(r.Value) && o.Priority == r.Priority {
			return "The same record already exists."
		}
	}
	if r.Type == "CNAME" && r.Name == "@" {
		return "The domain itself (@) cannot be a CNAME; use an A/AAAA record."
	}
	return ""
}

// recordKey is the import's identity of a record.
func recordKey(name, typ, value string, priority int) string {
	return strings.ToLower(name + "|" + typ + "|" + value + "|" + strconv.Itoa(priority))
}

// SaveRecord adds (recordID nil), changes or deletes ($delete) a record and
// reloads the zone; nothing changes if the agent refuses the zone
// (DnsService::saveRecord).
func (s *DnsService) SaveRecord(ctx context.Context, zone *DnsZoneFull, recordID *int64, in map[string]string, delete bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var before *DnsRecord
	if recordID != nil {
		var b DnsRecord
		var disabled int
		err := tx.QueryRowContext(ctx,
			`SELECT id, name, type, ttl, value, priority, weight, port, disabled FROM dns_records WHERE id = ? AND zone_id = ?`,
			*recordID, zone.ID).
			Scan(&b.ID, &b.Name, &b.Type, &b.TTL, &b.Value, &b.Priority, &b.Weight, &b.Port, &disabled)
		if err == sql.ErrNoRows {
			return &UserError{What: "The record no longer exists."}
		}
		if err != nil {
			return err
		}
		b.Disabled = disabled == 1
		before = &b
	}
	var after *CleanRecord
	if delete {
		if _, err := tx.ExecContext(ctx, "DELETE FROM dns_records WHERE id = ?", *recordID); err != nil {
			return err
		}
	} else {
		after, err = s.CleanRecord(in, zone.Name)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT id, name, type, ttl, value, priority, weight, port, disabled FROM dns_records WHERE zone_id = ?`+
				(idNotClause(recordID)), *recordIDOr(zone.ID, recordID)...)
		if err != nil {
			return err
		}
		var others []DnsRecord
		for rows.Next() {
			var o DnsRecord
			var disabled int
			if rows.Scan(&o.ID, &o.Name, &o.Type, &o.TTL, &o.Value, &o.Priority, &o.Weight, &o.Port, &disabled) != nil {
				continue
			}
			o.Disabled = disabled == 1
			others = append(others, o)
		}
		rows.Close()
		if c := Conflicts(*after, others); c != "" {
			return &ValidationError{Fields: map[string]string{"name": c}}
		}
		if recordID == nil {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM dns_records WHERE zone_id = ?", zone.ID).Scan(&n); err != nil {
				return err
			}
			if n >= 2000 {
				return &UserError{What: "A zone can hold at most 2000 records."}
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO dns_records (zone_id, name, type, ttl, value, priority, weight, port, disabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
				zone.ID, after.Name, after.Type, after.TTL, after.Value, after.Priority, after.Weight, after.Port); err != nil {
				return err
			}
		} else if _, err := tx.ExecContext(ctx,
			`UPDATE dns_records SET name = ?, type = ?, ttl = ?, value = ?, priority = ?, weight = ?, port = ? WHERE id = ?`,
			after.Name, after.Type, after.TTL, after.Value, after.Priority, after.Weight, after.Port, *recordID); err != nil {
			return err
		}
	}
	if err := s.pushTx(ctx, tx, zone.ID); err != nil {
		return err
	}
	action := "dns.record_delete"
	switch {
	case !delete && recordID == nil:
		action = "dns.record_create"
	case !delete:
		action = "dns.record_update"
	}
	details := map[string]any{"zone": zone.Name}
	s.auditRecord(ctx, action, zone.ID, details, before, after)
	return tx.Commit()
}

func idNotClause(recordID *int64) string {
	if recordID == nil {
		return ""
	}
	return " AND id != ?"
}

// recordArgs returns (zoneID) plus the record id when restricted.
func recordIDOr(zoneID int64, recordID *int64) []any {
	args := []any{zoneID}
	if recordID != nil {
		args = append(args, *recordID)
	}
	return args
}

// auditRecord adds the before/after names to a record-op audit detail.
func (s *DnsService) auditRecord(ctx context.Context, action string, zoneID int64, details map[string]any, before *DnsRecord, after *CleanRecord) {
	if s.auditlog == nil {
		return
	}
	if before != nil {
		details["before"] = map[string]any{"name": before.Name, "type": before.Type, "value": before.Value}
	} else {
		details["before"] = nil
	}
	if after != nil {
		details["after"] = map[string]any{"name": after.Name, "type": after.Type, "value": after.Value}
	} else {
		details["after"] = nil
	}
	s.auditlog(ctx, action, "dns_zone", fmt.Sprint(zoneID), "success", details)
}

// Settings saves the zone's default TTL and the secondary servers allowed to
// transfer it (DnsService::settings).
func (s *DnsService) Settings(ctx context.Context, zone *DnsZoneFull, ttl int, secondaries string) error {
	ips := []string{}
	for _, ip := range strings.FieldsFunc(secondaries, func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == ',' }) {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		if net.ParseIP(ip) == nil {
			return &ValidationError{Fields: map[string]string{"secondaries": "Enter IP addresses separated by commas."}}
		}
		ips = append(ips, ip)
	}
	if ttl < 60 || ttl > 604800 {
		return &ValidationError{Fields: map[string]string{"ttl": "Use 60–604800 seconds."}}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"UPDATE dns_zones SET ttl = ?, secondaries = ?, updated_at = ? WHERE id = ?",
		ttl, strings.Join(ips, ","), time.Now().UTC().Format(time.RFC3339), zone.ID); err != nil {
		return err
	}
	if err := s.pushTx(ctx, tx, zone.ID); err != nil {
		return err
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "dns.zone_settings", "dns_zone", fmt.Sprint(zone.ID), "success", map[string]any{"zone": zone.Name, "ttl": ttl, "secondaries": ips})
	}
	return tx.Commit()
}

// Sync loads the zone again, e.g. after the DNS server was reinstalled
// (DnsService::sync).
func (s *DnsService) Sync(ctx context.Context, zone *DnsZoneFull) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.pushTx(ctx, tx, zone.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// Bulk enables, disables or deletes several records at once in one zone
// reload (DnsService::bulk).
func (s *DnsService) Bulk(ctx context.Context, zone *DnsZoneFull, ids []int64, op string) (int, error) {
	if op != "enable" && op != "disable" && op != "delete" {
		return 0, &UserError{What: "Unknown operation."}
	}
	seen := map[int64]bool{}
	clean := []int64{}
	for _, id := range ids {
		if id > 0 && !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return 0, &UserError{What: "Select records first."}
	}
	place := strings.TrimSuffix(strings.Repeat("?,", len(clean)), ",")
	args := append([]any{zone.ID}, toAny(clean)...)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND id IN ("+place+")", args...).Scan(&n); err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, &UserError{What: "The selected records are not in this zone."}
	}
	switch op {
	case "delete":
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM dns_records WHERE zone_id = ? AND id IN ("+place+")", args...); err != nil {
			return 0, err
		}
	default:
		disabled := 0
		if op == "disable" {
			disabled = 1
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE dns_records SET disabled = ? WHERE zone_id = ? AND id IN ("+place+")",
			append([]any{disabled}, args...)...); err != nil {
			return 0, err
		}
	}
	if err := s.pushTx(ctx, tx, zone.ID); err != nil {
		return 0, err
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "dns.records_"+op, "dns_zone", fmt.Sprint(zone.ID), "success", map[string]any{"zone": zone.Name, "count": n})
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

func toAny(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// ImportSkipped is one line the import left out, with why (the agent's
// zone_parse skipped shape).
type ImportSkipped struct {
	Line   int    `json:"line"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// ImportResult is the import's outcome (DnsService::import's reply).
type ImportResult struct {
	Added      int
	Duplicates int
	Skipped    []ImportSkipped
}

// Import adds a zone file's records that are not in the zone yet (SOA and
// unsupported lines are skipped and reported) (DnsService::import).
func (s *DnsService) Import(ctx context.Context, zone *DnsZoneFull, text string) (*ImportResult, error) {
	if strings.TrimSpace(text) == "" || len(text) > 1<<20 {
		return nil, &ValidationError{Fields: map[string]string{"zonefile": "Paste or upload a zone file of up to 1 MB."}}
	}
	res, err := s.agent.Call(ctx, "dns.zone_parse", map[string]any{"zone": zone.Name, "text": text}, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Records []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			TTL      int    `json:"ttl"`
			Value    string `json:"value"`
			Priority int    `json:"priority"`
			Weight   int    `json:"weight"`
			Port     int    `json:"port"`
		} `json:"records"`
		Skipped []ImportSkipped `json:"skipped"`
	}
	if err := json.Unmarshal(res, &parsed); err != nil {
		return nil, err
	}
	out := &ImportResult{Skipped: parsed.Skipped}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var existing []DnsRecord
	rows, err := tx.QueryContext(ctx,
		`SELECT id, name, type, ttl, value, priority, weight, port, disabled FROM dns_records WHERE zone_id = ?`, zone.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var o DnsRecord
		var disabled int
		if rows.Scan(&o.ID, &o.Name, &o.Type, &o.TTL, &o.Value, &o.Priority, &o.Weight, &o.Port, &disabled) != nil {
			continue
		}
		o.Disabled = disabled == 1
		existing = append(existing, o)
	}
	rows.Close()
	have := map[string]bool{}
	for _, r := range existing {
		have[recordKey(r.Name, r.Type, r.Value, r.Priority)] = true
	}
	for _, r := range parsed.Records {
		rec := CleanRecord{Name: r.Name, Type: r.Type, TTL: r.TTL, Value: r.Value, Priority: r.Priority, Weight: r.Weight, Port: r.Port}
		if have[recordKey(rec.Name, rec.Type, rec.Value, rec.Priority)] || (rec.Type == "NS" && rec.Name == "@") {
			out.Duplicates++ // the zone's own name servers stay
			continue
		}
		// What the zone could not hold (a CNAME next to other records) is listed, not added.
		if why := Conflicts(rec, existing); why != "" {
			out.Skipped = append(out.Skipped, ImportSkipped{Line: 0, Text: rec.Name + " " + rec.Type + " " + rec.Value, Reason: why})
			continue
		}
		if len(existing) >= 2000 {
			return nil, &UserError{What: "A zone can hold at most 2000 records."}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO dns_records (zone_id, name, type, ttl, value, priority, weight, port, disabled) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
			zone.ID, rec.Name, rec.Type, rec.TTL, rec.Value, rec.Priority, rec.Weight, rec.Port); err != nil {
			return nil, err
		}
		existing = append(existing, DnsRecord{
			Name: rec.Name, Type: rec.Type, TTL: rec.TTL, Value: rec.Value,
			Priority: rec.Priority, Weight: rec.Weight, Port: rec.Port,
		})
		have[recordKey(rec.Name, rec.Type, rec.Value, rec.Priority)] = true
		out.Added++
	}
	if out.Added > 0 {
		if err := s.pushTx(ctx, tx, zone.ID); err != nil {
			return nil, err
		}
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "dns.zone_import", "dns_zone", fmt.Sprint(zone.ID), "success", map[string]any{"zone": zone.Name, "added": out.Added})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// Export is the zone file as served, BIND format (DnsService::export).
func (s *DnsService) Export(ctx context.Context, zoneID int64) (string, error) {
	payload, err := s.repo.ZonePayload(ctx, zoneID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", &UserError{What: "The zone no longer exists."}
		}
		return "", err
	}
	var serial int
	if err := s.db.QueryRowContext(ctx, "SELECT serial FROM dns_zones WHERE id = ?", zoneID).Scan(&serial); err != nil {
		if err == sql.ErrNoRows {
			return "", &UserError{What: "The zone no longer exists."}
		}
		return "", err
	}
	payload["serial"] = serial
	res, err := s.agent.Call(ctx, "dns.zone_render", payload, 60*time.Second)
	if err != nil {
		return "", err
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	return out.Text, nil
}

// Delete removes the zone: the agent stops serving it (a zone it does not
// know is fine), the row goes, the records with it (DnsService::delete).
func (s *DnsService) Delete(ctx context.Context, zone *DnsZoneFull, confirm string) error {
	if strings.ToLower(strings.TrimSpace(confirm)) != zone.Name {
		return &ValidationError{Fields: map[string]string{"confirm": "Type the zone name to confirm."}}
	}
	if _, err := s.agent.Call(ctx, "dns.zone_delete", map[string]any{"zone": zone.Name, "confirm": zone.Name}); err != nil {
		var ae *agent.Error
		if !errors.As(err, &ae) || ae.Status != 404 {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM dns_zones WHERE id = ?", zone.ID); err != nil {
		return err
	}
	if s.auditlog != nil {
		s.auditlog(ctx, "dns.zone_delete", "dns_zone", fmt.Sprint(zone.ID), "success", map[string]any{"zone": zone.Name})
	}
	return nil
}

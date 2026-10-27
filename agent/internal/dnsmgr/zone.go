// Package dnsmgr hosts authoritative DNS zones with BIND.
//
// The panel keeps the records; for each change the agent renders the whole
// zone file with a higher serial, validates it with named-checkzone and
// the server configuration with named-checkconf, loads it with rndc and
// verifies that BIND now serves the new serial, restoring the previous
// files otherwise. WhaCP's zones and their list live in their own folder;
// BIND's configuration gets a single include line, added only after the
// administrator confirms.
package dnsmgr

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Record is one resource record. Name is relative to the zone ("@" for
// the apex, "www", "*.dev"). Targets (CNAME, MX, NS, SRV) are host names:
// absolute with a trailing dot, otherwise relative to the zone.
type Record struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	TTL      int    `json:"ttl"`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
	Weight   int    `json:"weight,omitempty"`
	Port     int    `json:"port,omitempty"`
}

// SOA holds the zone's start-of-authority values.
type SOA struct {
	PrimaryNS  string `json:"primary_ns"`
	Hostmaster string `json:"hostmaster"` // an email address
	Refresh    int    `json:"refresh"`
	Retry      int    `json:"retry"`
	Expire     int    `json:"expire"`
	Minimum    int    `json:"minimum"`
}

// Zone is a complete zone as the panel sends it.
type Zone struct {
	Name        string   `json:"zone"`
	TTL         int      `json:"ttl"`
	SOA         SOA      `json:"soa"`
	Records     []Record `json:"records"`
	Secondaries []string `json:"secondaries"`
}

var (
	labelRe    = regexp.MustCompile(`^(\*|[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?)$`)
	hostRe     = regexp.MustCompile(`^([A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\.)*[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9])?\.?$`)
	emailRe    = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9.-]{1,190}$`)
	caaTags    = map[string]bool{"issue": true, "issuewild": true, "iodef": true}
	maxRecords = 2000
)

// Types lists the supported record types.
var Types = []string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA", "PTR"}

func validName(n string) bool {
	if n == "@" {
		return true
	}
	if len(n) > 200 {
		return false
	}
	for i, l := range strings.Split(n, ".") {
		if l == "*" && i != 0 {
			return false
		}
		if !labelRe.MatchString(l) {
			return false
		}
	}
	return true
}

func validHost(h string) bool { return h == "@" || (len(h) <= 253 && hostRe.MatchString(h)) }

// Validate checks the zone and normalises it (upper-case types, defaults).
func (z *Zone) Validate() error {
	z.Name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(z.Name), "."))
	if err := validate.Domain("zone", z.Name); err != nil {
		return err
	}
	if z.TTL == 0 {
		z.TTL = 3600
	}
	if z.TTL < 60 || z.TTL > 604800 {
		return fmt.Errorf("ttl must be 60–604800 seconds")
	}
	s := &z.SOA
	for _, v := range []*int{&s.Refresh, &s.Retry, &s.Expire, &s.Minimum} {
		if *v < 0 || *v > 2419200 {
			return fmt.Errorf("SOA timers must be 0–2419200 seconds")
		}
	}
	if s.Refresh == 0 {
		s.Refresh = 3600
	}
	if s.Retry == 0 {
		s.Retry = 900
	}
	if s.Expire == 0 {
		s.Expire = 1209600
	}
	if s.Minimum == 0 {
		s.Minimum = 300
	}
	if !validHost(s.PrimaryNS) || s.PrimaryNS == "@" {
		return fmt.Errorf("soa.primary_ns must be a host name")
	}
	if !emailRe.MatchString(s.Hostmaster) {
		return fmt.Errorf("soa.hostmaster must be an email address")
	}
	for _, ip := range z.Secondaries {
		if net.ParseIP(ip) == nil {
			return fmt.Errorf("secondaries must be IP addresses")
		}
	}
	if len(z.Records) > maxRecords {
		return fmt.Errorf("at most %d records", maxRecords)
	}
	names := map[string][]string{} // name → types
	apexNS := 0
	for i := range z.Records {
		r := &z.Records[i]
		r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
		r.Name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.Name), "."+z.Name))
		if r.Name == "" || r.Name == z.Name {
			r.Name = "@"
		}
		r.Value = strings.TrimSpace(r.Value)
		if err := r.validate(); err != nil {
			return fmt.Errorf("record %d (%s %s): %v", i+1, r.Name, r.Type, err)
		}
		if r.Type == "PTR" && !strings.HasSuffix(z.Name, ".arpa") {
			return fmt.Errorf("record %d (%s PTR): PTR records belong in reverse zones (in-addr.arpa, ip6.arpa)", i+1, r.Name)
		}
		names[r.Name] = append(names[r.Name], r.Type)
		if r.Name == "@" && r.Type == "NS" {
			apexNS++
		}
	}
	for n, types := range names {
		for _, t := range types {
			if t == "CNAME" && len(types) > 1 {
				return fmt.Errorf("%s has a CNAME and other records; a CNAME must be the only record of its name", n)
			}
		}
	}
	if names["@"] != nil {
		for _, t := range names["@"] {
			if t == "CNAME" {
				return fmt.Errorf("the zone apex (@) cannot be a CNAME")
			}
		}
	}
	if apexNS == 0 {
		return fmt.Errorf("the zone needs at least one NS record at @")
	}
	return nil
}

func (r *Record) validate() error {
	if !validName(r.Name) {
		return fmt.Errorf("name must be @ or labels of letters, digits, - and _ (a leading * is allowed)")
	}
	if r.TTL != 0 && (r.TTL < 60 || r.TTL > 604800) {
		return fmt.Errorf("ttl must be 60–604800 seconds")
	}
	v := r.Value
	switch r.Type {
	case "A":
		if ip := net.ParseIP(v); ip == nil || ip.To4() == nil {
			return fmt.Errorf("value must be an IPv4 address")
		}
	case "AAAA":
		if ip := net.ParseIP(v); ip == nil || ip.To4() != nil {
			return fmt.Errorf("value must be an IPv6 address")
		}
	case "CNAME", "NS", "PTR":
		if !validHost(v) {
			return fmt.Errorf("value must be a host name")
		}
	case "MX":
		if !validHost(v) || r.Priority < 0 || r.Priority > 65535 {
			return fmt.Errorf("value must be a host name and priority 0–65535")
		}
	case "SRV":
		if !(validHost(v) || v == ".") || r.Priority < 0 || r.Priority > 65535 || r.Weight < 0 || r.Weight > 65535 || r.Port < 0 || r.Port > 65535 {
			return fmt.Errorf("value must be a host name and priority, weight and port 0–65535")
		}
	case "TXT":
		if v == "" || len(v) > 4000 || strings.ContainsAny(v, "\x00\r\n") {
			return fmt.Errorf("text must be 1–4000 characters on one line")
		}
	case "CAA":
		// value: <tag> <value>, e.g. issue letsencrypt.org; priority = flags.
		tag, val, ok := strings.Cut(v, " ")
		val = strings.Trim(strings.TrimSpace(val), `"`)
		if !ok || !caaTags[tag] || val == "" || len(val) > 255 || strings.ContainsAny(val, "\"\\\x00\r\n") || (r.Priority != 0 && r.Priority != 128) {
			return fmt.Errorf("value must be issue|issuewild|iodef and a value, flags 0 or 128")
		}
		r.Value = tag + " " + val
	default:
		return fmt.Errorf("type must be one of %s", strings.Join(Types, ", "))
	}
	return nil
}

// quoteTXT splits text into quoted strings of at most 255 bytes.
func quoteTXT(s string) string {
	var parts []string
	for len(s) > 0 {
		n := min(255, len(s))
		chunk := s[:n]
		s = s[n:]
		parts = append(parts, `"`+strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(chunk)+`"`)
	}
	return strings.Join(parts, " ")
}

func rname(email string) string {
	local, domain, _ := strings.Cut(email, "@")
	return strings.ReplaceAll(local, ".", `\.`) + "." + strings.TrimSuffix(domain, ".") + "."
}

func abs(h, zone string) string {
	switch {
	case h == "@":
		return zone + "."
	case h == ".", strings.HasSuffix(h, "."):
		return h
	}
	return h
}

// NextSerial returns a YYYYMMDDnn serial greater than old.
func NextSerial(old uint32, now time.Time) uint32 {
	day, _ := strconv.ParseUint(now.UTC().Format("20060102")+"01", 10, 32)
	if uint32(day) > old {
		return uint32(day)
	}
	return old + 1
}

// Render produces the zone file.
func (z Zone) Render(serial uint32) string {
	var b strings.Builder
	fmt.Fprintf(&b, "; Managed by WhaCP (DNS). Do not edit: changes are overwritten.\n; serial %d\n", serial)
	fmt.Fprintf(&b, "$ORIGIN %s.\n$TTL %d\n", z.Name, z.TTL)
	s := z.SOA
	fmt.Fprintf(&b, "@\tIN\tSOA\t%s %s (\n\t\t%d ; serial\n\t\t%d ; refresh\n\t\t%d ; retry\n\t\t%d ; expire\n\t\t%d ; minimum\n\t)\n",
		abs(s.PrimaryNS, z.Name), rname(s.Hostmaster), serial, s.Refresh, s.Retry, s.Expire, s.Minimum)
	recs := append([]Record(nil), z.Records...)
	order := map[string]int{"NS": 0, "A": 1, "AAAA": 2, "CNAME": 3, "MX": 4, "TXT": 5, "SRV": 6, "CAA": 7, "PTR": 8}
	sort.SliceStable(recs, func(i, j int) bool {
		if (recs[i].Name == "@") != (recs[j].Name == "@") {
			return recs[i].Name == "@"
		}
		if recs[i].Name != recs[j].Name {
			return recs[i].Name < recs[j].Name
		}
		return order[recs[i].Type] < order[recs[j].Type]
	})
	for _, r := range recs {
		ttl := ""
		if r.TTL != 0 {
			ttl = strconv.Itoa(r.TTL)
		}
		var data string
		switch r.Type {
		case "MX":
			data = fmt.Sprintf("%d %s", r.Priority, abs(r.Value, z.Name))
		case "SRV":
			data = fmt.Sprintf("%d %d %d %s", r.Priority, r.Weight, r.Port, abs(r.Value, z.Name))
		case "CNAME", "NS", "PTR":
			data = abs(r.Value, z.Name)
		case "TXT":
			data = quoteTXT(r.Value)
		case "CAA":
			tag, val, _ := strings.Cut(r.Value, " ")
			data = fmt.Sprintf("%d %s %q", r.Priority, tag, val)
		default:
			data = r.Value
		}
		fmt.Fprintf(&b, "%s\t%s\tIN\t%s\t%s\n", r.Name, ttl, r.Type, data)
	}
	return b.String()
}

var serialRe = regexp.MustCompile(`(?m)^; serial (\d{1,10})$`)

// FileSerial reads the serial from a zone file WhaCP rendered.
func FileSerial(data string) uint32 {
	m := serialRe.FindStringSubmatch(data)
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseUint(m[1], 10, 32)
	return uint32(n)
}

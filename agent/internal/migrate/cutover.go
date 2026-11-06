package migrate

import (
	"context"
	"fmt"
	"net"
	"sort"
	"time"
)

// Resolver answers "what addresses does this name have?" as one DNS server sees it.
type Resolver interface {
	Lookup(ctx context.Context, server, domain string) ([]string, error)
}

// netResolver asks a named DNS server (or the system's when server is empty).
type netResolver struct{}

func (netResolver) Lookup(ctx context.Context, server, domain string) ([]string, error) {
	r := net.DefaultResolver
	if server != "" {
		d := net.Dialer{Timeout: 5 * time.Second}
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, "udp", server)
		}}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return r.LookupHost(ctx, domain)
}

// cutoverResolvers are asked in turn: this server's own resolver and well-known public ones, so
// a change that has reached some resolvers and not others is visible.
var cutoverResolvers = []struct{ Name, Server string }{
	{"This server's resolver", ""},
	{"Cloudflare (1.1.1.1)", "1.1.1.1:53"},
	{"Google (8.8.8.8)", "8.8.8.8:53"},
	{"Quad9 (9.9.9.9)", "9.9.9.9:53"},
}

// Statuses of a domain's DNS in a cut-over check.
const (
	DNSHere      = "here"      // every resolver that answered points to this server
	DNSElsewhere = "elsewhere" // none points to this server (the DNS change has not been made, or has not arrived)
	DNSMixed     = "mixed"     // some resolvers point here and some do not: the change is still spreading
	DNSNoAnswer  = "no_answer" // no resolver gave an address
)

// ResolverAnswer is what one resolver said.
type ResolverAnswer struct {
	Name       string   `json:"name"`
	Addrs      []string `json:"addrs"`
	PointsHere bool     `json:"points_here"`
	Error      string   `json:"error,omitempty"`
}

// DomainDNS is the DNS of one migrated website.
type DomainDNS struct {
	Domain    string           `json:"domain"`
	Status    string           `json:"status"`
	Advice    string           `json:"advice"`
	Resolvers []ResolverAnswer `json:"resolvers"`
}

// CutoverReport is the result of a DNS check. It never changes anything.
type CutoverReport struct {
	At        time.Time   `json:"at"`
	ServerIPs []string    `json:"server_ips"`
	Domains   []DomainDNS `json:"domains"`
	Notes     []string    `json:"notes"`
}

var advice = map[string]string{
	DNSHere:      "Every resolver that answered points to this server. Open the site to confirm it works, then keep the old server for a few days before removing it.",
	DNSElsewhere: "DNS still points elsewhere. When the site works here (see Validation), change its A/AAAA records at your DNS provider to this server's address. NimVor does not change DNS or nameservers.",
	DNSMixed:     "The change has reached some resolvers and not others: it is still spreading (it takes as long as the record's TTL). Check again later and keep the old server running until every resolver agrees.",
	DNSNoAnswer:  "No resolver returned an address. The name may not exist yet, or the DNS provider is not answering: check the records at your DNS provider.",
}

// CutoverCheck reads where each migrated website's name points now and whether that is this
// server, as seen by several resolvers. serverIPs are this server's public addresses.
// It reads DNS only; it contacts no source and changes no record.
func (e *Engine) CutoverCheck(ctx context.Context, id string, serverIPs []string) (*CutoverReport, error) {
	m, err := e.Store.Load(id)
	if err != nil {
		return nil, err
	}
	if m.Status == Created || m.Status == RolledBack {
		return nil, fmt.Errorf("a migration that is %s has nothing to switch", m.Status)
	}
	here := map[string]bool{}
	var ips []string
	for _, s := range serverIPs {
		if ip := net.ParseIP(s); ip != nil && len(ips) < 32 {
			here[ip.String()] = true
			ips = append(ips, ip.String())
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("this server's public addresses are not known, so DNS cannot be compared")
	}
	sort.Strings(ips)
	rep := &CutoverReport{At: time.Now().UTC(), ServerIPs: ips, Domains: []DomainDNS{}, Notes: []string{
		"NimVor does not change DNS records or nameservers. Update the records at your DNS provider, then check again.",
		"Lower the records' TTL (for example to 300 seconds) a day before switching, so the change spreads quickly, and raise it again afterwards. The TTL cannot be read here: look at it at your DNS provider.",
		"Mail records (MX) are not compared, and this version does not move email: do not change MX until your mail has been moved.",
	}}
	res := e.Resolver
	if res == nil {
		res = netResolver{}
	}
	seen := map[string]bool{}
	for _, f := range m.Plan.Files {
		if !domainName.MatchString(f.Domain) || seen[f.Domain] {
			continue
		}
		seen[f.Domain] = true
		d := DomainDNS{Domain: f.Domain, Resolvers: []ResolverAnswer{}}
		answered, pointing := 0, 0
		for _, r := range cutoverResolvers {
			a := ResolverAnswer{Name: r.Name, Addrs: []string{}}
			addrs, err := res.Lookup(ctx, r.Server, f.Domain)
			if err != nil {
				a.Error = "no answer"
			} else {
				all := len(addrs) > 0
				for _, s := range addrs {
					if ip := net.ParseIP(s); ip != nil {
						a.Addrs = append(a.Addrs, ip.String())
						if !here[ip.String()] {
							all = false
						}
					}
				}
				sort.Strings(a.Addrs)
				if len(a.Addrs) > 0 {
					answered++
					a.PointsHere = all
					if all {
						pointing++
					}
				}
			}
			d.Resolvers = append(d.Resolvers, a)
		}
		switch {
		case answered == 0:
			d.Status = DNSNoAnswer
		case pointing == answered:
			d.Status = DNSHere
		case pointing == 0:
			d.Status = DNSElsewhere
		default:
			d.Status = DNSMixed
		}
		d.Advice = advice[d.Status]
		rep.Domains = append(rep.Domains, d)
	}
	m.Cutover = rep
	return rep, e.Store.Save(m)
}

package mailmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/spf"
)

// Email authentication checker: what the public DNS says about a mail
// domain (MX, SPF, DKIM, DMARC) and this server (host name, reverse DNS),
// compared with what NimVor expects. Read-only lookups.

// AuthResolver is the DNS the checker uses (net.Resolver satisfies it).
type AuthResolver interface {
	spf.Resolver
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

// Replaced in tests.
var (
	authResolver AuthResolver = net.DefaultResolver
	selectorRe                = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
)

// Check results.
const (
	CheckPass    = "pass"
	CheckWarn    = "warning"
	CheckFail    = "fail"
	CheckMissing = "missing"
	CheckInfo    = "info"
)

// AuthCheck is one result.
type AuthCheck struct {
	ID       string `json:"id"` // mx, spf, dkim, dmarc, host, rdns
	Label    string `json:"label"`
	Status   string `json:"status"`
	Found    string `json:"found,omitempty"`
	Expected string `json:"expected,omitempty"`
	Detail   string `json:"detail"`
}

// AuthCheckInput describes the domain and this server.
type AuthCheckInput struct {
	Domain   string   `json:"domain"`
	Hostname string   `json:"hostname"` // the mail host name (HELO, MX target)
	IPs      []string `json:"ips"`      // this server's public sending addresses
	Selector string   `json:"dkim_selector"`
	DKIMTXT  string   `json:"dkim_txt"` // the published key NimVor generated
	Routing  string   `json:"routing"`  // local, backup, remote
}

func lookupFailed(err error) bool {
	var de *net.DNSError
	return err != nil && !(errors.As(err, &de) && de.IsNotFound)
}

func txtRecords(ctx context.Context, r AuthResolver, name, prefix string) ([]string, error) {
	txts, err := r.LookupTXT(ctx, name)
	if lookupFailed(err) {
		return nil, err
	}
	var out []string
	for _, t := range txts {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(t)), strings.ToLower(prefix)) {
			out = append(out, t)
		}
	}
	return out, nil
}

// dkimKey returns the p= value of a DKIM record (whitespace removed).
func dkimKey(rec string) string {
	for _, part := range strings.Split(rec, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.TrimSpace(k) == "p" {
			return strings.Join(strings.Fields(v), "")
		}
	}
	return ""
}

func dmarcTags(rec string) map[string]string {
	tags := map[string]string{}
	for _, part := range strings.Split(rec, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			tags[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	return tags
}

// CheckAuth runs the checks.
func CheckAuth(ctx context.Context, r AuthResolver, in AuthCheckInput) []AuthCheck {
	dom := strings.ToLower(strings.TrimSuffix(in.Domain, "."))
	hostName := strings.ToLower(strings.TrimSuffix(in.Hostname, "."))
	var ips []netip.Addr
	for _, s := range in.IPs {
		if a, err := netip.ParseAddr(s); err == nil {
			ips = append(ips, a.Unmap())
		}
	}
	var out []AuthCheck
	add := func(c AuthCheck) { out = append(out, c) }
	ourAddr := func(addrs []netip.Addr) bool {
		for _, a := range addrs {
			if slices.Contains(ips, a.Unmap()) {
				return true
			}
		}
		return false
	}

	// Host name: the name this server greets with must point to it.
	if hostName != "" {
		addrs, err := r.LookupNetIP(ctx, "ip", hostName)
		switch {
		case lookupFailed(err):
			add(AuthCheck{ID: "host", Label: "Mail host name", Status: CheckWarn, Found: hostName, Detail: "The lookup failed (" + err.Error() + "); try again later."})
		case len(addrs) == 0:
			add(AuthCheck{ID: "host", Label: "Mail host name", Status: CheckMissing, Found: hostName, Expected: strings.Join(in.IPs, ", "), Detail: hostName + " has no A or AAAA record. Mail clients and other servers cannot find this server by name; add an A record pointing to this server."})
		case len(ips) > 0 && !ourAddr(addrs):
			add(AuthCheck{ID: "host", Label: "Mail host name", Status: CheckFail, Found: addrString(addrs), Expected: strings.Join(in.IPs, ", "), Detail: hostName + " points to another address than this server's, so the server's greeting and TLS certificate name do not match its address."})
		default:
			add(AuthCheck{ID: "host", Label: "Mail host name", Status: CheckPass, Found: addrString(addrs), Detail: hostName + " points to this server."})
		}
	}

	// MX.
	mxs, err := r.LookupMX(ctx, dom)
	switch {
	case lookupFailed(err):
		add(AuthCheck{ID: "mx", Label: "MX", Status: CheckWarn, Detail: "The lookup failed (" + err.Error() + "); try again later."})
	case len(mxs) == 0:
		st, detail := CheckMissing, dom+" has no MX record, so other servers cannot deliver mail for it here. Add an MX record for "+hostName+"."
		if in.Routing == "remote" {
			st, detail = CheckInfo, "No MX record; mail for this domain is handled elsewhere (routing: remote), so this server does not need one."
		}
		add(AuthCheck{ID: "mx", Label: "MX", Status: st, Expected: hostName, Detail: detail})
	default:
		var names []string
		here := false
		for _, mx := range mxs {
			n := strings.ToLower(strings.TrimSuffix(mx.Host, "."))
			names = append(names, fmt.Sprintf("%d %s", mx.Pref, n))
			if n == hostName {
				here = true
			} else if a, err := r.LookupNetIP(ctx, "ip", n); err == nil && ourAddr(a) {
				here = true
			}
		}
		c := AuthCheck{ID: "mx", Label: "MX", Found: strings.Join(names, ", "), Expected: hostName}
		switch {
		case here:
			c.Status, c.Detail = CheckPass, "Mail for "+dom+" is delivered to this server."
		case in.Routing == "remote":
			c.Status, c.Detail = CheckInfo, "The MX records point elsewhere; this domain's mail is handled there (routing: remote)."
		default:
			c.Status, c.Detail = CheckFail, "The MX records point to other servers, so incoming mail does not reach the mailboxes here. Point the MX record to "+hostName+", or set the domain's mail routing to remote if another server handles its mail."
		}
		add(c)
	}

	// SPF.
	recs, err := txtRecords(ctx, r, dom, "v=spf1")
	switch {
	case lookupFailed(err):
		add(AuthCheck{ID: "spf", Label: "SPF", Status: CheckWarn, Detail: "The lookup failed (" + err.Error() + "); try again later."})
	case len(recs) == 0:
		add(AuthCheck{ID: "spf", Label: "SPF", Status: CheckMissing, Expected: "v=spf1 a mx ~all", Detail: dom + " has no SPF record. Receiving servers cannot tell whether this server may send for it, and mail is more likely to be marked as spam. Publish the SPF record shown below."})
	case len(recs) > 1:
		add(AuthCheck{ID: "spf", Label: "SPF", Status: CheckFail, Found: strings.Join(recs, " | "), Detail: "There are several SPF records; receivers treat this as an error (RFC 7208 “permerror”). Merge them into one record."})
	default:
		c := AuthCheck{ID: "spf", Label: "SPF", Found: recs[0]}
		if len(ips) == 0 {
			c.Status, c.Detail = CheckInfo, "The record exists, but this server's public address is not known, so it cannot be tested."
			add(c)
			break
		}
		worst := spf.Pass
		var notes []string
		for _, ip := range ips {
			res, err := spf.Check(ctx, r, ip, "postmaster@"+dom, hostName)
			if res != spf.Pass {
				worst = res
				n := ip.String() + ": " + string(res)
				if err != nil {
					n += " (" + err.Error() + ")"
				}
				notes = append(notes, n)
			}
		}
		switch worst {
		case spf.Pass:
			c.Status, c.Detail = CheckPass, "The record authorizes this server ("+strings.Join(in.IPs, ", ")+") to send for "+dom+"."
		case spf.SoftFail, spf.Neutral:
			c.Status, c.Detail = CheckWarn, "The record does not authorize this server ("+strings.Join(notes, "; ")+"); receivers may mark its mail as spam. Add this server, e.g. with “a mx” or ip4:"+ips[0].String()+"."
		case spf.TempError:
			c.Status, c.Detail = CheckWarn, "A DNS lookup failed while evaluating the record ("+strings.Join(notes, "; ")+"); try again later."
		default:
			c.Status, c.Detail = CheckFail, "The record rejects mail from this server or is invalid ("+strings.Join(notes, "; ")+"). Receivers will refuse or junk its mail. Fix the record."
		}
		add(c)
	}

	// DKIM.
	if in.Selector != "" {
		name := in.Selector + "._domainkey." + dom
		recs, err := r.LookupTXT(ctx, name)
		want := dkimKey(in.DKIMTXT)
		c := AuthCheck{ID: "dkim", Label: "DKIM", Expected: name}
		var found string
		for _, t := range recs {
			if strings.Contains(t, "p=") {
				found = t
			}
		}
		switch {
		case lookupFailed(err):
			c.Status, c.Detail = CheckWarn, "The lookup failed ("+err.Error()+"); try again later."
		case found == "":
			c.Status, c.Detail = CheckMissing, "There is no DKIM key at "+name+". Mail is signed, but receivers cannot verify the signature. Publish the DKIM record shown below."
		case want != "" && dkimKey(found) != want:
			c.Status, c.Found, c.Detail = CheckFail, abbreviate(found, 120), "The published key differs from the key this server signs with, so signatures fail. Replace the record with the one shown below."
		default:
			c.Status, c.Found, c.Detail = CheckPass, abbreviate(found, 120), "The published key matches the key this server signs with."
		}
		add(c)
	}

	// DMARC.
	recs, err = txtRecords(ctx, r, "_dmarc."+dom, "v=DMARC1")
	switch {
	case lookupFailed(err):
		add(AuthCheck{ID: "dmarc", Label: "DMARC", Status: CheckWarn, Detail: "The lookup failed (" + err.Error() + "); try again later."})
	case len(recs) == 0:
		add(AuthCheck{ID: "dmarc", Label: "DMARC", Status: CheckMissing, Expected: "v=DMARC1; p=none; rua=mailto:postmaster@" + dom, Detail: "There is no DMARC record at _dmarc." + dom + ". Large providers expect one from bulk senders; start with p=none and tighten it once SPF and DKIM pass."})
	case len(recs) > 1:
		add(AuthCheck{ID: "dmarc", Label: "DMARC", Status: CheckFail, Found: strings.Join(recs, " | "), Detail: "There are several DMARC records; receivers ignore all of them. Keep one."})
	default:
		tags := dmarcTags(recs[0])
		c := AuthCheck{ID: "dmarc", Label: "DMARC", Found: recs[0]}
		switch strings.ToLower(tags["p"]) {
		case "reject", "quarantine":
			c.Status, c.Detail = CheckPass, "Policy “"+tags["p"]+"”: receivers act on mail that fails SPF and DKIM alignment."
		case "none":
			c.Status, c.Detail = CheckWarn, "Policy “none” only monitors: mail that fails checks is still delivered. Change it to quarantine or reject once reports show your mail passes."
		default:
			c.Status, c.Detail = CheckFail, "The record has no valid policy (p=none, quarantine or reject), so receivers ignore it."
		}
		add(c)
	}

	// Reverse DNS of each sending address.
	if len(ips) == 0 {
		add(AuthCheck{ID: "rdns", Label: "Reverse DNS", Status: CheckInfo, Detail: "This server's public address is not known (set the default IPv4 address under DNS settings), so its reverse DNS cannot be checked."})
	}
	for _, ip := range ips {
		c := AuthCheck{ID: "rdns", Label: "Reverse DNS " + ip.String(), Expected: hostName}
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			c.Status, c.Detail = CheckInfo, "A private address: the reverse DNS that counts is that of the public address this server sends from (set by the provider of that address)."
			add(c)
			continue
		}
		names, err := r.LookupAddr(ctx, ip.String())
		switch {
		case lookupFailed(err):
			c.Status, c.Detail = CheckWarn, "The lookup failed ("+err.Error()+"); try again later."
		case len(names) == 0:
			c.Status, c.Detail = CheckMissing, ip.String()+" has no reverse DNS (PTR) record. Many receivers refuse mail from such addresses. Ask the provider of this address to set its PTR to "+hostName+"."
		default:
			var clean []string
			confirmed, matches := false, false
			for _, n := range names {
				n = strings.ToLower(strings.TrimSuffix(n, "."))
				clean = append(clean, n)
				if a, err := r.LookupNetIP(ctx, "ip", n); err == nil && slices.Contains(unmapAll(a), ip) {
					confirmed = true
					if n == hostName {
						matches = true
					}
				}
			}
			c.Found = strings.Join(clean, ", ")
			switch {
			case matches:
				c.Status, c.Detail = CheckPass, "The PTR record names "+hostName+", which points back to this address."
			case confirmed:
				c.Status, c.Detail = CheckWarn, "The PTR record names another host than the mail host name "+hostName+". It is valid, but some receivers expect it to match the name the server greets with; ask the provider to set it to "+hostName+"."
			default:
				c.Status, c.Detail = CheckFail, "The PTR name does not point back to "+ip.String()+" (forward-confirmed reverse DNS fails); receivers treat such mail as suspicious. Ask the provider to set the PTR to "+hostName+" and make sure "+hostName+" points to this address."
			}
		}
		add(c)
	}
	return out
}

func unmapAll(a []netip.Addr) []netip.Addr {
	out := make([]netip.Addr, len(a))
	for i, x := range a {
		out[i] = x.Unmap()
	}
	return out
}

func addrString(a []netip.Addr) string {
	s := make([]string, len(a))
	for i, x := range a {
		s[i] = x.Unmap().String()
	}
	return strings.Join(s, ", ")
}

func abbreviate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func registerAuthCheck(r *actions.Registry, e env) {
	r.Register(actions.Action{Name: "mail.auth_check", Description: "Check a mail domain's public DNS: host name, MX, SPF (evaluated for this server), DKIM (compared with the signing key), DMARC and reverse DNS of the server's addresses.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in AuthCheckInput
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
		in.Hostname = strings.ToLower(strings.TrimSpace(in.Hostname))
		if !domainRe.MatchString(in.Domain) || (in.Hostname != "" && !domainRe.MatchString(in.Hostname)) {
			return nil, actions.InvalidParams("domain and hostname must be domain names")
		}
		if in.Selector != "" && !selectorRe.MatchString(in.Selector) {
			return nil, actions.InvalidParams("invalid DKIM selector")
		}
		if len(in.IPs) > 8 || len(in.DKIMTXT) > 4096 {
			return nil, actions.InvalidParams("too many addresses or a DKIM record too long")
		}
		for _, ip := range in.IPs {
			if _, err := netip.ParseAddr(ip); err != nil {
				return nil, actions.InvalidParams("invalid address " + ip)
			}
		}
		if e.Cfg.SimulateCommands {
			return map[string]any{"checks": []AuthCheck{}, "simulated": true}, nil
		}
		ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		return map[string]any{"checks": CheckAuth(ctx, authResolver, in), "checked_at": e.Now().UTC()}, nil
	}})
}

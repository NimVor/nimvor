package webprotect

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/netip"
	"sort"
	"strings"
)

// Access lists: addresses, browsers (User-Agent) and URLs a website refuses
// (deny) or always serves (allow). An allow entry of any kind wins over every
// deny entry, so a trusted address or URL is never locked out by a broad deny.

const (
	KindIP  = "ip"
	KindUA  = "ua"
	KindURL = "url"

	ActionAllow = "allow"
	ActionDeny  = "deny"

	// MaxAccess is the number of entries per website; MaxGlobalAccess for
	// the server-wide list.
	MaxAccess       = 200
	MaxGlobalAccess = 1000
	maxRangeParts   = 64
)

// AccessStatuses is what a refused request is answered with.
var AccessStatuses = []int{403, 404, 444}

// Access is one list entry.
type Access struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
}

// ValidAccessStatus reports whether a refused request may be answered so.
func ValidAccessStatus(n int) bool {
	for _, s := range AccessStatuses {
		if s == n {
			return true
		}
	}
	return false
}

// Validate checks one entry and returns it cleaned.
func (a Access) Validate() (Access, error) {
	a.Value = strings.TrimSpace(a.Value)
	a.Note = strings.TrimSpace(a.Note)
	if a.Action != ActionAllow && a.Action != ActionDeny {
		return a, fmt.Errorf("action: allow or deny")
	}
	if len(a.Note) > 100 || strings.ContainsAny(a.Note, "\r\n\x00") {
		return a, fmt.Errorf("note: at most 100 characters on one line")
	}
	switch a.Kind {
	case KindIP:
		if _, err := ParseAddresses(a.Value); err != nil {
			return a, err
		}
	case KindUA:
		if a.Value == "" || len(a.Value) > 100 {
			return a, fmt.Errorf("browser: 1 to 100 characters")
		}
		for _, r := range a.Value {
			if r < 0x20 || r > 0x7e || strings.ContainsRune("\"$\\{};", r) {
				return a, fmt.Errorf("browser: letters, digits, spaces and . , : - _ / ( ) + = ! ? * | @ # %% & ' only (no quotes, $, backslash, braces or semicolon)")
			}
		}
	case KindURL:
		if !strings.HasPrefix(a.Value, "/") {
			return a, fmt.Errorf("URL: must start with /")
		}
		p, err := CleanPath(a.Value)
		if err != nil {
			return a, fmt.Errorf("URL: %v", err)
		}
		a.Value = p
	default:
		return a, fmt.Errorf("kind: ip, ua or url")
	}
	return a, nil
}

// ParseAddresses turns "1.2.3.4", "10.0.0.0/8", "::1", "2001:db8::/32" or
// "10.0.0.1-10.0.0.9" into prefixes.
func ParseAddresses(v string) ([]netip.Prefix, error) {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 100 {
		return nil, fmt.Errorf("address: an address, a network like 10.0.0.0/8 or a range like 10.0.0.1-10.0.0.9")
	}
	if i := strings.Index(v, "-"); i > 0 {
		lo, err1 := netip.ParseAddr(strings.TrimSpace(v[:i]))
		hi, err2 := netip.ParseAddr(strings.TrimSpace(v[i+1:]))
		if err1 != nil || err2 != nil || lo.Is4() != hi.Is4() || lo.Is4In6() || lo.Zone() != "" || hi.Zone() != "" {
			return nil, fmt.Errorf("address: %q is not a range of two addresses of one kind", v)
		}
		if hi.Less(lo) {
			return nil, fmt.Errorf("address: the range %q ends before it starts", v)
		}
		out := rangePrefixes(lo, hi)
		if len(out) > maxRangeParts {
			return nil, fmt.Errorf("address: the range %q is too irregular (use networks like 10.0.0.0/8)", v)
		}
		return out, nil
	}
	if strings.Contains(v, "/") {
		p, err := netip.ParsePrefix(v)
		if err != nil || p.Addr().Zone() != "" || p.Addr().Is4In6() {
			return nil, fmt.Errorf("address: %q is not a network like 10.0.0.0/8", v)
		}
		return []netip.Prefix{p.Masked()}, nil
	}
	a, err := netip.ParseAddr(v)
	if err != nil || a.Zone() != "" || a.Is4In6() {
		return nil, fmt.Errorf("address: %q is not an IP address", v)
	}
	return []netip.Prefix{netip.PrefixFrom(a, a.BitLen())}, nil
}

func addrInt(a netip.Addr) *big.Int { return new(big.Int).SetBytes(a.AsSlice()) }

func intAddr(n *big.Int, v4 bool) netip.Addr {
	size := 16
	if v4 {
		size = 4
	}
	b := n.FillBytes(make([]byte, size))
	a, _ := netip.AddrFromSlice(b)
	return a
}

// rangePrefixes covers lo..hi with the fewest networks.
func rangePrefixes(lo, hi netip.Addr) []netip.Prefix {
	v4 := lo.Is4()
	bits := lo.BitLen()
	cur, end := addrInt(lo), addrInt(hi)
	one := big.NewInt(1)
	var out []netip.Prefix
	for cur.Cmp(end) <= 0 {
		// The largest block that starts at cur and stays inside the range.
		size := 0
		for size < bits {
			next := size + 1
			block := new(big.Int).Lsh(one, uint(next))
			if new(big.Int).Mod(cur, block).Sign() != 0 {
				break
			}
			last := new(big.Int).Add(cur, block)
			last.Sub(last, one)
			if last.Cmp(end) > 0 {
				break
			}
			size = next
		}
		out = append(out, netip.PrefixFrom(intAddr(cur, v4), bits-size))
		cur = new(big.Int).Add(cur, new(big.Int).Lsh(one, uint(size)))
		if len(out) > maxRangeParts*2 {
			break
		}
	}
	return out
}

func accessID(domain string) string {
	h := sha1.Sum([]byte("access:" + domain))
	return hex.EncodeToString(h[:])[:10]
}

const globalAccessID = "global"

// accessVars are the variable names of one list.
type accessVars struct{ ip, ua, url, block string }

func varsFor(id string) accessVars {
	return accessVars{ip: "nvip_" + id, ua: "nvua_" + id, url: "nvurl_" + id, block: "nvblock_" + id}
}

func hasKind(list []Access, kind string) bool {
	for _, a := range list {
		if a.Kind == kind {
			return true
		}
	}
	return false
}

func hasAction(list []Access, kind, action string) bool {
	for _, a := range list {
		if a.Kind == kind && a.Action == action {
			return true
		}
	}
	return false
}

// verdict is what a map or geo yields: 1 refuse, 2 serve.
func verdict(action string) string {
	if action == ActionAllow {
		return "2"
	}
	return "1"
}

// renderAccessOne is one list's variables.
func renderAccessOne(b *strings.Builder, id, label string, list []Access, withACME bool) {
	v := varsFor(id)
	if hasKind(list, KindIP) {
		// Identical networks: an allow entry wins. Different sizes: the most
		// specific network wins (how geo works).
		seen := map[netip.Prefix]string{}
		for _, a := range list {
			if a.Kind != KindIP {
				continue
			}
			ps, err := ParseAddresses(a.Value)
			if err != nil {
				continue
			}
			for _, p := range ps {
				if old, ok := seen[p]; !ok || a.Action == ActionAllow && old != ActionAllow {
					seen[p] = a.Action
				}
			}
		}
		keys := make([]netip.Prefix, 0, len(seen))
		for p := range seen {
			keys = append(keys, p)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		fmt.Fprintf(b, "# %s\ngeo $%s {\n    default 0;\n", label, v.ip)
		for _, p := range keys {
			fmt.Fprintf(b, "    %s %s;\n", p.String(), verdict(seen[p]))
		}
		b.WriteString("}\n")
	}
	if hasKind(list, KindUA) {
		fmt.Fprintf(b, "map $http_user_agent $%s {\n    default 0;\n", v.ua)
		// Allow entries first: the first matching pattern decides.
		for _, action := range []string{ActionAllow, ActionDeny} {
			for _, a := range list {
				if a.Kind == KindUA && a.Action == action {
					fmt.Fprintf(b, "    \"~*%s\" %s;\n", quoteRe(a.Value), verdict(action))
				}
			}
		}
		b.WriteString("}\n")
	}
	if hasKind(list, KindURL) || withACME {
		fmt.Fprintf(b, "map $uri $%s {\n    default 0;\n", v.url)
		if withACME {
			// Certificates keep renewing whatever the lists say.
			b.WriteString("    \"~^/\\.well-known/acme-challenge/\" 2;\n")
		}
		for _, action := range []string{ActionAllow, ActionDeny} {
			for _, a := range list {
				if a.Kind == KindURL && a.Action == action {
					fmt.Fprintf(b, "    \"~^%s\" %s;\n", quoteRe(a.Value), verdict(action))
				}
			}
		}
		b.WriteString("}\n")
	}
}

// parts is the variables of a list, in the order they are joined.
func (v accessVars) parts(list []Access, withACME bool) []string {
	var out []string
	if hasKind(list, KindIP) {
		out = append(out, "$"+v.ip)
	}
	if hasKind(list, KindUA) {
		out = append(out, "$"+v.ua)
	}
	if hasKind(list, KindURL) || withACME {
		out = append(out, "$"+v.url)
	}
	return out
}

// effectiveSites are the websites with a list of their own.
func accessSites(st State) []string {
	var out []string
	for d, s := range st.Sites {
		if s != nil && len(s.Access) > 0 {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// hasGlobal reports whether a server-wide list is in force.
func (st State) hasGlobal() bool { return len(st.Global) > 0 }

// blockVar is the variable a website's server block tests ("" for none).
func blockVar(st State, domain string) string {
	if s := st.Sites[domain]; s != nil && len(s.Access) > 0 {
		return varsFor(accessID(domain)).block
	}
	if st.hasGlobal() {
		return varsFor(globalAccessID).block
	}
	return ""
}

// renderAccessHTTP is the http-level part of every list.
func renderAccessHTTP(st State) string {
	sites := accessSites(st)
	if len(sites) == 0 && !st.hasGlobal() {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Managed by NimVor (website access lists). Do not edit.\n")
	var gparts []string
	if st.hasGlobal() {
		renderAccessOne(&b, globalAccessID, "server-wide", st.Global, true)
		gparts = varsFor(globalAccessID).parts(st.Global, true)
		combine(&b, globalAccessID, gparts)
	}
	for _, d := range sites {
		s := st.Sites[d]
		id := accessID(d)
		renderAccessOne(&b, id, d, s.Access, !st.hasGlobal())
		parts := append(append([]string{}, gparts...), varsFor(id).parts(s.Access, !st.hasGlobal())...)
		combine(&b, id, parts)
	}
	// The allow-listed addresses, for the traffic limits.
	all := map[string]bool{}
	for d := range st.Sites {
		all[d] = true
	}
	doms := make([]string, 0, len(all))
	for d := range all {
		doms = append(doms, d)
	}
	sort.Strings(doms)
	for _, d := range doms {
		if hasEnabledLimit(st.Sites[d]) {
			renderAllowMaps(&b, st, d)
		}
	}
	return b.String()
}

// combine yields the final verdict: an allow anywhere wins, else a deny.
func combine(b *strings.Builder, id string, parts []string) {
	v := varsFor(id)
	fmt.Fprintf(b, "map \"%s\" $%s {\n    default 0;\n    \"~2\" 0;\n    \"~1\" 1;\n}\n", strings.Join(parts, ""), v.block)
}

// renderAccessServer switches a website's lists on (server level).
func renderAccessServer(st State, domain string) string {
	v := blockVar(st, domain)
	if v == "" {
		return ""
	}
	status := 403
	if s := st.Sites[domain]; s != nil && ValidAccessStatus(s.AccessStatus) {
		status = s.AccessStatus
	} else if ValidAccessStatus(st.GlobalStatus) {
		status = st.GlobalStatus
	}
	return fmt.Sprintf("    # Access lists\n    if ($%s) {\n        return %d;\n    }\n", v, status)
}

// allowVar is the variable that is 1 for an allow-listed address ("" when the
// website has no allowed address).
func allowVar(st State, domain string) string {
	var parts []string
	if st.hasGlobal() && hasAction(st.Global, KindIP, ActionAllow) {
		parts = append(parts, "$"+varsFor(globalAccessID).ip)
	}
	if s := st.Sites[domain]; s != nil && hasAction(s.Access, KindIP, ActionAllow) {
		parts = append(parts, "$"+varsFor(accessID(domain)).ip)
	}
	if len(parts) == 0 {
		return ""
	}
	return "nvallow_" + accessID(domain)
}

// renderAllowMaps is the verdict variables behind allowVar.
func renderAllowMaps(b *strings.Builder, st State, domain string) {
	var parts []string
	if st.hasGlobal() && hasAction(st.Global, KindIP, ActionAllow) {
		parts = append(parts, "$"+varsFor(globalAccessID).ip)
	}
	if s := st.Sites[domain]; s != nil && hasAction(s.Access, KindIP, ActionAllow) {
		parts = append(parts, "$"+varsFor(accessID(domain)).ip)
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(b, "map \"%s\" $nvallow_%s {\n    default 0;\n    \"~2\" 1;\n}\n", strings.Join(parts, ""), accessID(domain))
}

package webprotect

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Country restrictions: a website refuses visitors from chosen countries
// (deny), or serves only visitors from chosen countries (allow_only). The
// panel resolves countries to address ranges (its DB-IP database); the agent
// turns them into networks and an Nginx geo variable. Private and loopback
// addresses are never refused, and an allow entry of the website's access list
// (access.go) beats a country.

const (
	CountryDeny      = "deny"
	CountryAllowOnly = "allow_only"

	MaxCountryCodes    = 250
	MaxCountryPrefixes = 600000
)

var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)

// Countries is a website's country restriction.
type Countries struct {
	Mode  string   `json:"mode"`
	Codes []string `json:"codes"`
}

// CountryRange is an address range of a country.
type CountryRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// privateRanges are never refused by an allow_only restriction.
var privateRanges = []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "::1/128", "fc00::/7", "fe80::/10"}

// Validate checks a restriction.
func (c Countries) Validate() error {
	if c.Mode != CountryDeny && c.Mode != CountryAllowOnly {
		return fmt.Errorf("mode: deny or allow_only")
	}
	if len(c.Codes) == 0 || len(c.Codes) > MaxCountryCodes {
		return fmt.Errorf("countries: 1 to %d", MaxCountryCodes)
	}
	seen := map[string]bool{}
	for _, code := range c.Codes {
		if !countryRe.MatchString(code) {
			return fmt.Errorf("countries: %q is not a two-letter country code", code)
		}
		if seen[code] {
			return fmt.Errorf("countries: %s twice", code)
		}
		seen[code] = true
	}
	return nil
}

// RangePrefixes covers a range of addresses with networks (text form).
func RangePrefixes(from, to string) ([]string, error) {
	lo, err1 := netip.ParseAddr(from)
	hi, err2 := netip.ParseAddr(to)
	if err1 != nil || err2 != nil || lo.Is4() != hi.Is4() || hi.Less(lo) || lo.Zone() != "" || hi.Zone() != "" {
		return nil, fmt.Errorf("%s-%s is not an address range", from, to)
	}
	var out []string
	if lo.Is4() {
		a, b := v4Int(lo), v4Int(hi)
		for a <= b {
			size := uint(0)
			for size < 32 {
				n := size + 1
				block := uint64(1) << n
				if uint64(a)%block != 0 || uint64(a)+block-1 > uint64(b) {
					break
				}
				size = n
			}
			out = append(out, fmt.Sprintf("%s/%d", intV4(a), 32-size))
			next := uint64(a) + uint64(1)<<size
			if next > 0xffffffff {
				break
			}
			a = uint32(next)
		}
		return out, nil
	}
	for _, p := range rangePrefixes(lo, hi) {
		out = append(out, p.String())
	}
	return out, nil
}

func v4Int(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func intV4(n uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)})
}

func countrySetID(c *Countries) string {
	codes := append([]string{}, c.Codes...)
	sort.Strings(codes)
	h := sha1.Sum([]byte("country:" + c.Mode + ":" + strings.Join(codes, ",")))
	return hex.EncodeToString(h[:])[:10]
}

func countryVar(c *Countries) string { return "nvcc_" + countrySetID(c) }

// renderCountries is the geo variable of every distinct restriction.
func renderCountries(st State) string {
	type set struct {
		c     *Countries
		label string
	}
	sets := map[string]set{}
	domains := make([]string, 0, len(st.Sites))
	for d := range st.Sites {
		domains = append(domains, d)
	}
	sort.Strings(domains)
	for _, d := range domains {
		if s := st.Sites[d]; s != nil && s.Countries != nil {
			id := countrySetID(s.Countries)
			if old, ok := sets[id]; ok {
				sets[id] = set{old.c, old.label + ", " + d}
			} else {
				sets[id] = set{s.Countries, d}
			}
		}
	}
	ids := make([]string, 0, len(sets))
	for id := range sets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		c := sets[id].c
		seen := map[string]bool{}
		var prefixes []string
		for _, code := range c.Codes {
			for _, p := range st.Geo[code] {
				if !seen[p] {
					seen[p] = true
					prefixes = append(prefixes, p)
				}
			}
		}
		sort.Strings(prefixes)
		verdict, def := "1", "0"
		if c.Mode == CountryAllowOnly {
			verdict, def = "0", "1"
		}
		fmt.Fprintf(&b, "# countries %s (%s): %s\ngeo $nvcc_%s {\n    default %s;\n", c.Mode, strings.Join(c.Codes, " "), sets[id].label, id, def)
		if c.Mode == CountryAllowOnly {
			for _, p := range privateRanges {
				if !seen[p] {
					fmt.Fprintf(&b, "    %s 0;\n", p)
				}
			}
		}
		for _, p := range prefixes {
			fmt.Fprintf(&b, "    %s %s;\n", p, verdict)
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// pruneGeo forgets the networks of countries no website uses.
func (st *State) pruneGeo() {
	used := map[string]bool{}
	for _, s := range st.Sites {
		if s != nil && s.Countries != nil {
			for _, c := range s.Countries.Codes {
				used[c] = true
			}
		}
	}
	for code := range st.Geo {
		if !used[code] {
			delete(st.Geo, code)
		}
	}
	if len(st.Geo) == 0 {
		st.Geo = nil
	}
}

// UpdateCountries sets (or, with nil, removes) a website's country
// restriction, together with the networks of its countries.
func UpdateCountries(ctx context.Context, e *host.Env, domain string, c *Countries, ranges map[string][]CountryRange, status int) (*Site, error) {
	var geo map[string][]string
	if c != nil {
		geo = map[string][]string{}
		total := 0
		for _, code := range c.Codes {
			rs := ranges[code]
			seen := map[string]bool{}
			for _, r := range rs {
				ps, err := RangePrefixes(r.From, r.To)
				if err != nil {
					return nil, actions.InvalidParams(fmt.Sprintf("ranges %s: %v", code, err))
				}
				for _, p := range ps {
					if !seen[p] {
						seen[p] = true
						geo[code] = append(geo[code], p)
					}
				}
				total += len(ps)
				if total > MaxCountryPrefixes {
					return nil, actions.InvalidParams("ranges: too many networks (choose fewer countries)")
				}
			}
			if len(geo[code]) == 0 {
				return nil, actions.InvalidParams("ranges: the country database has no addresses for " + code)
			}
		}
	}
	return updateWith(ctx, e, domain, func(st *State, s *Site) error {
		if c == nil {
			s.Countries = nil
		} else {
			s.Countries = c
			s.AccessStatus = status
			if st.Geo == nil {
				st.Geo = map[string][]string{}
			}
			for code, ps := range geo {
				st.Geo[code] = ps
			}
		}
		st.pruneGeo()
		return nil
	})
}

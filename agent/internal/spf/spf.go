// Package spf evaluates SPF records (RFC 7208) and lists the address
// ranges a domain's SPF record allows. It is used by greylisting (bypass
// for senders that pass SPF, trusted mail provider ranges) and by the
// email authentication checker.
package spf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// Result is an SPF result.
type Result string

const (
	None      Result = "none"
	Neutral   Result = "neutral"
	Pass      Result = "pass"
	Fail      Result = "fail"
	SoftFail  Result = "softfail"
	TempError Result = "temperror"
	PermError Result = "permerror"
)

// Resolver is the DNS the evaluator uses (net.Resolver satisfies it).
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

// DNS lookup limits of RFC 7208 section 4.6.4.
const (
	maxLookups     = 10
	maxVoidLookups = 2
	maxMXAddrs     = 10
)

var errLimit = errors.New("too many DNS lookups")

type checker struct {
	r       Resolver
	ip      netip.Addr
	sender  string // local@domain
	helo    string
	lookups int
	voids   int
	limit   int
	// Flatten mode: collect ranges instead of matching.
	flatten bool
	ranges  []netip.Prefix
}

// Record returns the SPF record of a domain ("" and None when there is
// none; PermError when there are several).
func Record(ctx context.Context, r Resolver, domain string) (string, Result, error) {
	txts, err := r.LookupTXT(ctx, domain)
	if err != nil {
		var de *net.DNSError
		if errors.As(err, &de) && de.IsNotFound {
			return "", None, nil
		}
		return "", TempError, err
	}
	var found []string
	for _, t := range txts {
		l := strings.ToLower(t)
		if l == "v=spf1" || strings.HasPrefix(l, "v=spf1 ") {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 0:
		return "", None, nil
	case 1:
		return found[0], Pass, nil
	}
	return "", PermError, fmt.Errorf("%s has %d SPF records", domain, len(found))
}

// Check evaluates the SPF record of the sender's domain (or of the HELO
// name when the sender is empty) for ip.
func Check(ctx context.Context, r Resolver, ip netip.Addr, sender, helo string) (Result, error) {
	sender = strings.ToLower(strings.TrimSpace(sender))
	helo = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(helo), "."))
	if sender == "" {
		sender = "postmaster@" + helo
	} else if !strings.Contains(sender, "@") {
		sender = "postmaster@" + sender
	}
	_, domain, _ := strings.Cut(sender, "@")
	if !validDomain(domain) {
		return None, nil
	}
	c := &checker{r: r, ip: ip.Unmap(), sender: sender, helo: helo, limit: maxLookups}
	return c.evaluate(ctx, domain, 0)
}

// Ranges lists the address ranges a domain's SPF record allows through
// ip4, ip6, a, mx, include and redirect (exists and ptr cannot be listed
// and are skipped). Meant for trusted mail providers, whose records use
// more includes than a check allows, so the lookup limit is higher.
func Ranges(ctx context.Context, r Resolver, domain string) ([]netip.Prefix, error) {
	c := &checker{r: r, flatten: true, limit: 50, sender: "postmaster@" + domain}
	res, err := c.evaluate(ctx, strings.ToLower(domain), 0)
	if err != nil {
		return nil, err
	}
	if res == None {
		return nil, fmt.Errorf("%s has no SPF record", domain)
	}
	return c.ranges, nil
}

func (c *checker) lookup() error {
	c.lookups++
	if c.lookups > c.limit {
		return errLimit
	}
	return nil
}

// void counts lookups that found nothing (RFC 7208 4.6.4).
func (c *checker) void() error {
	c.voids++
	if c.voids > maxVoidLookups && !c.flatten {
		return errLimit
	}
	return nil
}

func notFound(err error) bool {
	var de *net.DNSError
	return errors.As(err, &de) && de.IsNotFound
}

func (c *checker) evaluate(ctx context.Context, domain string, depth int) (Result, error) {
	if depth > 10 {
		return PermError, errLimit
	}
	rec, res, err := Record(ctx, c.r, domain)
	if rec == "" {
		return res, err
	}
	var redirect string
	for _, term := range strings.Fields(rec)[1:] {
		lower := strings.ToLower(term)
		if strings.HasPrefix(lower, "redirect=") {
			redirect = term[len("redirect="):]
			continue
		}
		if strings.HasPrefix(lower, "exp=") {
			continue
		}
		if i := strings.IndexByte(term, '='); i > 0 && !strings.ContainsAny(term[:i], ":/") {
			continue // unknown modifier
		}
		q := Pass
		switch term[0] {
		case '+':
			term = term[1:]
		case '-':
			q, term = Fail, term[1:]
		case '~':
			q, term = SoftFail, term[1:]
		case '?':
			q, term = Neutral, term[1:]
		}
		match, err := c.mechanism(ctx, domain, term, q, depth)
		if err != nil {
			if errors.Is(err, errLimit) {
				return PermError, err
			}
			var pe permError
			if errors.As(err, &pe) {
				return PermError, err
			}
			return TempError, err
		}
		if match && !c.flatten {
			return q, nil
		}
	}
	if redirect != "" {
		if err := c.lookup(); err != nil {
			return PermError, err
		}
		target, err := c.expand(redirect, domain)
		if err != nil {
			return PermError, err
		}
		res, err := c.evaluate(ctx, target, depth+1)
		if res == None {
			return PermError, fmt.Errorf("redirect to %s: no SPF record", target)
		}
		return res, err
	}
	return Neutral, nil
}

type permError struct{ msg string }

func (e permError) Error() string { return e.msg }

// splitCIDR separates "domain/24//64".
func splitCIDR(s string) (string, int, int, error) {
	v4, v6 := 32, 128
	if i := strings.Index(s, "//"); i >= 0 {
		n, err := strconv.Atoi(s[i+2:])
		if err != nil || n < 0 || n > 128 {
			return "", 0, 0, permError{"bad ip6 prefix in " + s}
		}
		v6, s = n, s[:i]
	}
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		n, err := strconv.Atoi(s[i+1:])
		if err != nil || n < 0 || n > 32 {
			return "", 0, 0, permError{"bad ip4 prefix in " + s}
		}
		v4, s = n, s[:i]
	}
	return s, v4, v6, nil
}

func (c *checker) addrMatch(addrs []netip.Addr, v4, v6 int) bool {
	for _, a := range addrs {
		a = a.Unmap()
		bits := v6
		if a.Is4() {
			bits = v4
		}
		p, err := a.Prefix(bits)
		if err != nil {
			continue
		}
		if c.flatten {
			c.ranges = append(c.ranges, p)
		} else if p.Contains(c.ip) {
			return true
		}
	}
	return false
}

func (c *checker) mechanism(ctx context.Context, domain, term string, q Result, depth int) (bool, error) {
	name, arg, _ := strings.Cut(term, ":")
	lname := strings.ToLower(name)
	var cidr string
	if i := strings.IndexByte(lname, '/'); i >= 0 && arg == "" {
		lname, cidr = lname[:i], name[i:]
	}
	target := func() (string, int, int, error) {
		t, v4, v6, err := splitCIDR(arg + cidr)
		if err != nil {
			return "", 0, 0, err
		}
		if t == "" {
			return domain, v4, v6, nil
		}
		t, err = c.expand(t, domain)
		return t, v4, v6, err
	}
	switch lname {
	case "all":
		return !c.flatten, nil
	case "ip4", "ip6":
		s := arg
		if !strings.Contains(s, "/") {
			if lname == "ip4" {
				s += "/32"
			} else {
				s += "/128"
			}
		}
		p, err := netip.ParsePrefix(s)
		if err != nil || (lname == "ip4") != p.Addr().Is4() {
			return false, permError{"bad " + term}
		}
		p = p.Masked()
		if c.flatten {
			if q == Pass {
				c.ranges = append(c.ranges, p)
			}
			return false, nil
		}
		return p.Contains(c.ip), nil
	case "a":
		if err := c.lookup(); err != nil {
			return false, err
		}
		t, v4, v6, err := target()
		if err != nil {
			return false, err
		}
		addrs, err := c.r.LookupNetIP(ctx, "ip", t)
		if err != nil {
			if notFound(err) {
				return false, c.void()
			}
			return false, err
		}
		if c.flatten && q != Pass {
			return false, nil
		}
		return c.addrMatch(addrs, v4, v6), nil
	case "mx":
		if err := c.lookup(); err != nil {
			return false, err
		}
		t, v4, v6, err := target()
		if err != nil {
			return false, err
		}
		mxs, err := c.r.LookupMX(ctx, t)
		if err != nil {
			if notFound(err) {
				return false, c.void()
			}
			return false, err
		}
		if len(mxs) > maxMXAddrs {
			return false, permError{"more than 10 MX records"}
		}
		for _, mx := range mxs {
			addrs, err := c.r.LookupNetIP(ctx, "ip", strings.TrimSuffix(mx.Host, "."))
			if err != nil {
				continue
			}
			if c.flatten && q != Pass {
				continue
			}
			if c.addrMatch(addrs, v4, v6) {
				return true, nil
			}
		}
		return false, nil
	case "include":
		if err := c.lookup(); err != nil {
			return false, err
		}
		if arg == "" {
			return false, permError{"include without a domain"}
		}
		t, err := c.expand(arg, domain)
		if err != nil {
			return false, err
		}
		if c.flatten && q != Pass {
			return false, nil
		}
		res, err := c.evaluate(ctx, t, depth+1)
		switch res {
		case Pass:
			return !c.flatten, nil
		case None:
			return false, permError{"include of " + t + ": no SPF record"}
		case TempError, PermError:
			if c.flatten && err != nil && !errors.Is(err, errLimit) {
				return false, nil // list what can be listed
			}
			if res == PermError {
				return false, permError{fmt.Sprint(err)}
			}
			return false, err
		}
		return false, nil
	case "exists":
		if err := c.lookup(); err != nil {
			return false, err
		}
		if c.flatten {
			return false, nil
		}
		t, err := c.expand(arg, domain)
		if err != nil {
			return false, err
		}
		addrs, err := c.r.LookupNetIP(ctx, "ip4", t)
		if err != nil {
			if notFound(err) {
				return false, c.void()
			}
			return false, err
		}
		return len(addrs) > 0, nil
	case "ptr":
		// Deprecated (RFC 7208 5.5) and slow: never matches here.
		if err := c.lookup(); err != nil {
			return false, err
		}
		return false, nil
	}
	return false, permError{"unknown mechanism " + name}
}

// expand applies SPF macros (RFC 7208 section 7) to a domain spec.
func (c *checker) expand(spec, domain string) (string, error) {
	if !strings.Contains(spec, "%") {
		return strings.TrimSuffix(strings.ToLower(spec), "."), nil
	}
	local, sdomain, _ := strings.Cut(c.sender, "@")
	var b strings.Builder
	for i := 0; i < len(spec); i++ {
		if spec[i] != '%' {
			b.WriteByte(spec[i])
			continue
		}
		if i+1 >= len(spec) {
			return "", permError{"bad macro in " + spec}
		}
		i++
		switch spec[i] {
		case '%':
			b.WriteByte('%')
			continue
		case '_':
			b.WriteByte(' ')
			continue
		case '-':
			b.WriteString("%20")
			continue
		case '{':
		default:
			return "", permError{"bad macro in " + spec}
		}
		end := strings.IndexByte(spec[i:], '}')
		if end < 2 {
			return "", permError{"bad macro in " + spec}
		}
		m := spec[i+1 : i+end]
		i += end
		var v string
		switch strings.ToLower(m[:1]) {
		case "s":
			v = c.sender
		case "l":
			v = local
		case "o":
			v = sdomain
		case "d":
			v = domain
		case "h":
			v = c.helo
		case "v":
			if c.ip.Is4() {
				v = "in-addr"
			} else {
				v = "ip6"
			}
		case "i":
			if c.ip.Is4() {
				v = c.ip.String()
			} else {
				var parts []string
				for _, x := range c.ip.As16() {
					parts = append(parts, fmt.Sprintf("%x", x>>4), fmt.Sprintf("%x", x&15))
				}
				v = strings.Join(parts, ".")
			}
		default:
			return "", permError{"unsupported macro %{" + m + "}"}
		}
		// Transformers: digits, r, delimiters.
		rest := m[1:]
		n := 0
		for len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9' {
			n = n*10 + int(rest[0]-'0')
			rest = rest[1:]
		}
		reverse := false
		if len(rest) > 0 && (rest[0] == 'r' || rest[0] == 'R') {
			reverse, rest = true, rest[1:]
		}
		delims := "."
		if rest != "" {
			if strings.Trim(rest, ".-+,/_=") != "" {
				return "", permError{"bad macro delimiter in " + spec}
			}
			delims = rest
		}
		parts := strings.FieldsFunc(v, func(r rune) bool { return strings.ContainsRune(delims, r) })
		if reverse {
			for a, z := 0, len(parts)-1; a < z; a, z = a+1, z-1 {
				parts[a], parts[z] = parts[z], parts[a]
			}
		}
		if n > 0 && n < len(parts) {
			parts = parts[len(parts)-n:]
		}
		b.WriteString(strings.Join(parts, "."))
	}
	out := strings.TrimSuffix(strings.ToLower(b.String()), ".")
	for len(out) > 253 {
		_, out, _ = strings.Cut(out, ".")
	}
	return out, nil
}

func validDomain(d string) bool {
	if d == "" || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, l := range strings.Split(d, ".") {
		if l == "" || len(l) > 63 {
			return false
		}
	}
	return true
}

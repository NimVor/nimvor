// Package clientip resolves the address of the client behind trusted reverse
// proxies (Request::ip with security.trusted_proxies in the PHP panel).
package clientip

import (
	"net"
	"net/http"
	"strings"
	"sync/atomic"
)

var trusted atomic.Pointer[[]*net.IPNet]

// Parse turns "10.0.0.1, 192.168.0.0/16, ::1" into networks; invalid entries
// are reported so a typo cannot silently widen or narrow trust.
func Parse(list string) ([]*net.IPNet, []string) {
	var nets []*net.IPNet
	var bad []string
	for _, e := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if !strings.Contains(e, "/") {
			ip := net.ParseIP(e)
			if ip == nil {
				bad = append(bad, e)
				continue
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
				ip = ip.To4()
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(e)
		if err != nil {
			bad = append(bad, e)
			continue
		}
		nets = append(nets, n)
	}
	return nets, bad
}

// SetTrusted installs the trusted proxy networks.
func SetTrusted(nets []*net.IPNet) { trusted.Store(&nets) }

func isTrusted(ip net.IP) bool {
	p := trusted.Load()
	if p == nil || ip == nil {
		return false
	}
	for _, n := range *p {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// FromRequest returns the client address: the TCP peer, unless the peer is a
// trusted proxy, in which case the right-most X-Forwarded-For hop that is not
// itself a trusted proxy.
func FromRequest(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !isTrusted(net.ParseIP(host)) {
		return host
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		h := strings.TrimSpace(hops[i])
		ip := net.ParseIP(h)
		if ip == nil {
			return host // malformed header: do not trust any of it
		}
		if !isTrusted(ip) {
			return ip.String()
		}
	}
	return host
}

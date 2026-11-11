package mailmgr

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strings"
)

// listenSet is where Nginx's other sites listen, so the mail names' and webmail's files can listen on the same
// addresses. A plain "listen 443" (every address) cannot be started by a reload where the running Nginx already
// holds a specific address on that port ("bind() to 0.0.0.0:443 failed (98: Address already in use)"): the
// new site then never answers, and websites NimVor creates are bound to the server's own address.
type listenSet struct {
	v4, v6       map[string][]string // port → specific addresses
	wild4, wild6 map[string]bool     // port → a wildcard listener exists
	ipv6         bool                // the server has IPv6 at all
}

// listenSet reads the listen lines of every site other than the ones this package writes.
func (e env) listenSet(ctx context.Context) listenSet {
	ls := listenSet{v4: map[string][]string{}, v6: map[string][]string{}, wild4: map[string]bool{}, wild6: map[string]bool{}, ipv6: e.IPv6()}
	ours := map[string]bool{"nimvor-webmail.conf": true, "nimvor-mail-names.conf": true}
	for _, v := range e.Discovery.Nginx(ctx).VHosts {
		if ours[filepath.Base(v.File)] {
			continue
		}
		for _, l := range v.Listen {
			ls.add(l)
		}
	}
	return ls
}

// add records one "listen" argument list ("161.97.165.17:443 ssl http2", "[::]:80 default_server", "443").
func (ls *listenSet) add(arg string) {
	f := strings.Fields(arg)
	if len(f) == 0 || strings.HasPrefix(f[0], "unix:") {
		return
	}
	host, port := "", f[0]
	if strings.ContainsAny(f[0], ":") {
		h, p, err := net.SplitHostPort(f[0])
		if err != nil {
			return
		}
		host, port = h, p
	}
	switch {
	case host == "" || host == "*" || host == "0.0.0.0":
		ls.wild4[port] = true
	case host == "::":
		ls.wild6[port] = true
	case strings.Contains(host, ":"):
		ls.v6[port] = appendOnce(ls.v6[port], host)
	default:
		ls.v4[port] = appendOnce(ls.v4[port], host)
	}
}

func appendOnce(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// lines writes the listen lines of a block for port (params: "ssl", "ssl http2" or ""): the addresses the other
// sites hold when there is no wildcard listener on that port, every address otherwise (or when nothing else
// listens there).
func (ls listenSet) lines(port, params string) string {
	var b strings.Builder
	suffix := ""
	if params != "" {
		suffix = " " + params
	}
	if a := ls.v4[port]; len(a) > 0 && !ls.wild4[port] {
		for _, x := range a {
			fmt.Fprintf(&b, "    listen %s:%s%s;\n", x, port, suffix)
		}
	} else {
		fmt.Fprintf(&b, "    listen %s%s;\n", port, suffix)
	}
	if a := ls.v6[port]; len(a) > 0 && !ls.wild6[port] {
		for _, x := range a {
			fmt.Fprintf(&b, "    listen [%s]:%s%s;\n", x, port, suffix)
		}
	} else if ls.ipv6 {
		fmt.Fprintf(&b, "    listen [::]:%s%s;\n", port, suffix)
	}
	return b.String()
}

// dial is the address of this machine's HTTPS listener, for the check that webmail answers: the server's own
// address when that is all Nginx listens on, the loopback address otherwise.
func (ls listenSet) dial() string {
	if a := ls.v4["443"]; len(a) > 0 && !ls.wild4["443"] {
		return net.JoinHostPort(a[0], "443")
	}
	return "127.0.0.1:443"
}

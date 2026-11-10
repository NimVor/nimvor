package sites

import (
	"fmt"
	"regexp"
	"strings"
)

// NginxTuning is an administrator's per-website Nginx tuning. Every field is
// optional (zero = the server-wide value applies). The structured fields are
// rendered as fixed directives; Extra lines are limited to an allowlist of
// harmless directives so a website can never change what it serves, where
// from, or how it is authenticated.
type NginxTuning struct {
	ReadTimeoutSeconds int      `json:"read_timeout_seconds,omitempty"` // fastcgi/proxy read timeout, 10-3600
	SendTimeoutSeconds int      `json:"send_timeout_seconds,omitempty"` // send_timeout, 10-3600
	BodyTimeoutSeconds int      `json:"body_timeout_seconds,omitempty"` // client_body_timeout, 10-3600
	KeepaliveSeconds   int      `json:"keepalive_seconds,omitempty"`    // keepalive_timeout, 1-600
	FastcgiBufferKB    int      `json:"fastcgi_buffer_kb,omitempty"`    // PHP sites: fastcgi_buffer_size, 4-256
	Gzip               *bool    `json:"gzip,omitempty"`                 // nil = server-wide
	GzipLevel          int      `json:"gzip_level,omitempty"`           // 1-9 (only with gzip on)
	RateLimitKBps      int      `json:"rate_limit_kbps,omitempty"`      // limit_rate per connection, 0 = none
	Extra              []string `json:"extra,omitempty"`                // allowlisted directives, e.g. "client_body_buffer_size 256k"
}

const maxExtraLines = 20

// extraAllowed are the directives a custom line may use, mapped to the
// structured field that already renders them (a duplicate would make Nginx
// refuse the file), or "" when free.
var extraAllowed = map[string]string{
	"client_body_buffer_size": "", "client_header_buffer_size": "", "large_client_header_buffers": "",
	"client_header_timeout": "", "keepalive_requests": "", "reset_timedout_connection": "",
	"tcp_nodelay": "", "tcp_nopush": "", "limit_rate_after": "",
	"fastcgi_connect_timeout": "", "fastcgi_send_timeout": "", "fastcgi_buffers": "", "fastcgi_busy_buffers_size": "",
	"proxy_connect_timeout": "", "proxy_send_timeout": "", "proxy_buffering": "", "proxy_buffer_size": "",
	"proxy_buffers": "", "proxy_busy_buffers_size": "",
	"gzip_min_length": "", "gzip_vary": "", "gzip_proxied": "", "gzip_types": "",
	"send_timeout": "send_timeout_seconds", "client_body_timeout": "body_timeout_seconds",
	"keepalive_timeout": "keepalive_seconds", "fastcgi_buffer_size": "fastcgi_buffer_kb",
	"gzip_comp_level": "gzip_level", "limit_rate": "rate_limit_kbps", "gzip": "gzip",
}

// A custom value: words of letters, digits and . _ : / * + - only, so no
// quote, $, brace, semicolon, backslash or newline can end the directive.
var extraValueRe = regexp.MustCompile(`^[A-Za-z0-9._:/*+-]+( [A-Za-z0-9._:/*+-]+){0,9}$`)

func (t *NginxTuning) normalize(kind string) error {
	rng := func(field string, v, lo, hi int) error {
		if v != 0 && (v < lo || v > hi) {
			return fail("nginx."+field, "must be between %d and %d", lo, hi)
		}
		return nil
	}
	for _, c := range []struct {
		f      string
		v      int
		lo, hi int
	}{
		{"read_timeout_seconds", t.ReadTimeoutSeconds, 10, 3600},
		{"send_timeout_seconds", t.SendTimeoutSeconds, 10, 3600},
		{"body_timeout_seconds", t.BodyTimeoutSeconds, 10, 3600},
		{"keepalive_seconds", t.KeepaliveSeconds, 1, 600},
		{"fastcgi_buffer_kb", t.FastcgiBufferKB, 4, 256},
		{"gzip_level", t.GzipLevel, 1, 9},
		{"rate_limit_kbps", t.RateLimitKBps, 1, 1048576},
	} {
		if err := rng(c.f, c.v, c.lo, c.hi); err != nil {
			return err
		}
	}
	if t.FastcgiBufferKB != 0 && kind != "php" {
		return fail("nginx.fastcgi_buffer_kb", "only applies to PHP websites")
	}
	if t.GzipLevel != 0 && t.Gzip != nil && !*t.Gzip {
		return fail("nginx.gzip_level", "needs compression to be on")
	}
	if len(t.Extra) > maxExtraLines {
		return fail("nginx.extra", "at most %d custom directives", maxExtraLines)
	}
	seen := map[string]bool{}
	for i, line := range t.Extra {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ";"))
		t.Extra[i] = line
		name, value, ok := strings.Cut(line, " ")
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			return fail("nginx.extra", "%q needs a value", line)
		}
		owner, allowed := extraAllowed[name]
		if !allowed {
			return fail("nginx.extra", "%q is not an allowed directive", name)
		}
		if owner != "" && t.structured(owner) {
			return fail("nginx.extra", "%q is already set by its own field", name)
		}
		if seen[name] {
			return fail("nginx.extra", "%q is given twice", name)
		}
		seen[name] = true
		if !extraValueRe.MatchString(value) {
			return fail("nginx.extra", "%q has a value with characters that are not allowed", name)
		}
	}
	return nil
}

// structured reports whether the named structured field is set.
func (t *NginxTuning) structured(field string) bool {
	switch field {
	case "send_timeout_seconds":
		return t.SendTimeoutSeconds > 0
	case "body_timeout_seconds":
		return t.BodyTimeoutSeconds > 0
	case "keepalive_seconds":
		return t.KeepaliveSeconds > 0
	case "fastcgi_buffer_kb":
		return t.FastcgiBufferKB > 0
	case "gzip_level":
		return t.GzipLevel > 0
	case "rate_limit_kbps":
		return t.RateLimitKBps > 0
	case "gzip":
		return t.Gzip != nil
	}
	return false
}

// serverLines renders the tuning for the server block.
func (t *NginxTuning) serverLines() string {
	if t == nil {
		return ""
	}
	var b strings.Builder
	if t.SendTimeoutSeconds > 0 {
		fmt.Fprintf(&b, "    send_timeout %ds;\n", t.SendTimeoutSeconds)
	}
	if t.BodyTimeoutSeconds > 0 {
		fmt.Fprintf(&b, "    client_body_timeout %ds;\n", t.BodyTimeoutSeconds)
	}
	if t.KeepaliveSeconds > 0 {
		fmt.Fprintf(&b, "    keepalive_timeout %ds;\n", t.KeepaliveSeconds)
	}
	if t.FastcgiBufferKB > 0 {
		fmt.Fprintf(&b, "    fastcgi_buffer_size %dk;\n", t.FastcgiBufferKB)
	}
	if t.Gzip != nil {
		if *t.Gzip {
			b.WriteString("    gzip on;\n")
			if t.GzipLevel > 0 {
				fmt.Fprintf(&b, "    gzip_comp_level %d;\n", t.GzipLevel)
			}
		} else {
			b.WriteString("    gzip off;\n")
		}
	} else if t.GzipLevel > 0 {
		fmt.Fprintf(&b, "    gzip_comp_level %d;\n", t.GzipLevel)
	}
	if t.RateLimitKBps > 0 {
		fmt.Fprintf(&b, "    limit_rate %dk;\n", t.RateLimitKBps)
	}
	for _, line := range t.Extra {
		b.WriteString("    " + line + ";\n")
	}
	return b.String()
}

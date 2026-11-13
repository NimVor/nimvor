package webprotect

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Traffic limits: requests per time from one address (optionally one
// address with one browser) to a website, or to a URL of it, above which
// Nginx answers with an error instead of serving the page. Rendered with
// Nginx's own limit_req module, no extra software.
//
// A limit is a rule of the website: one zone per rule, keyed by a map that
// is only non-empty for that website's matching URLs (an empty key is not
// counted), so rules for different URLs and different websites do not
// interfere. The zones and maps live in one http-level file; the website's
// own file only switches the rules on.

const (
	// MaxLimits is the most rules of one website.
	MaxLimits = 20
	// LimitFile is the http-level file's name (loaded before the websites').
	LimitFile = "nimvor-00-protect.conf"
)

// Limit is one traffic rule.
type Limit struct {
	Name string `json:"name"`
	// Match: "prefix" (the path and everything below it) or "exact".
	Match string `json:"match"`
	Path  string `json:"path"`
	// Rate requests per Unit ("s" second, "m" minute) from one address.
	Rate int    `json:"rate"`
	Unit string `json:"unit"`
	// Burst: extra requests allowed at once before the limit answers.
	Burst int `json:"burst"`
	// PerUA counts an address and its browser identification together.
	PerUA   bool `json:"per_ua"`
	Enabled bool `json:"enabled"`
}

// LimitStatuses are the answers a limit can give: 429 Too Many Requests,
// 503 Service Unavailable, 444 (Nginx closes the connection).
var LimitStatuses = []int{429, 503, 444}

var (
	limitNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	limitPathRe = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,198}$`)
)

// Validate checks a rule.
func (l Limit) Validate() error {
	if !limitNameRe.MatchString(l.Name) {
		return errors.New("name: 1 to 40 lowercase letters, digits, dashes or underscores")
	}
	if l.Match != "prefix" && l.Match != "exact" {
		return errors.New("match: prefix or exact")
	}
	if !limitPathRe.MatchString(l.Path) || strings.Contains(l.Path, "..") || strings.Contains(l.Path, "//") {
		return errors.New("path: a URL path such as / or /wp-login.php (letters, digits and . _ ~ - / only)")
	}
	switch l.Unit {
	case "s":
		if l.Rate < 1 || l.Rate > 1000 {
			return errors.New("rate: 1 to 1000 requests per second")
		}
	case "m":
		if l.Rate < 1 || l.Rate > 60000 {
			return errors.New("rate: 1 to 60000 requests per minute")
		}
	default:
		return errors.New("unit: s (second) or m (minute)")
	}
	if l.Burst < 0 || l.Burst > 1000 {
		return errors.New("burst: 0 to 1000")
	}
	return nil
}

// ValidLimitStatus reports whether code is one of LimitStatuses.
func ValidLimitStatus(code int) bool {
	for _, s := range LimitStatuses {
		if s == code {
			return true
		}
	}
	return false
}

// limitID names a rule's zone and map (stable, unique across websites).
func limitID(domain, name string) string {
	h := sha1.Sum([]byte(domain + "\x00" + name))
	return hex.EncodeToString(h[:])[:10]
}

func (l Limit) pathRegex() string {
	re := quoteRe(l.Path)
	if l.Match == "exact" {
		return re + "$"
	}
	return re
}

// renderLimitsHTTP is the http-level part of every website's rules.
func renderLimitsHTTP(st State) string {
	domains := make([]string, 0, len(st.Sites))
	for d, s := range st.Sites {
		if s != nil && hasEnabledLimit(s) {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		return ""
	}
	sort.Strings(domains)
	var b strings.Builder
	b.WriteString("# Managed by NimVor (website traffic limits). Do not edit.\n")
	for _, d := range domains {
		for _, l := range st.Sites[d].Limits {
			if !l.Enabled {
				continue
			}
			id := limitID(d, l.Name)
			key := "$binary_remote_addr"
			if l.PerUA {
				key = "$binary_remote_addr$http_user_agent"
			}
			fmt.Fprintf(&b, "# %s: %s\nmap \"$server_name$uri\" $nvl_%s {\n    default \"\";\n", d, l.Name, id)
			// Certificates keep renewing whatever the limit.
			fmt.Fprintf(&b, "    \"~^%s/\\.well-known/acme-challenge/\" \"\";\n", quoteRe(d))
			fmt.Fprintf(&b, "    \"~^%s%s\" \"%s\";\n}\n", quoteRe(d), l.pathRegex(), key)
			fmt.Fprintf(&b, "limit_req_zone $nvl_%s zone=nvl_%s:5m rate=%dr/%s;\n", id, id, l.Rate, l.Unit)
		}
	}
	return b.String()
}

func hasEnabledLimit(s *Site) bool {
	for _, l := range s.Limits {
		if l.Enabled {
			return true
		}
	}
	return false
}

// renderLimitsServer switches a website's rules on (server level).
func renderLimitsServer(domain string, s *Site) string {
	if !hasEnabledLimit(s) {
		return ""
	}
	status := s.LimitStatus
	if !ValidLimitStatus(status) {
		status = 429
	}
	var b strings.Builder
	fmt.Fprintf(&b, "    # Traffic limits\n    limit_req_status %d;\n    limit_req_log_level warn;\n", status)
	for _, l := range s.Limits {
		if !l.Enabled {
			continue
		}
		// Nginx rejects "burst=0": without a burst the parameter is left out.
		burst := ""
		if l.Burst > 0 {
			burst = fmt.Sprintf(" burst=%d", l.Burst)
		}
		fmt.Fprintf(&b, "    limit_req zone=nvl_%s%s nodelay;\n", limitID(domain, l.Name), burst)
	}
	return b.String()
}

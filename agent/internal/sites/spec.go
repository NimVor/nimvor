// Package sites manages WhaCP-owned websites: Nginx server blocks, per-site
// PHP-FPM pools, site directories and site system users.
//
// WhaCP only ever writes files named whacp-<domain>.conf; configuration
// created by hand or by another panel is never modified. A site cannot be
// created for a hostname that an unmanaged configuration already serves.
package sites

import (
	"fmt"
	"net/netip"
	"path/filepath"
	"regexp"

	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// PHPSettings are per-site PHP-FPM pool settings.
type PHPSettings struct {
	MemoryLimitMB  int  `json:"memory_limit_mb"`
	UploadMaxMB    int  `json:"upload_max_mb"`
	MaxExecSeconds int  `json:"max_execution_seconds"`
	MaxChildren    int  `json:"max_children"`
	Harden         bool `json:"harden"`
	// Profile selects the disable_functions set: "recommended", "balanced",
	// "compatibility" or "custom". Empty keeps the legacy Harden behaviour so
	// existing websites are unchanged. With "custom", DisableFunctions is the
	// explicit comma-separated list.
	Profile          string `json:"profile"`
	DisableFunctions string `json:"disable_functions"`
	// PHP-FPM process manager: ondemand (default), dynamic or static.
	PM string `json:"pm"`
	// dynamic: processes at start and idle ones kept (0: derived from
	// max_children).
	StartServers int `json:"start_servers"`
	MinSpare     int `json:"min_spare_servers"`
	MaxSpare     int `json:"max_spare_servers"`
	// ondemand: seconds an idle process is kept.
	IdleTimeoutSec int `json:"idle_timeout_seconds"`
	// Requests a process serves before it is replaced.
	MaxRequests int `json:"max_requests"`
	// Hard limit per request in seconds, killing the process (0: none).
	RequestTimeoutSec int `json:"request_timeout_seconds"`
	// Requests slower than this are logged with a backtrace (php-slow.log).
	SlowlogSec int `json:"slowlog_seconds"`
	// INI: php.ini directives of the website (see PHPDirectives).
	INI map[string]string `json:"ini,omitempty"`
}

// Redirect is a path-level redirect.
type Redirect struct {
	From string `json:"from"`
	To   string `json:"to"`
	Code int    `json:"code"`
}

// Spec is the complete desired state of a website.
type Spec struct {
	Domain     string   `json:"domain"`
	Aliases    []string `json:"aliases"`
	User       string   `json:"user"`
	CreateUser bool     `json:"create_user"`
	// NewUser: the account must not exist yet (a hosting customer's new
	// website): an existing account, e.g. another customer's or a server
	// login, is refused instead of being used.
	NewUser      bool        `json:"new_user,omitempty"`
	Root         string      `json:"root"`
	DocumentRoot string      `json:"document_root"`
	Kind         string      `json:"kind"`
	PHPVersion   string      `json:"php_version"`
	PHP          PHPSettings `json:"php"`
	ProxyPass    string      `json:"proxy_pass"`
	HTTPS        bool        `json:"https"`
	ForceHTTPS   bool        `json:"force_https"`
	HSTS         bool        `json:"hsts"`
	// SecurityHeaders adds Referrer-Policy, X-Frame-Options and
	// Permissions-Policy (and upgrade-insecure-requests over HTTPS).
	SecurityHeaders bool       `json:"security_headers,omitempty"`
	CertFile        string     `json:"cert_file"`
	KeyFile         string     `json:"key_file"`
	Enabled         bool       `json:"enabled"`
	Redirects       []Redirect `json:"redirects"`
	ClientMaxBodyMB int        `json:"client_max_body_mb"`
	// Autoindex lists the files of folders without an index page (nginx
	// autoindex); off by default.
	Autoindex bool `json:"autoindex,omitempty"`
	// IPv4 and IPv6: the addresses the website listens on ("" = every
	// address of the server).
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
	// BandwidthExceeded: the account has used its plan's monthly data
	// transfer; every request is answered with 509 (certificate renewals
	// still work) until the panel clears it.
	BandwidthExceeded bool `json:"bandwidth_exceeded,omitempty"`
}

var (
	proxyRe    = regexp.MustCompile(`^http://(127\.0\.0\.1|\[::1\]|localhost):[0-9]{2,5}(/[A-Za-z0-9._~/-]*)?$`)
	fromRe     = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,200}$`)
	toRe       = regexp.MustCompile(`^(https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?)?(/[A-Za-z0-9._~/%?=&+-]{0,300})?$`)
	certPrefix = []string{"/etc/letsencrypt/live/"}
)

func fail(field, msg string, a ...any) error {
	return &validate.Error{Field: field, Message: fmt.Sprintf(msg, a...)}
}

// Normalize fills defaults and validates the spec against the agent's
// sites root and state directory.
func (s *Spec) Normalize(sitesRoot, stateDir string) error {
	if err := validate.Domain("domain", s.Domain); err != nil {
		return err
	}
	if len(s.Aliases) > 50 {
		return fail("aliases", "at most 50 aliases are allowed")
	}
	if err := validate.Domains("aliases", append([]string{s.Domain}, s.Aliases...)); err != nil {
		return err
	}
	if err := validate.Username("user", s.User); err != nil {
		return err
	}
	if s.IPv4 != "" {
		a, err := netip.ParseAddr(s.IPv4)
		if err != nil || !a.Is4() || a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() {
			return fail("ipv4", "must be an IPv4 address of this server")
		}
		s.IPv4 = a.String()
	}
	if s.IPv6 != "" {
		a, err := netip.ParseAddr(s.IPv6)
		if err != nil || !a.Is6() || a.Is4In6() || a.Zone() != "" || a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() || a.IsLinkLocalUnicast() {
			return fail("ipv6", "must be a global IPv6 address of this server")
		}
		s.IPv6 = a.String()
	}
	if s.Root == "" {
		s.Root = filepath.Join(sitesRoot, s.Domain)
	}
	if err := validate.Within("root", sitesRoot, s.Root); err != nil {
		return err
	}
	if filepath.Clean(s.Root) == filepath.Clean(sitesRoot) {
		return fail("root", "must be a directory inside %s", sitesRoot)
	}
	if s.DocumentRoot == "" {
		s.DocumentRoot = filepath.Join(s.Root, "public_html")
	}
	if err := validate.Within("document_root", s.Root, s.DocumentRoot); err != nil {
		return err
	}
	switch s.Kind {
	case "php":
		if err := validate.PHPVersion("php_version", s.PHPVersion); err != nil {
			return err
		}
	case "static":
		s.PHPVersion = ""
	case "proxy":
		s.PHPVersion = ""
		if !proxyRe.MatchString(s.ProxyPass) {
			return fail("proxy_pass", "must be a local address such as http://127.0.0.1:3000")
		}
	default:
		return fail("kind", "must be php, static or proxy")
	}
	if s.Kind != "proxy" {
		s.ProxyPass = ""
	}
	p := &s.PHP
	ini, err := NormalizeINI(p.INI, s.Root)
	if err != nil {
		return err
	}
	p.INI = ini
	// PHP security profile (items 33–35). Empty keeps the legacy Harden
	// behaviour for existing websites; a named profile sets disable_functions.
	if p.Profile != "" && !validProfiles[p.Profile] {
		return fail("php.profile", "must be recommended, balanced, compatibility or custom")
	}
	if p.Profile == ProfileCustom {
		p.DisableFunctions = normalizeFuncList(p.DisableFunctions)
	} else {
		p.DisableFunctions = ""
	}
	defaults := map[*int]int{&p.MemoryLimitMB: 256, &p.UploadMaxMB: 64, &p.MaxExecSeconds: 60, &p.MaxChildren: 5, &s.ClientMaxBodyMB: 0}
	for ptr, def := range defaults {
		if *ptr == 0 {
			*ptr = def
		}
	}
	if s.ClientMaxBodyMB == 0 {
		s.ClientMaxBodyMB = p.UploadMaxMB
	}
	switch p.PM {
	case "":
		p.PM = "ondemand"
	case "ondemand", "dynamic", "static":
	default:
		return fail("php.pm", "must be ondemand, dynamic or static")
	}
	for ptr, def := range map[*int]int{&p.IdleTimeoutSec: 10, &p.MaxRequests: 500, &p.SlowlogSec: 10} {
		if *ptr == 0 {
			*ptr = def
		}
	}
	if p.PM == "dynamic" {
		if p.MinSpare == 0 {
			p.MinSpare = 1
		}
		if p.MaxSpare == 0 {
			p.MaxSpare = min(3, p.MaxChildren)
		}
		if p.StartServers == 0 {
			p.StartServers = min(max(2, p.MinSpare), p.MaxSpare)
		}
		if !(1 <= p.MinSpare && p.MinSpare <= p.StartServers && p.StartServers <= p.MaxSpare && p.MaxSpare <= p.MaxChildren) {
			return fail("php.start_servers", "dynamic needs 1 ≤ min spare ≤ start ≤ max spare ≤ max processes")
		}
	} else {
		p.StartServers, p.MinSpare, p.MaxSpare = 0, 0, 0
	}
	limits := []struct {
		field     string
		v, lo, hi int
	}{
		{"php.memory_limit_mb", p.MemoryLimitMB, 32, 8192},
		{"php.upload_max_mb", p.UploadMaxMB, 1, 4096},
		{"php.max_execution_seconds", p.MaxExecSeconds, 5, 3600},
		{"php.max_children", p.MaxChildren, 1, 200},
		{"php.idle_timeout_seconds", p.IdleTimeoutSec, 1, 3600},
		{"php.max_requests", p.MaxRequests, 1, 100000},
		{"php.request_timeout_seconds", p.RequestTimeoutSec, 0, 86400},
		{"php.slowlog_seconds", p.SlowlogSec, 1, 3600},
		{"client_max_body_mb", s.ClientMaxBodyMB, 1, 4096},
	}
	for _, l := range limits {
		if l.v < l.lo || l.v > l.hi {
			return fail(l.field, "must be between %d and %d", l.lo, l.hi)
		}
	}
	if s.HTTPS {
		prefixes := append(append([]string{}, certPrefix...), filepath.Join(stateDir, "certs")+"/")
		for field, f := range map[string]string{"cert_file": s.CertFile, "key_file": s.KeyFile} {
			ok := false
			for _, pre := range prefixes {
				if validate.Within(field, filepath.Clean(pre), f) == nil {
					ok = true
				}
			}
			if !ok {
				return fail(field, "must be a certificate managed by certbot or WhaCP")
			}
		}
	} else {
		s.ForceHTTPS, s.HSTS, s.CertFile, s.KeyFile = false, false, "", ""
	}
	if len(s.Redirects) > 100 {
		return fail("redirects", "at most 100 redirects are allowed")
	}
	for i, r := range s.Redirects {
		if !fromRe.MatchString(r.From) {
			return fail(fmt.Sprintf("redirects[%d].from", i), "must be a path starting with /")
		}
		if !toRe.MatchString(r.To) || r.To == "" {
			return fail(fmt.Sprintf("redirects[%d].to", i), "must be a URL or a path")
		}
		switch r.Code {
		case 301, 302, 307, 308:
		case 0:
			s.Redirects[i].Code = 301
		default:
			return fail(fmt.Sprintf("redirects[%d].code", i), "must be 301, 302, 307 or 308")
		}
	}
	return nil
}

// Names returns the primary domain followed by aliases.
func (s Spec) Names() []string { return append([]string{s.Domain}, s.Aliases...) }

// Package sites manages WhaCP-owned websites: Nginx server blocks, per-site
// PHP-FPM pools, site directories and site system users.
//
// WhaCP only ever writes files named whacp-<domain>.conf; configuration
// created by hand or by another panel is never modified. A site cannot be
// created for a hostname that an unmanaged configuration already serves.
package sites

import (
	"fmt"
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
}

// Redirect is a path-level redirect.
type Redirect struct {
	From string `json:"from"`
	To   string `json:"to"`
	Code int    `json:"code"`
}

// Spec is the complete desired state of a website.
type Spec struct {
	Domain          string      `json:"domain"`
	Aliases         []string    `json:"aliases"`
	User            string      `json:"user"`
	CreateUser      bool        `json:"create_user"`
	Root            string      `json:"root"`
	DocumentRoot    string      `json:"document_root"`
	Kind            string      `json:"kind"`
	PHPVersion      string      `json:"php_version"`
	PHP             PHPSettings `json:"php"`
	ProxyPass       string      `json:"proxy_pass"`
	HTTPS           bool        `json:"https"`
	ForceHTTPS      bool        `json:"force_https"`
	HSTS            bool        `json:"hsts"`
	CertFile        string      `json:"cert_file"`
	KeyFile         string      `json:"key_file"`
	Enabled         bool        `json:"enabled"`
	Redirects       []Redirect  `json:"redirects"`
	ClientMaxBodyMB int         `json:"client_max_body_mb"`
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
	defaults := map[*int]int{&p.MemoryLimitMB: 256, &p.UploadMaxMB: 64, &p.MaxExecSeconds: 60, &p.MaxChildren: 5, &s.ClientMaxBodyMB: 0}
	for ptr, def := range defaults {
		if *ptr == 0 {
			*ptr = def
		}
	}
	if s.ClientMaxBodyMB == 0 {
		s.ClientMaxBodyMB = p.UploadMaxMB
	}
	limits := []struct {
		field     string
		v, lo, hi int
	}{
		{"php.memory_limit_mb", p.MemoryLimitMB, 32, 8192},
		{"php.upload_max_mb", p.UploadMaxMB, 1, 4096},
		{"php.max_execution_seconds", p.MaxExecSeconds, 5, 3600},
		{"php.max_children", p.MaxChildren, 1, 200},
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

package sites

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A website's customer-set security headers and cache rules. Everything is a
// choice from a fixed set or a value checked against a strict grammar, and is
// rendered by NimVor's own templates: customers never write nginx directives.
// Server-level rules (nosniff, dotfile and PHP restrictions, request limits,
// ModSecurity, protection includes) are emitted by RenderNginx as before and
// are neither repeated nor replaced here.

// HSTSPolicy is the Strict-Transport-Security header (HTTPS websites only).
type HSTSPolicy struct {
	Enabled           bool `json:"enabled"`
	MaxAge            int  `json:"max_age,omitempty"` // seconds
	IncludeSubdomains bool `json:"include_subdomains,omitempty"`
	Preload           bool `json:"preload,omitempty"`
}

// HeaderPolicy replaces the legacy "recommended security headers" bundle when
// a website has one, so no header is ever sent twice.
type HeaderPolicy struct {
	// Clickjacking: off, sameorigin, deny or allow (frame-ancestors list).
	Clickjacking   string   `json:"clickjacking"`
	FrameAncestors []string `json:"frame_ancestors,omitempty"`
	// CSP is the policy text (without frame-ancestors, which Clickjacking
	// owns); CSPReportOnly sends it as Content-Security-Policy-Report-Only.
	CSP           string `json:"csp,omitempty"`
	CSPReportOnly bool   `json:"csp_report_only,omitempty"`
	// Referrer is a Referrer-Policy value, or "off" to send none.
	Referrer string `json:"referrer"`
	// Permissions: off, restrictive, strict or custom.
	Permissions       string     `json:"permissions"`
	PermissionsCustom string     `json:"permissions_custom,omitempty"`
	HSTS              HSTSPolicy `json:"hsts"`
}

// CachePolicy is the browser-cache behaviour of a website.
type CachePolicy struct {
	// StaticTTL is the browser-cache lifetime of static assets in seconds
	// (0: leave it to the application).
	StaticTTL int `json:"static_ttl"`
	// DynamicMode: app (leave it), revalidate, no-store or public.
	DynamicMode string `json:"dynamic_mode"`
	DynamicTTL  int    `json:"dynamic_ttl,omitempty"` // seconds, public mode
	// BypassPaths are extra path prefixes that are never cached publicly.
	BypassPaths []string `json:"bypass_paths,omitempty"`
}

// Bounds of the customer-set values.
const (
	MinStaticTTL  = 60
	MaxStaticTTL  = 31536000
	MinDynamicTTL = 30
	MaxDynamicTTL = 3600
	MinHSTSAge    = 300
	MaxHSTSAge    = 63072000
	maxCSPLen     = 4000
	maxBypass     = 20
)

var (
	refererValues = map[string]bool{"no-referrer": true, "no-referrer-when-downgrade": true, "same-origin": true, "origin": true, "strict-origin": true, "origin-when-cross-origin": true, "strict-origin-when-cross-origin": true}

	cspDirectives = map[string]bool{
		"default-src": true, "script-src": true, "script-src-elem": true, "script-src-attr": true, "style-src": true, "style-src-elem": true, "style-src-attr": true,
		"img-src": true, "font-src": true, "connect-src": true, "media-src": true, "object-src": true, "frame-src": true, "child-src": true, "worker-src": true,
		"manifest-src": true, "base-uri": true, "form-action": true, "prefetch-src": true,
	}
	cspKeyword = regexp.MustCompile(`^'(?:self|none|unsafe-inline|unsafe-eval|strict-dynamic|report-sample|unsafe-hashes|wasm-unsafe-eval)'$`)
	cspHash    = regexp.MustCompile(`^'(?:sha256|sha384|sha512)-[A-Za-z0-9+/=_-]{20,200}'$`)
	cspScheme  = regexp.MustCompile(`^(?:https|http|data|blob|wss|ws):$`)
	cspHost    = regexp.MustCompile(`^(?:(?:https?|wss?)://)?(?:\*\.)?[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*(?::(?:[0-9]{1,5}|\*))?(?:/[A-Za-z0-9._~/%-]*)?$`)
	cspReport  = regexp.MustCompile(`^(?:https?://[A-Za-z0-9.-]+(?::[0-9]{1,5})?)?/[A-Za-z0-9._~/%?=&+-]{0,300}$`)
	originRe   = regexp.MustCompile(`^https://(?:\*\.)?[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+(?::[0-9]{1,5})?$`)
	bypassRe   = regexp.MustCompile(`^/[A-Za-z0-9._~-]+(?:/[A-Za-z0-9._~-]+)*$`)

	permFeatures = map[string]bool{
		"accelerometer": true, "ambient-light-sensor": true, "autoplay": true, "battery": true, "bluetooth": true, "browsing-topics": true, "camera": true,
		"clipboard-read": true, "clipboard-write": true, "display-capture": true, "document-domain": true, "encrypted-media": true, "fullscreen": true,
		"gamepad": true, "geolocation": true, "gyroscope": true, "hid": true, "idle-detection": true, "interest-cohort": true, "magnetometer": true,
		"microphone": true, "midi": true, "payment": true, "picture-in-picture": true, "publickey-credentials-get": true, "screen-wake-lock": true,
		"serial": true, "usb": true, "web-share": true, "xr-spatial-tracking": true,
	}
	permOrigin = `"https://[A-Za-z0-9.-]+(?::[0-9]{1,5})?"`
	permList   = regexp.MustCompile(`^(?:\*|\(\)|\((?:self|` + permOrigin + `)(?: (?:self|` + permOrigin + `))*\))$`)
	permName   = regexp.MustCompile(`^[a-z][a-z-]{1,40}$`)
)

// Permissions-Policy presets.
const (
	permsRestrictive = "camera=(), microphone=(), geolocation=(), payment=(), usb=()"
	permsStrict      = "accelerometer=(), bluetooth=(), browsing-topics=(), camera=(), display-capture=(), geolocation=(), gyroscope=(), hid=(), interest-cohort=(), magnetometer=(), microphone=(), midi=(), payment=(), serial=(), usb=()"
)

// normalizeCSP checks a Content-Security-Policy and returns it in canonical
// form. frame-ancestors is refused: Clickjacking protection owns it, so the
// two can never disagree.
func normalizeCSP(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	if len(text) > maxCSPLen {
		return "", fmt.Errorf("the policy is longer than %d characters", maxCSPLen)
	}
	if strings.ContainsAny(text, "\"\\$`{}<>") || strings.ContainsFunc(text, func(r rune) bool { return r < ' ' && r != '\n' && r != '\r' && r != '\t' || r == 0x7f }) {
		return "", fmt.Errorf("the policy contains a character that is not allowed")
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(text), ";") {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		vals := fields[1:]
		if name == "frame-ancestors" {
			return "", fmt.Errorf("frame-ancestors is set by Clickjacking protection; remove it here")
		}
		if seen[name] {
			return "", fmt.Errorf("%s appears twice", name)
		}
		seen[name] = true
		switch {
		case name == "upgrade-insecure-requests" || name == "block-all-mixed-content":
			if len(vals) != 0 {
				return "", fmt.Errorf("%s takes no value", name)
			}
		case name == "report-uri":
			if len(vals) != 1 || !cspReport.MatchString(vals[0]) {
				return "", fmt.Errorf("report-uri needs one URL or path")
			}
		case cspDirectives[name]:
			if len(vals) == 0 {
				return "", fmt.Errorf("%s needs at least one source (for example 'self')", name)
			}
			if len(vals) > 40 {
				return "", fmt.Errorf("%s has too many sources", name)
			}
			for _, v := range vals {
				if v == "*" || cspKeyword.MatchString(v) || cspHash.MatchString(v) || cspScheme.MatchString(v) || (cspHost.MatchString(v) && !strings.HasPrefix(v, "'")) {
					continue
				}
				return "", fmt.Errorf("“%s” in %s is not a valid source", v, name)
			}
			if len(vals) > 1 {
				for _, v := range vals {
					if v == "'none'" {
						return "", fmt.Errorf("'none' cannot be combined with other sources in %s", name)
					}
				}
			}
		default:
			return "", fmt.Errorf("%s is not a supported directive", name)
		}
		out = append(out, strings.Join(append([]string{name}, vals...), " "))
	}
	if len(out) == 0 {
		return "", nil
	}
	return strings.Join(out, "; "), nil
}

// normalizePermissions checks custom Permissions-Policy directives and
// returns them canonical (sorted, one entry per feature).
func normalizePermissions(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("enter at least one directive, for example camera=(), geolocation=(self)")
	}
	if len(text) > 1500 {
		return "", fmt.Errorf("the policy is too long")
	}
	seen := map[string]string{}
	for _, entry := range strings.Split(text, ",") {
		entry = strings.TrimSpace(entry)
		name, list, ok := strings.Cut(entry, "=")
		name, list = strings.TrimSpace(name), strings.TrimSpace(list)
		if !ok || !permName.MatchString(name) {
			return "", fmt.Errorf("“%s” is not feature=(allowlist)", entry)
		}
		if !permFeatures[name] {
			return "", fmt.Errorf("%s is not a supported feature", name)
		}
		if list == "self" {
			list = "(self)"
		}
		if !permList.MatchString(list) {
			return "", fmt.Errorf("the allowlist of %s must be (), *, (self) or (self \"https://example.com\")", name)
		}
		if _, dup := seen[name]; dup {
			return "", fmt.Errorf("%s appears twice", name)
		}
		seen[name] = list
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = n + "=" + seen[n]
	}
	return strings.Join(parts, ", "), nil
}

// normalize validates and completes the header policy.
func (h *HeaderPolicy) normalize(https bool) error {
	switch h.Clickjacking {
	case "":
		h.Clickjacking = "sameorigin"
	case "off", "sameorigin", "deny":
		h.FrameAncestors = nil
	case "allow":
		if len(h.FrameAncestors) == 0 || len(h.FrameAncestors) > 10 {
			return fail("headers.frame_ancestors", "list 1 to 10 sites that may embed this website")
		}
		seen := map[string]bool{}
		for _, o := range h.FrameAncestors {
			if !originRe.MatchString(o) || seen[o] {
				return fail("headers.frame_ancestors", "“%s” must be an https:// origin such as https://example.com, listed once", o)
			}
			seen[o] = true
		}
	default:
		return fail("headers.clickjacking", "choose off, same origin, deny or allow specific sites")
	}
	csp, err := normalizeCSP(h.CSP)
	if err != nil {
		return fail("headers.csp", "%v", err)
	}
	h.CSP = csp
	if csp == "" {
		h.CSPReportOnly = false
	}
	if h.Referrer == "" {
		h.Referrer = "strict-origin-when-cross-origin"
	}
	if h.Referrer != "off" && !refererValues[h.Referrer] {
		return fail("headers.referrer", "choose one of the listed Referrer-Policy values")
	}
	switch h.Permissions {
	case "":
		h.Permissions = "restrictive"
		h.PermissionsCustom = ""
	case "off", "restrictive", "strict":
		h.PermissionsCustom = ""
	case "custom":
		p, err := normalizePermissions(h.PermissionsCustom)
		if err != nil {
			return fail("headers.permissions_custom", "%v", err)
		}
		h.PermissionsCustom = p
	default:
		return fail("headers.permissions", "choose off, restrictive, strict or custom")
	}
	if !https {
		h.HSTS = HSTSPolicy{}
		return nil
	}
	if h.HSTS.Enabled {
		if h.HSTS.MaxAge == 0 {
			h.HSTS.MaxAge = 31536000
		}
		if h.HSTS.MaxAge < MinHSTSAge || h.HSTS.MaxAge > MaxHSTSAge {
			return fail("headers.hsts.max_age", "must be between %d and %d seconds", MinHSTSAge, MaxHSTSAge)
		}
		if h.HSTS.Preload && (!h.HSTS.IncludeSubdomains || h.HSTS.MaxAge < 31536000) {
			return fail("headers.hsts.preload", "preload needs include subdomains and a lifetime of at least one year")
		}
	} else {
		h.HSTS = HSTSPolicy{}
	}
	return nil
}

// normalize validates and completes the cache policy.
func (c *CachePolicy) normalize() error {
	if c.StaticTTL != 0 && (c.StaticTTL < MinStaticTTL || c.StaticTTL > MaxStaticTTL) {
		return fail("cache.static_ttl", "must be 0 or between %d and %d seconds", MinStaticTTL, MaxStaticTTL)
	}
	switch c.DynamicMode {
	case "":
		c.DynamicMode = "app"
	case "app", "revalidate", "no-store":
		c.DynamicTTL = 0
	case "public":
		if c.DynamicTTL == 0 {
			c.DynamicTTL = 60
		}
		if c.DynamicTTL < MinDynamicTTL || c.DynamicTTL > MaxDynamicTTL {
			return fail("cache.dynamic_ttl", "must be between %d and %d seconds", MinDynamicTTL, MaxDynamicTTL)
		}
	default:
		return fail("cache.dynamic_mode", "choose app default, revalidate, no-store or public")
	}
	if len(c.BypassPaths) > maxBypass {
		return fail("cache.bypass_paths", "at most %d paths are allowed", maxBypass)
	}
	seen := map[string]bool{}
	paths := make([]string, 0, len(c.BypassPaths))
	for _, p := range c.BypassPaths {
		p = strings.TrimRight(strings.TrimSpace(p), "/")
		if len(p) > 100 || !bypassRe.MatchString(p) || strings.Contains(p, "..") {
			return fail("cache.bypass_paths", "“%s” must be a path such as /members", p)
		}
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	c.BypassPaths = paths
	return nil
}

// ngxString quotes s for an nginx directive. Values reaching it were
// validated and contain no "$" or backslash; quotes are escaped anyway.
func ngxString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (h HeaderPolicy) frameAncestors() string {
	switch h.Clickjacking {
	case "sameorigin":
		return "frame-ancestors 'self'"
	case "deny":
		return "frame-ancestors 'none'"
	case "allow":
		return "frame-ancestors 'self' " + strings.Join(h.FrameAncestors, " ")
	}
	return ""
}

func (h HeaderPolicy) permissions() string {
	switch h.Permissions {
	case "restrictive":
		return permsRestrictive
	case "strict":
		return permsStrict
	case "custom":
		return h.PermissionsCustom
	}
	return ""
}

// hstsValue is the Strict-Transport-Security value ("" when off).
func (h HeaderPolicy) hstsValue() string {
	if !h.HSTS.Enabled {
		return ""
	}
	v := fmt.Sprintf("max-age=%d", h.HSTS.MaxAge)
	if h.HSTS.IncludeSubdomains {
		v += "; includeSubDomains"
	}
	if h.HSTS.Preload {
		v += "; preload"
	}
	return v
}

// headerLines renders the customer's headers, each name once. X-Content-Type-
// Options and HSTS are written by RenderNginx.
func (h HeaderPolicy) headerLines() string {
	var b strings.Builder
	add := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&b, "    add_header %s %s always;\n", name, ngxString(value))
		}
	}
	if h.Clickjacking == "sameorigin" {
		add("X-Frame-Options", "SAMEORIGIN")
	} else if h.Clickjacking == "deny" {
		add("X-Frame-Options", "DENY")
	}
	fa := h.frameAncestors()
	switch {
	case h.CSP != "" && !h.CSPReportOnly && fa != "":
		add("Content-Security-Policy", h.CSP+"; "+fa)
	case h.CSP != "" && !h.CSPReportOnly:
		add("Content-Security-Policy", h.CSP)
	default:
		add("Content-Security-Policy", fa)
	}
	if h.CSP != "" && h.CSPReportOnly {
		add("Content-Security-Policy-Report-Only", h.CSP)
	}
	if h.Referrer != "off" {
		add("Referrer-Policy", h.Referrer)
	}
	add("Permissions-Policy", h.permissions())
	return b.String()
}

// siteVar is a variable name unique to the website.
func siteVar(domain, kind string) string {
	sum := sha1.Sum([]byte(domain))
	return "nimvor_" + kind + "_" + hex.EncodeToString(sum[:])[:10]
}

const staticExt = `css|js|mjs|map|png|jpe?g|gif|webp|avif|svg|ico|woff2?|ttf|otf|eot`

// sensitivePaths are never cached publicly, whatever the website chooses.
const sensitivePaths = `wp-admin|wp-login\.php|wp-json|xmlrpc\.php|administrator|admin|login|logout|signin|signout|register|account|my-account|checkout|cart|user|users|dashboard|api|auth|oauth|password|reset|profile|billing|order|orders`

// sessionCookies mark a request as signed in; matched anywhere in the Cookie
// header, so an unusual cookie name errs on the side of not caching.
const sessionCookies = `phpsessid|sessid|session|logged_in|wordpress_sec|wp-postpass|comment_author|xsrf|csrf|connect\.sid|jsessionid|auth|token|jwt|remember|woocommerce_|edd_|cart|login|prestashop-|ocsessid|joomla|sess[a-f0-9]{6}|_sid`

// RenderCacheFlags holds the request and response flags every website's
// cache rules share (nimvor-00-cache-flags.conf, written once like the proxy
// map). Each flag is 0 or 1 so the per-site maps can combine them safely.
func RenderCacheFlags() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", header)
	b.WriteString("map $http_authorization $nimvor_af {\n    default 1;\n    \"\" 0;\n}\n")
	fmt.Fprintf(&b, "map $http_cookie $nimvor_cf {\n    default 0;\n    \"~*(?:%s)\" 1;\n}\n", sessionCookies)
	b.WriteString("map $upstream_http_set_cookie $nimvor_sf {\n    default 1;\n    \"\" 0;\n}\n")
	b.WriteString("map $upstream_http_cache_control $nimvor_kf {\n    default 1;\n    \"\" 0;\n}\n")
	b.WriteString("map $request_method $nimvor_mf {\n    default 1;\n    GET 0;\n    HEAD 0;\n}\n")
	return b.String()
}

// renderCacheMaps are the per-site maps, written above the server blocks
// (the file is included in the http context).
func renderCacheMaps(s Spec) string {
	c := s.Cache
	if c == nil || (c.StaticTTL == 0 && c.DynamicMode == "app") {
		return ""
	}
	pf := siteVar(s.Domain, "pf")
	var b strings.Builder
	fmt.Fprintf(&b, "map $uri $%s {\n    default 0;\n", pf)
	fmt.Fprintf(&b, "    \"~*^/(?:%s)(?:/|\\.|-|$)\" 1;\n", sensitivePaths)
	if len(c.BypassPaths) > 0 {
		esc := make([]string, len(c.BypassPaths))
		for i, p := range c.BypassPaths {
			esc[i] = strings.NewReplacer(".", `\.`).Replace(p)
		}
		fmt.Fprintf(&b, "    \"~*^(?:%s)(?:/|\\.|$)\" 1;\n", strings.Join(esc, "|"))
	}
	fmt.Fprintf(&b, "    \"~*\\.(?:%s)$\" 2;\n}\n", staticExt)
	if c.StaticTTL > 0 {
		// Static assets of GET/HEAD requests without an Authorization header,
		// outside sensitive paths: key is path flag, auth flag, method flag.
		fmt.Fprintf(&b, "map \"$%s$nimvor_af$nimvor_mf\" $%s {\n    default off;\n    \"200\" %ds;\n}\n", pf, siteVar(s.Domain, "exp"), c.StaticTTL)
	}
	if c.DynamicMode != "app" {
		var value string
		switch c.DynamicMode {
		case "revalidate":
			value = "no-cache"
		case "no-store":
			value = "no-store"
		case "public":
			value = fmt.Sprintf("public, max-age=%d", c.DynamicTTL)
		}
		const private = `"private, no-store"`
		fmt.Fprintf(&b, "map \"$%s$nimvor_af$nimvor_cf$nimvor_sf$nimvor_kf$nimvor_mf\" $%s {\n", pf, siteVar(s.Domain, "cc"))
		fmt.Fprintf(&b, "    default %s;\n", ngxString(value))
		b.WriteString("    \"~^2\" \"\";\n")               // static assets: the static TTL handles them
		fmt.Fprintf(&b, "    \"~^1\" %s;\n", private)      // sensitive path
		fmt.Fprintf(&b, "    \"~^.1\" %s;\n", private)     // Authorization header
		fmt.Fprintf(&b, "    \"~^..1\" %s;\n", private)    // session cookie
		fmt.Fprintf(&b, "    \"~^...1\" %s;\n", private)   // the response sets a cookie
		fmt.Fprintf(&b, "    \"~^.....1\" %s;\n", private) // not GET or HEAD
		b.WriteString("    \"~^....1\" \"\";\n")           // the application sent its own Cache-Control
		b.WriteString("}\n")
	}
	return b.String()
}

// cacheDirectives are the server-level lines using those maps.
func cacheDirectives(s Spec) string {
	c := s.Cache
	if c == nil {
		return ""
	}
	var b strings.Builder
	if c.StaticTTL > 0 {
		fmt.Fprintf(&b, "    expires $%s;\n", siteVar(s.Domain, "exp"))
	}
	if c.DynamicMode != "app" {
		fmt.Fprintf(&b, "    add_header Cache-Control $%s;\n", siteVar(s.Domain, "cc"))
	}
	return b.String()
}

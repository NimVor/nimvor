package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// This file ports app/Services/WebsiteService.php: the website write path —
// creating, updating, toggling, deleting websites and adding subdomains and
// addon domains. It owns form validation (Validate), the agent spec for a
// stored website (Spec) and applying the stored state to the server (apply).

// PhpSettings is one website's PHP-FPM pool settings, stored as JSON in
// websites.php_settings. Keys and defaults mirror WebsiteService::validate.
type PhpSettings struct {
	MemoryLimitMB         int    `json:"memory_limit_mb"`
	UploadMaxMB           int    `json:"upload_max_mb"`
	MaxExecutionSeconds   int    `json:"max_execution_seconds"`
	MaxChildren           int    `json:"max_children"`
	StartServers          int    `json:"start_servers"`
	MinSpareServers       int    `json:"min_spare_servers"`
	MaxSpareServers       int    `json:"max_spare_servers"`
	IdleTimeoutSeconds    int    `json:"idle_timeout_seconds"`
	MaxRequests           int    `json:"max_requests"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
	SlowlogSeconds        int    `json:"slowlog_seconds"`
	Harden                bool   `json:"harden"`
	Profile               string `json:"profile"`
	DisableFunctions      string `json:"disable_functions"`
	PM                    string `json:"pm"`
	INI                   map[string]string `json:"ini,omitempty"`
}

// Redirect is one rewrite rule line ("from to code").
type Redirect struct {
	From string `json:"from"`
	To   string `json:"to"`
	Code int    `json:"code"`
}

// siteKind labels mirror WebsiteService::KINDS / ::APP_KINDS.
var (
	kindLabels       = map[string]string{"php": "PHP application", "static": "Static files", "proxy": "Reverse proxy (Node.js, Python, …)"}
	appKinds         = map[string]string{"nodejs": "Node.js app", "python": "Python app"}
	appRuntimePrefix = map[string]string{"nodejs": "node", "python": "python"}
	kindFeature      = map[string]string{"php": "type_php", "static": "type_static", "proxy": "type_proxy"}
)

// serverPHPSettings are the PHP-FPM settings only server-wide roles may
// change (WebsiteService::SERVER_PHP_SETTINGS); customers and resellers keep
// the stored values for these keys.
var serverPHPSettings = []string{"max_children", "harden", "pm", "start_servers", "min_spare_servers", "max_spare_servers", "idle_timeout_seconds", "max_requests", "request_timeout_seconds", "slowlog_seconds"}

// reservedSystemUsers can never be a website account.
var reservedSystemUsers = map[string]bool{"root": true, "www-data": true, "nobody": true, "mysql": true, "whacp": true, "admin": true}

var (
	hostLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	userRe      = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,30}$`)
	subdomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	proxyPassRe = regexp.MustCompile(`^http://(127\.0\.0\.1|\[::1\]|localhost):[0-9]{2,5}(/[A-Za-z0-9._~/-]*)?$`)
	redirectFromRe = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{0,200}$`)
	redirectToRe   = regexp.MustCompile(`^(https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?)?(/[A-Za-z0-9._~/%?=&+-]{0,300})?$`)
	docrootSubRe   = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
	docrootDotDot  = regexp.MustCompile(`(^|/)\.\.?(/|$)`)
	fnNameRe       = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,63}$`)
	iniNameRe      = regexp.MustCompile(`^[a-z][a-z0-9_.]{0,127}$`)
	portInProxyRe  = regexp.MustCompile(`:(\d+)`)
)

// Auditor records a website action for the audit log. The HTTP layer binds the
// actor, IP and user agent, mirroring AuditLogger::record.
type Auditor func(ctx context.Context, action, targetType, target, result string, details map[string]any)

// WebsiteService manages the website write path. Mirrors WebsiteService.php.
type WebsiteService struct {
	sites    *WebsiteRepository
	agent    AgentCaller
	features *authz.FeatureService
	limits   *PlanLimits
	dns      *DnsRepository
	cfg      *settings.Repository
	audit    Auditor
	serverID int64
}

// NewWebsiteService builds the service. serverID is the active server
// (ServerContext::id() in the PHP panel; single-server in Go).
func NewWebsiteService(sites *WebsiteRepository, agent AgentCaller, features *authz.FeatureService, limits *PlanLimits, dns *DnsRepository, cfg *settings.Repository, audit Auditor, serverID int64) *WebsiteService {
	return &WebsiteService{sites: sites, agent: agent, features: features, limits: limits, dns: dns, cfg: cfg, audit: audit, serverID: serverID}
}

// recordAudit is the nil-safe audit hook.
func (s *WebsiteService) recordAudit(ctx context.Context, action, targetType, target, result string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, targetType, target, result, details)
	}
}

// ---------------------------------------------------------------------------
// Pure helpers (WebsiteService statics)

// NormalizeHost trims dots and whitespace and lowercases a hostname
// (WebsiteService::normalizeHost; IDN punycode is not applied — the panel
// form guidance is ASCII domains).
func NormalizeHost(h string) string {
	h = strings.ToLower(strings.Trim(h, " \t\n\r\x00\x0b."))
	return h
}

// ValidHost reports whether h is a syntactically valid hostname
// (WebsiteService::validHost): 3–253 chars, at least one dot, not an IP
// address, labels of 1–63 letters/digits/hyphens not edged by hyphens.
func ValidHost(h string) bool {
	if len(h) < 3 || len(h) > 253 || !strings.Contains(h, ".") {
		return false
	}
	if net.ParseIP(h) != nil {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if !hostLabelRe.MatchString(label) {
			return false
		}
	}
	return true
}

// SuggestUser derives the default system account from a domain
// (WebsiteService::suggestUser).
func SuggestUser(domain string) string {
	parts := strings.Split(domain, ".")
	base := prefixAlnum(parts[0])
	if base == "" {
		base = "site"
	}
	if !isASCIILower(base[0]) {
		base = "s" + base
	}
	if len(base) < 3 {
		base += "site"
	}
	return base[:min(len(base), 24)]
}

func prefixAlnum(s string) string {
	b := []rune{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b = append(b, r)
		}
	}
	return string(b)
}

func isASCIILower(c byte) bool { return c >= 'a' && c <= 'z' }

// AppType returns the app kind of a website from its runtime: "nodejs",
// "python" or "" (WebsiteService::appType).
func AppType(w *Website) string {
	rt := w.AppRuntime
	for typ, prefix := range appRuntimePrefix {
		if strings.HasPrefix(rt, prefix+":") {
			return typ
		}
	}
	return ""
}

// RedirectsText renders redirect rules as form text (WebsiteService::redirectsText).
func RedirectsText(rs []Redirect) string {
	lines := make([]string, 0, len(rs))
	for _, r := range rs {
		lines = append(lines, fmt.Sprintf("%s %s %d", r.From, r.To, r.Code))
	}
	return strings.Join(lines, "\n")
}

// parsePHPInt mimics filter_var($v, FILTER_VALIDATE_INT): optional surrounding
// whitespace and sign, then digits only.
func parsePHPInt(v string) (int, bool) {
	t := strings.TrimSpace(v)
	start := 0
	if start < len(t) && (t[0] == '+' || t[0] == '-') {
		start = 1
	}
	if start == len(t) {
		return 0, false
	}
	n, err := strconv.Atoi(t[start:])
	if err != nil {
		return 0, false
	}
	return n, true
}

// formBool mimics filter_var(..., FILTER_VALIDATE_BOOL) for form values.
func formBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// versionLess compares dotted version strings (8.3.1 < 8.4), like
// version_compare used by phpVersions/defaultPhpVersion.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var an, bn int
		if i < len(as) {
			an, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bn, _ = strconv.Atoi(bs[i])
		}
		if an != bn {
			return an < bn
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Plan / feature / discovery helpers

// kindAllowed reports whether the actor's plan allows creating a site of this
// kind (WebsiteService::kindAllowed). A reverse-proxy kind is allowed when any
// proxy-family type is included.
func (s *WebsiteService) kindAllowed(ctx context.Context, actor *identity.User, kind string) (bool, error) {
	if s.features == nil {
		return true, nil
	}
	var keys []string
	switch kind {
	case "nodejs":
		keys = []string{"type_nodejs"}
	case "python":
		keys = []string{"type_python"}
	case "proxy":
		keys = []string{"type_proxy", "type_nodejs", "type_python"}
	default:
		keys = []string{kindFeature[kind]}
	}
	for _, k := range keys {
		if ok, err := s.features.FeatureEnabled(ctx, actor, k); err != nil {
			return false, err
		} else if ok {
			return true, nil
		}
	}
	return false, nil
}

// PhpVersions lists installed PHP versions with FPM, ascending
// (WebsiteService::phpVersions). Agent failures yield an empty list.
func (s *WebsiteService) PhpVersions(ctx context.Context) []string {
	raw, err := s.agent.Call(ctx, "discovery.php")
	if err != nil {
		return []string{}
	}
	var d struct {
		Versions []struct {
			Version   string `json:"version"`
			FPMBinary string `json:"fpm_binary"`
		} `json:"versions"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return []string{}
	}
	out := []string{}
	for _, v := range d.Versions {
		if v.FPMBinary != "" {
			out = append(out, v.Version)
		}
	}
	sort.Slice(out, func(i, j int) bool { return versionLess(out[i], out[j]) })
	return out
}

// SitesRoot is the agent's sites root, defaulting to /home/whacp
// (WebsiteService::sitesRoot).
func (s *WebsiteService) SitesRoot(ctx context.Context) string {
	raw, err := s.agent.Call(ctx, "agent.status")
	if err != nil {
		return "/home/whacp"
	}
	var d struct {
		SitesRoot string `json:"sites_root"`
	}
	if json.Unmarshal(raw, &d) != nil || d.SitesRoot == "" {
		return "/home/whacp"
	}
	return d.SitesRoot
}

// DefaultPhpVersion is the PHP version new websites start with: the configured
// default when installed, else the newest installed (WebsiteService::defaultPhpVersion).
func (s *WebsiteService) DefaultPhpVersion(installed []string) string {
	want := ""
	if s.cfg != nil {
		want, _ = s.cfg.String(ctx0(), "default_php_version", "")
	}
	if want != "" {
		for _, v := range installed {
			if v == want {
				return want
			}
		}
	}
	sort.Slice(installed, func(i, j int) bool { return versionLess(installed[i], installed[j]) })
	if len(installed) == 0 {
		return ""
	}
	return installed[len(installed)-1]
}

// HardenNewSites reports whether new websites start with shell functions
// disabled (WebsiteService::hardenNewSites).
func (s *WebsiteService) HardenNewSites() bool {
	if s.cfg == nil {
		return false
	}
	b, _ := s.cfg.Bool(ctx0(), "php_harden_new_sites", false)
	return b
}

// DefaultPhpProfile is the PHP security profile new websites start on
// (WebsiteService::defaultPhpProfile): recommended, balanced or compatibility,
// defaulting to recommended.
func (s *WebsiteService) DefaultPhpProfile() string {
	p := ""
	if s.cfg != nil {
		p, _ = s.cfg.String(ctx0(), "default_php_security_profile", "")
	}
	switch p {
	case "recommended", "balanced", "compatibility":
		return p
	}
	return "recommended"
}

// ---------------------------------------------------------------------------
// Validation

type validatedSite struct {
	Row   Website
	Names []string // primary first, then aliases
}

// intBounds mirrors the [lo, hi, def] table in WebsiteService::validate.
type intBound struct{ lo, hi, def int }

var phpIntBounds = map[string]intBound{
	"memory_limit_mb":       {32, 8192, 256},
	"upload_max_mb":         {1, 4096, 64},
	"max_execution_seconds": {5, 3600, 60},
	"max_children":          {1, 200, 5},
	"start_servers":         {0, 200, 0},
	"min_spare_servers":     {0, 200, 0},
	"max_spare_servers":     {0, 200, 0},
	"idle_timeout_seconds":  {1, 3600, 10},
	"max_requests":          {1, 100000, 500},
	"request_timeout_seconds": {0, 86400, 0},
	"slowlog_seconds":       {1, 3600, 10},
}

// Validate checks form input against the server and returns the row to
// persist plus all names (primary + aliases). It ports WebsiteService::validate
// field for field, including the customer rule that server-wide PHP settings
// are ignored when serverSettings is false.
func (s *WebsiteService) Validate(ctx context.Context, in map[string]string, existing *Website, serverSettings bool) (*validatedSite, error) {
	if !serverSettings {
		for _, k := range serverPHPSettings {
			delete(in, k)
		}
	}
	errs := map[string]string{}
	addErr := func(field, msg string) {
		if _, ok := errs[field]; !ok {
			errs[field] = msg
		}
	}

	var existingID int64
	domain := NormalizeHost(in["domain"])
	if existing != nil {
		domain = existing.Domain
		existingID = existing.ID
	} else if !ValidHost(domain) {
		addErr("domain", "Enter a valid domain name such as example.com.")
	}

	aliases := []string{}
	for _, a := range strings.FieldsFunc(in["aliases"], func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' }) {
		a = NormalizeHost(a)
		if !ValidHost(a) {
			addErr("aliases", "“"+a+"” is not a valid domain name.")
			break
		}
		if a != domain && !containsStr(aliases, a) {
			aliases = append(aliases, a)
		}
	}
	if len(aliases) > 50 {
		addErr("aliases", "At most 50 aliases are allowed.")
	}
	names := append([]string{domain}, aliases...)
	for _, n := range names {
		owner, err := s.sites.DomainOwner(ctx, s.serverID, n)
		if err != nil {
			return nil, err
		}
		if owner != 0 && owner != existingID {
			if n == domain {
				addErr("domain", n+" already belongs to another website.")
			} else {
				addErr("aliases", n+" already belongs to another website.")
			}
		}
	}

	// nodejs/python are reverse-proxy sites bound to a runtime. On edit a site
	// keeps its app type (the kind cannot change after creation).
	var existingAppType string
	if existing != nil {
		existingAppType = AppType(existing)
	}
	kind := ""
	if existingAppType != "" {
		kind = existingAppType
	} else {
		requested := in["kind"]
		if existing != nil {
			kind = requested
			if kind == "" {
				kind = existing.Kind
			}
		} else {
			kind = requested
			if kind == "" {
				kind = "php"
			}
		}
	}
	appRuntime := ""
	if existing != nil {
		appRuntime = existing.AppRuntime
	}
	proxy := ""
	if existing != nil {
		proxy = existing.ProxyPass
	}
	if _, isApp := appKinds[kind]; isApp {
		prefix := appRuntimePrefix[kind]
		if v, ok := in["app_runtime"]; ok {
			appRuntime = strings.TrimSpace(v)
		}
		if !regexp.MustCompile(`^`+prefix+`:[0-9][0-9A-Za-z.]{0,31}$`).MatchString(appRuntime) {
			addErr("app_runtime", "Choose a "+appKinds[kind]+" version (install one under Runtimes).")
		}
		var port int
		if v, ok := in["app_port"]; ok {
			port, _ = parsePHPInt(v)
		} else if m := portInProxyRe.FindStringSubmatch(proxy); m != nil {
			port, _ = strconv.Atoi(m[1])
		}
		if port < 1024 || port > 65535 {
			addErr("app_port", "Enter a port between 1024 and 65535 (where your app listens).")
		} else {
			proxy = "http://127.0.0.1:" + strconv.Itoa(port)
		}
		kind = "proxy" // the agent renders it as a reverse proxy
	} else if _, isKnown := kindLabels[kind]; !isKnown {
		addErr("kind", "Choose a website type.")
		appRuntime = ""
	} else {
		appRuntime = ""
	}

	php := ""
	if kind == "php" {
		if v, ok := in["php_version"]; ok {
			php = v
		} else if existing != nil {
			php = existing.PhpVersion
		}
		available := s.PhpVersions(ctx)
		if !containsStr(available, php) {
			if len(available) == 0 {
				addErr("php_version", "No PHP-FPM version is installed. Install one on the PHP page first.")
			} else {
				addErr("php_version", "Choose an installed PHP version.")
			}
		}
	}
	if kind == "proxy" && appRuntime == "" {
		if v, ok := in["proxy_pass"]; ok {
			proxy = strings.TrimSpace(v)
		}
		if !proxyPassRe.MatchString(proxy) {
			addErr("proxy_pass", "Enter a local address such as http://127.0.0.1:3000.")
		}
	}

	user := ""
	if existing != nil {
		user = existing.SystemUser
	} else {
		user = strings.ToLower(strings.TrimSpace(in["system_user"]))
		if user == "" {
			user = SuggestUser(domain)
		}
		if !userRe.MatchString(user) || reservedSystemUsers[user] {
			addErr("system_user", "Use 2–31 lowercase letters, digits, - or _, starting with a letter.")
		}
	}

	// PHP-FPM integer settings (bounds above).
	stored := PhpSettings{}
	if existing != nil {
		stored = existing.PhpSettings
	}
	set := func(k string) int {
		b := phpIntBounds[k]
		var raw string
		var have bool
		if v, ok := in[k]; ok {
			raw, have = v, true
		} else if existing != nil {
			raw = strconv.Itoa(storedInt(stored, k))
			have = true
		}
		if !have {
			return b.def
		}
		if n, ok := parsePHPInt(raw); ok && n >= b.lo && n <= b.hi {
			return n
		}
		addErr(k, fmt.Sprintf("Enter a number between %d and %d.", b.lo, b.hi))
		return b.def
	}
	ps := PhpSettings{
		MemoryLimitMB:         set("memory_limit_mb"),
		UploadMaxMB:           set("upload_max_mb"),
		MaxExecutionSeconds:   set("max_execution_seconds"),
		MaxChildren:           set("max_children"),
		StartServers:          set("start_servers"),
		MinSpareServers:       set("min_spare_servers"),
		MaxSpareServers:       set("max_spare_servers"),
		IdleTimeoutSeconds:    set("idle_timeout_seconds"),
		MaxRequests:           set("max_requests"),
		RequestTimeoutSeconds: set("request_timeout_seconds"),
		SlowlogSeconds:        set("slowlog_seconds"),
	}
	if v, ok := in["harden"]; ok {
		ps.Harden = formBool(v)
	} else if existing != nil {
		ps.Harden = stored.Harden
	} else {
		ps.Harden = s.HardenNewSites()
	}
	profile := ""
	if v, ok := in["php_profile"]; ok {
		profile = v
	} else if existing != nil {
		profile = stored.Profile
	} else {
		profile = s.DefaultPhpProfile()
	}
	switch profile {
	case "", "recommended", "balanced", "compatibility", "custom":
	default:
		addErr("php_profile", "Choose Recommended, Balanced, Compatibility or Custom.")
		if existing == nil {
			profile = s.DefaultPhpProfile()
		} else {
			profile = ""
		}
	}
	ps.Profile = profile
	ps.DisableFunctions = ""
	if profile == "custom" {
		raw := ""
		if v, ok := in["disable_functions"]; ok {
			raw = v
		} else if existing != nil {
			raw = stored.DisableFunctions
		}
		seen := map[string]bool{}
		fns := []string{}
		for _, fn := range strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool { return r == ' ' || r == ',' }) {
			if fnNameRe.MatchString(fn) && !seen[fn] {
				seen[fn] = true
				fns = append(fns, fn)
			}
		}
		ps.DisableFunctions = strings.Join(fns, ",")
	}
	pm := ""
	if v, ok := in["pm"]; ok {
		pm = v
	} else if existing != nil {
		pm = stored.PM
	}
	if pm == "" {
		pm = "ondemand"
	}
	switch pm {
	case "ondemand", "dynamic", "static":
	default:
		addErr("pm", "Choose on demand, dynamic or static.")
		pm = "ondemand"
	}
	ps.PM = pm
	if pm == "dynamic" {
		mins, start, max := ps.MinSpareServers, ps.StartServers, ps.MaxSpareServers
		explicit := mins != 0 || start != 0 || max != 0
		if explicit && !(1 <= mins && mins <= start && start <= max && max <= ps.MaxChildren) {
			addErr("start_servers", "Dynamic needs 1 ≤ min spare ≤ start ≤ max spare ≤ max PHP processes (or leave all three at 0).")
		}
	}
	// The website's own php.ini directives. The Go form does not offer the
	// directive editor yet (a later batch); values arrive as ini[name]=value
	// and are checked minimally (the agent re-checks on apply).
	ps.INI = stored.INI
	if ini := collectINI(in); len(ini) > 0 {
		valid := map[string]string{}
		for name, value := range ini {
			if !iniNameRe.MatchString(name) || len(value) > 1024 {
				addErr("ini."+name, "Not a valid php.ini directive or value.")
				continue
			}
			valid[name] = value
		}
		ps.INI = valid
	}

	body := 0
	{
		var raw string
		var have bool
		if v, ok := in["client_max_body_mb"]; ok {
			raw, have = v, true
		} else if existing != nil {
			raw = strconv.Itoa(existing.ClientMaxBodyMB)
			have = true
		}
		if !have {
			raw = strconv.Itoa(ps.UploadMaxMB)
		}
		if n, ok := parsePHPInt(raw); ok && n >= 1 && n <= 4096 {
			body = n
		} else {
			addErr("client_max_body_mb", "Enter a number between 1 and 4096.")
			body = ps.UploadMaxMB
		}
	}

	// Redirects: one "from to [code]" per line.
	redirectText := ""
	if v, ok := in["redirects"]; ok {
		redirectText = v
	} else if existing != nil {
		redirectText = RedirectsText(existing.Redirects)
	}
	redirects := []Redirect{}
	for i, line := range strings.Split(redirectText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		from := ""
		to := ""
		code := 301
		if len(parts) > 0 {
			from = parts[0]
		}
		if len(parts) > 1 {
			to = parts[1]
		}
		if len(parts) > 2 {
			code, _ = parsePHPInt(parts[2])
		}
		if !redirectFromRe.MatchString(from) || !redirectToRe.MatchString(to) || to == "" || (code != 301 && code != 302 && code != 307 && code != 308) {
			addErr("redirects", fmt.Sprintf("Line %d: use “/old-path https://example.com/new [301]”.", i+1))
			break
		}
		redirects = append(redirects, Redirect{From: from, To: to, Code: code})
	}

	sub := strings.Trim(in["document_root_sub"], "/")
	if sub != "" && (!docrootSubRe.MatchString(sub) || docrootDotDot.MatchString(sub)) {
		addErr("document_root_sub", "Use a relative folder such as public_html/public.")
	}

	if len(errs) > 0 {
		return nil, &ValidationError{Fields: errs}
	}

	root := ""
	docroot := ""
	if existing != nil {
		root = existing.Root
		docroot = existing.DocumentRoot
	} else {
		root = strings.TrimSuffix(s.SitesRoot(ctx), "/") + "/" + domain
		docroot = root + "/public_html"
	}
	if sub != "" {
		docroot = root + "/" + sub
	}

	enabled := true
	if existing != nil {
		enabled = existing.Enabled
	}
	row := Website{
		Domain:          domain,
		SystemUser:      user,
		Root:            root,
		DocumentRoot:    docroot,
		Kind:            kind,
		AppRuntime:      appRuntime,
		PhpVersion:      php,
		PhpSettings:     ps,
		ProxyPass:       proxy,
		Redirects:       redirects,
		ClientMaxBodyMB: body,
		Enabled:         enabled,
	}
	return &validatedSite{Row: row, Names: names}, nil
}

// collectINI gathers ini[name]=value form fields.
func collectINI(in map[string]string) map[string]string {
	out := map[string]string{}
	prefix := "ini["
	for k, v := range in {
		if strings.HasPrefix(k, prefix) && strings.HasSuffix(k, "]") {
			out[strings.TrimSuffix(k[len(prefix):], "]")] = v
		}
	}
	return out
}

func storedInt(ps PhpSettings, k string) int {
	switch k {
	case "memory_limit_mb":
		return ps.MemoryLimitMB
	case "upload_max_mb":
		return ps.UploadMaxMB
	case "max_execution_seconds":
		return ps.MaxExecutionSeconds
	case "max_children":
		return ps.MaxChildren
	case "start_servers":
		return ps.StartServers
	case "min_spare_servers":
		return ps.MinSpareServers
	case "max_spare_servers":
		return ps.MaxSpareServers
	case "idle_timeout_seconds":
		return ps.IdleTimeoutSeconds
	case "max_requests":
		return ps.MaxRequests
	case "request_timeout_seconds":
		return ps.RequestTimeoutSeconds
	case "slowlog_seconds":
		return ps.SlowlogSeconds
	}
	return 0
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ctx0 is a background context for settings lookups (settings never use
// request-scoped state).
func ctx0() context.Context { return context.Background() }

// ---------------------------------------------------------------------------
// Ownership rules

// resolveOwner determines the owner of a new website (WebsiteService::resolveOwner):
// server-wide roles may pick one; customers and resellers own what they create.
func (s *WebsiteService) resolveOwner(in map[string]string, actor *identity.User) *int64 {
	if actor != nil && actor.RoleScope == "all" {
		if v, ok := in["owner_id"]; ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
				return &n
			}
		}
		return nil
	}
	if actor == nil {
		return nil
	}
	id := actor.ID
	return &id
}

// assertNotUnderOthers stops customers/resellers from adding names under
// another account's domain (WebsiteService::assertNotUnderOthers).
func (s *WebsiteService) assertNotUnderOthers(ctx context.Context, names []string, ownerID *int64) error {
	for _, n := range names {
		parts := strings.Split(n, ".")
		for i := 1; i < len(parts)-1; i++ {
			siteID, err := s.sites.DomainOwner(ctx, s.serverID, strings.Join(parts[i:], "."))
			if err != nil || siteID == 0 {
				continue
			}
			owner, err := s.sites.Find(ctx, s.serverID, siteID, nil)
			if err != nil || owner == nil {
				continue
			}
			if orInt(owner.OwnerID) != orInt(ownerID) {
				field := "aliases"
				if n == names[0] {
					field = "domain"
				}
				return &ValidationError{Fields: map[string]string{field: n + " is under a domain of another account."}}
			}
		}
	}
	return nil
}

// checkSystemUser reports whether a requested system account must be created
// new by the agent (WebsiteService::checkSystemUser). Hosting customers and
// resellers may reuse only the account of one of their own websites with the
// same owner; administrators may attach existing accounts.
func (s *WebsiteService) checkSystemUser(ctx context.Context, user string, ownerID *int64, actor *identity.User) (bool, error) {
	usedBy, err := s.sites.UsingSystemUser(ctx, s.serverID, user)
	if err != nil {
		return false, err
	}
	if actor != nil && actor.RoleScope == "all" {
		return false, nil
	}
	if len(usedBy) == 0 {
		return true, nil
	}
	for _, w := range usedBy {
		found, err := s.sites.Find(ctx, s.serverID, w.ID, actor)
		if err != nil || found == nil || orInt(w.OwnerID) != orInt(ownerID) {
			return false, &ValidationError{Fields: map[string]string{"system_user": "The system user " + user + " belongs to another account. Choose another name."}}
		}
	}
	return false, nil
}

func orInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// ownerPtr converts a nullable owner column to the pointer form the limit and
// ownership rules use (nil = unassigned, counted against no plan).
func ownerPtr(o sql.NullInt64) *int64 {
	if !o.Valid {
		return nil
	}
	v := o.Int64
	return &v
}

// ---------------------------------------------------------------------------
// The agent spec (WebsiteService::spec)

// Spec builds the agent's `sites.apply` payload for a stored website.
// Mirrors WebsiteService::spec key for key.
func Spec(w *Website, createUser bool) map[string]any {
	php := map[string]any{
		"memory_limit_mb":         w.PhpSettings.MemoryLimitMB,
		"upload_max_mb":           w.PhpSettings.UploadMaxMB,
		"max_execution_seconds":   w.PhpSettings.MaxExecutionSeconds,
		"max_children":            w.PhpSettings.MaxChildren,
		"harden":                  w.PhpSettings.Harden,
		"profile":                 w.PhpSettings.Profile,
		"disable_functions":       w.PhpSettings.DisableFunctions,
		"pm":                      w.PhpSettings.PM,
		"start_servers":           w.PhpSettings.StartServers,
		"min_spare_servers":       w.PhpSettings.MinSpareServers,
		"max_spare_servers":       w.PhpSettings.MaxSpareServers,
		"idle_timeout_seconds":    w.PhpSettings.IdleTimeoutSeconds,
		"max_requests":            w.PhpSettings.MaxRequests,
		"request_timeout_seconds": w.PhpSettings.RequestTimeoutSeconds,
		"slowlog_seconds":         w.PhpSettings.SlowlogSeconds,
	}
	if len(w.PhpSettings.INI) > 0 {
		ini := map[string]any{}
		for k, v := range w.PhpSettings.INI {
			ini[k] = v
		}
		php["ini"] = ini
	}
	aliases := w.Aliases
	if aliases == nil {
		aliases = []string{}
	}
	redirects := w.Redirects
	if redirects == nil {
		redirects = []Redirect{}
	}
	spec := map[string]any{
		"domain":           w.Domain,
		"aliases":          aliases,
		"user":             w.SystemUser,
		"create_user":      createUser,
		"root":             w.Root,
		"document_root":    w.DocumentRoot,
		"kind":             w.Kind,
		"php_version":      phpVersionFor(w),
		"php":              php,
		"proxy_pass":       proxyPassFor(w),
		"https":            w.HTTPS,
		"force_https":      w.ForceHTTPS,
		"hsts":             w.HSTS,
		"security_headers": w.SecurityHeaders,
		"cert_file":        w.CertFile,
		"key_file":         w.KeyFile,
		"enabled":          w.Enabled,
		"redirects":        redirects,
		"client_max_body_mb": w.ClientMaxBodyMB,
	}
	if w.DirectoryListing && w.Kind != "proxy" {
		spec["autoindex"] = true
	}
	if w.IPv4.Valid {
		spec["ipv4"] = w.IPv4.String
	}
	if w.IPv6.Valid {
		spec["ipv6"] = w.IPv6.String
	}
	return spec
}

func phpVersionFor(w *Website) string {
	if w.Kind == "php" {
		return w.PhpVersion
	}
	return ""
}

func proxyPassFor(w *Website) string {
	if w.Kind == "proxy" {
		return w.ProxyPass
	}
	return ""
}

// ---------------------------------------------------------------------------
// Apply / restore

// apply pushes the stored state to the server and records the resulting
// status (WebsiteService::apply).
func (s *WebsiteService) apply(ctx context.Context, site *Website, newUser bool) error {
	spec := Spec(site, true)
	// A suspended account's websites are off; their own setting is kept.
	if site.AccountSuspended {
		spec["enabled"] = false
	}
	// Over the plan's monthly bandwidth: the website answers 509.
	if site.BandwidthBlocked {
		spec["bandwidth_exceeded"] = true
	}
	if newUser {
		spec["new_user"] = true
	}
	if _, err := s.agent.Call(ctx, "sites.apply", spec); err != nil {
		return err
	}
	status := "active"
	switch {
	case site.AccountSuspended:
		status = "suspended"
	case !site.Enabled:
		status = "disabled"
	}
	if err := s.sites.SetStatus(ctx, site.ID, status, ""); err != nil {
		return err
	}
	return s.sites.SetBandwidthApplied(ctx, site.ID, site.BandwidthBlocked)
}

// restore re-applies a previous website state after a failed update
// (WebsiteService::restore).
func (s *WebsiteService) restore(ctx context.Context, previous *Website) error {
	fields := map[string]any{
		"domain": previous.Domain, "system_user": previous.SystemUser,
		"root": previous.Root, "document_root": previous.DocumentRoot,
		"kind": previous.Kind, "php_version": previous.PhpVersion,
		"php_settings": previous.PhpSettings, "proxy_pass": previous.ProxyPass,
		"https": previous.HTTPS, "force_https": previous.ForceHTTPS, "hsts": previous.HSTS,
		"security_headers": previous.SecurityHeaders, "ssl_mode": previous.SSLMode,
		"cert_file": previous.CertFile, "key_file": previous.KeyFile,
		"redirects": previous.Redirects, "client_max_body_mb": previous.ClientMaxBodyMB,
		"enabled": previous.Enabled, "status": previous.Status,
		"directory_listing": previous.DirectoryListing, "ipv4": nullStr(previous.IPv4),
		"ipv6": nullStr(previous.IPv6),
	}
	if err := s.sites.UpdateFields(ctx, previous.ID, fields); err != nil {
		return err
	}
	names := append([]string{previous.Domain}, previous.Aliases...)
	return s.sites.ReplaceDomains(ctx, previous.ServerID, previous.ID, names)
}

func nullStr(s sql.NullString) any {
	if s.Valid {
		return s.String
	}
	return nil
}

// ---------------------------------------------------------------------------
// Mutations

// Create adds a new website (WebsiteService::create, $parent = null) or a
// child of $parent (subdomain/addon; see CreateChild). Returns the new id.
func (s *WebsiteService) Create(ctx context.Context, in map[string]string, actor *identity.User, parent *Website) (int64, error) {
	rawKind := in["kind"]
	if rawKind == "" {
		rawKind = "php"
	}
	if parent == nil {
		ok, err := s.kindAllowed(ctx, actor, rawKind)
		if err != nil {
			return 0, err
		}
		if !ok {
			label := appKinds[rawKind]
			if label == "" {
				label = kindLabels[rawKind]
			}
			if label == "" {
				label = rawKind
			}
			return 0, &ValidationError{Fields: map[string]string{"kind": "Creating " + label + " websites is not included in your hosting plan."}}
		}
	}
	serverSettings := actor != nil && actor.RoleScope == "all"
	v, err := s.Validate(ctx, in, nil, serverSettings)
	if err != nil {
		return 0, err
	}
	// A subdomain or addon domain belongs to its parent's owner.
	var ownerID *int64
	if parent != nil {
		ownerID = ownerPtr(parent.OwnerID)
	} else {
		ownerID = s.resolveOwner(in, actor)
	}
	if parent == nil {
		if err := s.limits.AssertCanCreate(ctx, "websites", ownerID, 1); err != nil {
			return 0, err
		}
	}
	if len(v.Names) > 1 {
		if err := s.limits.AssertCanCreate(ctx, "parked_domains", ownerID, len(v.Names)-1); err != nil {
			return 0, err
		}
	}
	if actor != nil && actor.RoleScope != "all" {
		if err := s.assertNotUnderOthers(ctx, v.Names, ownerID); err != nil {
			return 0, err
		}
	}
	var newUser bool
	if in["new_system_user"] != "" {
		// A new hosting account: its own, new system account (the agent
		// refuses one that exists, e.g. a server login).
		used, err := s.sites.UsingSystemUser(ctx, s.serverID, v.Row.SystemUser)
		if err != nil {
			return 0, err
		}
		if len(used) > 0 {
			return 0, &ValidationError{Fields: map[string]string{"system_user": "The system user " + v.Row.SystemUser + " is in use. Choose another username."}}
		}
		newUser = true
	} else {
		newUser, err = s.checkSystemUser(ctx, v.Row.SystemUser, ownerID, actor)
		if err != nil {
			return 0, err
		}
	}

	row := v.Row
	row.ServerID = s.serverID
	row.OwnerID = sql.NullInt64{}
	if ownerID != nil {
		row.OwnerID = sql.NullInt64{Int64: *ownerID, Valid: true}
	}
	row.ParentID = sql.NullInt64{}
	if parent != nil {
		row.ParentID = sql.NullInt64{Int64: parent.ID, Valid: true}
	}
	row.Status = "pending"

	id, err := s.sites.Insert(ctx, s.serverID, &row)
	if err != nil {
		return 0, err
	}
	cleanup := func() { s.sites.DeleteSite(ctx, id) }
	if err := s.sites.ReplaceDomains(ctx, s.serverID, id, v.Names); err != nil {
		cleanup()
		return 0, err
	}
	site, err := s.sites.FindFull(ctx, s.serverID, id, nil)
	if err != nil {
		cleanup()
		return 0, err
	}
	if err := s.apply(ctx, site, newUser); err != nil {
		cleanup()
		s.recordAudit(ctx, "website.create", "website", row.Domain, "failure", map[string]any{"error": err.Error()})
		return 0, err
	}
	s.recordAudit(ctx, "website.create", "website", strconv.FormatInt(id, 10), "success", map[string]any{
		"domain": row.Domain, "kind": row.Kind, "php": row.PhpVersion, "user": row.SystemUser,
	})
	return id, nil
}

// Update saves changed settings for a website (WebsiteService::update). The
// site must already be access-checked by the caller.
func (s *WebsiteService) Update(ctx context.Context, site *Website, in map[string]string, actor *identity.User) error {
	serverSettings := actor == nil || actor.RoleScope == "all"
	v, err := s.Validate(ctx, in, site, serverSettings)
	if err != nil {
		return err
	}
	if actor != nil && actor.RoleScope != "all" {
		if err := s.assertNotUnderOthers(ctx, v.Names, ownerPtr(site.OwnerID)); err != nil {
			return err
		}
	}
	added := len(v.Names) - 1 - len(site.Aliases)
	if added > 0 {
		if err := s.limits.AssertCanCreate(ctx, "parked_domains", ownerPtr(site.OwnerID), added); err != nil {
			return err
		}
	}
	previous := *site
	if err := s.sites.UpdateFields(ctx, site.ID, validatedFields(v.Row)); err != nil {
		return err
	}
	if err := s.sites.ReplaceDomains(ctx, site.ServerID, site.ID, v.Names); err != nil {
		s.restore(ctx, &previous)
		return err
	}
	updated, err := s.sites.FindFull(ctx, site.ServerID, site.ID, nil)
	if err != nil {
		s.restore(ctx, &previous)
		return err
	}
	if err := s.apply(ctx, updated, false); err != nil {
		s.restore(ctx, &previous)
		return err
	}
	s.recordAudit(ctx, "website.update", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain": site.Domain, "changes": websiteDiff(&previous, v.Row),
	})
	return nil
}

// validatedFields are the columns validate() produces (the update set).
func validatedFields(row Website) map[string]any {
	return map[string]any{
		"domain":              row.Domain,
		"system_user":         row.SystemUser,
		"root":                row.Root,
		"document_root":       row.DocumentRoot,
		"kind":                row.Kind,
		"app_runtime":         row.AppRuntime,
		"php_version":         row.PhpVersion,
		"php_settings":        row.PhpSettings,
		"proxy_pass":          row.ProxyPass,
		"redirects":           row.Redirects,
		"client_max_body_mb":  row.ClientMaxBodyMB,
		"enabled":             row.Enabled,
	}
}

// websiteDiff reports the changed row fields as {field: {from, to}} for the
// audit log (WebsiteService::diff).
func websiteDiff(before *Website, after Website) map[string]any {
	out := map[string]any{}
	add := func(key string, before, after any) {
		if fmt.Sprint(before) != fmt.Sprint(after) {
			out[key] = map[string]any{"from": before, "to": after}
		}
	}
	add("domain", before.Domain, after.Domain)
	add("system_user", before.SystemUser, after.SystemUser)
	add("root", before.Root, after.Root)
	add("document_root", before.DocumentRoot, after.DocumentRoot)
	add("kind", before.Kind, after.Kind)
	add("app_runtime", before.AppRuntime, after.AppRuntime)
	add("php_version", before.PhpVersion, after.PhpVersion)
	add("php_settings", before.PhpSettings, after.PhpSettings)
	add("proxy_pass", before.ProxyPass, after.ProxyPass)
	add("redirects", before.Redirects, after.Redirects)
	add("client_max_body_mb", before.ClientMaxBodyMB, after.ClientMaxBodyMB)
	add("enabled", before.Enabled, after.Enabled)
	return out
}

// patch changes fields owned by other features (enable, directory listing …)
// and applies (WebsiteService::patch).
func (s *WebsiteService) patch(ctx context.Context, site *Website, fields map[string]any, action string) error {
	previous := *site
	if err := s.sites.UpdateFields(ctx, site.ID, fields); err != nil {
		return err
	}
	updated, err := s.sites.FindFull(ctx, site.ServerID, site.ID, nil)
	if err != nil {
		return err
	}
	if err := s.apply(ctx, updated, false); err != nil {
		s.restore(ctx, &previous)
		return err
	}
	s.recordAudit(ctx, action, "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain": site.Domain, "changes": patchDiff(&previous, fields),
	})
	return nil
}

func patchDiff(before *Website, fields map[string]any) map[string]any {
	out := map[string]any{}
	if v, ok := fields["enabled"]; ok {
		b, _ := v.(bool)
		if b != before.Enabled {
			out["enabled"] = map[string]any{"from": before.Enabled, "to": b}
		}
	}
	if v, ok := fields["directory_listing"]; ok {
		b, _ := v.(bool)
		if b != before.DirectoryListing {
			out["directory_listing"] = map[string]any{"from": before.DirectoryListing, "to": b}
		}
	}
	return out
}

// SetEnabled toggles a website (WebsiteService::setEnabled).
func (s *WebsiteService) SetEnabled(ctx context.Context, site *Website, enabled bool) error {
	action := "website.disable"
	if enabled {
		action = "website.enable"
	}
	return s.patch(ctx, site, map[string]any{"enabled": enabled}, action)
}

// SetDirectoryListing turns directory listing on/off (WebsiteService::setDirectoryListing).
func (s *WebsiteService) SetDirectoryListing(ctx context.Context, site *Website, on bool) error {
	if site.Kind == "proxy" {
		return &UserError{What: "A proxy website's application serves its own pages; directory listing does not apply."}
	}
	action := "website.listing_off"
	if on {
		action = "website.listing_on"
	}
	return s.patch(ctx, site, map[string]any{"directory_listing": on}, action)
}

// Delete removes a website from the server and the panel, returning warnings
// (WebsiteService::delete).
func (s *WebsiteService) Delete(ctx context.Context, site *Website, removeFiles, removeUser bool) ([]string, error) {
	kept, err := s.sites.UsingSystemUser(ctx, site.ServerID, site.SystemUser)
	if err != nil {
		return nil, err
	}
	// The agent only sees websites with a PHP pool; static and disabled
	// websites have none. The panel knows them all: keep a shared account.
	keptFor := []int64{}
	for _, w := range kept {
		if w.ID != site.ID {
			keptFor = append(keptFor, w.ID)
		}
	}
	if len(keptFor) > 0 {
		removeUser = false
	}
	raw, err := s.agent.Call(ctx, "sites.delete", map[string]any{
		"domain": site.Domain, "user": site.SystemUser,
		"remove_files": removeFiles, "remove_user": removeUser,
	})
	if err != nil {
		return nil, err
	}
	var res struct {
		Trash    string   `json:"trash"`
		Warnings []string `json:"warnings"`
	}
	_ = json.Unmarshal(raw, &res)
	if err := s.sites.DeleteSite(ctx, site.ID); err != nil {
		return nil, err
	}
	files := "kept"
	if removeFiles && res.Trash != "" {
		files = res.Trash
	}
	s.recordAudit(ctx, "website.delete", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain": site.Domain, "files": files, "user_removed": removeUser,
	})
	warnings := res.Warnings
	if len(keptFor) > 0 {
		warnings = append(warnings, "The system user "+site.SystemUser+" was kept because other websites use it.")
	}
	return warnings, nil
}

// CreateChild adds a subdomain (name.parent-domain) or an addon domain to a
// website: a website of its own, with the parent's account, owner and PHP
// version; the subdomain also gets an A record when the parent's DNS zone is
// hosted here (WebsiteService::createChild). Returns the child id and whether
// a DNS record was added.
func (s *WebsiteService) CreateChild(ctx context.Context, parent *Website, typ string, in map[string]string, actor *identity.User) (int64, bool, error) {
	var domain, label string
	if typ == "subdomain" {
		label = strings.ToLower(strings.TrimSpace(in["subdomain"]))
		if !subdomainRe.MatchString(label) || label == "www" {
			return 0, false, &ValidationError{Fields: map[string]string{"subdomain": "Use letters, digits and hyphens, e.g. blog or shop (www is an alias of the website itself)."}}
		}
		domain = label + "." + parent.Domain
	} else {
		domain = NormalizeHost(in["domain"])
		if domain == parent.Domain || strings.HasSuffix(domain, "."+parent.Domain) {
			return 0, false, &ValidationError{Fields: map[string]string{"domain": "That is a subdomain of " + parent.Domain + "; add it as a subdomain."}}
		}
	}
	kind := parent.Kind
	if kind == "proxy" {
		kind = "php"
	}
	child := map[string]string{
		"domain":      domain,
		"system_user": parent.SystemUser,
		"kind":        kind,
		"php_version": parent.PhpVersion,
	}
	if typ == "addon" && in["www"] != "" {
		child["aliases"] = "www." + domain
	}
	if kind == "php" && child["php_version"] == "" {
		versions := s.PhpVersions(ctx)
		if len(versions) > 0 {
			child["php_version"] = versions[0]
		}
	}
	if err := s.limits.AssertCanCreate(ctx, childLimit(typ), ownerPtr(parent.OwnerID), 1); err != nil {
		return 0, false, err
	}
	// An addon domain is a separate site, so it also counts against the
	// website limit (subdomains do not — they have their own limit).
	if typ == "addon" {
		if err := s.limits.AssertCanCreate(ctx, "websites", ownerPtr(parent.OwnerID), 1); err != nil {
			return 0, false, err
		}
	}
	id, err := s.Create(ctx, child, actor, parent)
	if err != nil {
		return 0, false, err
	}
	dnsAdded := false
	if typ == "subdomain" && in["dns"] != "" {
		dnsAdded = s.addSubdomainRecord(ctx, parent, label)
	}
	return id, dnsAdded, nil
}

func childLimit(typ string) string {
	if typ == "subdomain" {
		return "subdomains"
	}
	return "addon_domains"
}

// addSubdomainRecord adds an A record for a new subdomain to the parent's
// hosted DNS zone (WebsiteService::addSubdomainRecord). It never fails: when
// it cannot add the record, the website still exists and the record can be
// added on the DNS page.
func (s *WebsiteService) addSubdomainRecord(ctx context.Context, parent *Website, label string) bool {
	if s.dns == nil || s.cfg == nil {
		return false
	}
	zone, err := s.dns.ZoneForWebsite(ctx, parent.ID)
	if err != nil || zone == nil {
		return false
	}
	ip := strings.TrimSpace(s.stringSetting("dns_default_ipv4"))
	if net.ParseIP(ip) == nil || !ipIsV4(ip) {
		return false
	}
	exists, err := s.dns.RecordNameExists(ctx, zone.ID, label)
	if err != nil || exists {
		return false
	}
	id, err := s.dns.InsertRecord(ctx, zone.ID, label, "A", ip, 0, 0, 0, 0)
	if err != nil {
		return false
	}
	fail := func() bool {
		s.dns.DeleteRecord(ctx, id)
		return false
	}
	payload, err := s.dns.ZonePayload(ctx, zone.ID)
	if err != nil {
		return fail()
	}
	raw, err := s.agent.Call(ctx, "dns.zone_apply", payload, 60*time.Second)
	if err != nil {
		return fail()
	}
	var z struct {
		Serial int `json:"serial"`
	}
	_ = json.Unmarshal(raw, &z)
	if err := s.dns.UpdateSerial(ctx, zone.ID, z.Serial); err != nil {
		return fail()
	}
	s.recordAudit(ctx, "dns.record_create", "dns_zone", strconv.FormatInt(zone.ID, 10), "success", map[string]any{
		"zone": zone.Name, "after": map[string]any{"name": label, "type": "A", "value": ip},
	})
	return true
}

func (s *WebsiteService) stringSetting(key string) string {
	if s.cfg == nil {
		return ""
	}
	v, _ := s.cfg.String(ctx0(), key, "")
	return v
}

func ipIsV4(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.To4() != nil
}

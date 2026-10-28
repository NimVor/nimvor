package sites

import (
	"context"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

const (
	testRoot  = "/home/nimvor"
	testState = "/var/lib/nimvor-agent"
)

var policyLayout = Layout{Socket: "/run/php/x.sock", LogDir: "/l", ACMERoot: "/a"}

func policySpec(h *HeaderPolicy, c *CachePolicy) Spec {
	s := spec()
	s.Headers, s.Cache = h, c
	return s
}

func renderPolicy(t *testing.T, h *HeaderPolicy, c *CachePolicy) string {
	t.Helper()
	s := policySpec(h, c)
	if err := s.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	return RenderNginx(s, policyLayout)
}

// A header must appear once in the generated server block.
func headerCount(conf, name string) int {
	return strings.Count(conf, "add_header "+name+" ")
}

func TestHeaderPolicyDefaultsAndPresets(t *testing.T) {
	conf := renderPolicy(t, &HeaderPolicy{}, nil)
	for _, want := range []string{
		`add_header X-Frame-Options "SAMEORIGIN" always;`,
		`add_header Content-Security-Policy "frame-ancestors 'self'" always;`,
		`add_header Referrer-Policy "strict-origin-when-cross-origin" always;`,
		`add_header Permissions-Policy "camera=(), microphone=(), geolocation=(), payment=(), usb=()" always;`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q\n%s", want, conf)
		}
	}
	for _, name := range []string{"X-Frame-Options", "Content-Security-Policy", "Referrer-Policy", "Permissions-Policy", "X-Content-Type-Options"} {
		if n := headerCount(conf, name); n != 1 {
			t.Errorf("%s sent %d times", name, n)
		}
	}
}

func TestHeaderPolicyReplacesTheLegacyBundleWithoutDuplicates(t *testing.T) {
	s := policySpec(&HeaderPolicy{Clickjacking: "deny", Referrer: "no-referrer", Permissions: "strict"}, nil)
	s.SecurityHeaders = true // the older switch is still on
	if err := s.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	conf := RenderNginx(s, policyLayout)
	for _, name := range []string{"X-Frame-Options", "Referrer-Policy", "Permissions-Policy", "Content-Security-Policy", "X-Content-Type-Options"} {
		if n := headerCount(conf, name); n != 1 {
			t.Errorf("%s sent %d times:\n%s", name, n, conf)
		}
	}
	if !strings.Contains(conf, `X-Frame-Options "DENY"`) || !strings.Contains(conf, "frame-ancestors 'none'") || strings.Contains(conf, "SAMEORIGIN") {
		t.Errorf("the customer's choice must win over the bundle:\n%s", conf)
	}
}

func TestClickjackingModes(t *testing.T) {
	cases := map[string]struct{ xfo, fa string }{
		"sameorigin": {`X-Frame-Options "SAMEORIGIN"`, "frame-ancestors 'self'"},
		"deny":       {`X-Frame-Options "DENY"`, "frame-ancestors 'none'"},
		"allow":      {"", "frame-ancestors 'self' https://partner.example"},
		"off":        {"", ""},
	}
	for mode, want := range cases {
		h := &HeaderPolicy{Clickjacking: mode}
		if mode == "allow" {
			h.FrameAncestors = []string{"https://partner.example"}
		}
		conf := renderPolicy(t, h, nil)
		if (want.xfo != "") != strings.Contains(conf, "X-Frame-Options") || (want.xfo != "" && !strings.Contains(conf, want.xfo)) {
			t.Errorf("%s: X-Frame-Options wrong:\n%s", mode, conf)
		}
		if want.fa == "" && strings.Contains(conf, "frame-ancestors") || want.fa != "" && !strings.Contains(conf, want.fa) {
			t.Errorf("%s: frame-ancestors wrong:\n%s", mode, conf)
		}
	}
}

func TestCSPEnforcedMergesFrameAncestorsIntoOneHeader(t *testing.T) {
	conf := renderPolicy(t, &HeaderPolicy{Clickjacking: "deny", CSP: "default-src 'self'; img-src 'self' https: data:"}, nil)
	want := `add_header Content-Security-Policy "default-src 'self'; img-src 'self' https: data:; frame-ancestors 'none'" always;`
	if !strings.Contains(conf, want) || headerCount(conf, "Content-Security-Policy") != 1 || strings.Contains(conf, "Report-Only") {
		t.Errorf("enforced CSP:\n%s", conf)
	}
}

func TestCSPReportOnlyStillEnforcesFrameAncestors(t *testing.T) {
	conf := renderPolicy(t, &HeaderPolicy{Clickjacking: "sameorigin", CSP: "default-src 'self'", CSPReportOnly: true}, nil)
	if !strings.Contains(conf, `add_header Content-Security-Policy "frame-ancestors 'self'" always;`) ||
		!strings.Contains(conf, `add_header Content-Security-Policy-Report-Only "default-src 'self'" always;`) {
		t.Errorf("report-only:\n%s", conf)
	}
	if strings.Contains(strings.Split(conf, "Report-Only")[0], "default-src") {
		t.Error("the report-only policy must not be enforced")
	}
}

func TestCSPValidation(t *testing.T) {
	good := []string{
		"default-src 'self'",
		"default-src 'self'; script-src 'self' https://cdn.example.com 'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU='; upgrade-insecure-requests",
		"img-src * data: blob:; report-uri /csp-report",
		"connect-src 'self' wss://live.example.com:8443 *.example.org",
	}
	for _, p := range good {
		if _, err := normalizeCSP(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	bad := map[string]string{
		"frame-ancestors 'self'":                 "frame-ancestors",
		"default-src 'self'; default-src 'none'": "twice",
		"default-src 'self' \"; add_header X 1":  "not allowed",
		"default-src 'self'$host":                "not allowed",
		"default-src 'self' }":                   "not allowed",
		"script-src 'nonce-abc'":                 "not a valid source",
		"script-src javascript:":                 "not a valid source",
		"default-src":                            "at least one source",
		"foo-src 'self'":                         "not a supported directive",
		"default-src 'none' 'self'":              "'none'",
		"upgrade-insecure-requests x":            "no value",
		"report-uri javascript:alert(1)":         "report-uri",
		strings.Repeat("a", maxCSPLen+1):         "longer",
	}
	for p, why := range bad {
		_, err := normalizeCSP(p)
		if err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%q: error %v, want mention of %q", p, err, why)
		}
	}
}

func TestPermissionsPolicyValidation(t *testing.T) {
	got, err := normalizePermissions(`geolocation=(self), camera=(), microphone=(self "https://meet.example.com"), fullscreen=*`)
	if err != nil || got != `camera=(), fullscreen=*, geolocation=(self), microphone=(self "https://meet.example.com")` {
		t.Errorf("%q, %v", got, err)
	}
	for _, p := range []string{"", "camera", "camera=(http://x)", "nonsense=()", "camera=(), camera=()", "camera=(self \"http://insecure.example\")", "camera=\"; add_header", "camera=($host)"} {
		if _, err := normalizePermissions(p); err == nil {
			t.Errorf("%q must be refused", p)
		}
	}
	conf := renderPolicy(t, &HeaderPolicy{Permissions: "custom", PermissionsCustom: `microphone=(self "https://meet.example.com")`}, nil)
	if !strings.Contains(conf, `add_header Permissions-Policy "microphone=(self \"https://meet.example.com\")" always;`) {
		t.Errorf("quotes must be escaped:\n%s", conf)
	}
}

func TestHeaderPolicyValidationErrors(t *testing.T) {
	for name, h := range map[string]HeaderPolicy{
		"clickjacking":     {Clickjacking: "everyone"},
		"frame_ancestors":  {Clickjacking: "allow"},
		"frame_ancestors2": {Clickjacking: "allow", FrameAncestors: []string{"http://plain.example"}},
		"referrer":         {Referrer: "unsafe-url"},
		"permissions":      {Permissions: "yolo"},
		"csp":              {CSP: "default-src 'self'; frame-ancestors *"},
	} {
		s := policySpec(&h, nil)
		err := s.Normalize(testRoot, testState)
		if err == nil || !strings.Contains(err.Error(), "headers.") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestHSTSOnlyForHTTPSAndBounded(t *testing.T) {
	// Not HTTPS: the setting is dropped, nothing is sent.
	conf := renderPolicy(t, &HeaderPolicy{HSTS: HSTSPolicy{Enabled: true, MaxAge: 31536000}}, nil)
	if strings.Contains(conf, "Strict-Transport-Security") {
		t.Errorf("HSTS over plain HTTP:\n%s", conf)
	}
	s := policySpec(&HeaderPolicy{HSTS: HSTSPolicy{Enabled: true, MaxAge: 31536000, IncludeSubdomains: true, Preload: true}}, nil)
	s.HTTPS, s.ForceHTTPS = true, true
	s.CertFile, s.KeyFile = "/etc/letsencrypt/live/new.test/fullchain.pem", "/etc/letsencrypt/live/new.test/privkey.pem"
	if err := s.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	conf = RenderNginx(s, policyLayout)
	if !strings.Contains(conf, `add_header Strict-Transport-Security "max-age=31536000; includeSubDomains; preload" always;`) || headerCount(conf, "Strict-Transport-Security") != 1 {
		t.Errorf("HSTS:\n%s", conf)
	}
	// The legacy flag is ignored once the policy exists (no second header).
	s.HSTS = true
	if conf = RenderNginx(s, policyLayout); headerCount(conf, "Strict-Transport-Security") != 1 {
		t.Errorf("HSTS twice:\n%s", conf)
	}
	for name, hs := range map[string]HSTSPolicy{
		"too short":       {Enabled: true, MaxAge: 10},
		"too long":        {Enabled: true, MaxAge: MaxHSTSAge + 1},
		"preload short":   {Enabled: true, MaxAge: 600, IncludeSubdomains: true, Preload: true},
		"preload no subs": {Enabled: true, MaxAge: 31536000, Preload: true},
	} {
		bad := s
		bad.Headers = &HeaderPolicy{HSTS: hs}
		if err := bad.Normalize(testRoot, testState); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	// Policy off: the legacy flag no longer sends it.
	off := s
	off.Headers = &HeaderPolicy{}
	if conf = RenderNginx(off, policyLayout); strings.Contains(conf, "Strict-Transport-Security") {
		t.Errorf("HSTS off in the policy must stay off:\n%s", conf)
	}
}

func TestCacheRulesRender(t *testing.T) {
	conf := renderPolicy(t, nil, &CachePolicy{StaticTTL: 604800, DynamicMode: "public", DynamicTTL: 120, BypassPaths: []string{"/members", "/shop.v2/"}})
	for _, want := range []string{
		"map $uri $nimvor_pf_",
		`"~*^/(?:wp-admin|wp-login\.php`,
		`"~*^(?:/members|/shop\.v2)(?:/|\.|$)" 1;`,
		`"200" 604800s;`,
		"expires $nimvor_exp_",
		"add_header Cache-Control $nimvor_cc_",
		`default "public, max-age=120";`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q\n%s", want, conf)
		}
	}
	// Every sensitive case answers private, no-store, regardless of mode.
	for _, rule := range []string{`"~^1" "private, no-store";`, `"~^.1" "private, no-store";`, `"~^..1" "private, no-store";`, `"~^...1" "private, no-store";`, `"~^.....1" "private, no-store";`} {
		if !strings.Contains(conf, rule) {
			t.Errorf("missing bypass rule %s", rule)
		}
	}
	// The maps come before the server blocks (http context).
	if strings.Index(conf, "map ") > strings.Index(conf, "server {") {
		t.Error("maps must precede the server blocks")
	}
	// The cache header is never sent for a response the application cached itself.
	if !strings.Contains(conf, `"~^....1" "";`) {
		t.Error("an application's own Cache-Control must be respected")
	}
}

func TestCacheRulesBypassOrderIsSafe(t *testing.T) {
	conf := renderPolicy(t, nil, &CachePolicy{DynamicMode: "public", DynamicTTL: 60})
	// Safe rules must precede the application-header rule, and static assets
	// are the only thing the first rule skips.
	i := func(s string) int { return strings.Index(conf, s) }
	if !(i(`"~^1"`) < i(`"~^....1"`) && i(`"~^.1"`) < i(`"~^....1"`) && i(`"~^.....1"`) < i(`"~^....1"`) && i(`"~^2"`) < i(`"~^1"`)) {
		t.Errorf("rule order:\n%s", conf)
	}
}

func TestCacheModesAndOff(t *testing.T) {
	for mode, want := range map[string]string{"revalidate": `default "no-cache";`, "no-store": `default "no-store";`} {
		if conf := renderPolicy(t, nil, &CachePolicy{DynamicMode: mode}); !strings.Contains(conf, want) {
			t.Errorf("%s:\n%s", mode, conf)
		}
	}
	// Defaults change nothing: no maps, no directives.
	for _, c := range []*CachePolicy{{}, {DynamicMode: "app"}} {
		conf := renderPolicy(t, nil, c)
		if strings.Contains(conf, "map ") || strings.Contains(conf, "expires") || strings.Contains(conf, "Cache-Control") {
			t.Errorf("default cache rules must emit nothing:\n%s", conf)
		}
	}
	// Static TTL alone: expires only.
	conf := renderPolicy(t, nil, &CachePolicy{StaticTTL: 3600})
	if !strings.Contains(conf, "expires $nimvor_exp_") || strings.Contains(conf, "add_header Cache-Control") {
		t.Errorf("static only:\n%s", conf)
	}
}

func TestCachePolicyValidation(t *testing.T) {
	bad := []CachePolicy{
		{StaticTTL: 5},
		{StaticTTL: MaxStaticTTL + 1},
		{DynamicMode: "forever"},
		{DynamicMode: "public", DynamicTTL: 5},
		{DynamicMode: "public", DynamicTTL: MaxDynamicTTL + 1},
		{BypassPaths: []string{"members"}},
		{BypassPaths: []string{"/a/../b"}},
		{BypassPaths: []string{"/x;y"}},
		{BypassPaths: []string{"/a b"}},
		{BypassPaths: []string{"/x\"}"}},
		{BypassPaths: make([]string, maxBypass+1)},
	}
	for i, c := range bad {
		c := c
		s := policySpec(nil, &c)
		if err := s.Normalize(testRoot, testState); err == nil || !strings.Contains(err.Error(), "cache.") {
			t.Errorf("case %d: %v", i, err)
		}
	}
	ok := CachePolicy{StaticTTL: MinStaticTTL, DynamicMode: "public", BypassPaths: []string{"/a", "/a/", "/b/c"}}
	s := policySpec(nil, &ok)
	if err := s.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	if s.Cache.DynamicTTL != 60 || len(s.Cache.BypassPaths) != 2 {
		t.Errorf("normalized: %+v", s.Cache)
	}
}

func TestCacheFlagsFileIsSelfContained(t *testing.T) {
	f := RenderCacheFlags()
	for _, v := range []string{"$nimvor_af", "$nimvor_cf", "$nimvor_sf", "$nimvor_kf", "$nimvor_mf"} {
		if !strings.Contains(f, "map ") || !strings.Contains(f, v+" {") {
			t.Errorf("missing %s", v)
		}
	}
	if !strings.Contains(f, "XSRF") && !strings.Contains(f, "xsrf") || !strings.Contains(f, "phpsessid") || !strings.Contains(f, "wordpress_sec") {
		t.Error("session cookie names")
	}
}

// Existing server-level rules are still emitted exactly as before and nothing
// of the new policy leaks into sites that have none.
func TestExistingRulesUnchangedByThePolicy(t *testing.T) {
	plain := spec()
	if err := plain.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	withPolicy := policySpec(&HeaderPolicy{Clickjacking: "deny"}, &CachePolicy{StaticTTL: 3600, DynamicMode: "no-store"})
	if err := withPolicy.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	l := policyLayout
	l.ModSecInclude = "    include /etc/nginx/nimvor-modsec/x/*.conf;\n"
	l.ProtectInclude = "    include /etc/nginx/nimvor-protect/x/*.conf;\n"
	for name, s := range map[string]Spec{"no policy": plain, "policy": withPolicy} {
		conf := RenderNginx(s, l)
		for _, want := range []string{
			"    add_header X-Content-Type-Options \"nosniff\" always;\n",
			"    location ~ /\\.(?!well-known) {\n        deny all;\n    }\n",
			"    disable_symlinks if_not_owner from=$document_root;\n",
			"        try_files $uri =404;\n        fastcgi_split_path_info",
			"    client_max_body_size ",
			"/etc/nginx/nimvor-modsec/x/*.conf",
			"/etc/nginx/nimvor-protect/x/*.conf",
			"location ^~ /.well-known/acme-challenge/",
		} {
			if !strings.Contains(conf, want) {
				t.Errorf("%s: lost %q", name, want)
			}
		}
		if n := strings.Count(conf, "    add_header X-Content-Type-Options \"nosniff\" always;\n"); n != 1 {
			t.Errorf("%s: nosniff marker appears %d times (ModSecurity and protection find it by this line)", name, n)
		}
		// The includes still follow the marker, and the policy sits before them.
		if strings.Index(conf, "nimvor-modsec") < strings.Index(conf, "X-Content-Type-Options") {
			t.Errorf("%s: includes must come after the marker", name)
		}
	}
	// No policy: byte-for-byte what it was (no maps, no cache lines).
	conf := RenderNginx(plain, l)
	if strings.Contains(conf, "map ") || strings.Contains(conf, "expires") || strings.Contains(conf, "nimvor_cc") {
		t.Errorf("a site without a policy changed:\n%s", conf)
	}
	// The bandwidth page keeps its own no-store header.
	bw := withPolicy
	bw.BandwidthExceeded = true
	if c := RenderNginx(bw, l); !strings.Contains(c, "return 509") {
		t.Error("bandwidth page lost")
	}
}

func TestLegacyHeadersStillWorkWithoutPolicy(t *testing.T) {
	s := spec()
	s.SecurityHeaders = true
	s.HSTS = true
	s.HTTPS, s.ForceHTTPS = true, true
	s.CertFile, s.KeyFile = "/etc/letsencrypt/live/new.test/fullchain.pem", "/etc/letsencrypt/live/new.test/privkey.pem"
	if err := s.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	conf := RenderNginx(s, policyLayout)
	for _, want := range []string{`Strict-Transport-Security "max-age=31536000"`, `X-Frame-Options "SAMEORIGIN"`, `Content-Security-Policy "upgrade-insecure-requests"`} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestApplyKeepsTheLastGoodConfigWhenNginxRejectsAPolicy(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	good := policySpec(&HeaderPolicy{Clickjacking: "sameorigin"}, &CachePolicy{StaticTTL: 3600})
	if _, err := m.Apply(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	file := "etc/nginx/sites-available/nimvor-new.test.conf"
	before := h.Read(file)
	if !strings.Contains(before, "X-Frame-Options") || !h.Exists("etc/nginx/sites-enabled/nimvor-00-cache-flags.conf") {
		t.Fatalf("first apply:\n%s", before)
	}
	h.Fail["nginx -t"] = "nginx: [emerg] unknown directive"
	h.FailFrom["nginx -t"] = 2 // the pre-check passes; validating the new file fails
	changed := policySpec(&HeaderPolicy{Clickjacking: "deny", CSP: "default-src 'self'"}, &CachePolicy{StaticTTL: 86400, DynamicMode: "no-store"})
	_, err := m.Apply(context.Background(), changed)
	if err == nil || !strings.Contains(err.Error(), "not applied") {
		t.Fatalf("a configuration nginx rejects must fail at validation: %v", err)
	}
	if after := h.Read(file); after != before {
		t.Errorf("the previous working file was not kept:\n%s", after)
	}
}

func TestApplyRefusesInvalidPoliciesBeforeTouchingTheHost(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	_, err := m.Apply(context.Background(), policySpec(&HeaderPolicy{CSP: "default-src 'self'; add_header X-Evil 1"}, nil))
	if err == nil {
		t.Fatal("invalid CSP accepted")
	}
	if h.Exists("etc/nginx/sites-available/nimvor-new.test.conf") || h.Runner.Called("nginx -t") {
		t.Error("an invalid policy must not reach the host")
	}
}

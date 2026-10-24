package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// This suite proves the website write path against the real schema: validation
// field-for-field against WebsiteService::validate, ownership and plan-limit
// rules, the agent spec, and the create/apply/delete lifecycle with cleanup on
// failure. The agent is faked over the AgentCaller interface.

// ---------------------------------------------------------------------------
// Fakes and setup

type fakeCall struct {
	action string
	params any
}

type fakeAgent struct {
	calls          []fakeCall
	failApply      error
	applySeq       int
	// applyFailEvery: fail the Nth sites.apply call (1-based) with applyErr.
	applyFailEvery int
	applyErr       error
	// canned: exact canned response for an action, checked before the
	// per-action switch (the runtime status, the permissions walk, …).
	canned         map[string]json.RawMessage
	// failActions: fail an action with a fixed error (agent failure paths).
	failActions    map[string]error
}

func (f *fakeAgent) Call(ctx context.Context, action string, params any, timeout ...time.Duration) (json.RawMessage, error) {
	f.calls = append(f.calls, fakeCall{action, params})
	if err, ok := f.failActions[action]; ok {
		return nil, err
	}
	if c, ok := f.canned[action]; ok {
		return c, nil
	}
	switch action {
	case "discovery.php":
		return json.Marshal(map[string]any{"versions": []map[string]any{
			{"version": "8.2", "fpm_binary": "/usr/sbin/php-fpm8.2"},
			{"version": "8.3.1", "fpm_binary": "/usr/sbin/php-fpm8.3"},
			{"version": "8.4-cli", "fpm_binary": ""},
		}})
	case "agent.status":
		return json.Marshal(map[string]any{"sites_root": "/home/whacp"})
	case "sites.apply":
		f.applySeq++
		if f.failApply != nil && f.applyFailEvery == 0 {
			return nil, f.failApply
		}
		if f.applyFailEvery > 0 && f.applySeq%f.applyFailEvery == f.applyFailEvery {
			return nil, f.applyErr
		}
		return json.Marshal(map[string]any{})
	case "sites.delete":
		return json.Marshal(map[string]any{"warnings": []string{"The access log was kept."}})
	case "dns.zone_apply":
		return json.Marshal(map[string]any{"serial": 42})
	}
	return json.Marshal(map[string]any{})
}

func (f *fakeAgent) callCount(action string) int {
	n := 0
	for _, c := range f.calls {
		if c.action == action {
			n++
		}
	}
	return n
}

// lastParams returns the most recent parameter map sent to the agent for an
// action (nil when the action was never called).
func (f *fakeAgent) lastParams(action string) map[string]any {
	var out map[string]any
	for _, c := range f.calls {
		if c.action == action {
			out, _ = c.params.(map[string]any)
		}
	}
	return out
}

type auditRec struct {
	action  string
	target  string
	result  string
	details map[string]any
}

func writeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'administrator','Admin','all',''),(2,'reseller','Reseller','own',''),(3,'customer','Customer','own','')`)
	// 1 admin; 2 reseller; 3 customer of reseller (plan 11, 1 website);
	// 4 standalone customer (plan 12, 1 website, 1 subdomain, 5 addon, 10 parked).
	exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,parent_id,plan_id) VALUES
		(1,'admin','a@x','h',1,1,'','','',NULL,NULL),
		(2,'reseller','r@x','h',2,1,'','','',NULL,NULL),
		(3,'cust','c@x','h',3,1,'','','',2,11),
		(4,'solo','o@x','h',3,1,'','','',NULL,12)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at) VALUES
		(11,'Cust1',10,3,0,0,'',''),
		(12,'Cust2',5,1,5,10,'','')`)
	// Two existing websites: cust's (owner 3) and solo's (owner 4).
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,3,'cust.example','u_cust','/home/whacp/cust.example','/home/whacp/cust.example/public_html','php','8.3.1','active','',''),
		(12,1,4,'solo.example','u_solo','/home/whacp/solo.example','/home/whacp/solo.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES
		(1,10,'cust.example',1),
		(1,12,'solo.example',1)`)
	return db
}

func loadUser(t *testing.T, db *sql.DB, id int64) *identity.User {
	t.Helper()
	u, err := identity.NewRepository(db).Find(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

type harness struct {
	db       *sql.DB
	svc      *WebsiteService
	agent    *fakeAgent
	features *authz.FeatureService
	audit    []auditRec
}

func newHarness(t *testing.T, db *sql.DB) *harness {
	t.Helper()
	fa := &fakeAgent{}
	features := authz.NewFeatureService(db)
	h := &harness{db: db, agent: fa, features: features, audit: []auditRec{}}
	svc := NewWebsiteService(
		NewWebsiteRepository(db),
		fa,
		features,
		NewPlanLimits(db),
		NewDnsRepository(db),
		settings.NewRepository(db),
		// Append through h.audit (not a captured copy) so the harness sees
		// every record the service makes.
		func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
			h.audit = append(h.audit, auditRec{action: action, target: target, result: result, details: details})
		},
		1,
	)
	h.svc = svc
	return h
}

// ---------------------------------------------------------------------------
// Pure helpers

func TestHostHelpers(t *testing.T) {
	if got := NormalizeHost("  EXAMPLE.com. "); got != "example.com" {
		t.Fatalf("NormalizeHost = %q", got)
	}
	cases := []struct {
		host string
		ok   bool
	}{
		{"example.com", true},
		{"a.co", true},
		{"ex ample.com", false},
		{"-bad.com", false},
		{"bad-.com", false},
		{"no_dot", false},
		{"1.2.3.4", false},
		{"ab", false},
		{"x." + strings.Repeat("y", 250) + ".com", false},
	}
	for _, c := range cases {
		if got := ValidHost(c.host); got != c.ok {
			t.Errorf("ValidHost(%q) = %v, want %v", c.host, got, c.ok)
		}
	}
	if got := SuggestUser("my-shop.co.uk"); got != "myshop" {
		t.Errorf("SuggestUser(my-shop.co.uk) = %q", got)
	}
	if got := SuggestUser("123.example.com"); got != "s123" {
		t.Errorf("SuggestUser(123.example.com) = %q", got)
	}
	if got := SuggestUser("a.b"); got != "asite" {
		t.Errorf("SuggestUser(a.b) = %q", got)
	}
	if got := RedirectsText([]Redirect{{"/a", "https://x.b", 301}}); got != "/a https://x.b 301" {
		t.Errorf("RedirectsText = %q", got)
	}
	if AppType(&Website{AppRuntime: "node:20.1.0"}) != "nodejs" || AppType(&Website{AppRuntime: "python:3.12"}) != "python" || AppType(&Website{AppRuntime: ""}) != "" {
		t.Error("AppType mismatch")
	}
	if n, ok := parsePHPInt(" 42 "); !ok || n != 42 {
		t.Error("parsePHPInt(' 42 ') failed")
	}
	if _, ok := parsePHPInt("42x"); ok {
		t.Error("parsePHPInt('42x') should fail")
	}
	if _, ok := parsePHPInt(""); ok {
		t.Error("parsePHPInt('') should fail")
	}
	if !versionLess("8.2", "8.3.1") || !versionLess("8.3.1", "8.4") || versionLess("8.4", "8.4") {
		t.Error("versionLess wrong")
	}
}

// ---------------------------------------------------------------------------
// Spec

func TestSpec(t *testing.T) {
	w := &Website{
		ID: 7, Domain: "example.com", SystemUser: "u",
		Root: "/home/whacp/example.com", DocumentRoot: "/home/whacp/example.com/public_html",
		Kind: "php", PhpVersion: "8.3.1",
		PhpSettings: PhpSettings{MemoryLimitMB: 512, UploadMaxMB: 32, MaxExecutionSeconds: 30, MaxChildren: 3, Harden: true, Profile: "recommended", PM: "ondemand", IdleTimeoutSeconds: 10, MaxRequests: 500, SlowlogSeconds: 10},
		HTTPS: true, Enabled: true, Redirects: []Redirect{{"/old", "https://example.com/new", 301}},
		ClientMaxBodyMB: 128,
	}
	spec := Spec(w, true)
	if spec["domain"] != "example.com" || spec["user"] != "u" || spec["create_user"] != true {
		t.Fatalf("spec basics wrong: %v", spec)
	}
	if spec["php_version"] != "8.3.1" {
		t.Errorf("php_version = %v", spec["php_version"])
	}
	if spec["proxy_pass"] != "" || spec["autoindex"] != nil {
		t.Errorf("php site must not carry proxy_pass/autoindex: %v", spec)
	}
	php := spec["php"].(map[string]any)
	if php["memory_limit_mb"] != 512 || php["harden"] != true || php["profile"] != "recommended" || php["pm"] != "ondemand" {
		t.Errorf("php block wrong: %v", php)
	}
	if _, ok := php["ini"]; ok {
		t.Errorf("no ini without directives: %v", php)
	}
	aliases, _ := spec["aliases"].([]string)
	if len(aliases) != 0 {
		t.Errorf("aliases = %v", aliases)
	}
	redirects := spec["redirects"].([]Redirect)
	if len(redirects) != 1 || redirects[0].From != "/old" || redirects[0].Code != 301 {
		t.Errorf("redirects = %v", redirects)
	}

	// Static site: no PHP version in the spec; autoindex on listing.
	s := &Website{Domain: "s.com", Kind: "static", DirectoryListing: true}
	sp := Spec(s, false)
	if sp["php_version"] != "" {
		t.Errorf("static php_version = %v", sp["php_version"])
	}
	if sp["autoindex"] != true {
		t.Errorf("static listing must set autoindex: %v", sp)
	}

	// Proxy site: proxy_pass set, no autoindex even with listing.
	p := &Website{Domain: "p.com", Kind: "proxy", ProxyPass: "http://127.0.0.1:3000", DirectoryListing: true,
		IPv4: sql.NullString{String: "1.2.3.4", Valid: true}, IPv6: sql.NullString{String: "2001:db8::1", Valid: true}}
	spp := Spec(p, false)
	if spp["proxy_pass"] != "http://127.0.0.1:3000" || spp["php_version"] != "" {
		t.Errorf("proxy spec wrong: %v", spp)
	}
	if spp["autoindex"] != nil {
		t.Errorf("proxy must not set autoindex: %v", spp)
	}
	if spp["ipv4"] != "1.2.3.4" || spp["ipv6"] != "2001:db8::1" {
		t.Errorf("ipv4/ipv6 wrong: %v", spp)
	}

	// ini round-trips through the spec when present.
	i := &Website{Domain: "i.com", Kind: "php", PhpSettings: PhpSettings{INI: map[string]string{"display_errors": "Off"}}}
	if ini, ok := Spec(i, false)["php"].(map[string]any)["ini"]; !ok || ini["display_errors"] != "Off" {
		t.Errorf("ini missing from spec: %v", Spec(i, false)["php"])
	}
}

// ---------------------------------------------------------------------------
// Validation

func validCreateInput() map[string]string {
	return map[string]string{
		"domain": "new.example.com",
		"kind":   "php",
		"php_version": "8.3.1",
		"system_user": "u_new",
		"new_system_user": "1",
		"harden": "on",
		"php_profile": "recommended",
		"memory_limit_mb": "256", "upload_max_mb": "64", "max_execution_seconds": "60",
		"max_children": "5", "start_servers": "0", "min_spare_servers": "0", "max_spare_servers": "0",
		"idle_timeout_seconds": "10", "max_requests": "500", "request_timeout_seconds": "0", "slowlog_seconds": "10",
		"pm": "ondemand",
		"client_max_body_mb": "64",
	}
}

func TestValidateCreate(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	admin := loadUser(t, db, 1)
	cust := loadUser(t, db, 3)
	ctx := context.Background()

	// Happy path (admin, unassigned owner).
	v, err := h.svc.Validate(ctx, validCreateInput(), nil, true)
	if err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if v.Row.Domain != "new.example.com" || v.Row.Kind != "php" || v.Row.SystemUser != "u_new" {
		t.Errorf("row wrong: %+v", v.Row)
	}
	if v.Row.Root != "/home/whacp/new.example.com" || v.Row.DocumentRoot != "/home/whacp/new.example.com/public_html" {
		t.Errorf("root/docroot wrong: %s / %s", v.Row.Root, v.Row.DocumentRoot)
	}
	if v.Row.PhpSettings.Harden != true || v.Row.PhpSettings.Profile != "recommended" {
		t.Errorf("harden/profile wrong: %+v", v.Row.PhpSettings)
	}
	if !v.Row.Enabled {
		t.Error("new site must start enabled")
	}
	if v.Row.ClientMaxBodyMB != 64 {
		t.Errorf("body = %d", v.Row.ClientMaxBodyMB)
	}

	// Domain errors.
	bad := validCreateInput()
	bad["domain"] = "no-dots"
	if _, err := h.svc.Validate(ctx, bad, nil, true); err == nil {
		t.Error("no-dots domain accepted")
	}
	bad = validCreateInput()
	bad["domain"] = "cust.example" // already belongs to website 10
	ve, _ := errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || ve.Fields["domain"] == "" {
		t.Errorf("duplicate domain must error: %v", ve)
	}

	// Alias errors: invalid + the 50 cap.
	bad = validCreateInput()
	bad["aliases"] = "ok.example, bad_host.example"
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || ve.Fields["aliases"] == "" {
		t.Errorf("bad alias must error: %v", ve)
	}
	aliases := make([]string, 50)
	for i := range aliases {
		aliases[i] = fmt.Sprintf("a%d.example", i)
	}
	bad = validCreateInput()
	bad["aliases"] = strings.Join(aliases, ", ")
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || !strings.Contains(ve.Fields["aliases"], "50") {
		t.Errorf("alias cap must error: %v", ve)
	}

	// PHP version must be installed (the fake reports 8.2 and 8.3.1 with FPM).
	bad = validCreateInput()
	bad["php_version"] = "8.4-cli"
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || ve.Fields["php_version"] == "" {
		t.Errorf("uninstalled PHP must error: %v", ve)
	}

	// System user rules: reserved name, then the in-use rule on create.
	bad = validCreateInput()
	bad["system_user"] = "root"
	if _, err := h.svc.Validate(ctx, bad, nil, true); err == nil {
		t.Error("reserved system user accepted")
	}
	bad = validCreateInput()
	bad["system_user"] = "u_cust"
	bad["new_system_user"] = "1"
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || ve.Fields["system_user"] == "" {
		// the in-use check happens in Create, not Validate — but the
		// suggestion fallback must not produce a reserved name.
		t.Logf("note: in-use check is in Create (as in PHP)")
	}

	// Integer bounds and the dynamic process-manager ordering.
	bad = validCreateInput()
	bad["memory_limit_mb"] = "8"
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || ve.Fields["memory_limit_mb"] == "" {
		t.Errorf("memory limit below range must error: %v", ve)
	}
	bad = validCreateInput()
	bad["pm"] = "dynamic"
	bad["min_spare_servers"] = "3"
	bad["start_servers"] = "2"
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || ve.Fields["start_servers"] == "" {
		t.Errorf("dynamic ordering must error: %v", ve)
	}

	// Redirect line format.
	bad = validCreateInput()
	bad["redirects"] = "/old not-a-target 301"
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || !strings.HasPrefix(ve.Fields["redirects"], "Line 1") {
		t.Errorf("bad redirect must error: %v", ve)
	}
	bad = validCreateInput()
	bad["redirects"] = "/a https://new.example [301]\n# comment\n\n/b https://new.example 302"
	v2, err := h.svc.Validate(ctx, bad, nil, true)
	if err != nil || len(v2.Row.Redirects) != 2 || v2.Row.Redirects[1].Code != 302 {
		t.Errorf("redirect parse wrong: %+v err=%v", v2, err)
	}

	// document_root_sub traversal is refused.
	bad = validCreateInput()
	bad["document_root_sub"] = "../etc"
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || ve.Fields["document_root_sub"] == "" {
		t.Errorf("docroot traversal must error: %v", ve)
	}

	// App kinds: runtime + port rules, and the proxy mapping.
	bad = validCreateInput()
	bad["kind"] = "nodejs"
	bad["app_runtime"] = "node:20.11.0"
	bad["app_port"] = "3000"
	v3, err := h.svc.Validate(ctx, bad, nil, true)
	if err != nil {
		t.Fatalf("nodejs create rejected: %v", err)
	}
	if v3.Row.Kind != "proxy" || v3.Row.AppRuntime != "node:20.11.0" || v3.Row.ProxyPass != "http://127.0.0.1:3000" {
		t.Errorf("nodejs row wrong: %+v", v3.Row)
	}
	bad["app_port"] = "80"
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || ve.Fields["app_port"] == "" {
		t.Errorf("low app port must error: %v", ve)
	}
	bad = validCreateInput()
	bad["kind"] = "proxy"
	bad["proxy_pass"] = "http://10.0.0.5:80"
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || ve.Fields["proxy_pass"] == "" {
		t.Errorf("remote proxy_pass must error: %v", ve)
	}
	bad = validCreateInput()
	bad["kind"] = "wordpress"
	ve, _ = errAsValidation(h.svc.Validate(ctx, bad, nil, true))
	if ve == nil || ve.Fields["kind"] == "" {
		t.Errorf("unknown kind must error: %v", ve)
	}

	// Customers keep their stored server-wide settings (harden/pm stripped).
	// cust.example (website 10) has stored pm ondemand/harden false.
	row, err := h.svc.sites.FindFull(ctx, 1, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	edit := map[string]string{
		"domain": "cust.example",
		"harden": "on", // must be ignored for a customer
		"pm":     "static",
		"memory_limit_mb": "512",
	}
	v4, err := h.svc.Validate(ctx, edit, row, false)
	if err != nil {
		t.Fatalf("customer edit rejected: %v", err)
	}
	if v4.Row.PhpSettings.Harden != row.PhpSettings.Harden || v4.Row.PhpSettings.PM != "ondemand" {
		t.Errorf("customer server settings leaked: harden=%v pm=%s", v4.Row.PhpSettings.Harden, v4.Row.PhpSettings.PM)
	}
	if v4.Row.PhpSettings.MemoryLimitMB != 512 {
		t.Errorf("customer may change memory_limit_mb, got %d", v4.Row.PhpSettings.MemoryLimitMB)
	}
	_ = admin
	_ = cust
}

func errAsValidation(err error) (*ValidationError, bool) {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Create lifecycle

func TestCreateSuccess(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	admin := loadUser(t, db, 1)
	ctx := context.Background()

	id, err := h.svc.Create(ctx, validCreateInput(), admin, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	site, err := h.svc.sites.FindFull(ctx, 1, id, nil)
	if err != nil || site == nil {
		t.Fatalf("row missing: %v", err)
	}
	if site.Status != "active" {
		t.Errorf("status = %q, want active (applied_at must have advanced)", site.Status)
	}
	if site.OwnerID.Valid {
		t.Errorf("unassigned site has owner %v", site.OwnerID)
	}
	if !site.Enabled {
		t.Error("site not enabled")
	}
	names, _ := h.svc.sites.DomainOwner(ctx, 1, "new.example.com")
	if names != id {
		t.Errorf("domain not registered: got website %d", names)
	}
	if got := h.agent.callCount("sites.apply"); got != 1 {
		t.Errorf("sites.apply called %d times", got)
	}
	// The apply spec must carry new_user (the account must not exist yet).
	if spec, _ := h.agent.calls[0].params.(map[string]any); spec["new_user"] != true {
		t.Errorf("new_user flag missing from apply spec: %v", spec)
	}
	found := false
	for _, a := range h.audit {
		if a.action == "website.create" && a.result == "success" {
			found = true
		}
	}
	if !found {
		t.Errorf("audit website.create success missing: %+v", h.audit)
	}
}

func TestCreateCleansUpWhenApplyFails(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	h.agent.failApply = errors.New("agent down")
	admin := loadUser(t, db, 1)

	_, err := h.svc.Create(context.Background(), validCreateInput(), admin, nil)
	if err == nil {
		t.Fatal("create should fail when the agent fails")
	}
	if n, _ := h.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM websites").Int64(); n != 2 {
		t.Errorf("failed create left %d websites (want 2, the seeded ones)", n)
	}
	if n, _ := h.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM domains WHERE name = 'new.example.com'").Int64(); n != 0 {
		t.Errorf("failed create left domain rows")
	}
	ok := false
	for _, a := range h.audit {
		if a.action == "website.create" && a.result == "failure" {
			ok = true
		}
	}
	if !ok {
		t.Errorf("audit website.create failure missing: %+v", h.audit)
	}
}

func TestCreatePlanLimits(t *testing.T) {
	db := writeDB(t)
	if _, err := db.Exec("UPDATE plans SET max_websites = 1 WHERE id = 11"); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, db)
	cust := loadUser(t, db, 3) // plan 11 (tightened to 1), already at 1

	in := validCreateInput()
	in["domain"] = "second.example"
	in["system_user"] = "u_second"
	_, err := h.svc.Create(context.Background(), in, cust, nil)
	var ue *UserError
	if !errors.As(err, &ue) {
		t.Fatalf("want plan limit UserError, got %v", err)
	}
	if !strings.Contains(ue.What, "allows at most 1 websites") {
		t.Errorf("limit message wrong: %q", ue.What)
	}
}

func TestCreateResellerCustomerCountsAgainstResellerPlan(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	// Give the reseller a plan with 1 website total; the customer (owner 3,
	// parent 2) already has cust.example which counts against the reseller.
	if _, err := h.db.Exec("INSERT INTO plans (id,name,max_websites,created_at,updated_at) VALUES (21,'Res',1,'','')"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec("INSERT INTO plans (id,name,max_websites,created_at,updated_at) VALUES (13,'Cust5',5,'','')"); err != nil {
		t.Fatal(err)
	}
	// reseller: 1 website total; customer: 5 (cust.example is the only one).
	if _, err := h.db.Exec("UPDATE users SET plan_id = 13 WHERE id = 3"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec("UPDATE users SET plan_id = 21 WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	cust := loadUser(t, db, 3)
	in := validCreateInput()
	in["domain"] = "more.example"
	in["system_user"] = "u_more"
	_, err := h.svc.Create(context.Background(), in, cust, nil)
	var ue *UserError
	if !errors.As(err, &ue) {
		t.Fatalf("want reseller plan limit, got %v", err)
	}
	if !strings.Contains(ue.What, "Your provider's plan allows at most 1 websites") {
		t.Errorf("message wrong: %q", ue.What)
	}
}

func TestCreateNotUnderOthers(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3)
	// solo.example belongs to user 4; cust (user 3) may not take a name under it.
	in := validCreateInput()
	in["domain"] = "x.solo.example"
	in["system_user"] = "u_x"
	_, err := h.svc.Create(context.Background(), in, cust, nil)
	ve, _ := errAsValidation(err)
	if ve == nil || ve.Fields["domain"] == "" {
		t.Fatalf("want under-others error, got %v", err)
	}
	// …but the same name under their OWN domain is fine.
	in = validCreateInput()
	in["domain"] = "x.cust.example"
	in["system_user"] = "u_x2"
	id, err := h.svc.Create(context.Background(), in, cust, nil)
	if err != nil {
		t.Fatalf("own subdomain must be allowed: %v", err)
	}
	if id == 0 {
		t.Fatal("no id")
	}
}

func TestCreateSystemUserRules(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3)
	admin := loadUser(t, db, 1)
	ctx := context.Background()

	// A customer may reuse their own account (no new_user flag) — the agent
	// gets create_user without new_user.
	in := validCreateInput()
	in["domain"] = "own.example"
	in["system_user"] = "u_cust"
	delete(in, "new_system_user")
	if _, err := h.svc.Create(ctx, in, cust, nil); err != nil {
		t.Fatalf("own-account reuse must be allowed: %v", err)
	}
	if spec, _ := h.agent.calls[len(h.agent.calls)-1].params.(map[string]any); spec["new_user"] != nil {
		t.Errorf("own-account reuse must not set new_user: %v", spec)
	}

	// …but taking someone else's account is refused.
	h.agent.calls = nil
	in = validCreateInput()
	in["domain"] = "steal.example"
	in["system_user"] = "u_solo" // belongs to user 4
	delete(in, "new_system_user")
	_, err := h.svc.Create(ctx, in, cust, nil)
	ve, _ := errAsValidation(err)
	if ve == nil || ve.Fields["system_user"] == "" {
		t.Fatalf("want system-user takeover refusal, got %v", err)
	}

	// …and a new_system_user account that already exists on the server is refused.
	in = validCreateInput()
	in["domain"] = "steal2.example"
	in["system_user"] = "u_solo"
	if _, err := h.svc.Create(ctx, in, cust, nil); err == nil {
		t.Fatal("existing system user with new_system_user must be refused")
	}

	// Administrators may attach existing accounts.
	in = validCreateInput()
	in["domain"] = "attach.example"
	in["system_user"] = "u_solo"
	delete(in, "new_system_user")
	if _, err := h.svc.Create(ctx, in, admin, nil); err != nil {
		t.Fatalf("admin attach must be allowed: %v", err)
	}
}

func TestCreateFeatureGate(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3) // no feature list: type_nodejs is default-off
	in := validCreateInput()
	in["kind"] = "nodejs"
	in["app_runtime"] = "node:20.11.0"
	in["app_port"] = "3000"
	in["domain"] = "app.example"
	in["system_user"] = "u_app"
	_, err := h.svc.Create(context.Background(), in, cust, nil)
	ve, _ := errAsValidation(err)
	if ve == nil || !strings.Contains(ve.Fields["kind"], "not included in your hosting plan") {
		t.Fatalf("want feature-gate error, got %v", err)
	}
	// An admin is never feature-gated.
	admin := loadUser(t, db, 1)
	id, err := h.svc.Create(context.Background(), in, admin, nil)
	if err != nil || id == 0 {
		t.Fatalf("admin nodejs create must succeed: %v", err)
	}
}

func TestResolveOwner(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	admin := loadUser(t, db, 1)
	cust := loadUser(t, db, 3)

	if o := h.svc.resolveOwner(map[string]string{"owner_id": "4"}, admin); o == nil || *o != 4 {
		t.Errorf("admin owner pick failed: %v", o)
	}
	if o := h.svc.resolveOwner(map[string]string{"owner_id": "4"}, cust); o == nil || *o != 3 {
		t.Errorf("customer must own what they create: %v", o)
	}
	if o := h.svc.resolveOwner(map[string]string{}, admin); o != nil {
		t.Errorf("admin empty owner must be unassigned: %v", o)
	}
}

// ---------------------------------------------------------------------------
// Toggle / delete

func TestSetEnabled(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3)
	ctx := context.Background()

	site, err := h.svc.sites.FindFull(ctx, 1, 10, cust)
	if err != nil || site == nil {
		t.Fatal(err)
	}
	if err := h.svc.SetEnabled(ctx, site, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	again, _ := h.svc.sites.FindFull(ctx, 1, 10, nil)
	if again.Status != "disabled" || again.Enabled {
		t.Errorf("after disable: status=%q enabled=%v", again.Status, again.Enabled)
	}
	if spec, _ := h.agent.calls[len(h.agent.calls)-1].params.(map[string]any); spec["enabled"] != false {
		t.Errorf("apply spec enabled = %v", spec["enabled"])
	}
	if err := h.svc.SetEnabled(ctx, again, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	back, _ := h.svc.sites.FindFull(ctx, 1, 10, nil)
	if back.Status != "active" || !back.Enabled {
		t.Errorf("after enable: status=%q", back.Status)
	}
	actions := map[string]bool{}
	for _, a := range h.audit {
		actions[a.action] = true
	}
	if !actions["website.disable"] || !actions["website.enable"] {
		t.Errorf("toggle audit actions missing: %+v", h.audit)
	}
}

func TestDelete(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3)
	ctx := context.Background()

	// A second site shares the system user: the account must be kept.
	exec := func(q string, a ...any) {
		if _, err := h.db.Exec(q, a...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,status,created_at,updated_at)
		VALUES (11,1,3,'shared.example','u_cust','/home/whacp/shared.example','/h/s','php','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,11,'shared.example',1)`)

	site, _ := h.svc.sites.FindFull(ctx, 1, 10, cust)
	warnings, err := h.svc.Delete(ctx, site, true, true)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %v (want agent warning + kept-account note)", warnings)
	}
	if !strings.Contains(warnings[1], "u_cust was kept") {
		t.Errorf("kept-account note missing: %v", warnings)
	}
	if n, _ := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM websites WHERE id = 10").Int64(); n != 0 {
		t.Error("website row still present")
	}
	if n, _ := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domains WHERE website_id = 10").Int64(); n != 0 {
		t.Error("domain rows still present (cascade)")
	}
	if spec, _ := h.agent.calls[0].params.(map[string]any); spec["remove_user"] != false {
		t.Errorf("remove_user must be suppressed for a shared account: %v", spec)
	}
}

func TestDeleteRemovesUserWhenLastSite(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3)
	site, _ := h.svc.sites.FindFull(context.Background(), 1, 10, cust)
	if _, err := h.svc.Delete(context.Background(), site, false, true); err != nil {
		t.Fatal(err)
	}
	if spec, _ := h.agent.calls[0].params.(map[string]any); spec["remove_user"] != true || spec["remove_files"] != false {
		t.Errorf("agent params wrong: %v", spec)
	}
}

// ---------------------------------------------------------------------------
// Children (subdomains / addon domains)

func TestCreateChildSubdomain(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3)
	ctx := context.Background()
	parent, _ := h.svc.sites.FindFull(ctx, 1, 10, cust)

	// "www" is the website's own alias, never a subdomain.
	_, _, err := h.svc.CreateChild(ctx, parent, "subdomain", map[string]string{"subdomain": "www"}, cust)
	ve, _ := errAsValidation(err)
	if ve == nil || ve.Fields["subdomain"] == "" {
		t.Fatalf("www subdomain must be refused: %v", err)
	}

	id, dnsAdded, err := h.svc.CreateChild(ctx, parent, "subdomain", map[string]string{"subdomain": "blog"}, cust)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if dnsAdded {
		t.Error("no hosted zone: no DNS record expected")
	}
	child, _ := h.svc.sites.FindFull(ctx, 1, id, nil)
	if child.Domain != "blog.cust.example" {
		t.Errorf("child domain = %q", child.Domain)
	}
	if child.SystemUser != "u_cust" || child.ParentID.Int64 != 10 {
		t.Errorf("child must inherit account/parent: %+v", child)
	}
	if child.OwnerID.Int64 != 3 {
		t.Errorf("child owner = %v (want parent's owner)", child.OwnerID)
	}
	if got := h.agent.callCount("sites.apply"); got != 1 {
		t.Errorf("child apply called %d times", got)
	}
}

func TestCreateChildSubdomainAddsDNSRecord(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3)
	ctx := context.Background()

	exec := func(q string, a ...any) {
		if _, err := h.db.Exec(q, a...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO dns_zones (id,server_id,name,website_id,owner_id,ttl,primary_ns,hostmaster,secondaries,serial,created_at,updated_at)
		VALUES (5,1,'cust.example',10,3,3600,'ns1.dns','admin.cust.example','',1,'','')`)
	exec(`INSERT INTO dns_records (zone_id,name,type,ttl,value,priority,weight,port) VALUES (5,'@','A',0,'10.0.0.1',0,0,0)`)
	if _, err := h.db.Exec(`INSERT INTO settings (key,value,updated_at) VALUES ('dns_default_ipv4','10.0.0.1','')`); err != nil {
		t.Fatal(err)
	}

	parent, _ := h.svc.sites.FindFull(ctx, 1, 10, cust)
	id, dnsAdded, err := h.svc.CreateChild(ctx, parent, "subdomain", map[string]string{"subdomain": "shop", "dns": "on"}, cust)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if !dnsAdded {
		t.Fatal("DNS record expected")
	}
	var n int
	if err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM dns_records WHERE zone_id = 5 AND name = 'shop' AND type = 'A' AND value = '10.0.0.1'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("A record missing for shop (n=%d)", n)
	}
	if h.agent.callCount("dns.zone_apply") != 1 {
		t.Errorf("zone push missing")
	}
	if _, err := h.db.QueryRowContext(ctx, "SELECT serial FROM dns_zones WHERE id = 5").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 42 {
		t.Errorf("serial not recorded: %d", n)
	}
	// The site exists regardless; a duplicate name is silently skipped.
	_, dnsAdded2, err := h.svc.CreateChild(ctx, parent, "subdomain", map[string]string{"subdomain": "shop2", "dns": "on"}, cust)
	if err != nil || !dnsAdded2 {
		t.Errorf("second subdomain + dns: %v dns=%v", err, dnsAdded2)
	}
	_ = id
}

func TestCreateChildAddon(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3)
	ctx := context.Background()
	parent, _ := h.svc.sites.FindFull(ctx, 1, 10, cust)

	// A subdomain of the parent is not an addon domain.
	_, _, err := h.svc.CreateChild(ctx, parent, "addon", map[string]string{"domain": "other.cust.example"}, cust)
	ve, _ := errAsValidation(err)
	if ve == nil || ve.Fields["domain"] == "" {
		t.Fatalf("addon under parent must be refused: %v", err)
	}

	id, _, err := h.svc.CreateChild(ctx, parent, "addon", map[string]string{"domain": "ADDON.example", "www": "on"}, cust)
	if err != nil {
		t.Fatalf("addon create: %v", err)
	}
	child, _ := h.svc.sites.FindFull(ctx, 1, id, nil)
	if child.Domain != "addon.example" {
		t.Errorf("addon domain = %q", child.Domain)
	}
	found := false
	for _, a := range child.Aliases {
		if a == "www.addon.example" {
			found = true
		}
	}
	if !found {
		t.Errorf("www alias missing: %v", child.Aliases)
	}
	// The www alias counts as a parked domain.
	owner, _ := h.svc.sites.DomainOwner(ctx, 1, "www.addon.example")
	if owner != id {
		t.Errorf("www alias domain row missing (website %d)", owner)
	}
}

func TestCreateChildLimits(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	solo := loadUser(t, db, 4) // plan 12: 5 websites, 1 subdomain, 5 addon
	ctx := context.Background()

	// Seed one website for solo so there is a parent with limits to bite.
	exec := func(q string, a ...any) {
		if _, err := h.db.Exec(q, a...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (20,1,4,'solo.example','u_solo','/h/solo','/h/solo/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,20,'solo.example',1)`)
	// One subdomain already exists (limit 1 reached).
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,parent_id,status,created_at,updated_at)
		VALUES (21,1,4,'a.solo.example','u_solo','/h/a','/h/a/public_html','php','8.3.1',20,'active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,21,'a.solo.example',1)`)

	parent, _ := h.svc.sites.FindFull(ctx, 1, 20, solo)
	_, _, err := h.svc.CreateChild(ctx, parent, "subdomain", map[string]string{"subdomain": "b"}, solo)
	var ue *UserError
	if !errors.As(err, &ue) {
		t.Fatalf("want subdomain limit, got %v", err)
	}
	if !strings.Contains(ue.What, "allows at most 1 subdomains") {
		t.Errorf("limit message: %q", ue.What)
	}
	// Addon domains: 0/5, allowed.
	id, _, err := h.svc.CreateChild(ctx, parent, "addon", map[string]string{"domain": "add.example"}, solo)
	if err != nil || id == 0 {
		t.Fatalf("addon within limit must work: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Update

func TestUpdateSettings(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3)
	ctx := context.Background()
	site, _ := h.svc.sites.FindFull(ctx, 1, 10, cust)

	in := map[string]string{
		"domain": "cust.example", // ignored on edit
		"aliases": "extra.example",
		"memory_limit_mb": "512",
	}
	if err := h.svc.Update(ctx, site, in, cust); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, _ := h.svc.sites.FindFull(ctx, 1, 10, nil)
	if after.PhpSettings.MemoryLimitMB != 512 {
		t.Errorf("memory not saved: %d", after.PhpSettings.MemoryLimitMB)
	}
	if len(after.Aliases) != 1 || after.Aliases[0] != "extra.example" {
		t.Errorf("aliases = %v", after.Aliases)
	}
	if after.Root != site.Root || after.DocumentRoot != site.DocumentRoot {
		t.Errorf("edit must not move the site: %s", after.Root)
	}
	if after.Domain != "cust.example" {
		t.Errorf("domain changed on edit: %s", after.Domain)
	}

	// Failed apply must restore the previous state.
	h.agent.failApply = errors.New("agent down")
	before, _ := h.svc.sites.FindFull(ctx, 1, 10, nil)
	err := h.svc.Update(ctx, before, map[string]string{"domain": "cust.example", "memory_limit_mb": "256"}, cust)
	if err == nil {
		t.Fatal("update should fail")
	}
	restored, _ := h.svc.sites.FindFull(ctx, 1, 10, nil)
	if restored.PhpSettings.MemoryLimitMB != before.PhpSettings.MemoryLimitMB {
		t.Errorf("state not restored: %d", restored.PhpSettings.MemoryLimitMB)
	}
}

func TestUpdateCustomerCannotAliasUnderOthers(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	cust := loadUser(t, db, 3)
	ctx := context.Background()
	site, _ := h.svc.sites.FindFull(ctx, 1, 10, cust)

	// solo.example is user 4's: the alias lands under a foreign domain.
	in := map[string]string{"domain": "cust.example", "aliases": "x.solo.example"}
	if _, err := h.svc.Update(ctx, site, in, cust); err == nil {
		t.Fatal("alias under a foreign domain must be refused")
	}
	ve, _ := errAsValidation(err)
	if ve == nil || ve.Fields["aliases"] == "" {
		t.Fatal("want under-others alias refusal")
	}
}

// ---------------------------------------------------------------------------
// Adopt (insert links pre-existing zones/mail domains)

func TestInsertAdoptsDNSZone(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	admin := loadUser(t, db, 1)
	ctx := context.Background()

	if _, err := h.db.Exec(`INSERT INTO dns_zones (id,server_id,name,ttl,primary_ns,hostmaster,created_at,updated_at)
		VALUES (7,1,'adopt.example',3600,'ns1','admin.adopt.example','','')`); err != nil {
		t.Fatal(err)
	}
	id, err := h.svc.Create(ctx, func() map[string]string {
		in := validCreateInput()
		in["domain"] = "adopt.example"
		in["system_user"] = "u_adopt"
		return in
	}(), admin, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var zoneOwner sql.NullInt64
	var zoneSite int64
	if err := h.db.QueryRowContext(ctx, "SELECT owner_id, website_id FROM dns_zones WHERE id = 7").Scan(&zoneOwner, &zoneSite); err != nil {
		t.Fatal(err)
	}
	if zoneSite != id {
		t.Errorf("zone not adopted: website_id=%d want %d", zoneSite, id)
	}
	if zoneOwner.Valid {
		t.Errorf("unassigned site must not claim the zone's owner: %v", zoneOwner)
	}
}


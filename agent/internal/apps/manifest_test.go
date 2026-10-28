package apps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// goodManifest is a valid testing-status binary service, the shape a future
// native application takes.
func goodManifest() Manifest {
	return Manifest{
		Schema: ManifestSchema, ID: "demo-app", Name: "Demo App", Kind: KindApplication, Category: "developer-tools",
		Description: "A demo.", Status: StatusTesting,
		Runtime:  RuntimeSpec{Type: "php", Min: "8.1", Max: "8.4", Extensions: []string{"pdo_mysql"}},
		Database: DatabaseSpec{Type: "mysql"}, Strategy: "archive",
		Source:  SourceSpec{Kind: "url", URL: "https://github.com/acme/demo/releases/download/v1/demo.zip", Integrity: "sha256-sidecar"},
		Secrets: []string{"APP_KEY"}, Health: HealthSpec{Type: "file"},
		Backup: "files-and-database", Upgrade: "none", Uninstall: "none",
		License: LicenseSpec{SPDX: "MIT", URL: "https://github.com/acme/demo/blob/main/LICENSE"},
		Compat:  Compat{Arch: []string{"amd64", "arm64"}, MinDiskMB: 100},
	}
}

func hasError(errs []string, want string) bool {
	for _, e := range errs {
		if strings.Contains(e, want) {
			return true
		}
	}
	return false
}

func TestValidManifestPasses(t *testing.T) {
	if errs := goodManifest().Validate(); len(errs) != 0 {
		t.Fatalf("a good manifest was refused: %v", errs)
	}
}

func TestInvalidManifestsAreRefused(t *testing.T) {
	cases := map[string]struct {
		mut  func(*Manifest)
		want string
	}{
		"schema":           {func(m *Manifest) { m.Schema = 9 }, "schema"},
		"id":               {func(m *Manifest) { m.ID = "Bad ID;rm" }, "id must"},
		"name":             {func(m *Manifest) { m.Name = "" }, "name is required"},
		"status":           {func(m *Manifest) { m.Status = "live" }, "status must"},
		"kind":             {func(m *Manifest) { m.Kind = "tool" }, "kind must"},
		"strategy":         {func(m *Manifest) { m.Strategy = "curl-pipe-bash" }, "not one of the trusted"},
		"runtime type":     {func(m *Manifest) { m.Runtime.Type = "ruby" }, "runtime type"},
		"runtime/strategy": {func(m *Manifest) { m.Runtime.Type = "python" }, "does not support the python runtime"},
		"version format":   {func(m *Manifest) { m.Runtime.Min = "eight" }, "runtime versions"},
		"version order":    {func(m *Manifest) { m.Runtime.Min, m.Runtime.Max = "8.4", "8.1" }, "min is above max"},
		"extension name":   {func(m *Manifest) { m.Runtime.Extensions = []string{"x; rm -rf /"} }, "extension name"},
		"os package":       {func(m *Manifest) { m.Runtime.OSPackages = []string{"$(id)"} }, "operating-system package"},
		"database":         {func(m *Manifest) { m.Database.Type = "oracle" }, "database type"},
		"source kind":      {func(m *Manifest) { m.Source.Kind = "ftp" }, "source kind"},
		"strategy source": {func(m *Manifest) {
			m.Source = SourceSpec{Kind: "pypi", Package: "django", Integrity: "https-only", IntegrityNote: "x"}
		}, "cannot take its application"},
		"http url":           {func(m *Manifest) { m.Source.URL = "http://github.com/a/b.zip" }, "https URL"},
		"untrusted host":     {func(m *Manifest) { m.Source.URL = "https://evil.example/app.zip" }, "not a trusted download source"},
		"ip host":            {func(m *Manifest) { m.Source.URL = "https://203.0.113.9/app.zip" }, "not an IP address"},
		"credentials":        {func(m *Manifest) { m.Source.URL = "https://user:pw@github.com/a/b.zip" }, "credentials"},
		"odd port":           {func(m *Manifest) { m.Source.URL = "https://github.com:8443/a/b.zip" }, "default https port"},
		"lookalike host":     {func(m *Manifest) { m.Source.URL = "https://github.com.evil.example/a.zip" }, "not a trusted download source"},
		"integrity kind":     {func(m *Manifest) { m.Source.Integrity = "trust-me" }, "source integrity"},
		"https-only note":    {func(m *Manifest) { m.Source.Integrity = "https-only" }, "integrity_note"},
		"secret name":        {func(m *Manifest) { m.Secrets = []string{"db password"} }, "secret name"},
		"duplicate secret":   {func(m *Manifest) { m.Secrets = []string{"A_B", "A_B"} }, "secret name"},
		"health type":        {func(m *Manifest) { m.Health.Type = "shell" }, "health type"},
		"http health path":   {func(m *Manifest) { m.Health = HealthSpec{Type: "http", Path: "health"} }, "health path"},
		"lifecycle":          {func(m *Manifest) { m.Upgrade = "sh -c 'curl x | sh'" }, "not a trusted lifecycle"},
		"licence":            {func(m *Manifest) { m.License.SPDX = "" }, "license"},
		"licence unknown":    {func(m *Manifest) { m.License.SPDX = "NOASSERTION" }, "licence recorded"},
		"licence url":        {func(m *Manifest) { m.License.URL = "http://evil.example/l" }, "license url"},
		"arch":               {func(m *Manifest) { m.Compat.Arch = []string{"mips"} }, "architecture"},
		"enable unverified":  {func(m *Manifest) { m.Status = StatusEnabled }, "real-host installation test"},
		"service on archive": {func(m *Manifest) { m.Service = &ServiceSpec{Type: "worker", Proxy: true} }, "has no service"},
	}
	for name, c := range cases {
		m := goodManifest()
		c.mut(&m)
		errs := m.Validate()
		if !hasError(errs, c.want) {
			t.Errorf("%s: want an error mentioning %q, got %v", name, c.want, errs)
		}
	}
}

func TestStatusGates(t *testing.T) {
	// Enabled needs verification.
	m := goodManifest()
	m.Status, m.Verified = StatusEnabled, "realhost:app-install-demo"
	if errs := m.Validate(); len(errs) != 0 {
		t.Fatalf("a verified manifest can be enabled: %v", errs)
	}
	// A strategy the agent does not implement can only be a draft.
	p := goodManifest()
	p.Strategy, p.Runtime = "python-venv", RuntimeSpec{Type: "python", Min: "3.10"}
	p.Source = SourceSpec{Kind: "pypi", Package: "fastapi", Integrity: "https-only", IntegrityNote: "pip verifies nothing beyond TLS"}
	p.Service = &ServiceSpec{Type: "worker", Proxy: true}
	p.Health = HealthSpec{Type: "http", Path: "/healthz"}
	p.Database = DatabaseSpec{Type: "none"}
	p.Status = StatusTesting
	if !StrategyImplemented("python-venv") {
		if errs := p.Validate(); !hasError(errs, "not implemented yet") {
			t.Errorf("an unimplemented strategy cannot be testing: %v", errs)
		}
		p.Status = StatusDraft
		if errs := p.Validate(); len(errs) != 0 {
			t.Errorf("but it can be a draft: %v", errs)
		}
	}
	// A downloaded program must be verified by a checksum or signature.
	b := goodManifest()
	b.Strategy, b.Runtime = "binary-service", RuntimeSpec{Type: "binary"}
	b.Database = DatabaseSpec{Type: "sqlite"}
	b.Service = &ServiceSpec{Type: "worker", Proxy: true}
	b.Health = HealthSpec{Type: "http", Path: "/healthz"}
	b.Status = StatusDraft
	if errs := b.Validate(); len(errs) != 0 {
		t.Errorf("a binary service with a checksum is valid: %v", errs)
	}
	b.Source.Integrity, b.Source.IntegrityNote = "https-only", "nothing published"
	if errs := b.Validate(); !hasError(errs, "needs a checksum or signature") {
		t.Errorf("an executable download needs more than https: %v", errs)
	}
	// Who may install by status.
	for st, want := range map[Status][2]bool{
		StatusDraft: {false, false}, StatusTesting: {false, true}, StatusEnabled: {true, true}, StatusDeprecated: {false, false}, StatusDisabled: {false, false},
	} {
		if st.Installable(false) != want[0] || st.Installable(true) != want[1] {
			t.Errorf("%s: installable customer/admin = %v/%v, want %v", st, st.Installable(false), st.Installable(true), want)
		}
	}
}

func TestRuntimeBounds(t *testing.T) {
	r := RuntimeSpec{Type: "php", Min: "8.1", Max: "8.4"}
	for v, want := range map[string]bool{"7.4": false, "8.0": false, "8.1": true, "8.3": true, "8.4": true, "8.5": false, "10.0": false} {
		if r.Supports(v) != want {
			t.Errorf("PHP %s: %v, want %v", v, r.Supports(v), want)
		}
	}
	if !(RuntimeSpec{Type: "php"}).Supports("5.6") {
		t.Error("no bounds means any version")
	}
}

// Every application the installer offers has a manifest that validates, so an
// entry added without the required information fails here, not at install time.
func TestEveryCatalogApplicationHasAValidManifest(t *testing.T) {
	all := AllManifests()
	if len(all) < len(Catalog)+2 {
		t.Fatalf("only %d manifests for %d catalog entries", len(all), len(Catalog))
	}
	seen := map[string]bool{}
	for _, m := range all {
		if seen[m.ID] {
			t.Errorf("duplicate manifest %s", m.ID)
		}
		seen[m.ID] = true
		if errs := m.Validate(); len(errs) != 0 {
			t.Errorf("%s: %v", m.ID, errs)
		}
	}
	for slug := range Catalog {
		if _, ok := ManifestFor(slug); !ok {
			t.Errorf("no manifest for %s", slug)
		}
	}
	// WordPress plugins are extensions, not applications.
	for name := range WPPlugins {
		m, ok := ManifestFor("wp-" + name)
		if !ok || m.Kind != KindExtension {
			t.Errorf("plugin %s must be an extension manifest", name)
		}
	}
	// Legacy entries are the only ones allowed an unrecorded licence.
	for _, m := range all {
		if m.License.SPDX == "NOASSERTION" && m.Verified != "legacy" {
			t.Errorf("%s: a new entry must record its licence", m.ID)
		}
	}
}

func TestManifestJSONRoundTrip(t *testing.T) {
	b, err := json.Marshal(goodManifest())
	if err != nil {
		t.Fatal(err)
	}
	var back Manifest
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if errs := back.Validate(); len(errs) != 0 {
		t.Errorf("after a round trip: %v", errs)
	}
}

func TestPlanIsReadOnlyAndReportsRequirements(t *testing.T) {
	h, _ := setup(t)
	req := request()
	req.App, req.Root = "joomla", "/home/nimvor/shop.test"
	cred, _, _ := h.Env.Credential("example")
	before := len(h.Runner.Calls)

	oldDisk := diskFree
	t.Cleanup(func() { diskFree = oldDisk })
	diskFree = func(string) (uint64, error) { return 10 << 30, nil }
	p := BuildPlan(context.Background(), h.Env, req, cred)
	if !p.OK || p.Name != "Joomla" || p.Status != StatusEnabled {
		t.Fatalf("plan: %+v", p)
	}
	for _, want := range []string{"Create database", "Download", "Write files", "Finish", "Record"} {
		found := false
		for _, s := range p.Steps {
			found = found || s.Action == want
		}
		if !found {
			t.Errorf("no %q step in %+v", want, p.Steps)
		}
	}
	// It changed nothing: the only host calls are the read-only PHP check.
	for _, c := range h.Runner.Calls[before:] {
		if !strings.HasPrefix(c, "php8.3 -r") {
			t.Errorf("a plan ran %q", c)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(h.Base, "home/nimvor/shop.test/public_html")); len(entries) != 1 {
		t.Errorf("a plan touched the folder: %v", entries)
	}

	// Insufficient disk space.
	diskFree = func(string) (uint64, error) { return 50 << 20, nil }
	p = BuildPlan(context.Background(), h.Env, req, cred)
	if p.OK || !hasError(p.Conflicts, "Enough free disk space") {
		t.Errorf("low disk space must be a conflict: %+v", p.Conflicts)
	}
	// Missing PHP extension.
	diskFree = func(string) (uint64, error) { return 10 << 30, nil }
	h.Fail["php8.3 -r"] = "mysqli"
	p = BuildPlan(context.Background(), h.Env, req, cred)
	if p.OK || !hasError(p.Conflicts, "PHP extensions are installed") {
		t.Errorf("a missing extension must be a conflict: %+v", p.Conflicts)
	}
	delete(h.Fail, "php8.3 -r")
	// A folder that already has files.
	os.WriteFile(filepath.Join(h.Base, "home/nimvor/shop.test/public_html/existing.php"), []byte("x"), 0o644)
	if p = BuildPlan(context.Background(), h.Env, req, cred); p.OK || !hasError(p.Conflicts, "Folder is empty") {
		t.Errorf("a used folder must be a conflict: %+v", p.Conflicts)
	}
	// Unknown applications.
	req.App = "no-such-app"
	if p = BuildPlan(context.Background(), h.Env, req, cred); p.OK {
		t.Error("an unknown application cannot be planned")
	}
}

func TestPlanRefusesAnApplicationThatCannotBeInstalled(t *testing.T) {
	h, _ := setup(t)
	cred, _, _ := h.Env.Credential("example")
	oldDisk := diskFree
	t.Cleanup(func() { diskFree = oldDisk })
	diskFree = func(string) (uint64, error) { return 10 << 30, nil }
	for _, st := range []Status{StatusDraft, StatusDeprecated, StatusDisabled} {
		m := goodManifest()
		m.ID, m.Status = "parked", st
		overrideManifests["parked"] = m
		req := request()
		req.App, req.Root = "parked", "/home/nimvor/shop.test"
		p := BuildPlan(context.Background(), h.Env, req, cred)
		if p.OK || !hasError(p.Conflicts, "Application can be installed") {
			t.Errorf("%s must not be installable: %+v", st, p.Conflicts)
		}
	}
	m := goodManifest()
	m.ID, m.Status = "parked", StatusTesting
	overrideManifests["parked"] = m
	req := request()
	req.App, req.Root = "parked", "/home/nimvor/shop.test"
	if p := BuildPlan(context.Background(), h.Env, req, cred); p.Status != StatusTesting || hasError(p.Conflicts, "Application can be installed") {
		t.Errorf("a testing entry is plannable: %+v", p.Conflicts)
	}
	// A manifest that does not validate is reported, whatever its status.
	m.Source.URL = "https://evil.example/x.zip"
	overrideManifests["parked"] = m
	if p := BuildPlan(context.Background(), h.Env, req, cred); p.OK || !hasError(p.Conflicts, "Catalog entry is valid") {
		t.Errorf("an invalid manifest must be a conflict: %+v", p.Conflicts)
	}
	delete(overrideManifests, "parked")
}

func TestInstallTargetsAreClaimedOneRequestAtATime(t *testing.T) {
	if !claimInstall("a.test|public_html") {
		t.Fatal("the first claim is granted")
	}
	if claimInstall("a.test|public_html") {
		t.Error("a second claim for the same target must be refused")
	}
	if !claimInstall("a.test|blog") || !claimInstall("b.test|public_html") {
		t.Error("other folders and websites are independent")
	}
	releaseInstall("a.test|public_html")
	if !claimInstall("a.test|public_html") {
		t.Error("a released target can be claimed again")
	}
	for _, k := range []string{"a.test|public_html", "a.test|blog", "b.test|public_html"} {
		releaseInstall(k)
	}
	// Under concurrency exactly one wins.
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if claimInstall("race.test|app") {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	releaseInstall("race.test|app")
	if wins != 1 {
		t.Errorf("%d concurrent requests were granted the same target", wins)
	}
}

func TestDuplicateInstallRequestsDoNotInstallTwice(t *testing.T) {
	h, r := setup(t)
	a, _ := r.Lookup("apps.install")
	Catalog["dupapp"] = AppDef{Name: "Dup App", URL: "https://github.com/acme/dup/releases/download/v1/dup.tar.gz", Format: "targz", Strip: 1, NoDB: true, Finish: "/"}
	t.Cleanup(func() { delete(Catalog, "dupapp") })
	// Hold the claim as a running job would; the second request is refused
	// before it reaches the job queue, and nothing is written.
	key := "shop.test|public_html"
	if !claimInstall(key) {
		t.Fatal("setup claim")
	}
	t.Cleanup(func() { releaseInstall(key) })
	req := request()
	req.App, req.Root = "dupapp", "/home/nimvor/shop.test"
	b, _ := json.Marshal(req)
	_, err := a.Handler(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("a duplicate request must be refused as already in progress: %v", err)
	}
	if active := h.Env.Jobs.Active(); len(active) != 0 {
		t.Errorf("a refused request started %d jobs", len(active))
	}
}

func TestInstallRefusesWhatTheManifestDoesNotAllow(t *testing.T) {
	cases := map[string]struct {
		status  Status
		testing bool
		bad     bool
		refused bool
	}{
		"draft":                 {StatusDraft, true, false, true},
		"deprecated":            {StatusDeprecated, true, false, true},
		"disabled":              {StatusDisabled, true, false, true},
		"testing for customers": {StatusTesting, false, false, true},
		"testing for admins":    {StatusTesting, true, false, false},
		"enabled":               {StatusEnabled, false, false, false},
		"invalid manifest":      {StatusTesting, true, true, true},
	}
	for name, c := range cases {
		m := goodManifest()
		m.Status = c.status
		if c.status == StatusEnabled {
			m.Verified = "realhost:test"
		}
		if c.bad {
			m.Source.URL = "https://evil.example/x.zip"
		}
		overrideManifests["gate"] = m
		err := checkInstallable(Request{App: "gate", AllowTesting: c.testing})
		if (err != nil) != c.refused {
			t.Errorf("%s: err=%v, refused should be %v", name, err, c.refused)
		}
	}
	delete(overrideManifests, "gate")
	if err := checkInstallable(Request{App: "no-such"}); err == nil {
		t.Error("an unknown application is refused")
	}
}

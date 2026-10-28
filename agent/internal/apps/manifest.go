package apps

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// A Manifest describes one installable application as data: what it needs,
// where it comes from, how it is verified, how it runs. It never contains a
// command. How an application is installed is chosen by Strategy, one of the
// fixed set of trusted procedures below; a manifest only selects a procedure
// and gives it values that Validate has checked.

// ManifestSchema is the current manifest layout. A manifest with another
// schema is refused rather than guessed at.
const ManifestSchema = 1

// Status is where an application is in its life in the catalog.
type Status string

const (
	// StatusDraft: being written; cannot be installed by anyone.
	StatusDraft Status = "draft"
	// StatusTesting: implemented but not yet proven by a real-host
	// installation test; administrators only.
	StatusTesting Status = "testing"
	// StatusEnabled: proven; may be offered to customers.
	StatusEnabled Status = "enabled"
	// StatusDeprecated: still installed where it is, no new installations.
	StatusDeprecated Status = "deprecated"
	// StatusDisabled: switched off.
	StatusDisabled Status = "disabled"
)

// Installable reports whether an application in this status may be installed
// by an administrator (testing) or by anyone the panel permits (enabled).
func (s Status) Installable(administrator bool) bool {
	return s == StatusEnabled || (s == StatusTesting && administrator)
}

// Kind separates standalone applications from extensions that install into an
// existing application (a WordPress plugin).
type Kind string

const (
	KindApplication Kind = "application"
	KindExtension   Kind = "extension"
)

// RuntimeSpec is what an application runs on.
type RuntimeSpec struct {
	// Type: php, python, node or binary (a program that needs no runtime).
	Type string `json:"type"`
	// Min and Max are inclusive major.minor bounds ("" = no bound).
	Min string `json:"min,omitempty"`
	Max string `json:"max,omitempty"`
	// Extensions are the PHP extensions (or Python modules for a python app)
	// that must be present.
	Extensions []string `json:"extensions,omitempty"`
	// OSPackages are operating-system dependencies, by the names used for
	// the package manager's own checks; they are only reported, never
	// installed by the application installer.
	OSPackages []string `json:"os_packages,omitempty"`
}

// DatabaseSpec is the database an application needs.
type DatabaseSpec struct {
	// Type: none, mysql (MySQL or MariaDB), sqlite or postgresql.
	Type string `json:"type"`
}

// SourceSpec says where the application comes from and how it is verified.
type SourceSpec struct {
	// Kind: url, github-release, composer or pypi.
	Kind string `json:"kind"`
	URL  string `json:"url,omitempty"`
	// GitHub ("owner/repo") and AssetRe select a release asset.
	GitHub  string `json:"github,omitempty"`
	AssetRe string `json:"asset_re,omitempty"`
	// Package is the Composer or PyPI package.
	Package string `json:"package,omitempty"`
	// Integrity: sha256-sidecar, sha1-sidecar, checksums-file, signature,
	// composer-lock (Composer's own hashing) or https-only.
	Integrity string `json:"integrity"`
	// IntegrityNote says why https-only is all there is (the vendor publishes
	// nothing to verify).
	IntegrityNote string `json:"integrity_note,omitempty"`
}

// ServiceSpec is how a long-running application is supervised and reached.
type ServiceSpec struct {
	// Type: worker (a NimVor worker: a systemd service as the site account).
	Type string `json:"type"`
	// Proxy: the site is a reverse proxy to the application's loopback port.
	Proxy bool `json:"proxy"`
}

// HealthSpec is how an installation is judged to be up.
type HealthSpec struct {
	// Type: http (a request to the application over loopback), service (the
	// unit is active) or file (the installed files are there).
	Type string `json:"type"`
	Path string `json:"path,omitempty"`
}

// LicenseSpec is the application's licence and where it is stated.
type LicenseSpec struct {
	SPDX string `json:"spdx"`
	URL  string `json:"url,omitempty"`
}

// Compat are constraints an installation is checked against.
type Compat struct {
	// Arch: the CPU architectures supported (Go's GOARCH names).
	Arch      []string `json:"arch,omitempty"`
	MinDiskMB int      `json:"min_disk_mb,omitempty"`
	MinRAMMB  int      `json:"min_ram_mb,omitempty"`
}

// Manifest is one application.
type Manifest struct {
	Schema      int          `json:"schema"`
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Kind        Kind         `json:"kind"`
	Category    string       `json:"category"`
	Description string       `json:"description"`
	Status      Status       `json:"status"`
	Runtime     RuntimeSpec  `json:"runtime"`
	Database    DatabaseSpec `json:"database"`
	Strategy    string       `json:"strategy"`
	Source      SourceSpec   `json:"source"`
	Secrets     []string     `json:"secrets,omitempty"`
	Service     *ServiceSpec `json:"service,omitempty"`
	Health      HealthSpec   `json:"health"`
	Backup      string       `json:"backup"`
	Upgrade     string       `json:"upgrade"`
	Uninstall   string       `json:"uninstall"`
	License     LicenseSpec  `json:"license"`
	Compat      Compat       `json:"compat"`
	// Verified names the real-host test that proved this entry works; an
	// entry cannot be enabled without it. "legacy" marks an entry that was in
	// the installer before manifests existed.
	Verified string `json:"verified,omitempty"`
}

// strategyInfo describes one trusted installation procedure.
type strategyInfo struct {
	// Runtimes the procedure can serve.
	Runtimes []string
	// Sources it can take its application from.
	Sources []string
	// Service: the procedure sets up a supervised service.
	Service bool
	// ExecutesDownload: what is downloaded runs as a program, so it must be
	// verified by a checksum or signature (not https alone).
	ExecutesDownload bool
	// Implemented: the agent has the code. A manifest for a procedure that is
	// not implemented can only be a draft.
	Implemented bool
}

// strategies are the procedures a manifest may select. Adding one is a code
// change that goes through review; nothing here comes from data.
var strategies = map[string]strategyInfo{
	"archive":          {Runtimes: []string{"php"}, Sources: []string{"url", "github-release"}, Implemented: true},
	"composer-project": {Runtimes: []string{"php"}, Sources: []string{"composer"}, Implemented: true},
	"wordpress":        {Runtimes: []string{"php"}, Sources: []string{"url"}, Implemented: true},
	"wordpress-plugin": {Runtimes: []string{"php"}, Sources: []string{"url"}, Implemented: true},
	"laravel":          {Runtimes: []string{"php"}, Sources: []string{"composer"}, Implemented: true},
	"binary-service":   {Runtimes: []string{"binary"}, Sources: []string{"url", "github-release"}, Service: true, ExecutesDownload: true},
	"python-venv":      {Runtimes: []string{"python"}, Sources: []string{"pypi"}, Service: true},
}

// StrategyImplemented reports whether the agent can run the strategy.
func StrategyImplemented(id string) bool { return strategies[id].Implemented }

// trustedHosts are the only hosts an application may be downloaded from. A new
// host is a reviewed code change. Hosts are matched exactly (no wildcard
// subdomains) over https on the default port.
var trustedHosts = map[string]bool{
	// source hosting and package registries
	"github.com": true, "api.github.com": true, "objects.githubusercontent.com": true, "pypi.org": true, "files.pythonhosted.org": true,
	"packagist.org": true, "repo.packagist.org": true, "getcomposer.org": true,
	// vendors
	"downloads.wordpress.org": true, "wordpress.org": true, "dl.gitea.com": true,
	"downloads.joomla.org": true, "www.drupal.org": true, "download.phpbb.com": true, "download.simplemachines.org": true,
	"download.nextcloud.com": true, "builds.matomo.org": true, "download.moodle.org": true, "releases.wikimedia.org": true,
	"getgrav.org": true, "www.bludit.com": true, "sourceforge.net": true, "akaunting.com": true, "piwigo.org": true,
	"facturascripts.com": true,
}

var (
	idRe       = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	secretRe   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,40}$`)
	versionRe  = regexp.MustCompile(`^[0-9]{1,2}\.[0-9]{1,2}$`)
	spdxRe     = regexp.MustCompile(`^(NOASSERTION|[A-Za-z0-9.+-]{2,40})$`)
	categoryRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,40}$`)
	repoRe     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,60}/[A-Za-z0-9._-]{1,100}$`)
	packageRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,80}(/[A-Za-z0-9._-]{1,80})?$`)
)

var (
	runtimeTypes   = set("php", "python", "node", "binary")
	databaseTypes  = set("none", "mysql", "sqlite", "postgresql")
	sourceKinds    = set("url", "github-release", "composer", "pypi")
	integrityKinds = set("sha256-sidecar", "sha1-sidecar", "checksums-file", "signature", "composer-lock", "https-only")
	strongIntegrty = set("sha256-sidecar", "sha1-sidecar", "checksums-file", "signature")
	healthTypes    = set("http", "service", "file")
	lifecycleIDs   = set("none", "files-and-database", "files", "venv-rebuild", "binary-replace", "composer-update")
	archs          = set("amd64", "arm64")
)

func set(v ...string) map[string]bool {
	m := make(map[string]bool, len(v))
	for _, s := range v {
		m[s] = true
	}
	return m
}

// Validate checks a manifest. It returns every problem found (empty means the
// manifest is valid). A manifest that does not validate cannot be enabled,
// whatever its Status says.
func (m Manifest) Validate() []string {
	var e []string
	bad := func(format string, a ...any) { e = append(e, fmt.Sprintf(format, a...)) }
	if m.Schema != ManifestSchema {
		bad("schema must be %d", ManifestSchema)
	}
	if !idRe.MatchString(m.ID) {
		bad("id must be 2-41 lowercase letters, digits or -, starting with a letter")
	}
	if strings.TrimSpace(m.Name) == "" || len(m.Name) > 80 {
		bad("name is required (at most 80 characters)")
	}
	if len(m.Description) > 400 {
		bad("description is longer than 400 characters")
	}
	if !categoryRe.MatchString(m.Category) {
		bad("category must be a category slug")
	}
	if m.Kind != KindApplication && m.Kind != KindExtension {
		bad("kind must be application or extension")
	}
	switch m.Status {
	case StatusDraft, StatusTesting, StatusEnabled, StatusDeprecated, StatusDisabled:
	default:
		bad("status must be draft, testing, enabled, deprecated or disabled")
	}

	st, known := strategies[m.Strategy]
	if !known {
		bad("strategy %q is not one of the trusted installation strategies", m.Strategy)
	}
	if !runtimeTypes[m.Runtime.Type] {
		bad("runtime type must be php, python, node or binary")
	} else if known && !contains(st.Runtimes, m.Runtime.Type) {
		bad("strategy %s does not support the %s runtime", m.Strategy, m.Runtime.Type)
	}
	for _, v := range []string{m.Runtime.Min, m.Runtime.Max} {
		if v != "" && !versionRe.MatchString(v) {
			bad("runtime versions must look like 8.3 (got %q)", v)
		}
	}
	if m.Runtime.Min != "" && m.Runtime.Max != "" && compareVersion(m.Runtime.Min, m.Runtime.Max) > 0 {
		bad("runtime min is above max")
	}
	for _, x := range m.Runtime.Extensions {
		if !regexp.MustCompile(`^[A-Za-z0-9_]{2,40}$`).MatchString(x) {
			bad("extension name %q is not valid", x)
		}
	}
	for _, p := range m.Runtime.OSPackages {
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{1,60}$`).MatchString(p) {
			bad("operating-system package name %q is not valid", p)
		}
	}
	if !databaseTypes[m.Database.Type] {
		bad("database type must be none, mysql, sqlite or postgresql")
	}

	// Source.
	s := m.Source
	if !sourceKinds[s.Kind] {
		bad("source kind must be url, github-release, composer or pypi")
	} else if known && !contains(st.Sources, s.Kind) {
		bad("strategy %s cannot take its application from a %s source", m.Strategy, s.Kind)
	}
	switch s.Kind {
	case "url":
		if err := checkURL(s.URL); err != nil {
			bad("source url: %v", err)
		}
	case "github-release":
		if !repoRe.MatchString(s.GitHub) {
			bad("source github must be owner/repo")
		}
		if s.AssetRe == "" {
			bad("source asset_re is required for a GitHub release")
		} else if _, err := regexp.Compile(s.AssetRe); err != nil {
			bad("source asset_re is not a valid expression")
		}
	case "composer", "pypi":
		if !packageRe.MatchString(s.Package) {
			bad("source package name is not valid")
		}
	}
	if !integrityKinds[s.Integrity] {
		bad("source integrity must be one of sha256-sidecar, sha1-sidecar, checksums-file, signature, composer-lock, https-only")
	}
	if s.Integrity == "https-only" && strings.TrimSpace(s.IntegrityNote) == "" {
		bad("https-only integrity needs an integrity_note saying why nothing can be verified")
	}
	if known && st.ExecutesDownload && !strongIntegrty[s.Integrity] {
		bad("strategy %s runs what it downloads, so it needs a checksum or signature, not %s", m.Strategy, s.Integrity)
	}

	// Service and health.
	if known && st.Service {
		if m.Service == nil || m.Service.Type != "worker" || !m.Service.Proxy {
			bad("strategy %s needs a worker service behind the reverse proxy", m.Strategy)
		}
	} else if m.Service != nil {
		bad("strategy %s has no service; remove the service section", m.Strategy)
	}
	if !healthTypes[m.Health.Type] {
		bad("health type must be http, service or file")
	} else if m.Health.Type == "http" && !strings.HasPrefix(m.Health.Path, "/") {
		bad("health path must start with /")
	}
	if st.Service && m.Health.Type == "file" {
		bad("a service needs an http or service health check")
	}

	// Secrets: names of generated values only.
	seen := map[string]bool{}
	for _, n := range m.Secrets {
		if !secretRe.MatchString(n) || seen[n] {
			bad("secret name %q is not valid or repeated", n)
		}
		seen[n] = true
	}

	// Lifecycle strategies.
	for field, v := range map[string]string{"backup": m.Backup, "upgrade": m.Upgrade, "uninstall": m.Uninstall} {
		if !lifecycleIDs[v] {
			bad("%s strategy %q is not a trusted lifecycle strategy", field, v)
		}
	}

	// Licence.
	if !spdxRe.MatchString(m.License.SPDX) {
		bad("license spdx identifier is required")
	}
	if m.License.SPDX == "NOASSERTION" && m.Verified != "legacy" {
		bad("a new application needs its licence recorded (NOASSERTION is only for entries that predate manifests)")
	}
	if m.License.URL != "" {
		if err := checkURL(m.License.URL); err != nil {
			bad("license url: %v", err)
		}
	}

	// Compatibility.
	for _, a := range m.Compat.Arch {
		if !archs[a] {
			bad("architecture %q is not supported (amd64, arm64)", a)
		}
	}
	if m.Compat.MinDiskMB < 0 || m.Compat.MinDiskMB > 100000 || m.Compat.MinRAMMB < 0 || m.Compat.MinRAMMB > 100000 {
		bad("compat minimums are out of range")
	}

	// Status gates.
	switch m.Status {
	case StatusTesting, StatusEnabled:
		if known && !st.Implemented {
			bad("strategy %s is not implemented yet, so the application can only be a draft", m.Strategy)
		}
	}
	if m.Status == StatusEnabled && m.Verified == "" {
		bad("an application cannot be enabled before a real-host installation test has verified it (set verified)")
	}
	sort.Strings(e)
	return e
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// checkURL accepts only https URLs to a trusted host on the default port
// without credentials or IP-address hosts.
func checkURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("must be an https URL")
	}
	if u.User != nil {
		return fmt.Errorf("must not contain credentials")
	}
	if u.Port() != "" && u.Port() != "443" {
		return fmt.Errorf("must use the default https port")
	}
	host := strings.ToLower(u.Hostname())
	if net.ParseIP(host) != nil {
		return fmt.Errorf("must name a host, not an IP address")
	}
	if !trustedHosts[host] {
		return fmt.Errorf("host %s is not a trusted download source", host)
	}
	return nil
}

// compareVersion compares "major.minor" strings.
func compareVersion(a, b string) int {
	pa, pb := splitVersion(a), splitVersion(b)
	for i := 0; i < 2; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitVersion(v string) [2]int {
	var out [2]int
	parts := strings.SplitN(v, ".", 3)
	for i := 0; i < 2 && i < len(parts); i++ {
		n := 0
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}

// RuntimeSupports reports whether a runtime version ("8.3") is inside the
// manifest's bounds.
func (r RuntimeSpec) Supports(version string) bool {
	if r.Min != "" && compareVersion(version, r.Min) < 0 {
		return false
	}
	if r.Max != "" && compareVersion(version, r.Max) > 0 {
		return false
	}
	return true
}

// ManifestFor is the manifest of an installable application: those defined
// directly as manifests, then every entry of the legacy Catalog (described
// from its AppDef, so the two cannot disagree).
func ManifestFor(slug string) (Manifest, bool) {
	if m, ok := overrideManifests[slug]; ok {
		return m, true
	}
	for _, m := range builtinManifests() {
		if m.ID == slug {
			return m, true
		}
	}
	if def, ok := Catalog[slug]; ok {
		return fromAppDef(slug, def), true
	}
	return Manifest{}, false
}

// overrideManifests lets tests give an application a status the legacy entries
// never have; it is empty in production.
var overrideManifests = map[string]Manifest{}

// AllManifests lists every application, sorted by ID.
func AllManifests() []Manifest {
	out := builtinManifests()
	for slug, def := range Catalog {
		out = append(out, fromAppDef(slug, def))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// builtinManifests are applications that have their own installation code
// (WordPress, Laravel) or are extensions of another application.
func builtinManifests() []Manifest {
	out := []Manifest{
		{
			Schema: ManifestSchema, ID: "wordpress", Name: "WordPress", Kind: KindApplication, Category: "blogs-cms",
			Description: "Blog and website builder.", Status: StatusEnabled,
			Runtime:  RuntimeSpec{Type: "php", Extensions: []string{"mysqli", "json"}},
			Database: DatabaseSpec{Type: "mysql"}, Strategy: "wordpress",
			Source:  SourceSpec{Kind: "url", URL: "https://wordpress.org/latest.tar.gz", Integrity: "sha1-sidecar"},
			Secrets: []string{"ADMIN_PASSWORD"}, Health: HealthSpec{Type: "file"},
			Backup: "files-and-database", Upgrade: "none", Uninstall: "none",
			License: LicenseSpec{SPDX: "NOASSERTION"}, Verified: "legacy",
		},
		{
			Schema: ManifestSchema, ID: "laravel", Name: "Laravel", Kind: KindApplication, Category: "frameworks",
			Description: "PHP application framework.", Status: StatusEnabled,
			Runtime:  RuntimeSpec{Type: "php", Extensions: []string{"pdo_mysql", "mbstring", "xml", "curl", "zip"}},
			Database: DatabaseSpec{Type: "mysql"}, Strategy: "laravel",
			Source: SourceSpec{Kind: "composer", Package: "laravel/laravel", Integrity: "composer-lock"},
			Health: HealthSpec{Type: "file"}, Backup: "files-and-database", Upgrade: "none", Uninstall: "none",
			License: LicenseSpec{SPDX: "NOASSERTION"}, Verified: "legacy",
		},
	}
	// WordPress plugins are extensions of a WordPress website, not
	// applications of their own.
	names := make([]string, 0, len(WPPlugins))
	for name := range WPPlugins {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := WPPlugins[name]
		out = append(out, Manifest{
			Schema: ManifestSchema, ID: "wp-" + name, Name: name, Kind: KindExtension, Category: "wordpress-plugins",
			Status:   StatusEnabled,
			Runtime:  RuntimeSpec{Type: "php"},
			Database: DatabaseSpec{Type: "none"}, Strategy: "wordpress-plugin",
			Source: SourceSpec{Kind: "url", URL: p.URL, Integrity: "https-only", IntegrityNote: "wordpress.org publishes no checksum for the latest-stable download"},
			Health: HealthSpec{Type: "file"}, Backup: "files", Upgrade: "none", Uninstall: "none",
			License: LicenseSpec{SPDX: "NOASSERTION"}, Verified: "legacy",
		})
	}
	return out
}

// fromAppDef describes a pre-manifest catalog entry. Its licence and
// integrity details were never recorded, which the manifest says openly
// (NOASSERTION, "legacy") instead of guessing.
func fromAppDef(slug string, d AppDef) Manifest {
	m := Manifest{
		Schema: ManifestSchema, ID: slug, Name: d.Name, Kind: KindApplication, Category: "applications",
		Status:   StatusEnabled,
		Runtime:  RuntimeSpec{Type: "php", Extensions: d.Ext},
		Database: DatabaseSpec{Type: "mysql"},
		Strategy: "archive",
		Source:   SourceSpec{Kind: "url", URL: d.URL, Integrity: "https-only", IntegrityNote: "the vendor publishes no checksum file"},
		Health:   HealthSpec{Type: "file"},
		Backup:   "files-and-database", Upgrade: "none", Uninstall: "none",
		License:  LicenseSpec{SPDX: "NOASSERTION"},
		Verified: "legacy",
	}
	if d.NoDB {
		m.Database.Type = "none"
		m.Backup = "files"
	}
	switch {
	case d.Package != "":
		m.Strategy = "composer-project"
		m.Source = SourceSpec{Kind: "composer", Package: d.Package, Integrity: "composer-lock"}
	case d.GitHub != "":
		m.Source = SourceSpec{Kind: "github-release", GitHub: d.GitHub, AssetRe: d.AssetRe, Integrity: "https-only", IntegrityNote: "the release publishes no checksum file"}
	case d.SumURL != "" && d.SumAlgo == "sha1":
		m.Source.Integrity, m.Source.IntegrityNote = "sha1-sidecar", ""
	case d.SumURL != "":
		m.Source.Integrity, m.Source.IntegrityNote = "sha256-sidecar", ""
	}
	return m
}

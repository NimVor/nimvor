// Package discovery correlates read-only findings into a server inventory.
//
// Nothing in this package writes to the host. It combines Nginx virtual
// hosts, PHP-FPM pools and TLS certificates into "websites" as a person
// would understand them.
package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/cron"
	"github.com/mchkhawarshahzad/whacp/agent/internal/database"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/firewall"
	"github.com/mchkhawarshahzad/whacp/agent/internal/nginx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/php"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
	"github.com/mchkhawarshahzad/whacp/agent/internal/redis"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ssl"
	"github.com/mchkhawarshahzad/whacp/agent/internal/systemd"
	"github.com/mchkhawarshahzad/whacp/agent/internal/users"
)

// PHPBinding links a website to PHP-FPM.
type PHPBinding struct {
	Version  string `json:"version,omitempty"`
	Pool     string `json:"pool,omitempty"`
	Listen   string `json:"listen"`
	User     string `json:"user,omitempty"`
	Resolved bool   `json:"resolved"`
}

// SSLBinding summarises the certificate of a website.
type SSLBinding struct {
	CertFile      string    `json:"cert_file"`
	Issuer        string    `json:"issuer"`
	NotAfter      time.Time `json:"not_after"`
	DaysRemaining int       `json:"days_remaining"`
	Status        string    `json:"status"`
	LetsEncrypt   bool      `json:"lets_encrypt"`
	CoversDomain  bool      `json:"covers_domain"`
	Names         []string  `json:"names"`
	Error         string    `json:"error,omitempty"`
}

// Website is a correlated view of one site.
type Website struct {
	ID            string      `json:"id"`
	Domain        string      `json:"domain"`
	Aliases       []string    `json:"aliases"`
	Enabled       bool        `json:"enabled"`
	Kind          string      `json:"kind"`
	DocumentRoot  string      `json:"document_root,omitempty"`
	DocRootExists bool        `json:"document_root_exists"`
	PHP           *PHPBinding `json:"php,omitempty"`
	SSL           *SSLBinding `json:"ssl,omitempty"`
	HTTPS         bool        `json:"https"`
	HTTPSRedirect bool        `json:"https_redirect"`
	ProxyPass     string      `json:"proxy_pass,omitempty"`
	Listen        []string    `json:"listen"`
	ConfigFiles   []string    `json:"config_files"`
	AccessLogs    []string    `json:"access_logs"`
	ErrorLogs     []string    `json:"error_logs"`
	ManagedBy     string      `json:"managed_by,omitempty"`
}

// WebsitesResult is returned by the website discovery action.
type WebsitesResult struct {
	Websites     []Website `json:"websites"`
	CatchAll     int       `json:"catch_all_vhosts"`
	NginxVersion string    `json:"nginx_version,omitempty"`
	NginxFiles   []string  `json:"nginx_files"`
	Warnings     []string  `json:"warnings"`
}

// Inventory is the full read-only snapshot of the server.
type Inventory struct {
	GeneratedAt  time.Time           `json:"generated_at"`
	OS           platform.OSInfo     `json:"os"`
	Websites     WebsitesResult      `json:"websites"`
	PHP          php.Result          `json:"php"`
	Databases    database.Result     `json:"databases"`
	Certificates []ssl.LECertificate `json:"certificates"`
	Redis        redis.Result        `json:"redis"`
	Cron         cron.Result         `json:"cron"`
	Users        []users.User        `json:"users"`
	Firewall     firewall.Status     `json:"firewall"`
	Services     []systemd.Service   `json:"services"`
}

// Service performs discovery for one host.
type Service struct {
	Root     filesystem.Root
	Platform platform.Platform
	Runner   execx.Runner
	Now      func() time.Time
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

// PHP returns PHP discovery results.
func (s Service) PHP() php.Result { return php.Discover(s.Root, s.Platform.Paths()) }

// Nginx returns raw Nginx discovery results.
func (s Service) Nginx(ctx context.Context) nginx.Result {
	return nginx.Discoverer{Root: s.Root, Paths: s.Platform.Paths(), Runner: s.Runner}.Discover(ctx)
}

// Databases returns database discovery results.
func (s Service) Databases(ctx context.Context) database.Result {
	return database.Discover(ctx, s.Root, s.Platform.Paths(), s.Runner)
}

// Certificates returns Let's Encrypt lineages.
func (s Service) Certificates() []ssl.LECertificate {
	return ssl.DiscoverLetsEncrypt(s.Root, s.Platform.Paths().LetsEncryptDir, s.now())
}

// Redis returns Redis discovery results.
func (s Service) Redis(ctx context.Context) redis.Result {
	return redis.Discover(ctx, s.Root, s.Platform.Paths(), s.Runner)
}

// Cron returns cron discovery results.
func (s Service) Cron() cron.Result { return cron.Discover(s.Root, s.Platform.Paths()) }

// Users returns local accounts.
func (s Service) Users() ([]users.User, error) {
	return users.Discover(s.Root, s.Platform.Paths().PasswdFile)
}

// Firewall returns firewall state.
func (s Service) Firewall() firewall.Status { return firewall.Detect(s.Root, s.Platform.Paths()) }

// ServiceUnits returns the units WhaCP reports on: core services plus
// every PHP-FPM unit present on the host.
func (s Service) ServiceUnits() []string {
	paths := s.Platform.Paths()
	units := append([]string{}, s.Platform.CoreServices()...)
	units = append(units, systemd.DiscoverUnits(s.Root, paths.SystemdUnitDirs, s.Platform.PHPFPMUnitPatterns())...)
	seen := map[string]bool{}
	out := units[:0]
	for _, u := range units {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// Services returns the status of installed services (not-found units are
// dropped).
func (s Service) Services(ctx context.Context) ([]systemd.Service, error) {
	all, err := systemd.Client{Runner: s.Runner}.Status(ctx, s.ServiceUnits())
	if err != nil {
		return nil, err
	}
	out := make([]systemd.Service, 0, len(all))
	for _, svc := range all {
		if svc.LoadState == "not-found" || svc.LoadState == "" {
			continue
		}
		out = append(out, svc)
	}
	return out, nil
}

// Websites correlates Nginx, PHP-FPM and certificate data.
func (s Service) Websites(ctx context.Context) WebsitesResult {
	ng := s.Nginx(ctx)
	phpRes := s.PHP()
	return Correlate(s.Root, ng, phpRes, s.now())
}

// Full builds a complete inventory.
func (s Service) Full(ctx context.Context) Inventory {
	inv := Inventory{
		GeneratedAt:  s.now(),
		OS:           s.Platform.Info(),
		Websites:     s.Websites(ctx),
		PHP:          s.PHP(),
		Databases:    s.Databases(ctx),
		Certificates: s.Certificates(),
		Redis:        s.Redis(ctx),
		Cron:         s.Cron(),
		Firewall:     s.Firewall(),
		Users:        []users.User{},
		Services:     []systemd.Service{},
	}
	if u, err := s.Users(); err == nil {
		inv.Users = u
	}
	if svc, err := s.Services(ctx); err == nil {
		inv.Services = svc
	}
	return inv
}

// Correlate groups vhosts into websites.
func Correlate(root filesystem.Root, ng nginx.Result, phpRes php.Result, now time.Time) WebsitesResult {
	res := WebsitesResult{
		Websites:     []Website{},
		NginxVersion: ng.Version,
		NginxFiles:   ng.Files,
		Warnings:     append([]string{}, ng.Warnings...),
	}
	pools := map[string]php.Pool{}
	for _, p := range phpRes.Pools {
		if p.Listen != "" {
			pools[php.NormalizeListen(p.Listen)] = p
		}
	}
	certCache := map[string]ssl.Certificate{}

	byKey := map[string]*Website{}
	var order []string
	for _, v := range ng.VHosts {
		primary := primaryName(v.ServerNames)
		if primary == "" {
			res.CatchAll++
			continue
		}
		key := primary
		if !v.Enabled {
			key += "#disabled"
		}
		w, ok := byKey[key]
		if !ok {
			w = &Website{
				ID:          websiteID(primary, v.Enabled),
				Domain:      primary,
				Enabled:     v.Enabled,
				Aliases:     []string{},
				Listen:      []string{},
				ConfigFiles: []string{},
				AccessLogs:  []string{},
				ErrorLogs:   []string{},
			}
			byKey[key] = w
			order = append(order, key)
		}
		mergeVHost(w, v)
		if v.SSL {
			w.HTTPS = true
		}
		if v.RedirectHTTPS && !v.SSL {
			w.HTTPSRedirect = true
		}
		if v.FastCGIPass != "" && w.PHP == nil {
			w.PHP = bindPHP(v.FastCGIPass, pools)
		}
		if v.CertFile != "" && w.SSL == nil {
			c, ok := certCache[v.CertFile]
			if !ok {
				c = ssl.Inspect(root, v.CertFile, v.KeyFile, now)
				certCache[v.CertFile] = c
			}
			w.SSL = &SSLBinding{
				CertFile:      c.File,
				Issuer:        c.Issuer,
				NotAfter:      c.NotAfter,
				DaysRemaining: c.DaysRemaining,
				Status:        c.Status(),
				LetsEncrypt:   c.LetsEncrypt,
				CoversDomain:  c.Error == "" && c.Covers(primary),
				Names:         c.DNSNames,
				Error:         c.Error,
			}
		}
	}
	for _, key := range order {
		w := byKey[key]
		w.Kind = kind(w)
		if w.DocumentRoot != "" {
			w.DocRootExists = root.IsDir(w.DocumentRoot)
		}
		w.ManagedBy = managedBy(w)
		sort.Strings(w.Aliases)
		res.Websites = append(res.Websites, *w)
	}
	sort.SliceStable(res.Websites, func(i, j int) bool {
		if res.Websites[i].Domain != res.Websites[j].Domain {
			return res.Websites[i].Domain < res.Websites[j].Domain
		}
		return res.Websites[i].Enabled
	})
	return res
}

func primaryName(names []string) string {
	for _, n := range names {
		n = strings.ToLower(strings.TrimSuffix(n, "."))
		if n == "_" || n == "" || n == "localhost" || strings.HasPrefix(n, "~") || strings.ContainsAny(n, "*$") {
			continue
		}
		return n
	}
	return ""
}

func websiteID(domain string, enabled bool) string {
	sum := sha256.Sum256([]byte(domain + "|" + map[bool]string{true: "on", false: "off"}[enabled]))
	return hex.EncodeToString(sum[:])[:16]
}

func mergeVHost(w *Website, v nginx.VHost) {
	for _, n := range v.ServerNames {
		n = strings.ToLower(n)
		if n != w.Domain && n != "_" && !contains(w.Aliases, n) {
			w.Aliases = append(w.Aliases, n)
		}
	}
	for _, l := range v.Listen {
		if !contains(w.Listen, l) {
			w.Listen = append(w.Listen, l)
		}
	}
	if !contains(w.ConfigFiles, v.File) {
		w.ConfigFiles = append(w.ConfigFiles, v.File)
	}
	if v.AccessLog != "" && v.AccessLog != "off" && !contains(w.AccessLogs, v.AccessLog) {
		w.AccessLogs = append(w.AccessLogs, v.AccessLog)
	}
	if v.ErrorLog != "" && !contains(w.ErrorLogs, v.ErrorLog) {
		w.ErrorLogs = append(w.ErrorLogs, v.ErrorLog)
	}
	// Prefer the document root of the HTTPS server block.
	if v.Root != "" && (w.DocumentRoot == "" || v.SSL) {
		w.DocumentRoot = v.Root
	}
	if v.ProxyPass != "" && w.ProxyPass == "" {
		w.ProxyPass = v.ProxyPass
	}
}

var socketVersion = regexp.MustCompile(`php-?(\d+\.\d+)`)

func bindPHP(pass string, pools map[string]php.Pool) *PHPBinding {
	b := &PHPBinding{Listen: pass}
	if p, ok := pools[php.NormalizeListen(pass)]; ok {
		b.Version, b.Pool, b.User, b.Resolved = p.Version, p.Name, p.User, true
		return b
	}
	if m := socketVersion.FindStringSubmatch(pass); m != nil {
		b.Version = m[1]
	}
	return b
}

func kind(w *Website) string {
	switch {
	case w.PHP != nil:
		return "php"
	case w.ProxyPass != "":
		return "proxy"
	case w.DocumentRoot != "":
		return "static"
	case w.HTTPSRedirect:
		return "redirect"
	default:
		return "other"
	}
}

// managedBy guesses which control panel created a site, based on well-known
// path conventions. It is a hint for the operator, never used for decisions.
func managedBy(w *Website) string {
	paths := append(append([]string{w.DocumentRoot}, w.ConfigFiles...), w.AccessLogs...)
	joined := strings.Join(paths, "\n")
	switch {
	case strings.Contains(joined, "/www/server/") || strings.Contains(joined, "/www/wwwroot/"):
		return "aaPanel"
	case strings.Contains(joined, "fastpanel"):
		return "FASTPANEL"
	case strings.Contains(joined, "/hestia/") || strings.Contains(joined, "/conf/web/"):
		return "HestiaCP"
	case strings.Contains(joined, "/usr/local/lsws") || strings.Contains(joined, "cyberpanel"):
		return "CyberPanel"
	case strings.Contains(joined, "whacp-"):
		return "WhaCP"
	}
	return ""
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

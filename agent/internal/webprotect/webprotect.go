// Package webprotect writes a website's protection rules for Nginx:
// password protection (HTTP basic authentication, per URL path), hotlink
// protection (images and media only served to pages of allowed sites) and
// the web server side of WordPress hardening (per installation: blocking
// sensitive files, xmlrpc.php, author scans, PHP in uploads and
// wp-includes, and known attack tools).
//
// Everything is rendered from the agent's own state into one file per
// website, included by the website's server blocks; every change is
// checked with nginx -t and rolled back when Nginx does not accept or
// reload it. Passwords are stored only as SHA-512 crypt hashes, in a file
// readable by root and the web server's group.
package webprotect

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// State is every website's protection.
type State struct {
	Sites map[string]*Site `json:"sites"`
}

// Site is one website's protection.
type Site struct {
	// Password protects URL paths ("/" is the whole website).
	Password []Protected `json:"password,omitempty"`
	Hotlink  *Hotlink    `json:"hotlink,omitempty"`
	// WordPress holds hardening rules per installation URL path.
	WordPress map[string][]string `json:"wordpress,omitempty"`
}

// Protected is a password-protected URL path.
type Protected struct {
	Path  string `json:"path"`
	Realm string `json:"realm"`
	User  string `json:"user"`
	Hash  string `json:"hash"`
}

// Hotlink lists who may embed the website's files.
type Hotlink struct {
	Allowed    []string `json:"allowed"`
	Extensions []string `json:"extensions"`
}

// Nginx rules of WordPress hardening (the others are WordPress settings).
const (
	BlockSensitive  = "block_sensitive_files"
	BlockXMLRPC     = "block_xmlrpc"
	BlockAuthorScan = "block_author_scans"
	NoPHPUploads    = "no_php_in_uploads"
	NoPHPIncludes   = "no_php_in_includes"
	BlockBots       = "block_bad_bots"
)

// Rules are the WordPress rules this package renders, in order.
var Rules = []string{BlockSensitive, BlockXMLRPC, BlockAuthorScan, NoPHPUploads, NoPHPIncludes, BlockBots}

// DefaultExtensions are protected from hotlinking unless others are given.
var DefaultExtensions = []string{"jpg", "jpeg", "png", "gif", "webp", "avif", "svg", "bmp", "ico", "mp4", "webm", "mp3", "ogg", "pdf"}

var (
	urlPathRe = regexp.MustCompile(`^/([A-Za-z0-9._~-]+/)*$`)
	userRe    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	realmRe   = regexp.MustCompile(`^[A-Za-z0-9 ._,()-]{1,80}$`)
	hostRe    = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	extRe     = regexp.MustCompile(`^[a-z0-9]{1,8}$`)
)

// CleanPath checks a URL path and returns it with a leading and trailing
// slash ("/" is the whole website).
func CleanPath(p string) (string, error) {
	p = "/" + strings.Trim(p, "/") + "/"
	if p == "//" {
		p = "/"
	}
	if !urlPathRe.MatchString(p) || strings.Contains(p, "/../") || strings.Contains(p, "/./") || len(p) > 200 {
		return "", fmt.Errorf("path must be a URL path made of letters, digits, dots, dashes and underscores")
	}
	return p, nil
}

var mu sync.Mutex

// Dir holds the website files and the password files.
func Dir(e *host.Env) string {
	return filepath.Join(filepath.Dir(e.M.NginxAvailable), "nimvor-protect")
}

// SitePath is a website's file.
func SitePath(e *host.Env, domain string) string { return filepath.Join(Dir(e), domain+".conf") }

func passwdPath(e *host.Env, domain string, i int) string {
	return filepath.Join(Dir(e), "htpasswd", fmt.Sprintf("%s-%d", domain, i))
}

// SiteInclude is the vhost line: a glob, so it matches nothing while the
// website has no protection.
func SiteInclude(e *host.Env, domain string) string {
	return "    include " + strings.TrimSuffix(SitePath(e, domain), "f") + "[f];\n"
}

func statePath(e *host.Env) string { return filepath.Join(e.Cfg.StateDir, "webprotect.json") }

// AllowPaths lets the agent write this package's files.
func AllowPaths(e *host.Env) {
	e.Writer.Policy.Patterns = append(e.Writer.Policy.Patterns, Dir(e)+"/**")
}

// Load reads the state (empty when there is none).
func Load(e *host.Env) State {
	st := State{Sites: map[string]*Site{}}
	if b, err := e.Root.ReadFile(statePath(e)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Sites == nil {
		st.Sites = map[string]*Site{}
	}
	return st
}

func (st State) site(domain string) *Site {
	s := st.Sites[domain]
	if s == nil {
		s = &Site{}
		st.Sites[domain] = s
	}
	return s
}

func (s *Site) empty() bool {
	return len(s.Password) == 0 && s.Hotlink == nil && len(s.WordPress) == 0
}

// quoteRe escapes a URL path for a regular expression.
func quoteRe(p string) string { return regexp.QuoteMeta(p) }

// Render is a website's file ("" when it has no protection).
func Render(e *host.Env, domain string, s *Site) string {
	if s == nil || s.empty() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "    # Managed by NimVor (website protection of %s). Do not edit.\n", domain)
	// Password protection: the longest matching path decides.
	if len(s.Password) > 0 {
		list := append([]Protected{}, s.Password...)
		sort.SliceStable(list, func(i, j int) bool { return len(list[i].Path) < len(list[j].Path) })
		b.WriteString("    set $nimvor_auth off;\n    set $nimvor_auth_file /dev/null;\n")
		for _, p := range list {
			i := indexOf(s.Password, p.Path)
			if p.Path == "/" {
				fmt.Fprintf(&b, "    set $nimvor_auth %q;\n    set $nimvor_auth_file %s;\n", p.Realm, passwdPath(e, domain, i))
				continue
			}
			fmt.Fprintf(&b, "    if ($uri ~ \"^%s\") {\n        set $nimvor_auth %q;\n        set $nimvor_auth_file %s;\n    }\n", quoteRe(p.Path), p.Realm, passwdPath(e, domain, i))
		}
		// Certificates keep renewing behind a password.
		b.WriteString("    if ($uri ~ \"^/\\.well-known/acme-challenge/\") {\n        set $nimvor_auth off;\n    }\n")
		b.WriteString("    auth_basic $nimvor_auth;\n    auth_basic_user_file $nimvor_auth_file;\n")
	}
	paths := make([]string, 0, len(s.WordPress))
	for p := range s.WordPress {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		rules := map[string]bool{}
		for _, r := range s.WordPress[p] {
			rules[r] = true
		}
		q := quoteRe(p)
		fmt.Fprintf(&b, "    # WordPress in %s\n", p)
		if rules[BlockSensitive] {
			fmt.Fprintf(&b, "    location ~* ^%s(wp-config\\.php|wp-config-sample\\.php|readme\\.html|license\\.txt|wp-admin/install\\.php|wp-admin/setup-config\\.php)$ {\n        deny all;\n    }\n", q)
			fmt.Fprintf(&b, "    location ~* ^%s.*\\.(sql|sqlite|bak|old|orig|save|swp|log|ini|sh|dist)$ {\n        deny all;\n    }\n", q)
		}
		if rules[BlockXMLRPC] {
			fmt.Fprintf(&b, "    location = %sxmlrpc.php {\n        deny all;\n    }\n", p)
		}
		if rules[BlockAuthorScan] {
			fmt.Fprintf(&b, "    if ($request_uri ~* \"^%s(index\\.php)?\\?(.*&)?author=[0-9]\") {\n        return 403;\n    }\n", q)
		}
		if rules[NoPHPUploads] {
			fmt.Fprintf(&b, "    location ~* ^%swp-content/uploads/.*\\.(php[0-9]?|phtml|phar|pht)$ {\n        deny all;\n    }\n", q)
		}
		if rules[NoPHPIncludes] {
			fmt.Fprintf(&b, "    location ~* ^%swp-includes/(?!js/tinymce/wp-tinymce\\.php$|ms-files\\.php$).*\\.php$ {\n        deny all;\n    }\n", q)
		}
		if rules[BlockBots] {
			b.WriteString("    if ($http_user_agent ~* \"(sqlmap|nikto|wpscan|nmap|masscan|zgrab|acunetix|nessus|dirbuster|gobuster|havij|w3af|netsparker|fimap|jorgee)\") {\n        return 403;\n    }\n")
		}
	}
	if h := s.Hotlink; h != nil {
		exts := h.Extensions
		if len(exts) == 0 {
			exts = DefaultExtensions
		}
		allowed := append([]string{"none", "blocked", "server_names"}, h.Allowed...)
		// Never for hidden files (the website's own rule denies those, but
		// this location comes first).
		fmt.Fprintf(&b, "    location ~* ^(?!.*/\\.).*\\.(%s)$ {\n        valid_referers %s;\n        if ($invalid_referer) {\n            return 403;\n        }\n    }\n", strings.Join(exts, "|"), strings.Join(allowed, " "))
	}
	return b.String()
}

func indexOf(list []Protected, path string) int {
	for i, p := range list {
		if p.Path == path {
			return i
		}
	}
	return -1
}

// vhostMarker is a line every NimVor server block with content carries;
// the include goes right after it (for websites created before this).
const vhostMarker = "    add_header X-Content-Type-Options \"nosniff\" always;\n"

// apply writes a website's protection (and the state), checks Nginx and
// reloads it; anything that fails is rolled back. The caller holds the
// web configuration lock, then mu (the order site deletion uses too).
func apply(ctx context.Context, e *host.Env, st State, domain string) error {
	if err := e.PrecheckNginx(ctx); err != nil {
		return err
	}
	vhost := filepath.Join(e.M.NginxAvailable, "nimvor-"+domain+".conf")
	b, err := e.Root.ReadFile(vhost)
	if err != nil {
		return fmt.Errorf("the website %s has no NimVor web server configuration", domain)
	}
	set := e.Changes("webprotect-" + domain)
	set.Mkdir(Dir(e), 0o755, agentfs.RootOwner)
	s := st.Sites[domain]
	body := Render(e, domain, s)
	if inc := SiteInclude(e, domain); !strings.Contains(string(b), inc) {
		if !strings.Contains(string(b), vhostMarker) {
			return fmt.Errorf("the web server configuration of %s was changed outside NimVor; save the website in the panel first", domain)
		}
		set.Write(vhost, []byte(strings.ReplaceAll(string(b), vhostMarker, vhostMarker+inc)), 0o644, agentfs.RootOwner)
	}
	// Password files: one per protected path, root and the web server.
	web, err := e.WebUser()
	if err != nil {
		return fmt.Errorf("the web server account was not found: %v", err)
	}
	existing, _ := e.Root.Glob(filepath.Join(Dir(e), "htpasswd", domain+"-*"))
	keep := map[string]bool{}
	if s != nil && len(s.Password) > 0 {
		set.Mkdir(filepath.Join(Dir(e), "htpasswd"), 0o750, agentfs.Owner{UID: 0, GID: web.GID})
		for i, p := range s.Password {
			f := passwdPath(e, domain, i)
			keep[f] = true
			set.Write(f, []byte(p.User+":"+p.Hash+"\n"), 0o640, agentfs.Owner{UID: 0, GID: web.GID})
		}
	}
	for _, f := range existing {
		if !keep[f] {
			set.Remove(f)
		}
	}
	if body == "" {
		if e.Root.Exists(SitePath(e, domain)) {
			set.Remove(SitePath(e, domain))
		}
		delete(st.Sites, domain)
	} else {
		set.Write(SitePath(e, domain), []byte(body), 0o644, agentfs.RootOwner)
	}
	sb, _ := json.MarshalIndent(st, "", "  ")
	set.Write(statePath(e), sb, 0o600, agentfs.RootOwner)
	applied, err := set.Commit(func() error { return e.NginxTest(ctx) })
	if err != nil {
		return host.FailWithValidation("The protection was not changed", err)
	}
	if len(applied.Changed) > 0 {
		if err := e.ReloadNginx(ctx); err != nil {
			applied.Rollback()
			e.ReloadNginx(ctx)
			return host.FailWithValidation("Nginx did not reload; the previous protection was restored", err)
		}
	}
	return nil
}

// Update changes one website's protection under the package lock.
func Update(ctx context.Context, e *host.Env, domain string, change func(*Site) error) (*Site, error) {
	defer e.LockWebConfig()()
	mu.Lock()
	defer mu.Unlock()
	st := Load(e)
	s := st.site(domain)
	if err := change(s); err != nil {
		return nil, err
	}
	if err := apply(ctx, e, st, domain); err != nil {
		return nil, err
	}
	return Load(e).Sites[domain], nil
}

// Remove forgets a deleted website: the paths to remove with it (the
// caller holds the web configuration lock).
func Remove(e *host.Env, domain string) []string {
	mu.Lock()
	defer mu.Unlock()
	st := Load(e)
	if _, ok := st.Sites[domain]; !ok {
		return nil
	}
	delete(st.Sites, domain)
	b, _ := json.MarshalIndent(st, "", "  ")
	set := e.Changes("webprotect-forget-" + domain)
	set.Write(statePath(e), b, 0o600, agentfs.RootOwner)
	set.Commit(nil)
	out := []string{}
	if e.Root.Exists(SitePath(e, domain)) {
		out = append(out, SitePath(e, domain))
	}
	files, _ := e.Root.Glob(filepath.Join(Dir(e), "htpasswd", domain+"-*"))
	return append(out, files...)
}

// Status is what the panel shows (never password hashes).
func Status(e *host.Env, domain string) map[string]any {
	s := Load(e).Sites[domain]
	pw := []map[string]string{}
	var hot any
	wp := map[string][]string{}
	if s != nil {
		for _, p := range s.Password {
			pw = append(pw, map[string]string{"path": p.Path, "user": p.User, "realm": p.Realm})
		}
		if s.Hotlink != nil {
			hot = s.Hotlink
		}
		for k, v := range s.WordPress {
			wp[k] = v
		}
	}
	return map[string]any{"password": pw, "hotlink": hot, "wordpress": wp}
}

// ValidUser, ValidRealm, ValidHost and ValidExtension check input.
func ValidUser(u string) bool  { return userRe.MatchString(u) }
func ValidRealm(r string) bool { return realmRe.MatchString(r) }
func ValidHost(h string) bool  { return hostRe.MatchString(h) }
func ValidExtension(x string) bool {
	return extRe.MatchString(x)
}

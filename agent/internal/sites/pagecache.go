package sites

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// PageCache is Nginx's own cache of a PHP website's pages (fastcgi_cache). Pages are stored for TTL seconds and served
// without starting PHP. It reuses the rules of the browser-cache feature for what must never be cached: sign-in and
// administration paths, requests with an Authorization header or a session/sign-in cookie, requests with a query string,
// every request other than GET and HEAD, and every response that sets a cookie or says it is private.
type PageCache struct {
	Enabled bool `json:"enabled"`
	// TTL is how long a page is served from the cache, in seconds (one of PageCacheTTLs).
	TTL int `json:"ttl,omitempty"`
}

// PageCacheTTLs are the lifetimes a website can choose: 1 minute, 5, 10, 30 minutes, 1, 6 hours and 1 day.
var PageCacheTTLs = []int{60, 300, 600, 1800, 3600, 21600, 86400}

// PageCacheBase holds every website's cache folder, owned by the web server's account (mode 0700), so no website's
// PHP account can read another's cached pages.
const PageCacheBase = "/var/cache/nimvor/fastcgi"

// Each website's cache is limited to this size on disk, whatever its lifetime.
const pageCacheMaxSize = "256m"

func (c *PageCache) normalize() error {
	if !c.Enabled {
		c.TTL = 0
		return nil
	}
	if c.TTL == 0 {
		c.TTL = 600
	}
	for _, t := range PageCacheTTLs {
		if c.TTL == t {
			return nil
		}
	}
	return fail("cache.page_cache.ttl", "choose 1, 5, 10 or 30 minutes, or 1, 6 or 24 hours")
}

// pageCacheOn reports whether the website's PHP pages are cached by Nginx.
func pageCacheOn(s Spec) bool {
	return s.Kind == "php" && s.Cache != nil && s.Cache.PageCache != nil && s.Cache.PageCache.Enabled
}

func pageCacheZone(domain string) string { return siteVar(domain, "pc") }

// pageCacheDir is the website's cache folder (the name is a hash, so the domain never reaches a path).
func pageCacheDir(domain string) string {
	return filepath.Join(PageCacheBase, strings.TrimPrefix(siteVar(domain, "pc"), "nimvor_pc_"))
}

// renderPageCachePath is the cache's own definition, written above the server blocks (the http context).
func renderPageCachePath(s Spec) string {
	if !pageCacheOn(s) {
		return ""
	}
	return fmt.Sprintf("fastcgi_cache_path %s levels=1:2 keys_zone=%s:10m max_size=%s inactive=1d use_temp_path=off;\n", pageCacheDir(s.Domain), pageCacheZone(s.Domain), pageCacheMaxSize)
}

// renderPageCacheMaps decides per request whether the cache is skipped: it is unless every flag is clear (the path is not
// sensitive or listed, no Authorization header, no session cookie, a GET or HEAD request, and a plain URL).
// The paths are matched on $request_uri, the address the visitor asked for: $uri becomes /index.php once a pretty
// address is rewritten to the front controller, which would hide /my-account/ or /wp-admin/.
func renderPageCacheMaps(s Spec) string {
	if !pageCacheOn(s) {
		return ""
	}
	pp, qf, bypass := siteVar(s.Domain, "pcp"), siteVar(s.Domain, "qf"), siteVar(s.Domain, "pcb")
	var b strings.Builder
	fmt.Fprintf(&b, "map $request_uri $%s {\n    default 0;\n", pp)
	fmt.Fprintf(&b, "    \"~*^/(?:%s)(?:/|\\.|-|\\?|$)\" 1;\n", sensitivePaths)
	if len(s.Cache.BypassPaths) > 0 {
		esc := make([]string, len(s.Cache.BypassPaths))
		for i, p := range s.Cache.BypassPaths {
			esc[i] = strings.NewReplacer(".", `\.`).Replace(p)
		}
		fmt.Fprintf(&b, "    \"~*^(?:%s)(?:/|\\.|\\?|$)\" 1;\n", strings.Join(esc, "|"))
	}
	b.WriteString("}\n")
	// A query string, a percent-encoded or a ";" address is never cached: each variant would be its own entry (a way to
	// fill the disk), and an encoded path could hide a sensitive one from the rules above.
	fmt.Fprintf(&b, "map $request_uri $%s {\n    default 0;\n    \"~[?%%;]\" 1;\n}\n", qf)
	fmt.Fprintf(&b, "map \"$%s$nimvor_af$nimvor_cf$nimvor_mf$%s\" $%s {\n    default 1;\n    \"00000\" 0;\n}\n", pp, qf, bypass)
	return b.String()
}

// pageCacheLocation is what the PHP location adds. It has no add_header: that directive in a location would replace the
// security headers set for the whole server block.
func pageCacheLocation(s Spec) string {
	if !pageCacheOn(s) {
		return ""
	}
	bypass := siteVar(s.Domain, "pcb")
	var b strings.Builder
	fmt.Fprintf(&b, "        fastcgi_cache %s;\n", pageCacheZone(s.Domain))
	b.WriteString("        fastcgi_cache_key \"$scheme$request_method$host$request_uri\";\n")
	fmt.Fprintf(&b, "        fastcgi_cache_valid 200 301 %ds;\n", s.Cache.PageCache.TTL)
	b.WriteString("        fastcgi_cache_methods GET HEAD;\n        fastcgi_cache_lock on;\n        fastcgi_cache_lock_timeout 5s;\n")
	b.WriteString("        fastcgi_cache_use_stale error timeout updating;\n")
	fmt.Fprintf(&b, "        fastcgi_cache_bypass $%s;\n        fastcgi_no_cache $%s $nimvor_sf;\n", bypass, bypass)
	return b.String()
}

// pageCacheHeader tells whether a page came from the cache (HIT, MISS, BYPASS, EXPIRED, STALE); an empty value, as for
// static files, sends no header.
func pageCacheHeader(s Spec) string {
	if !pageCacheOn(s) {
		return ""
	}
	return "    add_header X-Page-Cache $upstream_cache_status;\n"
}

// removePageCache deletes a website's cached pages (and the folder itself when withDir is set).
func (m *Manager) removePageCache(domain string, withDir bool) error {
	dir := pageCacheDir(domain)
	if !m.Env.Writer.Policy.Allows(dir) {
		return fmt.Errorf("%s is outside the paths the agent may change", dir)
	}
	real, err := m.Env.Root.Path(dir)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(real); err != nil {
		return nil // nothing cached
	}
	if withDir {
		return os.RemoveAll(real)
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(real, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) handlePageCachePurge(ctx context.Context, p json.RawMessage) (any, error) {
	var in struct {
		Domain string `json:"domain"`
	}
	if err := host.Decode(p, &in); err != nil {
		return nil, err
	}
	if err := validate.Domain("domain", in.Domain); err != nil {
		return nil, host.Invalid(err)
	}
	if err := m.removePageCache(in.Domain, false); err != nil {
		return nil, actions.Failed("The page cache could not be emptied.", err.Error())
	}
	return map[string]any{"domain": in.Domain, "purged": true}, nil
}

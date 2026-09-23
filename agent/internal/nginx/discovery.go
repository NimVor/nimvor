package nginx

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Location summarises a location block.
type Location struct {
	Match       string `json:"match"`
	Root        string `json:"root,omitempty"`
	FastCGIPass string `json:"fastcgi_pass,omitempty"`
	ProxyPass   string `json:"proxy_pass,omitempty"`
	Return      string `json:"return,omitempty"`
}

// VHost is one `server { ... }` block.
type VHost struct {
	ServerNames   []string   `json:"server_names"`
	Listen        []string   `json:"listen"`
	SSL           bool       `json:"ssl"`
	HTTP2         bool       `json:"http2"`
	DefaultServer bool       `json:"default_server"`
	Root          string     `json:"root,omitempty"`
	Index         []string   `json:"index,omitempty"`
	CertFile      string     `json:"ssl_certificate,omitempty"`
	KeyFile       string     `json:"ssl_certificate_key,omitempty"`
	FastCGIPass   string     `json:"fastcgi_pass,omitempty"`
	ProxyPass     string     `json:"proxy_pass,omitempty"`
	Return        string     `json:"return,omitempty"`
	RedirectHTTPS bool       `json:"redirects_to_https"`
	AccessLog     string     `json:"access_log,omitempty"`
	ErrorLog      string     `json:"error_log,omitempty"`
	Locations     []Location `json:"locations"`
	File          string     `json:"file"`
	Line          int        `json:"line"`
	Enabled       bool       `json:"enabled"`
}

// Result is the outcome of Nginx discovery.
type Result struct {
	Installed    bool     `json:"installed"`
	Version      string   `json:"version,omitempty"`
	MainConfig   string   `json:"main_config"`
	Files        []string `json:"files"`
	VHosts       []VHost  `json:"vhosts"`
	Warnings     []string `json:"warnings"`
	IncludeRoots []string `json:"include_roots"`
}

// maxIncludeFiles bounds the number of files followed via include.
const maxIncludeFiles = 2000

// Discoverer reads the Nginx configuration without modifying anything.
type Discoverer struct {
	Root   filesystem.Root
	Paths  platform.Paths
	Runner execx.Runner
}

type loader struct {
	root     filesystem.Root
	prefix   string
	allowed  []string
	seen     map[string]bool
	files    []string
	warnings []string
}

// Discover parses the main configuration (following includes) and any
// sites that exist in sites-available but are not enabled.
func (d Discoverer) Discover(ctx context.Context) Result {
	res := Result{
		MainConfig:   d.Paths.NginxMainConfig,
		Files:        []string{},
		VHosts:       []VHost{},
		Warnings:     []string{},
		IncludeRoots: d.Paths.NginxIncludeRoots,
	}
	if !d.Root.Exists(d.Paths.NginxMainConfig) {
		res.Warnings = append(res.Warnings, "Nginx main configuration not found at "+d.Paths.NginxMainConfig)
		return res
	}
	res.Installed = true
	if d.Runner != nil && d.Runner.Available("nginx") {
		if out, err := d.Runner.Run(ctx, "nginx", "-v"); err == nil || len(out) > 0 {
			res.Version = parseVersion(string(out))
		}
	}

	l := &loader{
		root:    d.Root,
		prefix:  filepath.Dir(d.Paths.NginxMainConfig),
		allowed: d.Paths.NginxIncludeRoots,
		seen:    map[string]bool{},
	}
	main := l.load(d.Paths.NginxMainConfig, 0)
	enabled := collectServers(main, true)

	// Sites that exist in sites-available without being enabled.
	enabledFiles := map[string]bool{}
	for _, f := range l.files {
		enabledFiles[f] = true
	}
	var disabled []VHost
	if d.Paths.NginxSitesAvail != "" {
		entries, err := d.Root.ReadDir(d.Paths.NginxSitesAvail)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() || strings.HasPrefix(e.Name(), ".") || strings.HasSuffix(e.Name(), "~") {
					continue
				}
				p := filepath.Join(d.Paths.NginxSitesAvail, e.Name())
				resolved, err := d.Root.Resolve(p)
				if err != nil {
					resolved = p
				}
				if enabledFiles[resolved] || enabledFiles[p] {
					continue
				}
				sub := &loader{root: d.Root, prefix: l.prefix, allowed: l.allowed, seen: map[string]bool{}}
				dirs := sub.load(p, 0)
				// A standalone site file is a list of server blocks.
				disabled = append(disabled, serversIn(dirs, false)...)
				l.warnings = append(l.warnings, sub.warnings...)
			}
		}
	}

	res.VHosts = append(enabled, disabled...)
	res.Files = l.files
	res.Warnings = append(res.Warnings, l.warnings...)
	return res
}

var versionRe = regexp.MustCompile(`nginx/([0-9][0-9.]*)`)

func parseVersion(out string) string {
	if m := versionRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

// load reads and parses file, inlining include directives.
func (l *loader) load(hostPath string, depth int) []Directive {
	if depth > 10 {
		l.warnings = append(l.warnings, "include depth exceeded at "+hostPath)
		return nil
	}
	resolved, err := l.root.Resolve(hostPath)
	if err != nil {
		l.warnings = append(l.warnings, "cannot resolve "+hostPath+": "+cleanErr(err))
		return nil
	}
	if !filesystem.WithinPrefixes(resolved, l.allowed) {
		l.warnings = append(l.warnings, "skipped "+hostPath+": outside permitted configuration roots")
		return nil
	}
	if l.seen[resolved] {
		return nil
	}
	if len(l.seen) >= maxIncludeFiles {
		l.warnings = append(l.warnings, "too many included files; stopped at "+hostPath)
		return nil
	}
	l.seen[resolved] = true
	data, err := l.root.ReadFile(resolved)
	if err != nil {
		l.warnings = append(l.warnings, "cannot read "+resolved+": "+cleanErr(err))
		return nil
	}
	l.files = append(l.files, resolved)
	dirs, err := Parse(string(data), resolved)
	if err != nil {
		l.warnings = append(l.warnings, "parse error: "+err.Error())
		return nil
	}
	return l.expand(dirs, depth)
}

func (l *loader) expand(dirs []Directive, depth int) []Directive {
	out := make([]Directive, 0, len(dirs))
	for _, d := range dirs {
		if d.Name == "include" && len(d.Args) == 1 && !d.HasBlock {
			pattern := d.Args[0]
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(l.prefix, pattern)
			}
			matches, err := l.root.Glob(pattern)
			if err != nil {
				l.warnings = append(l.warnings, "invalid include pattern "+d.Args[0])
				continue
			}
			for _, m := range matches {
				if l.root.IsDir(m) {
					continue
				}
				out = append(out, l.load(m, depth+1)...)
			}
			continue
		}
		if d.HasBlock {
			d.Block = l.expand(d.Block, depth)
		}
		out = append(out, d)
	}
	return out
}

func cleanErr(err error) string {
	s := err.Error()
	// Drop the "open /path:" prefix, the path is already in the message.
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}

// collectServers finds server blocks inside http blocks.
func collectServers(dirs []Directive, enabled bool) []VHost {
	var out []VHost
	for _, d := range dirs {
		if d.Name == "http" && d.HasBlock {
			out = append(out, serversIn(d.Block, enabled)...)
		}
	}
	return out
}

func serversIn(dirs []Directive, enabled bool) []VHost {
	var out []VHost
	for _, d := range dirs {
		if d.Name == "server" && d.HasBlock {
			v := vhostFrom(d)
			v.Enabled = enabled
			out = append(out, v)
		}
	}
	return out
}

func vhostFrom(s Directive) VHost {
	v := VHost{File: s.File, Line: s.Line, ServerNames: []string{}, Listen: []string{}, Locations: []Location{}}
	for _, d := range s.Block {
		switch d.Name {
		case "server_name":
			for _, n := range d.Args {
				if n != "" && n != `""` {
					v.ServerNames = append(v.ServerNames, n)
				}
			}
		case "listen":
			v.Listen = append(v.Listen, strings.Join(d.Args, " "))
			for _, a := range d.Args[1:] {
				switch a {
				case "ssl", "quic":
					v.SSL = true
				case "http2":
					v.HTTP2 = true
				case "default_server", "default":
					v.DefaultServer = true
				}
			}
		case "ssl":
			if len(d.Args) > 0 && d.Args[0] == "on" {
				v.SSL = true
			}
		case "http2":
			if len(d.Args) > 0 && d.Args[0] == "on" {
				v.HTTP2 = true
			}
		case "root":
			v.Root = first(d.Args)
		case "index":
			v.Index = d.Args
		case "ssl_certificate":
			v.CertFile = first(d.Args)
		case "ssl_certificate_key":
			v.KeyFile = first(d.Args)
		case "access_log":
			v.AccessLog = first(d.Args)
		case "error_log":
			v.ErrorLog = first(d.Args)
		case "fastcgi_pass":
			v.FastCGIPass = first(d.Args)
		case "proxy_pass":
			v.ProxyPass = first(d.Args)
		case "return":
			v.Return = strings.Join(d.Args, " ")
		case "if":
			// Common redirect idiom: if ($scheme != "https") { return 301 https://...; }
			for _, inner := range d.Block {
				if inner.Name == "return" && v.Return == "" {
					v.Return = strings.Join(inner.Args, " ")
				}
			}
		case "location":
			loc := locationFrom(d)
			v.Locations = append(v.Locations, loc)
		}
	}
	for _, loc := range v.Locations {
		if v.FastCGIPass == "" && loc.FastCGIPass != "" {
			v.FastCGIPass = loc.FastCGIPass
		}
		if v.ProxyPass == "" && loc.ProxyPass != "" {
			v.ProxyPass = loc.ProxyPass
		}
		if v.Root == "" && loc.Root != "" && (loc.Match == "/" || loc.Match == "= /") {
			v.Root = loc.Root
		}
		if v.Return == "" && loc.Match == "/" && loc.Return != "" {
			v.Return = loc.Return
		}
	}
	if v.CertFile != "" && !v.SSL {
		// Certificates configured on a server without "ssl" in listen are
		// still worth reporting; SSL is on if any listen uses port 443.
		for _, l := range v.Listen {
			if strings.HasSuffix(strings.Fields(l)[0], "443") {
				v.SSL = true
			}
		}
	}
	v.RedirectHTTPS = strings.Contains(v.Return, "https://")
	sort.Strings(v.Listen)
	return v
}

func locationFrom(d Directive) Location {
	loc := Location{Match: strings.Join(d.Args, " ")}
	var walk func([]Directive)
	walk = func(ds []Directive) {
		for _, in := range ds {
			switch in.Name {
			case "root", "alias":
				if loc.Root == "" {
					loc.Root = first(in.Args)
				}
			case "fastcgi_pass":
				if loc.FastCGIPass == "" {
					loc.FastCGIPass = first(in.Args)
				}
			case "proxy_pass":
				if loc.ProxyPass == "" {
					loc.ProxyPass = first(in.Args)
				}
			case "return":
				if loc.Return == "" {
					loc.Return = strings.Join(in.Args, " ")
				}
			case "location", "if":
				walk(in.Block)
			}
		}
	}
	walk(d.Block)
	return loc
}

func first(a []string) string {
	if len(a) == 0 {
		return ""
	}
	return a[0]
}

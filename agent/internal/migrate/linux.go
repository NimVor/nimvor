package migrate

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// linuxAdapter reads a server without a control panel: the sites come from
// the Nginx and Apache configuration, the databases from MySQL/MariaDB.
type linuxAdapter struct{}

func (linuxAdapter) ID() string       { return "linux" }
func (linuxAdapter) Name() string     { return "Custom Linux server (no panel)" }
func (linuxAdapter) Support() Support { return Experimental }

// Detect always succeeds: it is the fallback after the panels.
func (linuxAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq("head -n 6 /etc/os-release 2>/dev/null; uname -sr"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	name := ""
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			name = strings.Trim(v, `"`)
		}
	}
	return Detection{Panel: "linux", Name: "Custom Linux server", Version: name, Support: Experimental}, true, nil
}

const webConfigScript = `for f in /etc/nginx/sites-enabled/* /etc/nginx/conf.d/*.conf /etc/apache2/sites-enabled/* /etc/httpd/conf.d/*.conf; do [ -f "$f" ] && { echo "##file $f"; cat "$f"; }; done`

const dbScript = `mysql -N -e "SELECT table_schema, ROUND(SUM(data_length+index_length)/1024) FROM information_schema.tables WHERE table_schema NOT IN ('mysql','sys','information_schema','performance_schema') GROUP BY table_schema" 2>/dev/null`

func (linuxAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	progress("Reading the web server configuration…")
	out, err := t.Exec(ctx, "sh -c "+Shq(webConfigScript), nil)
	if err != nil {
		return nil, err
	}
	sites := parseWebConfig(out)
	if len(sites) == 0 {
		d.Warnings = append(d.Warnings, "No Nginx or Apache site was found in /etc/nginx and /etc/apache2 or /etc/httpd. Sites configured elsewhere are not listed.")
	}
	roots := map[string]int64{}
	for _, s := range sites {
		roots[s.DocRoot] = -1
	}
	if len(roots) > 0 {
		progress("Measuring the document roots…")
		sizes, err := measure(ctx, t, keys(roots))
		if err != nil {
			d.Warnings = append(d.Warnings, "The sizes of the document roots could not be measured: "+err.Error())
		}
		for p, n := range sizes {
			roots[p] = n
		}
	}
	acct := Account{ID: "server", Name: "server", Home: "/", Domains: []Domain{}, Databases: []Database{}, Email: []EmailAccount{}, Forwarders: []Forwarder{}, Cron: []CronJob{}}
	for _, s := range sites {
		s.SizeKB = roots[s.DocRoot]
		acct.Domains = append(acct.Domains, s)
	}
	progress("Listing the databases…")
	dbOut, err := t.Exec(ctx, "sh -c "+Shq(dbScript), nil)
	if err != nil || strings.TrimSpace(dbOut) == "" {
		d.Warnings = append(d.Warnings, "The databases could not be listed (the login needs access to MySQL/MariaDB, for example as root over the socket).")
	}
	acct.Databases = parseSchemas(dbOut, info.Engine)
	d.Accounts = []Account{acct}
	return d, nil
}

func keys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// safeRemotePath accepts an absolute path of plain characters: it is quoted
// anyway, this keeps odd values out of the commands altogether.
var safeRemotePath = regexp.MustCompile(`^/[A-Za-z0-9._@+=,:/ -]{0,1000}$`)

// ValidRemotePath reports whether p may be used in a command on the source.
func ValidRemotePath(p string) bool {
	if !safeRemotePath.MatchString(p) || strings.Contains(p, "..") {
		return false
	}
	return path.Clean(p) == strings.TrimSuffix(p, "/") || p == "/"
}

// measure returns the size in KB of each folder.
func measure(ctx context.Context, t Transport, dirs []string) (map[string]int64, error) {
	var args []string
	for _, p := range dirs {
		if ValidRemotePath(p) {
			args = append(args, Shq(p))
		}
	}
	if len(args) == 0 {
		return nil, nil
	}
	out, err := t.Exec(ctx, "sh -c "+Shq("du -sk -- "+strings.Join(args, " ")+" 2>/dev/null; true"), nil)
	if err != nil {
		return nil, err
	}
	res := map[string]int64{}
	for _, l := range strings.Split(out, "\n") {
		if i := strings.IndexByte(l, '\t'); i > 0 {
			if n, err := strconv.ParseInt(strings.TrimSpace(l[:i]), 10, 64); err == nil {
				res[strings.TrimSpace(l[i+1:])] = n
			}
		}
	}
	return res, nil
}

func parseSchemas(out, engine string) []Database {
	if engine == "" {
		engine = "mysql"
	}
	dbs := []Database{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(l), "\t")
		if len(f) != 2 || f[0] == "" {
			continue
		}
		n, _ := strconv.ParseInt(f[1], 10, 64)
		dbs = append(dbs, Database{Name: f[0], Engine: engine, SizeKB: n})
	}
	sort.Slice(dbs, func(i, j int) bool { return dbs[i].Name < dbs[j].Name })
	return dbs
}

var (
	nginxServer = regexp.MustCompile(`(?m)^\s*server\s*\{`)
	nginxName   = regexp.MustCompile(`(?m)^\s*server_name\s+([^;]+);`)
	nginxRoot   = regexp.MustCompile(`(?m)^\s*root\s+([^;]+);`)
	apacheVHost = regexp.MustCompile(`(?is)<VirtualHost[^>]*>(.*?)</VirtualHost>`)
	apacheName  = regexp.MustCompile(`(?mi)^\s*ServerName\s+(\S+)`)
	apacheRoot  = regexp.MustCompile(`(?mi)^\s*DocumentRoot\s+"?([^"\n]+?)"?\s*$`)
	domainName  = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	fileMarker  = regexp.MustCompile(`(?m)^##file (.+)$`)
)

// parseWebConfig reads the Nginx server blocks and Apache virtual hosts in
// the output of webConfigScript.
func parseWebConfig(out string) []Domain {
	var sites []Domain
	seen := map[string]bool{}
	add := func(name, root string) {
		name = strings.ToLower(strings.TrimSpace(name))
		root = strings.TrimSpace(strings.Trim(strings.TrimSpace(root), `"'`))
		if !domainName.MatchString(name) || !ValidRemotePath(root) || seen[name] {
			return
		}
		seen[name] = true
		sites = append(sites, Domain{Name: name, Kind: "site", DocRoot: strings.TrimSuffix(root, "/"), SizeKB: -1})
	}
	for _, block := range splitFiles(out) {
		for _, body := range nginxBlocks(block) {
			n := nginxName.FindStringSubmatch(body)
			r := nginxRoot.FindStringSubmatch(body)
			if n == nil || r == nil {
				continue
			}
			for _, name := range strings.Fields(n[1]) {
				add(name, r[1])
			}
		}
		for _, m := range apacheVHost.FindAllStringSubmatch(block, -1) {
			n := apacheName.FindStringSubmatch(m[1])
			r := apacheRoot.FindStringSubmatch(m[1])
			if n != nil && r != nil {
				add(n[1], r[1])
			}
		}
	}
	return sites
}

func splitFiles(out string) []string {
	idx := fileMarker.FindAllStringIndex(out, -1)
	var parts []string
	for i, m := range idx {
		end := len(out)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		parts = append(parts, out[m[1]:end])
	}
	return parts
}

// nginxBlocks returns the body of each top-level "server { … }" (braces counted).
func nginxBlocks(s string) []string {
	var bodies []string
	for _, loc := range nginxServer.FindAllStringIndex(s, -1) {
		depth, start := 1, loc[1]
		for i := start; i < len(s); i++ {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					bodies = append(bodies, s[start:i])
					i = len(s)
				}
			}
		}
	}
	return bodies
}

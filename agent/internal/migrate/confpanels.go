package migrate

import (
	"context"
	"regexp"
	"strings"
)

// Panels that generate ordinary Nginx/Apache virtual hosts in their own folders (CloudPanel, aaPanel):
// the sites are read from those files with the same reader as a server without a panel, then grouped
// into accounts by where their files live. Databases are listed for the whole server under one
// pseudo-account "databases" because these panels' own databases are not read.

// confPanel describes one such panel.
type confPanel struct {
	id, name string
	detect   string                              // shell test that prints the id when the panel is present
	confs    string                              // shell snippet that prints "##file <path>" blocks of the vhost files
	group    func(Domain) (account, home string) // account of a site; ("", "") leaves the site out
}

func (p confPanel) ID() string       { return p.id }
func (p confPanel) Name() string     { return p.name }
func (p confPanel) Support() Support { return Experimental }

func (p confPanel) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq(p.detect+"; true"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	if !strings.Contains(out, p.id) {
		return Detection{}, false, nil
	}
	return Detection{Panel: p.id, Name: p.name, Support: Experimental}, true, nil
}

func (p confPanel) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	progress("Reading the web server configuration of " + p.name + "…")
	out, err := t.Exec(ctx, "sh -c "+Shq(p.confs), nil)
	if err != nil {
		return nil, err
	}
	accts := groupSites(parseWebConfig(out), p.group)
	if len(accts) == 0 {
		d.Warnings = append(d.Warnings, "No sites were found in "+p.name+"'s web server configuration.")
	}
	d.Accounts = finishAccounts(ctx, t, info, accts, progress)
	progress("Listing the databases…")
	if s, err := t.Exec(ctx, "sh -c "+Shq(dbScript), nil); err == nil && strings.TrimSpace(s) != "" {
		if dbs := parseSchemas(s, info.Engine); len(dbs) > 0 {
			a := newAccount("databases", "/")
			a.Name = "databases (all of the server)"
			a.Databases = dbs
			d.Accounts = append(d.Accounts, *a)
		}
	} else {
		d.Warnings = append(d.Warnings, "The databases could not be listed (the login needs access to MariaDB/MySQL).")
	}
	d.Warnings = append(d.Warnings, p.name+"'s own database of sites and databases, mailboxes, forwarders, DNS, FTP and cron are not read: choose each database's destination yourself.")
	return d, nil
}

// groupSites sorts sites into accounts by the panel's own rule.
func groupSites(sites []Domain, group func(Domain) (string, string)) []*Account {
	set := newAcctSet(func(string) string { return "/" })
	for _, s := range sites {
		id, home := group(s)
		if id == "" {
			continue
		}
		a := set.get(id)
		if home != "" {
			a.Home = home
		}
		a.Domains = append(a.Domains, s)
	}
	return set.list()
}

var cloudPanelRoot = regexp.MustCompile(`^/home/([A-Za-z0-9._-]+)/htdocs/`)

func cloudPanelGroup(d Domain) (string, string) {
	m := cloudPanelRoot.FindStringSubmatch(d.DocRoot)
	if m == nil || m[1] == "clp" { // clp is CloudPanel's own site
		return "", ""
	}
	return m[1], "/home/" + m[1]
}

func aaPanelGroup(d Domain) (string, string) {
	if !strings.HasPrefix(d.DocRoot, "/www/wwwroot/") {
		return "", "" // phpMyAdmin and the panel's own sites are not websites to move
	}
	return "aapanel", "/www/wwwroot"
}

func cloudPanel() confPanel {
	return confPanel{
		id: "cloudpanel", name: "CloudPanel",
		detect: `{ [ -d /home/clp/htdocs/app ] || command -v clpctl >/dev/null 2>&1; } && echo cloudpanel`,
		confs:  `for f in /etc/nginx/sites-enabled/* /etc/nginx/conf.d/*.conf; do [ -f "$f" ] && { echo "##file $f"; cat "$f"; }; done`,
		group:  cloudPanelGroup,
	}
}

func aaPanel() confPanel {
	return confPanel{
		id: "aapanel", name: "aaPanel",
		detect: `{ [ -d /www/server/panel ] && [ -d /www/wwwroot ]; } && echo aapanel`,
		confs:  `for f in /www/server/panel/vhost/nginx/*.conf /www/server/panel/vhost/apache/*.conf; do [ -f "$f" ] && { echo "##file $f"; cat "$f"; }; done`,
		group:  aaPanelGroup,
	}
}

// ---- InterWorx -----------------------------------------------------------------

// interWorxAdapter reads InterWorx (SiteWorx accounts) from its documented folders: every
// /home/<unixuser>/<domain>/html with a /home/<unixuser>/var/<domain> beside it is a domain of the
// account <unixuser>. Databases are matched to accounts by the "<unixuser>_" prefix. Mailboxes and
// everything else are not read.
type interWorxAdapter struct{}

func (interWorxAdapter) ID() string       { return "interworx" }
func (interWorxAdapter) Name() string     { return "InterWorx" }
func (interWorxAdapter) Support() Support { return Experimental }

func (interWorxAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq("[ -d /usr/local/interworx ] && echo interworx; true"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	if !strings.Contains(out, "interworx") {
		return Detection{}, false, nil
	}
	return Detection{Panel: "interworx", Name: "InterWorx", Support: Experimental}, true, nil
}

const interWorxScript = `for h in /home/*; do u=$(basename "$h"); for d in "$h"/*/html; do [ -d "$d" ] || continue; dom=$(basename "$(dirname "$d")"); [ -d "$h/var/$dom" ] && echo "$u $dom"; done; done`

func (interWorxAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	progress("Looking for SiteWorx accounts…")
	out, err := t.Exec(ctx, "sh -c "+Shq(interWorxScript), nil)
	if err != nil {
		return nil, err
	}
	accts := parseInterWorx(out)
	if len(accts) == 0 {
		d.Warnings = append(d.Warnings, "No SiteWorx accounts were found under /home. The login must be root.")
	}
	if s, err := t.Exec(ctx, "sh -c "+Shq(dbScript), nil); err == nil {
		for _, db := range parseSchemas(s, info.Engine) {
			for _, a := range accts {
				if strings.HasPrefix(db.Name, a.ID+"_") {
					a.Databases = append(a.Databases, db)
				}
			}
		}
	}
	d.Accounts = finishAccounts(ctx, t, info, accts, progress)
	d.Warnings = append(d.Warnings, "Databases are matched to accounts by the account name and an underscore. Mailboxes, forwarders, DNS, FTP and cron are not read from InterWorx.")
	return d, nil
}

func parseInterWorx(out string) []*Account {
	set := newAcctSet(func(id string) string { return "/home/" + id })
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) != 2 || !pleskLogin.MatchString(f[0]) || !domainName.MatchString(f[1]) {
			continue
		}
		a := set.get(f[0])
		kind := "addon"
		if len(a.Domains) == 0 {
			kind = "main"
		}
		a.Domains = append(a.Domains, Domain{Name: strings.ToLower(f[1]), Kind: kind, DocRoot: "/home/" + f[0] + "/" + f[1] + "/html", SizeKB: -1})
	}
	return set.list()
}

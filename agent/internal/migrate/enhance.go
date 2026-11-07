package migrate

import (
	"context"
	"regexp"
	"strings"
)

// enhanceAdapter gives a limited reading of an Enhance server. Enhance keeps each website's account in
// /var/www/<website-id> (the id is a UUID), so the folders that look like that and hold a public_html
// folder are listed as sites, named by their id: the domain names are not read, because where Enhance
// keeps them could not be verified. Databases are listed for the whole server. Nothing else is read.
//
// Written from a hosting provider's description of the layout and tested on synthetic data only.
type enhanceAdapter struct{}

func (enhanceAdapter) ID() string       { return "enhance" }
func (enhanceAdapter) Name() string     { return "Enhance (limited)" }
func (enhanceAdapter) Support() Support { return Experimental }

func (enhanceAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq(`systemctl list-unit-files 2>/dev/null | grep -Eqi '^(orchd|appcd)[.a-z]*[[:space:]]' && echo enhance; true`), nil)
	if err != nil {
		return Detection{}, false, err
	}
	if !strings.Contains(out, "enhance") {
		return Detection{}, false, nil
	}
	return Detection{Panel: "enhance", Name: "Enhance (limited)", Support: Experimental}, true, nil
}

const enhanceScript = `for d in /var/www/*/public_html; do [ -d "$d" ] && basename "$(dirname "$d")"; done`

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (enhanceAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	progress("Looking for Enhance websites under /var/www…")
	out, err := t.Exec(ctx, "sh -c "+Shq(enhanceScript), nil)
	if err != nil {
		return nil, err
	}
	accts := parseEnhance(out)
	if len(accts) == 0 {
		d.Warnings = append(d.Warnings, "No website folders (/var/www/<id>/public_html) were found. The login must be root.")
	}
	d.Accounts = finishAccounts(ctx, t, info, accts, progress)
	if s, err := t.Exec(ctx, "sh -c "+Shq(dbScript), nil); err == nil && strings.TrimSpace(s) != "" {
		if dbs := parseSchemas(s, info.Engine); len(dbs) > 0 {
			a := newAccount("databases", "/")
			a.Name = "databases (all of the server)"
			a.Databases = dbs
			d.Accounts = append(d.Accounts, *a)
		}
	}
	d.Warnings = append(d.Warnings, "Enhance websites are listed by their id (the folder name), not by domain name; open the site in Enhance to see which id is which. Databases are listed for the whole server and you choose each destination. Mailboxes, forwarders, DNS, FTP and cron are not read.")
	return d, nil
}

func parseEnhance(out string) []*Account {
	set := newAcctSet(func(id string) string { return "/var/www/" + id })
	for _, l := range strings.Split(out, "\n") {
		id := strings.ToLower(strings.TrimSpace(l))
		if !uuidRe.MatchString(id) {
			continue
		}
		a := set.get(id)
		if len(a.Domains) == 0 {
			a.Domains = append(a.Domains, Domain{Name: id, Kind: "site", DocRoot: "/var/www/" + id + "/public_html", SizeKB: -1})
		}
	}
	return set.list()
}

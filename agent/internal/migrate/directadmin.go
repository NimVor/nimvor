package migrate

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// directAdminAdapter reads a DirectAdmin server from its files, over SSH as root:
//
//   - accounts: /usr/local/directadmin/data/users/<user>/ (one folder per user)
//   - domains: .../<user>/domains.list, one domain per line; document roots are
//     /home/<user>/domains/<domain>/public_html
//   - subdomains: .../<user>/domains/<domain>.subdomains
//   - mailboxes: /etc/virtual/<domain>/passwd, in /home/<user>/imap/<domain>/<name>/Maildir
//   - databases: DirectAdmin names a database "<user>_<name>", like cPanel.
//
// Written from DirectAdmin's documented layout and tested on synthetic data only: it stays
// Experimental until it has run on real servers.
type directAdminAdapter struct{}

func (directAdminAdapter) ID() string       { return "directadmin" }
func (directAdminAdapter) Name() string     { return "DirectAdmin" }
func (directAdminAdapter) Support() Support { return Experimental }

func (directAdminAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq("[ -x /usr/local/directadmin/directadmin ] && /usr/local/directadmin/directadmin v 2>/dev/null; true"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	m := regexp.MustCompile(`(?i)version:?\s*v?(\d+(?:\.\d+)+)`).FindStringSubmatch(out)
	if m == nil {
		return Detection{}, false, nil
	}
	return Detection{Panel: "directadmin", Name: "DirectAdmin", Version: m[1], Support: Experimental}, true, nil
}

const daUsersScript = `ls -1 /usr/local/directadmin/data/users 2>/dev/null`

func (directAdminAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	out, err := t.Exec(ctx, "sh -c "+Shq(daUsersScript), nil)
	if err != nil {
		return nil, err
	}
	var users []string
	for _, u := range strings.Split(out, "\n") {
		u = strings.TrimSpace(u)
		if cpUserRe.MatchString(u) && u != "admin_backup" {
			users = append(users, u)
		}
	}
	sort.Strings(users)
	if len(users) == 0 {
		d.Warnings = append(d.Warnings, "No users were found in /usr/local/directadmin/data/users. The login must be root.")
	}
	schemas := ""
	if len(users) > 0 {
		progress("Listing the databases…")
		if s, err := t.Exec(ctx, "sh -c "+Shq(dbScript), nil); err == nil && strings.TrimSpace(s) != "" {
			schemas = s
		} else {
			d.Warnings = append(d.Warnings, "The databases could not be listed (the login needs access to MariaDB/MySQL).")
		}
	}
	allDBs := parseSchemas(schemas, info.Engine)
	for _, u := range users {
		progress("Reading the user " + u + "…")
		script := fmt.Sprintf(`b=/usr/local/directadmin/data/users/%[1]s
echo '##dom'; cat "$b/domains.list" 2>/dev/null
echo '##sub'; for f in "$b"/domains/*.subdomains; do [ -f "$f" ] && { echo "@@d $(basename "$f" .subdomains)"; cat "$f"; }; done 2>/dev/null
echo '##mail'; for f in /etc/virtual/*/passwd; do dom=$(basename "$(dirname "$f")"); [ -d /home/%[1]s/imap/$dom ] && { echo "@@d $dom"; cut -d: -f1 "$f"; }; done 2>/dev/null
echo '##fwd'; for dom in $(cat "$b/domains.list" 2>/dev/null); do [ -f /etc/virtual/$dom/aliases ] && { echo "@@d $dom"; cat /etc/virtual/$dom/aliases; }; done 2>/dev/null
echo '##end'`, u)
		res, err := t.Exec(ctx, "sh -c "+Shq(script), nil)
		if err != nil {
			d.Warnings = append(d.Warnings, "The user "+u+" could not be read: "+err.Error())
			continue
		}
		a := parseDirectAdminUser(u, res)
		for _, db := range allDBs {
			if strings.HasPrefix(db.Name, u+"_") {
				a.Databases = append(a.Databases, db)
			}
		}
		var dirs []string
		for _, dom := range a.Domains {
			dirs = append(dirs, dom.DocRoot)
		}
		for _, m := range a.Email {
			dirs = append(dirs, m.Maildir)
		}
		if sz, err := measure(ctx, t, uniq(dirs)); err == nil {
			for i := range a.Domains {
				if n, ok := sz[a.Domains[i].DocRoot]; ok {
					a.Domains[i].SizeKB = n
				}
			}
			for i := range a.Email {
				if n, ok := sz[a.Email[i].Maildir]; ok {
					a.Email[i].SizeKB = n
				}
			}
		}
		d.Accounts = append(d.Accounts, *a)
	}
	d.Warnings = append(d.Warnings, "Databases are matched to users by DirectAdmin's naming (the user name and an underscore). PHP versions per domain, forwarders, DNS, FTP and cron are not read.")
	return d, nil
}

// parseDirectAdminUser turns the output of the per-user script into an account. The first domain of
// domains.list is the user's main domain; names that are not plain are left out.
func parseDirectAdminUser(user, out string) *Account {
	sec := sections(out)
	home := "/home/" + user
	a := &Account{ID: user, Name: user, Home: home, Domains: []Domain{}, Databases: []Database{}, Email: []EmailAccount{}, Forwarders: []Forwarder{}, FTP: []FTPAccount{}, Cron: []CronJob{}}
	var doms []string
	for i, l := range sec["dom"] {
		l = strings.ToLower(strings.TrimSpace(l))
		if !domainName.MatchString(l) {
			continue
		}
		kind := "addon"
		if i == 0 {
			kind = "main"
		}
		doms = append(doms, l)
		a.Domains = append(a.Domains, Domain{Name: l, Kind: kind, DocRoot: home + "/domains/" + l + "/public_html", SizeKB: -1})
	}
	known := map[string]bool{}
	for _, dn := range doms {
		known[dn] = true
	}
	cur := ""
	for _, l := range sec["sub"] {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "@@d ") {
			cur = strings.ToLower(strings.TrimPrefix(l, "@@d "))
			if !known[cur] {
				cur = ""
			}
			continue
		}
		if cur != "" && pleskLabel.MatchString(l) {
			a.Domains = append(a.Domains, Domain{Name: strings.ToLower(l) + "." + cur, Kind: "sub", DocRoot: home + "/domains/" + cur + "/public_html/" + l, SizeKB: -1})
		}
	}
	cur = ""
	for _, l := range sec["mail"] {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "@@d ") {
			cur = strings.ToLower(strings.TrimPrefix(l, "@@d "))
			if !known[cur] {
				cur = ""
			}
			continue
		}
		if cur != "" && pleskLocal.MatchString(l) {
			a.Email = append(a.Email, EmailAccount{Address: strings.ToLower(l) + "@" + cur, Maildir: home + "/imap/" + cur + "/" + l + "/Maildir", SizeKB: -1})
		}
	}
	cur = ""
	for _, l := range sec["fwd"] {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "@@d ") {
			cur = strings.ToLower(strings.TrimPrefix(l, "@@d "))
			if !known[cur] {
				cur = ""
			}
			continue
		}
		if cur != "" {
			if f, ok := parseValias(cur, l); ok {
				a.Forwarders = append(a.Forwarders, f)
			}
		}
	}
	return a
}

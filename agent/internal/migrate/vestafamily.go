package migrate

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// vestaFamily reads VestaCP and HestiaCP (Hestia is a fork of Vesta, with the same files), over SSH as
// root. Per user, under <root>/data/users/<user>/:
//
//   - web.conf: one line per web domain, as KEY='value' pairs (DOMAIN='example.com' …); the files are
//     in /home/<user>/web/<domain>/public_html
//   - db.conf: one line per database (DB='user_name' … TYPE='mysql' …)
//   - mail/<domain>.conf: one line per mailbox (ACCOUNT='name' …); the Maildir is
//     /home/<user>/mail/<domain>/<account>
//
// Written from the layout both projects document and tested on synthetic data only: Experimental.
type vestaFamily struct {
	id, name, root string
}

func (v vestaFamily) ID() string       { return v.id }
func (v vestaFamily) Name() string     { return v.name }
func (v vestaFamily) Support() Support { return Experimental }

func (v vestaFamily) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	script := fmt.Sprintf(`[ -x %[1]s/bin/v-list-sys-info ] && { echo present; grep -h '^VERSION=' %[1]s/conf/*.conf 2>/dev/null | head -n 1; }; true`, v.root)
	out, err := t.Exec(ctx, "sh -c "+Shq(script), nil)
	if err != nil {
		return Detection{}, false, err
	}
	if !strings.Contains(out, "present") {
		return Detection{}, false, nil
	}
	ver := ""
	if m := regexp.MustCompile(`VERSION='?([0-9][0-9.]*)`).FindStringSubmatch(out); m != nil {
		ver = m[1]
	}
	return Detection{Panel: v.id, Name: v.name, Version: ver, Support: Experimental}, true, nil
}

func (v vestaFamily) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	out, err := t.Exec(ctx, "sh -c "+Shq(fmt.Sprintf("ls -1 %s/data/users 2>/dev/null", v.root)), nil)
	if err != nil {
		return nil, err
	}
	var users []string
	for _, u := range strings.Split(out, "\n") {
		if u = strings.TrimSpace(u); pleskLogin.MatchString(u) {
			users = append(users, u)
		}
	}
	sort.Strings(users)
	if len(users) == 0 {
		d.Warnings = append(d.Warnings, "No users were found in "+v.root+"/data/users. The login must be root.")
	}
	for _, u := range users {
		progress("Reading the user " + u + "…")
		b := v.root + "/data/users/" + u
		script := fmt.Sprintf(`echo '##web'; cat %[1]s/web.conf 2>/dev/null
echo '##db'; cat %[1]s/db.conf 2>/dev/null
echo '##mail'; for f in %[1]s/mail/*.conf; do [ -f "$f" ] && { echo "@@d $(basename "$f" .conf)"; cat "$f"; }; done 2>/dev/null
echo '##end'`, b)
		res, err := t.Exec(ctx, "sh -c "+Shq(script), nil)
		if err != nil {
			d.Warnings = append(d.Warnings, "The user "+u+" could not be read: "+err.Error())
			continue
		}
		a := parseVestaUser(u, res)
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
		if len(a.Databases) > 0 {
			progress("Reading the database sizes…")
			if s, err := t.Exec(ctx, "sh -c "+Shq(dbScript), nil); err == nil {
				size := map[string]int64{}
				for _, db := range parseSchemas(s, info.Engine) {
					size[db.Name] = db.SizeKB
				}
				for i := range a.Databases {
					if n, ok := size[a.Databases[i].Name]; ok {
						a.Databases[i].SizeKB = n
					}
				}
			}
		}
		d.Accounts = append(d.Accounts, *a)
	}
	d.Warnings = append(d.Warnings, "PHP versions per domain, forwarders, DNS, FTP and cron are not read.")
	return d, nil
}

var vestaKV = regexp.MustCompile(`([A-Z_]+)='([^']*)'`)

// vestaLine reads one KEY='value' line into a map.
func vestaLine(l string) map[string]string {
	m := map[string]string{}
	for _, kv := range vestaKV.FindAllStringSubmatch(l, -1) {
		m[kv[1]] = kv[2]
	}
	return m
}

// parseVestaUser turns the output of the per-user script into an account.
func parseVestaUser(user, out string) *Account {
	sec := sections(out)
	home := "/home/" + user
	a := &Account{ID: user, Name: user, Home: home, Domains: []Domain{}, Databases: []Database{}, Email: []EmailAccount{}, Forwarders: []Forwarder{}, Cron: []CronJob{}}
	seen := map[string]bool{}
	for _, l := range sec["web"] {
		dn := strings.ToLower(vestaLine(l)["DOMAIN"])
		if !domainName.MatchString(dn) || seen[dn] {
			continue
		}
		seen[dn] = true
		a.Domains = append(a.Domains, Domain{Name: dn, Kind: "site", DocRoot: home + "/web/" + dn + "/public_html", SizeKB: -1})
	}
	for _, l := range sec["db"] {
		kv := vestaLine(l)
		if kv["TYPE"] == "mysql" && dbNameRe.MatchString(kv["DB"]) {
			a.Databases = append(a.Databases, Database{Name: kv["DB"], Engine: "mysql", SizeKB: -1})
		}
	}
	cur := ""
	for _, l := range sec["mail"] {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "@@d ") {
			cur = strings.ToLower(strings.TrimPrefix(l, "@@d "))
			if !domainName.MatchString(cur) {
				cur = ""
			}
			continue
		}
		if acct := vestaLine(l)["ACCOUNT"]; cur != "" && pleskLocal.MatchString(acct) {
			a.Email = append(a.Email, EmailAccount{Address: strings.ToLower(acct) + "@" + cur, Maildir: home + "/mail/" + cur + "/" + acct, SizeKB: -1})
		}
	}
	return a
}

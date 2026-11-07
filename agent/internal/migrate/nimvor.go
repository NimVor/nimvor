package migrate

import (
	"context"
	"fmt"
	"strings"
)

// nimvorAdapter reads another NimVor server from its own panel database (read-only, through the
// sqlite3 client): websites with their system user and document root, the databases tied to each site,
// mailboxes and aliases, DNS zones and cron jobs. Mail lives in /var/vmail/<domain>/<local>. The schema
// is NimVor's own, so this reader is checked against it; FTP accounts are not read.
type nimvorAdapter struct{}

const nimvorDB = "/var/lib/nimvor/nimvor.sqlite"

func (nimvorAdapter) ID() string       { return "nimvor" }
func (nimvorAdapter) Name() string     { return "NimVor" }
func (nimvorAdapter) Support() Support { return Experimental }

func (nimvorAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq("[ -f "+nimvorDB+" ] && { echo nimvor; head -n 1 /opt/nimvor/VERSION 2>/dev/null; }; true"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if lines[0] != "nimvor" {
		return Detection{}, false, nil
	}
	det := Detection{Panel: "nimvor", Name: "NimVor", Support: Experimental}
	if len(lines) > 1 {
		det.Version = strings.TrimSpace(lines[1])
	}
	return det, true, nil
}

const nimvorSep = "\x1f"

// nimvorRows runs one SELECT on NimVor's panel database. Fields are separated by the ASCII unit
// separator, so commands and values may contain | and spaces.
func nimvorRows(ctx context.Context, t Transport, query string) ([][]string, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq(fmt.Sprintf(`sqlite3 -readonly -separator "$(printf '\037')" %s %s 2>&1`, nimvorDB, Shq(query))), nil)
	if err != nil {
		return nil, err
	}
	var rows [][]string
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if strings.HasPrefix(l, "Error:") || strings.HasPrefix(l, "Parse error") || strings.Contains(l, "sqlite3: not found") || strings.Contains(l, "sqlite3: command not found") {
			return nil, fmt.Errorf("%s", truncate(l, 160))
		}
		rows = append(rows, strings.Split(l, nimvorSep))
	}
	return rows, nil
}

func (nimvorAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	progress("Reading NimVor's websites…")
	sites, err := nimvorRows(ctx, t, `SELECT domain, system_user, root, document_root, CASE WHEN parent_id IS NULL THEN 'main' ELSE 'addon' END FROM websites ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("NimVor's database could not be read (log in as root and install sqlite3 on the source): %v", err)
	}
	var dbs, mail, alias, cron [][]string
	if dbs, err = nimvorRows(ctx, t, `SELECT d.name, COALESCE(w.domain, '') FROM databases d LEFT JOIN websites w ON w.id = d.website_id`); err != nil {
		d.Warnings = append(d.Warnings, "The databases could not be read: "+err.Error())
	}
	if mail, err = nimvorRows(ctx, t, `SELECT m.local || '@' || d.domain FROM mail_mailboxes m JOIN mail_domains d ON d.id = m.domain_id`); err != nil {
		d.Warnings = append(d.Warnings, "The mailboxes could not be read: "+err.Error())
	}
	if alias, err = nimvorRows(ctx, t, `SELECT CASE WHEN a.local = '' THEN '*' ELSE a.local END || '@' || d.domain, a.destinations FROM mail_aliases a JOIN mail_domains d ON d.id = a.domain_id`); err != nil {
		d.Warnings = append(d.Warnings, "The aliases could not be read: "+err.Error())
	}
	if cron, err = nimvorRows(ctx, t, `SELECT w.domain, c.schedule, c.command FROM cron_jobs c JOIN websites w ON w.id = c.website_id ORDER BY c.id`); err != nil {
		d.Warnings = append(d.Warnings, "The cron jobs could not be read: "+err.Error())
	}
	accts := buildNimVor(sites, dbs, mail, alias, cron)
	if len(accts) == 0 {
		d.Warnings = append(d.Warnings, "No websites were found in NimVor.")
	}
	var all []*Account
	for _, a := range accts {
		all = append(all, a)
	}
	d.Accounts = finishAccounts(ctx, t, info, all, progress)
	d.Warnings = append(d.Warnings, "FTP accounts are not read from NimVor; DNS zones are read for the domains of the websites.")
	return d, nil
}

func buildNimVor(sites, dbs, mail, alias, cron [][]string) []*Account {
	set := newAcctSet(func(id string) string { return "/home/" + id })
	owner := map[string]string{} // domain → system user
	for _, r := range sites {
		dn, user, root, doc, kind := strings.ToLower(cell(r, 0)), cell(r, 1), cell(r, 2), cell(r, 3), cell(r, 4)
		if !domainName.MatchString(dn) || !pleskLogin.MatchString(user) || !ValidRemotePath(doc) {
			continue
		}
		a := set.get(user)
		if kind == "main" && ValidRemotePath(root) && a.Home == "/home/"+user {
			a.Home = strings.TrimSuffix(root, "/")
		}
		a.Domains = append(a.Domains, Domain{Name: dn, Kind: kind, DocRoot: strings.TrimSuffix(doc, "/"), SizeKB: -1})
		owner[dn] = user
	}
	var unlinked []Database
	for _, r := range dbs {
		name, site := cell(r, 0), strings.ToLower(cell(r, 1))
		if !dbNameRe.MatchString(name) {
			continue
		}
		db := Database{Name: name, Engine: "mysql", SizeKB: -1}
		if u := owner[site]; u != "" {
			set.get(u).Databases = append(set.get(u).Databases, db)
		} else {
			unlinked = append(unlinked, db)
		}
	}
	var mailAcct *Account
	for _, r := range mail {
		addr := strings.ToLower(cell(r, 0))
		m := mailAddrRe.FindStringSubmatch(addr)
		if m == nil {
			continue
		}
		local, dom := m[1], m[2]
		var a *Account
		if u := owner[dom]; u != "" {
			a = set.get(u)
		} else {
			if mailAcct == nil {
				mailAcct = set.get("mail")
				mailAcct.Name = "mail (domains without a website)"
				mailAcct.Home = "/"
			}
			a = mailAcct
		}
		a.Email = append(a.Email, EmailAccount{Address: addr, Maildir: "/var/vmail/" + dom + "/" + local, SizeKB: -1})
	}
	for _, r := range cron {
		if u := owner[strings.ToLower(cell(r, 0))]; u != "" {
			sched, cmd := cell(r, 1), cell(r, 2)
			if sched != "" && cmd != "" && len(cmd) <= 2000 {
				set.get(u).Cron = append(set.get(u).Cron, CronJob{Schedule: sched, Command: cmd})
			}
		}
	}
	out := attachForwarders(set.list(), forwardersFromRows(alias))
	if len(unlinked) > 0 {
		a := newAccount("databases", "/")
		a.Name = "databases (not linked to a site)"
		a.Databases = unlinked
		out = append(out, a)
	}
	return out
}

// nimvorZone renders a domain's DNS records from NimVor's database as a BIND zone file, so the DNS
// import works like it does for the panels that keep zone files.
func nimvorZone(ctx context.Context, t Transport, domain string) (string, error) {
	if !domainName.MatchString(domain) {
		return "", fmt.Errorf("%q is not a domain name", domain)
	}
	rows, err := nimvorRows(ctx, t, `SELECT r.name, r.type, r.ttl, r.value, r.priority, r.weight, r.port FROM dns_records r JOIN dns_zones z ON z.id = r.zone_id WHERE z.name = `+Shq(domain)+` AND r.disabled = 0 ORDER BY r.id`)
	if err != nil {
		return "", fmt.Errorf("the DNS records of %s could not be read: %v", domain, err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("the zone file of %s was not found on the source", domain)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "$ORIGIN %s.\n$TTL 3600\n@ IN SOA ns1.%s. hostmaster.%s. ( 1 3600 900 1209600 3600 )\n", domain, domain, domain)
	for _, r := range rows {
		name, typ, ttl, val := cell(r, 0), strings.ToUpper(cell(r, 1)), cell(r, 2), cell(r, 3)
		if name == "" || typ == "" || val == "" || strings.ContainsAny(name+val, "\r\n") {
			continue
		}
		if ttl == "" || ttl == "0" {
			ttl = "3600"
		}
		switch typ {
		case "MX":
			val = cell(r, 4) + " " + val
		case "SRV":
			val = cell(r, 4) + " " + cell(r, 5) + " " + cell(r, 6) + " " + val
		case "TXT":
			val = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(val) + `"`
		}
		fmt.Fprintf(&b, "%s %s IN %s %s\n", name, ttl, typ, val)
	}
	return b.String(), nil
}

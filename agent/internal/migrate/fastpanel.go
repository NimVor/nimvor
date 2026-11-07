package migrate

import (
	"context"
	"sort"
	"strings"
)

// fastPanelAdapter reads a FASTPANEL server over SSH as root, through its own CLI (`mogwai`):
//
//   - sites: `mogwai sites list`, a table with SERVER_NAME, OWNER and DOCUMENT_ROOT columns (found by
//     their header names, whatever their order); a site's files live in /var/www/<owner>/data/www/<domain>
//   - databases: every MySQL/MariaDB schema of the server, listed under one pseudo-account named
//     "databases" because the CLI's database-to-site link is not read: you choose each destination
//   - mailboxes, forwarders, DNS, FTP and cron are not read.
//
// Written from the FASTPANEL documentation's description of the CLI and the table headers, with only
// synthetic data to test it: the exact output format may differ between versions. When the expected
// columns are missing the adapter says so instead of guessing. It stays Experimental.
type fastPanelAdapter struct{}

func (fastPanelAdapter) ID() string       { return "fastpanel" }
func (fastPanelAdapter) Name() string     { return "FASTPANEL" }
func (fastPanelAdapter) Support() Support { return Experimental }

func (fastPanelAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq("{ command -v mogwai >/dev/null 2>&1 || [ -x /usr/local/fastpanel2/fastpanel ]; } && echo fastpanel; true"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	if !strings.Contains(out, "fastpanel") {
		return Detection{}, false, nil
	}
	return Detection{Panel: "fastpanel", Name: "FASTPANEL", Version: "", Support: Experimental}, true, nil
}

func (fastPanelAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	progress("Listing the sites…")
	out, err := t.Exec(ctx, "sh -c "+Shq("mogwai sites list 2>&1"), nil)
	if err != nil {
		return nil, err
	}
	accts, warnings := parseFastPanelSites(out)
	d.Warnings = append(d.Warnings, warnings...)
	var dirs []string
	for _, a := range accts {
		for _, dom := range a.Domains {
			dirs = append(dirs, dom.DocRoot)
		}
	}
	progress("Measuring folders…")
	if sz, err := measure(ctx, t, uniq(dirs)); err == nil {
		for ai := range accts {
			for i := range accts[ai].Domains {
				if n, ok := sz[accts[ai].Domains[i].DocRoot]; ok {
					accts[ai].Domains[i].SizeKB = n
				}
			}
		}
	}
	for i := range accts {
		d.Accounts = append(d.Accounts, *accts[i])
	}
	progress("Listing the databases…")
	if s, err := t.Exec(ctx, "sh -c "+Shq(dbScript), nil); err == nil && strings.TrimSpace(s) != "" {
		dbs := parseSchemas(s, info.Engine)
		linked := 0
		if lo, err := t.Exec(ctx, "sh -c "+Shq("mogwai databases list 2>&1"), nil); err == nil {
			siteOf := parseFastPanelDatabases(lo)
			before := len(dbs)
			var ptrs []*Account
			for i := range d.Accounts {
				ptrs = append(ptrs, &d.Accounts[i])
			}
			dbs = placeDatabases(ptrs, dbs, siteOf)
			linked = before - len(dbs)
		}
		if len(dbs) > 0 {
			d.Accounts = append(d.Accounts, Account{ID: "databases", Name: "databases (not linked to a site)", Home: "/", Domains: []Domain{}, Databases: dbs, Email: []EmailAccount{}, Forwarders: []Forwarder{}, FTP: []FTPAccount{}, Cron: []CronJob{}})
		}
		if linked == 0 {
			d.Warnings = append(d.Warnings, "FASTPANEL's database-to-site link could not be read, so the databases are listed for the whole server: choose each destination yourself.")
		}
	} else {
		d.Warnings = append(d.Warnings, "The databases could not be listed (the login needs access to MariaDB/MySQL).")
	}
	d.Warnings = append(d.Warnings, "Mailboxes, forwarders, DNS, FTP and cron are not read from FASTPANEL.")
	return d, nil
}

// parseFastPanelSites reads the table of `mogwai sites list`. Columns are found by their header names;
// a table drawn with | or │ is split on those characters, a plain one by the header's column offsets.
func parseFastPanelSites(out string) ([]*Account, []string) {
	var warnings []string
	rows := parseCLITable(out, "SERVER_NAME", "DOCUMENT_ROOT")
	if len(rows) == 0 {
		return nil, []string{"The site list of FASTPANEL could not be read (expected a table with SERVER_NAME, OWNER and DOCUMENT_ROOT columns). The login must be root and `mogwai` must work for it."}
	}
	accts := map[string]*Account{}
	var order []string
	for _, r := range rows {
		name := strings.ToLower(r["SERVER_NAME"])
		root := r["DOCUMENT_ROOT"]
		owner := r["OWNER"]
		if name == "" && root == "" {
			continue
		}
		if !domainName.MatchString(name) || !ValidRemotePath(root) || !pleskLogin.MatchString(owner) {
			warnings = append(warnings, "A site of the list was skipped because its name, owner or folder is not plain: "+truncate(strings.Join([]string{name, owner, root}, " "), 80))
			continue
		}
		a := accts[owner]
		if a == nil {
			a = &Account{ID: owner, Name: owner, Home: "/var/www/" + owner, Domains: []Domain{}, Databases: []Database{}, Email: []EmailAccount{}, Forwarders: []Forwarder{}, FTP: []FTPAccount{}, Cron: []CronJob{}}
			accts[owner] = a
			order = append(order, owner)
		}
		a.Domains = append(a.Domains, Domain{Name: name, Kind: "site", DocRoot: root, SizeKB: -1})
	}
	sort.Strings(order)
	var out2 []*Account
	for _, o := range order {
		sort.SliceStable(accts[o].Domains, func(i, j int) bool { return accts[o].Domains[i].Name < accts[o].Domains[j].Name })
		out2 = append(out2, accts[o])
	}
	return out2, warnings
}

// parseCLITable turns a CLI table into rows keyed by upper-case header name. The header is the first
// line that holds every name in need; box-drawing rules and blank lines are ignored.
func parseCLITable(out string, need ...string) []map[string]string {
	lines := strings.Split(strings.ReplaceAll(out, "\r", ""), "\n")
	hi := -1
	for i, l := range lines {
		u := strings.ToUpper(l)
		all := true
		for _, n := range need {
			if !strings.Contains(u, n) {
				all = false
			}
		}
		if all {
			hi = i
			break
		}
	}
	if hi < 0 {
		return nil
	}
	header := lines[hi]
	delim := ""
	for _, c := range []string{"│", "|"} {
		if strings.Contains(header, c) {
			delim = c
			break
		}
	}
	var names []string
	var offs []int
	if delim != "" {
		for _, p := range strings.Split(header, delim) {
			names = append(names, strings.ToUpper(strings.TrimSpace(p)))
		}
	} else {
		inWord, start := false, 0
		for i, r := range header + " " {
			if r != ' ' && r != '\t' && !inWord {
				inWord, start = true, i
			} else if (r == ' ' || r == '\t') && inWord {
				inWord = false
				names = append(names, strings.ToUpper(header[start:i]))
				offs = append(offs, start)
			}
		}
	}
	var rows []map[string]string
	for _, l := range lines[hi+1:] {
		t := strings.TrimSpace(l)
		if t == "" || strings.Trim(t, "-+=─┼┬┴├┤┌┐└┘╞╪╡ |│") == "" {
			continue
		}
		row := map[string]string{}
		if delim != "" {
			parts := strings.Split(l, delim)
			for i, n := range names {
				if i < len(parts) && n != "" {
					row[n] = strings.TrimSpace(parts[i])
				}
			}
		} else {
			for i, n := range names {
				from := offs[i]
				to := len(l)
				if i+1 < len(offs) && offs[i+1] < len(l) {
					to = offs[i+1]
				}
				if from < len(l) {
					row[n] = strings.TrimSpace(l[from:to])
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

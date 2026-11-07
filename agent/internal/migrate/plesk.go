package migrate

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// pleskAdapter reads a Plesk (Linux) server over SSH as root. It asks Plesk's own database (psa)
// with read-only SELECTs through `plesk db`, which is Plesk's documented way in:
//
//   - subscriptions and their domains: domains, hosting, sys_users (www_root is the document root)
//   - subdomains: subdomains
//   - databases: data_bases (MySQL/MariaDB only)
//   - mailboxes: mail, in /var/qmail/mailnames/<domain>/<name>/Maildir
//
// Written from Plesk's documented schema and tested on synthetic data only: it stays Experimental
// until it has run on real servers (the psa schema changes between Plesk versions).
type pleskAdapter struct{}

func (pleskAdapter) ID() string       { return "plesk" }
func (pleskAdapter) Name() string     { return "Plesk (Linux)" }
func (pleskAdapter) Support() Support { return Experimental }

func (pleskAdapter) Detect(ctx context.Context, t Transport) (Detection, bool, error) {
	out, err := t.Exec(ctx, "sh -c "+Shq("cat /usr/local/psa/version 2>/dev/null; true"), nil)
	if err != nil {
		return Detection{}, false, err
	}
	m := regexp.MustCompile(`^(\d+(?:\.\d+)+)`).FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return Detection{}, false, nil
	}
	return Detection{Panel: "plesk", Name: "Plesk", Version: m[1], Support: Experimental}, true, nil
}

// The queries only read. Columns are tab separated; every name is validated before it is used.
const pleskScript = `echo '##dom'; plesk db -Ne "SELECT d.id, d.name, d.webspace_id, IFNULL(h.www_root,''), IFNULL(s.login,''), IFNULL(s.home,'') FROM domains d LEFT JOIN hosting h ON h.dom_id=d.id LEFT JOIN sys_users s ON s.id=h.sys_user_id WHERE d.htype='vrt_hst' ORDER BY d.id" 2>&1
echo '##sub'; plesk db -Ne "SELECT sd.name, d.name, d.id, IFNULL(sd.www_root,'') FROM subdomains sd JOIN domains d ON d.id=sd.dom_id ORDER BY sd.id" 2>&1
echo '##db'; plesk db -Ne "SELECT db.name, db.dom_id FROM data_bases db WHERE db.type='mysql'" 2>&1
echo '##mail'; plesk db -Ne "SELECT m.mail_name, d.name, d.id FROM mail m JOIN domains d ON d.id=m.dom_id" 2>&1
echo '##end'`

var (
	pleskLogin = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	pleskLocal = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	pleskLabel = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

func (pleskAdapter) Discover(ctx context.Context, t Transport, info ServerInfo, progress func(string)) (*Discovery, error) {
	d := &Discovery{}
	progress("Reading Plesk's subscriptions, domains, databases and mailboxes…")
	out, err := t.Exec(ctx, "sh -c "+Shq(pleskScript), nil)
	if err != nil {
		return nil, err
	}
	schemas := ""
	progress("Listing the database sizes…")
	if s, err := t.Exec(ctx, "sh -c "+Shq(dbScript), nil); err == nil {
		schemas = s
	}
	sizes := map[string]int64{}
	for _, db := range parseSchemas(schemas, info.Engine) {
		sizes[db.Name] = db.SizeKB
	}
	accounts, warnings := parsePlesk(out, sizes)
	d.Warnings = append(d.Warnings, warnings...)
	if len(accounts) == 0 {
		d.Warnings = append(d.Warnings, "No Plesk domains were found. The login must be root, and `plesk db` must work for it.")
	}
	var dirs []string
	for _, a := range accounts {
		for _, dom := range a.Domains {
			dirs = append(dirs, dom.DocRoot)
		}
		for _, m := range a.Email {
			dirs = append(dirs, m.Maildir)
		}
	}
	progress("Measuring folders…")
	if sz, err := measure(ctx, t, uniq(dirs)); err == nil {
		for ai := range accounts {
			for i := range accounts[ai].Domains {
				if n, ok := sz[accounts[ai].Domains[i].DocRoot]; ok {
					accounts[ai].Domains[i].SizeKB = n
				}
			}
			for i := range accounts[ai].Email {
				if n, ok := sz[accounts[ai].Email[i].Maildir]; ok {
					accounts[ai].Email[i].SizeKB = n
				}
			}
		}
	}
	for i := range accounts {
		d.Accounts = append(d.Accounts, *accounts[i])
	}
	d.Warnings = append(d.Warnings, "PHP versions per domain are not read from Plesk: check them in the compatibility step.")
	return d, nil
}

// parsePlesk turns the output of pleskScript into accounts. A Plesk subscription (a main domain and the
// domains added to it) is one account. Anything whose name or path is not plain is left out.
func parsePlesk(out string, dbSizes map[string]int64) ([]*Account, []string) {
	sec := sections(out)
	var warnings []string
	type dom struct {
		id, space   int
		name, root  string
		login, home string
	}
	byID := map[int]*dom{}
	var order []int
	for _, l := range sec["dom"] {
		f := strings.Split(l, "\t")
		if len(f) != 6 {
			if strings.TrimSpace(l) != "" {
				warnings = append(warnings, "A line of Plesk's domain list was not understood and was skipped: "+truncate(l, 80))
			}
			continue
		}
		id, e1 := strconv.Atoi(f[0])
		space, e2 := strconv.Atoi(f[2])
		if e1 != nil || e2 != nil || !domainName.MatchString(f[1]) {
			continue
		}
		byID[id] = &dom{id: id, space: space, name: strings.ToLower(f[1]), root: f[3], login: f[4], home: f[5]}
		order = append(order, id)
	}
	accts := map[int]*Account{}
	var acctOrder []int
	acctOf := func(id int) *Account {
		d := byID[id]
		if d == nil {
			return nil
		}
		root := id
		if d.space != 0 && byID[d.space] != nil {
			root = d.space
		}
		if a := accts[root]; a != nil {
			return a
		}
		m := byID[root]
		name := m.name
		home := "/var/www/vhosts/" + m.name
		if m.home != "" && ValidRemotePath(m.home) {
			home = m.home
		}
		a := &Account{ID: m.name, Name: name, Home: home, Domains: []Domain{}, Databases: []Database{}, Email: []EmailAccount{}, Forwarders: []Forwarder{}, FTP: []FTPAccount{}, Cron: []CronJob{}}
		accts[root] = a
		acctOrder = append(acctOrder, root)
		return a
	}
	for _, id := range order {
		d := byID[id]
		a := acctOf(id)
		if d.root == "" || !ValidRemotePath(d.root) {
			continue // no web hosting, or a path that cannot be used
		}
		kind := "addon"
		if d.space == 0 {
			kind = "main"
		}
		a.Domains = append(a.Domains, Domain{Name: d.name, Kind: kind, DocRoot: d.root, SizeKB: -1})
	}
	for _, l := range sec["sub"] {
		f := strings.Split(l, "\t")
		if len(f) != 4 {
			continue
		}
		pid, err := strconv.Atoi(f[2])
		if err != nil || !pleskLabel.MatchString(f[0]) || !domainName.MatchString(f[1]) || f[3] == "" || !ValidRemotePath(f[3]) {
			continue
		}
		if a := acctOf(pid); a != nil {
			a.Domains = append(a.Domains, Domain{Name: strings.ToLower(f[0] + "." + f[1]), Kind: "sub", DocRoot: f[3], SizeKB: -1})
		}
	}
	for _, l := range sec["db"] {
		f := strings.Split(l, "\t")
		if len(f) != 2 || !dbNameRe.MatchString(f[0]) {
			continue
		}
		id, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		if a := acctOf(id); a != nil {
			n, ok := dbSizes[f[0]]
			if !ok {
				n = -1
			}
			a.Databases = append(a.Databases, Database{Name: f[0], Engine: "mysql", SizeKB: n})
		}
	}
	for _, l := range sec["mail"] {
		f := strings.Split(l, "\t")
		if len(f) != 3 || !pleskLocal.MatchString(f[0]) || !domainName.MatchString(f[1]) {
			continue
		}
		id, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		if a := acctOf(id); a != nil {
			dn := strings.ToLower(f[1])
			a.Email = append(a.Email, EmailAccount{Address: strings.ToLower(f[0]) + "@" + dn, Maildir: "/var/qmail/mailnames/" + dn + "/" + f[0] + "/Maildir", SizeKB: -1})
		}
	}
	sort.Ints(acctOrder)
	var out2 []*Account
	for _, r := range acctOrder {
		a := accts[r]
		sort.SliceStable(a.Domains, func(i, j int) bool {
			if (a.Domains[i].Kind == "main") != (a.Domains[j].Kind == "main") {
				return a.Domains[i].Kind == "main"
			}
			return a.Domains[i].Name < a.Domains[j].Name
		})
		out2 = append(out2, a)
	}
	return out2, warnings
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

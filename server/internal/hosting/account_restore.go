package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// RestoreDeps are the services a restore drives, each already bound to the
// acting administrator (audit hooks).
type RestoreDeps struct {
	Accounts  *AccountService
	Websites  *WebsiteService
	Databases *DatabaseService
	Dns       *DnsService
	DnsRepo   *DnsRepository
	Cron      *CronService
	Backups   *BackupService
	Users     *identity.Repository
	Sites     *WebsiteRepository
}

type restoreLine struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// ProblemText renders why a step failed for a report.
func ProblemText(err error) string { return problem(err) }

// problem renders why a step failed for the report.
func problem(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		parts := make([]string, 0, len(ve.Fields))
		for _, m := range ve.Fields {
			parts = append(parts, m)
		}
		sort.Strings(parts)
		return strings.Join(parts, " ")
	}
	var ue *UserError
	if errors.As(err, &ue) {
		return ue.What
	}
	return wrapAgentErr(err).Error()
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func list(m map[string]any, k string) []map[string]any {
	var out []map[string]any
	l, _ := m[k].([]any)
	for _, it := range l {
		if x, ok := it.(map[string]any); ok {
			out = append(out, x)
		}
	}
	return out
}

// RecreateAccount rebuilds an imported account backup's account on this server
// and starts restoring each website's files and databases
// (AccountBackupService::recreate): the account, websites (main first), databases
// and their users, DNS zones and cron jobs, each reported on its own line.
// It returns the report, the restore jobs to track and the generated passwords
// (shown once, never stored). Step failures are reported; the restore is
// 'done' either way, as in the PHP panel.
func (s *AccountBackupService) RecreateAccount(ctx context.Context, restoreID int64, actor *identity.User, d RestoreDeps) (map[string]any, error) {
	rec, err := s.RestoreRecord(ctx, restoreID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, &UserError{What: "The restore no longer exists."}
	}
	if rec.Status != "imported" {
		if rec.Status == "done" {
			return nil, &UserError{What: "This account was already restored."}
		}
		return nil, &UserError{What: "The file is not imported yet."}
	}
	now := func() string { return time.Now().UTC().Format(time.RFC3339) }
	if _, err := s.db.ExecContext(ctx, `UPDATE account_restores SET status='restoring', updated_at=? WHERE id=?`, now(), restoreID); err != nil {
		return nil, err
	}
	meta, _ := rec.Manifest["meta"].(map[string]any)
	sitesInFile := map[string]string{}
	for _, x := range list(rec.Manifest, "sites") {
		sitesInFile[str(x, "domain")] = str(x, "id")
	}
	var report []restoreLine
	note := func(level, text string) { report = append(report, restoreLine{level, text}) }
	var secrets []map[string]any
	var jobs []map[string]any
	fail := func(text string, err error) (map[string]any, error) {
		report = append([]restoreLine{}, restoreLine{"error", text})
		s.finishRecord(ctx, restoreID, "failed", restoreAny(report))
		return nil, err
	}
	serverID := s.serverID
	u, _ := meta["user"].(map[string]any)
	username := str(u, "username")

	// 1. The account.
	var accountID int64
	var scope string
	exists := true
	if err := s.db.QueryRowContext(ctx, `SELECT u.id, r.scope FROM users u JOIN roles r ON r.id = u.role_id WHERE u.username = ?`, username).Scan(&accountID, &scope); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fail("The account could not be looked up.", err)
		}
		exists = false
	}
	if exists && scope != "own" {
		return fail("A staff user named "+username+" exists; the account was not restored.", &UserError{What: "A staff user named " + username + " exists."})
	}
	planName := str(u, "plan")
	var planID *int64
	if planName != "" {
		var id int64
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM plans WHERE name = ? AND owner_id IS NULL`, planName).Scan(&id); err == nil {
			planID = &id
		} else {
			note("warning", `The plan "`+planName+`" does not exist here: the account has no plan. Assign one under Users.`)
		}
	}
	if !exists {
		email := str(u, "email")
		taken := false
		if email != "" {
			taken, _ = d.Users.EmailExists(ctx, email, 0)
		}
		if email == "" || taken {
			placeholder := username + "@restored.invalid"
			why := "The account had no email address"
			if email != "" {
				why = "The email address " + email + " belongs to another user here"
			}
			note("warning", why+": the account has "+placeholder+" until you change it.")
			email = placeholder
		}
		in := map[string]string{
			"username": username, "email": email, "display_name": str(u, "display_name"), "generate": "1",
		}
		if str(u, "role") == "reseller" {
			in["reseller"] = "1"
		}
		if planID != nil {
			in["plan_id"] = strconv.FormatInt(*planID, 10)
		}
		if opts, ok := u["options"].(map[string]any); ok {
			for k, v := range opts {
				switch t := v.(type) {
				case bool:
					if t {
						in[k] = "1"
					}
				case string:
					in[k] = t
				}
			}
		}
		made, err := d.Accounts.Create(ctx, in, actor)
		if err != nil {
			return fail("The account could not be created: "+problem(err), err)
		}
		accountID = made.ID
		secrets = append(secrets, map[string]any{"what": "Panel password of the account", "name": username, "password": made.Password.String})
		msg := "Account " + username + " created"
		if planID != nil {
			msg += " with the plan " + planName
		}
		note("ok", msg+".")
	} else {
		note("ok", "Account "+username+" exists: its websites are restored into it.")
	}
	if parent := str(u, "parent"); parent != "" {
		var pid int64
		err := s.db.QueryRowContext(ctx, `SELECT u.id FROM users u JOIN roles r ON r.id = u.role_id WHERE u.username = ? AND r.name = 'reseller'`, parent).Scan(&pid)
		switch {
		case err == nil && !exists:
			if err := d.Users.SetOwnership(ctx, accountID, &pid, planID); err == nil {
				note("ok", "Placed under the reseller "+parent+".")
			}
		case err != nil:
			note("warning", "The reseller "+parent+" does not exist here: the account is managed by administrators.")
		}
	}
	account, err := d.Users.Find(ctx, accountID)
	if err != nil || account == nil {
		return fail("The account could not be read back.", errors.New("account missing"))
	}

	// 2. Websites: main websites first, then subdomains and addon domains.
	siteIDs := map[string]int64{}
	websites := list(meta, "websites")
	sort.SliceStable(websites, func(i, j int) bool {
		mi, mj := str(websites[i], "type") == "" || str(websites[i], "type") == "main", str(websites[j], "type") == "" || str(websites[j], "type") == "main"
		return mi && !mj
	})
	for _, w := range websites {
		domain := str(w, "domain")
		var id int64
		var owner sql.NullInt64
		err := s.db.QueryRowContext(ctx, `SELECT id, owner_id FROM websites WHERE server_id = ? AND domain = ?`, serverID, domain).Scan(&id, &owner)
		if err == nil {
			if !owner.Valid || owner.Int64 != accountID {
				note("error", domain+" exists here and belongs to another account: not restored.")
				continue
			}
			siteIDs[domain] = id
			note("ok", domain+" exists in the account: its files and databases are restored into it.")
			continue
		}
		typ := str(w, "type")
		var newID int64
		if parentID, ok := siteIDs[str(w, "parent")]; typ != "" && typ != "main" && ok {
			parent, perr := d.Sites.FindFull(ctx, serverID, parentID, nil)
			if perr != nil || parent == nil {
				note("error", "Website "+domain+" could not be created: its parent website is missing.")
				continue
			}
			in := map[string]string{}
			if typ == "subdomain" {
				in["subdomain"] = strings.TrimSuffix(domain, "."+parent.Domain)
			} else {
				in["domain"] = domain
				for _, a := range strList(w["aliases"]) {
					if a == "www."+domain {
						in["www"] = "1"
					}
				}
			}
			newID, _, err = d.Websites.CreateChild(ctx, parent, typ, in, account)
		} else {
			sysUser := str(w, "system_user")
			if sysUser == "" {
				sysUser = username
			}
			kind := str(w, "kind")
			if kind == "" {
				kind = "php"
			}
			in := map[string]string{
				"domain": domain, "kind": kind, "php_version": str(w, "php_version"), "proxy_pass": str(w, "proxy_pass"),
				"system_user": sysUser, "aliases": strings.Join(strList(w["aliases"]), " "),
			}
			if used, uerr := d.Sites.UsingSystemUser(ctx, serverID, sysUser); uerr == nil && len(used) == 0 {
				in["new_system_user"] = "1"
			}
			newID, err = d.Websites.Create(ctx, in, account, nil)
		}
		if err != nil {
			note("error", "Website "+domain+" could not be created: "+problem(err))
			continue
		}
		siteIDs[domain] = newID
		note("ok", "Website "+domain+" created.")
	}

	// 3. Databases and their users.
	dbIDs := map[string]int64{}
	for _, db := range list(meta, "databases") {
		name := str(db, "name")
		var id int64
		var owner sql.NullInt64
		if err := s.db.QueryRowContext(ctx, `SELECT id, owner_id FROM databases WHERE server_id = ? AND name = ?`, serverID, name).Scan(&id, &owner); err == nil {
			if owner.Valid && owner.Int64 == accountID {
				dbIDs[name] = id
				note("ok", "Database "+name+" exists in the account: its contents are restored into it.")
			} else {
				note("error", "Database "+name+" exists here and belongs to another account: not restored.")
			}
			continue
		}
		var websiteID *int64
		if sid, ok := siteIDs[str(db, "website")]; ok && str(db, "website") != "" {
			websiteID = &sid
		}
		id, err := d.Databases.CreateDatabase(ctx, serverID, actor, name, websiteID)
		if err != nil {
			note("error", "Database "+name+" could not be created: "+problem(err))
			continue
		}
		dbIDs[name] = id
		if websiteID == nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE databases SET owner_id = ? WHERE id = ?`, accountID, id)
		}
		note("ok", "Database "+name+" created.")
	}
	for _, du := range list(meta, "database_users") {
		name, host := str(du, "name"), str(du, "host")
		if host == "" {
			host = "localhost"
		}
		if taken, _ := d.Databases.db.DBUserTaken(ctx, serverID, name, host); taken {
			note("warning", "Database user "+name+" exists here: kept as it is.")
			continue
		}
		made, password, err := d.Databases.CreateDBUser(ctx, serverID, actor, name, host, true)
		if err != nil {
			note("error", "Database user "+name+" could not be created: "+problem(err))
			continue
		}
		var uid int64
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM database_users WHERE server_id = ? AND username = ? AND host = ?`, serverID, made, host).Scan(&uid); err != nil {
			note("error", "Database user "+name+" could not be found after creating it.")
			continue
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE database_users SET owner_id = ? WHERE id = ?`, accountID, uid)
		secrets = append(secrets, map[string]any{"what": "Database user (new password: update the website's configuration)", "name": made, "password": password})
		for _, g := range list(du, "grants") {
			dbID, ok := dbIDs[str(g, "database")]
			if !ok {
				continue
			}
			var priv *string
			if p := str(g, "privileges"); p != "" {
				priv = &p
			}
			if _, _, err := d.Databases.GrantDBAccess(ctx, serverID, actor, dbID, uid, priv); err != nil {
				note("error", "Database user "+made+" could not be given access to "+str(g, "database")+": "+problem(err))
			}
		}
		note("ok", "Database user "+made+" created with a new password.")
	}

	// 4. Restore each website backup (files and databases).
	_ = d.Backups.Refresh(ctx)
	for _, w := range websites {
		domain := str(w, "domain")
		siteID, ok := siteIDs[domain]
		backupID := sitesInFile[domain]
		if !ok || backupID == "" {
			continue
		}
		site, err := d.Sites.FindFull(ctx, serverID, siteID, nil)
		if err != nil || site == nil {
			continue
		}
		b, err := s.recordImported(ctx, d.Backups, site, backupID, actor)
		if err != nil {
			note("error", "The backup of "+domain+" could not be restored: "+problem(err))
			continue
		}
		var dbs []string
		for _, p := range b.Parts {
			if _, ok := dbIDs[p.Database]; p.Kind == "database" && ok {
				dbs = append(dbs, p.Database)
			}
		}
		job, err := d.Backups.RestoreSite(ctx, site, b, true, dbs, site.Domain)
		if err != nil {
			note("error", "The backup of "+domain+" could not be restored: "+problem(err))
			continue
		}
		jobs = append(jobs, map[string]any{"job": job, "domain": domain, "website_id": site.ID})
		extra := ""
		if len(dbs) > 0 {
			extra = " and " + strconv.Itoa(len(dbs)) + " database(s)"
		}
		note("ok", "Restoring the files"+extra+" of "+domain+".")
	}

	// 5. DNS zones (records not in the zone yet are added) and cron jobs.
	for _, z := range list(meta, "dns_zones") {
		name := str(z, "name")
		var zid int64
		var owner sql.NullInt64
		err := s.db.QueryRowContext(ctx, `SELECT id, owner_id FROM dns_zones WHERE server_id = ? AND name = ?`, serverID, name).Scan(&zid, &owner)
		if err == nil && (!owner.Valid || owner.Int64 != accountID) {
			note("error", "DNS zone "+name+" exists here and belongs to another account: not restored.")
			continue
		}
		if errors.Is(err, sql.ErrNoRows) {
			in := map[string]string{"name": name}
			if sid, ok := siteIDs[str(z, "website")]; ok && str(z, "website") != "" {
				in["website_id"] = strconv.FormatInt(sid, 10)
			}
			zid, err = d.Dns.Create(ctx, serverID, in, actor)
			if err != nil {
				note("error", "DNS zone "+name+": "+problem(err))
				continue
			}
			_, _ = s.db.ExecContext(ctx, `UPDATE dns_zones SET owner_id = ? WHERE id = ?`, accountID, zid)
		} else if err != nil {
			note("error", "DNS zone "+name+": "+problem(err))
			continue
		}
		if text := str(z, "zonefile"); strings.TrimSpace(text) != "" {
			zone, err := d.DnsRepo.Zone(ctx, serverID, zid, actor)
			if err != nil || zone == nil {
				note("error", "DNS zone "+name+": the zone could not be read back.")
				continue
			}
			res, err := d.Dns.Import(ctx, zone, text)
			if err != nil {
				note("error", "DNS zone "+name+": "+problem(err))
				continue
			}
			note("ok", "DNS zone "+name+": "+strconv.Itoa(res.Added)+" record(s) added.")
		}
	}
	cronAdded := 0
	for _, c := range list(meta, "cron_jobs") {
		domain := str(c, "website")
		siteID, ok := siteIDs[domain]
		if !ok {
			continue
		}
		site, err := d.Sites.FindFull(ctx, serverID, siteID, nil)
		if err != nil || site == nil {
			continue
		}
		var n int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cron_jobs WHERE website_id = ? AND schedule = ? AND command = ?`, siteID, str(c, "schedule"), str(c, "command")).Scan(&n)
		if n > 0 {
			continue
		}
		enabled := "0"
		if b, _ := c["enabled"].(bool); b {
			enabled = "1"
		}
		in := map[string]string{"schedule": str(c, "schedule"), "command": str(c, "command"), "description": str(c, "description"), "enabled": enabled}
		if err := d.Cron.Save(ctx, site, 0, in, account.Email); err != nil {
			note("error", `Cron job "`+str(c, "command")+`" on `+domain+": "+problem(err))
			continue
		}
		cronAdded++
	}
	if cronAdded > 0 {
		note("ok", strconv.Itoa(cronAdded)+" cron job(s) added.")
	}
	note("info", "Not in account backups: email (mailboxes and messages), SSL certificates (AutoSSL issues them again once the domains point here) and FTP accounts.")

	s.finishRecord(ctx, restoreID, "done", restoreAny(report))
	asAny := func(in []map[string]any) []any {
		out := make([]any, len(in))
		for i, m := range in {
			out[i] = m
		}
		return out
	}
	problems := 0
	for _, l := range report {
		if l.Level == "error" {
			problems++
		}
	}
	return map[string]any{"report": restoreAny(report), "jobs": asAny(jobs), "secrets": asAny(secrets), "account_id": accountID, "problems": problems, "websites": siteIDs}, nil
}

func restoreAny(lines []restoreLine) []any {
	out := make([]any, len(lines))
	for i, l := range lines {
		out[i] = map[string]any{"level": l.Level, "text": l.Text}
	}
	return out
}

func strList(v any) []string {
	var out []string
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// recordImported is the backups row of a website backup imported from an
// account backup (made when the agent has it but the panel does not yet).
func (s *AccountBackupService) recordImported(ctx context.Context, bs *BackupService, site *Website, backupID string, actor *identity.User) (*Backup, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM backups WHERE server_id = ? AND backup_id = ?`, s.serverID, backupID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		raw, err := s.agent.Call(ctx, "backup.inspect", map[string]any{"domain": site.Domain, "id": backupID}, 60*time.Second)
		if err != nil {
			return nil, wrapAgentErr(err)
		}
		m := map[string]any{}
		_ = json.Unmarshal(raw, &m)
		delete(m, "meta")
		created := str(m, "created_at")
		if created == "" {
			created = time.Now().UTC().Format(time.RFC3339)
		}
		label := str(m, "label")
		if label == "" {
			label = "Account backup"
		}
		if r := []rune(label); len(r) > 100 {
			label = string(r[:100])
		}
		size, _ := m["size"].(float64)
		blob, _ := json.Marshal(m)
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO backups (server_id, website_id, kind, domain, backup_id, label, manifest, size, encrypted, local, status, user_id, created_at, finished_at)
			 VALUES (?, ?, 'site', ?, ?, ?, ?, ?, 1, 1, 'completed', ?, ?, ?)`,
			s.serverID, site.ID, site.Domain, backupID, label, string(blob), int64(size), actor.ID, created, created)
		if err != nil {
			return nil, err
		}
		id, _ = res.LastInsertId()
	} else if err != nil {
		return nil, err
	} else {
		// A backup kept from before the account was terminated.
		_, _ = s.db.ExecContext(ctx, `UPDATE backups SET website_id = ? WHERE id = ? AND website_id IS NULL`, site.ID, id)
	}
	b, err := bs.FindSite(ctx, site.ID, id)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, &UserError{What: "The imported backup of " + site.Domain + " was not found."}
	}
	return b, nil
}

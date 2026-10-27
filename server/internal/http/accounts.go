package http

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// accountsCreate GET /accounts/new — new-account form page. Renders the
// real form (domain, username, contact, password, plan, reseller/www
// toggles) backed by hosting.AccountService::create.
// Mirrors AccountsController::create (accounts.create, users.manage).
type acctSwitch struct {
	Name, Label, Help string
	On                bool
	Raw               bool // Help contains markup
}

type acctRouting struct {
	Value, Label, Help string
	Checked            bool
}

func (s *Server) accountsCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sess := sessionFrom(ctx)
	user := userFrom(ctx)
	vals, _ := s.settings.Values(ctx)
	errs, old := flashJSON(sess, "errors"), flashJSON(sess, "old")
	sent := len(old) > 0
	on := func(k string) bool {
		if sent {
			return old[k] == "1" || strings.EqualFold(old[k], "on") || strings.EqualFold(old[k], "true")
		}
		return vals["account_"+k] == "1"
	}
	ip := strings.TrimSpace(vals["dns_default_ipv4"])
	spf := "v=spf1 +a +mx"
	if net.ParseIP(ip) != nil && strings.Count(ip, ".") == 3 {
		spf += " +ip4:" + ip
	}
	spf += " ~all"
	nameservers, _ := s.siteDnsService(r, user).Nameservers(ctx)
	versions := s.websiteService(r, user).PhpVersions(ctx)
	phpSel := old["php_version"]
	if !sent || phpSel == "" {
		phpSel = vals["default_php_version"]
		if phpSel == "" || !hasString(versions, phpSel) {
			phpSel = ""
			if len(versions) > 0 {
				phpSel = versions[len(versions)-1]
			}
		}
	}
	planSel := old["plan_id"]
	if !sent && vals["default_plan_id"] != "0" {
		planSel = vals["default_plan_id"]
	}
	routing := old["mail_routing"]
	if routing == "" {
		routing = vals["account_mail_routing"]
	}
	if routing == "" {
		routing = "auto"
	}
	var routings []acctRouting
	for _, o := range [][3]string{
		{"auto", "Automatically detect configuration (recommended)", "Local when the domain's MX record points here."},
		{"local", "Local mail exchanger", "This server receives the domain's mail."},
		{"backup", "Backup mail exchanger", "Another server receives the mail; nothing is stored here."},
		{"remote", "Remote mail exchanger", "Another server receives the mail; nothing is stored here (no local mailboxes)."},
	} {
		routings = append(routings, acctRouting{o[0], o[1], o[2], routing == o[0]})
	}
	sw := func(name, label, help string, raw bool) acctSwitch {
		return acctSwitch{Name: name, Label: label, Help: help, On: on(name), Raw: raw}
	}
	type planOpt struct {
		ID       int64
		Label    string
		Selected bool
	}
	var plans []planOpt
	if ps, err := hosting.NewPlanRepository(s.db).Visible(ctx, user); err == nil {
		for _, p := range ps {
			label := p.Name
			if p.Description != "" {
				label += " — " + p.Description
			}
			plans = append(plans, planOpt{p.ID, label, strconv.FormatInt(p.ID, 10) == planSel})
		}
	}
	type phpOpt struct {
		V        string
		Selected bool
	}
	var php []phpOpt
	for _, v := range versions {
		php = append(php, phpOpt{v, v == phpSel})
	}
	data := map[string]any{
		"CSRF": session.CSRFToken(sess), "User": user, "Title": "Create a new account", "Plans": plans, "PhpVersions": php,
		"Error": pullFlashKey(sess, "error"), "Success": pullFlashKey(sess, "success"), "CanReseller": hosting.IsAll(user),
		"Old": old, "Errors": errs, "SPF": spf, "Nameservers": nameservers, "Routings": routings,
		"Reseller": old["reseller"] == "1",
		"MailSwitches": []acctSwitch{sw("spam_filter", "Enable the spam filter (Rspamd)", "Scans incoming mail for the account's domains when the server's spam filter is on.", false),
			sw("spam_box", "Enable Spam Box", "Mail marked as spam goes to the mailbox's Junk folder instead of the inbox.", false)},
		"DNSSwitches": []acctSwitch{
			sw("dns_dkim", "Enable DKIM on this account", "Signs the domain's mail; needs email hosted here.", false),
			sw("dns_spf", "Enable SPF on this account", spf, false),
			sw("dns_dmarc", "Enable DMARC on this account", "v=DMARC1; p=none; — needs DKIM and SPF.", false),
			sw("dns_registrar_ns", "Use the name servers at the domain's registrar", "No DNS zone is created here (locally set name servers are ignored).", false),
			sw("dns_overwrite", "Overwrite an existing DNS zone of the account", "Replaces the records of a zone that exists for the domain.", false)},
	}
	s.render(w, "accounts_create", data)
}

// accountsStore POST /accounts — creates a new hosting account. Wires to
// hosting.AccountService::create which provisions the panel login, plan,
// hosting options, initial website (with www alias), DNS zone and SSL,
// rolling back the login if website creation fails.
// Mirrors AccountsController::store.
func (s *Server) accountsStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	actor := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	svc := s.accountService(r, actor)
	acct, err := svc.Create(r.Context(), postValues(r), actor)
	if err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.backWithErrors(w, r, "/accounts/new", ve.Fields)
			return
		}
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/accounts/new", http.StatusSeeOther)
		return
	}
	if acct.Password.Valid {
		sess.Flash("success", "Account "+acct.Username+" created. Generated password: "+acct.Password.String)
	} else {
		sess.Flash("success", "Account "+acct.Username+" created.")
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// accountService builds hosting.AccountService with per-request audit hooks
// (same pattern as websiteService/websiteAuditHook).
func (s *Server) accountService(r *http.Request, user *identity.User) *hosting.AccountService {
	users := identity.NewRepository(s.db)
	roles := authz.NewRoleRepository(s.db)
	plans := hosting.NewPlanRepository(s.db)
	limits := hosting.NewPlanLimits(s.db)
	usvc := hosting.NewUserService(users, roles, s.policy, plans, limits, s.accountAuditHook(r, user))
	ws := hosting.NewWebsiteService(
		hosting.NewWebsiteRepository(s.db), s.agent, s.features, limits,
		hosting.NewDnsRepository(s.db), s.settings, s.websiteAuditHook(r, user),
		s.currentServerID(r),
	)
	s.onWebsiteCreated(ws, r, user)
	return hosting.NewAccountService(usvc, users, ws, hosting.NewAccountOptionStore(s.db), s.db, s.accountAuditHook(r, user))
}

func (s *Server) accountAuditHook(r *http.Request, user *identity.User) hosting.Auditor {
	return func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		if targetType == "" {
			targetType = "user"
		}
		uid := user.ID
		s.audit.Record(ctx, audit.Entry{
			UserID:     &uid,
			Username:   user.Username,
			Action:     action,
			TargetType: targetType,
			TargetID:   target,
			Result:     result,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    details,
		})
	}
}

// accountsBandwidth GET /accounts/bandwidth — bandwidth usage report per
// account. Calls agent `accounts.bandwidth` and renders the result; if
// the agent action is unavailable it surfaces the error on the page so
// the operator can see what's missing. Mirrors
// AccountAdminController::bandwidth.
type bwRow struct {
	ID                                  int64
	Username, Role, Plan                string
	Used, Limit, Excess                 string
	Over, Suspended, Blocked            bool
	Confirm, UserSuspendConfirm, Reason string
}

func commaInt(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// accountsBandwidth GET /accounts/bandwidth — accounts with a monthly
// bandwidth limit and this month's use (AccountAdminController::bandwidth).
func (s *Server) accountsBandwidth(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	ctx := r.Context()
	all := r.URL.Query().Get("show") == "all"
	month := time.Now().UTC().Format("January 2006")
	data := map[string]any{
		"CSRF": session.CSRFToken(sess), "User": user, "Title": "Bandwidth", "All": all, "Month": month,
		"Success": "", "Error": "", "Admin": hosting.IsAll(user),
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	limits := hosting.NewPlanLimits(s.db)
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.username, r.name, p.name,
		(u.suspended_at IS NOT NULL OR EXISTS (SELECT 1 FROM users sp WHERE sp.id = u.parent_id AND sp.suspended_at IS NOT NULL)),
		COALESCE(u.bw_blocked_month = strftime('%Y-%m','now') OR EXISTS (SELECT 1 FROM users bp WHERE bp.id = u.parent_id AND bp.bw_blocked_month = strftime('%Y-%m','now')), 0)
		FROM users u JOIN roles r ON r.id = u.role_id JOIN plans p ON p.id = u.plan_id ORDER BY u.username`)
	type acct struct {
		id                 int64
		name, role, plan   string
		suspended, blocked bool
	}
	var accts []acct
	if err == nil {
		for rows.Next() {
			var a acct
			var su, bl int
			if rows.Scan(&a.id, &a.name, &a.role, &a.plan, &su, &bl) == nil {
				a.suspended, a.blocked = su == 1, bl == 1
				accts = append(accts, a)
			}
		}
		rows.Close()
	}
	type scored struct {
		row          bwRow
		excess, used int
	}
	var out []scored
	for _, a := range accts {
		id := a.id
		limit, _ := limits.Value(ctx, &id, "bandwidth_mb")
		if limit <= 0 {
			continue
		}
		used := limits.BandwidthUsedMB(ctx, limits.Owners(ctx, a.id))
		if !all && used <= limit {
			continue
		}
		excess := used - limit
		if excess < 0 {
			excess = 0
		}
		row := bwRow{ID: a.id, Username: a.name, Role: a.role, Plan: a.plan, Used: commaInt(used), Limit: commaInt(limit), Excess: "—",
			Over: excess > 0, Suspended: a.suspended, Blocked: a.blocked, Reason: "Bandwidth limit exceeded (" + month + ")"}
		if excess > 0 {
			row.Excess = commaInt(excess)
		}
		row.Confirm = "Reset " + a.name + "'s bandwidth for " + month + " to 0? The usage counted so far this month (" + commaInt(used) + " MB) is discarded; earlier months are not affected."
		if a.blocked {
			row.Confirm += " Its websites are unblocked at once."
		}
		row.UserSuspendConfirm = "Suspend " + a.name + "? Its websites go offline and email logins, databases, FTP/SFTP, cron jobs and workers stop until you unsuspend it. Nothing is deleted."
		out = append(out, scored{row, excess, used})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].excess != out[j].excess {
			return out[i].excess > out[j].excess
		}
		return out[i].used > out[j].used
	})
	var list []bwRow
	for _, o := range out {
		list = append(list, o.row)
	}
	data["Rows"] = list
	s.render(w, "accounts_bandwidth", data)
}

// accountsTerminate POST /accounts/terminate — starts termination of a
// hosting account. Administrators only; takes a single user_id (the PHP
// controller supports bulk checkboxes from the users list, but Go keeps
// it single-account for now to avoid building the confirm page and
// JobTracker queue). Mirrors AccountAdminController::confirmTerminate.
// terminateAccount removes the hosting account's websites, databases,
// DNS zones (website-free ones), mail domains and finally the user row.
// Mirrors TerminationService::one for the single-account, single-server
// case. On-server teardown of mail/databases is a best-effort follow-up;
// websites are fully removed through WebsiteService.Delete.
func (s *Server) terminateAccount(r *http.Request, actor *identity.User, target *identity.User) (string, error) {
	ctx := r.Context()
	ws := s.websiteService(r, actor)
	sitesRepo := hosting.NewWebsiteRepository(s.db)
	// Children first, parents last (child sites must be removed before their
	// parent addon domain so nginx -t always sees a consistent config).
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, server_id FROM websites WHERE owner_id = ? ORDER BY parent_id IS NULL, id`,
		target.ID)
	if err != nil {
		return "", err
	}
	type siteRow struct{ id, server int64 }
	var sites []siteRow
	for rows.Next() {
		var r siteRow
		if err := rows.Scan(&r.id, &r.server); err != nil {
			rows.Close()
			return "", err
		}
		sites = append(sites, r)
	}
	rows.Close()

	webCount := 0
	var warns []string
	for _, r := range sites {
		site, err := sitesRepo.FindFull(ctx, r.server, r.id, nil)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return "", fmt.Errorf("load site %d: %w", r.id, err)
		}
		ww, derr := ws.Delete(ctx, site, true, true)
		if derr != nil {
			return "", fmt.Errorf("delete website %s: %w", site.Domain, derr)
		}
		webCount++
		warns = append(warns, ww...)
	}
	// Mail, databases, database users and DNS zones go through the same code
	// paths as removing them one by one, so the server's copies (mailboxes, DKIM
	// keys, MySQL databases and logins, served zones) are removed too. A failure
	// stops the termination and keeps the account and what is left.
	ids := func(q string) ([]int64, error) {
		rows, err := s.db.QueryContext(ctx, q, target.ID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, rows.Err()
	}
	serverID := s.currentServerID(r)
	mailSvc, mailRepo := s.siteMailService(r, actor), hosting.NewMailRepository(s.db)
	mailIDs, err := ids(`SELECT id FROM mail_domains WHERE owner_id = ?`)
	if err != nil {
		return "", err
	}
	for _, id := range mailIDs {
		d, err := mailRepo.Domain(ctx, serverID, id, actor)
		if err != nil || d == nil {
			continue
		}
		aliases, _ := mailRepo.AliasList(ctx, d.ID)
		for _, a := range aliases {
			if err := mailSvc.DeleteAlias(ctx, d, a.ID); err != nil {
				return "", fmt.Errorf("remove the forwarder %s@%s: %w", a.Local, d.Domain, err)
			}
		}
		boxes, _ := mailRepo.MailboxList(ctx, d.ID)
		for _, m := range boxes {
			if err := mailSvc.DeleteMailbox(ctx, d, m.ID, m.Local+"@"+d.Domain); err != nil {
				return "", fmt.Errorf("remove the mailbox %s@%s: %w", m.Local, d.Domain, err)
			}
		}
		if err := mailSvc.RemoveDomain(ctx, d, d.Domain); err != nil {
			return "", fmt.Errorf("remove the email of %s: %w", d.Domain, err)
		}
	}
	dbSvc, dbRepo := s.databaseService(r, actor), hosting.NewDatabaseRepository(s.db)
	var dbCount int
	dbIDs, err := ids(`SELECT id FROM databases WHERE owner_id = ?`)
	if err != nil {
		return "", err
	}
	for _, id := range dbIDs {
		db, err := dbRepo.Find(ctx, serverID, id, actor)
		if err != nil || db == nil {
			continue
		}
		if _, err := dbSvc.DropDatabase(ctx, serverID, actor, id, db.Name); err != nil {
			return "", fmt.Errorf("drop the database %s: %w", db.Name, err)
		}
		dbCount++
	}
	userIDs, err := ids(`SELECT id FROM database_users WHERE owner_id = ?`)
	if err != nil {
		return "", err
	}
	for _, id := range userIDs {
		if err := dbSvc.DropDBUser(ctx, serverID, actor, id); err != nil {
			return "", fmt.Errorf("remove the database user: %w", err)
		}
	}
	// DNS zones not tied to a website (website zones went with their website).
	var zoneCount int
	zoneIDs, err := ids(`SELECT id FROM dns_zones WHERE owner_id = ?`)
	if err != nil {
		return "", err
	}
	dnsSvc, dnsRepo := s.siteDnsService(r, actor), hosting.NewDnsRepository(s.db)
	for _, id := range zoneIDs {
		zone, err := dnsRepo.Zone(ctx, serverID, id, actor)
		if err != nil || zone == nil {
			continue
		}
		if err := dnsSvc.Delete(ctx, zone, zone.Name); err != nil {
			return "", fmt.Errorf("remove the DNS zone %s: %w", zone.Name, err)
		}
		zoneCount++
	}
	// WebAuthn credentials, API tokens, limit overrides.
	_, _ = s.db.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE user_id = ?`, target.ID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE user_id = ?`, target.ID)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM limit_overrides WHERE user_id = ?`, target.ID)
	// Finally the user row.
	if err := identity.NewRepository(s.db).Delete(ctx, target.ID); err != nil {
		return "", err
	}
	msg := fmt.Sprintf("%s terminated: %d website(s), %d database(s), %d DNS zone(s) removed.", target.Username, webCount, dbCount, zoneCount)
	if len(warns) > 0 {
		msg += " Warnings: " + strings.Join(warns, " ")
	}
	return msg, nil
}

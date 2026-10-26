package http

// This file ports the WordPress toolkit's list and scan actions
// (WordPressController::index, scanAll, siteList, scanSite): the
// all-installations list (GET /wordpress, websites.view) with the
// outdated / issues filters, the scan-all (POST /wordpress/scan,
// websites.manage), the per-website list (GET /sites/{id}/wordpress —
// a single installation is opened directly) and the per-website scan
// (POST /sites/{id}/wordpress/scan — the website's folders, a website
// inside another's folder included). The agent reads files only.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

func (s *Server) wordpressService(r *http.Request, user *identity.User) *hosting.WordPressService {
	return hosting.NewWordPressService(s.db, s.agent, s.currentServerID(r), s.wordpressAudit(r, user))
}

// wordpressAudit binds the audit hook for the WordPress actions.
func (s *Server) wordpressAudit(r *http.Request, user *identity.User) hosting.Auditor {
	return func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		entry := audit.Entry{
			Action:     action,
			TargetType: targetType,
			TargetID:   target,
			Result:     result,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    details,
		}
		if user != nil {
			uid := user.ID
			entry.UserID = &uid
			entry.Username = user.Username
		}
		s.audit.Record(ctx, entry)
	}
}

// wpFlashRedirect flashes a message and redirects (the panel's
// redirectWithSuccess / redirectWithError).
func (s *Server) wpFlashRedirect(w http.ResponseWriter, r *http.Request, kind, msg, to string) {
	sessionFrom(r.Context()).Flash(kind, msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// wordpressIndex renders the all-installations list
// (WordPressController::index).
func (s *Server) wordpressIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	filter := r.URL.Query().Get("filter")
	if filter != "outdated" && filter != "issues" {
		filter = ""
	}
	svc := s.wordpressService(r, user)
	installs, err := svc.Installs(r.Context(), user, nil, filter)
	if err != nil {
		s.log.Error("wordpress index", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var all []hosting.WordPressInstall
	if filter != "" {
		all, err = svc.Installs(r.Context(), user, nil, "")
		if err != nil {
			s.log.Error("wordpress index", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	every := installs
	if filter != "" {
		every = all
	}
	outdated, issues := 0, 0
	for _, i := range every {
		if i.Outdated || i.PluginUpdates+i.ThemeUpdates > 0 {
			outdated++
		}
		if i.Issues > 0 {
			issues++
		}
	}
	sess := sessionFrom(r.Context())
	successMsg, _ := sess.PullFlash("success")
	errorMsg, _ := sess.PullFlash("error")
	jobMsg, _ := sess.PullFlash("job")
	s.render(w, "wordpress_index", map[string]any{
		"Success":       successMsg,
		"Error":         errorMsg,
		"Job":           jobMsg,
		"User":          user,
		"CSRF":          session.CSRFToken(sess),
		"Installs":      installs,
		"All":           all,
		"Filter":        filter,
		"AllCount":      len(every),
		"OutdatedCount": outdated,
		"IssuesCount":   issues,
		"CanManage":     s.can(r, "websites.manage"),
	})
}

// wordpressScanAll scans every website folder the user may see
// (WordPressController::scanAll).
func (s *Server) wordpressScanAll(w http.ResponseWriter, r *http.Request) {
	if !s.wpFormToken(w, r) {
		return
	}
	user := userFrom(r.Context())
	sites, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("wordpress scan", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	res, err := s.wordpressService(r, user).Scan(r.Context(), sites)
	if err != nil {
		s.wpScanError(w, r, err, "/wordpress")
		return
	}
	s.wpScanned(w, r, res, "/wordpress", "/wordpress")
}

// wpScanned is the scan's reply (WordPressController::scanned): a success
// flash, or the message with the not-scanned websites as an error.
func (s *Server) wpScanned(w http.ResponseWriter, r *http.Request, res *hosting.WordPressScan, to, errTo string) {
	msg := wpScanCount(res.Scanned) + " website folder(s) scanned: " + wpScanCount(res.Found) + " WordPress installation(s) found."
	if len(res.Errors) > 0 {
		n := len(res.Errors)
		if n > 5 {
			n = 5
		}
		s.wpFlashRedirect(w, r, "error", msg+" Not scanned: "+strings.Join(res.Errors[:n], "; "), errTo)
		return
	}
	s.wpFlashRedirect(w, r, "success", msg, to)
}

func wpScanCount(n int) string {
	return strconv.Itoa(n)
}

// wordpressSiteList renders a website's installations (WordPressController::
// siteList); a website with a single installation is opened directly.
func (s *Server) wordpressSiteList(w http.ResponseWriter, r *http.Request) {
	site := s.wpSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.wpFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	id := site.ID
	installs, err := s.wordpressService(r, user).Installs(r.Context(), user, &id, "")
	if err != nil {
		s.log.Error("wordpress site list", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(installs) == 1 && r.URL.Query().Get("list") == "" {
		http.Redirect(w, r, fmt.Sprintf("/sites/%d/wordpress/%d", id, installs[0].ID), http.StatusSeeOther)
		return
	}
	sess := sessionFrom(r.Context())
	successMsg, _ := sess.PullFlash("success")
	errorMsg, _ := sess.PullFlash("error")
	jobMsg, _ := sess.PullFlash("job")
	s.render(w, "wordpress_site", map[string]any{
		"Success":   successMsg,
		"Error":     errorMsg,
		"Job":       jobMsg,
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Site":      site,
		"Installs":  installs,
		"CanManage": s.can(r, "websites.manage"),
	})
}

// wordpressScanSite scans the website's folders — and the folders of any
// website inside them (WordPressController::scanSite).
func (s *Server) wordpressScanSite(w http.ResponseWriter, r *http.Request) {
	if !s.wpFormToken(w, r) {
		return
	}
	site := s.wpSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.wpFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	id := site.ID
	all, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("wordpress scan", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	base := strings.TrimRight(site.Root, "/") + "/"
	sites := []hosting.Website{}
	for _, st := range all {
		if st.ID == id || strings.HasPrefix(st.Root, base) {
			sites = append(sites, st)
		}
	}
	res, err := s.wordpressService(r, user).Scan(r.Context(), sites)
	if err != nil {
		s.wpScanError(w, r, err, fmt.Sprintf("/sites/%d/wordpress?list=1", id))
		return
	}
	s.wpScanned(w, r, res, fmt.Sprintf("/sites/%d/wordpress", id), fmt.Sprintf("/sites/%d/wordpress?list=1", id))
}

// wpFormToken checks the CSRF token (the panel's forms all carry it).
func (s *Server) wpFormToken(w http.ResponseWriter, r *http.Request) bool {
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return false
	}
	return true
}

// wpSiteOf resolves the website for a /sites/{id} route.
func (s *Server) wpSiteOf(r *http.Request, id string) *hosting.Website {
	user := userFrom(r.Context())
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), n, user)
	if err != nil || site == nil {
		return nil
	}
	return site
}

func (s *Server) wpFlashNotFound(w http.ResponseWriter, r *http.Request) {
	sessionFrom(r.Context()).Flash("error", "Website not found.")
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

// wpScanError flashes the scan failure (the agent's message when it
// answered, the generic one when it could not be reached) and redirects.
func (s *Server) wpScanError(w http.ResponseWriter, r *http.Request, err error, to string) {
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		s.wpFlashRedirect(w, r, "error", ue.What, to)
		return
	}
	var ae *agent.Error
	msg := "The agent could not be reached."
	if errors.As(err, &ae) && ae.Message != "" {
		msg = ae.Message
	}
	s.log.Error("wordpress scan", "error", err)
	s.wpFlashRedirect(w, r, "error", msg, to)
}

// ---------------------------------------------------------------------------
// The installation's overview and tools (WordPressController::show, check,
// login, logs).
// ---------------------------------------------------------------------------

var wpRestoreIDRe = regexp.MustCompile(`^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})Z$`)

// wpRestoreRow is one restore point, display-ready (its id is a UTC time).
type wpRestoreRow struct {
	ID       string
	When     string
	Reason   string
	Database bool
}

// wpVulnRow is one stored vulnerability, display-ready for the template.
type wpVulnRow struct {
	SevClass, SevLabel, Score, Title, Version, Name, Cve, FixedIn, Link string
	Unfixed                                                             bool
}

func (s *Server) wpTools(r *http.Request, user *identity.User) *hosting.WordPressTools {
	cron := hosting.NewCronService(s.db, s.agent, hosting.NewPlanLimits(s.db), s.wordpressAudit(r, user))
	return hosting.NewWordPressTools(s.db, s.agent, s.currentServerID(r), hosting.NewWebsiteRepository(s.db), s.crypto, cron, hosting.NewWpVulnerabilities(wpVulnsCacheDir(s.cfg.DBPath)), s.databaseService(r, user), s.wordpressAudit(r, user))
}

// wpInstallOf resolves the installation of a /sites/{id}/wordpress/{wp}
// route; a missing website or installation flashes and redirects.
func (s *Server) wpInstallOf(w http.ResponseWriter, r *http.Request) (*hosting.Website, *hosting.WordPressInstall, bool) {
	site := s.wpSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.wpFlashNotFound(w, r)
		return nil, nil, false
	}
	wid, err := strconv.ParseInt(r.PathValue("wp"), 10, 64)
	if err != nil {
		s.wpFlashNotFound(w, r)
		return nil, nil, false
	}
	install, err := s.wordpressService(r, userFrom(r.Context())).Install(r.Context(), site, wid)
	if err != nil {
		var ue *hosting.UserError
		msg := "That WordPress installation was not found; scan the website again."
		if errors.As(err, &ue) {
			msg = ue.What
		}
		s.wpFlashRedirect(w, r, "error", msg, fmt.Sprintf("/sites/%d/wordpress?list=1", site.ID))
		return nil, nil, false
	}
	return site, install, true
}

// wpShow renders the installation's overview (WordPressController::show).
func (s *Server) wpShow(w http.ResponseWriter, r *http.Request) {
	site, install, ok := s.wpInstallOf(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	user := userFrom(ctx)
	sess := sessionFrom(ctx)
	force, _ := sess.PullFlash("wp_force")

	var info *hosting.WordPressInfo
	var agentErr *agent.Error
	i, err := s.wordpressService(r, user).Info(ctx, site, install, force == "1")
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			agentErr = ae
		} else {
			s.log.Error("wordpress show", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else {
		info = i
		// The overview refreshed the stored summary.
		if fresh, err := s.wordpressService(r, user).Install(ctx, site, install.ID); err == nil {
			install = fresh
		}
	}
	// The agent could not read the website: the overview is not rendered.
	if agentErr != nil {
		where := install.Path
		if where == "" {
			where = "/"
		}
		version := install.Version
		if version == "" {
			version = "?"
		}
		successMsg, _ := sess.PullFlash("success")
		errorMsg, _ := sess.PullFlash("error")
		jobMsg, _ := sess.PullFlash("job")
		siteInstalls, _ := s.wordpressService(r, user).Installs(ctx, user, &site.ID, "")
		s.render(w, "wordpress_show_error", map[string]any{
			"User":       user,
			"CSRF":       session.CSRFToken(sess),
			"Site":       site,
			"Install":    install,
			"AgentError": agentErr,
			"Title":      "WordPress",
			"Version":    version,
			"Where":      where,
			"Others":     len(siteInstalls) > 1,
			"CheckURL":   fmt.Sprintf("/sites/%d/wordpress/%d/check", site.ID, install.ID),
			"ListURL":    fmt.Sprintf("/sites/%d/wordpress?list=1", site.ID),
			"Suspended":  site.AccountSuspended,
			"Success":    successMsg,
			"Error":      errorMsg,
			"Job":        jobMsg,
		})
		return
	}

	// The toolkit's extras: a refused one (the installation outside the
	// document root) is simply left out.
	tools := s.wpTools(r, user)
	var protection *hosting.WpProtection
	var security *hosting.WpSecurity
	restorePoints := []hosting.WpRestorePoint{}
	if p, err := tools.Protection(ctx, site); err != nil {
		var ue *hosting.UserError
		if !errors.As(err, &ue) {
			s.log.Error("wordpress show", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else {
		protection = p
	}
	if sec, err := tools.Security(ctx, site, install, "status", nil); err != nil {
		var ue *hosting.UserError
		if !errors.As(err, &ue) {
			s.log.Error("wordpress show", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else {
		security = sec
	}
	if rp, err := tools.RestorePoints(ctx, site, install); err != nil {
		var ue *hosting.UserError
		if !errors.As(err, &ue) {
			s.log.Error("wordpress show", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else {
		restorePoints = rp
	}
	copyTargets, err := tools.CopyTargets(ctx, site, user)
	if err != nil {
		s.log.Error("wordpress show", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	targetSite := map[int64]bool{}
	for _, t := range copyTargets {
		targetSite[t.ID] = true
	}
	syncTargets := []hosting.WordPressInstall{}
	siteInstalls, err := s.wordpressService(r, user).Installs(ctx, user, &site.ID, "")
	if err != nil {
		s.log.Error("wordpress show", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for _, i := range s.wpAllInstalls(r, user) {
		if i.ID != install.ID && targetSite[i.WebsiteID] {
			syncTargets = append(syncTargets, i)
		}
	}
	vulns := hosting.WpStoredVulnerabilitiesOf(install)
	var dbID *int64
	if install.DBName != "" {
		if id, ok := s.wordpressService(r, user).DatabaseID(ctx, user, install.DBName); ok {
			dbID = &id
		}
	}
	newDbPassword, _ := sess.PullFlash("wp_db_password")
	verify, hasVerify := sess.PullFlash("wp_verify")
	verifyList := []string{}
	if hasVerify && verify != "" {
		verifyList = strings.Split(verify, "\n")
	}
	canManage := s.can(r, "websites.manage")
	suspended := site.AccountSuspended
	// The installation's URL path, when web server rules can reach it.
	urlPath := ""
	hasURLPath := false
	if p, err := hosting.WpURLPath(site, install); err == nil {
		urlPath, hasURLPath = p, true
	}
	protected := (*hosting.WpPasswordProtect)(nil)
	hotlinkOn := false
	hotlinkAllowed := []string{}
	if protection != nil {
		hotlinkOn = protection.Hotlink != nil
		if allowed, ok := protection.Hotlink["allowed"].([]any); ok {
			for _, a := range allowed {
				if t, ok := a.(string); ok {
					hotlinkAllowed = append(hotlinkAllowed, t)
				}
			}
		}
		for i := range protection.Password {
			if hasURLPath && protection.Password[i].Path == urlPath {
				protected = &protection.Password[i]
			}
		}
	}
	where := install.Path
	if where == "" {
		where = "/"
	}
	fmStart := strings.Trim(install.Path, "/")
	// The overview's pieces, flattened for the template.
	version := install.Version
	title := "WordPress"
	var core hosting.WpCoreInfo
	var db hosting.WpDbInfo
	var mysql string
	var plugins []hosting.WpPlugin
	var themes []hosting.WpTheme
	var admins []hosting.WpAdmin
	consts := hosting.WpConstants{}
	checkedAt := ""
	issues := 0
	if info != nil {
		title = info.Title
		if title == "" {
			title = "WordPress"
		}
		version = info.Version
		core = info.Core
		db = info.DB
		mysql = info.MySQL
		plugins = info.Plugins
		themes = info.Themes
		admins = info.Admins
		consts = info.Constants
		if core.CheckedAt > 0 {
			checkedAt = time.Unix(core.CheckedAt, 0).UTC().Format("2006-01-02 15:04")
		}
		for _, c := range info.Checks {
			if c.Level != "info" {
				issues++
			}
		}
	}
	if version == "" {
		version = "?"
	}
	dbName := install.DBName
	dbTables, dbBytes := 0, int64(0)
	if info != nil {
		dbName = db.Name
		dbTables, dbBytes = db.Tables, db.Bytes
	}
	pUpd, tUpd := 0, 0
	for _, p := range plugins {
		if p.Update != "" {
			pUpd++
		}
	}
	for _, t := range themes {
		if t.Update != "" {
			tUpd++
		}
	}
	secApplied, secTotal := 0, 0
	if security != nil {
		secTotal = len(security.Measures)
		for _, m := range security.Measures {
			if m.Applied {
				secApplied++
			}
		}
	}
	restoreRows := []wpRestoreRow{}
	for _, p := range restorePoints {
		row := wpRestoreRow{ID: p.ID, Reason: p.Reason, Database: p.Database, When: p.ID}
		if m := wpRestoreIDRe.FindStringSubmatch(p.ID); m != nil {
			row.When = m[1] + "-" + m[2] + "-" + m[3] + " " + m[4] + ":" + m[5] + " UTC"
		}
		restoreRows = append(restoreRows, row)
	}
	vulnCheckedAt := vulns.CheckedAt
	if len(vulnCheckedAt) >= 16 {
		vulnCheckedAt = vulnCheckedAt[:16]
	}
	vulnRows := []wpVulnRow{}
	n := len(vulns.Items)
	if n > 50 {
		n = 50
	}
	for _, v := range vulns.Items[:n] {
		r := wpVulnRow{SevLabel: "Unrated", Title: v.Title, Version: v.Version, Name: v.Name, Cve: v.Cve,
			FixedIn: v.FixedIn, Unfixed: v.Unfixed, Link: v.Link, Score: v.Score}
		r.SevClass = "text-bg-light border"
		switch v.Severity {
		case "critical", "high":
			r.SevClass = "text-bg-danger"
		case "medium":
			r.SevClass = "text-bg-warning"
		case "low":
			r.SevClass = "text-bg-secondary"
		case "":
		default:
		}
		if v.Severity != "" {
			r.SevLabel = strings.ToUpper(v.Severity[:1]) + v.Severity[1:]
		}
		vulnRows = append(vulnRows, r)
	}
	adminLogins := []string{}
	for _, a := range admins {
		adminLogins = append(adminLogins, a.Login)
	}
	cronOn := install.CronJobID.Valid || consts.DisableCron
	successMsg, _ := sess.PullFlash("success")
	errorMsg, _ := sess.PullFlash("error")
	jobMsg, _ := sess.PullFlash("job")
	s.render(w, "wordpress_show", map[string]any{
		"Success":           successMsg,
		"Error":             errorMsg,
		"Job":               jobMsg,
		"Title":             title,
		"Version":           version,
		"SiteURL":           siteURLDisplay(info),
		"Core":              core,
		"DB":                db,
		"MySQL":             mysql,
		"DbName":            dbName,
		"DbTables":          dbTables,
		"DbBytes":           formatBytes(dbBytes),
		"Plugins":           plugins,
		"Themes":            themes,
		"Admins":            admins,
		"AdminLogins":       strings.Join(adminLogins, ", "),
		"Constants":         consts,
		"CheckedAtUTC":      checkedAt,
		"Issues":            issues,
		"PUpd":              pUpd,
		"TUpd":              tUpd,
		"SecApplied":        secApplied,
		"SecTotal":          secTotal,
		"VulnCheckedAt":     vulnCheckedAt,
		"VulnRows":          vulnRows,
		"VulnCount":         len(vulns.Items),
		"UpdatesTotal":      pUpd + tUpd,
		"HotlinkAllowedStr": strings.Join(hotlinkAllowed, ", "),
		"URLPathTrimmed":    strings.TrimRight(urlPath, "/"),
		"CronOn":            cronOn,
		"DebugOn":           consts.Debug,
		"SearchEnginesOn":   info != nil && info.SearchEngines,
		"MaintenanceOn":     info != nil && info.Maintenance,
		"Multisite":         info != nil && info.Multisite,
		"Outdated":          install.Outdated,
		"Latest":            install.Latest,
		"User":              user,
		"CSRF":              session.CSRFToken(sess),
		"Site":              site,
		"Install":           install,
		"Info":              info,
		"AgentError":        agentErr,
		"Protection":        protection,
		"Security":          security,
		"RestorePoints":     restoreRows,
		"CopyTargets":       copyTargets,
		"SyncTargets":       syncTargets,
		"Vulns":             vulns,
		"DbID":              dbID,
		"NewDbPassword":     newDbPassword,
		"Verify":            verifyList,
		"HasVerify":         hasVerify,
		"Others":            len(siteInstalls) > 1,
		"CanManage":         canManage,
		"Suspended":         suspended,
		"Can":               canManage && !suspended,
		"Manage":            fmt.Sprintf("/sites/%d/wordpress/%d/manage", site.ID, install.ID),
		"ToolURL":           fmt.Sprintf("/sites/%d/wordpress/%d/tools", site.ID, install.ID),
		"CheckURL":          fmt.Sprintf("/sites/%d/wordpress/%d/check", site.ID, install.ID),
		"LoginURL":          fmt.Sprintf("/sites/%d/wordpress/%d/login", site.ID, install.ID),
		"LogsURL":           fmt.Sprintf("/sites/%d/wordpress/%d/logs", site.ID, install.ID),
		"ListURL":           fmt.Sprintf("/sites/%d/wordpress?list=1", site.ID),
		"URLPath":           urlPath,
		"HasURLPath":        hasURLPath,
		"Protected":         protected,
		"HotlinkOn":         hotlinkOn,
		"HotlinkAllowed":    hotlinkAllowed,
		"Where":             where,
		"FMStart":           fmStart,
		"AutoNow":           wpAutoNow(info),
	})
}

// wpAllInstalls is the user's installations (the sync targets come from it).
func (s *Server) wpAllInstalls(r *http.Request, user *identity.User) []hosting.WordPressInstall {
	out, _ := s.wordpressService(r, user).Installs(r.Context(), user, nil, "")
	return out
}

// wpAutoNow is the automatic updates choice the overview's constants mean
// (the show view's $autoNow).
func wpAutoNow(info *hosting.WordPressInfo) string {
	if info == nil {
		return "minor"
	}
	return wpAutoNowConst(info.Constants)
}

// wpAutoNowConst maps the constants to the select's value.
func wpAutoNowConst(c hosting.WpConstants) string {
	if c.AutoCore == "'minor'" {
		return "minor"
	}
	if c.AutoCore == "true" {
		return "major"
	}
	if c.AutoCore == "false" || c.AutoDisabled {
		return "off"
	}
	return "minor"
}

// wpManage runs one operation on the installation (WordPressController::
// manage): the updates start as tracked jobs, the verify flashes its file
// list for the overview.
func (s *Server) wpManage(w http.ResponseWriter, r *http.Request) {
	if !s.wpFormToken(w, r) {
		return
	}
	site, install, ok := s.wpInstallOf(w, r)
	if !ok {
		return
	}
	to := fmt.Sprintf("/sites/%d/wordpress/%d", site.ID, install.ID)
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		s.wpFlashRedirect(w, r, "error", "The form could not be read.", to)
		return
	}
	items := r.PostForm["items[]"]
	if items == nil {
		if v := r.PostFormValue("items"); v != "" {
			items = []string{v}
		}
	}
	res, err := s.wordpressService(r, user).Manage(r.Context(), site, install, r.PostFormValue("op"), items, r.PostFormValue("value"))
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.wpFlashRedirect(w, r, "error", ue.What, to)
			return
		}
		s.log.Error("wordpress manage", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess := sessionFrom(r.Context())
	if res.Job != "" {
		if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), res.Job, res.JobKind, "website", fmt.Sprintf("%d", site.ID), &user.ID); err != nil {
			s.log.Error("track wordpress job", "job", res.Job, "error", err)
		}
		sess.Flash("job", res.Job)
	}
	if res.Files != nil {
		sess.Flash("wp_verify", strings.Join(res.Files, "\n"))
	}
	s.wpFlashRedirect(w, r, "success", res.Message, to)
}

// wpTool runs one of the WordPress toolkit's tools (WordPressController::tool):
// the debug switches, the cron takeover, the password and hotlink protection,
// the vulnerability protection, the vulnerability-database check, the
// administrator and database passwords, the security measures, and the copy
// tools (clone / sync / restore).
func (s *Server) wpTool(w http.ResponseWriter, r *http.Request) {
	if !s.wpFormToken(w, r) {
		return
	}
	site, install, ok := s.wpInstallOf(w, r)
	if !ok {
		return
	}
	to := fmt.Sprintf("/sites/%d/wordpress/%d", site.ID, install.ID)
	if err := r.ParseForm(); err != nil {
		s.wpFlashRedirect(w, r, "error", "The form could not be read.", to)
		return
	}
	tools := s.wpTools(r, userFrom(r.Context()))
	tool := r.PostFormValue("tool")
	on := r.PostFormValue("enabled") == "1"
	switch tool {
	case "debug":
		items := r.PostForm["debug[]"]
		if items == nil {
			if v := r.PostFormValue("debug"); v != "" {
				items = []string{v}
			}
		}
		msg, err := tools.SetDebug(r.Context(), site, install, items)
		s.wpToolFinish(w, r, msg, err, to)
	case "cron":
		msg, err := tools.SetCronTakeover(r.Context(), site, install, on)
		s.wpToolFinish(w, r, msg, err, to)
	case "vulnerability":
		msg, err := s.wpVulnerabilityTool(r, userFrom(r.Context()), site, install, on)
		s.wpToolFinish(w, r, msg, err, to)
	case "vuln_check":
		svc := s.wordpressService(r, userFrom(r.Context()))
		info, err := svc.Info(r.Context(), site, install, false)
		if err != nil {
			s.wpToolError(w, r, err, to)
			return
		}
		items, reachable, _, err := tools.CheckVulnerabilities(install, info)
		if err != nil {
			s.wpToolError(w, r, err, to)
			return
		}
		if !reachable && len(items) == 0 {
			s.wpFlashRedirect(w, r, "error", "The vulnerability database (wpvulnerability.net) could not be reached from this server; try again later.", to+"#security")
			return
		}
		msg := strconv.Itoa(len(items)) + " known vulnerabilit"
		if len(items) == 1 {
			msg += "y"
		} else {
			msg += "ies"
		}
		s.wpFlashRedirect(w, r, "success", msg+" found.", to+"#security")
		return
	case "clone", "sync", "restore":
		sess := sessionFrom(r.Context())
		user := userFrom(r.Context())
		var (
			job string
			err error
		)
		switch tool {
		case "clone":
			job, err = tools.CloneTo(r.Context(), site, install, map[string]string{
				"target_site": r.PostFormValue("target_site"),
				"target_path": r.PostFormValue("target_path"),
				"staging":     r.PostFormValue("staging"),
				"overwrite":   r.PostFormValue("overwrite"),
			}, user)
		case "sync":
			job, err = tools.Sync(r.Context(), site, install, map[string]string{
				"target":   r.PostFormValue("target"),
				"files":    r.PostFormValue("files"),
				"database": r.PostFormValue("database"),
			}, user)
		default:
			job, err = tools.Restore(r.Context(), site, install, r.PostFormValue("restore_point"), user)
		}
		if err != nil {
			s.wpToolError(w, r, err, to)
			return
		}
		sess.Flash("job", job)
		msg := "Cloning started; the copy appears in the WordPress list when it is done."
		switch tool {
		case "sync":
			msg = "Sync started. A restore point of the target is saved first."
		case "restore":
			msg = "Restoring started."
		}
		s.wpFlashRedirect(w, r, "success", msg, to+"#staging")
		return
	case "password_protection":
		msg, err := tools.SetPasswordProtection(r.Context(), site, install, on, r.PostFormValue("protect_user"), r.PostFormValue("protect_password"))
		s.wpToolFinish(w, r, msg, err, to)
	case "hotlink":
		allowed := []string{}
		for _, part := range strings.FieldsFunc(r.PostFormValue("hotlink_allowed"), func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
		}) {
			allowed = append(allowed, part)
		}
		msg, err := tools.SetHotlink(r.Context(), site, on, allowed)
		s.wpToolFinish(w, r, msg, err, to)
	case "admin_password":
		id, _ := strconv.Atoi(r.PostFormValue("admin_id"))
		msg, err := tools.AdminPassword(r.Context(), site, install, id, r.PostFormValue("admin_password"), r.PostFormValue("admin_password_confirm"))
		s.wpToolFinish(w, r, msg, err, to)
	case "db_password":
		password, err := tools.DBPassword(r.Context(), site, install)
		if err != nil {
			s.wpToolError(w, r, err, to)
			return
		}
		sess := sessionFrom(r.Context())
		sess.Flash("wp_db_password", password)
		s.wpFlashRedirect(w, r, "success", "The database password was changed in the database and in wp-config.php.", to+"#database")
		return
	case "security":
		op := "apply"
		if r.PostFormValue("op") == "rollback" {
			op = "rollback"
		}
		var measures []string
		if r.PostFormValue("recommended") == "1" {
			measures = hosting.WpRecommendedMeasures
		} else {
			measures = r.PostForm["measures[]"]
			if m := r.PostFormValue("measures"); m != "" {
				measures = append(measures, m)
			}
		}
		if len(measures) == 0 {
			s.wpFlashRedirect(w, r, "error", "Choose at least one measure.", to+"#security")
			return
		}
		if _, err := tools.Security(r.Context(), site, install, op, measures); err != nil {
			s.wpToolError(w, r, err, to)
			return
		}
		msg := "Security measures applied: " + strconv.Itoa(len(measures)) + " measure(s)."
		if op == "rollback" {
			msg = "Rolled back: " + strconv.Itoa(len(measures)) + " measure(s)."
		}
		s.wpFlashRedirect(w, r, "success", msg, to+"#security")
		return
	default:
		s.wpFlashRedirect(w, r, "error", "Unknown tool.", to)
		return
	}
}

// wpToolFinish flashes a tool's result (wpToolError on failure, the success
// message on success).
func (s *Server) wpToolFinish(w http.ResponseWriter, r *http.Request, msg string, err error, to string) {
	if err != nil {
		s.wpToolError(w, r, err, to)
		return
	}
	s.wpFlashRedirect(w, r, "success", msg, to)
}

// wpToolError maps a tool error onto PHP's redirects: the form and the
// user-facing errors as failures, the agent and the unknown errors as 500s.
func (s *Server) wpToolError(w http.ResponseWriter, r *http.Request, err error, to string) {
	var ve *hosting.ValidationError
	var ue *hosting.UserError
	var me *security.ModsecError
	switch {
	case errors.As(err, &ve):
		msg := "Please correct the form."
		if len(ve.Fields) != 0 {
			keys := make([]string, 0, len(ve.Fields))
			for k := range ve.Fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out := make([]string, 0, len(keys))
			for _, k := range keys {
				out = append(out, ve.Fields[k])
			}
			msg = strings.Join(out, " ")
		}
		s.wpFlashRedirect(w, r, "error", msg, to)
	case errors.As(err, &ue):
		msg := ""
		for _, p := range []string{ue.What, ue.Why, ue.Fix} {
			if p != "" {
				if msg != "" {
					msg += " "
				}
				msg += p
			}
		}
		s.wpFlashRedirect(w, r, "error", msg, to)
	case errors.As(err, &me):
		s.wpFlashRedirect(w, r, "error", me.Message, to)
	default:
		s.log.Error("wordpress tool", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// wpVulnsCacheDir is the per-item vulnerability answer cache (next to the
// panel database; none for the in-memory test database).
func wpVulnsCacheDir(dbPath string) string {
	if dbPath == ":memory:" {
		return ""
	}
	return filepath.Join(filepath.Dir(dbPath), "wpvulns")
}

// wpModsecConnState is the modsec status' connector state (the module's
// state when the connector reports none).
func wpModsecConnState(status map[string]any) string {
	if conn, ok := status["connector"].(map[string]any); ok {
		if st, ok := conn["state"].(string); ok && st != "" {
			return st
		}
	}
	if loaded, _ := status["module_loaded"].(bool); loaded {
		return "functional"
	}
	return "missing"
}

// wpVulnerabilityTool runs the ModSecurity part of the vulnerability
// protection (WordPressToolsService::setVulnerabilityProtection: the status
// guard and the site mode) and records the state through the service.
func (s *Server) wpVulnerabilityTool(r *http.Request, user *identity.User, site *hosting.Website, install *hosting.WordPressInstall, on bool) (string, error) {
	if site.AccountSuspended {
		return "", &hosting.UserError{What: "The hosting account is suspended; WordPress cannot be changed."}
	}
	ctx := r.Context()
	var held bool
	if on {
		st, ae := s.modsecStatus(ctx)
		if ae != nil {
			return "", ae
		}
		active, _ := st["active"].(bool)
		if wpModsecConnState(st) != "functional" || !active {
			remedy := "Ask your hosting provider to turn on ModSecurity."
			if hosting.IsAll(user) {
				remedy = "Install and turn it on under Security → ModSecurity (with the OWASP Core Rule Set), then turn this on again."
			}
			return "", &hosting.UserError{
				What: "Vulnerability protection needs ModSecurity, which is not installed or not turned on on this server.",
				Fix:  remedy,
			}
		}
		if err := s.modsecSetSite(ctx, r, site, "on", nil, nil, false, user); err != nil {
			return "", err
		}
		if st, ae := s.modsecStatus(ctx); ae == nil {
			if cfg, ok := st["config"].(map[string]any); ok {
				held, _ = cfg["detection_only"].(bool)
			}
		}
	} else {
		// Other installations of the website may still want it.
		var others int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM wordpress_installs WHERE website_id = ? AND id <> ? AND vuln_protection = 1`, site.ID, install.ID).Scan(&others); err != nil {
			return "", err
		}
		if others == 0 {
			if err := s.modsecSetSite(ctx, r, site, "detect", nil, nil, false, user); err != nil {
				return "", err
			}
		}
	}
	return s.wpTools(r, user).SetVulnerabilityProtection(ctx, site, install, on, held)
}

// wpCheck forces a fresh check of updates and opens the overview
// (WordPressController::check).
func (s *Server) wpCheck(w http.ResponseWriter, r *http.Request) {
	site, _, ok := s.wpInstallOf(w, r)
	if !ok {
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("wp_force", "1")
	http.Redirect(w, r, fmt.Sprintf("/sites/%d/wordpress/%s", site.ID, r.PathValue("wp")), http.StatusSeeOther)
}

// wpLogin opens WordPress signed in with a one-time link
// (WordPressController::login).
func (s *Server) wpLogin(w http.ResponseWriter, r *http.Request) {
	site, install, ok := s.wpInstallOf(w, r)
	if !ok {
		return
	}
	to := fmt.Sprintf("/sites/%d/wordpress/%d", site.ID, install.ID)
	if !site.HTTPS {
		s.wpFlashRedirect(w, r, "error", "One-click sign-in needs HTTPS on the website (the sign-in cookie would travel unencrypted). Issue a certificate under SSL / HTTPS, or sign in at /wp-admin/.", to)
		return
	}
	adminID, err := strconv.ParseInt(r.PostFormValue("admin_id"), 10, 64)
	if err != nil {
		s.wpFlashRedirect(w, r, "error", "Choose the administrator to sign in as.", to)
		return
	}
	url, err := s.wordpressService(r, userFrom(r.Context())).LoginURL(r.Context(), site, install, adminID)
	if err != nil {
		var ue *hosting.UserError
		msg := "The one-time sign-in link could not be created."
		if errors.As(err, &ue) {
			msg = ue.What
		}
		s.wpFlashRedirect(w, r, "error", msg, to)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

// wpLogs renders the end of the WordPress debug log
// (WordPressController::logs).
func (s *Server) wpLogs(w http.ResponseWriter, r *http.Request) {
	site, install, ok := s.wpInstallOf(w, r)
	if !ok {
		return
	}
	log, err := s.wpTools(r, userFrom(r.Context())).DebugLog(r.Context(), site, install)
	if err != nil {
		var ue *hosting.UserError
		msg := "The agent could not be reached."
		if errors.As(err, &ue) {
			msg = ue.What
		} else {
			var ae *agent.Error
			if errors.As(err, &ae) && ae.Message != "" {
				msg = ae.Message
			}
		}
		s.wpFlashRedirect(w, r, "error", msg, fmt.Sprintf("/sites/%d/wordpress/%d", site.ID, install.ID))
		return
	}
	sess := sessionFrom(r.Context())
	successMsg, _ := sess.PullFlash("success")
	errorMsg, _ := sess.PullFlash("error")
	s.render(w, "wordpress_logs", map[string]any{
		"User":    userFrom(r.Context()),
		"CSRF":    session.CSRFToken(sess),
		"Success": successMsg,
		"Error":   errorMsg,
		"Site":    site,
		"Install": install,
		"Log":     log,
		"LogSize": formatBytes(int64(log.Size)),
		"Where":   installPathOrSlash(install.Path),
		"ShowURL": fmt.Sprintf("/sites/%d/wordpress/%d", site.ID, install.ID),
		"SelfURL": fmt.Sprintf("/sites/%d/wordpress/%d/logs", site.ID, install.ID),
		"LogsURL": fmt.Sprintf("/sites/%d/logs", site.ID),
	})
}

func installPathOrSlash(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func siteURLDisplay(info *hosting.WordPressInfo) string {
	if info == nil {
		return ""
	}
	return info.SiteURL
}

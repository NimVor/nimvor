package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/mail"
	"github.com/mchkhawarshahzad/whacp/server/internal/notify"
)

// The scheduler is one pass of background work, run every few minutes by
// whacp-scheduler.timer as `whacp schedule:run` (bin/whacp schedule:run in the
// PHP panel). Each pass reports what it did, one line each. Passes ported so
// far are listed in schedulerPasses; the others of the PHP scheduler
// (monitoring samples and alerts, scheduled backups, antivirus, the nightly
// housekeeping) are not part of this panel yet.

// schedulerUser acts for work no administrator started.
var schedulerUser = &identity.User{ID: 0, Username: "scheduler", RoleScope: "all", RoleName: "admin"}

type schedulerPass struct {
	name string
	run  func(ctx context.Context, s *Server, r *http.Request) []string
}

var schedulerPasses = []schedulerPass{
	{"bandwidth", func(ctx context.Context, s *Server, r *http.Request) []string {
		return s.reconcileBandwidth(r.WithContext(ctx), schedulerUser)
	}},
	{"update check", func(ctx context.Context, s *Server, r *http.Request) []string {
		u := hosting.NewUpdateCheckService(s.agent, s.settings)
		if !u.Due(ctx) {
			return nil
		}
		if c := u.Check(ctx); c.Available {
			return []string{"a WhaCP update is available"}
		}
		return nil
	}},
	{"ModSecurity", func(ctx context.Context, s *Server, r *http.Request) []string {
		// Keep the audit log small.
		raw, err := s.agent.Call(ctx, "modsec.trim", map[string]any{}, 60*time.Second)
		if err != nil {
			if quietAgentError(err) {
				return nil
			}
			return []string{"ModSecurity audit log not checked: " + agentMessage(err)}
		}
		if n := int64(jnum(agentObject(raw), "removed_bytes")); n > 0 {
			return []string{"ModSecurity audit log trimmed by " + strconv.FormatInt(n, 10) + " bytes."}
		}
		return nil
	}},
	{"trash", func(ctx context.Context, s *Server, r *http.Request) []string {
		return s.trashCleanup(ctx, time.Now())
	}},
	{"shell access", func(ctx context.Context, s *Server, r *http.Request) []string {
		lines, err := s.shellReconcile(ctx)
		if err != nil && !quietAgentError(err) {
			lines = append(lines, "shell access not checked: "+agentMessage(err))
		}
		return lines
	}},
	{"hardening", func(ctx context.Context, s *Server, r *http.Request) []string {
		// Package updates on the RHEL family reset the compiler and traceroute
		// restrictions' modes: re-applied once an hour (first ten minutes).
		if time.Now().Minute() >= 10 {
			return nil
		}
		if _, err := s.agent.Call(ctx, "hardening.reapply", map[string]any{}, 120*time.Second); err != nil && !quietAgentError(err) {
			return []string{"hardening not reapplied: " + agentMessage(err)}
		}
		return nil
	}},
	{"ffmpeg", func(ctx context.Context, s *Server, r *http.Request) []string {
		lines, err := s.ffmpegService(r, schedulerUser).Reconcile(ctx, false)
		if err != nil && !quietAgentError(err) {
			lines = append(lines, "FFmpeg access not checked: "+agentMessage(err))
		}
		return lines
	}},
	{"disk quotas", func(ctx context.Context, s *Server, r *http.Request) []string {
		lines, err := s.reconcileQuotas(ctx)
		if err != nil && !quietAgentError(err) {
			lines = append(lines, "disk quotas not checked: "+agentMessage(err))
		}
		return lines
	}},
	{"brute force protection", func(ctx context.Context, s *Server, r *http.Request) []string {
		return s.hulkRun(ctx)
	}},
	{"malware scans", func(ctx context.Context, s *Server, r *http.Request) []string {
		return s.avRun(ctx, time.Now())
	}},
	{"resource limits", func(ctx context.Context, s *Server, r *http.Request) []string {
		lines, err := s.reconcileLimits(r, schedulerUser)
		if err != nil {
			return []string{"resource limits not checked: " + err.Error()}
		}
		return lines
	}},
	{"monitoring", func(ctx context.Context, s *Server, r *http.Request) []string {
		return s.monitoringRun(ctx)
	}},
	{"backups", func(ctx context.Context, s *Server, r *http.Request) []string {
		return s.backupScheduleRun(ctx, r)
	}},
	{"auto SSL", func(ctx context.Context, s *Server, r *http.Request) []string {
		return s.autoSslService(r, schedulerUser).Run(ctx)
	}},
	{"greylisting", func(ctx context.Context, s *Server, r *http.Request) []string {
		// Trusted providers' address ranges: asked in the first ten minutes
		// of every sixth hour; the agent reads them at most once a day and
		// only while greylisting is on.
		now := time.Now()
		if now.Hour()%6 != 0 || now.Minute() >= 10 {
			return nil
		}
		if _, err := s.agent.Call(ctx, "mail.greylist_update_providers", map[string]any{"all": false}, 300*time.Second); err != nil && !quietAgentError(err) {
			return []string{"greylisting providers not updated: " + agentMessage(err)}
		}
		return nil
	}},
	{"mail limits", func(ctx context.Context, s *Server, r *http.Request) []string {
		notes := notify.NewRepository(s.db)
		lines, err := hosting.RateAlerts(ctx, s.agent, hosting.NewWebsiteRepository(s.db), s.currentServerID(r), time.Now(),
			func(ctx context.Context, key string, websiteID, ownerID sql.NullInt64, title, body, link string) bool {
				id, _ := notes.StoreOwned(ctx, websiteID, ownerID, "mail.limit", "warning", title, body, link, key)
				return id > 0
			})
		if err != nil && !quietAgentError(err) {
			lines = append(lines, "mail sending limits not checked: "+agentMessage(err))
		}
		return lines
	}},
	{"jobs", func(ctx context.Context, s *Server, r *http.Request) []string {
		// Results of background jobs nobody watched.
		notes := notify.NewRepository(s.db)
		return hosting.NewJobTracker(s.db).SettlePending(ctx, s.agent, s.currentServerID(r), time.Now(),
			func(ctx context.Context, key string, websiteID, ownerID sql.NullInt64, severity, title, body, link string) {
				_, _ = notes.StoreOwned(ctx, websiteID, ownerID, "job", severity, title, body, link, key)
			})
	}},
	{"WordPress", func(ctx context.Context, s *Server, r *http.Request) []string {
		w := s.wordpressScheduler(r)
		harden, _ := s.settings.Bool(ctx, "wordpress_auto_harden", true)
		return append(w.AutoHarden(ctx, harden), w.DailyVulnerabilityCheck(ctx)...)
	}},
}

// wordpressScheduler builds the scheduled WordPress work (the vulnerability
// check and the automatic hardening) acting as the scheduler.
func (s *Server) wordpressScheduler(r *http.Request) *hosting.WordPressScheduler {
	w := hosting.NewWordPressScheduler(s.db, s.currentServerID(r), hosting.NewWebsiteRepository(s.db),
		s.wordpressService(r, schedulerUser), s.wpTools(r, schedulerUser))
	notes := notify.NewRepository(s.db)
	w.Notify = func(ctx context.Context, websiteID, ownerID sql.NullInt64, kind, severity, title, body, link, key string) {
		_, _ = notes.StoreOwned(ctx, websiteID, ownerID, kind, severity, title, body, link, key)
	}
	return w
}

// autoSslService builds the AutoSSL service bound to the actor.
func (s *Server) autoSslService(r *http.Request, user *identity.User) *hosting.AutoSslService {
	return hosting.NewAutoSslService(s.db, s.currentServerID(r), s.settings, s.agent,
		s.sslService(r, user), hosting.NewWebsiteRepository(s.db), s.siteDnsService(r, user),
		hosting.NewSiteOverview(s.db, s.agent, s.settings, s.databaseService(r, user)))
}

// RunScheduler runs one scheduler pass and records when it ran. Output lines
// are prefixed with the pass; a pass that panics is reported and the rest run.
func (s *Server) RunScheduler(ctx context.Context) []string {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return []string{"scheduler: " + err.Error()}
	}
	var out []string
	for _, p := range schedulerPasses {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					s.log.Error("scheduler pass panicked", "pass", p.name, "panic", rec)
					out = append(out, p.name+": failed unexpectedly (see the panel log)")
				}
			}()
			out = append(out, p.run(ctx, s, r)...)
		}()
	}
	// Doctor and the AutoSSL page read when it last ran.
	_ = s.settings.Set(ctx, hosting.SchedulerRunKey, time.Now().UTC().Format(time.RFC3339), 0)
	return out
}

// backupScheduleRun starts due scheduled backups and applies retention.
func (s *Server) backupScheduleRun(ctx context.Context, r *http.Request) []string {
	notes := notify.NewRepository(s.db)
	return s.backupSched(r, schedulerUser).Run(ctx, hosting.ScheduleRunner{
		Backups: s.backupsAdminService(r, schedulerUser),
		Sites:   hosting.NewWebsiteRepository(s.db),
		Jobs:    hosting.NewJobTracker(s.db),
		Notify: func(ctx context.Context, websiteID sql.NullInt64, severity, title, body, link, key string) {
			_, _ = notes.Store(ctx, websiteID, "job", severity, title, body, link, key)
		},
	})
}

// monitoringRun records a metrics sample and opens, refreshes and resolves
// alerts (email to the alert recipients, webhook and notifications).
func (s *Server) monitoringRun(ctx context.Context) []string {
	notes := notify.NewRepository(s.db)
	m := hosting.NewMonitoringService(s.db, 1, s.agent, s.settings, hosting.NewPlanLimits(s.db))
	m.Notify = func(ctx context.Context, websiteID, ownerID sql.NullInt64, kind, severity, title, link, key string) {
		_, _ = notes.StoreOwned(ctx, websiteID, ownerID, kind, severity, title, "", link, key)
	}
	mailer := mail.New(s.settings, s.crypto, s.log)
	m.Email = mailer.Send
	return m.Run(ctx)
}

// RunSetup runs the server setup for a profile from the command line
// (`whacp setup:apply`): each step in turn, jobs waited for, progress to out.
func (s *Server) RunSetup(ctx context.Context, profile string, opts hosting.SetupOptions, only []string, out func(string)) ([]hosting.SetupResult, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return nil, err
	}
	return s.serverSetup(r, schedulerUser).Run(ctx, profile, opts, only, out)
}

// SetupStatus is the last server setup run (nil when there was none).
func (s *Server) SetupStatus(ctx context.Context) *hosting.SetupState {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return nil
	}
	return s.serverSetup(r, schedulerUser).Last(ctx)
}

const (
	avNightFrom, avNightTo = 1, 6 // scheduled scans start between 01:00 and 06:00
	avPerPass              = 3
)

// avRun records the results of finished malware scans, then starts the
// scheduled scans of websites not scanned within av_scan_days
// (AvService::run). A subdomain inside its parent's folder is scanned with the
// parent.
func (s *Server) avRun(ctx context.Context, now time.Time) []string {
	sid := int64(1)
	var log []string
	if err := s.avRefresh(ctx, sid); err != nil {
		return []string{"Malware scans not checked: " + agentMessage(err)}
	}
	days, _ := s.settings.Int(ctx, "av_scan_days", 0)
	if hr := now.Hour(); days <= 0 || hr < avNightFrom || hr >= avNightTo {
		return log
	}
	cutoff := now.Add(-time.Duration(days)*24*time.Hour + time.Hour).UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT w.id, w.domain, w.system_user, w.root FROM websites w LEFT JOIN websites p ON p.id = w.parent_id
		 WHERE w.server_id = ? AND w.status = 'active'
		   AND (p.id IS NULL OR substr(w.root, 1, length(p.root) + 1) <> p.root || '/')
		   AND NOT EXISTS (SELECT 1 FROM av_scans s WHERE s.website_id = w.id AND (s.status = 'running' OR s.started_at > ?))
		 ORDER BY (SELECT MAX(s.id) FROM av_scans s WHERE s.website_id = w.id) IS NOT NULL, w.id LIMIT ?`, sid, cutoff, avPerPass)
	if err != nil {
		return append(log, "Malware scans not started: "+err.Error())
	}
	type site struct {
		id                 int64
		domain, user, root string
	}
	var due []site
	for rows.Next() {
		var st site
		if rows.Scan(&st.id, &st.domain, &st.user, &st.root) == nil {
			due = append(due, st)
		}
	}
	rows.Close()
	quarantine, _ := s.settings.Bool(ctx, "av_quarantine", false)
	for _, st := range due {
		res, err := s.agent.Call(ctx, "av.scan", map[string]any{"domain": st.domain, "user": st.user, "root": st.root, "quarantine": quarantine}, 60*time.Second)
		if err != nil {
			msg := agentMessage(err)
			if strings.Contains(msg, "not installed") {
				return log
			}
			log = append(log, "Malware scan of "+st.domain+" not started: "+msg)
			continue
		}
		var out struct {
			JobID string `json:"job_id"`
		}
		_ = json.Unmarshal(res, &out)
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO av_scans (server_id, website_id, job_id, source, user_id, status, quarantine, paths, started_at)
			 VALUES (?, ?, ?, 'scheduled', NULL, 'running', ?, '[]', ?)`,
			sid, st.id, out.JobID, quarantineToBool(quarantine), now.UTC().Format(time.RFC3339)); err != nil {
			log = append(log, "Malware scan of "+st.domain+" not recorded: "+err.Error())
			continue
		}
		log = append(log, "Malware scan of "+st.domain+" started.")
	}
	return log
}

// reconcileQuotas sets the disk quotas from the plans (DiskQuotaService::reconcile).
func (s *Server) reconcileQuotas(ctx context.Context) ([]string, error) {
	return hosting.NewDiskQuotaService(s.db, 1, s.agent, hosting.NewPlanLimits(s.db),
		func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
			s.audit.Record(ctx, audit.Entry{Username: "system", Action: action, TargetType: targetType, TargetID: target, Result: result, Details: details})
		}).Reconcile(ctx)
}

// quietAgentError: an agent without the action, or in read-only mode, is not
// worth reporting every five minutes.
func quietAgentError(err error) bool {
	m := err.Error()
	return strings.Contains(m, "unknown_action") || strings.Contains(m, "write_disabled")
}

// shellReconcile removes SSH shell access from accounts whose plan no longer
// includes it (ShellService::reconcile).
func (s *Server) shellReconcile(ctx context.Context) ([]string, error) {
	raw, err := s.agent.Call(ctx, "shell.status", map[string]any{}, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var st struct {
		Accounts []struct {
			User string `json:"user"`
			Mode string `json:"mode"`
		} `json:"accounts"`
	}
	_ = json.Unmarshal(raw, &st)
	users := identity.NewRepository(s.db)
	var revoke []string
	for _, a := range st.Accounts {
		if a.Mode == "" || a.Mode == "disabled" {
			continue
		}
		var owner sql.NullInt64
		_ = s.db.QueryRowContext(ctx, `SELECT owner_id FROM websites WHERE server_id = 1 AND system_user = ? AND owner_id IS NOT NULL LIMIT 1`, a.User).Scan(&owner)
		if !owner.Valid {
			continue
		}
		account, err := users.Find(ctx, owner.Int64)
		if err != nil || account == nil {
			continue
		}
		if off, _ := s.features.DisabledFor(ctx, account); hasString(off, "ssh") {
			revoke = append(revoke, a.User)
		}
	}
	if len(revoke) == 0 {
		return nil, nil
	}
	if _, err := s.agent.Call(ctx, "shell.set", map[string]any{"users": revoke, "mode": "disabled"}, 120*time.Second); err != nil {
		return nil, err
	}
	s.audit.Record(ctx, audit.Entry{Username: "system", Action: "shell.set", TargetType: "server", TargetID: "1", Result: audit.Success,
		Details: map[string]any{"mode": "disabled", "users": revoke, "reason": "plan without SSH"}})
	return []string{"Shell access removed (plan without SSH): " + strings.Join(revoke, ", ") + "."}, nil
}

// trashCleanup empties File Manager trash items older than trash_retention_days,
// once a day (the first passes after 03:00).
func (s *Server) trashCleanup(ctx context.Context, now time.Time) []string {
	keep, _ := s.settings.Int(ctx, "trash_retention_days", 0)
	if v, err := s.settings.Values(ctx); err == nil {
		keep, _ = strconv.Atoi(strings.TrimSpace(v["trash_retention_days"]))
	}
	if keep <= 0 || now.Hour() != 3 || now.Minute() >= 10 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT domain FROM websites WHERE server_id = 1`)
	if err != nil {
		return nil
	}
	var domains []string
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			domains = append(domains, d)
		}
	}
	rows.Close()
	var log []string
	for _, d := range domains {
		raw, err := s.agent.Call(ctx, "files.trash_empty", map[string]any{"domain": d, "older_than_days": keep}, 300*time.Second)
		if err != nil {
			if m := err.Error(); !strings.Contains(m, "unknown_action") && !strings.Contains(m, "write_disabled") && !strings.Contains(m, "not_found") {
				log = append(log, d+": trash not emptied: "+agentMessage(err))
			}
			continue
		}
		if n := int(jnum(agentObject(raw), "deleted")); n > 0 {
			log = append(log, fmt.Sprintf("%s: %d item(s) older than %d days deleted from the trash", d, n, keep))
		}
	}
	return log
}

// reconcileBandwidth enforces the plans' monthly bandwidth: marks accounts over
// it, lifts the mark when they are not, and re-applies the websites that differ
// (BandwidthLimitService::reconcile).
func (s *Server) reconcileBandwidth(r *http.Request, user *identity.User) []string {
	notes := notify.NewRepository(s.db)
	svc := hosting.NewBandwidthLimitService(s.db, s.currentServerID(r), hosting.NewPlanLimits(s.db), hosting.NewWebsiteRepository(s.db),
		s.websiteService(r, user), s.websiteAuditHook(r, user))
	svc.Notify = func(ctx context.Context, owner int64, title, body, key string) {
		_, _ = notes.StoreOwned(ctx, sql.NullInt64{}, sql.NullInt64{Int64: owner, Valid: true}, "bandwidth", "warning", title, body, "", key)
	}
	return svc.Reconcile(r.Context())
}

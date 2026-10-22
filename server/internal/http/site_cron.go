package http

// This file ports the per-website cron area (CronController + the write side
// of CronService): adding/editing/pausing/deleting scheduled jobs, running
// one now, and the job's log. The panel stores the jobs; after every change
// the complete schedule is pushed to the agent (cron.apply) and restored if
// the apply fails. Routes:
//   GET  /sites/{id}/cron                (cron.view)
//   POST /sites/{id}/cron                (cron.manage)
//   GET  /sites/{id}/cron/{job}/log      (cron.view)
//   POST /sites/{id}/cron/{job}/run      (cron.manage)
//   POST /sites/{id}/cron/{job}/toggle   (cron.manage)
//   POST /sites/{id}/cron/{job}/delete   (cron.manage)

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// cronJobView is a cron job with the preset's human label for the page.
type cronJobView struct {
	hosting.CronJob
	Preset string
}

func cronJobViewOf(j hosting.CronJob) cronJobView {
	return cronJobView{CronJob: j, Preset: hosting.PresetLabel(j.Schedule)}
}

func cronJobViews(jobs []hosting.CronJob) []cronJobView {
	out := make([]cronJobView, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, cronJobViewOf(j))
	}
	return out
}

// cronSiteOf resolves the website (nil = not found / no access). FindFull:
// the page needs the root and the system user.
func (s *Server) cronSiteOf(r *http.Request, id string) *hosting.Website {
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

func (s *Server) cronService(r *http.Request, user *identity.User) *hosting.CronService {
	return hosting.NewCronService(s.db, s.agent, hosting.NewPlanLimits(s.db), s.websiteAuditHook(r, user))
}

func (s *Server) cronRepo() *hosting.CronRepository { return hosting.NewCronRepository(s.db) }

// cronFlashErr redirects back with the error flash (the PHP's
// redirectWithFailure).
func (s *Server) cronFlashErr(w http.ResponseWriter, r *http.Request, err error) {
	sess := sessionFrom(r.Context())
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		sess.Flash("error", ue.What)
	} else {
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Message != "" {
			sess.Flash("error", ae.Message)
		} else {
			s.log.Error("cron action", "error", err)
			sess.Flash("error", "The operation failed; the cron jobs were left as they were.")
		}
	}
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/cron", http.StatusSeeOther)
}

func (s *Server) cronFlashNotFound(w http.ResponseWriter, r *http.Request) {
	sessionFrom(r.Context()).Flash("error", "Website not found.")
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

func (s *Server) cronRedirectWith(w http.ResponseWriter, r *http.Request, msg string) {
	sessionFrom(r.Context()).Flash("success", msg)
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/cron", http.StatusSeeOther)
}

// cronPageData renders the cron page (the PHP's sites/cron view variables).
// form/fieldErrs re-render a rejected form with its input; editID opens the
// edit form pre-filled for that job (the PHP's ?edit=).
func (s *Server) cronPageData(w http.ResponseWriter, r *http.Request, user *identity.User, site *hosting.Website, editID int64, form map[string]string, fieldErrs map[string]string) {
	sess := sessionFrom(r.Context())
	jobs, err := s.cronRepo().Jobs(r.Context(), site.ID)
	if err != nil {
		s.log.Error("list cron jobs", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var edit *cronJobView
	if editID > 0 {
		j, err := s.cronService(r, user).Find(r.Context(), site, editID)
		if err != nil {
			s.log.Error("find cron job", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if j != nil {
			v := cronJobViewOf(*j)
			edit = &v
		}
	}
	// The effective form: a rejected submission's input, else the job being
	// edited, else the defaults (the PHP's $form = $old ?: $edit ?: []).
	if form == nil {
		form = map[string]string{"enabled": "1"}
		if edit != nil {
			sched := edit.Schedule
			isPreset := hosting.PresetLabel(sched) != ""
			custom := ""
			if !isPreset {
				custom = sched
			}
			enabled, mail := "0", "0"
			if edit.Enabled {
				enabled = "1"
			}
			if edit.Email != "" {
				mail = "1"
			}
			form = map[string]string{
				"schedule":        sched,
				"schedule_custom": custom,
				"command":         edit.Command,
				"description":     edit.Description,
				"enabled":         enabled,
				"email_output":    mail,
			}
		}
	}
	sched := form["schedule"]
	if sched == "" {
		sched = "*/5 * * * *"
	}
	isPreset := hosting.PresetLabel(sched) != ""
	schedCustom := ""
	if !isPreset {
		schedCustom = sched
	}
	mailTo := user.Email
	if edit != nil && edit.Email != "" {
		mailTo = edit.Email
	}
	data := map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sessionFrom(r.Context())),
		"Site":        site,
		"Jobs":        cronJobViews(jobs),
		"Edit":        edit,
		"Presets":     hosting.PresetSchedules,
		"CanManage":   s.can(r, "cron.manage"),
		"Form":        form,
		"Errors":      fieldErrs,
		"Sched":       sched,
		"SchedCustom": schedCustom,
		"IsPreset":    isPreset,
		"MailTo":      mailTo,
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	data["Job"], _ = sess.PullFlash("job")
	s.render(w, "site_cron", data)
}

// cronFormInput collects the cron form's fields (the PHP's $request->all()).
func cronFormInput(r *http.Request) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"schedule", "schedule_custom", "command", "description", "enabled", "email_output"} {
		out[k] = r.FormValue(k)
	}
	return out
}

// cronJobIDOf parses the {job} path value (-1 = not a number).
func cronJobIDOf(r *http.Request) int64 {
	n, err := strconv.ParseInt(r.PathValue("job"), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// siteCronStore adds or updates a job (sites.cron.store — cron.manage).
// A rejected form re-renders the page with the field errors and input
// (the PHP's backWithErrors).
func (s *Server) siteCronStore(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.cronFlashErr(w, r, errors.New("bad request"))
		return
	}
	site := s.cronSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cronFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	var jobID int64
	if v, err := strconv.ParseInt(r.FormValue("job_id"), 10, 64); err == nil && v > 0 {
		jobID = v
	}
	svc := s.cronService(r, user)
	input := cronFormInput(r)
	if err := svc.Save(r.Context(), site, jobID, input, user.Email); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.cronPageData(w, r, user, site, jobID, input, ve.Fields)
			return
		}
		s.cronFlashErr(w, r, err)
		return
	}
	if jobID > 0 {
		s.cronRedirectWith(w, r, "Cron job updated.")
		return
	}
	s.cronRedirectWith(w, r, "Cron job added. It runs as "+site.SystemUser+".")
}

// siteCronToggle pauses or resumes a job (sites.cron.toggle — cron.manage).
func (s *Server) siteCronToggle(w http.ResponseWriter, r *http.Request) {
	site := s.cronSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cronFlashNotFound(w, r)
		return
	}
	jobID := cronJobIDOf(r)
	if jobID < 0 {
		s.cronFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	svc := s.cronService(r, user)
	job, err := svc.Find(r.Context(), site, jobID)
	if err != nil || job == nil {
		s.cronFlashErr(w, r, &hosting.UserError{What: "Cron job not found."})
		return
	}
	if err := svc.SetEnabled(r.Context(), site, jobID, !job.Enabled); err != nil {
		s.cronFlashErr(w, r, err)
		return
	}
	if job.Enabled {
		s.cronRedirectWith(w, r, "Cron job paused.")
		return
	}
	s.cronRedirectWith(w, r, "Cron job enabled.")
}

// siteCronDelete removes a job (sites.cron.delete — cron.manage).
func (s *Server) siteCronDelete(w http.ResponseWriter, r *http.Request) {
	site := s.cronSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cronFlashNotFound(w, r)
		return
	}
	jobID := cronJobIDOf(r)
	if jobID < 0 {
		s.cronFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	svc := s.cronService(r, user)
	if job, err := svc.Find(r.Context(), site, jobID); err != nil || job == nil {
		s.cronFlashErr(w, r, &hosting.UserError{What: "Cron job not found."})
		return
	}
	if err := svc.Delete(r.Context(), site, jobID); err != nil {
		s.cronFlashErr(w, r, err)
		return
	}
	s.cronRedirectWith(w, r, "Cron job deleted.")
}

// siteCronRun runs a job now (sites.cron.run — cron.manage). The run is an
// agent job: the tracker keeps it and the page flashes it.
func (s *Server) siteCronRun(w http.ResponseWriter, r *http.Request) {
	site := s.cronSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cronFlashNotFound(w, r)
		return
	}
	jobID := cronJobIDOf(r)
	if jobID < 0 {
		s.cronFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	svc := s.cronService(r, user)
	job, err := svc.Find(r.Context(), site, jobID)
	if err != nil || job == nil {
		s.cronFlashErr(w, r, &hosting.UserError{What: "Cron job not found."})
		return
	}
	agentJob, err := svc.Run(r.Context(), site, job)
	if err != nil {
		s.cronFlashErr(w, r, err)
		return
	}
	kind := "Cron: " + job.Command
	if rs := []rune(kind); len(rs) > 60 {
		kind = string(rs[:60])
	}
	if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), agentJob, kind, "website", fmt.Sprintf("%d", site.ID), &user.ID); err != nil {
		s.log.Error("track cron job", "job", agentJob, "error", err)
	}
	sessionFrom(r.Context()).Flash("job", agentJob)
	s.cronRedirectWith(w, r, "Command started.")
}

// cronRunView is one run row of the log page with its started-at, duration,
// result, trigger and output summary done (the PHP's $when / $duration
// closures, the result badge and the output collapsing).
type cronRunView struct {
	hosting.CronRun
	Started     string // "Y-m-d H:i:s UTC" ("" = earlier output)
	Duration    string
	Result      string // "—" | Running or stopped | Success | Failed (exit N)
	ResultClass string // "" | is-warn | is-ok | is-error
	Trigger     string // "" | Manual | Scheduled
	FirstLine   string // the output's first line, for the summary
	LineCount   int
	SingleLine  bool // one line within 120 chars: shown as <code>
}

var (
	cronRunTimeParse = time.Parse
	cronRunTimeFmt   = "2006-01-02 15:04:05"
)

func formatCronTime(ts string) string {
	t, err := cronRunTimeParse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format(cronRunTimeFmt) + " UTC"
}

func cronDuration(start, end *string) string {
	if start == nil || end == nil {
		return "—"
	}
	a, err1 := cronRunTimeParse(time.RFC3339, *start)
	b, err2 := cronRunTimeParse(time.RFC3339, *end)
	if err1 != nil || err2 != nil {
		return "—"
	}
	secs := int(b.Sub(a).Seconds())
	if secs < 0 {
		secs = 0
	}
	switch {
	case secs < 60:
		return fmt.Sprintf("%d s", secs)
	case secs < 3600:
		return fmt.Sprintf("%d min %d s", secs/60, secs%60)
	default:
		return fmt.Sprintf("%d h %d min", secs/3600, (secs%3600)/60)
	}
}

// formatCronBytes is the PHP's format_bytes for the log size.
func formatCronBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

// siteCronLog shows a job's runs from its log (sites.cron.log — cron.view).
// An agent error leaves the page with an empty log and the message.
func (s *Server) siteCronLog(w http.ResponseWriter, r *http.Request) {
	site := s.cronSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.cronFlashNotFound(w, r)
		return
	}
	jobID := cronJobIDOf(r)
	if jobID < 0 {
		s.cronFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	svc := s.cronService(r, user)
	job, err := svc.Find(r.Context(), site, jobID)
	if err != nil || job == nil {
		s.cronFlashErr(w, r, &hosting.UserError{What: "Cron job not found."})
		return
	}
	sess := sessionFrom(r.Context())
	result, err := svc.Runs(r.Context(), site, jobID)
	agentError := ""
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Message != "" {
			agentError = ae.Message
		} else {
			s.log.Error("read cron log", "error", err)
			agentError = "The log could not be read."
		}
		result = hosting.CronLogResult{}
	}
	runs := make([]cronRunView, 0, len(result.Runs))
	for _, rn := range result.Runs {
		started := ""
		if rn.Start != nil {
			started = formatCronTime(*rn.Start)
		}
		rv := cronRunView{CronRun: rn, Started: started, Duration: cronDuration(rn.Start, rn.End)}
		switch {
		case rn.Start == nil:
			rv.Result = "—"
		case rn.Exit == nil:
			rv.Result, rv.ResultClass = "Running or stopped", "is-warn"
		case *rn.Exit == 0:
			rv.Result, rv.ResultClass = "Success", "is-ok"
		default:
			rv.Result, rv.ResultClass = fmt.Sprintf("Failed (exit %d)", *rn.Exit), "is-error"
		}
		if rn.Start != nil {
			if rn.Manual {
				rv.Trigger = "Manual"
			} else {
				rv.Trigger = "Scheduled"
			}
		}
		lines := strings.Split(rn.Output, "\n")
		if rn.Output != "" {
			rv.FirstLine = lines[0]
			if rs := []rune(rv.FirstLine); len(rs) > 100 {
				rv.FirstLine = string(rs[:100])
			}
			rv.LineCount = len(lines)
			rv.SingleLine = len(lines) == 1 && len(lines[0]) <= 120
		}
		runs = append(runs, rv)
	}
	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sessionFrom(r.Context())),
		"Site":       site,
		"Job":        cronJobViewOf(*job),
		"Runs":       runs,
		"Truncated":  result.Truncated,
		"Size":       result.Size,
		"SizeLabel":  formatCronBytes(result.Size),
		"AgentError": agentError,
		"CanManage":  s.can(r, "cron.manage"),
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "site_cron_log", data)
}

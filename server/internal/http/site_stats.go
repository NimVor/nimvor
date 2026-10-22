package http

// This file ports the website's visitor statistics (StatsController,
// AWStats): the per-website report drawn from AWStats' plain-text data
// file (stats.status/stats.data), the on/off switch (stats.enable, with
// the first update when turned on) and the on-demand update (stats.update,
// a job). The data file's parsing lives in internal/stats.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/stats"
)

// statsStatus is the agent's stats.status reply (the service's status()).
type statsStatus struct {
	Installed  bool
	Enabled    bool
	Months     []string // YYYYMM, newest first, the 6-digit ones only
	UpdatedAt  string   // "" when the agent gave none
}

// statsMonthOption is one option of the month select.
type statsMonthOption struct {
	Value    string
	Label    string
	Selected bool
}

type statsDayRowView struct {
	Label   string
	Visits  string
	Bar     int
	Pages   string
	Hits    string
	Bytes   string
}

type statsHourRowView struct {
	Label string
	Bar   int
	Count string
}

type statsListRowView struct {
	Label string
	Value string
	Bar   int
}

// statsListPage is one card of the lists grid (the view's $list closure
// plus its column and icon).
type statsListPage struct {
	Cols  string
	Title string
	Icon  string
	Unit  string
	Mono  bool
	Rows  []statsListRowView
}

// statsReportView is the view's $report (formatted).
type statsReportView struct {
	Unique    string
	Visits    string
	Pages     string
	Hits      string
	Bytes     string
	Days      []statsDayRowView
	DaysEmpty bool
	Hours     []statsHourRowView
	Lists     []statsListPage
}

// statsTarget is the agent's site target (the service's target()).
func statsTarget(site *hosting.Website) map[string]any {
	return map[string]any{
		"domain":  site.Domain,
		"aliases": site.Aliases,
		"user":    site.SystemUser,
		"root":    site.Root,
	}
}

// statsStatusOf is StatsService::status (the agent's stats.status).
func (s *Server) statsStatusOf(ctx context.Context, site *hosting.Website) (statsStatus, *agent.Error) {
	res, err := s.agent.Call(ctx, "stats.status", statsTarget(site), 30*time.Second)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			return statsStatus{}, ae
		}
		s.log.Error("stats status", "error", err)
		return statsStatus{}, &agent.Error{Code: "agent_error", Message: "The WhaCP agent could not be reached."}
	}
	var out struct {
		Installed bool     `json:"installed"`
		Enabled   bool     `json:"enabled"`
		Months    []string `json:"months"`
		UpdatedAt *string  `json:"updated_at"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return statsStatus{}, &agent.Error{Code: "agent_bad_response", Message: "The WhaCP agent answered with an unreadable state."}
	}
	st := statsStatus{Installed: out.Installed, Enabled: out.Enabled}
	for _, m := range out.Months {
		// The service keeps the 6-digit months only (^[0-9]{6}$).
		if len(m) == 6 && isDigits(m) {
			st.Months = append(st.Months, m)
		}
	}
	if out.UpdatedAt != nil {
		st.UpdatedAt = *out.UpdatedAt
	}
	return st, nil
}

// statsDataOf is StatsService::report: one month's data file, parsed
// (nil without data).
func (s *Server) statsDataOf(ctx context.Context, site *hosting.Website, month string) (*stats.Report, *agent.Error) {
	res, err := s.agent.Call(ctx, "stats.data", mergeMaps(statsTarget(site), map[string]any{"month": month}), 60*time.Second)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			return nil, ae
		}
		s.log.Error("stats data", "error", err)
		return nil, &agent.Error{Code: "agent_error", Message: "The WhaCP agent could not be reached."}
	}
	var out struct {
		Found   bool   `json:"found"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, &agent.Error{Code: "agent_bad_response", Message: "The WhaCP agent answered with an unreadable state."}
	}
	if !out.Found {
		return nil, nil
	}
	return &stats.Parse(out.Content), nil
}

// statsUpdateOf is StatsService::update (the job id).
func (s *Server) statsUpdateOf(ctx context.Context, site *hosting.Website) (string, error) {
	res, err := s.agent.Call(ctx, "stats.update", statsTarget(site), 60*time.Second)
	if err != nil {
		return "", err
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(res, &out)
	return out.JobID, nil
}

// mergeMaps is a one-level copy of a + b.
func mergeMaps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// statsListPageOf is the view's $list helper: the rows' bars (rounded up
// to the next 5 percent).
func statsListPageOf(cols, title, icon, unit string, mono bool, rows []stats.TopRow) statsListPage {
	max := 0
	for _, r := range rows {
		if r.Value > max {
			max = r.Value
		}
	}
	out := statsListPage{
		Cols: cols, Title: title, Icon: icon, Unit: unit, Mono: mono,
		Rows: make([]statsListRowView, 0, len(rows)),
	}
	for _, r := range rows {
		out.Rows = append(out.Rows, statsListRowView{
			Label: r.Label,
			Value: numberFormat(r.Value),
			Bar:   stats.Bar(r.Value, max),
		})
	}
	return out
}

// siteStats renders the website's statistics page (show — logs.view).
func (s *Server) siteStats(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.statsNotFound(w)
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(ctx, s.currentServerID(r), id, user)
	if errors.Is(err, hosting.ErrWebsiteNotFound) || site == nil {
		s.statsNotFound(w)
		return
	}
	if err != nil {
		s.log.Error("find website", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	month := r.URL.Query().Get("month")
	var st statsStatus
	var report *stats.Report
	agentMsg := ""
	if status, ae := s.statsStatusOf(ctx, site); ae != nil {
		agentMsg = ae.Message
	} else {
		st = status
		if len(st.Months) > 0 {
			if !containsString(st.Months, month) {
				month = st.Months[0]
			}
			if rep, ae := s.statsDataOf(ctx, site, month); ae != nil {
				agentMsg = ae.Message
			} else {
				report = rep
			}
		}
	}
	ok, _ := s.authorizer().Can(ctx, user, "websites.manage")
	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Site":       site,
		"CanManage":  ok,
		"AgentError": agentMsg,
		"Status":     (*statsStatus)(nil),
		"Months":     []statsMonthOption{},
		"Report":     (*statsReportView)(nil),
		"Success":    "",
		"Error":      "",
		"Job":        "",
	}
	if agentMsg == "" {
		data["Status"] = &st
		months := make([]statsMonthOption, 0, len(st.Months))
		for _, m := range st.Months {
			months = append(months, statsMonthOption{Value: m, Label: stats.MonthName(m), Selected: m == month})
		}
		data["Months"] = months
		if report != nil {
			data["Report"] = statsReportViewOf(report)
		}
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	data["Job"], _ = sess.PullFlash("job")
	s.render(w, "sites_stats", data)
}

func (s *Server) statsNotFound(w http.ResponseWriter) {
	renderStatus(w, s, "error", map[string]any{
		"Title":   "Website not found",
		"Message": "This website does not exist or you do not have access to it.",
	}, http.StatusNotFound)
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// statsReportViewOf formats the parsed report for the template.
func statsReportViewOf(rep *stats.Report) *statsReportView {
	maxV := 0
	for _, d := range rep.Days {
		if d.Visits > maxV {
			maxV = d.Visits
		}
	}
	days := make([]statsDayRowView, 0, len(rep.Days))
	for _, d := range rep.Days {
		days = append(days, statsDayRowView{
			Label:  stats.DayLabel(d.Day),
			Visits: numberFormat(d.Visits),
			Bar:    stats.Bar(d.Visits, maxV),
			Pages:  numberFormat(d.Pages),
			Hits:   numberFormat(d.Hits),
			Bytes:  formatBytes(int64(d.Bytes)),
		})
	}
	maxH := 0
	for _, h := range rep.Hours {
		if h.Pages > maxH {
			maxH = h.Pages
		}
	}
	hours := make([]statsHourRowView, 0, 24)
	for h, row := range rep.Hours {
		hours = append(hours, statsHourRowView{
			Label: fmt.Sprintf("%02d", h),
			Bar:   stats.Bar(row.Pages, maxH),
			Count: numberFormat(row.Pages),
		})
	}
	return &statsReportView{
		Unique:    numberFormat(rep.Unique),
		Visits:    numberFormat(rep.Visits),
		Pages:     numberFormat(rep.Pages),
		Hits:      numberFormat(rep.Hits),
		Bytes:     formatBytes(int64(rep.Bytes)),
		Days:      days,
		DaysEmpty: len(days) == 0,
		Hours:     hours,
		Lists: []statsListPage{
			statsListPageOf("col-lg-6", "Most viewed pages", "file-earmark-text", "views", true, rep.PagesTop),
			statsListPageOf("col-lg-6", "Referring pages", "link-45deg", "visits", true, rep.Referrers),
			statsListPageOf("col-md-6 col-xl-4", "Search engines", "search", "visits", false, rep.Search),
			statsListPageOf("col-md-6 col-xl-4", "Browsers", "browser-chrome", "hits", false, rep.Browsers),
			statsListPageOf("col-md-6 col-xl-4", "Operating systems", "laptop", "hits", false, rep.Systems),
			statsListPageOf("col-md-6 col-xl-6", "Countries", "globe", "hits", false, rep.Countries),
			statsListPageOf("col-md-6 col-xl-6", "HTTP errors", "exclamation-triangle", "hits", false, rep.Errors),
		},
	}
}

// siteStatsEnable turns the website's statistics on or off (enable —
// websites.manage). Turning them on starts the first update.
func (s *Server) siteStatsEnable(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		sess.Flash("error", "Website not found.")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(ctx, s.currentServerID(r), id, user)
	if err != nil || site == nil {
		sess.Flash("error", "Website not found.")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
		return
	}
	to := fmt.Sprintf("/sites/%d/stats", site.ID)
	on := r.FormValue("enabled") == "1"
	if _, err := s.agent.Call(ctx, "stats.enable", mergeMaps(statsTarget(site), map[string]any{"enabled": on}), 120*time.Second); err != nil {
		agentErrorRedirect(w, r, "stats.enable", err, to)
		return
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     statsAuditAction(on),
		TargetType: "website",
		TargetID:   strconv.FormatInt(site.ID, 10),
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"domain": site.Domain},
	})
	if on {
		job, err := s.statsUpdateOf(ctx, site)
		if err != nil {
			agentErrorRedirect(w, r, "stats.update", err, to)
			return
		}
		if job != "" {
			userID := user.ID
			if err := hosting.NewJobTracker(s.db).Track(ctx, s.currentServerID(r), job,
				"Statistics of "+site.Domain, "website", strconv.FormatInt(site.ID, 10), &userID); err != nil {
				s.log.Error("track job", "job", job, "error", err)
			}
		}
	}
	if on {
		sess.Flash("success", "Statistics are on. They are updated every 6 hours; the first update is running now.")
	} else {
		sess.Flash("success", "Statistics are off. The collected data is kept.")
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func statsAuditAction(on bool) string {
	if on {
		return "stats.enable"
	}
	return "stats.disable"
}

// siteStatsUpdate updates the statistics now (update — websites.manage,
// a job). The PHP's update() does not audit (only the on/off switch does).
func (s *Server) siteStatsUpdate(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		sess.Flash("error", "Website not found.")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(ctx, s.currentServerID(r), id, user)
	if err != nil || site == nil {
		sess.Flash("error", "Website not found.")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
		return
	}
	to := fmt.Sprintf("/sites/%d/stats", site.ID)
	job, err := s.statsUpdateOf(ctx, site)
	if err != nil {
		agentErrorRedirect(w, r, "stats.update", err, to)
		return
	}
	if job != "" {
		userID := user.ID
		if err := hosting.NewJobTracker(s.db).Track(ctx, s.currentServerID(r), job,
			"Statistics of "+site.Domain, "website", strconv.FormatInt(site.ID, 10), &userID); err != nil {
			s.log.Error("track job", "job", job, "error", err)
		}
		sess.Flash("job", job)
	}
	sess.Flash("success", "Updating the statistics.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

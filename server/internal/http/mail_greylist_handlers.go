// This file ports the greylisting pages (MailGreylistController): the
// settings, trusted hosts, trusted mail providers and the triplet report
// with its cleanup actions. The agent keeps the configuration; every
// change sends the whole of it.
package http

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mailGreylistBack is where the settings changes come back (the PHP BACK).
const mailGreylistBack = "/mail/admin/greylisting"

// mailGreylistAuditHook is the shared audit hook with the target fixed
// (PHP: $audit->record($action, 'server', 'greylisting', …)).
func (s *Server) mailGreylistAuditHook(r *http.Request, user *identity.User) hosting.GreylistAudit {
	return func(action, result string, details map[string]any) {
		s.mailAdminAudit(r, user, action, "greylisting", result, details)
	}
}

// mailGreylistChange runs one greylisting change: on failure the error
// message is flashed and the browser redirected, on success the handler
// continues.
func (s *Server) mailGreylistChange(w http.ResponseWriter, r *http.Request, user *identity.User, action string, details map[string]any, change func(map[string]any) (map[string]any, error), back string) bool {
	if back == "" {
		back = mailGreylistBack
	}
	if _, err := hosting.GreylistChange(r.Context(), s.agent, s.mailGreylistAuditHook(r, user), action, details, change, "The greylisting settings could not be changed."); err != nil {
		sessionFrom(r.Context()).Flash("error", err.Error())
		http.Redirect(w, r, back, http.StatusSeeOther)
		return false
	}
	return true
}

// mailGreylistIndex renders the settings + trusted hosts + providers
// (MailGreylistController::index).
func (s *Server) mailGreylistIndex(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	var st *hosting.GreylistState
	agentErr := ""
	if raw, err := s.agent.Call(r.Context(), "mail.greylist_get", nil, 60*time.Second); err != nil {
		agentErr = s.mailAgentErr("mail.greylist_get", err)
	} else if err := json.Unmarshal(raw, &st); err != nil {
		s.log.Error("greylisting state", "error", err)
		agentErr = "The greylisting settings came back unreadable."
	}
	s.render(w, "mail_admin_greylisting", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Nav":       "greylisting",
		"State":     st,
		"Providers": mailGreylistProviderRows(st),
		"CanManage": s.can(r, "server.manage"),
		"AgentErr":  agentErr,
		"Success":   pullFlashKey(sess, "success"),
		"Error":     pullFlashKey(sess, "error"),
	})
}

// mailGreylistSave applies the settings form (MailGreylistController::save).
func (s *Server) mailGreylistSave(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	_ = r.ParseForm()
	in := map[string]any{
		"enabled":        r.PostFormValue("enabled") == "1",
		"deferral":       atoiDefault(r.PostFormValue("deferral"), 0),
		"resend":         atoiDefault(r.PostFormValue("resend"), 0),
		"expire":         atoiDefault(r.PostFormValue("expire"), 0),
		"spf_bypass":     r.PostFormValue("spf_bypass") == "1",
		"auto_trust_new": r.PostFormValue("auto_trust_new") == "1",
	}
	if !s.mailGreylistChange(w, r, user, "mail.greylist_settings", in, func(p map[string]any) (map[string]any, error) {
		for k, v := range in {
			p[k] = v
		}
		return p, nil
	}, "") {
		return
	}
	if in["enabled"].(bool) {
		sess.Flash("success", "Greylisting settings saved; greylisting is on.")
	} else {
		sess.Flash("success", "Greylisting settings saved; greylisting is off.")
	}
	http.Redirect(w, r, mailGreylistBack, http.StatusSeeOther)
}

// mailGreylistSaveTrusted adds or edits a trusted host
// (MailGreylistController::saveTrusted).
func (s *Server) mailGreylistSaveTrusted(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	_ = r.ParseForm()
	rng := strings.TrimSpace(r.PostFormValue("range"))
	comment := strings.TrimSpace(r.PostFormValue("comment"))
	index := r.PostFormValue("index")
	if err := hosting.GreylistValidateRange(rng); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, mailGreylistBack+"#trusted", http.StatusSeeOther)
		return
	}
	if !s.mailGreylistChange(w, r, user, "mail.greylist_trusted", map[string]any{"range": rng, "comment": comment}, func(p map[string]any) (map[string]any, error) {
		trusted := mailGreylistTrusted(p["trusted"])
		entry := map[string]string{"range": rng, "comment": comment}
		if i, err := strconv.Atoi(index); err == nil && i >= 0 && i < len(trusted) {
			trusted[i] = entry
		} else {
			trusted = append(trusted, entry)
		}
		p["trusted"] = trusted
		return p, nil
	}, "") {
		return
	}
	sess.Flash("success", "Trusted host "+rng+" saved.")
	http.Redirect(w, r, mailGreylistBack, http.StatusSeeOther)
}

// mailGreylistDeleteTrusted removes a trusted host
// (MailGreylistController::deleteTrusted).
func (s *Server) mailGreylistDeleteTrusted(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	n := r.PathValue("n")
	if !s.mailGreylistChange(w, r, user, "mail.greylist_trusted_delete", map[string]any{"index": atoiDefault(n, 0)}, func(p map[string]any) (map[string]any, error) {
		i, err := strconv.Atoi(n)
		if err != nil || i < 0 {
			return nil, &hosting.UserError{What: "That trusted host no longer exists."}
		}
		trusted := mailGreylistTrusted(p["trusted"])
		if i >= len(trusted) {
			return nil, &hosting.UserError{What: "That trusted host no longer exists."}
		}
		trusted = append(trusted[:i], trusted[i+1:]...)
		p["trusted"] = trusted
		return p, nil
	}, "") {
		return
	}
	sess.Flash("success", "Trusted host removed.")
	http.Redirect(w, r, mailGreylistBack, http.StatusSeeOther)
}

// mailGreylistSaveProviders saves the trust / auto-update choices of the
// provider table (MailGreylistController::saveProviders).
func (s *Server) mailGreylistSaveProviders(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	_ = r.ParseForm()
	trust := r.PostForm["trust[]"]
	auto := r.PostForm["auto_update[]"]
	if !s.mailGreylistChange(w, r, user, "mail.greylist_providers", map[string]any{"trusted": trust}, func(p map[string]any) (map[string]any, error) {
		providers := map[string]map[string]bool{}
		if cur, ok := p["providers"].(map[string]map[string]bool); ok {
			for id := range cur {
				providers[id] = map[string]bool{
					"trust":       containsString(trust, id),
					"auto_update": containsString(auto, id),
				}
			}
		}
		p["providers"] = providers
		return p, nil
	}, "") {
		return
	}
	sess.Flash("success", "Mail providers saved.")
	http.Redirect(w, r, mailGreylistBack, http.StatusSeeOther)
}

// mailGreylistUpdateProviders refreshes the provider ranges from their SPF
// records (MailGreylistController::updateProviders).
func (s *Server) mailGreylistUpdateProviders(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if msg, err := hosting.GreylistUpdateProviders(r.Context(), s.agent, s.mailGreylistAuditHook(r, user), nil); err != nil {
		sess.Flash("error", msg)
		http.Redirect(w, r, mailGreylistBack+"#providers", http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Provider address ranges updated from their SPF records.")
	http.Redirect(w, r, mailGreylistBack+"#providers", http.StatusSeeOther)
}

// mailGreylistQuery is the report query (MailGreylistController::query):
// the whitelists, the defaults and the bounds.
func mailGreylistQuery(q url.Values) map[string]any {
	get := func(k string) string {
		return strings.TrimSpace(q.Get(k))
	}
	status := get("status")
	statusValid := false
	for _, st := range hosting.GreylistStatuses {
		if st.Key == status {
			statusValid = true
			break
		}
	}
	if !statusValid {
		status = ""
	}
	sortKey := get("sort")
	sortValid := false
	for _, sk := range hosting.GreylistSorts {
		if sk == sortKey {
			sortValid = true
			break
		}
	}
	if !sortValid {
		sortKey = "created"
	}
	dir := get("dir")
	if dir != "asc" {
		dir = "desc"
	}
	search := get("search")
	if r := []rune(search); len(r) > 200 {
		search = string(r[:200])
	}
	page, _ := strconv.Atoi(get("page"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	per, _ := strconv.Atoi(get("per"))
	if per != 25 && per != 50 && per != 100 && per != 250 {
		per = 50
	}
	return map[string]any{
		"search": search, "status": status, "sort": sortKey, "dir": dir, "page": page, "per": per,
	}
}

// mailGreylistRow is one report row, prepared for the view.
type mailGreylistRow struct {
	IP           string
	From         string
	To           string
	Deferred     int
	Accepted     int
	Created      string
	BlockExpire  string
	MustRetry    string
	RecordExpire string
	StatusLabel  string
	StatusClass  string
	Sel          string // the checkbox value: the JSON [ip, from, to]
}

func mailGreylistRows(rep *hosting.GreylistReport) []mailGreylistRow {
	if rep == nil {
		return nil
	}
	labels := map[string]string{}
	for _, st := range hosting.GreylistStatuses {
		labels[st.Key] = st.Label
	}
	rows := make([]mailGreylistRow, 0, len(rep.Rows))
	for _, row := range rep.Rows {
		b, _ := json.Marshal([]string{row.IP, row.From, row.To})
		class := "is-off"
		switch row.Status {
		case "deferring":
			class = "is-warn"
		case "waiting", "passed":
			class = "is-ok"
		}
		label := labels[row.Status]
		if label == "" {
			label = row.Status
		}
		rows = append(rows, mailGreylistRow{
			IP:           row.IP,
			From:         row.From,
			To:           row.To,
			Deferred:     row.Deferred,
			Accepted:     row.Accepted,
			Created:      greylistTime(row.Created),
			BlockExpire:  greylistTime(row.BlockExpire),
			MustRetry:    greylistTime(row.MustRetry),
			RecordExpire: greylistTime(row.RecordExpire),
			StatusLabel:  label,
			StatusClass:  class,
			Sel:          string(b),
		})
	}
	return rows
}

// greylistTime is the report's time cell ("Y-m-d H:i", "—" when unset).
func greylistTime(t int64) string {
	if t <= 0 {
		return "—"
	}
	return time.Unix(t, 0).Format("2006-01-02 15:04")
}

// mailGreylistReport renders the triplet report
// (MailGreylistController::report).
func (s *Server) mailGreylistReport(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	q := mailGreylistQuery(r.URL.Query())
	var rep *hosting.GreylistReport
	agentErr := ""
	if raw, err := s.agent.Call(r.Context(), "mail.greylist_report", map[string]any{
		"search": q["search"], "status": q["status"], "sort": q["sort"],
		"desc": q["dir"] == "desc", "page": q["page"], "per": q["per"],
	}, 60*time.Second); err != nil {
		agentErr = s.mailAgentErr("mail.greylist_report", err)
	} else if err := json.Unmarshal(raw, &rep); err != nil {
		s.log.Error("greylisting report", "error", err)
		agentErr = "The greylisting report came back unreadable."
	}
	cols, total, pages := mailGreylistReportPage(q, rep)
	page := q["page"].(int)
	s.render(w, "mail_admin_greylist_report", map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sess),
		"Nav":         "greylisting",
		"Report":      rep,
		"Rows":        mailGreylistRows(rep),
		"Cols":        cols,
		"Counts":      greylistCountList(rep),
		"CountsTotal": rep.GreylistCountsTotal(),
		"Statuses":    greylistStatusLabelList(),
		"Percents":    []int{25, 50, 100, 250},
		"Q":           q,
		"Total":       total,
		"Pages":       pages,
		"PageShow":    greylistPageShow(page, pages),
		"PrevURL":     greylistReportURL(q, map[string]any{"page": greylistPageMax1(page - 1)}),
		"NextURL":     greylistReportURL(q, map[string]any{"page": greylistPageMin(pages, page+1)}),
		"CanManage":   s.can(r, "server.manage"),
		"AgentErr":    agentErr,
		"Success":     pullFlashKey(sess, "success"),
		"Error":       pullFlashKey(sess, "error"),
	})
}

// mailGreylistCol is one sortable column link (the PHP $link helper: the
// current query + the change, empties dropped).
type mailGreylistCol struct {
	Key    string
	Label  string
	URL    string
	Active bool
}

var mailGreylistColDefs = []struct {
	Key   string
	Label string
}{
	{"ip", "Sender IP address"},
	{"from", "From address"},
	{"to", "To address"},
	{"deferred", "Deferred"},
	{"accepted", "Accepted"},
	{"created", "Create time"},
	{"block_expire", "Block expire time"},
	{"must_retry", "Must retry time"},
	{"record_expire", "Record expire time"},
}

func greylistReportURL(q map[string]any, change map[string]any) string {
	merged := map[string]string{}
	for k, v := range q {
		if s, ok := v.(string); ok {
			merged[k] = s
		} else if n, ok := v.(int); ok {
			merged[k] = strconv.Itoa(n)
		}
	}
	for k, v := range change {
		if s, ok := v.(string); ok {
			merged[k] = s
		} else if n, ok := v.(int); ok {
			merged[k] = strconv.Itoa(n)
		}
	}
	vals := url.Values{}
	for _, k := range []string{"search", "status", "sort", "dir", "page", "per"} {
		if v, ok := merged[k]; ok && v != "" {
			vals.Set(k, v)
		}
	}
	return "/mail/admin/greylisting/report?" + vals.Encode()
}

func mailGreylistReportPage(q map[string]any, rep *hosting.GreylistReport) ([]mailGreylistCol, int, int) {
	total := 0
	if rep != nil {
		total = rep.Total
	}
	per := 50
	if n, ok := q["per"].(int); ok {
		per = n
	}
	if per < 1 {
		per = 50
	}
	pages := (total + per - 1) / per
	if pages < 1 {
		pages = 1
	}
	cols := make([]mailGreylistCol, 0, len(mailGreylistColDefs))
	sortKey, _ := q["sort"].(string)
	dir, _ := q["dir"].(string)
	for _, c := range mailGreylistColDefs {
		active := sortKey == c.Key
		next := "asc"
		if active && dir == "asc" {
			next = "desc"
		}
		col := mailGreylistCol{Key: c.Key, Label: c.Label, Active: active}
		col.URL = greylistReportURL(q, map[string]any{"sort": c.Key, "dir": next, "page": 1})
		cols = append(cols, col)
	}
	return cols, total, pages
}

func greylistPageMax1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func greylistPageMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func greylistPageShow(page, pages int) int {
	return greylistPageMin(page, pages)
}

type mailGreylistCount struct {
	Label string
	N     int
}

func greylistCountList(rep *hosting.GreylistReport) []mailGreylistCount {
	out := []mailGreylistCount{}
	for _, st := range hosting.GreylistStatuses {
		n := 0
		if rep != nil {
			n = rep.Counts[st.Key]
		}
		out = append(out, mailGreylistCount{Label: st.Label, N: n})
	}
	return out
}

type mailGreylistStatusLabel struct {
	Key   string
	Label string
}

func greylistStatusLabelList() []mailGreylistStatusLabel {
	out := []mailGreylistStatusLabel{}
	for _, st := range hosting.GreylistStatuses {
		out = append(out, mailGreylistStatusLabel{Key: st.Key, Label: st.Label})
	}
	return out
}

// mailGreylistCleanup removes records (MailGreylistController::cleanup).
func (s *Server) mailGreylistCleanup(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	_ = r.ParseForm()
	op := r.PostFormValue("op")
	back := "/mail/admin/greylisting/report"
	triplets := []hosting.GreylistTriplet{}
	if op == "delete" {
		for _, v := range r.PostForm["sel[]"] {
			var t []string
			if err := json.Unmarshal([]byte(v), &t); err != nil || len(t) != 3 {
				continue
			}
			triplets = append(triplets, hosting.GreylistTriplet{IP: t[0], From: t[1], To: t[2]})
		}
		if len(triplets) == 0 {
			sess.Flash("error", "Select records first.")
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
	} else if op != "purge" && op != "delete_all" {
		sess.Flash("error", "Unknown operation.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	removed, msg, err := hosting.GreylistCleanup(r.Context(), s.agent, op, triplets)
	if err != nil {
		sess.Flash("error", msg)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	s.mailAdminAudit(r, user, "mail.greylist_cleanup", "greylisting", "success", map[string]any{"op": op, "count": len(triplets)})
	sess.Flash("success", strconv.Itoa(removed)+" record(s) removed.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// mailGreylistProviderRow is one providers-table row, prepared for the view.
type mailGreylistProviderRow struct {
	ID          string
	Name        string
	SPF         string
	UpdatedText string // "N · updated …" when ranges were read
	Error       string
	Trust       bool
	AutoUpdate  bool
}

func mailGreylistProviderRows(st *hosting.GreylistState) []mailGreylistProviderRow {
	rows := []mailGreylistProviderRow{}
	if st == nil {
		return rows
	}
	now := time.Now()
	for _, p := range st.Providers {
		row := mailGreylistProviderRow{ID: p.ID, Name: p.Name, SPF: strings.Join(p.SPF, ", ")}
		if st.Config != nil {
			if ps := st.Config.Providers[p.ID]; ps != nil {
				row.Trust = ps.Trust
				row.AutoUpdate = ps.AutoUpdate
				row.Error = hosting.MailShorten(ps.Error, 160)
				if len(ps.Ranges) > 0 {
					row.UpdatedText = strconv.Itoa(len(ps.Ranges)) + " · updated " + hosting.GreylistAgo(ps.Updated, now)
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// mailGreylistTrusted reads the "trusted" parameter ([]map[string]string).
func mailGreylistTrusted(v any) []map[string]string {
	out := []map[string]string{}
	if list, ok := v.([]map[string]string); ok {
		out = list
	}
	return out
}

// atoiDefault parses a form integer, falling back when unreadable.
func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

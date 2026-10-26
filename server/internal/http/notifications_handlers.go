package http

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/notify"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// notificationsIndex lists the notifications the user may see, newest first.
// Mirrors NotificationsController::index (route "notifications",
// notifications.view).
const notificationsPerPage = 25

type notifRow struct {
	ID                                  int64
	Title, Body, When, Where, Job, Link string
	Icon, Label                         string
	IsRead                              bool
	HasLink                             bool
}

func notifQuery(show, severity, website string, page int) string {
	q := url.Values{}
	if show != "" {
		q.Set("show", show)
	}
	if severity != "" {
		q.Set("severity", severity)
	}
	if website != "" {
		q.Set("website", website)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

func notifFilter(r *http.Request) (notify.Filter, string, string, string) {
	q := r.URL.Query()
	f := notify.Filter{Unread: q.Get("show") != "all"}
	show := ""
	if !f.Unread {
		show = "all"
	}
	sev := q.Get("severity")
	switch sev {
	case "error", "warning", "success", "info":
		f.Severity = sev
	default:
		sev = ""
	}
	site := ""
	if id, _ := strconv.ParseInt(q.Get("website"), 10, 64); id > 0 {
		f.WebsiteID = id
		site = strconv.FormatInt(id, 10)
	}
	return f, show, sev, site
}

func (s *Server) notificationsIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := userFrom(ctx)
	sess := sessionFrom(ctx)
	repo := notify.NewRepository(s.db)
	f, show, sev, site := notifFilter(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	total, err := repo.Count(ctx, user, f)
	if err != nil {
		s.log.Error("count notifications", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	items, err := repo.ListFiltered(ctx, user, f, notificationsPerPage, (page-1)*notificationsPerPage)
	if err != nil {
		s.log.Error("list notifications", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	unread, _ := repo.UnreadCount(ctx, user)
	icons := map[string]string{"error": "x-octagon-fill text-danger", "warning": "exclamation-triangle-fill text-warning", "success": "check-circle-fill text-success", "info": "info-circle-fill text-info"}
	labels := map[string]string{"error": "Failures", "warning": "Warnings", "success": "Finished", "info": "Information"}
	rows := make([]notifRow, 0, len(items))
	for _, n := range items {
		icon, ok := icons[n.Severity]
		if !ok {
			icon = icons["info"]
		}
		label, ok := labels[n.Severity]
		if !ok {
			label = labels["info"]
		}
		row := notifRow{ID: n.ID, Title: n.Title, Body: n.Body, Icon: icon, Label: label, IsRead: n.IsRead, Job: n.JobID, Link: n.Link,
			HasLink: strings.HasPrefix(n.Link, "/") && !strings.HasPrefix(n.Link, "//")}
		if t, err := time.Parse(time.RFC3339, n.CreatedAt); err == nil {
			row.When = t.UTC().Format("2006-01-02 15:04")
		} else {
			row.When = n.CreatedAt
		}
		if n.Domain.Valid {
			row.Where = n.Domain.String
		}
		rows = append(rows, row)
	}
	pages := (total + notificationsPerPage - 1) / notificationsPerPage
	if pages < 1 {
		pages = 1
	}
	type siteOpt struct {
		ID       int64
		Domain   string
		Selected bool
	}
	var sites []siteOpt
	if list, err := hosting.NewWebsiteRepository(s.db).All(ctx, s.currentServerID(r), user); err == nil {
		for _, st := range list {
			sites = append(sites, siteOpt{st.ID, st.Domain, st.ID == f.WebsiteID})
		}
	}
	emailOn, _ := repo.EmailOpt(ctx, user.ID)
	type sevOpt struct {
		Value, Label string
		Selected     bool
	}
	var sevs []sevOpt
	for _, k := range []string{"error", "warning", "success", "info"} {
		sevs = append(sevs, sevOpt{k, labels[k], k == sev})
	}
	data := map[string]any{
		"User": user, "CSRF": session.CSRFToken(sess), "Title": "Notifications",
		"Success": pullFlashKey(sess, "success"), "Error": pullFlashKey(sess, "error"),
		"Rows": rows, "Unread": unread, "ShowAll": show == "all", "Show": show, "Severity": sev, "Website": site,
		"Sites": sites, "Sevs": sevs, "EmailOn": emailOn, "Email": user.Email,
		"Page": page, "Pages": pages, "Paged": pages > 1,
		"UnreadURL": "/notifications" + notifQuery("", sev, site, 1), "AllURL": "/notifications" + notifQuery("all", sev, site, 1),
		"PrevURL": "/notifications" + notifQuery(show, sev, site, page-1), "NextURL": "/notifications" + notifQuery(show, sev, site, page+1),
		"HasPrev": page > 1, "HasNext": page < pages, "Retention": 30,
	}
	s.render(w, "notifications", data)
}

// notificationsRead marks all the user's visible notifications read (CSRF-
// protected). Mirrors NotificationsController::read with no ids.
func (s *Server) notificationsRead(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	user := userFrom(r.Context())
	repo := notify.NewRepository(s.db)
	var marked int64
	var err error
	if r.FormValue("all") == "1" {
		marked, err = repo.MarkAllRead(r.Context(), user)
	} else {
		var ids []int64
		for _, v := range append(append([]string{}, r.Form["ids[]"]...), r.Form["ids"]...) {
			if id, e := strconv.ParseInt(v, 10, 64); e == nil && id > 0 {
				ids = append(ids, id)
			}
		}
		marked, err = repo.MarkRead(r.Context(), user, ids)
	}
	if err != nil {
		s.log.Error("mark notifications read", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if wantsJSON(r) {
		unread, _ := repo.UnreadCount(r.Context(), user)
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "marked": marked, "unread": unread})
		return
	}
	q := url.Values{}
	for _, k := range []string{"show", "severity", "website"} {
		if v := r.FormValue(k); v != "" {
			q.Set(k, v)
		}
	}
	back := "/notifications"
	if len(q) > 0 {
		back += "?" + q.Encode()
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// notificationsSummary returns the JSON payload the topbar bell polls: unread
// count plus the newest 8 unread items (severity/title/body truncated/ago/link).
// Mirrors NotificationsController::summary (GET /notifications/summary).
func (s *Server) notificationsSummary(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	repo := notify.NewRepository(s.db)
	unread, err := repo.UnreadCount(r.Context(), user)
	if err != nil {
		s.log.Error("unread count", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "internal"})
		return
	}
	latest, err := repo.ListUnread(r.Context(), user, 5)
	if err != nil {
		s.log.Error("latest notifications", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "internal"})
		return
	}
	items, err := repo.ListUnread(r.Context(), user, 8)
	if err != nil {
		s.log.Error("notifications list", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": "internal"})
		return
	}
	latestOut := make([]map[string]any, 0, len(latest))
	for _, n := range latest {
		latestOut = append(latestOut, map[string]any{
			"id": n.ID, "severity": n.Severity, "title": n.Title, "created_at": n.CreatedAt,
		})
	}
	itemsOut := make([]map[string]any, 0, len(items))
	for _, n := range items {
		body := n.Body
		if len([]rune(body)) > 160 {
			body = string([]rune(body)[:159]) + "…"
		}
		link := n.Link
		if !strings.HasPrefix(link, "/") || strings.HasPrefix(link, "//") {
			link = ""
		}
		where := ""
		if n.Domain.Valid {
			where = n.Domain.String
		}
		itemsOut = append(itemsOut, map[string]any{
			"id":       n.ID,
			"severity": n.Severity,
			"title":    n.Title,
			"body":     body,
			"ago":      notificationAgo(n.CreatedAt),
			"read":     n.IsRead,
			"link":     link,
			"where":    where,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"unread":  unread,
		"latest":  latestOut,
		"items":   itemsOut,
	})
}

// notificationsEmail toggles the current user's email-notification opt-in.
// Mirrors NotificationsController::email (POST /notifications/email).
func (s *Server) notificationsEmail(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	user := userFrom(r.Context())
	vals := r.Form["notify_email"]
	on := len(vals) > 0 && vals[len(vals)-1] == "1"
	if err := notify.NewRepository(s.db).SetEmailOpt(r.Context(), user.ID, on); err != nil {
		s.log.Error("set email opt-in", "error", err)
		sess.Flash("error", "Could not save the preference.")
		http.Redirect(w, r, "/notifications", http.StatusSeeOther)
		return
	}
	if on {
		sess.Flash("success", "You will be emailed about failures you can see. Email must be set up under Settings.")
	} else {
		sess.Flash("success", "You will no longer be emailed about failures.")
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
}

// notificationAgo is a tiny English relative-time formatter for ISO-8601 created_at
// timestamps ("3 minutes ago", "2 hours ago"), matching the PHP time_ago()
// helper used by the summary payload. Precise granularity is not required for
// the UI; we stay in whole units.
func notificationAgo(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		n := int(d / time.Minute)
		if n == 1 {
			return "1 minute ago"
		}
		return itoa(n) + " minutes ago"
	case d < 24*time.Hour:
		n := int(d / time.Hour)
		if n == 1 {
			return "1 hour ago"
		}
		return itoa(n) + " hours ago"
	case d < 30*24*time.Hour:
		n := int(d / (24 * time.Hour))
		if n == 1 {
			return "1 day ago"
		}
		return itoa(n) + " days ago"
	default:
		n := int(d / (30 * 24 * time.Hour))
		if n <= 1 {
			return "1 month ago"
		}
		return itoa(n) + " months ago"
	}
}

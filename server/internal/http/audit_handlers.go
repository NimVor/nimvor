package http

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

type auditRow struct {
	Time, User, Action, Target, Result, Cls, IP, Details string
}

const auditPerPage = 50

// auditIndex lists audit-log entries (newest first), with optional filters.
// Staff-only (audit.view); no tenant scoping. Mirrors AuditLogController::index
// (route "audit", audit.view).
func (s *Server) auditIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	q := r.URL.Query()
	f := audit.Filters{Action: q.Get("action"), Result: q.Get("result"), User: q.Get("user"), Q: q.Get("q")}
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	res, err := s.audit.Search(r.Context(), f, page, auditPerPage)
	if err != nil {
		s.log.Error("audit search", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rows := make([]auditRow, 0, len(res.Rows))
	for _, a := range res.Rows {
		row := auditRow{Time: a.CreatedAt, User: "—", Action: a.Action, Target: strings.TrimSpace(a.TargetType + " " + a.TargetID), Result: a.Result, IP: a.IP}
		if t, err := time.Parse(time.RFC3339, a.CreatedAt); err == nil {
			row.Time = t.UTC().Format("2006-01-02 15:04:05")
		}
		if a.Username != "" {
			row.User = a.Username
		}
		switch a.Result {
		case "success":
			row.Cls = "text-bg-success"
		case "failure":
			row.Cls = "text-bg-danger"
		case "denied":
			row.Cls = "text-bg-warning"
		default:
			row.Cls = "text-bg-secondary"
		}
		if a.Details != "" && a.Details != "{}" && a.Details != "[]" {
			row.Details = a.Details
		}
		rows = append(rows, row)
	}
	pages := (res.Total + auditPerPage - 1) / auditPerPage
	if pages < 1 {
		pages = 1
	}
	link := func(p int) string {
		v := url.Values{}
		for k, val := range map[string]string{"action": f.Action, "user": f.User, "result": f.Result, "q": f.Q} {
			if val != "" {
				v.Set(k, val)
			}
		}
		v.Set("page", strconv.Itoa(p))
		return "/audit?" + v.Encode()
	}
	type resOpt struct {
		Value, Label string
		Selected     bool
	}
	var results []resOpt
	for _, v := range []string{"success", "failure", "denied"} {
		results = append(results, resOpt{v, strings.ToUpper(v[:1]) + v[1:], v == f.Result})
	}
	s.render(w, "audit", map[string]any{
		"User": user, "CSRF": session.CSRFToken(sessionFrom(r.Context())), "Title": "Audit log", "Rows": rows, "Total": res.Total,
		"Filters": f, "Results": results, "Page": page, "Pages": pages, "Paged": pages > 1,
		"PrevURL": link(page - 1), "NextURL": link(page + 1), "HasPrev": page > 1, "HasNext": page < pages,
	})
}

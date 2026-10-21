package http

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// This file ports app/Http/Controllers/WorkersController (the site-scoped
// half: show/store/toggle/restart/delete). The global /workers index is the
// Runtimes/admin batch. PHP renders the create/edit form in a modal and
// re-renders validation errors through a redirect-and-flash round trip; the
// panel renders the form inline and re-renders errors in place (the same
// convention as the website form and the runtime page).

// workerRestartOptions is WorkerService::RESTART (ordered).
var workerRestartOptions = []struct{ Value, Label string }{
	{"always", "Always"},
	{"on-failure", "On failure"},
	{"no", "Never"},
}

// workerView is one row of the workers table: the stored worker plus its
// aggregated systemd state (PHP's $status[$w['unit']]).
type workerView struct {
	hosting.WorkerFull
	StateClass  string // alert-success | alert-error | muted
	StateText   string
	StateDetail string
}

// stateClass mirrors the PHP statusBadge() mapping onto the panel's classes:
// healthy green, failed/dead red, everything else muted.
func stateClass(active string) (class, text string) {
	switch active {
	case "active", "running", "valid", "enabled", "success", "ok", "yes", "succeeded", "completed":
		return "alert alert-success", active
	case "failed", "expired", "error", "failure", "inactive", "dead":
		return "alert alert-error", active
	default:
		return "small text-muted", active
	}
}

// workerStateDetail builds the "3/4 up · 12.5 MB" detail under the state.
func workerStateDetail(st *hosting.WorkerStatus) string {
	parts := []string{}
	if st.ProcsTotal > 1 {
		parts = append(parts, fmt.Sprintf("%d/%d up", st.ProcsRunning, st.ProcsTotal))
	}
	if st.MemoryBytes != nil {
		parts = append(parts, fmt.Sprintf("%.1f MB", float64(*st.MemoryBytes)/1048576))
	}
	return strings.Join(parts, " · ")
}

// siteWorkers renders the worker list with live state plus the create/edit
// form (WorkersController::show).
func (s *Server) siteWorkers(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	ws := s.workerService(r, user)
	workers, err := ws.ForSite(r.Context(), site.ID)
	if err != nil {
		s.log.Error("list workers", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	status := ws.Status(r.Context(), workers)
	var edit *hosting.WorkerFull
	if e := r.URL.Query().Get("edit"); e != "" {
		if id, perr := strconv.ParseInt(e, 10, 64); perr == nil {
			edit, err = ws.Find(r.Context(), site.ID, id)
			if err != nil {
				s.log.Error("find worker", "error", err)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}
	}
	runtimes := hosting.NewRuntimeService(s.agent).Selectable(r.Context())
	s.renderWorkers(w, r, user, site, workers, status, edit, runtimes, nil, nil)
}

// renderWorkers renders the workers page: the table and the form (open with
// the edit values or the rejected submission, as in PHP's old input).
func (s *Server) renderWorkers(w http.ResponseWriter, r *http.Request, user *identity.User, site *hosting.Website, workers []hosting.WorkerFull, status map[string]hosting.WorkerStatus, edit *hosting.WorkerFull, runtimes []string, old map[string]string, fields map[string]string) {
	rows := make([]workerView, 0, len(workers))
	for _, wr := range workers {
		v := workerView{WorkerFull: wr}
		if st, ok := status[wr.Unit()]; ok {
			v.StateClass, v.StateText = stateClass(st.ActiveState)
			v.StateDetail = workerStateDetail(&st)
		}
		rows = append(rows, v)
	}
	// Form values: the create defaults, the edit row, or the rejected
	// submission (PHP: $old ?: ($edit ?? [])).
	form := map[string]string{"restart": "always", "memory_mb": "0", "procs": "1", "enabled": "1"}
	if edit != nil {
		keys := make([]string, 0, len(edit.Env))
		for k := range edit.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys))
		for _, k := range keys {
			lines = append(lines, k+"="+edit.Env[k])
		}
		form = map[string]string{
			"name":      edit.Name,
			"command":   edit.Command,
			"directory": edit.Directory,
			"env":       strings.Join(lines, "\n"),
			"restart":   edit.Restart,
			"memory_mb": strconv.Itoa(edit.MemoryMB),
			"procs":     strconv.Itoa(edit.Procs),
			"runtime":   edit.Runtime,
			"enabled":   "0",
		}
		if edit.Enabled {
			form["enabled"] = "1"
		}
	}
	if len(old) > 0 {
		form = old
	}
	sess := sessionFrom(r.Context())
	errMsg, _ := sess.PullFlash("error")
	okMsg, _ := sess.PullFlash("success")
	s.render(w, "site_workers", map[string]any{
		"User":     user,
		"CSRF":     session.CSRFToken(sess),
		"Site":     site,
		"Workers":  rows,
		"Edit":     edit,
		"Form":     form,
		"Runtimes": runtimes,
		"Restarts": workerRestartOptions,
		"Errors":   fields,
		"Error":    errMsg,
		"Success":  okMsg,
	})
}

// workersSite resolves the route's website like PHP's act(): a missing or
// invisible site flashes and lands on the list, not a 404.
func (s *Server) workersSite(w http.ResponseWriter, r *http.Request, user *identity.User) (*hosting.Website, bool) {
	id, perr := strconv.ParseInt(r.PathValue("id"), 10, 64)
	site, ferr := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), id, user)
	if perr != nil || ferr != nil || site == nil {
		sess := sessionFrom(r.Context())
		sess.Flash("error", "Website not found.")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
		return nil, false
	}
	return site, true
}

// workersPage is the back-link for a site's worker routes.
func workersPage(site *hosting.Website) string {
	return fmt.Sprintf("/sites/%d/workers", site.ID)
}

// workerInput maps the workers form to WorkerInput (PHP passes the request
// input straight to WorkerService::save; the field names match).
func workerInput(in map[string]string) hosting.WorkerInput {
	// PHP: filter_var($input['enabled'] ?? true, FILTER_VALIDATE_BOOL) — the
	// form always submits the checkbox (with a hidden 0), missing means on.
	enabled := true
	if v, ok := in["enabled"]; ok {
		enabled = v == "1"
	}
	// PHP: (int) ($input['procs'] ?? 1) — a submitted value (even "" from a
	// cleared field, which is 0) is explicit; only an absent field defaults.
	var procs *int
	if v, ok := in["procs"]; ok {
		procs = hosting.ProcsPtr(phpInt(v))
	}
	return hosting.WorkerInput{
		Name:      in["name"],
		Command:   in["command"],
		Directory: in["directory"],
		Restart:   in["restart"],
		MemoryMB:  phpInt(in["memory_mb"]),
		Procs:     procs,
		Runtime:   in["runtime"],
		EnvText:   in["env"],
		Enabled:   boolPtr(enabled),
	}
}

// phpInt mirrors PHP's (int) cast on a form string: leading sign and digit
// prefix, everything else zero ("", "abc" → 0, "1.5" → 1).
func phpInt(v string) int {
	t := strings.TrimLeft(v, " \t\n\r")
	start := 0
	if start < len(t) && (t[0] == '+' || t[0] == '-') {
		start = 1
	}
	i := start
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	if i == start {
		return 0
	}
	n, _ := strconv.Atoi(t[:i])
	return n
}

// userErrorMessage joins a UserError's What/Why/Fix the way PHP's
// redirectWithFailure composes its flash (message + reason + remedy).
func userErrorMessage(ue *hosting.UserError) string {
	parts := []string{}
	for _, p := range []string{ue.What, ue.Why, ue.Fix} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

// flashWorkerError flashes a worker-op failure: the agent's message when the
// agent refused (PHP's AgentException is a UserFacingException), the service's
// user message, else a generic fallback; then redirects back.
func (s *Server) flashWorkerError(w http.ResponseWriter, r *http.Request, sess *session.Session, back, fallback string, err error) {
	var ae *agent.Error
	var ue *hosting.UserError
	switch {
	case errors.As(err, &ae):
		sess.Flash("error", ae.Message)
	case errors.As(err, &ue):
		sess.Flash("error", userErrorMessage(ue))
	default:
		s.log.Error("worker op", "error", err)
		sess.Flash("error", fallback)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// workersStore creates or updates a worker (WorkersController::store): a
// worker_id in the form means the edit form (update), otherwise a create.
func (s *Server) workersStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	site, ok := s.workersSite(w, r, user)
	if !ok {
		return
	}
	in := postValues(r)
	if !session.ValidateCSRF(sess, in["_token"]) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	var id *int64
	if n, perr := strconv.ParseInt(in["worker_id"], 10, 64); perr == nil && n > 0 {
		id = &n
	}
	ws := s.workerService(r, user)
	if err := ws.Save(r.Context(), site, id, workerInput(in)); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			// Re-render the full page in place with the field messages and
			// the submitted values (the panel's convention for rejected
			// forms; PHP redirects and re-renders show() with flashed old
			// input).
			workers, werr := ws.ForSite(r.Context(), site.ID)
			if werr != nil {
				s.log.Error("list workers", "error", werr)
				workers = nil
			}
			s.renderWorkers(w, r, user, site, workers, ws.Status(r.Context(), workers), nil, nil, in, ve.Fields)
			return
		}
		s.flashWorkerError(w, r, sess, workersPage(site), "The worker could not be saved.", err)
		return
	}
	msg := "Worker created."
	if id != nil {
		msg = "Worker updated and restarted."
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, workersPage(site), http.StatusSeeOther)
}

// workersWithWorker resolves the route's worker like PHP's withWorker(): a
// missing worker flashes and lands back on the workers page.
func (s *Server) workersWithWorker(w http.ResponseWriter, r *http.Request, user *identity.User, fn func(*hosting.Website, hosting.WorkerFull)) {
	site, ok := s.workersSite(w, r, user)
	if !ok {
		return
	}
	workerID, perr := strconv.ParseInt(r.PathValue("worker"), 10, 64)
	if perr != nil {
		sess := sessionFrom(r.Context())
		sess.Flash("error", "Worker not found.")
		http.Redirect(w, r, workersPage(site), http.StatusSeeOther)
		return
	}
	ws := s.workerService(r, user)
	worker, err := ws.Find(r.Context(), site.ID, workerID)
	if err != nil {
		s.log.Error("find worker", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if worker == nil {
		sess := sessionFrom(r.Context())
		sess.Flash("error", "Worker not found.")
		http.Redirect(w, r, workersPage(site), http.StatusSeeOther)
		return
	}
	fn(site, *worker)
}

// workersToggle starts or stops a worker (WorkersController::toggle).
func (s *Server) workersToggle(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	s.workersWithWorker(w, r, user, func(site *hosting.Website, worker hosting.WorkerFull) {
		wasEnabled := worker.Enabled
		if err := s.workerService(r, user).SetEnabled(r.Context(), site, worker, !wasEnabled); err != nil {
			s.flashWorkerError(w, r, sess, workersPage(site), "The worker state could not be changed.", err)
			return
		}
		msg := "Worker started."
		if wasEnabled {
			msg = "Worker stopped."
		}
		sess.Flash("success", msg)
		http.Redirect(w, r, workersPage(site), http.StatusSeeOther)
	})
}

// workersRestart restarts a worker's units (WorkersController::restart).
func (s *Server) workersRestart(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	s.workersWithWorker(w, r, user, func(site *hosting.Website, worker hosting.WorkerFull) {
		if err := s.workerService(r, user).Restart(r.Context(), site, worker); err != nil {
			s.flashWorkerError(w, r, sess, workersPage(site), "The worker could not be restarted.", err)
			return
		}
		sess.Flash("success", "Worker restarted.")
		http.Redirect(w, r, workersPage(site), http.StatusSeeOther)
	})
}

// workersDelete removes a worker (WorkersController::delete).
func (s *Server) workersDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	s.workersWithWorker(w, r, user, func(site *hosting.Website, worker hosting.WorkerFull) {
		if err := s.workerService(r, user).Delete(r.Context(), site, worker); err != nil {
			s.flashWorkerError(w, r, sess, workersPage(site), "The worker could not be removed.", err)
			return
		}
		sess.Flash("success", "Worker removed.")
		http.Redirect(w, r, workersPage(site), http.StatusSeeOther)
	})
}

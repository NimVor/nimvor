package http

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// This file ports WorkersController::index (GET /workers): the global list
// of every worker on the server the user can see, with its live state. The
// per-site workers page is the edit point, so the rows link there.

// globalWorkerView is one row of the global list: the worker, the website it
// belongs to, and its live systemd state (WorkersController::index rows).
type globalWorkerView struct {
	workerView
	Domain    string
	WebsiteID int64
}

// workersIndex renders the global workers page (WorkersController::index).
func (s *Server) workersIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	repo := hosting.NewWebsiteRepository(s.db)
	sites, err := repo.All(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list sites for workers index", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ws := s.workerService(r, user)
	type row struct {
		worker hosting.WorkerFull
		domain string
		siteID int64
	}
	rows := []row{}
	for i := range sites {
		workers, err := ws.ForSite(r.Context(), sites[i].ID)
		if err != nil {
			s.log.Error("list workers", "site", sites[i].ID, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		for j := range workers {
			rows = append(rows, row{workers[j], sites[i].Domain, sites[i].ID})
		}
	}
	// One status call for the whole list (PHP builds the rows first, then
	// asks the agent once).
	all := make([]hosting.WorkerFull, len(rows))
	for i, x := range rows {
		all[i] = x.worker
	}
	status := ws.Status(r.Context(), all)
	out := make([]globalWorkerView, 0, len(rows))
	for _, x := range rows {
		v := globalWorkerView{Domain: x.domain, WebsiteID: x.siteID}
		v.WorkerFull = x.worker
		if st, ok := status[x.worker.Unit()]; ok {
			v.StateClass, v.StateText = stateClass(st.ActiveState)
			v.StateDetail = workerStateDetail(&st)
		}
		out = append(out, v)
	}
	s.render(w, "workers_index", map[string]any{
		"User":    user,
		"CSRF":    session.CSRFToken(sessionFrom(r.Context())),
		"Workers": out,
	})
}

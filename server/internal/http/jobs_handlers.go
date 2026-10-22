package http

// This file ports the JSON job-log endpoint (JobsController::show): the
// panel's job viewer (whacp.js) polls it with an offset and renders the
// streamed log. The viewer's modal UI lives in the PHP layout's bootstrap
// shell and rides with the Go shell work; the databases pages carry the
// "Job <id> started" flash alert meanwhile.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
)

// jobsShow streams a background job's log: the polled JSON the reused
// whacp.js renders (job, log, offset, done, plus the tracked kind).
// Authenticated only — visibility is per job (JobTracker.VisibleTo), so the
// route carries no permission, as in PHP. Mirrors JobsController::show
// (route jobs.show).
func (s *Server) jobsShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	serverID := s.currentServerID(r)
	id := r.PathValue("id")
	tracker := hosting.NewJobTracker(s.db)

	var job *hosting.AgentJob
	if hosting.JobIDRe.MatchString(id) {
		var err error
		job, err = tracker.Find(r.Context(), serverID, id)
		if err != nil {
			s.log.Error("find job", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"success": false,
				"error":   map[string]string{"message": "Internal error."},
			})
			return
		}
	}
	if job == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Job not found."},
		})
		return
	}
	visible, err := tracker.VisibleTo(r.Context(), job, user)
	if err != nil {
		s.log.Error("check job visibility", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Internal error."},
		})
		return
	}
	if !visible {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Job not found."},
		})
		return
	}

	offset := 0
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	res, err := tracker.Poll(r.Context(), s.agent, serverID, id, offset)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			e := map[string]any{"message": ae.Message}
			if ae.Details != "" {
				e["details"] = ae.Details
			}
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": e})
			return
		}
		s.log.Error("poll job", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Internal error."},
		})
		return
	}

	var out map[string]any
	if err := json.Unmarshal(res, &out); err != nil {
		out = map[string]any{}
	}
	out["success"] = true
	out["kind"] = job.Kind
	writeJSON(w, http.StatusOK, out)
}

package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// plansStore POST /plans — creates or updates a hosting plan
// (PlansController::store). Resellers may only touch their own plans; staff
// may also set feature_list_id and ffmpeg. Limits/ffmpeg/bandwidth/disk-quota
// reconciliation is not ported yet; the plan row itself is saved faithfully.
func (s *Server) plansStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	var idPtr *int64
	if raw := r.FormValue("plan_id"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			idPtr = &v
		}
	}
	fields := map[string]int{}
	for _, f := range hosting.PlanFields {
		fields[f] = formInt(r, f, 0)
	}
	limits := map[string]int{}
	for f := range hosting.PlanLimitColumns {
		limits[f] = formInt(r, f, 0)
	}
	var fl *int64
	if hosting.IsAll(user) {
		if raw := r.FormValue("feature_list_id"); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
				fl = &v
			}
		}
	}
	in := hosting.PlanSave{
		ID:          idPtr,
		Name:        r.FormValue("name"),
		Description: r.FormValue("description"),
		Fields:      fields,
		Limits:      limits,
		FeatureList: fl,
		Ffmpeg:      lastPostValue(r, "ffmpeg") == "1",
	}
	saved, err := hosting.NewPlanRepository(s.db).Save(r.Context(), user, in)
	if err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.backWithErrors(w, r, "/plans", ve.Fields)
			return
		}
		sess.Flash("error", "The plan could not be saved: "+err.Error())
		http.Redirect(w, r, "/plans", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "plan.save", TargetType: "plan", TargetID: strconv.FormatInt(saved, 10),
		Result: audit.Success,
		Details: map[string]any{
			"name":            in.Name,
			"feature_list_id": fl,
			"ffmpeg":          in.Ffmpeg,
		},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Plan saved.")
	http.Redirect(w, r, "/plans", http.StatusSeeOther)
}

// plansDestroy POST /plans/{id}/delete — removes a plan with no assigned
// users (PlansController::destroy).
func (s *Server) plansDestroy(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	name, err := hosting.NewPlanRepository(s.db).Delete(r.Context(), user, id)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/plans", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "plan.delete", TargetType: "plan", TargetID: strconv.FormatInt(id, 10),
		Result: audit.Success, Details: map[string]any{"name": name},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Plan deleted.")
	http.Redirect(w, r, "/plans", http.StatusSeeOther)
}

// formInt parses a form value as a non-negative int, falling back to def.
func formInt(r *http.Request, key string, def int) int {
	raw := r.FormValue(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return def
	}
	return v
}

// lastPostValue is the last value posted for key: a switch sends a hidden 0
// before its checkbox's 1, and the last one counts (PHP's $_POST).
func lastPostValue(r *http.Request, key string) string {
	v := r.PostForm[key]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

package http

import (
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// featuresIndex GET /features — lists feature lists for administrators
// (FeaturesController::index).
func (s *Server) featuresIndex(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	data := map[string]any{
		"CSRF":     session.CSRFToken(sess),
		"User":     user,
		"Title":    "Feature lists",
		"Lists":    nil,
		"Catalogue": featureCatalogue(),
		"Success":  "",
		"Error":    "",
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	lists, err := s.features.Lists(r.Context())
	if err != nil {
		data["Error"] = err.Error()
	} else {
		data["Lists"] = lists
	}
	s.render(w, "features_index", data)
}

// featuresStore POST /features — creates or updates a feature list
// (FeaturesController::store).
func (s *Server) featuresStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	desc := r.FormValue("description")
	enabled := r.Form["features"]
	var idPtr *int64
	if raw := r.FormValue("list_id"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			idPtr = &v
		}
	}
	saved, err := s.features.Save(r.Context(), idPtr, name, desc, enabled, user.ID)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/features", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "feature_list.save", TargetType: "feature_list",
		TargetID: strconv.FormatInt(saved, 10), Result: audit.Success,
		Details: map[string]any{"name": name, "features": enabled},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Feature list saved. It applies to the plans using it immediately.")
	http.Redirect(w, r, "/features", http.StatusSeeOther)
}

// featuresDestroy POST /features/{id}/delete — removes a feature list,
// nulling it on any plans referencing it (FeaturesController::destroy).
func (s *Server) featuresDestroy(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
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
	name, err := s.features.Delete(r.Context(), id)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/features", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "feature_list.delete", TargetType: "feature_list",
		TargetID: strconv.FormatInt(id, 10), Result: audit.Success,
		Details: map[string]any{"name": name},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Feature list \u201C"+name+"\u201D removed; plans that used it now have every feature.")
	http.Redirect(w, r, "/features", http.StatusSeeOther)
}

// featureCatalogue returns the Features map grouped by Group for the UI.
func featureCatalogue() map[string][]authz.Feature {
	out := map[string][]authz.Feature{}
	for _, f := range authz.Features {
		out[f.Group] = append(out[f.Group], f)
	}
	return out
}

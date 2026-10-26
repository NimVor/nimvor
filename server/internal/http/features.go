package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// featuresIndex GET /features — lists feature lists for administrators
// (FeaturesController::index).
var featureOrder = []string{"php", "mysql", "mongodb", "email", "dns", "file_manager", "ssl", "subdomains", "addon_domains", "cloudflare", "ftp", "cron", "backups", "auto_backups", "apps", "git", "workers", "redis", "memcached", "nodejs", "python", "logs", "stats", "site_health", "malware", "firewall", "site_security", "ssh", "wordpress", "type_php", "type_static", "type_nodejs", "type_python", "type_proxy"}

type featureItem struct {
	Key, Label string
	On, Off    bool // On: checked in the form; Off: off by default
}

type featureGroup struct {
	Name  string
	Items []featureItem
}

type featureListRow struct {
	ID                int64
	Name, Description string
	Included, Total   int
	OffText           string
	Plans             int
	Groups            []featureGroup
	DeleteConfirm     string
}

func offDefault(key string) bool {
	for _, k := range authz.DefaultOff {
		if k == key {
			return true
		}
	}
	return false
}

func hasKey(l []string, k string) bool {
	for _, x := range l {
		if x == k {
			return true
		}
	}
	return false
}

// featureGroups groups the registry for the form, with each switch's state for
// the list (nil = a new list: everything on except the default-off features).
func featureGroups(l *authz.FeatureList) []featureGroup {
	var groups []featureGroup
	idx := map[string]int{}
	for _, key := range featureOrder {
		f, ok := authz.Features[key]
		if !ok {
			continue
		}
		isOff := offDefault(key)
		on := !isOff
		if l != nil {
			if isOff {
				on = hasKey(l.EnabledOptIn, key)
			} else {
				on = !hasKey(l.Disabled, key)
			}
		}
		g, ok := idx[f.Group]
		if !ok {
			g = len(groups)
			idx[f.Group] = g
			groups = append(groups, featureGroup{Name: f.Group})
		}
		groups[g].Items = append(groups[g].Items, featureItem{Key: key, Label: f.Label, On: on, Off: isOff})
	}
	return groups
}

// featuresIndex GET /features (FeaturesController::index).
func (s *Server) featuresIndex(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	data := map[string]any{
		"CSRF": session.CSRFToken(sess), "User": user, "Title": "Feature Manager", "Success": "", "Error": "",
		"NewGroups": featureGroups(nil), "Total": len(featureOrder),
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	var rows []featureListRow
	lists, err := s.features.Lists(r.Context())
	if err != nil {
		data["Error"] = err.Error()
	}
	for i := range lists {
		l := &lists[i]
		var off []string
		seen := map[string]bool{}
		for _, k := range l.Disabled {
			if !seen[k] {
				seen[k] = true
				off = append(off, k)
			}
		}
		for _, k := range authz.DefaultOff {
			if !hasKey(l.EnabledOptIn, k) && !seen[k] {
				seen[k] = true
				off = append(off, k)
			}
		}
		var names []string
		for _, k := range off {
			if f, ok := authz.Features[k]; ok {
				names = append(names, f.Label)
			} else {
				names = append(names, k)
			}
		}
		row := featureListRow{ID: l.ID, Name: l.Name, Description: l.Description, Total: len(featureOrder), Included: len(featureOrder) - len(off),
			Plans: l.Plans, Groups: featureGroups(l)}
		if len(names) > 0 {
			row.OffText = strings.Join(names, ", ")
		}
		row.DeleteConfirm = "Delete the feature list \u201C" + l.Name + "\u201D?"
		if l.Plans > 0 {
			row.DeleteConfirm += " The " + strconv.Itoa(l.Plans) + " plan(s) using it will have every feature."
		}
		rows = append(rows, row)
	}
	data["Lists"] = rows
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
	enabled := append(append([]string{}, r.Form["features[]"]...), r.Form["features"]...)
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
		UserID:  &uid, Username: user.Username,
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
		UserID:  &uid, Username: user.Username,
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

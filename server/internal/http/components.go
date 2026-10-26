package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// componentsPages maps component id to the route name of its own settings
// page (ComponentsController::PAGES).
var componentsPages = map[string]string{
	"web": "websites", "php": "php", "database": "databases.server", "mail": "mail",
	"dns": "dns", "ftp": "ftp", "redis": "redis", "memcached": "memcached",
	"mongodb": "mongo", "firewall": "security.firewall", "fail2ban": "security.fail2ban",
	"clamav": "security.malware", "certbot": "ssl.autossl",
}

// componentsIndex GET /components — lists server components and their
// install/start/boot/ports state as reported by the agent. Mirrors
// ComponentsController::index (components, server.view).
func (s *Server) componentsIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Components",
		"Components": nil,
		"AgentError": nil,
		"Expected":   []string{},
		"Success":    "",
		"Error":      "",
		"Pages":      componentsPages,
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	if raw, err := s.agent.Call(r.Context(), "components.status", nil, 60*time.Second); err == nil {
		var comps any
		_ = json.Unmarshal(raw, &comps)
		data["Components"] = comps
		// Compute expected list from the settings string; default to installed ids.
		expected, _ := s.settings.String(r.Context(), "components_expected", "")
		if expected == "" {
			// Fall back to installed ones.
			data["Expected"] = componentsInstalled(raw)
		} else if expected == "none" {
			data["Expected"] = []string{}
		} else {
			ids := []string{}
			for _, id := range strings.Split(expected, ",") {
				id = strings.TrimSpace(id)
				if _, ok := componentsPages[id]; ok {
					ids = append(ids, id)
				}
			}
			data["Expected"] = ids
		}
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "components_index", data)
}

// componentsInstalled returns the ids of components marked installed from
// the raw components.status payload (mirrors ComponentsController::expected
// default).
func componentsInstalled(raw json.RawMessage) []string {
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	var out []string
	for _, c := range arr {
		if inst, _ := c["installed"].(bool); inst {
			if id, _ := c["id"].(string); id != "" {
				out = append(out, id)
			}
		}
	}
	return out
}

// componentsProfile POST /components/profile — saves which components the
// server should have (the "expected" list). Mirrors
// ComponentsController::profile (components.profile, server.manage).
func (s *Server) componentsProfile(w http.ResponseWriter, r *http.Request) {
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
	if !hosting.IsAll(user) {
		http.Redirect(w, r, "/components", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		// already parsed
	}
	ids := r.Form["expected"]
	keep := []string{}
	for _, id := range ids {
		if _, ok := componentsPages[id]; ok {
			keep = append(keep, id)
		}
	}
	val := "none"
	if len(keep) > 0 {
		val = strings.Join(keep, ",")
	}
	if err := s.settings.Set(r.Context(), "components_expected", val, user.ID); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/components", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "components.profile", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, Details: map[string]any{"expected": keep},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Saved what this server should have.")
	http.Redirect(w, r, "/components", http.StatusSeeOther)
}

// componentsFix POST /components/{id}/{step} — runs one fix step for a
// component (install/start/boot/ports). Mirrors ComponentsController::fix
// (components.fix, permission varies per step).
func (s *Server) componentsFix(w http.ResponseWriter, r *http.Request) {
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
	if !hosting.IsAll(user) {
		http.Redirect(w, r, "/components", http.StatusSeeOther)
		return
	}
	id := r.PathValue("id")
	step := r.PathValue("step")
	if _, ok := componentsPages[id]; !ok {
		sess.Flash("error", "Unknown component or step.")
		http.Redirect(w, r, "/components", http.StatusSeeOther)
		return
	}
	needPerm := map[string]string{
		"install": "packages.manage",
		"start":   "services.manage",
		"boot":    "services.manage",
		"ports":   "security.manage",
	}[step]
	if needPerm == "" {
		sess.Flash("error", "Unknown component or step.")
		http.Redirect(w, r, "/components", http.StatusSeeOther)
		return
	}
	if !s.can(r, needPerm) {
		sess.Flash("error", "You may not do this.")
		http.Redirect(w, r, "/components#c-"+id, http.StatusSeeOther)
		return
	}
	// Re-fetch status and pull items for the requested step.
	raw, err := s.agent.Call(r.Context(), "components.status", nil, 60*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/components#c-"+id, http.StatusSeeOther)
		return
	}
	var comps []map[string]any
	_ = json.Unmarshal(raw, &comps)
	var target map[string]any
	for _, c := range comps {
		if i, _ := c["id"].(string); i == id {
			target = c
			break
		}
	}
	if target == nil {
		sess.Flash("error", "Unknown component.")
		http.Redirect(w, r, "/components", http.StatusSeeOther)
		return
	}
	stepKey := map[string]string{
		"install": "installed", "start": "running", "boot": "boot", "ports": "ports",
	}[step]
	items := []string{}
	if steps, _ := target["steps"].([]any); steps != nil {
		for _, sraw := range steps {
			if sm, ok := sraw.(map[string]any); ok {
				if sid, _ := sm["id"].(string); sid == stepKey {
					if st, _ := sm["status"].(string); st == "todo" {
						if arr, _ := sm["items"].([]any); arr != nil {
							for _, it := range arr {
								if s, ok := it.(string); ok {
									items = append(items, s)
								}
							}
						}
					}
				}
			}
		}
	}
	if len(items) == 0 {
		sess.Flash("success", "Nothing to do: component is already fine for this step.")
		http.Redirect(w, r, "/components#c-"+id, http.StatusSeeOther)
		return
	}
	uid := user.ID
	back := "/components#c-" + id
	switch step {
	case "install":
		res, err := s.agent.Call(r.Context(), "packages.install", map[string]any{"names": items}, 120*time.Second)
		if err != nil {
			sess.Flash("error", err.Error())
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		job := ""
		if m := agentObject(res); m != nil {
			if j, _ := m["job_id"].(string); j != "" {
				job = j
			}
		}
		if job != "" {
			_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Install "+id, "server", itoa(s.currentServerID(r)), &uid)
			sess.Flash("job", job)
		}
		s.audit.Record(r.Context(), audit.Entry{
			Action: "packages.install", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
			Result: audit.Success, Details: map[string]any{"names": items},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("success", "Installing "+strings.Join(items, ", ")+"…")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	case "start", "boot":
		op := "start"
		if step == "boot" {
			op = "enable"
		}
		for _, unit := range items {
			if _, err := s.agent.Call(r.Context(), "services.control", map[string]any{"unit": unit, "op": op}, 30*time.Second); err != nil {
				sess.Flash("error", err.Error())
				http.Redirect(w, r, back, http.StatusSeeOther)
				return
			}
			s.audit.Record(r.Context(), audit.Entry{
				Action: "service." + op, TargetType: "service", TargetID: unit,
				Result: audit.Success, UserID: &uid, Username: user.Username,
			})
		}
		if op == "start" {
			sess.Flash("success", "Started: "+strings.Join(items, ", ")+".")
		} else {
			sess.Flash("success", "Now started at boot: "+strings.Join(items, ", ")+".")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	default: // ports
		// Open the required ports in the firewall via agent action
		// firewall.allow_ports (matches ComponentsController::fix ports
		// branch). Each item is "port/proto" (proto defaults to "tcp").
		var ports []map[string]any
		for _, it := range items {
			port, proto := it, "tcp"
			if i := strings.Index(it, "/"); i >= 0 {
				port, proto = it[:i], it[i+1:]
				if proto == "" {
					proto = "tcp"
				}
			}
			ports = append(ports, map[string]any{"port": port, "proto": proto})
		}
		// Comment is the component name (≤60 chars, single line).
		comment := strings.TrimSpace(strings.Map(func(r rune) rune {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
				r == ' ' || r == '.' || r == '_' || r == ':' || r == '\\' || r == '/' || r == '-' {
				return r
			}
			return ' '
		}, nameOf(id)))
		if len(comment) > 60 {
			comment = comment[:60]
		}
		raw, err := s.agent.Call(r.Context(), "firewall.allow_ports", map[string]any{"ports": ports, "comment": comment}, 90*time.Second)
		if err != nil {
			sess.Flash("error", err.Error())
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		// Track any confirmation job (ports sometimes open with a
		// temporary "pending" rule that must be confirmed within 2 min).
		var jobID string
		var changed []any
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err == nil {
			if j, _ := parsed["pending"].(map[string]any); j != nil {
				if id, _ := j["id"].(string); id != "" {
					jobID = id
				}
			}
			if c, _ := parsed["changed"].([]any); c != nil {
				changed = c
			}
		}
		if jobID != "" {
			_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, "Open ports for "+nameOf(id), "server", itoa(s.currentServerID(r)), &uid)
			sess.Flash("job", jobID)
		}
		s.audit.Record(r.Context(), audit.Entry{
			Action: "firewall.allow_ports", TargetType: "server", TargetID: "security",
			Result: audit.Success, Details: map[string]any{"ports": items, "pending": jobID},
			UserID: &uid, Username: user.Username,
		})
		if len(changed) == 0 && jobID == "" {
			sess.Flash("success", "Those ports are already allowed.")
		} else {
			sess.Flash("success", "Opened "+strings.Join(items, ", ")+". Check that you can still connect, then click Keep change within 2 minutes — otherwise it is undone automatically.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
}

// nameOf returns a human-friendly title for a component id (used for audit
// comments and firewall rule comments). Falls back to the id.
func nameOf(id string) string {
	if t, ok := componentsPages[id]; ok && t != "" {
		return t
	}
	return id
}

package http

import (
	"encoding/json"
	"fmt"
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
	data["Cards"], data["UnusedCards"], data["Ready"], data["UsedCount"], data["AllReady"] = s.componentCards(r, data)
	s.render(w, "components_index", data)
}

type componentStep struct {
	ID, Label, Status, Detail, Icon string
	FixAction, FixLabel, FixConfirm string
	PageURL, PageLabel              string
}

type componentCard struct {
	ID, Name, Description, StateText, StateClass, SettingsURL, Units string
	Used, Installed                                                  bool
	Steps                                                            []componentStep
}

// componentCards ports the card building of components/index.php.
func (s *Server) componentCards(r *http.Request, data map[string]any) (used, unused []componentCard, ready, usedCount int, allReady bool) {
	comps, _ := data["Components"].([]any)
	expected := map[string]bool{}
	if ex, ok := data["Expected"].([]string); ok {
		for _, e := range ex {
			expected[e] = true
		}
	}
	user := userFrom(r.Context())
	az := s.authorizer()
	can := func(perm string) bool { ok, err := az.Can(r.Context(), user, perm); return err == nil && ok }
	labels := map[string]string{"installed": "Packages", "setup": "Set up", "running": "Running", "boot": "Starts at boot", "ports": "Ports open"}
	fix := map[string]struct{ action, perm, label string }{
		"installed": {"install", "packages.manage", "Install"}, "running": {"start", "services.manage", "Start"},
		"boot": {"boot", "services.manage", "Start at boot"}, "ports": {"ports", "security.manage", "Open ports"},
	}
	icons := map[string]string{"ok": "bi-check-circle-fill text-success", "todo": "bi-exclamation-circle-fill text-warning", "na": "bi-dash-circle text-body-secondary"}
	for _, c := range comps {
		id := jstr(c, "id")
		card := componentCard{ID: id, Name: jstr(c, "name"), Description: jstr(c, "description"), Used: expected[id], Installed: jbool(c, "installed")}
		pageURL := routeURL(componentsPages[id])
		switch {
		case !card.Installed:
			card.StateText = "Not installed"
		case jbool(c, "ready"):
			card.StateText, card.StateClass = "Ready", "is-ok"
		default:
			card.StateText, card.StateClass = "Needs attention", "is-warn"
		}
		if pageURL != "" && card.Installed {
			card.SettingsURL = pageURL
		}
		if us, ok := jmap(c)["units"].([]any); ok {
			var names []string
			for _, u := range us {
				if str, ok := u.(string); ok {
					names = append(names, str)
				}
			}
			card.Units = strings.Join(names, " ")
		}
		steps, _ := jmap(c)["steps"].([]any)
		for _, st := range steps {
			sid, status, detail := jstr(st, "id"), jstr(st, "status"), jstr(st, "detail")
			if status == "na" && detail == "" {
				continue
			}
			step := componentStep{ID: sid, Label: labels[sid], Status: status, Detail: detail, Icon: icons[status]}
			if step.Label == "" {
				step.Label = sid
			}
			if step.Icon == "" {
				step.Icon = "bi-dash-circle"
			}
			if status == "todo" {
				f, hasFix := fix[sid]
				viaPage := sid == "setup" || (sid == "installed" && (id == "php" || id == "mongodb")) || (sid == "running" && id == "firewall")
				if viaPage && pageURL != "" {
					step.PageURL, step.PageLabel = pageURL, "Open"
					if sid == "setup" {
						step.PageLabel = "Set up"
					}
				} else if !viaPage && hasFix && can(f.perm) {
					step.FixAction, step.FixLabel = "/components/"+id+"/"+f.action, f.label
					if f.action == "ports" {
						var items []string
						if its, ok := jmap(st)["items"].([]any); ok {
							for _, it := range its {
								items = append(items, fmt.Sprint(it))
							}
						}
						step.FixConfirm = "Open " + strings.Join(items, ", ") + " in the firewall for " + card.Name + "? You then have 2 minutes to keep the change."
					}
				}
			}
			card.Steps = append(card.Steps, step)
		}
		if card.Used {
			used = append(used, card)
			if jbool(c, "ready") {
				ready++
			}
		} else {
			unused = append(unused, card)
		}
	}
	return used, unused, ready, len(used), ready == len(used)
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

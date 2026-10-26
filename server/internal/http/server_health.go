package http

// Server → Health (HealthService port): one page answering "is anything
// wrong?", gathering checks that exist elsewhere, each with a link to where it
// is fixed. A part whose data is unavailable says so instead of failing the
// page. It only reads.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

const (
	healthOK      = "ok"
	healthWarn    = "warn"
	healthFail    = "fail"
	healthUnknown = "unknown"
)

type healthCheck struct {
	Status, Label, Detail, Route, URL string
	Word, Icon                        string
}

type healthSection struct {
	ID, Label, Icon, Route  string
	Checks                  []healthCheck
	Worst, WorstWord, State string
}

var healthSections = []struct{ id, label, icon, route string }{
	{"whacp", "WhaCP and the server", "heart-pulse", "/settings"},
	{"resources", "Resources", "speedometer", "/monitoring"},
	{"components", "Components", "puzzle", "/components"},
	{"security", "Security", "shield-check", "/security"},
	{"updates", "Updates", "box-seam", "/packages"},
	{"certificates", "Certificates", "shield-lock", "/ssl"},
	{"activity", "Backups and tasks", "archive", "/backups"},
}

var healthRank = map[string]int{healthFail: 0, healthWarn: 1, healthUnknown: 2, healthOK: 3}
var healthIcon = map[string]string{
	healthFail: "bi-x-circle-fill text-danger", healthWarn: "bi-exclamation-triangle-fill text-warning",
	healthUnknown: "bi-question-circle text-body-secondary", healthOK: "bi-check-circle-fill text-success",
}
var healthWord = map[string]string{healthFail: "Problem", healthWarn: "Attention", healthUnknown: "Not checked", healthOK: "OK"}
var healthState = map[string]string{healthFail: "is-error", healthWarn: "is-warn", healthUnknown: "", healthOK: "is-ok"}

func healthBytes(b float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}

func (s *Server) healthChecks(ctx context.Context, id string) ([]healthCheck, error) {
	call := func(action string, d time.Duration) (any, error) {
		raw, err := s.agent.Call(ctx, action, nil, d)
		if err != nil {
			return nil, err
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		return v, nil
	}
	var out []healthCheck
	switch id {
	case "whacp":
		out = append(out, healthCheck{Status: healthOK, Label: "Panel", Detail: "The panel is running and answering requests."})
		if v, err := call("agent.status", 10*time.Second); err == nil {
			if jbool(v, "write_enabled") {
				out = append(out, healthCheck{Status: healthOK, Label: "Agent", Detail: "Reachable, write mode on."})
			} else {
				out = append(out, healthCheck{Status: healthWarn, Label: "Agent", Detail: "Reachable but in read-only mode: WhaCP can observe the server, not change it.", Route: "/server"})
			}
		} else {
			return nil, err
		}
		out = append(out, healthCheck{Status: healthWarn, Label: "Scheduled tasks", Detail: "The background scheduler (backups, renewals, alerts) is not part of this panel yet: scheduled work does not run on its own.", Route: "/backups"})
	case "resources":
		m, err := call("system.metrics", 30*time.Second)
		if err != nil {
			return nil, err
		}
		diskAlert, _ := s.settings.Int(ctx, "alert_disk_percent", 90)
		memAlert, _ := s.settings.Int(ctx, "alert_memory_percent", 95)
		loadAlert, _ := s.settings.Int(ctx, "alert_load_per_core", 2)
		if ds, ok := jmap(m)["disks"].([]any); ok {
			for _, d := range ds {
				if jstr(d, "used_percent") == "" {
					continue
				}
				pct := jnum(d, "used_percent")
				st := healthOK
				if pct >= float64(max(95, diskAlert)) {
					st = healthFail
				} else if pct >= float64(min(diskAlert, 85)) {
					st = healthWarn
				}
				mount := jstr(d, "mount_point")
				if mount == "" {
					mount = "?"
				}
				out = append(out, healthCheck{Status: st, Label: "Disk " + mount, Detail: fmt.Sprintf("%.0f%% used, %s free", pct, healthBytes(jnum(d, "free_bytes"))), Route: "/monitoring"})
			}
		}
		if jstr(m, "memory", "used_percent") != "" {
			pct := jnum(m, "memory", "used_percent")
			st := healthOK
			if pct >= float64(memAlert) {
				st = healthWarn
			}
			out = append(out, healthCheck{Status: st, Label: "Memory", Detail: fmt.Sprintf("%.0f%% used of %s", pct, healthBytes(jnum(m, "memory", "total_bytes"))), Route: "/monitoring"})
			if total := jnum(m, "memory", "swap_total_bytes"); total > 0 {
				sw := 100 * jnum(m, "memory", "swap_used_bytes") / total
				st := healthOK
				if sw >= 80 {
					st = healthWarn
				}
				out = append(out, healthCheck{Status: st, Label: "Swap", Detail: fmt.Sprintf("%.0f%% used of %s", sw, healthBytes(total)), Route: "/monitoring"})
			}
		}
		if jstr(m, "load", "load1") != "" {
			cores := 1.0
			if info, err := call("system.info", 30*time.Second); err == nil {
				cores = max(1, jnum(info, "system", "cpu", "cores"))
			}
			l1 := jnum(m, "load", "load1")
			st := healthOK
			if l1/cores >= float64(loadAlert) {
				st = healthWarn
			}
			out = append(out, healthCheck{Status: st, Label: "Load", Detail: fmt.Sprintf("%.2f over the last minute on %.0f core(s)", l1, cores), Route: "/monitoring/processes"})
		}
	case "components":
		raw, err := s.agent.Call(ctx, "components.status", nil, 60*time.Second)
		if err != nil {
			return nil, err
		}
		var comps []map[string]any
		_ = json.Unmarshal(raw, &comps)
		saved, _ := s.settings.String(ctx, "components_expected", "")
		expected := map[string]bool{}
		if saved != "" {
			for _, e := range strings.Split(saved, ",") {
				expected[e] = true
			}
		} else {
			for _, id := range componentsInstalled(raw) {
				expected[id] = true
			}
		}
		for _, c := range comps {
			cid, _ := c["id"].(string)
			if !expected[cid] {
				continue
			}
			var todo []string
			down := false
			steps, _ := c["steps"].([]any)
			for _, st := range steps {
				if jstr(st, "status") != "todo" {
					continue
				}
				d := jstr(st, "detail")
				if d == "" {
					d = jstr(st, "id")
				}
				todo = append(todo, d)
				if sid := jstr(st, "id"); sid == "installed" || sid == "running" {
					down = true
				}
			}
			status, detail := healthOK, "Ready"
			if len(todo) > 0 {
				detail = strings.Join(todo, "; ")
				status = healthWarn
				if down {
					status = healthFail
				}
			}
			out = append(out, healthCheck{Status: status, Label: jstr(c, "name"), Detail: detail, Route: "/components"})
		}
	case "security":
		v, err := call("security.audit", 120*time.Second)
		if err != nil {
			return nil, err
		}
		var warns []string
		items, _ := v.([]any)
		for _, c := range items {
			st := jstr(c, "status")
			title := jstr(c, "title")
			if title == "" {
				title = jstr(c, "id")
			}
			if st == "fail" || (st == "warn" && jstr(c, "severity") == "high") {
				status := healthWarn
				if st == "fail" {
					status = healthFail
				}
				out = append(out, healthCheck{Status: status, Label: title, Detail: jstr(c, "detail"), Route: "/security"})
			} else if st == "warn" {
				warns = append(warns, title)
			}
		}
		if len(warns) > 0 {
			more := ""
			if len(warns) > 8 {
				more = ", …"
				warns = warns[:8]
			}
			out = append(out, healthCheck{Status: healthWarn, Label: fmt.Sprintf("%d recommendation(s)", len(warns)), Detail: strings.Join(warns, ", ") + more, Route: "/security"})
		}
		if len(out) == 0 {
			out = append(out, healthCheck{Status: healthOK, Label: "Security audit", Detail: "No problem found.", Route: "/security"})
		}
	case "updates":
		u, err := call("packages.updates", 60*time.Second)
		if err != nil {
			return nil, err
		}
		count, sec := int(jnum(u, "count")), int(jnum(u, "security"))
		st, detail := healthOK, "Up to date"
		if sec > 0 {
			st = healthWarn
		}
		if count > 0 {
			detail = fmt.Sprintf("%d update(s), %d of them security update(s)", count, sec)
		}
		out = append(out, healthCheck{Status: st, Label: "Package updates", Detail: detail, Route: "/packages"})
		if jbool(u, "index_stale") {
			d := "Not refreshed for a while"
			if r := jstr(u, "index_refreshed"); r != "" {
				d += " (last " + r[:min(16, len(r))] + ")"
			}
			out = append(out, healthCheck{Status: healthWarn, Label: "Package index", Detail: d + ": updates may be missing from the list.", Route: "/packages"})
		}
	case "certificates":
		out = append(out, healthCheck{Status: healthUnknown, Label: "Website certificates", Detail: "Certificate expiry is read from the SSL pages.", Route: "/ssl"})
	case "activity":
		serverID := s.currentServerID(nil)
		var failedBackups, failedJobs int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'failed' AND created_at >= ?`,
			serverID, time.Now().UTC().Add(-7*24*time.Hour).Format(time.RFC3339)).Scan(&failedBackups)
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_jobs WHERE server_id = ? AND status = 'failed' AND created_at >= ?`,
			serverID, time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339)).Scan(&failedJobs)
		bs, bd := healthOK, "None"
		if failedBackups > 0 {
			bs, bd = healthWarn, fmt.Sprintf("%d failed", failedBackups)
		}
		js, jd := healthOK, "None"
		if failedJobs > 0 {
			js, jd = healthWarn, fmt.Sprintf("%d failed: see Notifications", failedJobs)
		}
		out = append(out,
			healthCheck{Status: bs, Label: "Failed backups (7 days)", Detail: bd, Route: "/backups"},
			healthCheck{Status: js, Label: "Failed tasks (24 hours)", Detail: jd, Route: "/notifications"})
	}
	return out, nil
}

// serverHealth GET /server/health.
func (s *Server) serverHealth(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	counts := map[string]int{}
	var sections []healthSection
	for _, def := range healthSections {
		checks, err := s.healthChecks(r.Context(), def.id)
		if err != nil {
			checks = []healthCheck{{Status: healthUnknown, Label: "Not checked", Detail: agentMessage(err)}}
		}
		for i := range checks {
			c := &checks[i]
			c.Word, c.Icon = healthWord[c.Status], healthIcon[c.Status]
			if c.Status != healthOK && c.Route != "" {
				c.URL = c.Route
			}
			counts[c.Status]++
		}
		sort.SliceStable(checks, func(a, b int) bool { return healthRank[checks[a].Status] < healthRank[checks[b].Status] })
		sec := healthSection{ID: def.id, Label: def.label, Icon: def.icon, Route: def.route, Checks: checks, Worst: healthOK}
		if len(checks) > 0 {
			sec.Worst = checks[0].Status
		}
		sec.WorstWord, sec.State = healthWord[sec.Worst], healthState[sec.Worst]
		sections = append(sections, sec)
	}
	status := healthOK
	headline := "Everything looks healthy"
	switch {
	case counts[healthFail] > 0:
		status, headline = healthFail, fmt.Sprintf("%d problem(s) need fixing", counts[healthFail])
	case counts[healthWarn] > 0:
		status, headline = healthWarn, fmt.Sprintf("%d item(s) need attention", counts[healthWarn])
	case counts[healthUnknown] > 0:
		status, headline = healthUnknown, "Some parts could not be checked"
	}
	s.render(w, "server_health", map[string]any{
		"CSRF": session.CSRFToken(sess), "User": user, "Title": "Health",
		"Sections": sections, "Status": status, "Headline": headline, "StatusIcon": healthIcon[status],
		"Fail": counts[healthFail], "Warn": counts[healthWarn], "OK": counts[healthOK], "Unknown": counts[healthUnknown],
		"CheckedAt": time.Now().UTC().Format("2006-01-02 15:04"),
	})
}

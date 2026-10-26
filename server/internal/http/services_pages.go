package http

import (
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

type serviceRow struct {
	Unit, Description, State, Badge, Boot, PID, Memory, Since string
	Running, Controllable                                     bool
}

type whacpServiceRow struct {
	Key, Label, Unit, State, Badge, Since, Confirm string
	Restartable                                    bool
}

func serviceBadge(active string) string {
	switch active {
	case "active":
		return "text-bg-success"
	case "failed":
		return "text-bg-danger"
	case "activating", "reloading":
		return "text-bg-warning"
	}
	return "text-bg-secondary"
}

func sinceAgo(v any) string {
	if str := jstr(v); str != "" {
		return timeAgo(str)
	}
	return "—"
}

// servicesIndex GET /services — systemd units monitored by WhaCP, plus
// WhaCP's own services. Mirrors ServicesController::index.
func (s *Server) servicesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Services",
		"AgentError": nil,
		"Success":    "",
		"Error":      "",
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	az := s.authorizer()
	canManage, _ := az.Can(r.Context(), user, "services.manage")
	var rows []serviceRow
	if raw, err := s.agent.Call(r.Context(), "services.status", nil, 30*time.Second); err == nil {
		var svc any
		_ = json.Unmarshal(raw, &svc)
		list, _ := svc.([]any)
		if list == nil {
			list, _ = jmap(svc)["services"].([]any)
		}
		for _, x := range list {
			unit := jstr(x, "unit")
			row := serviceRow{Unit: unit, Description: jstr(x, "description"), State: jstr(x, "active_state") + " (" + jstr(x, "sub_state") + ")",
				Badge: serviceBadge(jstr(x, "active_state")), Boot: jstr(x, "unit_file_state"), PID: "—", Memory: fmtBytes(jnum(x, "memory_bytes")),
				Since: sinceAgo(jmap(x)["active_since"]), Running: jstr(x, "active_state") == "active"}
			if row.Boot == "" {
				row.Boot = "—"
			}
			if pid := jnum(x, "main_pid"); pid > 0 {
				row.PID = jstr(x, "main_pid")
			}
			row.Controllable = canManage && !serviceUnitProtected.MatchString(unit)
			rows = append(rows, row)
		}
	} else {
		data["AgentError"] = err.Error()
	}
	data["Rows"] = rows
	// Older agents may lack whacp.services; an empty list is fine.
	var wrows []whacpServiceRow
	if raw, err := s.agent.Call(r.Context(), "whacp.services", nil, 10*time.Second); err == nil {
		var wh any
		_ = json.Unmarshal(raw, &wh)
		list, _ := jmap(wh)["services"].([]any)
		for _, x := range list {
			key := jstr(x, "key")
			st := jmap(x)["status"]
			confirm := "Restart " + jstr(x, "label") + "?"
			if key == "agent" || key == "panel" {
				confirm += " The panel is unavailable for a few seconds."
			}
			wrows = append(wrows, whacpServiceRow{Key: key, Label: jstr(x, "label"), Unit: jstr(x, "unit"),
				State: jstr(st, "active_state") + " (" + jstr(st, "sub_state") + ")", Badge: serviceBadge(jstr(st, "active_state")),
				Since: sinceAgo(jmap(st)["active_since"]), Restartable: jbool(x, "restartable") && canManage && hosting.IsAll(user), Confirm: confirm})
		}
	}
	data["Whacp"] = wrows
	s.render(w, "server_services", data)
}

// serviceUnitProtected: the agent, panel and SSH cannot be controlled here.
var serviceUnitProtected = regexp.MustCompile(`^(whacp-agent|whacp-panel|ssh|sshd)\.service$`)

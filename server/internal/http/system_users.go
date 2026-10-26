package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
)

// systemUsersIndex GET /system-users — lists system accounts (UIDs from the
// latest discovery inventory), optionally showing system users. Mirrors
// ServerController::systemUsers (system_users, system_users.view).
func (s *Server) systemUsersIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	svc := hosting.NewDiscoveryService(s.db, s.agent, s.settings)
	inv, err := svc.Inventory(r.Context(), s.currentServerID(r), false, &user.ID)
	if err != nil {
		s.renderError(w, http.StatusBadGateway, "Agent unavailable", err.Error())
		return
	}
	// Users are reported under a "users" array in the inventory payload. The
	// exact shape is {uid, name, gid, system, home, shell}; we simply pass the
	// decoded list to the template, filtering non-system accounts per the
	// ?all= query flag.
	showSystem := r.URL.Query().Get("all") == "1"
	var payload struct {
		Users []map[string]any `json:"users"`
	}
	_ = json.Unmarshal(inv.Data, &payload)
	users := []map[string]any{}
	for _, u := range payload.Users {
		sys, _ := u["system"].(bool)
		uid, _ := u["uid"].(float64)
		if !showSystem && sys && int64(uid) != 0 {
			continue
		}
		users = append(users, u)
	}
	type row struct {
		Name, Comment, Home, Shell string
		UID, GID                   int64
		CanLogin                   bool
	}
	rows := make([]row, 0, len(users))
	for _, u := range users {
		sh := jstr(u, "shell")
		rw := row{Name: jstr(u, "name"), Comment: jstr(u, "comment"), Home: jstr(u, "home"), Shell: sh, UID: int64(jnum(u, "uid")), GID: int64(jnum(u, "gid"))}
		if _, ok := u["can_login"]; ok {
			rw.CanLogin = jbool(u, "can_login")
		} else {
			rw.CanLogin = sh != "" && !strings.HasSuffix(sh, "nologin") && !strings.HasSuffix(sh, "/false")
		}
		rows = append(rows, rw)
	}
	pd := s.discoveryPageData(r, *inv)
	pd["Title"] = "System accounts"
	pd["Rows"] = rows
	pd["ShowSystem"] = showSystem
	s.render(w, "server_system_users", pd)
}

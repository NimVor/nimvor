package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// limitsFields are the resource-limit fields reconciled against cgroups on
// the agent (ResourceLimitService::FIELDS — CPU%, memory, tasks, IO read/write).
var limitsFields = []string{"cpu_percent", "memory_mb", "tasks_max", "io_read_mbps", "io_write_mbps"}

// limitsForUser returns the effective cgroup limits for a user (its plan's
// values capped by any reseller parent's plan; administrators have no
// limits). Mirrors ResourceLimitService::forUser.
func (s *Server) limitsForUser(r *http.Request, userID int64) (map[string]int, error) {
	cols := make([]string, 0, len(limitsFields)*2)
	for _, f := range limitsFields {
		cols = append(cols, fmt.Sprintf("COALESCE(p.%s,0) AS %s, COALESCE(rp.%s,0) AS r_%s", f, f, f, f))
	}
	q := `SELECT ro.scope, ` + joinStrings(cols, ", ") + `
           FROM users u JOIN roles ro ON ro.id = u.role_id
           LEFT JOIN plans p ON p.id = u.plan_id
           LEFT JOIN users r ON r.id = u.parent_id
           LEFT JOIN plans rp ON rp.id = r.plan_id
           WHERE u.id = ?`
	row := s.db.QueryRowContext(r.Context(), q, userID)
	outs := make([]any, 0, 1+len(limitsFields)*2)
	var scope string
	outs = append(outs, &scope)
	vals := make([]int, len(limitsFields))
	rvals := make([]int, len(limitsFields))
	for i := range limitsFields {
		outs = append(outs, &vals[i], &rvals[i])
	}
	out := make(map[string]int, len(limitsFields))
	for _, f := range limitsFields {
		out[f] = 0
	}
	if err := row.Scan(outs...); err != nil {
		return out, err
	}
	if scope == "all" {
		return out, nil
	}
	for i, f := range limitsFields {
		own, cap := vals[i], rvals[i]
		if cap > 0 && (own == 0 || own > cap) {
			out[f] = cap
		} else {
			out[f] = own
		}
	}
	return out, nil
}

func joinStrings(ss []string, sep string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

// limitsDesired returns the acct<owner_id> → {owner_id, limits, users} map
// the agent should apply (ResourceLimitService::desired). A system user
// shared by several accounts belongs to the owner of its oldest website;
// accounts with zero limits across all fields are omitted.
func (s *Server) limitsDesired(r *http.Request) (map[string]map[string]any, error) {
	sid := s.currentServerID(r)
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT w.system_user, MIN(w.id) AS first_id
		 FROM websites w WHERE w.server_id = ? AND w.system_user <> ''
		 GROUP BY w.system_user`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type su struct {
		user    string
		ownerID int64
	}
	var sus []su
	for rows.Next() {
		var u string
		var firstID int64
		if err := rows.Scan(&u, &firstID); err != nil {
			continue
		}
		var owner int64
		if err := s.db.QueryRowContext(r.Context(), "SELECT owner_id FROM websites WHERE id = ?", firstID).Scan(&owner); err != nil || owner <= 0 {
			continue
		}
		sus = append(sus, su{user: u, ownerID: owner})
	}
	out := make(map[string]map[string]any)
	for _, it := range sus {
		lim, err := s.limitsForUser(r, it.ownerID)
		if err != nil {
			return nil, err
		}
		sum := 0
		for _, v := range lim {
			sum += v
		}
		if sum == 0 {
			continue
		}
		key := fmt.Sprintf("acct%d", it.ownerID)
		m, ok := out[key]
		if !ok {
			m = map[string]any{"owner_id": it.ownerID, "limits": lim, "users": []string{}}
			out[key] = m
		}
		m["users"] = append(m["users"].([]string), it.user)
	}
	for _, m := range out {
		us := m["users"].([]string)
		sort.Strings(us)
		m["users"] = us
	}
	return out, nil
}

// limitsIndex GET /limits — shows cgroup-style resource limits per account.
// The PHP page calls ResourceLimitService::status / ::desired and
// DiskQuotaService::status; those services are not ported to Go yet, so
// this handler returns a friendly page indicating the feature is not yet
// available while the route itself is registered so the nav link works.
// Mirrors LimitsController::index (limits, server.view).
func (s *Server) limitsIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Resource limits",
		"Success":    "",
		"Error":      "",
		"Status":     nil,
		"AgentError": nil,
		"NotReady":   true,
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	// Best-effort: try calling resource-limits.status on the agent; if it
	// doesn't exist the page will fall back to the "not yet ported" card.
	if raw, err := s.agent.Call(r.Context(), "resource_limits.status", nil, 60*time.Second); err == nil {
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		data["Status"] = parsed
		data["NotReady"] = false
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "limits_index", data)
}

// limitsApply POST /limits/apply — reconciles the server's cgroup slices
// with what every account's plan asks for by calling agent `limits.set`
// for each account that differs from the currently applied set. Mirrors
// ResourceLimitService::reconcile / LimitsController::apply
// (limits.apply, server.manage).
func (s *Server) limitsApply(w http.ResponseWriter, r *http.Request) {
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
	if !hosting.IsAll(user) {
		sess.Flash("error", "Resource limits are managed by server administrators.")
		http.Redirect(w, r, "/limits", http.StatusSeeOther)
		return
	}
	// Fetch currently-applied state (agent returns {accounts: {acctN: {limits, users}}}).
	raw, err := s.agent.Call(r.Context(), "limits.status", nil, 30*time.Second)
	applied := map[string]any{}
	if err == nil {
		var st map[string]any
		if json.Unmarshal(raw, &st) == nil {
			if a, ok := st["accounts"].(map[string]any); ok {
				applied = a
			}
		}
	}
	want, err := s.limitsDesired(r)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/limits", http.StatusSeeOther)
		return
	}
	// Union of keys present in either applied or desired.
	keys := map[string]bool{}
	for k := range applied {
		keys[k] = true
	}
	for k := range want {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	sid := s.currentServerID(r)
	uid := user.ID
	sites := hosting.NewWebsiteRepository(s.db)
	ws := s.websiteService(r, user)
	cron := hosting.NewCronService(s.db, s.agent, hosting.NewPlanLimits(s.db), s.websiteAuditHook(r, user))
	workers := hosting.NewWorkerService(s.db, s.agent, hosting.NewPlanLimits(s.db), s.websiteAuditHook(r, user), sid)

	lines := []string{}
	zeroLimits := map[string]int{}
	for _, f := range limitsFields {
		zeroLimits[f] = 0
	}
	for _, acct := range sorted {
		w, haveW := want[acct]
		curI, haveC := applied[acct]
		var wantLimits map[string]int
		var wantUsers []string
		if haveW {
			wantLimits = w["limits"].(map[string]int)
			wantUsers, _ = w["users"].([]string)
		} else {
			wantLimits = zeroLimits
		}
		curUsers := []string{}
		curLimits := map[string]int{}
		if haveC {
			if cm, ok := curI.(map[string]any); ok {
				if cu, ok := cm["users"].([]any); ok {
					for _, u := range cu {
						if us, ok := u.(string); ok {
							curUsers = append(curUsers, us)
						}
					}
				}
				if cl, ok := cm["limits"].(map[string]any); ok {
					for _, f := range limitsFields {
						if n, ok := cl[f].(float64); ok {
							curLimits[f] = int(n)
						}
					}
				}
			}
		}
		sortedWant := append([]string{}, wantUsers...)
		sort.Strings(sortedWant)
		sortedCur := append([]string{}, curUsers...)
		sort.Strings(sortedCur)
		// Compare limits over the FIELDS keys.
		same := haveC && haveW && sameStrings(sortedCur, sortedWant)
		if same {
			for _, f := range limitsFields {
				if curLimits[f] != wantLimits[f] {
					same = false
					break
				}
			}
		}
		if same || (!haveC && !haveW) {
			continue
		}
		// Users being removed are sent so their logins lose limits too.
		callUsers := unionStrings(sortedWant, sortedCur)
		if !haveW {
			callUsers = sortedCur
		}
		callLimits := wantLimits
		if !haveW {
			callLimits = zeroLimits
		}
		params := map[string]any{"account": acct, "users": callUsers}
		for _, f := range limitsFields {
			params[f] = callLimits[f]
		}
		if _, err := s.agent.Call(r.Context(), "limits.set", params, 60*time.Second); err != nil {
			lines = append(lines, acct+": failed: "+err.Error())
			continue
		}
		appliedUsers := callUsers
		if haveW {
			appliedUsers = sortedWant
		}
		s.audit.Record(r.Context(), audit.Entry{
			Action: "limits.apply", TargetType: "server", TargetID: "limits",
			Result: audit.Success, Details: map[string]any{"account": acct, "users": appliedUsers, "limits": callLimits},
			UserID: &uid, Username: user.Username,
		})
		if !haveW {
			lines = append(lines, acct+": limits removed")
		} else {
			parts := []string{}
			for _, f := range limitsFields {
				parts = append(parts, f+"="+strconv.Itoa(callLimits[f]))
			}
			lines = append(lines, acct+": limits applied ("+strings.Join(parts, ", ")+")")
		}
		// Re-apply websites/cron/workers for each system user so they move in or out of the slice.
		applyUsers := sortedWant
		if !haveW {
			applyUsers = sortedCur
		}
		for _, u := range applyUsers {
			srows, qerr := s.db.QueryContext(r.Context(),
				"SELECT id FROM websites WHERE server_id = ? AND system_user = ?", sid, u)
			if qerr != nil {
				continue
			}
			for srows.Next() {
				var wid int64
				if srows.Scan(&wid) != nil {
					continue
				}
				site, ferr := sites.Find(r.Context(), sid, wid, nil)
				if ferr != nil || site == nil {
					continue
				}
				if err := ws.Reapply(r.Context(), site); err != nil {
					lines = append(lines, site.Domain+": not re-applied: "+err.Error())
				}
				_ = cron.Apply(r.Context(), site)
				wks, err := workers.ForSite(r.Context(), site.ID)
				if err == nil {
					for _, wk := range wks {
						_ = workers.Apply(r.Context(), site, wk)
					}
				}
			}
			srows.Close()
		}
	}
	if len(lines) == 0 {
		sess.Flash("success", "Everything already matches the plans.")
	} else {
		sess.Flash("success", strings.Join(lines, " · "))
	}
	http.Redirect(w, r, "/limits", http.StatusSeeOther)
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func unionStrings(a, b []string) []string {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		set[x] = true
	}
	out := make([]string, 0, len(set))
	for x := range set {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

// limitsEnableQuota POST /limits/quota/enable — kicks off a background job
// that turns on disk quotas for websites' file systems via the agent's
// `quota.enable` action. Mirrors LimitsController::enableQuota
// (limits.quota.enable, server.manage).
func (s *Server) limitsEnableQuota(w http.ResponseWriter, r *http.Request) {
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
	// quota.enable is a privileged server-management action.
	if !s.can(r, "server.manage") {
		sess.Flash("error", "Disk quotas are managed by server administrators.")
		http.Redirect(w, r, "/limits#disk-quotas", http.StatusSeeOther)
		return
	}
	raw, err := s.agent.Call(r.Context(), "quota.enable", map[string]any{}, 60*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/limits#disk-quotas", http.StatusSeeOther)
		return
	}
	var parsed struct {
		JobID string `json:"job_id"`
	}
	jobID := ""
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.JobID != "" {
		jobID = parsed.JobID
	}
	uid := user.ID
	if jobID != "" {
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, "Turn on disk quotas", "server", itoa(s.currentServerID(r)), &uid)
		sess.Flash("job", jobID)
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "quota.enable", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, Details: map[string]any{"job_id": jobID},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Turning disk quotas on. Follow the log below; the plans' disk space is enforced once they are on.")
	http.Redirect(w, r, "/limits#disk-quotas", http.StatusSeeOther)
}

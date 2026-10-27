package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// FfmpegService keeps the FFmpeg group's members in line with the plans
// (FfmpegService): an account may run FFmpeg when its plan allows it (and its
// reseller's does), administrators always may; suspended accounts may not.
type FfmpegService struct {
	db       *sql.DB
	serverID int64
	agent    AgentCaller
	websites *WebsiteService
	audit    Auditor
}

// NewFfmpegService builds the service.
func NewFfmpegService(db *sql.DB, serverID int64, agent AgentCaller, websites *WebsiteService, audit Auditor) *FfmpegService {
	return &FfmpegService{db: db, serverID: serverID, agent: agent, websites: websites, audit: audit}
}

const suspendedSQL = `(u.suspended_at IS NOT NULL OR EXISTS (SELECT 1 FROM users sp WHERE sp.id = u.parent_id AND sp.suspended_at IS NOT NULL))`

// mayRunFfmpeg is the owners' condition shared by both queries.
const mayRunFfmpeg = `NOT ` + suspendedSQL + ` AND (ro.scope = 'all' OR (COALESCE(p.ffmpeg, 0) = 1 AND (r.id IS NULL OR rr.scope = 'all' OR COALESCE(rp.ffmpeg, 0) = 1)))`

const ffmpegJoins = `JOIN roles ro ON ro.id = u.role_id LEFT JOIN plans p ON p.id = u.plan_id LEFT JOIN users r ON r.id = u.parent_id
	LEFT JOIN roles rr ON rr.id = r.role_id LEFT JOIN plans rp ON rp.id = r.plan_id`

// AllowedUsers are the system accounts allowed on this server. An account
// shared with a website whose owner may not use FFmpeg is left out: access
// follows the most restricted owner.
func (s *FfmpegService) AllowedUsers(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT w.system_user FROM websites w JOIN users u ON u.id = w.owner_id `+ffmpegJoins+`
		 WHERE w.server_id = ? AND w.system_user <> '' AND `+mayRunFfmpeg+` ORDER BY w.system_user`, s.serverID)
	if err != nil {
		return nil, err
	}
	var users []string
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			users = append(users, u)
		}
	}
	rows.Close()
	if len(users) == 0 {
		return []string{}, nil
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(users)), ",")
	args := []any{s.serverID}
	for _, u := range users {
		args = append(args, u)
	}
	drows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT w.system_user FROM websites w WHERE w.server_id = ? AND w.system_user IN (`+marks+`)
		 AND (w.owner_id IS NULL OR w.owner_id NOT IN (SELECT u.id FROM users u `+ffmpegJoins+` WHERE `+mayRunFfmpeg+`))`, args...)
	if err != nil {
		return nil, err
	}
	denied := map[string]bool{}
	for drows.Next() {
		var u string
		if drows.Scan(&u) == nil {
			denied[u] = true
		}
	}
	drows.Close()
	out := []string{}
	for _, u := range users {
		if !denied[u] {
			out = append(out, u)
		}
	}
	return out, nil
}

// Reconcile brings the group's members in line with the plans (when FFmpeg is
// installed). Accounts that lost access get their PHP reloaded, so no running
// PHP process keeps the group.
func (s *FfmpegService) Reconcile(ctx context.Context, force bool) ([]string, error) {
	raw, err := s.agent.Call(ctx, "ffmpeg.status", map[string]any{}, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var st struct {
		Installed  bool     `json:"installed"`
		Restricted bool     `json:"restricted"`
		Members    []string `json:"members"`
	}
	_ = json.Unmarshal(raw, &st)
	if !st.Installed {
		return nil, nil
	}
	users, err := s.AllowedUsers(ctx)
	if err != nil {
		return nil, err
	}
	members := append([]string{}, st.Members...)
	sort.Strings(members)
	if !force && st.Restricted && strings.Join(members, ",") == strings.Join(users, ",") {
		return nil, nil
	}
	rraw, err := s.agent.Call(ctx, "ffmpeg.access", map[string]any{"users": users}, 120*time.Second)
	if err != nil {
		return nil, err
	}
	var res struct {
		Added   []string `json:"added"`
		Removed []string `json:"removed"`
	}
	_ = json.Unmarshal(rraw, &res)
	var log []string
	if len(res.Removed) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(res.Removed)), ",")
		args := []any{s.serverID}
		for _, u := range res.Removed {
			args = append(args, u)
		}
		rows, qerr := s.db.QueryContext(ctx, `SELECT id, php_version FROM websites WHERE server_id = ? AND php_version <> '' AND system_user IN (`+marks+`) ORDER BY id`, args...)
		if qerr == nil {
			type site struct {
				id int64
				v  string
			}
			seen := map[string]bool{}
			var first []site
			for rows.Next() {
				var x site
				if rows.Scan(&x.id, &x.v) == nil && !seen[x.v] {
					seen[x.v] = true
					first = append(first, x)
				}
			}
			rows.Close()
			for _, x := range first {
				w, ferr := NewWebsiteRepository(s.db).FindFull(ctx, s.serverID, x.id, nil)
				if ferr != nil || w == nil {
					continue
				}
				if rerr := s.websites.RestartPhp(ctx, w); rerr != nil {
					log = append(log, fmt.Sprintf("FFmpeg: PHP %s was not reloaded: %s", x.v, agentMessage(rerr)))
				}
			}
		}
	}
	if len(res.Added) > 0 || len(res.Removed) > 0 {
		if s.audit != nil {
			s.audit(ctx, "ffmpeg.access", "server", "ffmpeg", "success", map[string]any{"added": res.Added, "removed": res.Removed})
		}
		log = append(log, fmt.Sprintf("FFmpeg access: %d account(s) added, %d removed.", len(res.Added), len(res.Removed)))
	}
	return log, nil
}

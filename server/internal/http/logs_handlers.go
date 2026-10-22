package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// This file ports LogsController: the per-site log viewer (site), the JSON
// tail endpoint (tail) and the file download (download). Website logs are
// visible to anyone with access to the website; server logs and the panel log
// only to roles with server-wide scope. The viewer UI is the reused
// /assets/js/logs.js, driven by the data attributes below.

// logSource is one entry of the viewer's source list (the agent's Source).
type logSource struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Group    string `json:"group"`
	Kind     string `json:"kind"` // file or journal
	Size     int64  `json:"size"`
	Exists   bool   `json:"exists"`
	SizeText string // "(12.5 MB)" for existing files, "" otherwise (view only)
}

// logGroup is one <optgroup> of the source list, pre-grouped in the order
// PHP renders it (the agent's sorted groups, then "Workers" appended).
type logGroup struct {
	Name  string
	Items []logSource
}

var (
	logSiteSourceRe = regexp.MustCompile(`^site:([a-z0-9.-]+):`)
	logWorkerRe     = regexp.MustCompile(`^journal:whacp-worker-([a-z0-9-]+)\.service$`)
)

// logSourceAllowed mirrors LogsController::allowed: a site:* source belongs
// to whoever may see that website, a worker-journal source to the worker's
// website, and everything else (server logs, the panel log) to staff only.
func (s *Server) logSourceAllowed(r *http.Request, user *identity.User, source string) bool {
	ctx := r.Context()
	repo := hosting.NewWebsiteRepository(s.db)
	if m := logSiteSourceRe.FindStringSubmatch(source); m != nil {
		if user != nil && user.RoleScope == "all" {
			return true
		}
		site, err := repo.FindByDomain(ctx, s.currentServerID(r), m[1])
		if err != nil {
			return false
		}
		_, err = repo.Find(ctx, s.currentServerID(r), site.ID, user)
		return err == nil
	}
	if m := logWorkerRe.FindStringSubmatch(source); m != nil {
		if user != nil && user.RoleScope == "all" {
			return true
		}
		var siteID int64
		err := s.db.QueryRowContext(ctx,
			"SELECT website_id FROM workers WHERE server_id = ? AND slug = ?",
			s.currentServerID(r), m[1]).Scan(&siteID)
		if err != nil {
			return false
		}
		_, err = repo.Find(ctx, s.currentServerID(r), siteID, user)
		return err == nil
	}
	return user != nil && user.RoleScope == "all"
}

// siteLogs renders the per-site log viewer (LogsController::site): the
// website's log sources from the agent plus one entry per worker.
func (s *Server) siteLogs(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	ctx := r.Context()
	var sources []logSource
	var agentErr *agent.Error
	raw, err := s.agent.Call(ctx, "logs.sources", map[string]any{"domain": site.Domain})
	if err != nil {
		if ae := &agent.Error{}; errors.As(err, &ae) {
			agentErr = ae
		} else {
			s.log.Error("logs.sources", "site", site.ID, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else if err := json.Unmarshal(raw, &sources); err != nil {
		s.log.Error("logs.sources", "site", site.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The workers run as journal units; list them after the agent's sources.
	rows, err := s.db.QueryContext(ctx, "SELECT name, slug FROM workers WHERE website_id = ? ORDER BY id", site.ID)
	if err != nil {
		s.log.Error("list workers for logs", "site", site.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var name, slug string
		if err := rows.Scan(&name, &slug); err != nil {
			s.log.Error("list workers for logs", "site", site.ID, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		sources = append(sources, logSource{
			ID: "journal:whacp-worker-" + slug + ".service", Label: "Worker: " + name,
			Group: "Workers", Kind: "journal", Exists: true,
		})
	}
	groups := groupLogSources(sources)
	s.render(w, "site_logs", map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sessionFrom(r.Context())),
		"Site":          site,
		"Groups":        groups,
		"AgentError":    agentErr,
		"InitialSource": r.URL.Query().Get("source"),
	})
}

// groupLogSources sizes existing files and folds the list into <optgroup>s
// (PHP renders a new optgroup whenever the group changes).
func groupLogSources(sources []logSource) []logGroup {
	groups := []logGroup{}
	for _, src := range sources {
		if src.Kind == "file" && src.Exists {
			src.SizeText = " (" + formatBytes(src.Size) + ")"
		}
		if n := len(groups); n > 0 && groups[n-1].Name == src.Group {
			groups[n-1].Items = append(groups[n-1].Items, src)
			continue
		}
		groups = append(groups, logGroup{Name: src.Group, Items: []logSource{src}})
	}
	return groups
}

// logCategories is the "Logs → <kind>" sub-menu (LogsController::CATEGORIES),
// in menu order.
var logCategories = [][2]string{
	{"web", "Web server logs"}, {"php", "PHP-FPM logs"}, {"mail", "Mail server logs"},
	{"database", "Database server logs"}, {"security", "Security logs"},
	{"ssl", "SSL and Let's Encrypt logs"}, {"system", "System logs"},
	{"services", "Service logs"}, {"whacp", "WhaCP logs"},
}

func logCategoryTitle(key string) (string, bool) {
	for _, c := range logCategories {
		if c[0] == key {
			return c[1], true
		}
	}
	return "", false
}

var (
	logPHPUnitRe   = regexp.MustCompile(`^php.*fpm`)
	logMailUnitRe  = regexp.MustCompile(`^(postfix|dovecot|rspamd|whacp-opendkim|whacp-greylist|whacp-mailrate)`)
	logDBUnitRe    = regexp.MustCompile(`^(mariadb|mysql)`)
	logSecUnitRe   = regexp.MustCompile(`^(fail2ban|sshd?|ssh)\.`)
)

// logCategory is the kind a log source belongs to (LogsController::category).
func logCategory(id string) string {
	unit := ""
	if strings.HasPrefix(id, "journal:") {
		unit = id[len("journal:"):]
	}
	switch {
	case strings.HasPrefix(id, "nginx:") || unit == "nginx.service":
		return "web"
	case strings.HasPrefix(id, "php:") || logPHPUnitRe.MatchString(unit):
		return "php"
	case id == "system:mail" || logMailUnitRe.MatchString(unit):
		return "mail"
	case id == "system:mysql" || logDBUnitRe.MatchString(unit):
		return "database"
	case id == "system:auth" || id == "system:fail2ban" || id == "system:ufw" || logSecUnitRe.MatchString(unit):
		return "security"
	case id == "system:letsencrypt" || unit == "certbot.service":
		return "ssl"
	case id == "system:syslog":
		return "system"
	case id == "panel" || unit == "whacp-agent.service" || strings.HasPrefix(unit, "whacp-panel"):
		return "whacp"
	default:
		return "services"
	}
}

// logsIndex renders the global log list (LogsController::index). Server logs
// are staff-only — a non-staff user gets the 403 page, not an empty list.
// The sources are the agent's server-wide list plus the panel log, filtered
// by ?view=<kind>.
func (s *Server) logsIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if user.RoleScope != "all" {
		renderStatus(w, s, "error", map[string]any{
			"Title":   "Access denied",
			"Message": "Server logs are only available to administrators. Open a website to see its logs.",
		}, http.StatusForbidden)
		return
	}
	ctx := r.Context()
	var sources []logSource
	var agentErr *agent.Error
	raw, err := s.agent.Call(ctx, "logs.sources", nil)
	if err != nil {
		if ae := &agent.Error{}; errors.As(err, &ae) {
			agentErr = ae
		} else {
			s.log.Error("logs.sources", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else if err := json.Unmarshal(raw, &sources); err != nil {
		s.log.Error("logs.sources", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sources = append(sources, logSource{ID: "panel", Label: "WhaCP panel log", Group: "WhaCP", Kind: "file"})
	view := r.URL.Query().Get("view")
	title := "Logs"
	if _, ok := logCategoryTitle(view); ok {
		out := []logSource{}
		for _, src := range sources {
			if logCategory(src.ID) == view {
				out = append(out, src)
			}
		}
		sources = out
		title, _ = logCategoryTitle(view)
	}
	s.render(w, "logs_index", map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sessionFrom(r.Context())),
		"Title":         title,
		"View":          view,
		"Categories":    logCategories,
		"Groups":        groupLogSources(sources),
		"AgentError":    agentErr,
		"InitialSource": r.URL.Query().Get("source"),
	})
}

// logsTail serves the log tail endpoint (LogsController::tail): one of the
// user's own site logs, one of their workers' journals, or — staff only —
// any server log or the panel log.
func (s *Server) logsTail(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	q := r.URL.Query()
	source := q.Get("source")
	if !s.logSourceAllowed(r, user, source) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"success": false, "error": map[string]string{"message": "You may not read this log."},
		})
		return
	}
	lines := 200
	if raw, present := q["lines"]; present {
		lines, _ = strconv.Atoi(raw[0])
	}
	if lines < 10 {
		lines = 10
	}
	if lines > 5000 {
		lines = 5000
	}
	filter := q.Get("filter")
	if len(filter) > 200 {
		filter = filter[:200]
	}
	follow := q.Get("follow") == "1"
	offset := int64(0)
	if raw, present := q["offset"]; present {
		offset, _ = strconv.ParseInt(raw[0], 10, 64)
	}
	if source == "panel" {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": s.panelTail(lines, filter, follow, offset)})
		return
	}
	raw, err := s.agent.Call(r.Context(), "logs.tail", map[string]any{
		"source": source, "lines": lines, "filter": filter,
		"follow": follow, "offset": offset, "cursor": q.Get("cursor"),
	})
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		s.log.Error("logs.tail", "source", source, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The agent's data payload is already the JSON object the viewer wants.
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(append(append([]byte(`{"success":true,"data":`), raw...), '}'))
}

// logChunk is one chunk of logs.download (base64 file slice).
type logChunk struct {
	Data string `json:"data"`
	Size int64  `json:"size"`
	EOF  bool   `json:"eof"`
	Name string `json:"name"`
}

func (s *Server) logChunk(r *http.Request, source string, offset int64) (*logChunk, error) {
	raw, err := s.agent.Call(r.Context(), "logs.download", map[string]any{"source": source, "offset": offset})
	if err != nil {
		return nil, err
	}
	var c logChunk
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// logsDownload streams a downloadable log file (LogsController::download).
// The panel log and journal sources cannot be downloaded (the viewer hides
// the button for them too).
func (s *Server) logsDownload(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	source := r.URL.Query().Get("source")
	if source == "panel" || !s.logSourceAllowed(r, user, source) {
		http.NotFound(w, r)
		return
	}
	first, err := s.logChunk(r, source, 0)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			http.Error(w, "The log cannot be downloaded: "+ae.Message, http.StatusNotFound)
			return
		}
		s.log.Error("logs.download", "source", source, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeLogFilename(first.Name)+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	flusher, _ := w.(http.Flusher)
	offset := int64(0)
	for {
		b, _ := base64.StdEncoding.DecodeString(first.Data)
		if _, err := w.Write(b); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		offset += int64(len(b))
		if first.EOF || len(b) == 0 {
			return
		}
		first, err = s.logChunk(r, source, offset)
		if err != nil {
			// Mid-stream failure: stop; the partial file is what the PHP
			// stream does too (the loop ends on an empty chunk).
			return
		}
	}
}

// panelTail reads the panel's own log file (LogsController::panelTail).
// Missing file: no lines, as in PHP.
func (s *Server) panelTail(n int, filter string, follow bool, offset int64) map[string]any {
	empty := func() map[string]any { return map[string]any{"lines": []string{}, "offset": 0} }
	file := s.cfg.LogFile
	fi, err := os.Stat(file)
	if err != nil || fi.Size() == 0 {
		return empty()
	}
	f, err := os.Open(file)
	if err != nil {
		return empty()
	}
	defer f.Close()
	size := fi.Size()
	if follow && offset >= 0 && offset <= size {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return empty()
		}
		b, _ := io.ReadAll(io.LimitReader(f, 1<<20))
		data := ""
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			data = string(b[:i+1])
		}
		lines := []string{}
		if data != "" {
			lines = strings.Split(strings.TrimRight(data, "\n"), "\n")
		}
		return map[string]any{"lines": filterLogLines(lines, filter), "offset": offset + int64(len(data))}
	}
	if _, err := f.Seek(max(0, size-(4<<20)), io.SeekStart); err != nil {
		return empty()
	}
	b, _ := io.ReadAll(f)
	lines := []string{}
	if t := strings.TrimRight(string(b), "\n"); t != "" {
		lines = strings.Split(t, "\n")
	}
	lines = filterLogLines(lines, filter)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return map[string]any{"lines": lines, "offset": size}
}

// filterLogLines keeps lines containing the text (case-insensitive).
func filterLogLines(lines []string, filter string) []string {
	if filter == "" {
		return lines
	}
	f := strings.ToLower(filter)
	out := []string{}
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l), f) {
			out = append(out, l)
		}
	}
	return out
}

// safeLogFilename is PHP's preg_replace('/[^A-Za-z0-9._-]/','_') with the
// log.txt fallback.
func safeLogFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "log.txt"
	}
	return b.String()
}

// formatBytes mirrors helpers.php's format_bytes (1 decimal, 1024 steps).
func formatBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	v := float64(n)
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[i]
}

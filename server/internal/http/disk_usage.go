package http

// This file ports the website disk-usage pages (DiskUsageController): the
// per-folder view (sites.disk, files.manage) and the scan start (
// sites.disk.scan, files.manage). The agent measures in a background job;
// the latest result per folder is kept in disk_usage_scans and settled
// from the job's status on each page view. The page is static (a reload
// shows the finished scan); the "Measuring…" button opens the job via the
// /jobs viewer, whose modal UI rides with the Go shell work.

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// diskUsageRow is one measured folder of the view.
type diskUsageRow struct {
	Name  string
	Kind  string // dir, files or mail
	Bytes string
	Files string
	Share int
	Href  string // dir rows: the subfolder
}

// diskUsageCrumb is one breadcrumb (the domain is the implicit first item).
type diskUsageCrumb struct {
	Name   string
	Path   string
	Active bool
}

// diskUsageView is the rendered page data (the PHP view's derived values).
type diskUsageView struct {
	Path      string
	Sort      string
	Crumbs    []diskUsageCrumb
	Measured  string // time_ago of the scan ("" when none)
	Running   string // the running scan's job id ("" when none)
	HasScan   bool
	Status    string // done or failed
	Error     string
	Entries   []diskUsageRow
	Total     string
	Partial   bool
	Truncated int64
	LinkName  string
	LinkSize  string
	LinkFiles string
}

// diskUsageShow renders the disk-usage page (DiskUsageController::show,
// sites.disk, files.manage).
func (s *Server) diskUsageShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	// A bad ?path= is ignored (the PHP catches the exception and uses "").
	path, _ := hosting.DiskUsageCleanPath(r.URL.Query().Get("path"))
	sortKey := r.URL.Query().Get("sort")
	if sortKey != "name" && sortKey != "size" && sortKey != "files" {
		sortKey = "size"
	}
	svc := hosting.NewDiskUsageService(s.db)
	if err := svc.Refresh(r.Context(), s.currentServerID(r), site.ID, s.agent); err != nil {
		s.log.Error("disk usage refresh", "site", site.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	running, err := svc.Running(r.Context(), site.ID, path)
	if err != nil {
		s.log.Error("disk usage running", "site", site.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	scan, err := svc.Latest(r.Context(), site.ID, path)
	if err != nil {
		s.log.Error("disk usage latest", "site", site.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	v := buildDiskUsageView(site, path, sortKey, running, scan)
	sess := sessionFrom(r.Context())
	jobMsg, _ := sess.PullFlash("job")
	okMsg, _ := sess.PullFlash("success")
	errMsg, _ := sess.PullFlash("error")
	s.render(w, "disk_usage", map[string]any{
		"User":    user,
		"CSRF":    session.CSRFToken(sess),
		"Site":    site,
		"View":    v,
		"Job":     jobMsg,
		"Success": okMsg,
		"Error":   errMsg,
	})
}

// buildDiskUsageView mirrors the PHP view's derived values (the sort, the
// breadcrumb trail, the share bars, the measured time).
func buildDiskUsageView(site *hosting.Website, path, sortKey string, running, scan *hosting.DiskUsageScan) *diskUsageView {
	v := &diskUsageView{Path: path, Sort: sortKey}
	base := fmt.Sprintf("/sites/%d/disk-usage", site.ID)
	q := url.Values{}
	if path != "" {
		q.Set("path", path)
	}
	withSort := func(sortParam string) string {
		if sortParam != "" {
			q.Set("sort", sortParam)
		} else {
			q.Del("sort")
		}
		s := q.Encode()
		if s == "" {
			return base
		}
		return base + "?" + s
	}
	v.LinkName = withSort("name")
	v.LinkSize = withSort("")
	v.LinkFiles = withSort("files")
	acc := ""
	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue
		}
		acc = strings.TrimLeft(acc+"/"+part, "/")
		v.Crumbs = append(v.Crumbs, diskUsageCrumb{Name: part, Path: acc})
	}
	if n := len(v.Crumbs); n > 0 {
		v.Crumbs[n-1].Active = true
	}
	if running != nil {
		v.Running = running.JobID
	}
	if scan == nil {
		return v
	}
	v.HasScan = true
	v.Status = scan.Status
	v.Error = scan.Error
	if scan.FinishedAt != nil {
		v.Measured = timeAgo(*scan.FinishedAt)
	}
	v.Total = formatBytes(scan.Result.Bytes)
	v.Partial = scan.Result.Partial
	v.Truncated = scan.Result.Truncated
	entries := scan.Result.Entries
	switch sortKey {
	case "name":
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	case "files":
		sort.Slice(entries, func(i, j int) bool { return entries[i].Files > entries[j].Files })
	default:
		sort.Slice(entries, func(i, j int) bool { return entries[i].Bytes > entries[j].Bytes })
	}
	total := max(1, scan.Result.Bytes)
	for _, e := range entries {
		row := diskUsageRow{
			Name:  e.Name,
			Kind:  e.Kind,
			Bytes: formatBytes(e.Bytes),
			Files: thousands(float64(e.Files)),
			Share: int(math.Round(100 * float64(e.Bytes) / float64(total))),
		}
		if e.Kind == "dir" {
			sub := strings.TrimLeft(path+"/"+e.Name, "/")
			row.Href = base + "?" + url.Values{"path": {sub}}.Encode()
		}
		v.Entries = append(v.Entries, row)
	}
	return v
}

// diskUsageScan starts measuring a folder (DiskUsageController::scan,
// sites.disk.scan, files.manage).
func (s *Server) diskUsageScan(w http.ResponseWriter, r *http.Request) {
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
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		// The PHP redirects to the website list with the error.
		sess.Flash("error", "Website not found.")
		http.Redirect(w, r, "/websites", http.StatusSeeOther)
		return
	}
	path := r.FormValue("path")
	to := fmt.Sprintf("/sites/%d/disk-usage", site.ID)
	if path != "" {
		to += "?path=" + url.QueryEscape(path)
	}
	job, err := hosting.NewDiskUsageService(s.db).Start(r.Context(), s.currentServerID(r), site.ID, site.Domain, path, s.agent)
	if err != nil {
		var ae *agent.Error
		switch {
		case errors.Is(err, hosting.ErrDiskUsageBadPath),
			errors.Is(err, hosting.ErrDiskUsageLongPath),
			errors.Is(err, hosting.ErrDiskUsageRunning):
			sess.Flash("error", err.Error())
		case errors.As(err, &ae):
			sess.Flash("error", ae.Message)
		default:
			s.log.Error("disk usage scan", "site", site.ID, "error", err)
			sess.Flash("error", "The measurement could not be started.")
		}
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	sess.Flash("job", job)
	sess.Flash("success", "Measuring the folders. Large websites take a while; reload the page when the task has finished.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// timeAgo mirrors helpers.php's time_ago.
func timeAgo(iso string) string {
	if iso == "" {
		return "never"
	}
	ts, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "—"
	}
	diff := int(time.Since(ts) / time.Second)
	switch {
	case diff < 0:
		return "in the future"
	case diff < 60:
		return "just now"
	case diff < 3600:
		return fmt.Sprintf("%d min ago", diff/60)
	case diff < 86400:
		return fmt.Sprintf("%d h ago", diff/3600)
	default:
		return fmt.Sprintf("%d d ago", diff/86400)
	}
}

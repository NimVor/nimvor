package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// phpEOL is the last day of security fixes per PHP release (php.net).
var phpEOL = map[string]string{
	"7.4": "2022-11-28", "8.0": "2023-11-26", "8.1": "2025-12-31", "8.2": "2026-12-31",
	"8.3": "2027-12-31", "8.4": "2028-12-31", "8.5": "2029-12-31",
}

type phpMgrVersion struct {
	Version, Status, Used, Support, SupportClass string
	Installed, Offered, Checked                  bool
}

type phpMgrExt struct {
	Name, Description, On string
	Checked               bool
}

type phpReviewItem struct{ What, Detail, Packages string }

type phpReviewGroup struct {
	Key, Label, Icon string
	Open             bool
	Items            []phpReviewItem
}

type phpSavedProfile struct {
	Versions, Extensions []string
	JobID, AppliedAt     string
}

// phpSaved reads the stored profile (settings.php_profile).
func (s *Server) phpSaved(ctx context.Context) phpSavedProfile {
	var out phpSavedProfile
	raw, err := s.settings.String(ctx, "php_profile", "")
	if err != nil || raw == "" {
		return out
	}
	var p struct {
		Versions   []string `json:"versions"`
		Extensions []string `json:"extensions"`
		JobID      string   `json:"job_id"`
		AppliedAt  string   `json:"applied_at"`
	}
	if json.Unmarshal([]byte(raw), &p) != nil || p.Versions == nil || p.Extensions == nil {
		return out
	}
	return phpSavedProfile{Versions: p.Versions, Extensions: p.Extensions, JobID: p.JobID, AppliedAt: p.AppliedAt}
}

func phpVersionLess(a, b string) bool {
	pa, pb := strings.SplitN(a, ".", 2), strings.SplitN(b, ".", 2)
	for len(pa) < 2 {
		pa = append(pa, "0")
	}
	for len(pb) < 2 {
		pb = append(pb, "0")
	}
	var am, an, bm, bn int
	fmt.Sscan(pa[0], &am)
	fmt.Sscan(pa[1], &an)
	fmt.Sscan(pb[0], &bm)
	fmt.Sscan(pb[1], &bn)
	if am != bm {
		return am < bm
	}
	return an < bn
}

func strList(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if str, ok := x.(string); ok {
				out = append(out, str)
			}
		}
	}
	return out
}

func hasString(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// phpManagerView gathers what the server has and offers (PhpManagerController::index).
func (s *Server) phpManagerView(ctx context.Context) (map[string]any, error) {
	call := func(name string, params any) (any, error) {
		raw, err := s.agent.Call(ctx, name, params, 60*time.Second)
		if err != nil {
			return nil, err
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		return v, nil
	}
	disc, err := call("discovery.php", nil)
	if err != nil {
		return nil, err
	}
	var installed []string
	if vs, ok := jmap(disc)["versions"].([]any); ok {
		for _, v := range vs {
			if jstr(v, "fpm_binary") != "" {
				installed = append(installed, jstr(v, "version"))
			}
		}
	}
	sort.Slice(installed, func(i, j int) bool { return phpVersionLess(installed[i], installed[j]) })
	extOn := map[string][]string{} // extension -> versions it is on for
	allOn := map[string]bool{}
	for _, v := range installed {
		list, err := call("php.extensions", map[string]any{"version": v})
		if err != nil {
			continue
		}
		if l, ok := list.([]any); ok {
			for _, e := range l {
				if jbool(e, "installed") && jbool(e, "enabled") {
					extOn[jstr(e, "name")] = append(extOn[jstr(e, "name")], v)
					allOn[jstr(e, "name")] = true
				}
			}
		}
	}
	usage := map[string]int{}
	if rows, err := s.db.QueryContext(ctx, `SELECT php_version, COUNT(*) FROM websites WHERE server_id = ? AND php_version <> '' GROUP BY php_version`, s.currentServerID(nil)); err == nil {
		for rows.Next() {
			var v string
			var n int
			if rows.Scan(&v, &n) == nil {
				usage[v] = n
			}
		}
		rows.Close()
	}
	avail, err := call("php.available", nil)
	if err != nil {
		return nil, err
	}
	available := strList(avail)
	cat, err := call("php.catalog", nil)
	if err != nil {
		return nil, err
	}
	saved := s.phpSaved(ctx)
	selVersions, selExts := saved.Versions, saved.Extensions
	if len(selVersions) == 0 {
		selVersions = installed
		if len(installed) == 0 {
			selExts = strList(jmap(cat)["defaults"])
		} else {
			selExts = nil
			for e := range allOn {
				selExts = append(selExts, e)
			}
		}
	}
	seen := map[string]bool{}
	var versions []string
	for _, l := range [][]string{available, installed, selVersions} {
		for _, v := range l {
			if !seen[v] {
				seen[v] = true
				versions = append(versions, v)
			}
		}
	}
	sort.Slice(versions, func(i, j int) bool { return phpVersionLess(versions[i], versions[j]) })
	today := time.Now().UTC().Format("2006-01-02")
	var vrows []phpMgrVersion
	for _, v := range versions {
		inst, offered := hasString(installed, v), hasString(available, v)
		row := phpMgrVersion{Version: v, Installed: inst, Offered: offered, Checked: hasString(selVersions, v), Support: "—"}
		switch {
		case inst:
			row.Status = "Installed"
		case offered:
			row.Status = "Can be installed"
		default:
			row.Status = "Not offered by the repositories"
		}
		if n := usage[v]; n > 0 {
			row.Used = fmt.Sprintf("%d (cannot be removed)", n)
		}
		if end, ok := phpEOL[v]; ok {
			if end < today {
				row.Support, row.SupportClass = "Ended "+end, "is-error"
			} else {
				row.Support, row.SupportClass = "Until "+end, "is-ok"
			}
		}
		vrows = append(vrows, row)
	}
	var erows []phpMgrExt
	if l, ok := jmap(cat)["extensions"].([]any); ok {
		for _, x := range l {
			n := jstr(x, "name")
			on := "—"
			if vs := extOn[n]; len(vs) > 0 {
				sort.Slice(vs, func(i, j int) bool { return phpVersionLess(vs[i], vs[j]) })
				on = "PHP " + strings.Join(vs, ", ")
			}
			erows = append(erows, phpMgrExt{Name: n, Description: jstr(x, "description"), On: on, Checked: hasString(selExts, n)})
		}
	}
	installedText := ""
	if len(installed) > 0 {
		installedText = "PHP " + strings.Join(installed, ", ")
	}
	return map[string]any{"Versions": vrows, "Extensions": erows, "Installed": installedText, "Saved": saved, "HasSaved": len(saved.Versions) > 0}, nil
}

var phpReviewOrder = []struct{ key, label, icon string }{
	{"install", "To install", "bi-plus-circle text-success"},
	{"update", "To update", "bi-arrow-up-circle text-primary"},
	{"enable", "To switch on", "bi-toggle-on text-success"},
	{"disable", "To switch off", "bi-toggle-off text-warning"},
	{"remove", "To remove", "bi-dash-circle text-danger"},
	{"blocked", "Kept or not possible", "bi-lock text-body-secondary"},
	{"unchanged", "No change", "bi-check-circle text-body-secondary"},
}

// phpReviewData groups a profile plan the way manager_review.php does.
func phpReviewData(plan any) (groups []phpReviewGroup, changes, removals bool) {
	by := map[string][]phpReviewItem{}
	items, _ := jmap(plan)["items"].([]any)
	for _, it := range items {
		what := "PHP " + jstr(it, "version")
		if e := jstr(it, "extension"); e != "" {
			what += " · " + e
		}
		a := jstr(it, "action")
		by[a] = append(by[a], phpReviewItem{What: what, Detail: jstr(it, "detail"), Packages: strings.Join(strList(jmap(it)["packages"]), " ")})
	}
	for _, g := range phpReviewOrder {
		if len(by[g.key]) > 0 {
			groups = append(groups, phpReviewGroup{Key: g.key, Label: g.label, Icon: g.icon, Open: g.key != "unchanged", Items: by[g.key]})
		}
	}
	return groups, jbool(plan, "changes"), jbool(plan, "removals")
}

func (s *Server) renderPhpReview(w http.ResponseWriter, csrf string, user any, versions, exts []string, plan any) {
	groups, changes, removals := phpReviewData(plan)
	s.render(w, "php_manager_review", map[string]any{
		"CSRF": csrf, "User": user, "Title": "Review PHP changes",
		"Versions": versions, "Extensions": exts, "Groups": groups, "Changes": changes, "Removals": removals,
		"Subtitle": fmt.Sprintf("PHP %s with %d extension(s)", strings.Join(versions, ", "), len(exts)),
		"Error":    "",
	})
}

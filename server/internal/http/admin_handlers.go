package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// plansIndex lists hosting plans (staff only). Mirrors PlansController::index
// (route "plans", plans.manage).
type planFieldView struct {
	Name, Label, Value, Error string
	IsLimit                   bool
	Max                       int
	Unit                      string
}

type planForm struct {
	ID                       int64
	Existing                 bool
	Name, Description, Title string
	NameError, DescError     string
	Fields, Limits           []planFieldView
	Ffmpeg                   bool
	FeatureList              string
	Open                     bool
}

type planRow struct {
	ID                                                                       int64
	Name, Owner, Description, Cron, Workers, Customers, Disk, Bandwidth, Cpu string
	Websites, Databases, Email, Ftp, Users                                   string
	Confirm                                                                  string
	Form                                                                     planForm
}

var planFieldLabels = []struct{ Key, Label string }{
	{"disk_quota_mb", "Disk space quota (MB)"}, {"bandwidth_mb", "Monthly bandwidth (MB)"}, {"max_websites", "Websites"},
	{"max_subdomains", "Subdomains"}, {"max_parked_domains", "Parked domains (aliases)"}, {"max_addon_domains", "Addon domains"},
	{"max_databases", "SQL databases"}, {"max_ftp_accounts", "FTP accounts"}, {"max_email_accounts", "Email accounts"},
	{"max_email_quota_mb", "Max quota per email address (MB)"}, {"max_cron_jobs", "Cron jobs"}, {"max_workers", "Workers (applications)"},
	{"max_customers", "Customers (resellers)"},
}

var planLimitLabels = []struct {
	Key, Label, Unit string
	Max              int
}{
	{"cpu_percent", "CPU", "% (100 = one core)", 12800}, {"memory_mb", "Memory", "MB", 2097152}, {"tasks_max", "Processes and threads", "", 1048576},
	{"io_read_mbps", "Disk reads", "MB/s", 100000}, {"io_write_mbps", "Disk writes", "MB/s", 100000},
}

func planInf(n int) string {
	if n == 0 {
		return "∞"
	}
	return strconv.Itoa(n)
}

func planSize(mb int) string {
	if mb == 0 {
		return "∞"
	}
	return fmtBytes(float64(mb) * 1048576)
}

func (s *Server) buildPlanForm(p *hosting.Plan, admin bool, vals map[string]string, errs map[string]string) planForm {
	f := planForm{Existing: p != nil, Title: "New plan"}
	get := func(k string, def int) string {
		if v, ok := vals[k]; ok {
			return v
		}
		if p != nil {
			if v, ok := p.Fields[k]; ok {
				return strconv.Itoa(v)
			}
			if v, ok := p.Limits[k]; ok {
				return strconv.Itoa(v)
			}
		}
		return strconv.Itoa(def)
	}
	if p != nil {
		f.ID, f.Name, f.Description, f.Title = p.ID, p.Name, p.Description, "Edit plan "+p.Name
		f.Ffmpeg = p.Ffmpeg
		if p.FeatureList.Valid {
			f.FeatureList = strconv.FormatInt(p.FeatureList.Int64, 10)
		}
	}
	if v, ok := vals["name"]; ok {
		f.Name = v
	}
	if v, ok := vals["description"]; ok {
		f.Description = v
	}
	if len(vals) > 0 {
		f.Ffmpeg = vals["ffmpeg"] == "1"
		if v, ok := vals["feature_list_id"]; ok {
			f.FeatureList = v
		}
	}
	f.NameError, f.DescError = errs["name"], errs["description"]
	for _, fl := range planFieldLabels {
		if fl.Key == "max_customers" && !admin {
			continue
		}
		f.Fields = append(f.Fields, planFieldView{Name: fl.Key, Label: fl.Label, Value: get(fl.Key, 0), Error: errs[fl.Key]})
	}
	for _, l := range planLimitLabels {
		label := l.Label
		if l.Unit != "" {
			label += " (" + l.Unit + ")"
		}
		f.Limits = append(f.Limits, planFieldView{Name: l.Key, Label: label, Value: get(l.Key, 0), Error: errs[l.Key], IsLimit: true, Max: l.Max})
	}
	return f
}

// plansIndex GET /plans (PlansController::index).
func (s *Server) plansIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := userFrom(ctx)
	sess := sessionFrom(ctx)
	plans, err := hosting.NewPlanRepository(s.db).Visible(ctx, user)
	if err != nil {
		s.log.Error("list plans", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	admin := hosting.IsAll(user)
	errs, old := flashJSON(sess, "errors"), flashJSON(sess, "old")
	failedID := int64(-1)
	if len(old) > 0 {
		failedID, _ = strconv.ParseInt(old["plan_id"], 10, 64)
	}
	rows := make([]planRow, 0, len(plans))
	for i := range plans {
		p := &plans[i]
		row := planRow{ID: p.ID, Name: p.Name, Description: p.Description, Users: strconv.Itoa(p.Users),
			Websites: planInf(p.Fields["max_websites"]), Databases: planInf(p.Fields["max_databases"]), Cron: planInf(p.Fields["max_cron_jobs"]),
			Workers: planInf(p.Fields["max_workers"]), Customers: planInf(p.Fields["max_customers"]), Disk: planSize(p.Fields["disk_quota_mb"]),
			Bandwidth: planSize(p.Fields["bandwidth_mb"]), Email: planInf(p.Fields["max_email_accounts"]), Ftp: planInf(p.Fields["max_ftp_accounts"]),
			Confirm: "Delete the plan " + p.Name + "?"}
		if p.OwnerName.Valid && admin {
			row.Owner = p.OwnerName.String
		}
		cpu, mem := "∞", "∞"
		if c := p.Limits["cpu_percent"]; c > 0 {
			cpu = strconv.Itoa(c) + "%"
		}
		if m := p.Limits["memory_mb"]; m > 0 {
			mem = strings.TrimSuffix(fmtBytes(float64(m)*1048576), ".0")
		}
		row.Cpu = cpu + " / " + mem
		var vals, ferr map[string]string
		if failedID == p.ID {
			vals, ferr = old, errs
		}
		row.Form = s.buildPlanForm(p, admin, vals, ferr)
		row.Form.Open = failedID == p.ID
		rows = append(rows, row)
	}
	var newVals, newErrs map[string]string
	if failedID == 0 {
		newVals, newErrs = old, errs
	}
	newForm := s.buildPlanForm(nil, admin, newVals, newErrs)
	newForm.Open = failedID == 0
	type listOpt struct {
		ID   int64
		Name string
	}
	var lists []listOpt
	if admin {
		if ls, err := s.features.Lists(ctx); err == nil {
			for _, l := range ls {
				lists = append(lists, listOpt{l.ID, l.Name})
			}
		}
	}
	data := map[string]any{
		"User": user, "CSRF": session.CSRFToken(sess), "Title": "Hosting plans", "Rows": rows, "Count": len(rows), "Admin": admin,
		"NewForm": newForm, "FeatureLists": lists,
		"Subtitle": map[bool]string{true: "Limits for hosting customers and resellers", false: "Limits for hosting customers"}[admin],
		"Success":  pullFlashKey(sess, "success"), "Error": pullFlashKey(sess, "error"),
	}
	s.render(w, "plans", data)
}

// serversIndex lists the managed servers (staff only). Mirrors
// ServersController::index (route "servers", servers.manage).
type serverRow struct {
	ID                                                 int64
	Name, Where, Agent, StatusLabel, StatusClass, Seen string
	Websites                                           int
	Remote                                             bool
	Confirm, Pin, LastError                            string
}

// serversIndex lists the managed servers (staff only). Mirrors
// ServersController::index (route "servers", servers.manage).
func (s *Server) serversIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	servers, err := hosting.NewServerRepository(s.db).All(r.Context())
	if err != nil {
		s.log.Error("list servers", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	labels := map[string]string{"online": "online", "offline": "unreachable", "timeout": "timed out", "auth_failed": "authentication failed",
		"certificate_mismatch": "certificate mismatch", "refused": "refused", "version_mismatch": "version mismatch", "unknown": "not checked yet"}
	rows := make([]serverRow, 0, len(servers))
	for _, sv := range servers {
		row := serverRow{ID: sv.ID, Name: sv.Name, Agent: sv.AgentVersion, Websites: sv.Websites, Remote: sv.Kind == "remote",
			Pin: sv.Fingerprint, LastError: sv.LastError, Confirm: "Remove " + sv.Name + " from WhaCP? Nothing is changed on the server itself."}
		if sv.Kind == "local" {
			row.Where, row.StatusLabel, row.StatusClass = "this machine (Unix socket)", "local", "text-bg-success"
		} else {
			row.Where = sv.URL
			row.StatusLabel = labels[sv.Status]
			if row.StatusLabel == "" {
				row.StatusLabel = sv.Status
			}
			switch sv.Status {
			case "online":
				row.StatusClass = "text-bg-success"
			case "version_mismatch", "unknown":
				row.StatusClass = "text-bg-warning"
			default:
				row.StatusClass = "text-bg-danger"
			}
		}
		if sv.LastSeenAt.Valid {
			row.Seen = sv.LastSeenAt.String
			if len(row.Seen) > 16 {
				row.Seen = row.Seen[:16]
			}
		}
		rows = append(rows, row)
	}
	s.render(w, "servers", map[string]any{
		"User": user, "CSRF": session.CSRFToken(sess), "Title": "Servers", "Rows": rows,
		"Success": pullFlashKey(sess, "success"), "Error": pullFlashKey(sess, "error"),
	})
}

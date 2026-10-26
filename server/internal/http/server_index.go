package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// serverIndex GET /server — administrator overview of the server (OS, kernel,
// memory, uptime, CPU, disks, network, firewall). Mirrors
// ServerController::index (server, server.view).
func (s *Server) serverIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Server",
		"Info":       nil,
		"Metrics":    nil,
		"Firewall":   nil,
		"Settings":   nil,
		"Timezones":  []any{},
		"Graphical":  nil,
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
	infoRaw, err := s.agent.Call(r.Context(), "system.info", nil, 30*time.Second)
	if err != nil {
		data["AgentError"] = err.Error()
		data["V"] = buildServerView(data)
		s.render(w, "server_index", data)
		return
	}
	metricsRaw, err := s.agent.Call(r.Context(), "system.metrics", nil, 30*time.Second)
	if err != nil {
		data["AgentError"] = err.Error()
		var info any
		_ = json.Unmarshal(infoRaw, &info)
		data["Info"] = info
		data["V"] = buildServerView(data)
		s.render(w, "server_index", data)
		return
	}
	fwRaw, err := s.agent.Call(r.Context(), "discovery.firewall", nil, 30*time.Second)
	if err == nil {
		var fw any
		_ = json.Unmarshal(fwRaw, &fw)
		data["Firewall"] = fw
	}
	// Best-effort settings/zone/graphical; missing actions simply leave them nil.
	if raw, err := s.agent.Call(r.Context(), "server.settings", nil, 15*time.Second); err == nil {
		var v any
		_ = json.Unmarshal(raw, &v)
		data["Settings"] = v
	}
	if raw, err := s.agent.Call(r.Context(), "server.timezones", nil, 15*time.Second); err == nil {
		var v []any
		_ = json.Unmarshal(raw, &v)
		data["Timezones"] = v
	}
	if raw, err := s.agent.Call(r.Context(), "server.graphical", nil, 15*time.Second); err == nil {
		var v any
		_ = json.Unmarshal(raw, &v)
		data["Graphical"] = v
	}
	var info, metrics any
	_ = json.Unmarshal(infoRaw, &info)
	_ = json.Unmarshal(metricsRaw, &metrics)
	data["Info"] = info
	data["Metrics"] = metrics
	data["V"] = buildServerView(data)
	s.render(w, "server_index", data)
}

type serverDisk struct {
	Mount, Device, FS, Used, Total string
	Pct                            float64
	Level                          string
}

type serverIface struct {
	Name, MAC string
	Up        bool
	Addrs     []string
}

type serverView struct {
	Hostname, OSName, OSFamily, Kernel, Arch, CPU, MemTotal, MemUsed, Swap string
	Uptime, Booted, Timezone, Load, FirewallBackend, AgentVersion          string
	OSSupported, OSPreview, UFWEnabled                                     bool
	Disks                                                                  []serverDisk
	Ifaces                                                                 []serverIface
	HasSettings                                                            bool
	SetHostname, SetTimezone, NTP, ScheduledReboot, RebootBy               string
	RebootRequired                                                         bool
	DisplayManager, GraphicalTarget                                        string
	GraphicalOn, GraphicalActive                                           bool
	Timezones                                                              []string
}

func jmap(v any) map[string]any { m, _ := v.(map[string]any); return m }

func jstr(v any, keys ...string) string {
	for _, k := range keys {
		m := jmap(v)
		if m == nil {
			return ""
		}
		v = m[k]
	}
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

func jnum(v any, keys ...string) float64 {
	n, _ := strconv.ParseFloat(jstr(v, keys...), 64)
	return n
}

func jbool(v any, keys ...string) bool { return jstr(v, keys...) == "true" }

func fmtBytes(v float64) string {
	if v <= 0 {
		return "—"
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatFloat(v, 'f', 0, 64) + " B"
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[i]
}

// buildServerView flattens the agent's replies for the Server page
// (ServerController::index).
func buildServerView(d map[string]any) *serverView {
	info, metrics, fw, set, gr := d["Info"], d["Metrics"], d["Firewall"], d["Settings"], d["Graphical"]
	v := &serverView{
		Hostname: jstr(info, "system", "hostname"), OSName: jstr(info, "os", "pretty_name"), OSFamily: jstr(info, "os", "family"),
		Kernel: jstr(info, "system", "kernel"), Arch: jstr(info, "system", "architecture"),
		CPU:      jstr(info, "system", "cpu", "cores") + " × " + jstr(info, "system", "cpu", "model"),
		MemTotal: fmtBytes(jnum(metrics, "memory", "total_bytes")), MemUsed: fmtBytes(jnum(metrics, "memory", "used_bytes")),
		Swap:   fmtBytes(jnum(metrics, "memory", "swap_total_bytes")),
		Uptime: formatDuration(int64(jnum(info, "system", "uptime_seconds"))), Booted: jstr(info, "system", "boot_time"),
		Timezone:        jstr(info, "system", "timezone"),
		Load:            fmt.Sprintf("%.2f / %.2f / %.2f", jnum(metrics, "load", "load1"), jnum(metrics, "load", "load5"), jnum(metrics, "load", "load15")),
		FirewallBackend: jstr(fw, "backend"), UFWEnabled: jbool(fw, "enabled"), AgentVersion: jstr(info, "agent_version"),
		OSSupported: jbool(info, "os", "supported"), OSPreview: jbool(info, "os", "preview"),
	}
	if v.FirewallBackend == "" {
		v.FirewallBackend = "unknown"
	}
	if ds, ok := jmap(metrics)["disks"].([]any); ok {
		for _, x := range ds {
			p := jnum(x, "used_percent")
			lvl := ""
			if p >= 90 {
				lvl = "bg-danger"
			} else if p >= 75 {
				lvl = "bg-warning"
			}
			v.Disks = append(v.Disks, serverDisk{Mount: jstr(x, "mount_point"), Device: jstr(x, "device"), FS: jstr(x, "fs_type"),
				Used: fmtBytes(jnum(x, "used_bytes")), Total: fmtBytes(jnum(x, "total_bytes")), Pct: p, Level: lvl})
		}
	}
	if is, ok := jmap(jmap(info)["system"])["interfaces"].([]any); ok {
		for _, x := range is {
			ifc := serverIface{Name: jstr(x, "name"), MAC: jstr(x, "mac"), Up: jbool(x, "up")}
			if as, ok := jmap(x)["addresses"].([]any); ok {
				for _, a := range as {
					if str, ok := a.(string); ok {
						ifc.Addrs = append(ifc.Addrs, str)
					}
				}
			}
			v.Ifaces = append(v.Ifaces, ifc)
		}
	}
	if set != nil {
		v.HasSettings = true
		v.SetHostname = jstr(set, "hostname")
		if v.SetHostname == "" {
			v.SetHostname = v.Hostname
		}
		v.SetTimezone = jstr(set, "timezone")
		switch {
		case jbool(set, "ntp_synchronized"):
			v.NTP = "synchronised"
		case jbool(set, "ntp_enabled"):
			v.NTP = "enabled, not yet synchronised"
		default:
			v.NTP = "off"
		}
		v.ScheduledReboot = jstr(set, "scheduled_reboot")
		if len(v.ScheduledReboot) > 16 {
			v.ScheduledReboot = v.ScheduledReboot[:16]
		}
		v.RebootRequired = jbool(set, "reboot_required")
		if by, ok := jmap(set)["reboot_required_by"].([]any); ok {
			var names []string
			for _, b := range by {
				if str, ok := b.(string); ok {
					names = append(names, str)
				}
			}
			v.RebootBy = strings.Join(names, ", ")
		}
	}
	v.DisplayManager = jstr(gr, "display_manager")
	v.GraphicalTarget = jstr(gr, "default_target")
	v.GraphicalActive = jbool(gr, "active")
	v.GraphicalOn = v.GraphicalActive || v.GraphicalTarget == "graphical.target"
	if tz, ok := d["Timezones"].([]any); ok {
		for _, t := range tz {
			if str, ok := t.(string); ok {
				v.Timezones = append(v.Timezones, str)
			}
		}
	}
	if len(v.Timezones) == 0 && v.SetTimezone != "" {
		v.Timezones = []string{v.SetTimezone}
	}
	return v
}

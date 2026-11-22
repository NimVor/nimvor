// Package telemetry reads the facts an opt-in telemetry report is made of: the
// operating system, the hardware size, the software that is installed and a
// few health yes/no answers (docs/telemetry/README.md).
//
// The action is read-only and reports nothing by itself: the panel asks for it
// only when its administrator turned telemetry on, and sends what it gets to
// the central interface. Nothing here reads or returns a host name, address,
// domain, user name, path or file content: every field is a fixed, coarse fact
// (a version's major.minor, a core count, a size in MB or GB).
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/components"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/monitoring"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
)

// OS is the operating-system and hardware part.
type OS struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Arch           string `json:"arch"`
	KernelMajor    string `json:"kernel_major,omitempty"`
	Virtualization string `json:"virtualization"`
	InitSystem     string `json:"init_system"`
	CPUCores       *int   `json:"cpu_cores,omitempty"`
	MemoryMB       *int64 `json:"memory_mb,omitempty"`
	DiskGB         *int64 `json:"disk_gb,omitempty"`
}

// Software is one installed component.
type Software struct {
	Component string `json:"component"`
	Version   string `json:"version"`
	Running   *bool  `json:"running,omitempty"`
}

// Health is the yes/no and percentage part. A field the agent cannot know
// (backups and jobs belong to the panel) is left out, never guessed.
type Health struct {
	ServicesTotal     *int  `json:"services_total,omitempty"`
	ServicesFailed    *int  `json:"services_failed,omitempty"`
	DiskUsedPercent   *int  `json:"disk_used_percent,omitempty"`
	MemoryUsedPercent *int  `json:"memory_used_percent,omitempty"`
	UpdatesPending    *bool `json:"updates_pending,omitempty"`
	SSLExpiring       *bool `json:"ssl_expiring,omitempty"`
}

// Report is what telemetry.collect returns.
type Report struct {
	OS       OS         `json:"os"`
	Software []Software `json:"software"`
	Health   Health     `json:"health"`
}

// The central interface accepts only these names (website TelemetryService).
// Component → the NimVor package whose version stands for it.
var tracked = []struct{ id, name, pkg string }{
	{"web", "nginx", "nginx"},
	{"database", "mariadb", "mariadb-server"},
	{"mail", "postfix", "postfix"},
	{"mail", "dovecot", "dovecot-imapd"},
	{"mail", "rspamd", "rspamd"},
	{"dns", "bind", "bind9"},
	{"redis", "redis", "redis-server"},
	{"memcached", "memcached", "memcached"},
	{"mongodb", "mongodb", "mongodb-org"},
	{"fail2ban", "fail2ban", "fail2ban"},
	{"clamav", "clamav", "clamav"},
	{"certbot", "certbot", "certbot"},
}

var (
	epoch      = regexp.MustCompile(`^[0-9]+:`)
	majorMinor = regexp.MustCompile(`^([0-9]{1,3})(?:\.([0-9]{1,3}))?`)
)

// ShortVersion reduces a package version ("1:1.24.0-2ubuntu7", "10.11.8+maria~ubu2404") to major.minor.
func ShortVersion(v string) string {
	v = epoch.ReplaceAllString(strings.TrimSpace(v), "")
	m := majorMinor.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	if m[2] == "" {
		return m[1]
	}
	return m[1] + "." + m[2]
}

// KernelMajor reduces "6.8.0-45-generic" to "6.8".
func KernelMajor(release string) string {
	m := majorMinor.FindStringSubmatch(strings.TrimSpace(release))
	if m == nil || m[2] == "" {
		return ""
	}
	return m[1] + "." + m[2]
}

// OSVersion keeps what the central interface accepts: the Debian family's version as written
// (24.04, 12) and the Red Hat family's major version.
func OSVersion(id, versionID string) string {
	switch id {
	case "almalinux", "rocky", "rhel", "centos", "fedora":
		if i := strings.Index(versionID, "."); i > 0 {
			return versionID[:i]
		}
	}
	if m := regexp.MustCompile(`^[0-9]{1,2}(\.[0-9]{1,2})?`).FindString(versionID); m != "" {
		return m
	}
	return ""
}

// Architecture maps the kernel's name to amd64, arm64 or other.
func Architecture(machine string) string {
	switch strings.TrimSpace(machine) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return "other"
}

// Virtualization tells what the server runs on from files the kernel offers; "unknown" when it cannot tell,
// "none" for a physical machine.
func Virtualization(root, proc string) string {
	read := func(p string) string {
		b, _ := os.ReadFile(p)
		return strings.ToLower(strings.TrimSpace(string(b)))
	}
	if _, err := os.Stat(filepath.Join(root, ".dockerenv")); err == nil {
		return "docker"
	}
	cg := read(filepath.Join(proc, "1/cgroup"))
	switch {
	case strings.Contains(cg, "docker"):
		return "docker"
	case strings.Contains(cg, "lxc"):
		return "lxc"
	}
	if _, err := os.Stat(filepath.Join(proc, "vz")); err == nil {
		return "openvz"
	}
	dmi := read(filepath.Join(root, "sys/class/dmi/id/sys_vendor")) + " " + read(filepath.Join(root, "sys/class/dmi/id/product_name"))
	for _, p := range []struct{ needle, name string }{
		{"vmware", "vmware"}, {"virtualbox", "virtualbox"}, {"microsoft", "hyperv"}, {"xen", "xen"},
		{"qemu", "kvm"}, {"kvm", "kvm"}, {"bochs", "qemu"}, {"openstack", "kvm"}, {"amazon ec2", "kvm"}, {"google compute", "kvm"}, {"digitalocean", "kvm"}, {"hetzner", "kvm"},
	} {
		if strings.Contains(dmi, p.needle) {
			return p.name
		}
	}
	if b, err := os.ReadFile(filepath.Join(proc, "cpuinfo")); err == nil && bytes.Contains(b, []byte(" hypervisor")) {
		return "other"
	}
	if strings.TrimSpace(dmi) != "" {
		return "none"
	}
	return "unknown"
}

// InitSystem is systemd when its runtime directory exists.
func InitSystem(root string) string {
	if st, err := os.Stat(filepath.Join(root, "run/systemd/system")); err == nil && st.IsDir() {
		return "systemd"
	}
	return "unknown"
}

func intp(n int) *int     { return &n }
func i64p(n int64) *int64 { return &n }
func boolp(b bool) *bool  { return &b }
func pct(used, total uint64) *int {
	if total == 0 {
		return nil
	}
	v := int(float64(used) * 100 / float64(total))
	if v > 100 {
		v = 100
	}
	return &v
}

// Collect reads the report. Parts it cannot read are left out; it never fails as a whole.
func Collect(ctx context.Context, env *host.Env) Report {
	r := Report{Software: []Software{}}
	info := env.Platform.Info()
	os0 := OS{ID: info.ID, Version: OSVersion(info.ID, info.VersionID), Virtualization: Virtualization(env.Cfg.RootDir, env.Cfg.ProcDir), InitSystem: InitSystem(env.Cfg.RootDir)}
	switch os0.ID {
	case "ubuntu", "debian", "almalinux", "rocky", "rhel", "centos", "fedora":
	default:
		os0.ID = "other"
	}
	si := monitoring.ReadSystemInfo(env.Cfg.ProcDir, env.Cfg.RootDir)
	os0.Arch = Architecture(si.Architecture)
	os0.KernelMajor = KernelMajor(si.Kernel)
	if si.CPU.Cores > 0 {
		os0.CPUCores = intp(si.CPU.Cores)
	}
	if b, err := os.ReadFile(filepath.Join(env.Cfg.ProcDir, "meminfo")); err == nil {
		if m, err := monitoring.ParseMeminfo(b); err == nil && m.TotalBytes > 0 {
			os0.MemoryMB = i64p(int64(m.TotalBytes >> 20))
			r.Health.MemoryUsedPercent = pct(m.UsedBytes, m.TotalBytes)
		}
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.Clean(env.Cfg.RootDir), &st); err == nil && st.Blocks > 0 {
		total := st.Blocks * uint64(st.Bsize)
		free := st.Bfree * uint64(st.Bsize)
		os0.DiskGB = i64p(int64(total >> 30))
		r.Health.DiskUsedPercent = pct(total-free, total)
	}
	r.OS = os0

	cs := components.Status(ctx, env)
	byID := map[string]components.Component{}
	for _, c := range cs {
		byID[c.ID] = c
	}
	var names []string
	for _, t := range tracked {
		names = append(names, t.pkg)
	}
	versions := pkgs.Installed(ctx, env, names)
	running := func(c components.Component) *bool {
		for _, s := range c.Steps {
			if s.ID == "running" && (s.Status == components.OK || s.Status == components.Todo) {
				return boolp(s.Status == components.OK)
			}
		}
		return nil
	}
	seen := map[string]bool{}
	for _, t := range tracked {
		c, ok := byID[t.id]
		v, has := versions[t.pkg]
		if !ok || !has || seen[t.name] {
			continue
		}
		seen[t.name] = true
		r.Software = append(r.Software, Software{Component: t.name, Version: ShortVersion(v), Running: running(c)})
	}
	phpSeen := map[string]bool{}
	for _, v := range env.Discovery.PHP().Versions {
		if v.FPMBinary == "" || phpSeen[v.Version] {
			continue
		}
		phpSeen[v.Version] = true
		r.Software = append(r.Software, Software{Component: "php", Version: ShortVersion(v.Version)})
	}
	// The central interface keeps one entry per component: the newest PHP stands for them, the rest are counted by the panel.
	r.Software = dedupePHP(r.Software)
	sort.SliceStable(r.Software, func(i, j int) bool { return r.Software[i].Component < r.Software[j].Component })

	total, failed := 0, 0
	for _, c := range cs {
		if !c.Installed {
			continue
		}
		if run := running(c); run != nil {
			total++
			if !*run {
				failed++
			}
		}
	}
	r.Health.ServicesTotal, r.Health.ServicesFailed = intp(total), intp(failed)

	if pkgs.Available(env) {
		if ups, err := pkgs.Updates(ctx, env); err == nil {
			r.Health.UpdatesPending = boolp(len(ups) > 0)
		}
	}
	certs := env.Discovery.Certificates()
	if len(certs) > 0 {
		exp := false
		for _, c := range certs {
			if c.Certificate.Error == "" && c.Certificate.DaysRemaining < 14 {
				exp = true
			}
		}
		r.Health.SSLExpiring = boolp(exp)
	}
	return r
}

// dedupePHP keeps the highest PHP version as the single "php" entry.
func dedupePHP(in []Software) []Software {
	best := -1
	for i, s := range in {
		if s.Component != "php" {
			continue
		}
		if best < 0 || versionLess(in[best].Version, s.Version) {
			best = i
		}
	}
	out := make([]Software, 0, len(in))
	for i, s := range in {
		if s.Component == "php" && i != best {
			continue
		}
		out = append(out, s)
	}
	return out
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			if len(pa[i]) != len(pb[i]) {
				return len(pa[i]) < len(pb[i])
			}
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

// Register adds telemetry.collect (read-only).
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{
		Name:        "telemetry.collect",
		Description: "The facts of an opt-in telemetry report: operating system and hardware size, installed components with their major.minor version, service and update health. Read-only; nothing is sent by the agent and nothing identifies the server.",
		Handler: func(ctx context.Context, params json.RawMessage) (any, error) {
			var none struct{}
			if err := actions.DecodeParams(params, &none); err != nil {
				return nil, err
			}
			return Collect(ctx, env), nil
		},
	})
}

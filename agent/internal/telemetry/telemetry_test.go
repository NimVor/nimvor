package telemetry

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestShortVersion(t *testing.T) {
	for in, want := range map[string]string{
		"1:1.24.0-2ubuntu7":          "1.24",
		"10.11.8+maria~ubu2404":      "10.11",
		"8.3.6":                      "8.3",
		"3.0.9-1.el9":                "3.0",
		"7":                          "7",
		"":                           "",
		"unstable":                   "",
		"2:9.18.28-0ubuntu0.24.04.1": "9.18",
	} {
		if got := ShortVersion(in); got != want {
			t.Errorf("ShortVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKernelMajorOSVersionArchitecture(t *testing.T) {
	if got := KernelMajor("6.8.0-45-generic"); got != "6.8" {
		t.Errorf("kernel %q", got)
	}
	if got := KernelMajor("nonsense"); got != "" {
		t.Errorf("kernel %q", got)
	}
	for _, c := range []struct{ id, in, want string }{
		{"ubuntu", "24.04", "24.04"}, {"debian", "12", "12"}, {"almalinux", "9.4", "9"}, {"rocky", "9.6", "9"}, {"rhel", "10", "10"}, {"ubuntu", "", ""},
	} {
		if got := OSVersion(c.id, c.in); got != c.want {
			t.Errorf("OSVersion(%s, %q) = %q, want %q", c.id, c.in, got, c.want)
		}
	}
	if Architecture("x86_64") != "amd64" || Architecture("aarch64") != "arm64" || Architecture("riscv64") != "other" {
		t.Error("architecture mapping")
	}
}

func TestVirtualization(t *testing.T) {
	root, proc := t.TempDir(), t.TempDir()
	if got := Virtualization(root, proc); got != "unknown" {
		t.Errorf("nothing to read: %q", got)
	}
	write := func(base, rel, body string) {
		p := filepath.Join(base, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(root, "sys/class/dmi/id/sys_vendor", "QEMU\n")
	if got := Virtualization(root, proc); got != "kvm" {
		t.Errorf("qemu: %q", got)
	}
	write(root, "sys/class/dmi/id/sys_vendor", "Dell Inc.\n")
	write(root, "sys/class/dmi/id/product_name", "PowerEdge R640\n")
	if got := Virtualization(root, proc); got != "none" {
		t.Errorf("physical: %q", got)
	}
	write(proc, "1/cgroup", "0::/docker/abc\n")
	if got := Virtualization(root, proc); got != "docker" {
		t.Errorf("docker: %q", got)
	}
}

func TestDedupePHPKeepsTheHighest(t *testing.T) {
	got := dedupePHP([]Software{{Component: "php", Version: "8.1"}, {Component: "nginx", Version: "1.24"}, {Component: "php", Version: "8.3"}, {Component: "php", Version: "8.10"}})
	var php []string
	for _, s := range got {
		if s.Component == "php" {
			php = append(php, s.Version)
		}
	}
	if len(php) != 1 || php[0] != "8.10" || len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestCollectReportsFactsOnlyAndNothingThatIdentifiesTheServer(t *testing.T) {
	h := testhost.New(t)
	h.Respond = func(id string, a []string) (string, error, bool) {
		switch {
		case id == "dpkg-query":
			var b strings.Builder
			for _, p := range a {
				switch p {
				case "nginx":
					b.WriteString("nginx\tii \t1:1.24.0-2ubuntu7\n")
				case "postfix":
					b.WriteString("postfix\tii \t3.8.6-1build2\n")
				}
			}
			return b.String(), nil, true
		case id == "systemctl" && len(a) > 0 && a[0] == "show":
			var b strings.Builder
			for _, u := range a {
				if u == "nginx.service" {
					b.WriteString("Id=nginx.service\nLoadState=loaded\nActiveState=active\nUnitFileState=enabled\n\n")
				}
				if u == "postfix.service" {
					b.WriteString("Id=postfix.service\nLoadState=loaded\nActiveState=failed\nUnitFileState=enabled\n\n")
				}
			}
			return b.String(), nil, true
		}
		return "", nil, false
	}
	rep := Collect(t.Context(), h.Env)
	if rep.OS.ID != "ubuntu" || rep.OS.Version == "" {
		t.Errorf("os: %+v", rep.OS)
	}
	if rep.OS.Arch == "" || rep.OS.Virtualization == "" || rep.OS.InitSystem == "" {
		t.Errorf("os facts missing: %+v", rep.OS)
	}
	versions := map[string]Software{}
	for _, s := range rep.Software {
		versions[s.Component] = s
	}
	if ng := versions["nginx"]; ng.Version != "1.24" || ng.Running == nil || !*ng.Running {
		t.Errorf("nginx: %+v", ng)
	}
	if rep.Health.ServicesTotal == nil || rep.Health.ServicesFailed == nil || *rep.Health.ServicesFailed < 1 {
		t.Errorf("a failed postfix should count: %+v", rep.Health)
	}
	if rep.Health.DiskUsedPercent != nil && (*rep.Health.DiskUsedPercent < 0 || *rep.Health.DiskUsedPercent > 100) {
		t.Errorf("disk percent: %d", *rep.Health.DiskUsedPercent)
	}

	// Nothing that names the server or a person.
	b, _ := json.Marshal(rep)
	out := string(b)
	hn, _ := os.Hostname()
	if hn != "" && strings.Contains(out, hn) {
		t.Errorf("the host name is in the report: %s", out)
	}
	for _, bad := range []string{"example.com", "/etc/", "/var/", "@", "MAC", "hostname"} {
		if strings.Contains(out, bad) {
			t.Errorf("%q in the report: %s", bad, out)
		}
	}
	for _, f := range strings.FieldsFunc(out, func(r rune) bool { return strings.ContainsRune(`{}[]",: `, r) }) {
		if net.ParseIP(f) != nil {
			t.Errorf("an address in the report: %s", f)
		}
	}
	// Only the fields the central interface accepts.
	var generic map[string]any
	json.Unmarshal(b, &generic)
	if len(generic) != 3 {
		t.Errorf("sections: %v", generic)
	}
}

func TestTheActionIsReadOnlyAndTakesNoParameters(t *testing.T) {
	h := testhost.New(t)
	reg := actions.NewRegistry(true)
	Register(reg, h.Env)
	var found bool
	for _, a := range reg.List() {
		if a.Name == "telemetry.collect" {
			found = true
			if a.Mutating {
				t.Error("telemetry.collect must not be mutating")
			}
		}
	}
	if !found {
		t.Fatal("not registered")
	}
}

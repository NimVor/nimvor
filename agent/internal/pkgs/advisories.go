package pkgs

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Advisory is a pending update with the vulnerability information the OS
// itself provides — no custom CVE database. On RHEL, dnf updateinfo gives
// the advisory id, severity and CVE list; on Debian/Ubuntu the native signal
// is the security pocket, which tells security from normal but carries no
// per-package CVE, so those stay empty and the update is still reported.
type Advisory struct {
	Package   string   `json:"package"`
	Installed string   `json:"installed"`
	Fixed     string   `json:"fixed"`              // the version that fixes it (the update)
	Advisory  string   `json:"advisory,omitempty"` // e.g. RHSA-2026:1234 / USN id when known
	Severity  string   `json:"severity,omitempty"` // critical/important/moderate/low (RHEL)
	CVEs      []string `json:"cves,omitempty"`
	Kind      string   `json:"kind"` // "security", "normal" or "unknown"
}

// Advisories lists the pending updates enriched with advisory data.
func Advisories(ctx context.Context, env *host.Env) ([]Advisory, error) {
	ups, err := Updates(ctx, env)
	if err != nil {
		return nil, err
	}
	out := make([]Advisory, 0, len(ups))
	for _, u := range ups {
		a := Advisory{Package: u.Name, Installed: u.Current, Fixed: u.New, Kind: "normal"}
		if u.Security {
			a.Kind = "security"
		}
		out = append(out, a)
	}
	if dnf(env) {
		enrichDNF(ctx, env, out)
	}
	return out, nil
}

// enrichDNF fills advisory ids, severities and CVEs from dnf updateinfo. If
// the metadata cannot be read, the entries keep the security/normal kind
// from check-update; a package with an update but no resolvable metadata is
// left as "unknown" so it is never silently treated as safe.
func enrichDNF(ctx context.Context, env *host.Env, advs []Advisory) {
	list, err1 := env.Runner.RunWith(ctx, "dnf", execx.Opts{Timeout: -1}, "-q", "--cacheonly", "updateinfo", "list", "--updates")
	cves, _ := env.Runner.RunWith(ctx, "dnf", execx.Opts{Timeout: -1}, "-q", "--cacheonly", "updateinfo", "list", "--cves", "--updates")
	if err1 != nil {
		for i := range advs {
			if advs[i].Kind != "security" {
				advs[i].Kind = "unknown"
			}
		}
		return
	}
	ids, sev := parseUpdateinfoList(string(list))
	pkgCVEs := parseUpdateinfoCVEs(string(cves))
	for i := range advs {
		name := advs[i].Package
		if id, ok := ids[name]; ok {
			advs[i].Advisory = id
		}
		if s, ok := sev[name]; ok {
			advs[i].Severity = s
		}
		if cs, ok := pkgCVEs[name]; ok {
			advs[i].CVEs = cs
		}
	}
}

// nevraName extracts the package name from a name-version-release.arch
// string by trimming the trailing "-version-release.arch" (dnf's check-update
// reports the name without the arch, which this matches).
func nevraName(nevra string) string {
	if i := strings.LastIndex(nevra, "-"); i > 0 {
		if j := strings.LastIndex(nevra[:i], "-"); j > 0 {
			return nevra[:j]
		}
	}
	return nevra
}

var updateinfoRe = regexp.MustCompile(`^(\S+)\s+(\S+)\s+(\S+)$`)

// parseUpdateinfoList parses "advisory  type/severity  nevra" lines into
// package → advisory id and package → severity.
func parseUpdateinfoList(out string) (ids, sev map[string]string) {
	ids, sev = map[string]string{}, map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		m := updateinfoRe.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		adv, kind, nevra := m[1], m[2], m[3]
		name := nevraName(nevra)
		if _, seen := ids[name]; !seen {
			ids[name] = adv
		}
		if s := severityOf(kind); s != "" {
			sev[name] = s
		}
	}
	return ids, sev
}

// parseUpdateinfoCVEs parses "CVE-id  type/severity  nevra" lines into
// package → sorted unique CVE ids.
func parseUpdateinfoCVEs(out string) map[string][]string {
	m := map[string]map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		g := updateinfoRe.FindStringSubmatch(strings.TrimSpace(l))
		if g == nil || !strings.HasPrefix(g[1], "CVE-") {
			continue
		}
		name := nevraName(g[3])
		if m[name] == nil {
			m[name] = map[string]bool{}
		}
		m[name][g[1]] = true
	}
	out2 := map[string][]string{}
	for name, set := range m {
		var cs []string
		for c := range set {
			cs = append(cs, c)
		}
		sort.Strings(cs)
		out2[name] = cs
	}
	return out2
}

// severityOf normalizes dnf's "Important/Sec." style type field.
func severityOf(kind string) string {
	k := strings.ToLower(kind)
	switch {
	case strings.Contains(k, "critical"):
		return "critical"
	case strings.Contains(k, "important"):
		return "important"
	case strings.Contains(k, "moderate"):
		return "moderate"
	case strings.Contains(k, "low"):
		return "low"
	}
	return ""
}

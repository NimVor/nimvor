package sysmgr

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// hostnameRef is one place a host name still appears.
type hostnameRef struct {
	Category string `json:"category"` // hosts, nginx, dns, mail
	Path     string `json:"path"`
	Sample   string `json:"sample"` // the first matching line, trimmed
}

// currentHostname reads the server's own name from /etc/hostname.
func currentHostname(env *host.Env) string {
	if b, err := env.Root.ReadFile("/etc/hostname"); err == nil {
		return strings.ToLower(strings.TrimSpace(string(b)))
	}
	return ""
}

// firstMatch returns the first line of data that contains needle (trimmed,
// capped), or "" when none does. Binary/oversized files are skipped.
func firstMatch(data []byte, needle string) string {
	if len(data) > 1<<20 {
		data = data[:1<<20]
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(strings.ToLower(line), needle) {
			line = strings.TrimSpace(line)
			if len(line) > 200 {
				line = line[:200] + "…"
			}
			return line
		}
	}
	return ""
}

// scanDir adds a finding for every file directly under dir that mentions
// needle (non-recursive is enough for WhaCP's flat config layouts; one
// level of sub-directories is also scanned for DNS zones).
func scanDir(env *host.Env, dir, category, needle string, findings *[]hostnameRef) {
	entries, err := env.Root.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			scanDir(env, p, category, needle, findings)
			continue
		}
		b, err := env.Root.ReadFile(p)
		if err != nil {
			continue
		}
		if s := firstMatch(b, needle); s != "" {
			*findings = append(*findings, hostnameRef{Category: category, Path: p, Sample: s})
		}
	}
}

// mailHostname reads Postfix's configured myhostname from main.cf.
func mailHostname(env *host.Env) string {
	b, err := env.Root.ReadFile(filepath.Join(env.M.PostfixDir, "main.cf"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if f := strings.SplitN(line, "=", 2); len(f) == 2 && strings.TrimSpace(f[0]) == "myhostname" {
			return strings.ToLower(strings.TrimSpace(f[1]))
		}
	}
	return ""
}

func registerHostnameRefs(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "server.hostname_references", Description: "Find where a previous host name still appears in the server's managed configuration: /etc/hosts, WhaCP's Nginx sites, the mail server (Postfix myhostname) and the hosted DNS zones. Read-only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Old string `json:"old"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		old := strings.ToLower(strings.TrimSpace(in.Old))
		if old == "" || len(old) > 253 || !hostnameRe.MatchString(old) {
			return nil, actions.InvalidParams("old: enter the previous host name, e.g. vmi123.example-provider.net")
		}
		findings := []hostnameRef{}
		// /etc/hosts
		if b, err := env.Root.ReadFile("/etc/hosts"); err == nil {
			if s := firstMatch(b, old); s != "" {
				findings = append(findings, hostnameRef{Category: "hosts", Path: "/etc/hosts", Sample: s})
			}
		}
		scanDir(env, env.M.NginxAvailable, "nginx", old, &findings)
		scanDir(env, env.M.DNSZoneDir, "dns", old, &findings)
		// Mail: Postfix main.cf and WhaCP's mail config tree.
		if b, err := env.Root.ReadFile(filepath.Join(env.M.PostfixDir, "main.cf")); err == nil {
			if s := firstMatch(b, old); s != "" {
				findings = append(findings, hostnameRef{Category: "mail", Path: filepath.Join(env.M.PostfixDir, "main.cf"), Sample: s})
			}
		}
		scanDir(env, env.M.MailDir, "mail", old, &findings)

		mh := mailHostname(env)
		return map[string]any{
			"old":           old,
			"current":       currentHostname(env),
			"mail_hostname": mh,
			"mail_stale":    mh != "" && mh == old,
			"findings":      findings,
			"count":         len(findings),
		}, nil
	}})
}

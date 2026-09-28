package sslmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ssl"
	"github.com/mchkhawarshahzad/whacp/agent/internal/tlsprofile"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// The certificate store (every certificate on the server and where it is
// used), the SSL hosts (every host name Nginx serves and the certificate
// that answers for it) and the server-wide TLS configuration.

// StoredCert is one certificate in the store.
type StoredCert struct {
	Source      string          `json:"source"` // letsencrypt, whacp (uploaded / made here), other (found in Nginx)
	Name        string          `json:"name"`
	Certificate ssl.Certificate `json:"certificate"`
	UsedBy      []string        `json:"used_by"` // Nginx files
	Deletable   bool            `json:"deletable"`
}

// SSLHost is one host name Nginx serves.
type SSLHost struct {
	Name        string           `json:"name"`
	File        string           `json:"file"`
	Enabled     bool             `json:"enabled"`
	HTTPS       bool             `json:"https"`
	Redirects   bool             `json:"redirects_to_https"`
	WhaCP       bool             `json:"whacp"`
	Covered     bool             `json:"covered"`
	Certificate *ssl.Certificate `json:"certificate,omitempty"`
}

func customDir(env *host.Env) string { return filepath.Join(env.Cfg.StateDir, "certs") }

// Store lists certificates and hosts.
func Store(ctx context.Context, env *host.Env) ([]StoredCert, []SSLHost) {
	now := env.Now()
	vhosts := env.Discovery.Nginx(ctx).VHosts
	usedBy := map[string][]string{}
	for _, v := range vhosts {
		if v.CertFile != "" {
			usedBy[v.CertFile] = appendOnce(usedBy[v.CertFile], filepath.Base(v.File))
		}
	}
	certs := []StoredCert{}
	seen := map[string]bool{}
	for _, le := range ssl.DiscoverLetsEncrypt(env.Root, filepath.Dir(env.M.LiveDir()), now) {
		f := le.Certificate.File
		seen[f] = true
		certs = append(certs, StoredCert{Source: "letsencrypt", Name: le.Name, Certificate: le.Certificate, UsedBy: nonNil(usedBy[f]), Deletable: len(usedBy[f]) == 0})
	}
	entries, _ := env.Root.ReadDir(customDir(env))
	for _, e := range entries {
		if !e.IsDir() || validate.Domain("name", e.Name()) != nil {
			continue
		}
		dir := filepath.Join(customDir(env), e.Name())
		f := filepath.Join(dir, "fullchain.pem")
		seen[f] = true
		certs = append(certs, StoredCert{Source: "whacp", Name: e.Name(), Certificate: ssl.Inspect(env.Root, f, filepath.Join(dir, "privkey.pem"), now), UsedBy: nonNil(usedBy[f]), Deletable: len(usedBy[f]) == 0})
	}
	// Certificates Nginx uses from elsewhere (shown, never changed).
	var other []string
	for f := range usedBy {
		if !seen[f] {
			other = append(other, f)
		}
	}
	sort.Strings(other)
	for _, f := range other {
		certs = append(certs, StoredCert{Source: "other", Name: f, Certificate: ssl.Inspect(env.Root, f, "", now), UsedBy: usedBy[f]})
	}

	inspected := map[string]ssl.Certificate{}
	for _, c := range certs {
		inspected[c.Certificate.File] = c.Certificate
	}
	hosts := []SSLHost{}
	byName := map[string]int{}
	for _, v := range vhosts {
		for _, n := range v.ServerNames {
			n = strings.ToLower(n)
			if n == "" || n == "_" || n == "localhost" || strings.ContainsAny(n, "~$*") {
				continue
			}
			h := SSLHost{Name: n, File: filepath.Base(v.File), Enabled: v.Enabled, WhaCP: strings.HasPrefix(filepath.Base(v.File), "whacp-")}
			if i, ok := byName[n]; ok {
				h = hosts[i]
			}
			h.Redirects = h.Redirects || v.RedirectHTTPS
			if v.SSL && v.CertFile != "" {
				c, ok := inspected[v.CertFile]
				if !ok {
					c = ssl.Inspect(env.Root, v.CertFile, "", now)
					inspected[v.CertFile] = c
				}
				h.HTTPS, h.Certificate, h.Covered = true, &c, c.Covers(n)
				h.File = filepath.Base(v.File)
			}
			if i, ok := byName[n]; ok {
				hosts[i] = h
			} else {
				byName[n] = len(hosts)
				hosts = append(hosts, h)
			}
		}
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].Name < hosts[j].Name })
	return certs, hosts
}

func appendOnce(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func registerStore(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "ssl.store", Description: "Every certificate on the server (Let's Encrypt, uploaded or made here, and others Nginx uses) with where it is used, and every host name Nginx serves with the certificate that answers for it. No private keys.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		certs, hosts := Store(ctx, env)
		return map[string]any{"certificates": certs, "hosts": hosts}, nil
	}})

	r.Register(actions.Action{Name: "ssl.custom_delete", Mutating: true, Description: "Delete an uploaded (or installed from a server-made key) certificate and its key copy that no Nginx configuration uses.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Name string `json:"name"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("name", in.Name); err != nil {
			return nil, host.Invalid(err)
		}
		dir := filepath.Join(customDir(env), in.Name)
		if !env.Root.Exists(filepath.Join(dir, "fullchain.pem")) {
			return nil, actions.NotFound("That certificate does not exist.")
		}
		if users := referencing(ctx, env, dir); len(users) > 0 {
			return nil, actions.Conflict("The certificate is still used by: " + strings.Join(users, ", ") + ". Switch those websites to another certificate first.")
		}
		for _, f := range []string{"fullchain.pem", "privkey.pem"} {
			if _, err := env.Writer.Remove(filepath.Join(dir, f)); err != nil {
				return nil, actions.Failed("The certificate was not deleted completely.", err.Error())
			}
		}
		if real, err := env.Root.Path(dir); err == nil {
			os.Remove(real)
		}
		return map[string]any{"deleted": in.Name}, nil
	}})

	r.Register(actions.Action{Name: "ssl.tls_get", Description: "The server-wide TLS configuration WhaCP websites and webmail use, the snippet, and how many WhaCP virtual hosts still carry their own TLS lines.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		p := tlsprofile.Load(env)
		files, legacy := whacpVhosts(env)
		return map[string]any{"profile": p, "snippet": tlsprofile.Render(p), "path": tlsprofile.Path(env), "vhosts": len(files), "legacy_vhosts": legacy}, nil
	}})

	r.Register(actions.Action{Name: "ssl.tls_set", Mutating: true, Description: "Set the server-wide TLS configuration: writes the snippet every WhaCP virtual host includes (moving vhosts that carry their own TLS lines to the include), checks with nginx -t and reloads; restores everything if the check or reload fails.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var p tlsprofile.Profile
		if err := host.Decode(raw, &p); err != nil {
			return nil, err
		}
		if err := p.Validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		unlock := env.LockWebConfig()
		defer unlock()
		set := env.Changes("tls-profile")
		tlsprofile.Save(env, set, p)
		files, _ := whacpVhosts(env)
		migrated := 0
		for _, f := range files {
			b, err := env.Root.ReadFile(f)
			if err != nil || !strings.Contains(string(b), tlsprofile.LegacyBlock) {
				continue
			}
			set.Write(f, []byte(strings.ReplaceAll(string(b), tlsprofile.LegacyBlock, tlsprofile.Include(env))), 0o644, agentfs.RootOwner)
			migrated++
		}
		applied, err := set.Commit(func() error { return env.NginxTest(ctx) })
		if err != nil {
			return nil, host.FailWithValidation("The TLS configuration was not applied", err)
		}
		if len(applied.Changed) > 0 {
			if err := env.ReloadNginx(ctx); err != nil {
				applied.Rollback()
				env.ReloadNginx(ctx)
				return nil, host.FailWithValidation("Nginx did not reload; the previous TLS configuration was restored", err)
			}
		}
		return map[string]any{"profile": p, "migrated_vhosts": migrated, "changed": len(applied.Changed) > 0}, nil
	}})
}

// whacpVhosts lists WhaCP's own Nginx files and how many still carry the
// old inline TLS lines.
func whacpVhosts(env *host.Env) ([]string, int) {
	files, _ := env.Root.Glob(filepath.Join(env.M.NginxAvailable, "whacp-*.conf"))
	legacy := 0
	for _, f := range files {
		if b, err := env.Root.ReadFile(f); err == nil && strings.Contains(string(b), tlsprofile.LegacyBlock) {
			legacy++
		}
	}
	return files, legacy
}

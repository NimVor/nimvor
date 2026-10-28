package webprotect

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/shacrypt"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

const maxProtected = 20

// Register adds the protect.* actions.
func Register(r *actions.Registry, e *host.Env) {
	AllowPaths(e)
	r.Register(actions.Action{Name: "protect.status", Description: "A website's protection: password-protected paths (user names only), hotlink protection and WordPress web server rules.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, host.Invalid(err)
		}
		return Status(e, in.Domain), nil
	}})
	r.Register(actions.Action{Name: "protect.password", Mutating: true, Description: "Turn password protection (HTTP basic authentication) of a website path on or off. The password is stored only as a SHA-512 crypt hash. Checked with nginx -t, rolled back on failure.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain   string `json:"domain"`
			Path     string `json:"path"`
			Enabled  bool   `json:"enabled"`
			User     string `json:"user"`
			Password string `json:"password"`
			Realm    string `json:"realm"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, host.Invalid(err)
		}
		path, err := CleanPath(in.Path)
		if err != nil {
			return nil, actions.InvalidParams("path: " + err.Error())
		}
		if in.Realm == "" {
			in.Realm = "Restricted area"
		}
		if in.Enabled {
			if !ValidUser(in.User) {
				return nil, actions.InvalidParams("user: 1 to 64 letters, digits, dots, dashes or underscores")
			}
			if len(in.Password) < 8 || len(in.Password) > 128 || strings.ContainsAny(in.Password, "\x00\n\r") {
				return nil, actions.InvalidParams("password: 8 to 128 characters")
			}
			if !ValidRealm(in.Realm) {
				return nil, actions.InvalidParams("realm: letters, digits, spaces and . , ( ) - only")
			}
		}
		s, err := Update(ctx, e, in.Domain, func(s *Site) error {
			i := indexOf(s.Password, path)
			if !in.Enabled {
				if i >= 0 {
					s.Password = append(s.Password[:i], s.Password[i+1:]...)
				}
				return nil
			}
			entry := Protected{Path: path, Realm: in.Realm, User: in.User, Hash: shacrypt.HashPassword(in.Password)}
			if i >= 0 {
				s.Password[i] = entry
				return nil
			}
			if len(s.Password) >= maxProtected {
				return actions.Conflict("A website can have at most 20 password-protected paths.")
			}
			s.Password = append(s.Password, entry)
			return nil
		})
		if err != nil {
			return nil, err
		}
		_ = s
		return Status(e, in.Domain), nil
	}})
	r.Register(actions.Action{Name: "protect.hotlink", Mutating: true, Description: "Turn hotlink protection of a website on or off: its images and media are only served to its own pages, direct visits and the allowed sites. Checked with nginx -t, rolled back on failure.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain     string   `json:"domain"`
			Enabled    bool     `json:"enabled"`
			Allowed    []string `json:"allowed"`
			Extensions []string `json:"extensions"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, host.Invalid(err)
		}
		var h *Hotlink
		if in.Enabled {
			h = &Hotlink{Allowed: []string{}, Extensions: []string{}}
			if len(in.Allowed) > 50 || len(in.Extensions) > 40 {
				return nil, actions.InvalidParams("at most 50 allowed sites and 40 file types")
			}
			seen := map[string]bool{}
			for _, a := range in.Allowed {
				a = strings.ToLower(strings.TrimSpace(a))
				if a == "" || seen[a] {
					continue
				}
				if !ValidHost(a) {
					return nil, actions.InvalidParams("allowed: " + a + " is not a host name (a leading *. is allowed)")
				}
				seen[a] = true
				h.Allowed = append(h.Allowed, a)
			}
			for _, x := range in.Extensions {
				x = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(x), "."))
				if x == "" || seen["."+x] {
					continue
				}
				if !ValidExtension(x) || x == "php" || strings.HasPrefix(x, "php") || x == "html" || x == "htm" {
					return nil, actions.InvalidParams("extensions: " + x + " is not a media file type")
				}
				seen["."+x] = true
				h.Extensions = append(h.Extensions, x)
			}
		}
		if _, err := Update(ctx, e, in.Domain, func(s *Site) error { s.Hotlink = h; return nil }); err != nil {
			return nil, err
		}
		return Status(e, in.Domain), nil
	}})
}

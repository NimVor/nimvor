package ftpmgr

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// registerShell adds the shell access actions (the panel lets only
// administrators call shell.set).
func registerShell(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "shell.status", Description: "Shell access of the website accounts (disabled, jailed or normal; in effect while their SSH/SFTP login is on) and whether jailed shells can work here.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		st := loadState(env)
		all, err := env.Accounts()
		if err != nil {
			return nil, err
		}
		list := []map[string]any{}
		for _, u := range all {
			if u.UID < 1000 || filepath.Dir(filepath.Clean(u.Home)) != filepath.Clean(env.Cfg.SitesRoot) {
				continue
			}
			sftp, on := st.Users[u.Name]
			mode := st.Shells[u.Name]
			if mode == "" {
				mode = ShellDisabled
			}
			list = append(list, map[string]any{
				"user": u.Name, "folder": filepath.Base(u.Home), "mode": mode, "effective": shellMode(st, u.Name),
				"ssh_login": on, "keys": len(sftp.Keys), "password_login": on && sftp.PasswordLogin, "login_shell": u.Shell,
			})
		}
		sort.Slice(list, func(i, j int) bool { return list[i]["user"].(string) < list[j]["user"].(string) })
		return map[string]any{"accounts": list, "jail_problem": jailProblem(env), "userns_restricted": usernsRestricted(env)}, nil
	}})

	r.Register(actions.Action{Name: "shell.set", Mutating: true, Description: "Set the shell access of website accounts: disabled (SFTP only), jailed (a bubblewrap sandbox with only the account's website folder writable) or normal (/bin/bash). Applies while an account's SSH/SFTP login is on. Validated with sshd -t, verified with sshd -T, rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Users []string `json:"users"`
			Mode  string   `json:"mode"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if in.Mode != ShellDisabled && in.Mode != ShellJailed && in.Mode != ShellNormal {
			return nil, actions.InvalidParams("mode must be disabled, jailed or normal")
		}
		if len(in.Users) == 0 || len(in.Users) > 1000 {
			return nil, actions.InvalidParams("users: 1 to 1000 accounts")
		}
		mu.Lock()
		defer mu.Unlock()
		st := loadState(env)
		seen := map[string]bool{}
		names := []string{}
		for _, name := range in.Users {
			if seen[name] {
				continue
			}
			seen[name] = true
			if _, err := siteUser(env, name); err != nil {
				return nil, err
			}
			names = append(names, name)
			if in.Mode == ShellDisabled {
				delete(st.Shells, name)
			} else {
				st.Shells[name] = in.Mode
			}
		}
		if in.Mode == ShellJailed {
			if p := jailProblem(env); p != "" {
				return nil, actions.Conflict(p)
			}
		}
		if len(st.Users) > 0 {
			if err := chrootReady(env); err != nil {
				return nil, actions.Conflict(err.Error())
			}
		}
		if err := applySSH(ctx, env, st, names, "", ""); err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, name := range names {
			out = append(out, map[string]any{"user": name, "mode": in.Mode, "effective": shellMode(st, name)})
		}
		return map[string]any{"accounts": out}, nil
	}})
}

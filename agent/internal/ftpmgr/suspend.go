package ftpmgr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Account suspension of a website's system account: the account expires
// (usermod --expiredate 1), so SSH and SFTP refuse it, keys included (the
// PAM account check), and its FTP accounts' password hashes are locked
// with a "!" prefix, which no password matches. Files stay as they are.

// lockPrefix marks a locked FTP password hash.
const lockPrefix = "!"

// accountSuspended reports whether a system account is expired by
// suspension (shadow's expiry field is day 1 of 1970).
func accountSuspended(env *host.Env, user string) bool {
	b, err := env.Root.ReadFile("/etc/shadow")
	if err != nil {
		return false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) >= 8 && f[0] == user {
			return f[7] == "1"
		}
	}
	return false
}

func lockHash(h string, locked bool) string {
	h = strings.TrimPrefix(h, lockPrefix)
	if locked {
		return lockPrefix + h
	}
	return h
}

func registerSuspend(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "account.suspend", Mutating: true, Description: "Suspend or resume a website's system account: SSH/SFTP logins are refused (the account expires) and its FTP accounts are locked. Files are kept.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			User      string `json:"user"`
			Suspended bool   `json:"suspended"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		u, err := siteUser(env, in.User)
		if err != nil {
			return nil, err
		}
		expire := ""
		if in.Suspended {
			expire = "1"
		}
		if out, err := env.Runner.Run(ctx, "usermod", "--expiredate", expire, "--", u.Name); err != nil {
			return nil, actions.Failed("The system account "+u.Name+" could not be "+map[bool]string{true: "suspended", false: "resumed"}[in.Suspended]+".", host.Output(out))
		}
		if in.Suspended {
			credentialsChanged(u.Name)
		}
		p := ftpPaths(env)
		locked := 0
		if env.Root.Exists(p.passwd) {
			list := loadAccounts(env, p)
			changed := false
			for i, a := range list {
				if a.UID != u.UID {
					continue
				}
				if h := lockHash(a.hash, in.Suspended); h != a.hash {
					list[i].hash = h
					changed = true
				}
				locked++
			}
			if changed {
				if err := saveAccounts(ctx, env, p, list); err != nil {
					// Leave the account as it was.
					prev := "1"
					if in.Suspended {
						prev = ""
					}
					env.Runner.Run(ctx, "usermod", "--expiredate", prev, "--", u.Name)
					return nil, err
				}
			}
		}
		return map[string]any{"user": u.Name, "suspended": in.Suspended, "ftp_accounts": locked}, nil
	}})
}

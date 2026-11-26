package dbtools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/dbmgr"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// One-click sign-in. The panel asks for a sign-in to one database; the agent prepares a sign-in account of that
// database only (see dbmgr.ToolSignon), writes a one-time token file that only the tools' account can read, and the
// panel sends the browser to the tool's nimvor-signon.php with the token. That page claims the token file (it can
// be used once, for tokenTTL), starts the tool's session with the account and forwards to the tool. The account is
// locked again by dbtools.signon_sweep once sessionTTL has passed.

const (
	tokenTTL   = time.Minute
	sessionTTL = 2 * time.Hour
)

func (e env) signonDir() string  { return Dir + "/tmp/signon" }
func (e env) signonPath() string { return Dir + "/signon.json" }

type signonState struct {
	// Accounts maps a sign-in account to when it must be locked (unix seconds).
	Accounts map[string]int64 `json:"accounts"`
}

func (e env) loadSignon() signonState {
	s := signonState{Accounts: map[string]int64{}}
	if b, err := e.Root.ReadFile(e.signonPath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.Accounts == nil {
		s.Accounts = map[string]int64{}
	}
	return s
}

func (e env) saveSignon(s signonState) error {
	b, _ := json.Marshal(s)
	_, err := e.Writer.WriteFile(e.signonPath(), b, 0o600, agentfs.RootOwner)
	return err
}

func registerSignon(r *actions.Registry, e env) {
	c := dbmgr.Client{Env: e.Env}

	r.Register(actions.Action{Name: "dbtools.signon", Mutating: true, Description: "Prepare a one-click sign-in of an installed database tool to one database: a sign-in account with privileges on that database only (locked again after two hours), and a one-time link token that expires after a minute.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		in := struct {
			Tool     string `json:"tool"`
			Database string `json:"database"`
		}{}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		t, ok := toolByID(in.Tool)
		if !ok {
			return nil, actions.InvalidParams("tool must be phpmyadmin or adminer")
		}
		mu.Lock()
		defer mu.Unlock()
		st := e.load()
		if _, installed := st.Tools[t.ID]; !installed {
			return nil, actions.Conflict(t.Name + " is not installed.")
		}
		acct, err := e.LookupUser(account)
		if err != nil {
			return nil, actions.Failed("The database tools account is missing.", err.Error())
		}
		own := agentfs.Owner{UID: acct.UID, GID: acct.GID}
		if _, err := e.Writer.MkdirAll(e.signonDir(), 0o700, own); err != nil {
			return nil, actions.Failed("The sign-in folder could not be created.", err.Error())
		}
		user, password, err := c.ToolSignon(ctx, in.Database)
		if err != nil {
			return nil, err
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		token := hex.EncodeToString(b)
		payload, _ := json.Marshal(map[string]any{"user": user, "password": password, "db": in.Database, "exp": e.Now().Add(tokenTTL).Unix()})
		if _, err := e.Writer.WriteFile(e.signonDir()+"/"+token+".json", payload, 0o600, own); err != nil {
			return nil, actions.Failed("The sign-in token could not be written.", err.Error())
		}
		ss := e.loadSignon()
		ss.Accounts[user] = e.Now().Add(sessionTTL).Unix()
		if err := e.saveSignon(ss); err != nil {
			return nil, actions.Failed("The sign-in state could not be saved.", err.Error())
		}
		return map[string]any{"path": "/dbtools/" + t.ID + "/nimvor-signon.php?t=" + token, "expires_in": int(tokenTTL.Seconds())}, nil
	}})

	r.Register(actions.Action{Name: "dbtools.signon_sweep", Mutating: true, Description: "Lock the database tools' sign-in accounts whose time is up and remove unused sign-in tokens. Called every few minutes by the panel's scheduler.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := host.Decode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		return e.sweep(ctx, c, false)
	}})
}

// sweep locks the accounts that are due (all of them when force is set) and deletes stale token files.
func (e env) sweep(ctx context.Context, c dbmgr.Client, force bool) (map[string]any, error) {
	ss := e.loadSignon()
	now := e.Now().Unix()
	locked, failed := 0, 0
	for user, exp := range ss.Accounts {
		if !force && exp > now {
			continue
		}
		if err := c.LockToolAccount(ctx, user); err != nil {
			e.Log.Warn("a database tools sign-in account could not be locked", "account", user, "err", err)
			failed++
			continue
		}
		delete(ss.Accounts, user)
		locked++
	}
	if locked > 0 {
		if err := e.saveSignon(ss); err != nil {
			return nil, actions.Failed("The sign-in state could not be saved.", err.Error())
		}
	}
	// Tokens nobody used: gone after ten minutes (a used one is deleted by the sign-in page).
	removed := 0
	if real, err := e.Root.Path(e.signonDir()); err == nil {
		if entries, err := os.ReadDir(real); err == nil {
			for _, en := range entries {
				info, err := en.Info()
				if err != nil || en.IsDir() {
					continue
				}
				if time.Since(info.ModTime()) > 10*time.Minute {
					if os.Remove(filepath.Join(real, en.Name())) == nil {
						removed++
					}
				}
			}
		}
	}
	return map[string]any{"locked": locked, "failed": failed, "tokens_removed": removed}, nil
}

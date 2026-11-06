package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/backup"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// reserveKB is the free space a migration always leaves (the same reserve backups keep).
const reserveKB = backup.Reserve >> 10

// freeKB is replaced in tests.
var freeKB = func(path string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 1 << 40 // not measurable: not blocked
	}
	return int64(st.Bavail) * int64(st.Bsize) / 1024
}

// Register adds the migration actions.
func Register(r *actions.Registry, env *host.Env) {
	e := NewEngine(env)
	e.Reconcile()
	dec := func(p json.RawMessage, v any) error { return host.Decode(p, v) }
	invalid := func(err error) error { return actions.InvalidParams(err.Error()) }

	r.Register(actions.Action{Name: "migrate.panels", Description: "The source panels the migration wizard can read, and how far each is proven.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return map[string]any{"panels": Supported()}, nil
	}})

	r.Register(actions.Action{Name: "migrate.host_key", Timeout: 40 * time.Second, Description: "Ask a source server for its SSH host key so the person can confirm it. No login is sent.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		if err := dec(p, &in); err != nil {
			return nil, err
		}
		s := Source{Host: in.Host, Port: in.Port, User: "probe", Password: "probe"}
		if err := s.Validate(false); err != nil {
			return nil, invalid(err)
		}
		keys, err := backup.ScanHostKeys(ctx, env, backup.Remote{Type: "sftp", Host: s.Host, Port: s.Port})
		if err != nil {
			return nil, actions.Failed("The server could not be reached.", err.Error())
		}
		key := keys[0]
		for _, kind := range []string{"ssh-ed25519 ", "ecdsa-sha2-nistp256 ", "ssh-rsa "} {
			found := false
			for _, k := range keys {
				if strings.HasPrefix(k, kind) {
					key, found = k, true
					break
				}
			}
			if found {
				break
			}
		}
		return map[string]any{"host": s.Host, "port": s.Port, "host_key": key, "key_type": strings.Fields(key)[0], "fingerprint": backup.Fingerprint(key)}, nil
	}})

	connect := func(raw json.RawMessage) (Puller, Source, error) {
		var in struct {
			Source Source `json:"source"`
		}
		if err := dec(raw, &in); err != nil {
			return nil, Source{}, err
		}
		t, err := e.Open(in.Source)
		if err != nil {
			return nil, in.Source, invalid(err)
		}
		return t, in.Source, nil
	}

	r.Register(actions.Action{Name: "migrate.test", Timeout: 90 * time.Second, Description: "Connect to a source server over SSH (host key pinned), identify its system, panel, PHP, database engine and disk. Reads only.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		t, src, err := connect(p)
		if err != nil {
			return nil, err
		}
		info, err := Inspect(ctx, t)
		if err != nil {
			return nil, actions.Failed("The source server could not be read.", redact(err.Error(), src))
		}
		_, det, err := DetectPanel(ctx, t, src.Panel)
		if err != nil {
			return map[string]any{"server": info, "panel": nil, "panel_error": err.Error()}, nil
		}
		return map[string]any{"server": info, "panel": det}, nil
	}})

	r.Register(actions.Action{Name: "migrate.discover", Description: "Discover the accounts, domains, databases, mailboxes and cron jobs of a source server (job; reads only). The result is the job's result.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		t, src, err := connect(p)
		if err != nil {
			return nil, err
		}
		j := env.Jobs.Start("migrate.discover", "migrate:discover:"+src.Host, func(ctx context.Context, log io.Writer) (any, error) {
			d, err := DiscoverAll(ctx, t, src.Panel, func(s string) { fmt.Fprintln(log, s) })
			if err != nil {
				return nil, fmt.Errorf("%s", redact(err.Error(), src))
			}
			return d, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})

	r.Register(actions.Action{Name: "migrate.assess", Description: "Check a migration plan against this server (disk, temporary space, PHP, database engine) and the discovery. Reads only; contacts no server.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Discovery Discovery `json:"discovery"`
			Plan      Plan      `json:"plan"`
		}
		if err := dec(p, &in); err != nil {
			return nil, err
		}
		if err := in.Plan.Validate(env); err != nil {
			return nil, invalid(err)
		}
		dest := destInfo(ctx, env)
		dest.FreeKB = freeKB(env.Cfg.SitesRoot) - reserveKB
		temp := in.Plan.Config.TempDir
		if temp == "" {
			temp = env.Cfg.StateDir
		}
		dest.TempKB = freeKB(temp) - reserveKB
		f := Assess(&in.Discovery, &in.Plan, dest)
		return map[string]any{"findings": f, "blocked": Blocking(f), "dest": dest}, nil
	}})

	r.Register(actions.Action{Name: "migrate.create", Mutating: true, Description: "Store a migration plan whose destinations are existing NimVor websites and databases. Starts nothing.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Plan   Plan   `json:"plan"`
			Panel  string `json:"panel"`
			Source string `json:"source"`
		}
		if err := dec(p, &in); err != nil {
			return nil, err
		}
		m, err := e.Create(in.Plan, in.Panel, in.Source)
		if err != nil {
			return nil, invalid(err)
		}
		return m, nil
	}})

	start := func(name, what string) {
		r.Register(actions.Action{Name: name, Mutating: true, Description: what, Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
			var in struct {
				ID     string `json:"id"`
				Source Source `json:"source"`
			}
			if err := dec(p, &in); err != nil {
				return nil, err
			}
			m, err := e.Store.Load(in.ID)
			if err != nil {
				return nil, actions.NotFound(err.Error())
			}
			if m.Status == Completed || m.Status == Cancelled {
				return nil, actions.InvalidParams("this migration is " + m.Status)
			}
			if err := in.Source.Validate(true); err != nil {
				return nil, invalid(err)
			}
			src := in.Source
			j := env.Jobs.Start("migrate.run", "migrate:"+in.ID, func(ctx context.Context, log io.Writer) (any, error) {
				if err := e.Run(ctx, in.ID, src, log); err != nil {
					return nil, fmt.Errorf("%s", redact(err.Error(), src))
				}
				return e.Store.Load(in.ID)
			})
			return map[string]any{"job_id": j.ID, "id": in.ID}, nil
		}})
	}
	start("migrate.start", "Run a stored migration (job). The login is used for this run only and is not stored.")
	start("migrate.resume", "Continue a paused, failed or interrupted migration (job): tasks that are done are not repeated. The login is given again and not stored.")

	stop := func(name, why, what string) {
		r.Register(actions.Action{Name: name, Mutating: true, Description: what, Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
			var in struct {
				ID string `json:"id"`
			}
			if err := dec(p, &in); err != nil {
				return nil, err
			}
			if err := e.Stop(in.ID, why); err != nil {
				return nil, actions.InvalidParams(err.Error())
			}
			return map[string]any{"id": in.ID, "stopping": why}, nil
		}})
	}
	stop("migrate.pause", Paused, "Ask a running migration to pause at the next safe point; continue it later.")
	stop("migrate.cancel", Cancelled, "Cancel a migration. Copied files and imported databases stay; nothing is removed from the source or the destination.")

	r.Register(actions.Action{Name: "migrate.rollback", Mutating: true, Timeout: 10 * time.Minute, Description: "Undo a migration's changes in this server: remove the files it created while they are unchanged, empty the databases it filled only when told to and only if nothing was written to them since. Newer data is never removed; nothing on the source is touched.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			ID             string `json:"id"`
			Confirm        bool   `json:"confirm"`
			EmptyDatabases bool   `json:"empty_databases"`
		}
		if err := dec(p, &in); err != nil {
			return nil, err
		}
		if !in.Confirm {
			return nil, actions.InvalidParams("confirm: a rollback removes files from this server and must be confirmed")
		}
		var log strings.Builder
		rep, err := e.Rollback(ctx, in.ID, in.EmptyDatabases, &log)
		if err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		return rep, nil
	}})
	r.Register(actions.Action{Name: "migrate.get", Description: "The state of one migration: its tasks, their status, and what remains to be done by hand.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := dec(p, &in); err != nil {
			return nil, err
		}
		m, err := e.Store.Load(in.ID)
		if err != nil {
			return nil, actions.NotFound(err.Error())
		}
		return m, nil
	}})
	r.Register(actions.Action{Name: "migrate.list", Description: "Every migration on this server, newest first.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return map[string]any{"migrations": e.Store.List()}, nil
	}})
}

// redact removes the login from a message that is going to be shown.
func redact(s string, src Source) string {
	if src.Password != "" {
		s = strings.ReplaceAll(s, src.Password, "***")
	}
	return s
}

// destInfo reads this server's own facts: the PHP versions installed and the database server.
func destInfo(ctx context.Context, env *host.Env) DestInfo {
	d := DestInfo{Reserve: reserveKB}
	for _, v := range []string{"7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"} {
		if env.Runner.Available("php" + v) {
			d.PHP = append(d.PHP, v)
		}
	}
	for _, c := range []string{"mariadb", "mysql"} {
		if !env.Runner.Available(c) {
			continue
		}
		if out, err := env.Runner.Run(ctx, c, "--version"); err == nil {
			i := parseInfo("##mysql\n" + strings.TrimSpace(string(out)) + "\n")
			if i.Engine != "" {
				d.Engine, d.MySQL = i.Engine, i.MySQL
				break
			}
		}
	}
	return d
}

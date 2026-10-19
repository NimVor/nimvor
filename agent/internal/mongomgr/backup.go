package mongomgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Backups are gzip-compressed mongodump archives kept under the agent's backup
// directory. The file name is "<timestamp>_<db>.archive.gz": the timestamp is a
// fixed 15 characters ("20060102-150405", no underscore), so the database name
// — which may itself contain underscores — is recovered unambiguously as
// everything between the first underscore and the ".archive.gz" suffix.
const backupSubdir = "mongo"

const backupStamp = "20060102-150405"

var backupFileRe = regexp.MustCompile(`^\d{8}-\d{6}_[A-Za-z0-9_][A-Za-z0-9_.-]{0,80}\.archive\.gz$`)

// backupDir is the host path of the MongoDB backup directory.
func backupDir(env *host.Env) string {
	return filepath.Join(env.Cfg.BackupDir, backupSubdir)
}

// connArgs returns the mongodump/mongorestore connection flags: the admin URI
// when authentication is enabled, otherwise nothing (the local default).
func (c Client) connArgs() []string {
	if uri, err := c.Env.Root.ReadFile(AdminURIFile); err == nil {
		if u := strings.TrimSpace(string(uri)); u != "" {
			return []string{"--uri=" + u}
		}
	}
	return nil
}

func registerBackup(r *actions.Registry, env *host.Env, c Client) {
	r.Register(actions.Action{Name: "mongo.backup", Mutating: true, Description: "Back up one database to a gzip mongodump archive (async job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			DB string `json:"db"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !nameRe.MatchString(in.DB) {
			return nil, actions.InvalidParams("db: invalid database name")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		dir := backupDir(env)
		name := env.Now().Format(backupStamp) + "_" + in.DB + ".archive.gz"
		path := filepath.Join(dir, name)
		j := env.Jobs.Start("mongo.backup", "server:mongo", func(ctx context.Context, log io.Writer) (any, error) {
			if _, err := env.Writer.MkdirAll(dir, 0o700, agentfs.RootOwner); err != nil {
				return nil, fmt.Errorf("the backup directory could not be created: %w", err)
			}
			fmt.Fprintf(log, "Backing up %s to %s…\n", in.DB, name)
			args := append(c.connArgs(), "--db="+in.DB, "--archive="+path, "--gzip")
			out, err := env.Runner.RunWith(ctx, "mongodump", execx.Opts{Timeout: 30 * time.Minute, Stdout: log}, args...)
			if err != nil {
				return nil, fmt.Errorf("mongodump failed: %w: %s", err, lastLine(out))
			}
			size := int64(0)
			if fi, err := env.Root.Stat(path); err == nil {
				size = fi.Size()
			}
			fmt.Fprintf(log, "  ✓ backup complete (%d bytes)\n", size)
			return map[string]any{"file": name, "db": in.DB, "size": size}, nil
		})
		return map[string]any{"job_id": j.ID, "file": name}, nil
	}})

	r.Register(actions.Action{Name: "mongo.backups", Description: "List the MongoDB backup archives (optionally for one database).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			DB string `json:"db"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.DB != "" && !nameRe.MatchString(in.DB) {
			return nil, actions.InvalidParams("db: invalid database name")
		}
		dir := backupDir(env)
		backups := []map[string]any{}
		entries, err := env.Root.ReadDir(dir)
		if err != nil {
			// No directory yet simply means no backups have been taken.
			return map[string]any{"backups": backups}, nil
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !backupFileRe.MatchString(name) {
				continue
			}
			db := name[len(backupStamp)+1 : len(name)-len(".archive.gz")]
			if in.DB != "" && db != in.DB {
				continue
			}
			size, created := int64(0), ""
			if fi, err := env.Root.Stat(filepath.Join(dir, name)); err == nil {
				size = fi.Size()
			}
			if t, err := time.Parse(backupStamp, name[:len(backupStamp)]); err == nil {
				created = t.UTC().Format(time.RFC3339)
			}
			backups = append(backups, map[string]any{"file": name, "db": db, "size": size, "created": created})
		}
		return map[string]any{"backups": backups}, nil
	}})

	r.Register(actions.Action{Name: "mongo.restore", Mutating: true, Description: "Restore one database from a gzip mongodump archive (async job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			File string `json:"file"`
			DB   string `json:"db"`
			Drop bool   `json:"drop"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !backupFileRe.MatchString(in.File) {
			return nil, actions.InvalidParams("file: not a backup archive name")
		}
		if !nameRe.MatchString(in.DB) {
			return nil, actions.InvalidParams("db: invalid database name")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		path := filepath.Join(backupDir(env), in.File)
		if !env.Root.Exists(path) {
			return nil, actions.Conflict("That backup archive does not exist.")
		}
		drop := in.Drop
		db := in.DB
		j := env.Jobs.Start("mongo.restore", "server:mongo", func(ctx context.Context, log io.Writer) (any, error) {
			fmt.Fprintf(log, "Restoring %s from %s…\n", db, in.File)
			// nsInclude confines the restore to the chosen database even if the
			// archive happens to hold more.
			args := append(c.connArgs(), "--archive="+path, "--gzip", "--nsInclude="+db+".*")
			if drop {
				args = append(args, "--drop")
			}
			out, err := env.Runner.RunWith(ctx, "mongorestore", execx.Opts{Timeout: 30 * time.Minute, Stdout: log}, args...)
			if err != nil {
				return nil, fmt.Errorf("mongorestore failed: %w: %s", err, lastLine(out))
			}
			fmt.Fprintln(log, "  ✓ restore complete")
			return map[string]any{"db": db, "file": in.File}, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})

	r.Register(actions.Action{Name: "mongo.backup_delete", Mutating: true, Description: "Delete one MongoDB backup archive.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			File string `json:"file"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !backupFileRe.MatchString(in.File) {
			return nil, actions.InvalidParams("file: not a backup archive name")
		}
		path := filepath.Join(backupDir(env), in.File)
		if _, err := env.Writer.Remove(path); err != nil {
			return nil, actions.Failed("The backup could not be deleted.", err.Error())
		}
		return map[string]any{"deleted": in.File}, nil
	}})
}

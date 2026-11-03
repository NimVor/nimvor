package pgmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Backups are pg_dump custom-format archives under <backup_dir>/postgres, named "<timestamp>_<database>.dump" with a
// "<name>.sha256" next to each. The dump and the restore run as the database's own query account acting as its owner
// (never as the management role), so a backup holds exactly what the owner can read and a restore can only touch the
// owner's own objects.
const (
	backupSubdir = "postgres"
	backupStamp  = "20060102-150405"
)

var backupFileRe = regexp.MustCompile(`^\d{8}-\d{6}_[a-z_][a-z0-9_]{0,62}\.dump$`)

func backupDir(env *host.Env) string { return filepath.Join(env.Cfg.BackupDir, backupSubdir) }

// backupDirFor is where the backups of one server are kept. The local server keeps them in the folder itself; every
// registered server gets its own sub-folder, so two servers that both have a database of the same name never share a
// list of archives (a restore could otherwise load the other server's data).
func backupDirFor(env *host.Env, t *Target) string {
	if t == nil || !t.remote() {
		return backupDir(env)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(t.Host) + ":" + strconv.Itoa(t.Port)))
	return filepath.Join(backupDir(env), "remote-"+hex.EncodeToString(sum[:])[:12])
}

func backupDB(file string) string { return file[len(backupStamp)+1 : len(file)-len(".dump")] }

// clientEnv is the libpq environment for pg_dump / pg_restore as the given login (the password never appears in the
// command line). The returned cleanup removes the temporary CA file.
func clientEnv(t Target, owner string) ([]string, []string, func(), error) {
	host := t.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := t.Port
	if port == 0 {
		port = 5432
	}
	cleanup := func() {}
	envv := []string{"PGPASSWORD=" + t.Password, "PGCONNECT_TIMEOUT=10", "PGOPTIONS=-c lock_timeout=30s"}
	mode := t.SSLMode
	if mode == "" {
		mode = "prefer"
		if !t.remote() {
			mode = "disable"
		}
	}
	envv = append(envv, "PGSSLMODE="+mode)
	if t.CAPEM != "" {
		f, err := os.CreateTemp("", "nimvor-pg-ca-*.pem")
		if err != nil {
			return nil, nil, cleanup, err
		}
		cleanup = func() { os.Remove(f.Name()) }
		if _, err := f.WriteString(t.CAPEM); err != nil {
			f.Close()
			cleanup()
			return nil, nil, func() {}, err
		}
		f.Close()
		envv = append(envv, "PGSSLROOTCERT="+f.Name())
	}
	args := []string{"--host=" + host, "--port=" + strconv.Itoa(port), "--username=" + t.User, "--no-password", "--role=" + owner}
	return envv, args, cleanup, nil
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func lastLines(b []byte, n int) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// connect returns the query-account target and the owner of a database.
func queryTarget(ctx context.Context, env *host.Env, t *Target, database string) (Target, string, error) {
	s, err := openQuery(ctx, env, t, database, false)
	if err != nil {
		return Target{}, "", err
	}
	defer s.Close(ctx)
	var owner string
	if err := s.QueryRow(ctx, `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = current_database()`).Scan(&owner); err != nil {
		return Target{}, "", err
	}
	return s.target, owner, nil
}

// dumpTo writes a verified backup of database and returns its file name, size and checksum.
func dumpTo(ctx context.Context, env *host.Env, t *Target, database string, log io.Writer) (map[string]any, error) {
	tgt, owner, err := queryTarget(ctx, env, t, database)
	if err != nil {
		return nil, err
	}
	dir := backupDirFor(env, t)
	if _, err := env.Writer.MkdirAll(dir, 0o700, agentfs.RootOwner); err != nil {
		return nil, fmt.Errorf("the backup directory could not be created: %w", err)
	}
	// One backup per second and database: never overwrite an existing archive (a restore takes a safety backup
	// right after the one it restores).
	stamp := env.Now().UTC()
	name := stamp.Format(backupStamp) + "_" + database + ".dump"
	for env.Root.Exists(filepath.Join(dir, name)) {
		stamp = stamp.Add(time.Second)
		name = stamp.Format(backupStamp) + "_" + database + ".dump"
	}
	real, err := env.Root.Path(filepath.Join(dir, name))
	if err != nil {
		return nil, err
	}
	envv, args, cleanup, err := clientEnv(tgt, owner)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	tmp := real + ".partial"
	defer os.Remove(tmp)
	fmt.Fprintf(log, "Dumping %s…\n", database)
	args = append(args, "--format=custom", "--compress=6", "--no-owner", "--file="+tmp, "--dbname="+database)
	if out, err := env.Runner.RunWith(ctx, "pg_dump", execx.Opts{Env: envv, Timeout: -1}, args...); err != nil {
		return nil, fmt.Errorf("pg_dump failed: %s", lastLines(out, 5))
	}
	sum, err := fileSHA(tmp)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, real); err != nil {
		return nil, err
	}
	if _, err := env.Writer.WriteFile(filepath.Join(dir, name+".sha256"), []byte(sum+"  "+name+"\n"), 0o600, agentfs.RootOwner); err != nil {
		return nil, err
	}
	fi, _ := os.Stat(real)
	fmt.Fprintf(log, "  ✓ backup complete (%d bytes, sha256 %s)\n", fi.Size(), sum[:12])
	return map[string]any{"file": name, "database": database, "size": fi.Size(), "sha256": sum}, nil
}

func registerBackup(r *actions.Registry, env *host.Env) {
	fileParam := func(p json.RawMessage) (string, string, *Target, error) {
		var in struct {
			Database string  `json:"database"`
			File     string  `json:"file"`
			Target   *Target `json:"target"`
		}
		if err := host.Decode(p, &in); err != nil {
			return "", "", nil, err
		}
		if err := ValidName("database", in.Database); err != nil {
			return "", "", nil, err
		}
		if in.File != "" && (!backupFileRe.MatchString(in.File) || backupDB(in.File) != in.Database) {
			return "", "", nil, actions.InvalidParams("file: not a backup of this database")
		}
		return in.Database, in.File, in.Target, nil
	}

	r.Register(actions.Action{Name: "pg.backup_create", Mutating: true, Description: "Back up one database with pg_dump (custom format, checksummed). Async job.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		db, _, t, err := fileParam(p)
		if err != nil {
			return nil, err
		}
		j := env.Jobs.Start("pg.backup_create", "server:postgresql", func(ctx context.Context, log io.Writer) (any, error) {
			return dumpTo(ctx, env, t, db, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})

	r.Register(actions.Action{Name: "pg.backup_list", Description: "The backups of one database, newest first, with size and checksum.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		db, _, t, err := fileParam(p)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		dir := backupDirFor(env, t)
		entries, err := env.Root.ReadDir(dir)
		if err != nil {
			return map[string]any{"backups": out}, nil
		}
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !backupFileRe.MatchString(n) || backupDB(n) != db {
				continue
			}
			size, created, sum := int64(0), "", ""
			if fi, err := env.Root.Stat(filepath.Join(dir, n)); err == nil {
				size = fi.Size()
			}
			if ts, err := time.Parse(backupStamp, n[:len(backupStamp)]); err == nil {
				created = ts.UTC().Format(time.RFC3339)
			}
			if b, err := env.Root.ReadFile(filepath.Join(dir, n+".sha256")); err == nil {
				sum = strings.Fields(string(b) + " ")[0]
			}
			out = append(out, map[string]any{"file": n, "database": db, "size": size, "created": created, "sha256": sum})
		}
		sort.Slice(out, func(i, j int) bool { return out[i]["file"].(string) > out[j]["file"].(string) })
		return map[string]any{"backups": out}, nil
	}})

	r.Register(actions.Action{Name: "pg.backup_delete", Mutating: true, Description: "Delete one backup of a database.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		_, file, t, err := fileParam(p)
		if err != nil {
			return nil, err
		}
		if file == "" {
			return nil, actions.InvalidParams("file: required")
		}
		for _, n := range []string{file, file + ".sha256"} {
			if _, err := env.Writer.Remove(filepath.Join(backupDirFor(env, t), n)); err != nil {
				return nil, actions.Failed("The backup could not be deleted.", err.Error())
			}
		}
		return map[string]any{"deleted": file}, nil
	}})

	r.Register(actions.Action{Name: "pg.backup_prune", Mutating: true, Description: "Keep only the newest N backups of a database.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Database string  `json:"database"`
			Keep     int     `json:"keep"`
			Target   *Target `json:"target"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := ValidName("database", in.Database); err != nil {
			return nil, err
		}
		if in.Keep < 1 || in.Keep > 365 {
			return nil, actions.InvalidParams("keep: between 1 and 365")
		}
		pdir := backupDirFor(env, in.Target)
		entries, err := env.Root.ReadDir(pdir)
		if err != nil {
			return map[string]any{"deleted": []string{}}, nil
		}
		var mine []string
		for _, e := range entries {
			if !e.IsDir() && backupFileRe.MatchString(e.Name()) && backupDB(e.Name()) == in.Database {
				mine = append(mine, e.Name())
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(mine)))
		deleted := []string{}
		for i := in.Keep; i < len(mine); i++ {
			for _, n := range []string{mine[i], mine[i] + ".sha256"} {
				env.Writer.Remove(filepath.Join(pdir, n))
			}
			deleted = append(deleted, mine[i])
		}
		return map[string]any{"deleted": deleted}, nil
	}})

	r.Register(actions.Action{Name: "pg.backup_restore", Mutating: true, Description: "Restore a database from one of its backups. The checksum is verified and a safety backup of the current state is taken first. Async job.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		db, file, t, err := fileParam(p)
		if err != nil {
			return nil, err
		}
		if file == "" {
			return nil, actions.InvalidParams("file: required")
		}
		path := filepath.Join(backupDirFor(env, t), file)
		if !env.Root.Exists(path) {
			return nil, actions.NotFound("That backup no longer exists.")
		}
		j := env.Jobs.Start("pg.backup_restore", "server:postgresql", func(ctx context.Context, log io.Writer) (any, error) {
			real, err := env.Root.Path(path)
			if err != nil {
				return nil, err
			}
			fmt.Fprintln(log, "Checking the backup's checksum…")
			want, err := env.Root.ReadFile(path + ".sha256")
			if err != nil {
				return nil, fmt.Errorf("the checksum file of this backup is missing; it cannot be verified")
			}
			got, err := fileSHA(real)
			if err != nil {
				return nil, err
			}
			if f := strings.Fields(string(want)); len(f) == 0 || f[0] != got {
				return nil, fmt.Errorf("the backup is damaged (checksum mismatch); nothing was changed")
			}
			fmt.Fprintln(log, "Taking a safety backup of the current state first…")
			safety, err := dumpTo(ctx, env, t, db, log)
			if err != nil {
				return nil, fmt.Errorf("the safety backup failed, so nothing was changed: %w", err)
			}
			tgt, owner, err := queryTarget(ctx, env, t, db)
			if err != nil {
				return nil, err
			}
			envv, args, cleanup, err := clientEnv(tgt, owner)
			if err != nil {
				return nil, err
			}
			defer cleanup()
			fmt.Fprintf(log, "Restoring %s from %s…\n", db, file)
			args = append(args, "--clean", "--if-exists", "--no-owner", "--dbname="+db, real)
			out, rerr := env.Runner.RunWith(ctx, "pg_restore", execx.Opts{Env: envv, Timeout: -1}, args...)
			warnings := 0
			if rerr != nil {
				// pg_restore exits 1 when it ignored errors (an object that did not exist, a grant to a user that is gone).
				if m := regexp.MustCompile(`errors ignored on restore: (\d+)`).FindSubmatch(out); m != nil {
					warnings, _ = strconv.Atoi(string(m[1]))
				} else {
					return nil, fmt.Errorf("pg_restore failed: %s (the state before is saved as %s)", lastLines(out, 5), safety["file"])
				}
			}
			fmt.Fprintf(log, "  ✓ restored (%d warnings); the state before is saved as %s\n", warnings, safety["file"])
			return map[string]any{"restored": file, "safety_backup": safety["file"], "warnings": warnings}, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
}

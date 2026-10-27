package wordpress

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/dbmgr"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Staging and cloning: an installation's files and database are copied to
// another folder (usually another website of the same customer, such as
// staging.example.com), its wp-config.php pointed at the copy's database
// and its addresses replaced. The same copy runs the other way to sync a
// staging site back to the live one.
//
// Files are streamed by tar running as the source account on one side and
// as the target account on the other: root never reads or writes website
// files. Databases are dumped and imported by the agent; which databases
// comes only from the panel, which checks they belong to the websites'
// owner (a website's own wp-config.php could name anybody's database).
// Before anything of an existing installation is replaced, a restore point
// of it (files and database) is saved in the backup directory.

// DBTarget is the database a clone uses.
type DBTarget struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	Host     string `json:"host"`
}

// CopyRequest is a wordpress.copy call.
type CopyRequest struct {
	Source Target `json:"source"`
	Dest   Target `json:"target"`
	// Mode: clone (a new copy, wp-config.php rewritten), sync (files
	// and/or database over an existing copy, its wp-config.php kept) or
	// restore (a restore point of the target put back).
	Mode     string `json:"mode"`
	Files    bool   `json:"files"`
	Database bool   `json:"database"`
	// SourceDB and TargetDB are the databases (checked by the panel).
	SourceDB string    `json:"source_db"`
	TargetDB string    `json:"target_db"`
	DB       *DBTarget `json:"db"`
	// URL is the target's address; SourceURL the source's (both as
	// WordPress has them in its home option).
	URL          string `json:"url"`
	Overwrite    bool   `json:"overwrite"`
	Staging      bool   `json:"staging"`
	RestorePoint string `json:"restore_point"`
}

var (
	urlRe    = regexp.MustCompile(`^https?://[a-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?$`)
	dbHostRe = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,255}$`)
	pointRe  = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z$`)
)

func (in *CopyRequest) validate(env *host.Env) error {
	if err := in.Source.validate(env); err != nil {
		return err
	}
	if err := in.Dest.validate(env); err != nil {
		return err
	}
	in.URL = strings.TrimRight(in.URL, "/")
	switch in.Mode {
	case "clone":
		in.Files, in.Database = true, true
		if in.DB == nil {
			return &validate.Error{Field: "db", Message: "a clone needs its own database"}
		}
		if err := validate.DBIdentifier("db.name", in.DB.Name); err != nil {
			return err
		}
		if err := validate.DBIdentifier("db.user", in.DB.User); err != nil {
			return err
		}
		if !validSecret(in.DB.Password) {
			return &validate.Error{Field: "db.password", Message: "8 to 128 characters"}
		}
		if in.DB.Host == "" {
			in.DB.Host = "localhost"
		}
		if !dbHostRe.MatchString(in.DB.Host) {
			return &validate.Error{Field: "db.host", Message: "is not a database host"}
		}
		in.TargetDB = in.DB.Name
	case "sync":
		if !in.Files && !in.Database {
			return &validate.Error{Field: "files", Message: "choose files, the database or both"}
		}
	case "restore":
		if !pointRe.MatchString(in.RestorePoint) {
			return &validate.Error{Field: "restore_point", Message: "is not a restore point"}
		}
		return validate.DBIdentifier("target_db", in.TargetDB)
	default:
		return &validate.Error{Field: "mode", Message: "must be clone, sync or restore"}
	}
	if in.Database {
		if err := validate.DBIdentifier("source_db", in.SourceDB); err != nil {
			return err
		}
		if err := validate.DBIdentifier("target_db", in.TargetDB); err != nil {
			return err
		}
		if in.SourceDB == in.TargetDB {
			return &validate.Error{Field: "target_db", Message: "must be another database than the source's"}
		}
	}
	if !urlRe.MatchString(in.URL) {
		return &validate.Error{Field: "url", Message: "must be the copy's address, e.g. https://staging.example.com"}
	}
	src, dst := filepath.Join(in.Source.Root, in.Source.Path), filepath.Join(in.Dest.Root, in.Dest.Path)
	if src == dst || strings.HasPrefix(src+"/", dst+"/") {
		return &validate.Error{Field: "target", Message: "cannot be the installation itself or a folder that contains it"}
	}
	return nil
}

// restorePointDir holds a target's restore points.
func restorePointDir(env *host.Env, domain string) string {
	return filepath.Join(env.Cfg.BackupDir, "wordpress", domain)
}

type pointMeta struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Database string `json:"database"`
	Files    bool   `json:"files"`
	Reason   string `json:"reason"`
}

const keepRestorePoints = 5

// ListRestorePoints are a website's restore points, newest first.
func ListRestorePoints(env *host.Env, domain string) []pointMeta {
	dirs, _ := env.Root.Glob(filepath.Join(restorePointDir(env, domain), "*", "meta.json"))
	out := []pointMeta{}
	for _, m := range dirs {
		b, err := env.Root.ReadFile(m)
		var p pointMeta
		if err != nil || json.Unmarshal(b, &p) != nil || !pointRe.MatchString(p.ID) || p.ID != filepath.Base(filepath.Dir(m)) {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// tarPipe streams dir of one account into dir of another.
func tarPipe(ctx context.Context, env *host.Env, from *execx.Credential, fromDir string, to *execx.Credential, toDir string, exclude []string, log io.Writer) error {
	pr, pw := io.Pipe()
	args := []string{"-C", fromDir, "--exclude=./nimvor-login-*.php", "--exclude=./.nimvor-trash"}
	for _, x := range exclude {
		args = append(args, "--exclude=./"+x)
	}
	args = append(args, "-cf", "-", ".")
	errc := make(chan error, 1)
	go func() {
		out, err := env.Runner.RunWith(ctx, "tar", execx.Opts{As: from, Dir: "/", Stdout: pw, Timeout: -1}, args...)
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			err = nil // files changed while being read: still a valid archive
		}
		if err != nil {
			err = fmt.Errorf("reading the files failed: %s", host.Output(out))
		}
		pw.CloseWithError(err)
		errc <- err
	}()
	out, err := env.Runner.RunWith(ctx, "tar", execx.Opts{As: to, Dir: "/", Stdin: pr, Output: log, Timeout: -1}, "-C", toDir, "--no-same-owner", "-xf", "-")
	pr.CloseWithError(io.EOF)
	if rerr := <-errc; rerr != nil {
		return rerr
	}
	if err != nil {
		return fmt.Errorf("writing the files failed: %s", host.Output(out))
	}
	return nil
}

// configScript points a copied wp-config.php at its database and address
// (the values come inside the program, on standard input).
const configScript = `<?php
$cfg = getenv('NIMVOR_FILE');
$src = (string) @file_get_contents($cfg);
$vals = %s;
foreach ($vals as $name => $v) {
    $re = '/define\s*\(\s*([\'"])' . $name . '\1\s*,\s*(\'(?:\\\\.|[^\'\\\\])*\'|"(?:\\\\.|[^"\\\\])*")\s*\)\s*;/';
    $n = preg_match_all($re, $src);
    if ($n === 0 && in_array($name, ['WP_HOME', 'WP_SITEURL'], true)) {
        continue;
    }
    if ($n !== 1) {
        echo "\nNIMVOR-JSON " . json_encode(['ok' => false, 'error' => "wp-config.php does not set $name in the usual way"]) . "\n";
        exit(0);
    }
    $src = preg_replace_callback($re, fn () => "define( '$name', " . var_export($v, true) . " );", $src, 1);
}
$tmp = $cfg . '.nimvor-' . bin2hex(random_bytes(6));
if (@file_put_contents($tmp, $src) !== strlen($src) || !@chmod($tmp, fileperms($cfg) & 0777) || !@rename($tmp, $cfg)) {
    @unlink($tmp);
    echo "\nNIMVOR-JSON " . json_encode(['ok' => false, 'error' => 'wp-config.php could not be written']) . "\n";
    exit(0);
}
echo "\nNIMVOR-JSON " . json_encode(['ok' => true]) . "\n";
`

func phpArray(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, phpString(k)+" => "+phpString(m[k]))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// home is an installation's address as WordPress stores it.
func (r *runner) home(ctx context.Context, dir string) (string, error) {
	out, err := r.wp(ctx, dir, execx.Opts{}, "option", "get", "home")
	h := strings.TrimRight(strings.TrimSpace(lastLine(out)), "/")
	if err != nil || !urlRe.MatchString(h) {
		return "", fmt.Errorf("the address of the installation could not be read: %s", cliError(out))
	}
	return h, nil
}

// replaceURL replaces the old address with the new one in the database
// (also the other scheme and the JSON-escaped form).
func (r *runner) replaceURL(ctx context.Context, dir, from, to string, log io.Writer) error {
	if from == to {
		return nil
	}
	pairs := [][2]string{{from, to}}
	if s, ok := strings.CutPrefix(from, "https://"); ok {
		pairs = append(pairs, [2]string{"http://" + s, to})
	} else if s, ok := strings.CutPrefix(from, "http://"); ok {
		pairs = append(pairs, [2]string{"https://" + s, to})
	}
	esc := func(s string) string { return strings.ReplaceAll(s, "/", `\/`) }
	pairs = append(pairs, [2]string{esc(from), esc(to)})
	for _, p := range pairs {
		fmt.Fprintf(log, "Replacing %s with %s…\n", p[0], p[1])
		if out, err := r.wp(ctx, dir, execx.Opts{Output: log, Timeout: 30 * time.Minute}, "search-replace", p[0], p[1], "--all-tables-with-prefix", "--skip-columns=guid", "--report-changed-only"); err != nil {
			return fmt.Errorf("replacing the address failed: %s", cliError(out))
		}
	}
	return nil
}

// savePoint saves a restore point of the target (its files and database).
func savePoint(ctx context.Context, env *host.Env, t Target, cred *execx.Credential, dir, database, reason string, log io.Writer) (string, error) {
	id := env.Now().UTC().Format("20060102T150405Z")
	base := filepath.Join(restorePointDir(env, t.Domain), id)
	if _, err := env.Writer.MkdirAll(base, 0o700, agentfs.RootOwner); err != nil {
		return "", err
	}
	real, err := env.Root.Path(base)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(log, "Saving a restore point of %s (%s)…\n", display(t), id)
	f, err := os.OpenFile(filepath.Join(real, "files.tar.gz"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	gz := gzip.NewWriter(f)
	out, terr := env.Runner.RunWith(ctx, "tar", execx.Opts{As: cred, Dir: "/", Stdout: gz, Timeout: -1}, "-C", dir, "--exclude=./nimvor-login-*.php", "-cf", "-", ".")
	var ee *exec.ExitError
	if errors.As(terr, &ee) && ee.ExitCode() == 1 {
		terr = nil
	}
	gz.Close()
	f.Close()
	if terr != nil {
		os.RemoveAll(real)
		return "", fmt.Errorf("the restore point could not be saved (files): %s", host.Output(out))
	}
	if database != "" {
		df, err := os.OpenFile(filepath.Join(real, "db.sql.gz"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			os.RemoveAll(real)
			return "", err
		}
		_, derr := dbmgr.Client{Env: env}.DumpTo(ctx, database, df, log)
		df.Close()
		if derr != nil {
			os.RemoveAll(real)
			return "", fmt.Errorf("the restore point could not be saved (database): %v", derr)
		}
	}
	meta, _ := json.Marshal(pointMeta{ID: id, Path: t.Path, Database: database, Files: true, Reason: reason})
	if err := os.WriteFile(filepath.Join(real, "meta.json"), meta, 0o600); err != nil {
		os.RemoveAll(real)
		return "", err
	}
	// Keep the newest few.
	for i, p := range ListRestorePoints(env, t.Domain) {
		if i >= keepRestorePoints {
			if old, err := env.Root.Path(filepath.Join(restorePointDir(env, t.Domain), p.ID)); err == nil {
				os.RemoveAll(old)
			}
		}
	}
	return id, nil
}

// copyDatabase copies one database into another (through a dump file in
// the backup directory, removed afterwards).
func copyDatabase(ctx context.Context, env *host.Env, from, to string, log io.Writer) error {
	db := dbmgr.Client{Env: env}
	tmpDir := filepath.Join(env.Cfg.BackupDir, "wordpress", ".tmp")
	if _, err := env.Writer.MkdirAll(tmpDir, 0o700, agentfs.RootOwner); err != nil {
		return err
	}
	dump := filepath.Join(tmpDir, fmt.Sprintf("%s-%d.sql.gz", from, time.Now().UnixNano()))
	if _, err := db.Dump(ctx, from, dump, log); err != nil {
		return fmt.Errorf("the database %s could not be dumped: %v", from, err)
	}
	defer func() {
		if p, err := env.Root.Path(dump); err == nil {
			os.Remove(p)
		}
	}()
	// Tables the copy had are replaced; the others stay.
	if err := db.Import(ctx, to, dump, log); err != nil {
		return fmt.Errorf("the database could not be copied into %s: %v", to, err)
	}
	return nil
}

func copyJob(env *host.Env, in CopyRequest) (func(context.Context, io.Writer) (any, error), error) {
	ctx := context.Background()
	dst, err := newRunner(env, in.Dest.Site)
	if err != nil {
		return nil, err
	}
	dstDir, err := in.Dest.dir(env)
	if err != nil {
		return nil, actions.InvalidParams("target.path: " + err.Error())
	}
	hasWP := env.Root.Exists(filepath.Join(in.Dest.Root, in.Dest.Path, "wp-settings.php"))
	var src *runner
	var srcDir string
	switch in.Mode {
	case "restore":
		if !hasWP {
			return nil, actions.NotFound("There is no WordPress in " + display(in.Dest) + ".")
		}
		var point *pointMeta
		for _, p := range ListRestorePoints(env, in.Dest.Domain) {
			if p.ID == in.RestorePoint {
				point = &p
			}
		}
		if point == nil || point.Path != in.Dest.Path {
			return nil, actions.NotFound("That restore point does not exist for " + display(in.Dest) + ".")
		}
		if point.Database != "" && point.Database != in.TargetDB {
			return nil, actions.Conflict("The restore point is of the database " + point.Database + ", not " + in.TargetDB + ".")
		}
	default:
		src, srcDir, err = prepare(ctx, env, in.Source)
		if err != nil {
			return nil, err
		}
		if !env.Root.Exists(filepath.Join(in.Source.Root, in.Source.Path, "wp-config.php")) {
			return nil, actions.Conflict("The source's wp-config.php is outside its folder; such installations cannot be copied.")
		}
		if in.Mode == "clone" && hasWP && !in.Overwrite {
			return nil, actions.Conflict(display(in.Dest) + " already has WordPress; confirm replacing it.")
		}
		if in.Mode == "sync" && !hasWP {
			return nil, actions.NotFound("There is no WordPress in " + display(in.Dest) + " to sync to; clone it first.")
		}
	}
	if err := ensurePhar(ctx, env, DefaultSource); err != nil {
		return nil, actions.Failed("WP-CLI, which manages WordPress, is not available: "+err.Error(), "")
	}
	return func(ctx context.Context, log io.Writer) (any, error) {
		res := map[string]any{"mode": in.Mode, "target": display(in.Dest)}
		if in.Mode == "restore" {
			return restorePoint(ctx, env, in, dst, dstDir, log)
		}
		// A restore point of what is replaced.
		if hasWP {
			db := ""
			if in.Database || in.Mode == "clone" {
				db = in.TargetDB
				if in.Mode == "clone" {
					db = "" // a new database: nothing of it to keep
				}
			}
			id, err := savePoint(ctx, env, in.Dest, dst.cred, dstDir, db, in.Mode+" from "+display(in.Source), log)
			if err != nil {
				return nil, err
			}
			res["restore_point"] = id
		}
		srcHome, err := src.home(ctx, srcDir)
		if err != nil {
			return nil, err
		}
		if in.Files {
			if out, err := env.Runner.RunWith(ctx, "sh", execx.Opts{As: dst.cred, Dir: "/"}, "-c", `mkdir -p -- "$1"`, "sh", dstDir); err != nil {
				return nil, fmt.Errorf("the target folder could not be created: %s", host.Output(out))
			}
			var exclude []string
			if in.Mode == "sync" {
				exclude = append(exclude, "wp-config.php")
			}
			if rel, err := filepath.Rel(srcDir, dstDir); err == nil && !strings.HasPrefix(rel, "..") {
				exclude = append(exclude, rel) // the copy inside the installation
			}
			fmt.Fprintf(log, "Copying the files of %s to %s…\n", display(in.Source), display(in.Dest))
			if err := tarPipe(ctx, env, src.cred, srcDir, dst.cred, dstDir, exclude, log); err != nil {
				return nil, err
			}
		}
		if in.Mode == "clone" {
			vals := map[string]string{"DB_NAME": in.DB.Name, "DB_USER": in.DB.User, "DB_PASSWORD": in.DB.Password, "DB_HOST": in.DB.Host, "WP_HOME": in.URL, "WP_SITEURL": in.URL}
			var cr struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			stdout, errOut, err := dst.script(ctx, fmt.Sprintf(configScript, phpArray(vals)), []string{"NIMVOR_FILE=" + filepath.Join(dstDir, "wp-config.php")}, time.Minute)
			if rerr := result(stdout, &cr); rerr != nil || err != nil {
				return nil, fmt.Errorf("wp-config.php of the copy could not be changed: %s", host.Why(errOut, err))
			}
			if !cr.OK {
				return nil, fmt.Errorf("%s", cr.Error)
			}
		}
		if in.Database {
			if err := copyDatabase(ctx, env, in.SourceDB, in.TargetDB, log); err != nil {
				return nil, err
			}
			to := in.URL
			if err := dst.replaceURL(ctx, dstDir, srcHome, to, log); err != nil {
				return nil, err
			}
			if in.Mode == "clone" && in.Staging {
				dst.wp(ctx, dstDir, execx.Opts{}, "option", "update", "blog_public", "0")
			}
			dst.wp(ctx, dstDir, execx.Opts{}, "cache", "flush")
		}
		fmt.Fprintf(log, "Done: %s is a copy of %s.\n", in.URL, srcHome)
		res["url"] = in.URL
		return res, nil
	}, nil
}

// restorePoint puts a restore point back (files are overwritten, files
// added since are kept; the database is replaced table by table).
func restorePoint(ctx context.Context, env *host.Env, in CopyRequest, dst *runner, dstDir string, log io.Writer) (any, error) {
	base, err := env.Root.Path(filepath.Join(restorePointDir(env, in.Dest.Domain), in.RestorePoint))
	if err != nil {
		return nil, err
	}
	var meta pointMeta
	if b, err := os.ReadFile(filepath.Join(base, "meta.json")); err != nil || json.Unmarshal(b, &meta) != nil {
		return nil, fmt.Errorf("the restore point is damaged")
	}
	f, err := os.Open(filepath.Join(base, "files.tar.gz"))
	if err != nil {
		return nil, fmt.Errorf("the restore point has no files")
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("the restore point's files are damaged")
	}
	fmt.Fprintf(log, "Restoring the files of %s…\n", display(in.Dest))
	if out, err := env.Runner.RunWith(ctx, "tar", execx.Opts{As: dst.cred, Dir: "/", Stdin: gz, Output: log, Timeout: -1}, "-C", dstDir, "--no-same-owner", "-xf", "-"); err != nil {
		return nil, fmt.Errorf("restoring the files failed: %s", host.Output(out))
	}
	if meta.Database != "" {
		if err := (dbmgr.Client{Env: env}).Import(ctx, meta.Database, filepath.Join(restorePointDir(env, in.Dest.Domain), in.RestorePoint, "db.sql.gz"), log); err != nil {
			return nil, fmt.Errorf("restoring the database failed: %v", err)
		}
		dst.wp(ctx, dstDir, execx.Opts{}, "cache", "flush")
	}
	return map[string]any{"mode": "restore", "target": display(in.Dest), "restore_point": in.RestorePoint}, nil
}

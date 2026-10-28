// Package database discovers MySQL/MariaDB installations (read-only).
//
// Discovery does not need database credentials: schemas are listed from
// the server's data directory. No connection is made to the server.
package database

import (
	"bufio"
	"bytes"
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

// Schema is one database found in the data directory.
type Schema struct {
	Name         string `json:"name"`
	SizeBytes    int64  `json:"size_bytes"`
	SizeComplete bool   `json:"size_complete"`
	Tables       int    `json:"tables"`
}

// Result describes the database server.
type Result struct {
	Installed bool     `json:"installed"`
	Engine    string   `json:"engine,omitempty"`
	Version   string   `json:"version,omitempty"`
	Binary    string   `json:"binary,omitempty"`
	DataDir   string   `json:"data_dir,omitempty"`
	Socket    string   `json:"socket,omitempty"`
	Port      int      `json:"port,omitempty"`
	Bind      string   `json:"bind_address,omitempty"`
	Schemas   []Schema `json:"schemas"`
	Warnings  []string `json:"warnings"`
}

// systemSchemas are internal to the server and never shown as websites' DBs.
var systemSchemas = map[string]bool{
	"mysql": true, "performance_schema": true, "sys": true, "information_schema": true,
	"#innodb_temp": true, "#innodb_redo": true, "lost+found": true,
}

var binaries = map[string][]string{
	"mariadb": {"/usr/sbin/mariadbd", "/usr/bin/mariadbd", "/usr/libexec/mariadbd"},
	"mysql":   {"/usr/sbin/mysqld", "/usr/bin/mysqld", "/usr/libexec/mysqld"},
}

// Discover inspects the database server installation.
func Discover(ctx context.Context, root filesystem.Root, paths platform.Paths, runner execx.Runner) Result {
	res := Result{Schemas: []Schema{}, Warnings: []string{}}
	for _, engine := range []string{"mariadb", "mysql"} {
		for _, b := range binaries[engine] {
			if root.Exists(b) {
				res.Installed, res.Engine, res.Binary = true, engine, b
				break
			}
		}
		if res.Installed {
			break
		}
	}
	if !res.Installed {
		return res
	}
	if runner != nil {
		id := "mysqld"
		if strings.HasSuffix(res.Binary, "mariadbd") {
			id = "mariadbd"
		}
		if out, err := runner.Run(ctx, id, "--version"); err == nil {
			res.Version, res.Engine = parseVersion(string(out), res.Engine)
		}
	}

	cfg := readConfig(root, paths.MySQLConfigs)
	res.DataDir = paths.MySQLDataDir
	if v := cfg["datadir"]; v != "" && filepath.IsAbs(v) {
		res.DataDir = filepath.Clean(v)
	}
	res.Socket = cfg["socket"]
	res.Bind = cfg["bind-address"]
	if p, err := strconv.Atoi(cfg["port"]); err == nil {
		res.Port = p
	} else {
		res.Port = 3306
	}

	entries, err := root.ReadDir(res.DataDir)
	if err != nil {
		res.Warnings = append(res.Warnings, "data directory "+res.DataDir+" is not readable; schemas cannot be listed")
		return res
	}
	for _, e := range entries {
		if !e.IsDir() || systemSchemas[e.Name()] || strings.HasPrefix(e.Name(), "#") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := filepath.Join(res.DataDir, e.Name())
		s := Schema{Name: DecodeName(e.Name())}
		s.SizeBytes, s.SizeComplete, _ = root.DirSize(dir, 20000)
		if files, err := root.ReadDir(dir); err == nil {
			for _, f := range files {
				ext := filepath.Ext(f.Name())
				if ext == ".ibd" || ext == ".frm" || ext == ".MYD" {
					s.Tables++
				}
			}
		}
		res.Schemas = append(res.Schemas, s)
	}
	sort.Slice(res.Schemas, func(i, j int) bool { return res.Schemas[i].Name < res.Schemas[j].Name })
	return res
}

var versionRe = regexp.MustCompile(`(?i)Ver\s+([0-9][0-9.]*)(-MariaDB)?`)
var versionMariaRe = regexp.MustCompile(`(?i)([0-9]+\.[0-9]+\.[0-9]+)-MariaDB`)

func parseVersion(out, engine string) (string, string) {
	if m := versionMariaRe.FindStringSubmatch(out); m != nil {
		return m[1], "mariadb"
	}
	if m := versionRe.FindStringSubmatch(out); m != nil {
		if strings.Contains(strings.ToLower(out), "mariadb") {
			engine = "mariadb"
		}
		return m[1], engine
	}
	return "", engine
}

// DecodeName reverses MySQL's filename encoding (e.g. "my@002ddb" -> "my-db").
func DecodeName(s string) string {
	if !strings.Contains(s, "@") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '@' && i+4 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+5], 16, 32); err == nil {
				b.WriteRune(rune(v))
				i += 4
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// readConfig reads [mysqld]/[mariadb]/[server] options from config files,
// following !includedir. Later files override earlier ones. Password-like
// keys are never retained.
func readConfig(root filesystem.Root, files []string) map[string]string {
	cfg := map[string]string{}
	seen := map[string]bool{}
	var readFile func(p string, depth int)
	readFile = func(p string, depth int) {
		if depth > 5 || seen[p] {
			return
		}
		seen[p] = true
		data, err := root.ReadFile(p)
		if err != nil {
			return
		}
		section := ""
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
			if strings.HasPrefix(line, "!includedir") {
				dir := strings.TrimSpace(strings.TrimPrefix(line, "!includedir"))
				matches, _ := root.Glob(filepath.Join(dir, "*.cnf"))
				for _, m := range matches {
					readFile(m, depth+1)
				}
				continue
			}
			if strings.HasPrefix(line, "!include") {
				readFile(strings.TrimSpace(strings.TrimPrefix(line, "!include")), depth+1)
				continue
			}
			if strings.HasPrefix(line, "[") {
				section = strings.ToLower(strings.Trim(line, "[] "))
				continue
			}
			if section != "mysqld" && section != "mariadb" && section != "server" && section != "mariadbd" {
				continue
			}
			k, v, _ := strings.Cut(line, "=")
			k = strings.ReplaceAll(strings.TrimSpace(k), "_", "-")
			if strings.Contains(k, "password") {
				continue
			}
			switch k {
			case "datadir", "socket", "port", "bind-address":
				cfg[k] = strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
	}
	for _, f := range files {
		readFile(f, 0)
	}
	return cfg
}

// Package php discovers installed PHP versions and PHP-FPM pools (read-only).
package php

import (
	"bufio"
	"bytes"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Version is one installed PHP version.
type Version struct {
	Version    string `json:"version"`
	CLIBinary  string `json:"cli_binary,omitempty"`
	FPMBinary  string `json:"fpm_binary,omitempty"`
	FPMService string `json:"fpm_service,omitempty"`
	IniFile    string `json:"ini_file,omitempty"`
	IsDefault  bool   `json:"is_default"`
	PoolCount  int    `json:"pool_count"`
}

// Pool is one PHP-FPM pool definition.
type Pool struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	File        string `json:"file"`
	User        string `json:"user,omitempty"`
	Group       string `json:"group,omitempty"`
	Listen      string `json:"listen"`
	PM          string `json:"pm,omitempty"`
	MaxChildren string `json:"max_children,omitempty"`
	Chdir       string `json:"chdir,omitempty"`
	OpenBasedir string `json:"open_basedir,omitempty"`
	SlowlogFile string `json:"slowlog,omitempty"`
}

// Result is the outcome of PHP discovery.
type Result struct {
	Versions       []Version `json:"versions"`
	Pools          []Pool    `json:"pools"`
	DefaultVersion string    `json:"default_version,omitempty"`
	Warnings       []string  `json:"warnings"`
}

var versionInName = regexp.MustCompile(`(\d+)\.(\d+)`)
var remiVersion = regexp.MustCompile(`/php(\d)(\d+)/`)

// Discover finds PHP versions and pools.
func Discover(root filesystem.Root, paths platform.Paths) Result {
	res := Result{Versions: []Version{}, Pools: []Pool{}, Warnings: []string{}}
	versions := map[string]*Version{}
	get := func(v string) *Version {
		if versions[v] == nil {
			versions[v] = &Version{Version: v}
		}
		return versions[v]
	}

	for _, g := range paths.PHPBinaryGlobs {
		matches, _ := root.Glob(g)
		for _, m := range matches {
			if v := versionFromPath(m); v != "" {
				get(v).CLIBinary = m
			}
		}
	}
	for _, g := range paths.PHPFPMBinaryGlobs {
		matches, _ := root.Glob(g)
		for _, m := range matches {
			if v := versionFromPath(m); v != "" {
				ver := get(v)
				ver.FPMBinary = m
				ver.FPMService = "php" + v + "-fpm.service"
			}
		}
	}
	for _, g := range paths.PHPIniGlobs {
		matches, _ := root.Glob(g)
		for _, m := range matches {
			if v := versionFromPath(m); v != "" {
				get(v).IniFile = m
			}
		}
	}

	for _, g := range paths.PHPFPMPoolGlobs {
		matches, _ := root.Glob(g)
		for _, m := range matches {
			data, err := root.ReadFile(m)
			if err != nil {
				res.Warnings = append(res.Warnings, "cannot read pool file "+m)
				continue
			}
			v := versionFromPath(m)
			for _, p := range ParsePools(data, m, v) {
				res.Pools = append(res.Pools, p)
				if v != "" {
					get(v).PoolCount++
				}
			}
		}
	}

	// /usr/bin/php -> /etc/alternatives/php -> /usr/bin/php8.3
	if resolved, err := root.Resolve("/usr/bin/php"); err == nil {
		if v := versionFromPath(resolved); v != "" {
			res.DefaultVersion = v
		}
	}

	for _, v := range versions {
		v.IsDefault = v.Version == res.DefaultVersion
		res.Versions = append(res.Versions, *v)
	}
	sort.Slice(res.Versions, func(i, j int) bool { return versionLess(res.Versions[i].Version, res.Versions[j].Version) })
	sort.Slice(res.Pools, func(i, j int) bool {
		if res.Pools[i].Version != res.Pools[j].Version {
			return versionLess(res.Pools[i].Version, res.Pools[j].Version)
		}
		return res.Pools[i].Name < res.Pools[j].Name
	})
	return res
}

func versionFromPath(p string) string {
	if m := remiVersion.FindStringSubmatch(p); m != nil {
		return m[1] + "." + m[2]
	}
	base := filepath.Base(p)
	if m := versionInName.FindStringSubmatch(base); m != nil {
		return m[1] + "." + m[2]
	}
	// Debian layout: /etc/php/8.3/fpm/pool.d/www.conf
	for _, part := range strings.Split(p, "/") {
		if m := versionInName.FindStringSubmatch(part); m != nil && m[0] == part {
			return part
		}
	}
	return ""
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if len(pa[i]) != len(pb[i]) {
			return len(pa[i]) < len(pb[i])
		}
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

// ParsePools parses an FPM pool configuration file (INI syntax). A file may
// define several pools; [global] sections are ignored.
func ParsePools(data []byte, file, version string) []Pool {
	var pools []Pool
	var cur *Pool
	vars := map[string]string{"$pool": ""}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name := strings.TrimSpace(line[1 : len(line)-1])
			if cur != nil {
				pools = append(pools, *cur)
			}
			cur = nil
			if strings.EqualFold(name, "global") {
				continue
			}
			cur = &Pool{Name: name, Version: version, File: file}
			vars["$pool"] = name
			continue
		}
		if cur == nil {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(stripComment(v)), `"'`)
		v = strings.ReplaceAll(v, "$pool", vars["$pool"])
		switch k {
		case "user":
			cur.User = v
		case "group":
			cur.Group = v
		case "listen":
			cur.Listen = v
		case "pm":
			cur.PM = v
		case "pm.max_children":
			cur.MaxChildren = v
		case "chdir":
			cur.Chdir = v
		case "php_admin_value[open_basedir]", "php_value[open_basedir]":
			cur.OpenBasedir = v
		case "slowlog":
			cur.SlowlogFile = v
		}
	}
	if cur != nil {
		pools = append(pools, *cur)
	}
	return pools
}

func stripComment(v string) string {
	// ';' starts a comment unless inside quotes.
	inQuote := byte(0)
	for i := 0; i < len(v); i++ {
		c := v[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			continue
		}
		if c == ';' {
			return v[:i]
		}
	}
	return v
}

// NormalizeListen makes FastCGI addresses comparable between Nginx
// (unix:/run/php/x.sock, 127.0.0.1:9000) and FPM (/run/php/x.sock, 9000).
func NormalizeListen(addr string) string {
	a := strings.TrimSpace(addr)
	a = strings.TrimPrefix(a, "unix:")
	if strings.HasPrefix(a, "/") {
		return "unix:" + filepath.Clean(a)
	}
	if !strings.Contains(a, ":") {
		// bare port
		return "tcp:127.0.0.1:" + a
	}
	i := strings.LastIndex(a, ":")
	host, port := a[:i], a[i+1:]
	if host == "localhost" || host == "" || host == "0.0.0.0" || host == "*" || host == "[::1]" {
		host = "127.0.0.1"
	}
	return "tcp:" + host + ":" + port
}

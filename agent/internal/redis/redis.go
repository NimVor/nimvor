// Package redis discovers Redis installations (read-only).
package redis

import (
	"bufio"
	"bytes"
	"context"
	"regexp"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Result describes the Redis server.
type Result struct {
	Installed     bool     `json:"installed"`
	Version       string   `json:"version,omitempty"`
	ConfigFile    string   `json:"config_file,omitempty"`
	Bind          string   `json:"bind,omitempty"`
	Port          string   `json:"port,omitempty"`
	UnixSocket    string   `json:"unix_socket,omitempty"`
	MaxMemory     string   `json:"maxmemory,omitempty"`
	MaxMemPolicy  string   `json:"maxmemory_policy,omitempty"`
	Databases     string   `json:"databases,omitempty"`
	PasswordSet   bool     `json:"password_set"`
	ProtectedMode string   `json:"protected_mode,omitempty"`
	Warnings      []string `json:"warnings"`
}

var versionRe = regexp.MustCompile(`v=([0-9][0-9.]*)`)

// Discover inspects the Redis installation. The value of requirepass is
// never returned, only whether it is set.
func Discover(ctx context.Context, root filesystem.Root, paths platform.Paths, runner execx.Runner) Result {
	res := Result{Warnings: []string{}}
	for _, b := range []string{"/usr/bin/redis-server", "/usr/sbin/redis-server"} {
		if root.Exists(b) {
			res.Installed = true
		}
	}
	for _, c := range paths.RedisConfigs {
		if data, err := root.ReadFile(c); err == nil {
			res.Installed = true
			res.ConfigFile = c
			applyConfig(&res, data)
			break
		} else if root.Exists(c) {
			res.Installed = true
			res.ConfigFile = c
			res.Warnings = append(res.Warnings, "configuration file "+c+" is not readable")
		}
	}
	if res.Installed && runner != nil && runner.Available("redis-server") {
		if out, err := runner.Run(ctx, "redis-server", "--version"); err == nil {
			if m := versionRe.FindStringSubmatch(string(out)); m != nil {
				res.Version = m[1]
			}
		}
	}
	return res
}

func applyConfig(res *Result, data []byte) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		val := strings.Join(f[1:], " ")
		switch strings.ToLower(f[0]) {
		case "bind":
			res.Bind = val
		case "port":
			res.Port = val
		case "unixsocket":
			res.UnixSocket = val
		case "maxmemory":
			res.MaxMemory = val
		case "maxmemory-policy":
			res.MaxMemPolicy = val
		case "databases":
			res.Databases = val
		case "protected-mode":
			res.ProtectedMode = val
		case "requirepass":
			res.PasswordSet = strings.Trim(val, `"'`) != ""
		case "user":
			// ACL users may carry passwords (">secret"); only note presence.
			if strings.Contains(val, ">") || strings.Contains(val, "#") {
				res.PasswordSet = true
			}
		}
	}
}

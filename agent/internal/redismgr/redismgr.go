// Package redismgr reports on and configures the local Redis server.
//
// The agent talks to Redis with redis-cli. The admin password, if any, is
// read from redis.conf by the agent and passed through the REDISCLI_AUTH
// environment variable, so it never appears in process arguments or in any
// response. Configuration changes use CONFIG SET followed by CONFIG
// REWRITE (Redis rewrites its own file) after a backup of redis.conf.
package redismgr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Client runs redis-cli against the local server.
type Client struct {
	Env *host.Env
	// pass overrides the password from redis.conf (used right after
	// requirepass changed, before CONFIG REWRITE has saved it).
	pass *string
}

func (c Client) conf() (port, socket, pass string) {
	port = "6379"
	b, err := c.Env.Root.ReadFile(c.Env.M.RedisConfig)
	if err != nil {
		return
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch strings.ToLower(f[0]) {
		case "port":
			port = f[1]
		case "unixsocket":
			socket = f[1]
		case "requirepass":
			pass = strings.Trim(strings.Join(f[1:], " "), `"'`)
		}
	}
	return
}

// Cmd runs one Redis command (arguments are passed as separate argv items).
func (c Client) Cmd(ctx context.Context, args ...string) (string, error) {
	port, socket, pass := c.conf()
	if c.pass != nil {
		pass = *c.pass
	}
	base := []string{"--no-auth-warning", "--raw"}
	if socket != "" && c.Env.Root.Exists(socket) {
		base = append(base, "-s", socket)
	} else {
		base = append(base, "-h", "127.0.0.1", "-p", port)
	}
	var env []string
	if pass != "" {
		env = append(env, "REDISCLI_AUTH="+pass)
	}
	out, err := c.Env.Runner.RunWith(ctx, "redis-cli", execx.Opts{Env: env}, append(base, args...)...)
	s := strings.TrimSpace(string(out))
	if err != nil || strings.HasPrefix(s, "ERR") || strings.HasPrefix(s, "NOAUTH") || strings.HasPrefix(s, "WRONGPASS") {
		return redact(s, pass), fmt.Errorf("%s", redact(s, pass))
	}
	return s, nil
}

func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[REDACTED]")
}

// ChannelACLs reports whether Redis supports Pub/Sub channel rules in
// ACLs (6.2 and later).
func ChannelACLs(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return true // unknown: assume a current Redis
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return true
	}
	return major > 6 || (major == 6 && minor >= 2)
}

// ACLArgs is the ACL SETUSER command for a website's Redis user: its keys
// (and, where Redis supports it, its Pub/Sub channels) limited to its
// prefix, no administrative or dangerous commands.
func ACLArgs(user, password, prefix string, channels bool) []string {
	args := []string{"ACL", "SETUSER", user, "reset", "on", ">" + password, "~" + prefix + "*"}
	if channels {
		args = append(args, "resetchannels", "&"+prefix+"*")
	}
	return append(args, "+@all", "-@admin", "-@dangerous")
}

// ParseInfo parses the output of INFO into section → key → value.
func ParseInfo(s string) map[string]map[string]string {
	out := map[string]map[string]string{}
	section := "default"
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "# ") {
			section = strings.ToLower(strings.TrimPrefix(l, "# "))
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		if out[section] == nil {
			out[section] = map[string]string{}
		}
		out[section][k] = v
	}
	return out
}

var policies = map[string]bool{"noeviction": true, "allkeys-lru": true, "allkeys-lfu": true, "allkeys-random": true, "volatile-lru": true, "volatile-lfu": true, "volatile-random": true, "volatile-ttl": true}
var aclUser = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
var prefixRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)

func (c Client) backupConf() error {
	b, err := c.Env.Root.ReadFile(c.Env.M.RedisConfig)
	if err != nil {
		return nil // nothing to back up (e.g. no config file); CONFIG REWRITE will fail clearly
	}
	dir := filepath.Join(c.Env.Cfg.StateDir, "config-backups")
	if _, err := c.Env.Writer.MkdirAll(dir, 0o700, agentfs.RootOwner); err != nil {
		return err
	}
	_, err = c.Env.Writer.WriteFile(filepath.Join(dir, "redis.conf."+c.Env.Now().Format("20060102T150405Z")), b, 0o600, agentfs.RootOwner)
	return err
}

func (c Client) setAndPersist(ctx context.Context, pairs ...string) error {
	if err := c.backupConf(); err != nil {
		return actions.Failed("redis.conf could not be backed up; nothing was changed.", err.Error())
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if _, err := c.Cmd(ctx, "CONFIG", "SET", pairs[i], pairs[i+1]); err != nil {
			return actions.Failed("Redis rejected the setting "+pairs[i]+".", redact(err.Error(), pairs[i+1]))
		}
		if pairs[i] == "requirepass" {
			p := pairs[i+1]
			c.pass = &p
		}
	}
	if _, err := c.Cmd(ctx, "CONFIG", "REWRITE"); err != nil {
		return actions.Failed("The setting is active but could not be saved to redis.conf.", err.Error())
	}
	return nil
}

// Register adds Redis actions.
func Register(r *actions.Registry, env *host.Env) {
	registerBrowser(r, env)
	c := Client{Env: env}
	r.Register(actions.Action{Name: "redis.info", Description: "Version, memory, clients, keyspace and ACL users of the local Redis.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if !env.Runner.Available("redis-cli") {
			return nil, actions.Failed("redis-cli is not installed.", "")
		}
		out, err := c.Cmd(ctx, "INFO")
		if err != nil {
			return nil, actions.Failed("Redis is not reachable.", err.Error())
		}
		info := ParseInfo(out)
		res := map[string]any{"info": info}
		if mm, err := c.Cmd(ctx, "CONFIG", "GET", "maxmemory"); err == nil {
			parts := strings.Split(mm, "\n")
			if len(parts) == 2 {
				res["maxmemory"] = parts[1]
			}
		}
		if pol, err := c.Cmd(ctx, "CONFIG", "GET", "maxmemory-policy"); err == nil {
			parts := strings.Split(pol, "\n")
			if len(parts) == 2 {
				res["maxmemory_policy"] = parts[1]
			}
		}
		if users, err := c.Cmd(ctx, "ACL", "USERS"); err == nil {
			res["acl_users"] = strings.Fields(users)
		}
		return res, nil
	}})
	r.Register(actions.Action{Name: "redis.configure", Mutating: true, Description: "Set maxmemory and eviction policy (persisted with CONFIG REWRITE).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			MaxMemoryMB int    `json:"maxmemory_mb"`
			Policy      string `json:"policy"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.MaxMemoryMB < 0 || in.MaxMemoryMB > 1<<20 {
			return nil, actions.InvalidParams("maxmemory_mb: 0 (unlimited) or a positive size")
		}
		if !policies[in.Policy] {
			return nil, actions.InvalidParams("policy: unknown eviction policy")
		}
		return map[string]any{"ok": true}, c.setAndPersist(ctx, "maxmemory", strconv.Itoa(in.MaxMemoryMB)+"mb", "maxmemory-policy", in.Policy)
	}})
	r.Register(actions.Action{Name: "redis.password", Mutating: true, Description: "Set or clear the default user's password.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Password string `json:"password"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Password != "" {
			if err := validate.Password("password", in.Password, 16); err != nil {
				return nil, host.Invalid(err)
			}
		}
		return map[string]any{"password_set": in.Password != ""}, c.setAndPersist(ctx, "requirepass", in.Password)
	}})
	r.Register(actions.Action{Name: "redis.flush", Mutating: true, Description: "Delete all keys of one database (requires confirm).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			DB      int    `json:"db"`
			Confirm string `json:"confirm"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.DB < 0 || in.DB > 255 || in.Confirm != fmt.Sprintf("db%d", in.DB) {
			return nil, actions.InvalidParams("confirm must be db<number>")
		}
		if _, err := c.Cmd(ctx, "-n", strconv.Itoa(in.DB), "FLUSHDB"); err != nil {
			return nil, actions.Failed("FLUSHDB failed.", err.Error())
		}
		return map[string]any{"flushed": in.DB}, nil
	}})
	r.Register(actions.Action{Name: "redis.acl_set", Mutating: true, Description: "Create or update a Redis user limited to a key prefix (per-website isolation).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			User     string `json:"user"`
			Password string `json:"password"`
			Prefix   string `json:"prefix"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !aclUser.MatchString(in.User) || in.User == "default" {
			return nil, actions.InvalidParams("user: 2–32 lowercase letters, digits, - or _")
		}
		if !prefixRe.MatchString(in.Prefix) {
			return nil, actions.InvalidParams("prefix: letters, digits and _ . : -")
		}
		if err := validate.Password("password", in.Password, 16); err != nil {
			return nil, host.Invalid(err)
		}
		if err := c.backupConf(); err != nil {
			return nil, actions.Failed("redis.conf could not be backed up; nothing was changed.", err.Error())
		}
		info, err := c.Cmd(ctx, "INFO", "server")
		if err != nil {
			return nil, actions.Failed("Redis is not reachable.", err.Error())
		}
		channels := ChannelACLs(ParseInfo(info)["server"]["redis_version"])
		args := ACLArgs(in.User, in.Password, in.Prefix, channels)
		if _, err := c.Cmd(ctx, args...); err != nil {
			return nil, actions.Failed("Redis rejected the user.", err.Error())
		}
		if _, err := c.Cmd(ctx, "CONFIG", "REWRITE"); err != nil {
			return nil, actions.Failed("The user is active but could not be saved to redis.conf.", err.Error())
		}
		res := map[string]any{"user": in.User, "prefix": in.Prefix, "channels_restricted": channels}
		if !channels {
			res["notes"] = []string{"This Redis (before 6.2) cannot limit Pub/Sub channels per user: the user's keys are limited to its prefix, but it can publish and subscribe on every channel. Upgrade Redis to 6.2 or later to limit channels too."}
		}
		return res, nil
	}})
	r.Register(actions.Action{Name: "redis.acl_del", Mutating: true, Description: "Remove a Redis ACL user.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			User string `json:"user"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !aclUser.MatchString(in.User) || in.User == "default" {
			return nil, actions.InvalidParams("user: invalid")
		}
		if _, err := c.Cmd(ctx, "ACL", "DELUSER", in.User); err != nil {
			return nil, actions.Failed("The user could not be removed.", err.Error())
		}
		_, _ = c.Cmd(ctx, "CONFIG", "REWRITE")
		return map[string]any{"removed": in.User}, nil
	}})
}

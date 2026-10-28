package mongomgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// adminUser is the dedicated MongoDB account NimVor authenticates as once
// authentication is enabled. Its password is never stored by the panel; the
// connection string (with the password) lives only in AdminURIFile, root-only.
const adminUser = "nimvorAdmin"

// adminURI builds the connection string NimVor uses to reach MongoDB as the
// admin user, with the password percent-encoded so any character is safe.
func adminURI(env *host.Env, password string) string {
	port := parseConfig(string(readFileOrEmpty(env, ConfigFile))).Port
	if port < 1 || port > 65535 {
		port = 27017
	}
	userinfo := url.UserPassword(adminUser, password).String()
	return "mongodb://" + userinfo + "@127.0.0.1:" + strconv.Itoa(port) + "/?authSource=admin"
}

func registerSecurity(r *actions.Registry, env *host.Env, c Client) {
	r.Register(actions.Action{Name: "mongo.admin_password", Mutating: true, Description: "Create or reset the NimVor MongoDB admin user and store its connection string.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Password string `json:"password"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Password) < 8 || len(in.Password) > 256 {
			return nil, actions.InvalidParams("password: 8 to 256 characters")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		// Create the admin user, or reset its password if it already exists.
		// Runs with whatever connection is current: locally when auth is off,
		// or as the existing admin (read from AdminURIFile) when it is on. The
		// password is parsed from a JSON literal inside mongosh and the script
		// is fed on stdin, so it never reaches the command line.
		pw, _ := json.Marshal(in.Password)
		script := fmt.Sprintf(
			"var a=db.getSiblingDB('admin');var pw=JSON.parse(%s);"+
				"if(a.getUser(%s)){a.updateUser(%s,{pwd:pw,roles:['root']})}else{a.createUser({user:%s,pwd:pw,roles:['root']})}"+
				"print('ok')",
			string(pw), jsString(adminUser), jsString(adminUser), jsString(adminUser))
		if _, err := c.evalSecret(ctx, script); err != nil {
			return nil, actions.Failed("The admin user could not be set.", err.Error())
		}
		// Store the new credentials so later actions authenticate with them.
		_, _ = env.Writer.MkdirAll(path.Dir(AdminURIFile), 0o700, agentfs.RootOwner)
		if _, err := env.Writer.WriteFile(AdminURIFile, []byte(adminURI(env, in.Password)+"\n"), 0o600, agentfs.RootOwner); err != nil {
			return nil, actions.Failed("The admin password was changed, but its connection string could not be saved to "+AdminURIFile+".", err.Error())
		}
		return map[string]any{"user": adminUser}, nil
	}})

	r.Register(actions.Action{Name: "mongo.auth_set", Mutating: true, Description: "Enable or disable MongoDB authentication, then restart (rolls back on failure).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enable bool `json:"enable"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		// Guardrail: never turn authentication on without stored admin
		// credentials, or the panel (and the server owner) would be locked out.
		if in.Enable {
			if uri := strings.TrimSpace(string(readFileOrEmpty(env, AdminURIFile))); uri == "" {
				return nil, actions.Conflict("Set an admin password first; otherwise enabling authentication would lock everyone out.")
			}
		}
		old, err := env.Root.ReadFile(ConfigFile)
		if err != nil {
			return nil, actions.Conflict("MongoDB's configuration file was not found at " + ConfigFile + ".")
		}
		cfg := parseConfig(string(old))
		next, err := applyConfig(string(old), cfg.Port, cfg.BindIp, in.Enable)
		if err != nil {
			return nil, actions.Conflict(err.Error())
		}
		if _, err := env.Writer.WriteFile(ConfigFile, []byte(next), 0o644, agentfs.RootOwner); err != nil {
			return nil, actions.Failed("The configuration could not be written.", err.Error())
		}
		if err := c.restart(ctx); err != nil {
			_, _ = env.Writer.WriteFile(ConfigFile, old, 0o644, agentfs.RootOwner)
			_ = c.restart(ctx)
			return nil, actions.Failed("MongoDB did not restart; the previous configuration was restored.", err.Error())
		}
		// Only once MongoDB is confirmed running without auth do we drop the
		// stored credentials, so a failed restart never leaves us locked out.
		if !in.Enable {
			_, _ = env.Writer.Remove(AdminURIFile)
		}
		return map[string]any{"authorization": in.Enable}, nil
	}})
}

// Package mongomgr manages a MongoDB server: its databases and users.
//
// Commands run through mongosh against the local server. When MongoDB has
// authentication enabled, the admin connection string is read from
// /etc/whacp/mongo-admin.uri (root only); otherwise the local default is
// used. Names are validated to a strict character set before they are put
// into the small JavaScript snippets mongosh evaluates, so a database or
// user name can never break out of its string.
package mongomgr

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// AdminURIFile holds the admin connection string when auth is enabled.
const AdminURIFile = "/etc/whacp/mongo-admin.uri"

// Unit is the MongoDB service.
var Units = []string{"mongod.service", "mongod"}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,62}$`)
	userRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,62}$`)
	// Roles a database user may be given (scoped to one database).
	roles = map[string]bool{"read": true, "readWrite": true, "dbAdmin": true, "dbOwner": true}
)

// Client runs mongosh against the local MongoDB.
type Client struct {
	Env *host.Env
}

func installed(env *host.Env) bool { return env.Runner.Available("mongosh") }

// eval runs a JavaScript snippet with mongosh and returns its stdout. The
// snippet should print exactly the result (usually JSON).
func (c Client) eval(ctx context.Context, script string) ([]byte, error) {
	return c.run(ctx, script, false)
}

// evalSecret runs a snippet that embeds a secret (a password): the script is
// fed on stdin instead of on the command line, so it never appears in the
// process list.
func (c Client) evalSecret(ctx context.Context, script string) ([]byte, error) {
	return c.run(ctx, script, true)
}

func (c Client) run(ctx context.Context, script string, secret bool) ([]byte, error) {
	args := []string{"--quiet", "--norc"}
	if uri, err := c.Env.Root.ReadFile(AdminURIFile); err == nil {
		if u := strings.TrimSpace(string(uri)); u != "" {
			args = append(args, u)
		}
	}
	opts := execx.Opts{Timeout: 60 * time.Second}
	if secret {
		opts.Stdin = strings.NewReader(script)
	} else {
		args = append(args, "--eval", script)
	}
	out, err := c.Env.Runner.RunWith(ctx, "mongosh", opts, args...)
	if err != nil {
		return out, fmt.Errorf("mongosh: %w: %s", err, lastLine(out))
	}
	return out, nil
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// jsString quotes s as a single-quoted JavaScript string. Callers validate
// names first; this is defence in depth for any value that reaches a script.
func jsString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", "", "\r", "", " ", "", " ", "")
	return "'" + r.Replace(s) + "'"
}

// Status is the MongoDB service state.
type Status struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version"`
	Auth      bool   `json:"auth"` // an admin connection string is configured
}

// Database is one MongoDB database.
type Database struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"` // bytes on disk
	Empty bool   `json:"empty"`
}

// User is a MongoDB user and the databases/roles it holds.
type User struct {
	User  string   `json:"user"`
	DB    string   `json:"db"`
	Roles []string `json:"roles"`
}

// Register adds the MongoDB actions.
func Register(r *actions.Registry, env *host.Env) {
	c := Client{Env: env}
	registerRepo(r, env)

	r.Register(actions.Action{Name: "mongo.status", Description: "MongoDB: installed, running, version.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		st := Status{Installed: installed(env), Auth: env.Root.Exists(AdminURIFile)}
		for _, u := range Units {
			if env.UnitActive(ctx, u) {
				st.Running = true
			}
		}
		if st.Installed && st.Running {
			if out, err := c.eval(ctx, "print(db.version())"); err == nil {
				st.Version = strings.TrimSpace(string(out))
			}
		}
		return st, nil
	}})

	r.Register(actions.Action{Name: "mongo.overview", Description: "MongoDB databases (with sizes) and users.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed. Enable its repository and install it on the MongoDB page.")
		}
		dbs, err := c.databases(ctx)
		if err != nil {
			return nil, actions.Failed("MongoDB could not be queried.", err.Error())
		}
		users, err := c.users(ctx)
		if err != nil {
			return nil, actions.Failed("MongoDB users could not be read.", err.Error())
		}
		return map[string]any{"databases": dbs, "users": users}, nil
	}})

	r.Register(actions.Action{Name: "mongo.database_create", Mutating: true, Description: "Create a MongoDB database (an empty collection makes it exist).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Name string `json:"name"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !nameRe.MatchString(in.Name) || strings.ContainsAny(in.Name, "/\\. \"$*<>:|?") {
			return nil, actions.InvalidParams("name: letters, digits, _ and - only (up to 63)")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		if _, err := c.eval(ctx, "db.getSiblingDB("+jsString(in.Name)+").createCollection('_whacp')"); err != nil {
			return nil, actions.Failed("The database could not be created.", err.Error())
		}
		return map[string]any{"name": in.Name}, nil
	}})

	r.Register(actions.Action{Name: "mongo.database_drop", Mutating: true, Description: "Drop a MongoDB database (requires confirm = name).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Name    string `json:"name"`
			Confirm string `json:"confirm"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !nameRe.MatchString(in.Name) {
			return nil, actions.InvalidParams("name: invalid")
		}
		if in.Confirm != in.Name {
			return nil, actions.InvalidParams("confirm: type the database name to drop it")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		if _, err := c.eval(ctx, "db.getSiblingDB("+jsString(in.Name)+").dropDatabase()"); err != nil {
			return nil, actions.Failed("The database could not be dropped.", err.Error())
		}
		return map[string]any{"dropped": in.Name}, nil
	}})

	r.Register(actions.Action{Name: "mongo.user_create", Mutating: true, Description: "Create a MongoDB user scoped to one database with a role.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			User     string `json:"user"`
			Password string `json:"password"`
			DB       string `json:"db"`
			Role     string `json:"role"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !userRe.MatchString(in.User) {
			return nil, actions.InvalidParams("user: letters, digits, _ . - only (up to 63)")
		}
		if !nameRe.MatchString(in.DB) {
			return nil, actions.InvalidParams("db: invalid database name")
		}
		if in.Role == "" {
			in.Role = "readWrite"
		}
		if !roles[in.Role] {
			return nil, actions.InvalidParams("role: read, readWrite, dbAdmin or dbOwner")
		}
		if len(in.Password) < 8 || len(in.Password) > 256 {
			return nil, actions.InvalidParams("password: 8 to 256 characters")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		// The password is passed as a JSON argument parsed inside the script,
		// never concatenated into it.
		pw, _ := json.Marshal(in.Password)
		script := fmt.Sprintf("db.getSiblingDB(%s).createUser({user:%s,pwd:JSON.parse(%s),roles:[{role:%s,db:%s}]})",
			jsString(in.DB), jsString(in.User), jsString(string(pw)), jsString(in.Role), jsString(in.DB))
		if _, err := c.evalSecret(ctx, script); err != nil {
			return nil, actions.Failed("The user could not be created.", err.Error())
		}
		return map[string]any{"user": in.User, "db": in.DB}, nil
	}})

	r.Register(actions.Action{Name: "mongo.user_password", Mutating: true, Description: "Change a MongoDB user's password.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			User     string `json:"user"`
			DB       string `json:"db"`
			Password string `json:"password"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !userRe.MatchString(in.User) || !nameRe.MatchString(in.DB) {
			return nil, actions.InvalidParams("user/db: invalid")
		}
		if len(in.Password) < 8 || len(in.Password) > 256 {
			return nil, actions.InvalidParams("password: 8 to 256 characters")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		pw, _ := json.Marshal(in.Password)
		script := fmt.Sprintf("db.getSiblingDB(%s).changeUserPassword(%s,JSON.parse(%s))", jsString(in.DB), jsString(in.User), jsString(string(pw)))
		if _, err := c.evalSecret(ctx, script); err != nil {
			return nil, actions.Failed("The password could not be changed.", err.Error())
		}
		return map[string]any{"user": in.User, "db": in.DB}, nil
	}})

	r.Register(actions.Action{Name: "mongo.user_drop", Mutating: true, Description: "Drop a MongoDB user.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			User string `json:"user"`
			DB   string `json:"db"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !userRe.MatchString(in.User) || !nameRe.MatchString(in.DB) {
			return nil, actions.InvalidParams("user/db: invalid")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		if _, err := c.eval(ctx, "db.getSiblingDB("+jsString(in.DB)+").dropUser("+jsString(in.User)+")"); err != nil {
			return nil, actions.Failed("The user could not be dropped.", err.Error())
		}
		return map[string]any{"dropped": in.User, "db": in.DB}, nil
	}})
}

// databases lists the server's databases (excluding the internal ones).
func (c Client) databases(ctx context.Context) ([]Database, error) {
	out, err := c.eval(ctx, "print(JSON.stringify(db.adminCommand({listDatabases:1}).databases))")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name       string `json:"name"`
		SizeOnDisk int64  `json:"sizeOnDisk"`
		Empty      bool   `json:"empty"`
	}
	if err := json.Unmarshal(trimToJSON(out), &raw); err != nil {
		return nil, fmt.Errorf("unexpected output: %s", lastLine(out))
	}
	internal := map[string]bool{"admin": true, "local": true, "config": true}
	dbs := []Database{}
	for _, d := range raw {
		if internal[d.Name] {
			continue
		}
		dbs = append(dbs, Database{Name: d.Name, Size: d.SizeOnDisk, Empty: d.Empty})
	}
	return dbs, nil
}

// users lists users across databases (excluding internal databases).
func (c Client) users(ctx context.Context) ([]User, error) {
	script := "print(JSON.stringify(db.getSiblingDB('admin').system.users.find({},{user:1,db:1,roles:1}).toArray()))"
	out, err := c.eval(ctx, script)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		User  string `json:"user"`
		DB    string `json:"db"`
		Roles []struct {
			Role string `json:"role"`
			DB   string `json:"db"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(trimToJSON(out), &raw); err != nil {
		return nil, fmt.Errorf("unexpected output: %s", lastLine(out))
	}
	internal := map[string]bool{"admin": true, "local": true, "config": true}
	users := []User{}
	for _, u := range raw {
		if internal[u.DB] {
			continue
		}
		roleNames := make([]string, 0, len(u.Roles))
		for _, role := range u.Roles {
			roleNames = append(roleNames, role.Role)
		}
		users = append(users, User{User: u.User, DB: u.DB, Roles: roleNames})
	}
	return users, nil
}

// trimToJSON returns the first JSON array/object in mongosh's output, so any
// stray banner line is ignored.
func trimToJSON(b []byte) []byte {
	s := strings.TrimSpace(string(b))
	for _, open := range []byte{'[', '{'} {
		if i := strings.IndexByte(s, open); i >= 0 {
			return []byte(s[i:])
		}
	}
	return b
}

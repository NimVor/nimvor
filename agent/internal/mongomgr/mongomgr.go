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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// ConfigFile is MongoDB's main configuration file (Debian/Ubuntu and the
// RHEL packages both use this path).
const ConfigFile = "/etc/mongod.conf"

// defaultLogPath is where mongod logs when systemLog.path is not set.
const defaultLogPath = "/var/log/mongodb/mongod.log"

var bindIpRe = regexp.MustCompile(`^[0-9A-Za-z.:_-]+(,[0-9A-Za-z.:_-]+)*$`)

// AdminURIFile holds the admin connection string when auth is enabled.
const AdminURIFile = "/etc/whacp/mongo-admin.uri"

// Unit is the MongoDB service.
var Units = []string{"mongod.service", "mongod"}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,62}$`)
	userRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,62}$`)
	// A collection name: letters, digits, _ . - (dots allow dotted names).
	collRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,119}$`)
	// Roles a database user may be given (scoped to one database).
	roles = map[string]bool{"read": true, "readWrite": true, "dbAdmin": true, "dbOwner": true}
)

// validColl reports whether name is a safe, non-system collection name.
func validColl(name string) bool {
	return collRe.MatchString(name) && !strings.HasPrefix(name, "system.")
}

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

// mongoLong decodes a BSON 64-bit integer as rendered by JSON.stringify in the
// shell. The legacy mongo shell prints it as a plain JSON number, while
// mongosh prints a Long as an object {"low":..,"high":..,"unsigned":..}. We
// accept either so listing databases does not fail on a mongosh-based server.
type mongoLong int64

func (m *mongoLong) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*m = 0
		return nil
	}
	if b[0] == '{' {
		var o struct {
			Low  int64 `json:"low"`
			High int64 `json:"high"`
		}
		if err := json.Unmarshal(b, &o); err != nil {
			return err
		}
		// Reassemble the signed 64-bit value from its 32-bit halves.
		*m = mongoLong(o.High<<32 | (o.Low & 0xffffffff))
		return nil
	}
	// A plain number (possibly rendered with a fraction or exponent).
	var n float64
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*m = mongoLong(int64(n))
	return nil
}

// User is a MongoDB user and the databases/roles it holds.
type User struct {
	User  string   `json:"user"`
	DB    string   `json:"db"`
	Roles []string `json:"roles"`
}

// Collection is one collection in a database with its document count.
type Collection struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
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
		// The two reads are independent: a failure listing users (for
		// example a permission error on an authenticated server) must not
		// hide the databases, and vice versa. Only fail when both fail.
		dbs, dbErr := c.databases(ctx)
		users, userErr := c.users(ctx)
		if dbErr != nil && userErr != nil {
			return nil, actions.Failed("MongoDB could not be queried.", strings.TrimSpace(dbErr.Error()+"; "+userErr.Error()))
		}
		if dbs == nil {
			dbs = []Database{}
		}
		if users == nil {
			users = []User{}
		}
		out := map[string]any{"databases": dbs, "users": users}
		if dbErr != nil {
			out["databases_error"] = dbErr.Error()
		}
		if userErr != nil {
			out["users_error"] = userErr.Error()
		}
		return out, nil
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

	// --- Database browser (read, plus delete one document) ---

	r.Register(actions.Action{Name: "mongo.collections", Description: "List a database's collections with document counts.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
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
		cols, err := c.collections(ctx, in.DB)
		if err != nil {
			return nil, actions.Failed("The collections could not be listed.", err.Error())
		}
		return map[string]any{"collections": cols}, nil
	}})

	r.Register(actions.Action{Name: "mongo.documents", Description: "Find documents in a collection (optional EJSON filter, skip, limit).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			DB     string `json:"db"`
			Coll   string `json:"collection"`
			Filter string `json:"filter"`
			Skip   int    `json:"skip"`
			Limit  int    `json:"limit"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !nameRe.MatchString(in.DB) {
			return nil, actions.InvalidParams("db: invalid database name")
		}
		if !validColl(in.Coll) {
			return nil, actions.InvalidParams("collection: invalid name")
		}
		filter := strings.TrimSpace(in.Filter)
		if filter == "" {
			filter = "{}"
		}
		if len(filter) > 8192 || !json.Valid([]byte(filter)) {
			return nil, actions.InvalidParams("filter: enter a valid JSON object, e.g. {\"name\":\"x\"}")
		}
		if in.Skip < 0 {
			in.Skip = 0
		}
		if in.Limit <= 0 || in.Limit > 200 {
			in.Limit = 50
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		// The filter is embedded as a JSON string literal (json.Marshal does
		// the escaping) and parsed with EJSON inside mongosh, so it is data,
		// never code; the script is fed on stdin, not the command line.
		lit, _ := json.Marshal(filter)
		script := fmt.Sprintf(
			"var c=db.getSiblingDB(%s).getCollection(%s);var q=EJSON.parse(%s);"+
				"print(EJSON.stringify({documents:c.find(q).skip(%d).limit(%d).toArray(),total:c.countDocuments(q)},null,0,{relaxed:true}))",
			jsString(in.DB), jsString(in.Coll), string(lit), in.Skip, in.Limit)
		out, err := c.run(ctx, script, true)
		if err != nil {
			return nil, actions.Failed("The documents could not be read.", err.Error())
		}
		var res struct {
			Documents json.RawMessage `json:"documents"`
			Total     mongoLong       `json:"total"`
		}
		if err := json.Unmarshal(trimToJSON(out), &res); err != nil {
			return nil, actions.Failed("The documents could not be read.", lastLine(out))
		}
		if len(res.Documents) == 0 {
			res.Documents = json.RawMessage("[]")
		}
		return map[string]any{"documents": res.Documents, "total": int64(res.Total), "skip": in.Skip, "limit": in.Limit}, nil
	}})

	r.Register(actions.Action{Name: "mongo.document_delete", Mutating: true, Description: "Delete one document by its _id (EJSON value).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			DB   string `json:"db"`
			Coll string `json:"collection"`
			ID   string `json:"id"` // EJSON of the _id value
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !nameRe.MatchString(in.DB) || !validColl(in.Coll) {
			return nil, actions.InvalidParams("db/collection: invalid")
		}
		id := strings.TrimSpace(in.ID)
		if id == "" || len(id) > 4096 || !json.Valid([]byte(id)) {
			return nil, actions.InvalidParams("id: the document _id as EJSON")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		lit, _ := json.Marshal(id)
		script := fmt.Sprintf(
			"var r=db.getSiblingDB(%s).getCollection(%s).deleteOne({_id:EJSON.parse(%s)});print(JSON.stringify({deleted:r.deletedCount}))",
			jsString(in.DB), jsString(in.Coll), string(lit))
		out, err := c.run(ctx, script, true)
		if err != nil {
			return nil, actions.Failed("The document could not be deleted.", err.Error())
		}
		var res struct {
			Deleted int64 `json:"deleted"`
		}
		_ = json.Unmarshal(trimToJSON(out), &res)
		return map[string]any{"deleted": res.Deleted}, nil
	}})

	// --- Configuration and log ---

	r.Register(actions.Action{Name: "mongo.config_get", Description: "Read MongoDB's configuration (port, bindIp, paths, authorization) and the raw file.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		raw, err := env.Root.ReadFile(ConfigFile)
		if err != nil {
			return nil, actions.Conflict("MongoDB's configuration file was not found at " + ConfigFile + ".")
		}
		return parseConfig(string(raw)), nil
	}})

	r.Register(actions.Action{Name: "mongo.config_set", Mutating: true, Description: "Set MongoDB's port, bindIp and authorization, then restart (rolls back on failure).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Port          int    `json:"port"`
			BindIp        string `json:"bind_ip"`
			Authorization bool   `json:"authorization"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Port < 1 || in.Port > 65535 {
			return nil, actions.InvalidParams("port: 1 to 65535")
		}
		in.BindIp = strings.TrimSpace(in.BindIp)
		if in.BindIp == "" || len(in.BindIp) > 200 || !bindIpRe.MatchString(in.BindIp) {
			return nil, actions.InvalidParams("bind_ip: comma-separated IP addresses or host names")
		}
		old, err := env.Root.ReadFile(ConfigFile)
		if err != nil {
			return nil, actions.Conflict("MongoDB's configuration file was not found at " + ConfigFile + ".")
		}
		next, err := applyConfig(string(old), in.Port, in.BindIp, in.Authorization)
		if err != nil {
			return nil, actions.Conflict(err.Error())
		}
		if _, err := env.Writer.WriteFile(ConfigFile, []byte(next), 0o644, agentfs.RootOwner); err != nil {
			return nil, actions.Failed("The configuration could not be written.", err.Error())
		}
		if err := c.restart(ctx); err != nil {
			// Put the working configuration back and restart, so a bad change
			// never leaves MongoDB down.
			_, _ = env.Writer.WriteFile(ConfigFile, old, 0o644, agentfs.RootOwner)
			_ = c.restart(ctx)
			return nil, actions.Failed("MongoDB did not restart with the new configuration; the previous configuration was restored.", err.Error())
		}
		return parseConfig(next), nil
	}})

	r.Register(actions.Action{Name: "mongo.log", Description: "The tail of MongoDB's log.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Lines int `json:"lines"`
		}
		_ = host.Decode(p, &in)
		if in.Lines <= 0 || in.Lines > 2000 {
			in.Lines = 300
		}
		path := parseConfig(string(readFileOrEmpty(env, ConfigFile))).LogPath
		b, err := c.logTail(path, 256*1024)
		if err != nil {
			return nil, actions.Conflict("MongoDB's log could not be read at " + path + ".")
		}
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if len(lines) > in.Lines {
			lines = lines[len(lines)-in.Lines:]
		}
		return map[string]any{"path": path, "lines": strings.Join(lines, "\n")}, nil
	}})
}

// restart restarts MongoDB and waits briefly for it to come active.
func (c Client) restart(ctx context.Context) error {
	if out, err := c.Env.Runner.Run(ctx, "systemctl", "restart", "--", "mongod"); err != nil {
		return fmt.Errorf("%w: %s", err, lastLine(out))
	}
	for i := 0; i < 15; i++ {
		for _, u := range Units {
			if c.Env.UnitActive(ctx, u) {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("mongod did not become active")
}

func readFileOrEmpty(env *host.Env, path string) []byte {
	b, err := env.Root.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

// collections lists a database's collections with estimated document counts.
func (c Client) collections(ctx context.Context, dbName string) ([]Collection, error) {
	script := "var d=db.getSiblingDB(" + jsString(dbName) + ");" +
		"print(JSON.stringify(d.getCollectionNames().map(function(n){" +
		"var x;try{x=d.getCollection(n).estimatedDocumentCount()}catch(e){x=d.getCollection(n).countDocuments({})}" +
		"return {name:n,count:Number(x)}})))"
	out, err := c.eval(ctx, script)
	if err != nil {
		return nil, err
	}
	var cols []Collection
	if err := json.Unmarshal(trimToJSON(out), &cols); err != nil {
		return nil, fmt.Errorf("unexpected output: %s", lastLine(out))
	}
	if cols == nil {
		cols = []Collection{}
	}
	return cols, nil
}

// databases lists the server's databases (excluding the internal ones).
func (c Client) databases(ctx context.Context) ([]Database, error) {
	out, err := c.eval(ctx, "print(JSON.stringify(db.adminCommand({listDatabases:1}).databases))")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name       string    `json:"name"`
		SizeOnDisk mongoLong `json:"sizeOnDisk"`
		Empty      bool      `json:"empty"`
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
		dbs = append(dbs, Database{Name: d.Name, Size: int64(d.SizeOnDisk), Empty: d.Empty})
	}
	return dbs, nil
}

// users lists users across databases (excluding internal databases). It uses
// the usersInfo command (the supported way), not a direct read of the
// admin.system.users collection, which modern MongoDB restricts.
func (c Client) users(ctx context.Context) ([]User, error) {
	script := "print(JSON.stringify(db.adminCommand({usersInfo:{forAllDBs:true}}).users))"
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

// Config is the subset of mongod.conf WhaCP reads and (for a few keys) edits.
type Config struct {
	Path          string `json:"path"`
	Port          int    `json:"port"`
	BindIp        string `json:"bind_ip"`
	DBPath        string `json:"db_path"`
	LogPath       string `json:"log_path"`
	Authorization bool   `json:"authorization"`
	Raw           string `json:"raw"`
}

// parseConfig reads the flat YAML that mongod.conf uses. It tracks the current
// top-level section and the indented keys under it; anything it does not know
// is ignored (the raw text is returned for display).
func parseConfig(raw string) Config {
	c := Config{Path: ConfigFile, Port: 27017, BindIp: "127.0.0.1", Raw: raw}
	section := ""
	for _, line := range strings.Split(raw, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		indented := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")
		if !indented {
			if strings.HasSuffix(t, ":") {
				section = strings.TrimSpace(strings.TrimSuffix(t, ":"))
			} else {
				section = ""
			}
			continue
		}
		kv := strings.SplitN(t, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key, val := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		switch section + "." + key {
		case "net.port":
			if n, err := strconv.Atoi(val); err == nil {
				c.Port = n
			}
		case "net.bindIp":
			c.BindIp = val
		case "storage.dbPath":
			c.DBPath = val
		case "systemLog.path":
			c.LogPath = val
		case "security.authorization":
			c.Authorization = val == "enabled"
		}
	}
	if c.LogPath == "" {
		c.LogPath = defaultLogPath
	}
	return c
}

func authStr(on bool) string {
	if on {
		return "enabled"
	}
	return "disabled"
}

// applyConfig rewrites port, bindIp and authorization in the existing file,
// preserving every other line and the file's indentation. Unknown layouts are
// refused rather than guessed at, so the file is never corrupted.
func applyConfig(raw string, port int, bindIp string, auth bool) (string, error) {
	lines := strings.Split(raw, "\n")
	section := ""
	sawPort, sawBind, sawAuth, hasSecurity := false, false, false, false
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if strings.HasSuffix(t, ":") {
				section = strings.TrimSpace(strings.TrimSuffix(t, ":"))
				if section == "security" {
					hasSecurity = true
				}
			} else {
				section = ""
			}
			continue
		}
		kv := strings.SplitN(t, ":", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		switch section + "." + key {
		case "net.port":
			lines[i] = indent + "port: " + strconv.Itoa(port)
			sawPort = true
		case "net.bindIp":
			lines[i] = indent + "bindIp: " + bindIp
			sawBind = true
		case "security.authorization":
			lines[i] = indent + "authorization: " + authStr(auth)
			sawAuth = true
		}
	}
	if !sawPort || !sawBind {
		return "", fmt.Errorf("net.port and net.bindIp were not found in %s; edit the file directly", ConfigFile)
	}
	out := strings.Join(lines, "\n")
	if !sawAuth {
		if hasSecurity {
			return "", fmt.Errorf("the security section is present without an authorization key; edit %s directly", ConfigFile)
		}
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "security:\n  authorization: " + authStr(auth) + "\n"
	}
	return out, nil
}

// logTail returns the last n bytes of a log file (trimmed to whole lines),
// opened without following symlinks.
func (c Client) logTail(path string, n int64) ([]byte, error) {
	real, err := c.Env.Root.Path(path)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(real, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	off := st.Size() - n
	if off < 0 {
		off = 0
	}
	b := make([]byte, st.Size()-off)
	k, _ := f.ReadAt(b, off)
	b = b[:k]
	if off > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b, nil
}

// trimToJSON returns the first JSON array/object in mongosh's output, so any
// stray banner line is ignored.
func trimToJSON(b []byte) []byte {
	s := strings.TrimSpace(string(b))
	// Start at the earliest opening bracket, whether the value is an object or
	// an array — picking '[' unconditionally would cut into an object whose
	// first field is an array.
	best := -1
	for _, open := range []byte{'[', '{'} {
		if i := strings.IndexByte(s, open); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	if best >= 0 {
		return []byte(s[best:])
	}
	return b
}

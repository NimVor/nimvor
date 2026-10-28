package mongomgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Remote MongoDB servers: NimVor can connect to another MongoDB server (for
// example to migrate a database onto this one). The panel stores the
// credentials encrypted and passes them per call; the agent never writes them
// to disk except, for mongodump, a short-lived root-only password file that is
// removed immediately after use. Nothing secret is ever placed on a command
// line (visible in the process list): mongosh gets the whole connection string
// on standard input via new Mongo(), and mongodump reads the password from a
// --config file.

var (
	// A hostname or IPv4/IPv6 literal, kept deliberately strict.
	remoteHostRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,253}$`)
	// A MongoDB user name: letters, digits and a few safe punctuation marks.
	remoteUserRe = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,128}$`)
)

// remoteConn is the structured connection the panel sends. The password is
// never logged; it reaches a script on stdin or a --config file only.
type remoteConn struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	AuthDB   string `json:"auth_db"`
	TLS      bool   `json:"tls"`
}

func (in *remoteConn) normalise() error {
	in.Host = strings.TrimSpace(in.Host)
	if !remoteHostRe.MatchString(in.Host) {
		return actions.InvalidParams("host: a server name or IP address")
	}
	if in.Port == 0 {
		in.Port = 27017
	}
	if in.Port < 1 || in.Port > 65535 {
		return actions.InvalidParams("port: 1–65535")
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username != "" && !remoteUserRe.MatchString(in.Username) {
		return actions.InvalidParams("username: invalid")
	}
	in.AuthDB = strings.TrimSpace(in.AuthDB)
	if in.AuthDB == "" {
		in.AuthDB = "admin"
	}
	if !nameRe.MatchString(in.AuthDB) {
		return actions.InvalidParams("auth_db: invalid")
	}
	return nil
}

// uri builds the MongoDB connection string, percent-encoding the credentials
// so any character is safe. Used inside a mongosh script fed on stdin.
func (in remoteConn) uri() string {
	host := in.Host + ":" + strconv.Itoa(in.Port)
	cred := ""
	if in.Username != "" {
		cred = url.UserPassword(in.Username, in.Password).String() + "@"
	}
	q := "/?authSource=" + url.QueryEscape(in.AuthDB) + "&serverSelectionTimeoutMS=8000&connectTimeoutMS=8000"
	if in.TLS {
		q += "&tls=true"
	}
	return "mongodb://" + cred + host + q
}

// runRemoteScript runs a mongosh snippet that opens its own connection with
// new Mongo(). --nodb stops mongosh from first connecting to localhost; the
// script (which embeds the connection string) is fed on stdin, so the password
// never appears on the command line.
func (c Client) runRemoteScript(ctx context.Context, script string) ([]byte, error) {
	out, err := c.Env.Runner.RunWith(ctx, "mongosh", execx.Opts{
		Timeout: 25 * time.Second,
		Stdin:   strings.NewReader(script),
	}, "--nodb", "--quiet", "--norc")
	if err != nil {
		return out, fmt.Errorf("mongosh: %w: %s", err, lastLine(out))
	}
	return out, nil
}

func registerRemote(r *actions.Registry, env *host.Env, c Client) {
	// Test a remote connection and report the server version.
	r.Register(actions.Action{Name: "mongo.remote_ping", Description: "Test a connection to a remote MongoDB server.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in remoteConn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.normalise(); err != nil {
			return nil, err
		}
		if !installed(env) {
			return nil, actions.Conflict("The MongoDB client (mongosh) is not installed.")
		}
		lit, _ := json.Marshal(in.uri())
		script := fmt.Sprintf("var c=new Mongo(%s);"+
			"var b=c.getDB('admin').runCommand({buildInfo:1});"+
			"var d=c.getDB('admin').runCommand({listDatabases:1});"+
			"print(JSON.stringify({ok:1,version:b.version,databases:(d.databases||[]).length}))", string(lit))
		out, err := c.runRemoteScript(ctx, script)
		if err != nil {
			return nil, actions.Failed("The remote server could not be reached. Check the host, port and credentials.", lastLine(out))
		}
		var res struct {
			OK        int    `json:"ok"`
			Version   string `json:"version"`
			Databases int    `json:"databases"`
		}
		if err := json.Unmarshal(trimToJSON(out), &res); err != nil || res.OK != 1 {
			return nil, actions.Failed("The remote server did not answer as expected.", lastLine(out))
		}
		return map[string]any{"ok": true, "version": res.Version, "databases": res.Databases}, nil
	}})

	// List the databases on a remote server (excluding the internal ones).
	r.Register(actions.Action{Name: "mongo.remote_databases", Description: "List the databases on a remote MongoDB server.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in remoteConn
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.normalise(); err != nil {
			return nil, err
		}
		if !installed(env) {
			return nil, actions.Conflict("The MongoDB client (mongosh) is not installed.")
		}
		lit, _ := json.Marshal(in.uri())
		script := fmt.Sprintf("var c=new Mongo(%s);"+
			"var d=c.getDB('admin').runCommand({listDatabases:1}).databases||[];"+
			"var skip={admin:1,local:1,config:1};"+
			"print(JSON.stringify(d.filter(function(x){return !skip[x.name]}).map(function(x){return {name:x.name,size:Number(x.sizeOnDisk||0)}})))", string(lit))
		out, err := c.runRemoteScript(ctx, script)
		if err != nil {
			return nil, actions.Failed("The remote databases could not be listed.", lastLine(out))
		}
		var raw json.RawMessage = trimToJSON(out)
		if !json.Valid(raw) {
			return nil, actions.Failed("The remote databases could not be listed.", lastLine(out))
		}
		return map[string]any{"databases": raw}, nil
	}})

	// Copy one database from a remote server into the local one (async job):
	// mongodump from the remote into a temporary archive, then mongorestore it
	// into the local server under the chosen name.
	r.Register(actions.Action{Name: "mongo.remote_pull", Mutating: true, Description: "Copy a database from a remote MongoDB server into this one (async job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			remoteConn
			RemoteDB string `json:"remote_db"`
			TargetDB string `json:"target_db"`
			Drop     bool   `json:"drop"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.remoteConn.normalise(); err != nil {
			return nil, err
		}
		if !nameRe.MatchString(in.RemoteDB) {
			return nil, actions.InvalidParams("remote_db: invalid database name")
		}
		if in.TargetDB == "" {
			in.TargetDB = in.RemoteDB
		}
		if !nameRe.MatchString(in.TargetDB) {
			return nil, actions.InvalidParams("target_db: invalid database name")
		}
		if !installed(env) {
			return nil, actions.Conflict("MongoDB is not installed.")
		}
		conn := in.remoteConn
		remoteDB, targetDB, drop := in.RemoteDB, in.TargetDB, in.Drop
		dir := backupDir(env)
		stamp := env.Now().Format(backupStamp)
		archive := filepath.Join(dir, ".pull-"+stamp+"_"+targetDB+".archive.gz")
		confPath := filepath.Join(dir, ".pull-"+stamp+".conf")

		j := env.Jobs.Start("mongo.remote_pull", "server:mongo", func(ctx context.Context, log io.Writer) (any, error) {
			if _, err := env.Writer.MkdirAll(dir, 0o700, agentfs.RootOwner); err != nil {
				return nil, fmt.Errorf("the work directory could not be created: %w", err)
			}
			// The remote password is written to a short-lived root-only file so
			// it never appears on the command line; always removed afterwards.
			defer func() { _, _ = env.Writer.Remove(archive) }()
			defer func() { _, _ = env.Writer.Remove(confPath) }()

			dump := []string{"--host=" + conn.Host, "--port=" + strconv.Itoa(conn.Port)}
			if conn.Username != "" {
				if _, err := env.Writer.WriteFile(confPath, []byte("password: "+yamlQuote(conn.Password)+"\n"), 0o600, agentfs.RootOwner); err != nil {
					return nil, fmt.Errorf("the credentials file could not be written: %w", err)
				}
				dump = append(dump, "--username="+conn.Username, "--authenticationDatabase="+conn.AuthDB, "--config="+confPath)
			}
			if conn.TLS {
				dump = append(dump, "--ssl")
			}
			dump = append(dump, "--db="+remoteDB, "--archive="+archive, "--gzip")
			fmt.Fprintf(log, "Dumping %s from %s:%d…\n", remoteDB, conn.Host, conn.Port)
			if out, err := env.Runner.RunWith(ctx, "mongodump", execx.Opts{Timeout: 60 * time.Minute, Stdout: log}, dump...); err != nil {
				return nil, fmt.Errorf("mongodump from the remote server failed: %w: %s", err, lastLine(out))
			}
			restore := append(c.connArgs(), "--archive="+archive, "--gzip",
				"--nsFrom="+remoteDB+".*", "--nsTo="+targetDB+".*")
			if drop {
				restore = append(restore, "--drop")
			}
			fmt.Fprintf(log, "Restoring into local database %s…\n", targetDB)
			if out, err := env.Runner.RunWith(ctx, "mongorestore", execx.Opts{Timeout: 60 * time.Minute, Stdout: log}, restore...); err != nil {
				return nil, fmt.Errorf("mongorestore into the local server failed: %w: %s", err, lastLine(out))
			}
			fmt.Fprintln(log, "  ✓ copy complete")
			return map[string]any{"db": targetDB, "from": conn.Host}, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
}

// yamlQuote double-quotes a string for a YAML scalar, escaping backslashes and
// double quotes so any password is represented exactly.
func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

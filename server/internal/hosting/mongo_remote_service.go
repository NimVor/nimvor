package hosting

// MongoRemoteService — saved connections to other MongoDB servers
// (MongoRemoteService.php). They exist so an administrator can copy a
// database from another machine onto this one ("Get DB from server").
//
// Passwords are stored sealed (internal/crypto, byte-compatible with the
// panel's Security\Crypto) and only ever leave the panel inside an agent
// call — never to the browser and never in the audit log.

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
)

// MongoRemote is a saved connection, without its password.
type MongoRemote struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	AuthDB   string `json:"auth_db"`
	TLS      bool   `json:"tls"`
}

// MongoRemoteDatabase is one database on a remote server
// (mongo.remote_databases).
type MongoRemoteDatabase struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// MongoRemotePing is the reply to mongo.remote_ping.
type MongoRemotePing struct {
	Version   string `json:"version"`
	Databases int    `json:"databases"`
}

// The connection field patterns, copied from the PHP service.
var (
	mongoRemoteHostRe   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,253}$`)
	mongoRemoteUserRe   = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,128}$`)
	mongoRemoteAuthDBRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,62}$`)
)

// MongoRemoteInput is the add form's submitted fields.
type MongoRemoteInput struct {
	Name     string
	Host     string
	Port     int
	Username string
	Password string
	AuthDB   string
	TLS      bool
}

// mongoRemoteClean validates and normalises the submitted connection, with
// the PHP's messages and order.
func (in MongoRemoteInput) clean() (MongoRemoteInput, error) {
	c := in
	c.Name = strings.TrimSpace(in.Name)
	c.Host = strings.TrimSpace(in.Host)
	c.Username = strings.TrimSpace(in.Username)
	c.AuthDB = strings.TrimSpace(in.AuthDB)
	if c.AuthDB == "" {
		c.AuthDB = "admin"
	}
	if c.Port == 0 {
		c.Port = 27017
	}
	if c.Name == "" || len([]rune(c.Name)) > 64 {
		return c, &UserError{What: "Enter a name for this connection (up to 64 characters)."}
	}
	if c.Host == "" || !mongoRemoteHostRe.MatchString(c.Host) {
		return c, &UserError{What: "Enter a valid host name or IP address."}
	}
	if c.Port < 1 || c.Port > 65535 {
		return c, &UserError{What: "The port must be between 1 and 65535."}
	}
	if c.Username != "" && !mongoRemoteUserRe.MatchString(c.Username) {
		return c, &UserError{What: "The user name contains characters that are not allowed."}
	}
	if !mongoRemoteAuthDBRe.MatchString(c.AuthDB) {
		return c, &UserError{What: "The authentication database name is not valid."}
	}
	return c, nil
}

// agentParams is the connection object every remote agent call carries, with
// the password unsealed.
func (c MongoRemoteInput) agentParams() map[string]any {
	return map[string]any{
		"host": c.Host, "port": c.Port, "username": c.Username,
		"password": c.Password, "auth_db": c.AuthDB, "tls": c.TLS,
	}
}

// MongoRemoteService stores and uses the saved remotes of one server.
type MongoRemoteService struct {
	db       *sql.DB
	crypto   *crypto.Crypto
	agent    AgentCaller
	audit    Auditor
	serverID int64
}

// NewMongoRemoteService builds the service.
func NewMongoRemoteService(db *sql.DB, cr *crypto.Crypto, ag AgentCaller, audit Auditor, serverID int64) *MongoRemoteService {
	return &MongoRemoteService{db: db, crypto: cr, agent: ag, audit: audit, serverID: serverID}
}

// List returns the saved remotes, by name, without passwords.
func (s *MongoRemoteService) List(ctx context.Context) ([]MongoRemote, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, host, port, username, auth_db, tls FROM mongo_remotes
		 WHERE server_id = ? ORDER BY name`, s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MongoRemote{}
	for rows.Next() {
		var r MongoRemote
		// tls is SQLite's 0/1 integer; database/sql will not scan that
		// straight into a bool, so it comes back as an int.
		var tls int
		if err := rows.Scan(&r.ID, &r.Name, &r.Host, &r.Port, &r.Username, &r.AuthDB, &tls); err != nil {
			return nil, err
		}
		r.TLS = tls != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// row loads one saved remote's stored row (password included, still sealed).
func (s *MongoRemoteService) row(ctx context.Context, id int64) (MongoRemote, string, bool, error) {
	var r MongoRemote
	var enc string
	var tls int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, host, port, username, auth_db, tls, password_enc
		 FROM mongo_remotes WHERE id = ? AND server_id = ?`, id, s.serverID).
		Scan(&r.ID, &r.Name, &r.Host, &r.Port, &r.Username, &r.AuthDB, &tls, &enc)
	if err == sql.ErrNoRows {
		return MongoRemote{}, "", false, nil
	}
	if err != nil {
		return MongoRemote{}, "", false, err
	}
	r.TLS = tls != 0
	return r, enc, true, nil
}

// params turns a saved remote into an agent connection object, unsealing the
// password.
func (s *MongoRemoteService) params(ctx context.Context, id int64) (map[string]any, error) {
	r, enc, ok, err := s.row(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &UserError{What: "That remote server is not configured."}
	}
	password := ""
	if enc != "" && s.crypto != nil {
		plain, err := s.crypto.Decrypt(enc)
		if err != nil {
			return nil, &UserError{What: "The saved password could not be read."}
		}
		password = string(plain)
	}
	return map[string]any{
		"host": r.Host, "port": r.Port, "username": r.Username,
		"password": password, "auth_db": r.AuthDB, "tls": r.TLS,
	}, nil
}

// Add proves the connection works and then stores it sealed, returning its
// display name (MongoRemoteService::add).
func (s *MongoRemoteService) Add(ctx context.Context, in MongoRemoteInput) (string, error) {
	c, err := in.clean()
	if err != nil {
		return "", err
	}
	// Prove the connection works before storing it.
	if _, err := s.agent.Call(ctx, "mongo.remote_ping", c.agentParams(), 30*time.Second); err != nil {
		return "", err
	}
	enc := ""
	if c.Password != "" {
		if s.crypto == nil {
			return "", &UserError{What: "The panel's encryption key is not available."}
		}
		sealed, err := s.crypto.Encrypt([]byte(c.Password))
		if err != nil {
			return "", err
		}
		enc = sealed
	}
	tls := 0
	if c.TLS {
		tls = 1
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO mongo_remotes (server_id, name, host, port, username, password_enc, auth_db, tls, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.serverID, c.Name, c.Host, c.Port, c.Username, enc, c.AuthDB, tls, now, now); err != nil {
		return "", err
	}
	s.record(ctx, "mongo.remote_add", map[string]any{"name": c.Name, "host": c.Host})
	return c.Name, nil
}

// Delete removes a saved remote; an unknown id is a no-op, as in the PHP.
func (s *MongoRemoteService) Delete(ctx context.Context, id int64) error {
	r, _, ok, err := s.row(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM mongo_remotes WHERE id = ? AND server_id = ?`, id, s.serverID); err != nil {
		return err
	}
	s.record(ctx, "mongo.remote_delete", map[string]any{"name": r.Name})
	return nil
}

// Ping tests one saved remote (mongo.remote_ping).
func (s *MongoRemoteService) Ping(ctx context.Context, id int64) (*MongoRemotePing, error) {
	params, err := s.params(ctx, id)
	if err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.remote_ping", params, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var o MongoRemotePing
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// Databases lists the databases on one saved remote
// (mongo.remote_databases).
func (s *MongoRemoteService) Databases(ctx context.Context, id int64) ([]MongoRemoteDatabase, error) {
	params, err := s.params(ctx, id)
	if err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.remote_databases", params, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Databases []MongoRemoteDatabase `json:"databases"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Databases == nil {
		o.Databases = []MongoRemoteDatabase{}
	}
	return o.Databases, nil
}

// Pull copies a remote database onto this server as a background job
// (mongo.remote_pull), returning the job id.
func (s *MongoRemoteService) Pull(ctx context.Context, id int64, remoteDB, targetDB string, drop bool) (string, error) {
	params, err := s.params(ctx, id)
	if err != nil {
		return "", err
	}
	if targetDB == "" {
		targetDB = remoteDB
	}
	params["remote_db"] = remoteDB
	params["target_db"] = targetDB
	params["drop"] = drop
	res, err := s.agent.Call(ctx, "mongo.remote_pull", params)
	if err != nil {
		return "", err
	}
	s.record(ctx, "mongo.remote_pull", map[string]any{"remote_db": remoteDB, "target_db": targetDB})
	var o struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return "", err
	}
	return o.JobID, nil
}

// record writes the audit row against the server.
func (s *MongoRemoteService) record(ctx context.Context, action string, details map[string]any) {
	if s.audit == nil {
		return
	}
	s.audit(ctx, action, "server", strconv.FormatInt(s.serverID, 10), "success", details)
}

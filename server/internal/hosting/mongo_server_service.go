package hosting

// MongoServerService — the MongoDB server itself (MongoService.php): every
// database and user on the machine, the mongod configuration and log, the
// backups, and the package/service lifecycle. This is the administrator's
// view; the per-website variant is SiteMongoService, which confines a
// customer to the databases under their account prefix.
//
// Every change is audited against the server and nothing else.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// MongoServerStatus is the server's MongoDB state (mongo.status).
type MongoServerStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version"`
	Auth      bool   `json:"auth"`
}

// MongoRepository describes the distribution's MongoDB package repository
// (mongo.repository).
type MongoRepository struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Version   string `json:"version"`
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Note      string `json:"note"`
}

// MongoServerOverview is every database and user on the server
// (mongo.overview), with the per-section errors the agent reports rather
// than failing the whole call.
type MongoServerOverview struct {
	Databases      []MongoDatabase `json:"databases"`
	Users          []MongoUser     `json:"users"`
	DatabasesError string          `json:"databases_error"`
	UsersError     string          `json:"users_error"`
}

// MongoConfig is the parsed mongod.conf (mongo.config_get).
type MongoConfig struct {
	Path          string `json:"path"`
	Port          int    `json:"port"`
	BindIP        string `json:"bind_ip"`
	DBPath        string `json:"db_path"`
	LogPath       string `json:"log_path"`
	Authorization bool   `json:"authorization"`
	Raw           string `json:"raw"`
}

// MongoLog is the tail of the MongoDB log (mongo.log).
type MongoLog struct {
	Path  string `json:"path"`
	Lines string `json:"lines"`
}

// MongoBackupJob is what starting a backup returns.
type MongoBackupJob struct {
	JobID string `json:"job_id"`
	File  string `json:"file"`
}

// MongoServerService manages the machine's MongoDB installation.
type MongoServerService struct {
	agent    AgentCaller
	audit    Auditor
	serverID int64
}

// NewMongoServerService builds the service for one server.
func NewMongoServerService(ag AgentCaller, audit Auditor, serverID int64) *MongoServerService {
	return &MongoServerService{agent: ag, audit: audit, serverID: serverID}
}

// record writes the audit row against the server, the way the PHP
// AuditLogger does for this area.
func (s *MongoServerService) record(ctx context.Context, action string, details map[string]any) {
	if s.audit == nil {
		return
	}
	s.audit(ctx, action, "server", fmt.Sprintf("%d", s.serverID), "success", details)
}

// call runs one agent action and records the audit row when it succeeds.
func (s *MongoServerService) call(ctx context.Context, action string, params map[string]any, details map[string]any, timeout ...time.Duration) (json.RawMessage, error) {
	res, err := s.agent.Call(ctx, action, params, timeout...)
	if err != nil {
		return nil, err
	}
	s.record(ctx, action, details)
	return res, nil
}

// Status reads the MongoDB state (mongo.status).
func (s *MongoServerService) Status(ctx context.Context) (*MongoServerStatus, error) {
	res, err := s.agent.Call(ctx, "mongo.status", map[string]any{})
	if err != nil {
		return nil, err
	}
	var st MongoServerStatus
	if err := json.Unmarshal(res, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Repository reads the package repository state (mongo.repository).
func (s *MongoServerService) Repository(ctx context.Context) (*MongoRepository, error) {
	res, err := s.agent.Call(ctx, "mongo.repository", map[string]any{})
	if err != nil {
		return nil, err
	}
	var r MongoRepository
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// EnableRepository adds MongoDB's repository as a background job
// (mongo.repository.enable).
func (s *MongoServerService) EnableRepository(ctx context.Context) (string, error) {
	res, err := s.call(ctx, "mongo.repository.enable", map[string]any{}, map[string]any{})
	if err != nil {
		return "", err
	}
	var o struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return "", err
	}
	return o.JobID, nil
}

// Install installs MongoDB and starts it as a background job (mongo.install).
func (s *MongoServerService) Install(ctx context.Context) (string, error) {
	res, err := s.call(ctx, "mongo.install", map[string]any{}, map[string]any{})
	if err != nil {
		return "", err
	}
	var o struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return "", err
	}
	return o.JobID, nil
}

// Service starts, stops, restarts or enables mongod (services.control). The
// audit action follows the PHP's "service.<op>" with the unit as target.
func (s *MongoServerService) Service(ctx context.Context, op string) error {
	if _, err := s.agent.Call(ctx, "services.control", map[string]any{
		"unit": "mongod.service", "op": op,
	}); err != nil {
		return err
	}
	if s.audit != nil {
		s.audit(ctx, "service."+op, "service", "mongod", "success", map[string]any{})
	}
	return nil
}

// Overview reads every database and user (mongo.overview).
func (s *MongoServerService) Overview(ctx context.Context) (*MongoServerOverview, error) {
	res, err := s.agent.Call(ctx, "mongo.overview", map[string]any{})
	if err != nil {
		return nil, err
	}
	o := &MongoServerOverview{Databases: []MongoDatabase{}, Users: []MongoUser{}}
	if err := json.Unmarshal(res, o); err != nil {
		return nil, err
	}
	if o.Databases == nil {
		o.Databases = []MongoDatabase{}
	}
	if o.Users == nil {
		o.Users = []MongoUser{}
	}
	return o, nil
}

// CreateDatabase creates one database (mongo.database_create).
func (s *MongoServerService) CreateDatabase(ctx context.Context, name string) error {
	_, err := s.call(ctx, "mongo.database_create", map[string]any{"name": name},
		map[string]any{"name": name})
	return err
}

// DropDatabase drops one database; confirm must repeat the name
// (mongo.database_drop).
func (s *MongoServerService) DropDatabase(ctx context.Context, name, confirm string) error {
	_, err := s.call(ctx, "mongo.database_drop", map[string]any{"name": name, "confirm": confirm},
		map[string]any{"name": name})
	return err
}

// CreateUser creates a user in a database (mongo.user_create).
func (s *MongoServerService) CreateUser(ctx context.Context, user, password, db, role string) error {
	_, err := s.call(ctx, "mongo.user_create", map[string]any{
		"user": user, "password": password, "db": db, "role": role,
	}, map[string]any{"user": user, "db": db, "role": role})
	return err
}

// SetAdminPassword creates or resets WhaCP's MongoDB admin user
// (mongo.admin_password); the agent stores the credentials.
func (s *MongoServerService) SetAdminPassword(ctx context.Context, password string) error {
	_, err := s.call(ctx, "mongo.admin_password", map[string]any{"password": password},
		map[string]any{}, 60*time.Second)
	return err
}

// SetAuthorization turns MongoDB authentication on or off; the agent
// restarts the service (mongo.auth_set).
func (s *MongoServerService) SetAuthorization(ctx context.Context, enable bool) error {
	_, err := s.call(ctx, "mongo.auth_set", map[string]any{"enable": enable},
		map[string]any{"enable": enable}, 120*time.Second)
	return err
}

// SetPassword resets one user's password (mongo.user_password).
func (s *MongoServerService) SetPassword(ctx context.Context, user, db, password string) error {
	_, err := s.call(ctx, "mongo.user_password", map[string]any{
		"user": user, "db": db, "password": password,
	}, map[string]any{"user": user, "db": db})
	return err
}

// DropUser removes one user (mongo.user_drop).
func (s *MongoServerService) DropUser(ctx context.Context, user, db string) error {
	_, err := s.call(ctx, "mongo.user_drop", map[string]any{"user": user, "db": db},
		map[string]any{"user": user, "db": db})
	return err
}

// DatabaseUsers lists the users of one database (mongo.database_users).
func (s *MongoServerService) DatabaseUsers(ctx context.Context, db string) ([]MongoUser, error) {
	res, err := s.agent.Call(ctx, "mongo.database_users", map[string]any{"db": db})
	if err != nil {
		return nil, err
	}
	var o struct {
		Users []MongoUser `json:"users"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Users == nil {
		o.Users = []MongoUser{}
	}
	return o.Users, nil
}

// Backup starts a backup of one database (mongo.backup), returning the job
// and the archive name.
func (s *MongoServerService) Backup(ctx context.Context, db string) (*MongoBackupJob, error) {
	res, err := s.call(ctx, "mongo.backup", map[string]any{"db": db}, map[string]any{"db": db})
	if err != nil {
		return nil, err
	}
	var o MongoBackupJob
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	return &o, nil
}

// Backups lists the stored archives, optionally for one database
// (mongo.backups).
func (s *MongoServerService) Backups(ctx context.Context, db string) ([]MongoBackup, error) {
	params := map[string]any{}
	if db != "" {
		params["db"] = db
	}
	res, err := s.agent.Call(ctx, "mongo.backups", params)
	if err != nil {
		return nil, err
	}
	var o struct {
		Backups []MongoBackup `json:"backups"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Backups == nil {
		o.Backups = []MongoBackup{}
	}
	return o.Backups, nil
}

// Restore starts a restore from one archive (mongo.restore), returning the
// job id.
func (s *MongoServerService) Restore(ctx context.Context, file, db string, drop bool) (string, error) {
	res, err := s.call(ctx, "mongo.restore", map[string]any{"file": file, "db": db, "drop": drop},
		map[string]any{"db": db, "file": file, "drop": drop})
	if err != nil {
		return "", err
	}
	var o struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return "", err
	}
	return o.JobID, nil
}

// DeleteBackup removes one archive (mongo.backup_delete).
func (s *MongoServerService) DeleteBackup(ctx context.Context, file string) error {
	_, err := s.call(ctx, "mongo.backup_delete", map[string]any{"file": file},
		map[string]any{"file": file})
	return err
}

// Config reads the parsed mongod.conf (mongo.config_get), filling in the
// agent's own defaults for anything it omits.
func (s *MongoServerService) Config(ctx context.Context) (*MongoConfig, error) {
	res, err := s.agent.Call(ctx, "mongo.config_get", map[string]any{})
	if err != nil {
		return nil, err
	}
	c := &MongoConfig{Path: "/etc/mongod.conf", Port: 27017, BindIP: "127.0.0.1"}
	if err := json.Unmarshal(res, c); err != nil {
		return nil, err
	}
	if c.Path == "" {
		c.Path = "/etc/mongod.conf"
	}
	return c, nil
}

// SetConfig writes the managed settings and restarts (mongo.config_set).
func (s *MongoServerService) SetConfig(ctx context.Context, port int, bindIP string, authorization bool) error {
	_, err := s.call(ctx, "mongo.config_set", map[string]any{
		"port": port, "bind_ip": bindIP, "authorization": authorization,
	}, map[string]any{"port": port, "bind_ip": bindIP, "authorization": authorization}, 120*time.Second)
	return err
}

// WriteConfig replaces the whole mongod.conf and restarts, rolling back on
// failure (mongo.config_write).
func (s *MongoServerService) WriteConfig(ctx context.Context, content string) error {
	_, err := s.call(ctx, "mongo.config_write", map[string]any{"content": content},
		map[string]any{}, 120*time.Second)
	return err
}

// Log reads the tail of the MongoDB log (mongo.log).
func (s *MongoServerService) Log(ctx context.Context, lines int) (*MongoLog, error) {
	if lines <= 0 {
		lines = 300
	}
	res, err := s.agent.Call(ctx, "mongo.log", map[string]any{"lines": lines})
	if err != nil {
		return nil, err
	}
	l := &MongoLog{}
	if err := json.Unmarshal(res, l); err != nil {
		return nil, err
	}
	return l, nil
}

// MongoRoleChoices are the roles the user forms offer, in the panel's order.
var MongoRoleChoices = []string{"readWrite", "read", "dbAdmin", "dbOwner", "userAdmin"}

// MongoSectionValid reports whether a section name is one of the page's
// tabs, defaulting to "databases" like the controller does.
func MongoSectionValid(section string) bool {
	switch section {
	case "databases", "users", "backups", "remotes", "config", "log":
		return true
	}
	return false
}

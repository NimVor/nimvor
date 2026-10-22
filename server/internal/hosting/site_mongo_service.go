package hosting

// SiteMongoService — per-website MongoDB (SiteMongoService.php): the
// customer's own databases and users, prefixed with the website's system
// account ("<user>_" — the same way MySQL databases are prefixed), so each
// customer sees and manages only their own. The drop and password actions
// refuse any name that is not under the account's prefix, so one customer
// can never touch another's MongoDB database or user even by passing a
// crafted name.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// MongoStatus is the server's MongoDB state (mongo.status).
type MongoStatus struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Version   string `json:"version"`
}

// MongoDatabase is one MongoDB database (mongo.overview).
type MongoDatabase struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Empty bool   `json:"empty"`
}

// MongoUser is one MongoDB user (mongo.overview).
type MongoUser struct {
	User  string   `json:"user"`
	DB    string   `json:"db"`
	Roles []string `json:"roles"`
}

// MongoOverview is the account's own databases and users.
type MongoOverview struct {
	Databases []MongoDatabase
	Users     []MongoUser
}

// MongoBackup is one backup archive (mongo.backups).
type MongoBackup struct {
	File    string `json:"file"`
	DB      string `json:"db"`
	Size    int64  `json:"size"`
	Created string `json:"created"`
}

// mongoBackupFileRe is a backup archive name:
// "<20060102-150405>_<db>.archive.gz" (the timestamp is fixed-width, so the
// database — which may contain underscores — is the middle part).
var mongoBackupFileRe = regexp.MustCompile(`^\d{8}-\d{6}_([A-Za-z0-9_][A-Za-z0-9_.-]{0,80})\.archive\.gz$`)

// SiteMongoService manages a website's prefixed MongoDB databases, users
// and backups.
type SiteMongoService struct {
	agent AgentCaller
	audit Auditor
}

// NewSiteMongoService builds the service.
func NewSiteMongoService(ag AgentCaller, audit Auditor) *SiteMongoService {
	return &SiteMongoService{agent: ag, audit: audit}
}

func (s *SiteMongoService) recordAudit(ctx context.Context, action string, site *Website, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, "website", fmt.Sprintf("%d", site.ID), "success", details)
	}
}

// Prefix is the account's name prefix (SiteMongoService::prefix).
func (s *SiteMongoService) Prefix(site *Website) string {
	return strings.ToLower(site.SystemUser) + "_"
}

// Status is the server's MongoDB state (mongo.status).
func (s *SiteMongoService) Status(ctx context.Context) (*MongoStatus, error) {
	res, err := s.agent.Call(ctx, "mongo.status", map[string]any{})
	if err != nil {
		return nil, err
	}
	var st MongoStatus
	if err := json.Unmarshal(res, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// Overview is the account's own databases and users, filtered to its
// prefix (mongo.overview).
func (s *SiteMongoService) Overview(ctx context.Context, site *Website) (*MongoOverview, error) {
	res, err := s.agent.Call(ctx, "mongo.overview", map[string]any{})
	if err != nil {
		return nil, err
	}
	var o struct {
		Databases []MongoDatabase `json:"databases"`
		Users     []MongoUser     `json:"users"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	prefix := s.Prefix(site)
	out := &MongoOverview{Databases: []MongoDatabase{}, Users: []MongoUser{}}
	for _, d := range o.Databases {
		if strings.HasPrefix(strings.ToLower(d.Name), prefix) {
			out.Databases = append(out.Databases, d)
		}
	}
	for _, u := range o.Users {
		if strings.HasPrefix(strings.ToLower(u.DB), prefix) {
			out.Users = append(out.Users, u)
		}
	}
	return out, nil
}

// scoped prepends the account prefix to a customer-supplied name, removing
// a prefix they may have typed themselves so it is never doubled.
func (s *SiteMongoService) scoped(site *Website, name string) (string, error) {
	prefix := s.Prefix(site)
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", &UserError{What: "Enter a name."}
	}
	if strings.HasPrefix(name, prefix) {
		name = strings.TrimPrefix(name, prefix)
	}
	return prefix + name, nil
}

// assertOwned refuses a name that is not under this account's prefix.
func (s *SiteMongoService) assertOwned(site *Website, name string) error {
	if !strings.HasPrefix(strings.ToLower(name), s.Prefix(site)) {
		return &UserError{What: "That database or user does not belong to this website."}
	}
	return nil
}

// CreateDatabase creates "<prefix><name>" (mongo.database_create).
func (s *SiteMongoService) CreateDatabase(ctx context.Context, site *Website, name string) (string, error) {
	full, err := s.scoped(site, name)
	if err != nil {
		return "", err
	}
	if _, err := s.agent.Call(ctx, "mongo.database_create", map[string]any{"name": full}); err != nil {
		return "", err
	}
	s.recordAudit(ctx, "mongo.database_create", site, map[string]any{"name": full})
	return full, nil
}

// DropDatabase drops one of the account's own databases (mongo.database_drop).
func (s *SiteMongoService) DropDatabase(ctx context.Context, site *Website, name string) error {
	if err := s.assertOwned(site, name); err != nil {
		return err
	}
	if _, err := s.agent.Call(ctx, "mongo.database_drop", map[string]any{"name": name, "confirm": name}); err != nil {
		return err
	}
	s.recordAudit(ctx, "mongo.database_drop", site, map[string]any{"name": name})
	return nil
}

// CreateUser creates "<prefix><user>" in one of the account's databases
// (mongo.user_create).
func (s *SiteMongoService) CreateUser(ctx context.Context, site *Website, user, password, db, role string) (string, error) {
	if err := s.assertOwned(site, db); err != nil {
		return "", err
	}
	fullUser, err := s.scoped(site, user)
	if err != nil {
		return "", err
	}
	if _, err := s.agent.Call(ctx, "mongo.user_create", map[string]any{
		"user": fullUser, "password": password, "db": db, "role": role,
	}); err != nil {
		return "", err
	}
	s.recordAudit(ctx, "mongo.user_create", site, map[string]any{"user": fullUser, "db": db, "role": role})
	return fullUser, nil
}

// SetPassword changes one of the account's users' passwords
// (mongo.user_password).
func (s *SiteMongoService) SetPassword(ctx context.Context, site *Website, user, db, password string) error {
	if err := s.assertOwned(site, db); err != nil {
		return err
	}
	if err := s.assertOwned(site, user); err != nil {
		return err
	}
	if _, err := s.agent.Call(ctx, "mongo.user_password", map[string]any{
		"user": user, "db": db, "password": password,
	}); err != nil {
		return err
	}
	s.recordAudit(ctx, "mongo.user_password", site, map[string]any{"user": user, "db": db})
	return nil
}

// DropUser drops one of the account's users (mongo.user_drop).
func (s *SiteMongoService) DropUser(ctx context.Context, site *Website, user, db string) error {
	if err := s.assertOwned(site, db); err != nil {
		return err
	}
	if err := s.assertOwned(site, user); err != nil {
		return err
	}
	if _, err := s.agent.Call(ctx, "mongo.user_drop", map[string]any{"user": user, "db": db}); err != nil {
		return err
	}
	s.recordAudit(ctx, "mongo.user_drop", site, map[string]any{"user": user, "db": db})
	return nil
}

// Backup starts a backup of one of the account's own databases
// (mongo.backup, an async job). Returns the job id and the archive name.
func (s *SiteMongoService) Backup(ctx context.Context, site *Website, db string) (jobID, file string, err error) {
	if err := s.assertOwned(site, db); err != nil {
		return "", "", err
	}
	res, err := s.agent.Call(ctx, "mongo.backup", map[string]any{"db": db})
	if err != nil {
		return "", "", err
	}
	var out struct {
		JobID string `json:"job_id"`
		File  string `json:"file"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", "", err
	}
	s.recordAudit(ctx, "mongo.backup", site, map[string]any{"db": db})
	return out.JobID, out.File, nil
}

// Backups is the account's own backup archives, filtered to its prefix
// (mongo.backups).
func (s *SiteMongoService) Backups(ctx context.Context, site *Website) ([]MongoBackup, error) {
	res, err := s.agent.Call(ctx, "mongo.backups", map[string]any{})
	if err != nil {
		return nil, err
	}
	var o struct {
		Backups []MongoBackup `json:"backups"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	prefix := s.Prefix(site)
	out := []MongoBackup{}
	for _, b := range o.Backups {
		if strings.HasPrefix(strings.ToLower(b.DB), prefix) {
			out = append(out, b)
		}
	}
	return out, nil
}

// backupDb validates a backup archive name and returns the database it
// holds, after checking that database belongs to this account.
func (s *SiteMongoService) backupDb(site *Website, file string) (string, error) {
	m := mongoBackupFileRe.FindStringSubmatch(file)
	if m == nil {
		return "", &UserError{What: "That is not a backup archive."}
	}
	if err := s.assertOwned(site, m[1]); err != nil {
		return "", err
	}
	return m[1], nil
}

// Restore one of the account's own databases from one of its archives
// (mongo.restore, an async job). Returns the job id.
func (s *SiteMongoService) Restore(ctx context.Context, site *Website, file string, drop bool) (string, error) {
	db, err := s.backupDb(site, file)
	if err != nil {
		return "", err
	}
	res, err := s.agent.Call(ctx, "mongo.restore", map[string]any{"file": file, "db": db, "drop": drop})
	if err != nil {
		return "", err
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	s.recordAudit(ctx, "mongo.restore", site, map[string]any{"db": db, "file": file, "drop": drop})
	return out.JobID, nil
}

// DeleteBackup deletes one of the account's own backup archives
// (mongo.backup_delete).
func (s *SiteMongoService) DeleteBackup(ctx context.Context, site *Website, file string) error {
	if _, err := s.backupDb(site, file); err != nil {
		return err
	}
	if _, err := s.agent.Call(ctx, "mongo.backup_delete", map[string]any{"file": file}); err != nil {
		return err
	}
	s.recordAudit(ctx, "mongo.backup_delete", site, map[string]any{"file": file})
	return nil
}

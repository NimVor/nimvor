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
	"time"
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

// --- Database browser (confined to the account's own databases) ---

// MongoCollection is one collection (mongo.collections).
type MongoCollection struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// MongoDocuments is a page of documents (mongo.documents).
type MongoDocuments struct {
	Documents json.RawMessage
	Total     int
	Skip      int
	Limit     int
}

// MongoAggregate is an aggregation result (mongo.aggregate).
type MongoAggregate struct {
	Documents json.RawMessage
	Ms        int
	Limit     int
}

// MongoExport is an export result (mongo.export).
type MongoExport struct {
	Content string
	Count   int
	Format  string
}

// MongoImport is an import result (mongo.import).
type MongoImport struct {
	Inserted int
	Errors   int
	Total    int
}

// MongoSchema is a schema analysis (mongo.schema).
type MongoSchema struct {
	Sampled int
	Fields  json.RawMessage
}

// MongoDBUser is a user on one database (mongo.database_users).
type MongoDBUser struct {
	User  string   `json:"user"`
	Roles []string `json:"roles"`
}

// DatabaseUsers is the users defined on one of the account's own databases
// (mongo.database_users).
func (s *SiteMongoService) DatabaseUsers(ctx context.Context, site *Website, db string) ([]MongoDBUser, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.database_users", map[string]any{"db": db})
	if err != nil {
		return nil, err
	}
	var o struct {
		Users []MongoDBUser `json:"users"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	return o.Users, nil
}

// Collections are the collections in one of the account's own databases
// (mongo.collections).
func (s *SiteMongoService) Collections(ctx context.Context, site *Website, db string) ([]MongoCollection, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.collections", map[string]any{"db": db})
	if err != nil {
		return nil, err
	}
	var o struct {
		Collections []MongoCollection `json:"collections"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	return o.Collections, nil
}

// CreateCollection creates a collection in the account's own database
// (mongo.collection_create).
func (s *SiteMongoService) CreateCollection(ctx context.Context, site *Website, db, collection string) error {
	if err := s.assertOwned(site, db); err != nil {
		return err
	}
	if _, err := s.agent.Call(ctx, "mongo.collection_create", map[string]any{"db": db, "collection": collection}); err != nil {
		return err
	}
	s.recordAudit(ctx, "mongo.collection_create", site, map[string]any{"db": db, "collection": collection})
	return nil
}

// DropCollection drops a collection in the account's own database
// (mongo.collection_drop).
func (s *SiteMongoService) DropCollection(ctx context.Context, site *Website, db, collection string) error {
	if err := s.assertOwned(site, db); err != nil {
		return err
	}
	if _, err := s.agent.Call(ctx, "mongo.collection_drop", map[string]any{"db": db, "collection": collection}); err != nil {
		return err
	}
	s.recordAudit(ctx, "mongo.collection_drop", site, map[string]any{"db": db, "collection": collection})
	return nil
}

// EmptyCollection deletes every document in one of the account's
// collections (mongo.collection_empty). Returns the number deleted.
func (s *SiteMongoService) EmptyCollection(ctx context.Context, site *Website, db, collection string) (int, error) {
	if err := s.assertOwned(site, db); err != nil {
		return 0, err
	}
	res, err := s.agent.Call(ctx, "mongo.collection_empty", map[string]any{"db": db, "collection": collection}, 90*time.Second)
	if err != nil {
		return 0, err
	}
	var o struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return 0, err
	}
	s.recordAudit(ctx, "mongo.collection_empty", site, map[string]any{"db": db, "collection": collection})
	return o.Deleted, nil
}

// RenameCollection renames a collection in the account's own database
// (mongo.collection_rename).
func (s *SiteMongoService) RenameCollection(ctx context.Context, site *Website, db, collection, name string) error {
	if err := s.assertOwned(site, db); err != nil {
		return err
	}
	if _, err := s.agent.Call(ctx, "mongo.collection_rename", map[string]any{"db": db, "collection": collection, "name": name}); err != nil {
		return err
	}
	s.recordAudit(ctx, "mongo.collection_rename", site, map[string]any{"db": db, "collection": collection, "name": name})
	return nil
}

// Documents is one page of a collection in the account's own database
// (mongo.documents).
func (s *SiteMongoService) Documents(ctx context.Context, site *Website, db, collection, filter, sort, projection string, skip, limit int) (*MongoDocuments, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.documents", map[string]any{
		"db": db, "collection": collection, "filter": filter,
		"sort": sort, "projection": projection, "skip": skip, "limit": limit,
	}, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Documents json.RawMessage `json:"documents"`
		Total     int             `json:"total"`
		Skip      int             `json:"skip"`
		Limit     int             `json:"limit"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	out := &MongoDocuments{Documents: o.Documents, Total: o.Total, Skip: o.Skip, Limit: o.Limit}
	if out.Documents == nil {
		out.Documents = json.RawMessage("[]")
	}
	if out.Skip == 0 {
		out.Skip = skip
	}
	if out.Limit == 0 {
		out.Limit = limit
	}
	return out, nil
}

// DeleteDocument deletes one document by its _id (EJSON) from the
// account's own database (mongo.document_delete). Returns the number
// deleted.
func (s *SiteMongoService) DeleteDocument(ctx context.Context, site *Website, db, collection, id string) (int, error) {
	if err := s.assertOwned(site, db); err != nil {
		return 0, err
	}
	res, err := s.agent.Call(ctx, "mongo.document_delete", map[string]any{"db": db, "collection": collection, "id": id})
	if err != nil {
		return 0, err
	}
	var o struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return 0, err
	}
	s.recordAudit(ctx, "mongo.document_delete", site, map[string]any{"db": db, "collection": collection})
	return o.Deleted, nil
}

// InsertDocument inserts one document (EJSON) into the account's own
// database (mongo.document_insert).
func (s *SiteMongoService) InsertDocument(ctx context.Context, site *Website, db, collection, document string) (any, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.document_insert", map[string]any{"db": db, "collection": collection, "document": document})
	if err != nil {
		return nil, err
	}
	var o struct {
		InsertedID any `json:"inserted_id"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, "mongo.document_insert", site, map[string]any{"db": db, "collection": collection})
	return o.InsertedID, nil
}

// UpdateDocument replaces a document (found by its _id EJSON) in the
// account's own database (mongo.document_update).
func (s *SiteMongoService) UpdateDocument(ctx context.Context, site *Website, db, collection, id, document string) (int, int, error) {
	if err := s.assertOwned(site, db); err != nil {
		return 0, 0, err
	}
	res, err := s.agent.Call(ctx, "mongo.document_update", map[string]any{
		"db": db, "collection": collection, "id": id, "document": document,
	})
	if err != nil {
		return 0, 0, err
	}
	var o struct {
		Matched  int `json:"matched"`
		Modified int `json:"modified"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return 0, 0, err
	}
	s.recordAudit(ctx, "mongo.document_update", site, map[string]any{"db": db, "collection": collection})
	return o.Matched, o.Modified, nil
}

// Aggregate runs an aggregation pipeline in the account's own database
// (mongo.aggregate).
func (s *SiteMongoService) Aggregate(ctx context.Context, site *Website, db, collection, pipeline string, limit int) (*MongoAggregate, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.aggregate", map[string]any{
		"db": db, "collection": collection, "pipeline": pipeline, "limit": limit,
	}, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Documents json.RawMessage `json:"documents"`
		Ms        int             `json:"ms"`
		Limit     int             `json:"limit"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	out := &MongoAggregate{Documents: o.Documents, Ms: o.Ms, Limit: o.Limit}
	if out.Documents == nil {
		out.Documents = json.RawMessage("[]")
	}
	if out.Limit == 0 {
		out.Limit = limit
	}
	return out, nil
}

// Export documents of a collection in the account's own database as a
// JSON array or JSONL (mongo.export, bounded).
func (s *SiteMongoService) Export(ctx context.Context, site *Website, db, collection, format, filter string, limit int) (*MongoExport, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.export", map[string]any{
		"db": db, "collection": collection, "format": format, "filter": filter, "limit": limit,
	}, 120*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Content string `json:"content"`
		Count   int    `json:"count"`
		Format  string `json:"format"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	out := &MongoExport{Content: o.Content, Count: o.Count, Format: o.Format}
	if out.Format == "" {
		out.Format = format
	}
	s.recordAudit(ctx, "mongo.export", site, map[string]any{"db": db, "collection": collection, "format": out.Format})
	return out, nil
}

// Import documents into a collection of the account's own database
// (mongo.import).
func (s *SiteMongoService) Import(ctx context.Context, site *Website, db, collection, format, content string) (*MongoImport, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.import", map[string]any{
		"db": db, "collection": collection, "format": format, "content": content,
	}, 120*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Inserted int `json:"inserted"`
		Errors   int `json:"errors"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, "mongo.import", site, map[string]any{"db": db, "collection": collection, "format": format})
	return &MongoImport{Inserted: o.Inserted, Errors: o.Errors, Total: o.Total}, nil
}

// Schema analyses a collection of the account's own database over a
// bounded sample (mongo.schema).
func (s *SiteMongoService) Schema(ctx context.Context, site *Website, db, collection string, sample int) (*MongoSchema, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.schema", map[string]any{
		"db": db, "collection": collection, "sample": sample,
	}, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Sampled int             `json:"sampled"`
		Fields  json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	out := &MongoSchema{Sampled: o.Sampled, Fields: o.Fields}
	if out.Fields == nil {
		out.Fields = json.RawMessage("[]")
	}
	return out, nil
}

// Explain explains an aggregation pipeline in the account's own database
// (mongo.explain).
func (s *SiteMongoService) Explain(ctx context.Context, site *Website, db, collection, pipeline string) (json.RawMessage, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.explain", map[string]any{
		"db": db, "collection": collection, "pipeline": pipeline,
	}, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Explain json.RawMessage `json:"explain"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Explain == nil {
		o.Explain = json.RawMessage("{}")
	}
	return o.Explain, nil
}

// Indexes are the indexes of a collection in the account's own database
// (mongo.indexes).
func (s *SiteMongoService) Indexes(ctx context.Context, site *Website, db, collection string) (json.RawMessage, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.indexes", map[string]any{"db": db, "collection": collection})
	if err != nil {
		return nil, err
	}
	var o struct {
		Indexes json.RawMessage `json:"indexes"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Indexes == nil {
		o.Indexes = json.RawMessage("[]")
	}
	return o.Indexes, nil
}

// CollectionStats is the size, counts and validation rules for one of the
// account's collections (mongo.collection_stats).
func (s *SiteMongoService) CollectionStats(ctx context.Context, site *Website, db, collection string) (json.RawMessage, error) {
	if err := s.assertOwned(site, db); err != nil {
		return nil, err
	}
	res, err := s.agent.Call(ctx, "mongo.collection_stats", map[string]any{"db": db, "collection": collection})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// CreateIndex creates an index in the account's own database
// (mongo.index_create). Returns the created index's name.
func (s *SiteMongoService) CreateIndex(ctx context.Context, site *Website, db, collection, keys, options string) (string, error) {
	if err := s.assertOwned(site, db); err != nil {
		return "", err
	}
	res, err := s.agent.Call(ctx, "mongo.index_create", map[string]any{
		"db": db, "collection": collection, "keys": keys, "options": options,
	}, 300*time.Second)
	if err != nil {
		return "", err
	}
	var o struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return "", err
	}
	s.recordAudit(ctx, "mongo.index_create", site, map[string]any{"db": db, "collection": collection})
	return o.Name, nil
}

// DropIndex drops an index in the account's own database (mongo.index_drop).
func (s *SiteMongoService) DropIndex(ctx context.Context, site *Website, db, collection, name string) error {
	if err := s.assertOwned(site, db); err != nil {
		return err
	}
	if _, err := s.agent.Call(ctx, "mongo.index_drop", map[string]any{"db": db, "collection": collection, "name": name}); err != nil {
		return err
	}
	s.recordAudit(ctx, "mongo.index_drop", site, map[string]any{"db": db, "collection": collection, "name": name})
	return nil
}

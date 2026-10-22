package hosting

// The job-based database operations (DatabasesController::dump / ::import):
// DatabaseService::dump and DatabaseService::importFromSite.

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

var (
	// A .sql or .sql.gz file, relative to the website folder.
	dbImportPathRe = regexp.MustCompile(`\.sql(\.gz)?$`)
	// Only an upload the panel itself named is removed afterwards.
	dbImportUploadRe = regexp.MustCompile(`^tmp/whacp-import-[a-z0-9]{16}\.sql(\.gz)?$`)
)

// jobIDOut is the agent's job-start reply.
type jobIDOut struct {
	JobID string `json:"job_id"`
	File  string `json:"file"`
}

// DumpDatabase starts a dump job for a database the actor may see (the file
// goes to the server backup directory). Returns the job id and the database
// name (for the tracked job's kind and the messages). Mirrors
// DatabaseService::dump.
func (s *DatabaseService) DumpDatabase(ctx context.Context, serverID int64, actor *identity.User, id int64) (string, string, error) {
	db, err := s.db.Find(ctx, serverID, id, actor)
	if err != nil {
		return "", "", err // ErrDatabaseNotFound (out of tenant) or a real error
	}
	res, err := s.agent.Call(ctx, "db.dump", map[string]any{"name": db.Name})
	if err != nil {
		return "", "", &UserError{
			What: "The dump could not be started on the server.",
			Why:  err.Error(),
			Fix:  "Check the database service is running and try again.",
		}
	}
	var out jobIDOut
	if err := json.Unmarshal(res, &out); err != nil || out.JobID == "" {
		return "", "", &UserError{
			What: "The database server did not start the dump.",
			Why:  "unexpected db.dump reply: " + string(res),
			Fix:  "Try again; if it keeps happening, check the database service.",
		}
	}
	if s.auditlog != nil {
		s.auditlog("database.dump", "database", db.Name, map[string]any{"file": out.File})
	}
	return out.JobID, db.Name, nil
}

// ImportFromSite imports a .sql or .sql.gz file of a database's website (a
// path relative to its folder) as a job. Only a panel-named upload
// (tmp/whacp-import-<16hex>.sql(.gz)) is removed afterwards. Returns the job
// id. Mirrors DatabaseService::importFromSite.
func (s *DatabaseService) ImportFromSite(ctx context.Context, db *Database, site *Website, path string, uploaded bool) (string, error) {
	path = strings.TrimLeft(strings.TrimSpace(path), "/")
	if !dbImportPathRe.MatchString(path) || strings.Contains(path, "..") || len(path) > 255 {
		return "", &ValidationError{Fields: map[string]string{
			"path": "Choose a .sql or .sql.gz file inside the website folder.",
		}}
	}
	remove := uploaded && dbImportUploadRe.MatchString(path)
	res, err := s.agent.Call(ctx, "db.import_site", map[string]any{
		"name": db.Name, "domain": site.Domain, "path": path, "remove_after": remove,
	})
	if err != nil {
		return "", &UserError{
			What: "The import could not be started on the server.",
			Why:  err.Error(),
			Fix:  "Check the file is in the website folder and the database service is running, then try again.",
		}
	}
	var out jobIDOut
	if err := json.Unmarshal(res, &out); err != nil || out.JobID == "" {
		return "", &UserError{
			What: "The database server did not start the import.",
			Why:  "unexpected db.import_site reply: " + string(res),
			Fix:  "Try again; if it keeps happening, check the database service.",
		}
	}
	if s.auditlog != nil {
		s.auditlog("database.import", "database", db.Name, map[string]any{"website": site.Domain, "file": path})
	}
	return out.JobID, nil
}

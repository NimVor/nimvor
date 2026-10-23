package hosting

// This file ports ImportService: importing a discovered (non-WhaCP) website
// into WhaCP management. The agent previews the plan, copies the files into a
// new WhaCP website and switches Nginx over, restoring the old configuration
// on any error. The old configuration and files are left in place, and an
// import can be undone.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ImportPlan is the spec, database row and source for importing a discovered
// website (ImportService::plan).
type ImportPlan struct {
	Row    Website
	Names  []string // primary first, then aliases
	Source string   // the website's current document root
}

// WebsiteImport is one website_imports row.
type WebsiteImport struct {
	ID           int64
	WebsiteID    int64
	Domain       string
	Source       string
	DatabaseName string
	Status       string // running | succeeded | failed | undone
	Error        string
	JobID        string
	UserID       *int64
	CreatedAt    string
	FinishedAt   *string
}

// ImportService imports discovered websites.
type ImportService struct {
	db        *sql.DB
	agent     AgentCaller
	websites  *WebsiteService
	sites     *WebsiteRepository
	discovery *DiscoveryService
	serverID  int64
	audit     Auditor
}

// NewImportService builds the service.
func NewImportService(db *sql.DB, agent AgentCaller, websites *WebsiteService,
	sites *WebsiteRepository, discovery *DiscoveryService, serverID int64, audit Auditor) *ImportService {
	return &ImportService{db: db, agent: agent, websites: websites, sites: sites,
		discovery: discovery, serverID: serverID, audit: audit}
}

// recordAudit is the nil-safe audit hook.
func (s *ImportService) recordAudit(ctx context.Context, action, targetType, target, result string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, targetType, target, result, details)
	}
}

// Plan builds the import plan for a discovered website
// (ImportService::plan). found is the discovery record; input the choices
// (system_user, php_version).
func (s *ImportService) Plan(ctx context.Context, found *DiscoveredSite, input map[string]string) (*ImportPlan, error) {
	kind := "static"
	if found.ProxyPass != "" {
		kind = "proxy"
	} else if found.PHP != nil {
		kind = "php"
	}
	php := ""
	if found.PHP != nil {
		php = found.PHP.Version
	}
	if v, ok := input["php_version"]; ok && v != "" {
		php = v
	}
	if kind == "php" {
		available := s.websites.PhpVersions(ctx)
		if !containsStr(available, php) {
			if len(available) > 0 {
				php = available[len(available)-1]
			} else {
				php = ""
			}
		}
	}
	// The import always uses the server-wide PHP profile (PHP's plan() calls
	// validate() with its default serverSettings = true).
	data, err := s.websites.Validate(ctx, map[string]string{
		"domain":      found.Domain,
		"aliases":     strings.Join(found.Aliases, "\n"),
		"kind":        kind,
		"php_version": php,
		"proxy_pass":  found.ProxyPass,
		"system_user": input["system_user"],
	}, nil, true)
	if err != nil {
		return nil, err
	}
	row := data.Row
	// A Let's Encrypt certificate the site already uses is adopted as-is.
	if found.SSL != nil && found.SSL.CertFile != "" &&
		strings.HasPrefix(found.SSL.CertFile, "/etc/letsencrypt/live/") &&
		strings.HasPrefix(found.SSL.KeyFile, "/etc/letsencrypt/live/") {
		row.HTTPS = true
		row.ForceHTTPS = found.HTTPSRedirect
		row.SSLMode = "letsencrypt"
		row.CertFile = found.SSL.CertFile
		row.KeyFile = found.SSL.KeyFile
	}
	return &ImportPlan{Row: row, Names: data.Names, Source: found.DocumentRoot}, nil
}

// specFrom is the agent spec for an import (ImportService::specFrom): the
// row's own names[1:] as aliases and the import's fixed defaults, as a
// new-website spec.
func (s *ImportService) specFrom(plan *ImportPlan) map[string]any {
	w := plan.Row
	aliases := []string{}
	if len(plan.Names) > 1 {
		aliases = plan.Names[1:]
	}
	w.Aliases = aliases
	w.HTTPS = false
	w.ForceHTTPS = false
	w.HSTS = false
	w.CertFile = ""
	w.KeyFile = ""
	w.Enabled = true
	return Spec(&w, true)
}

// ImportPreview is the agent's adopt.preview reply.
type ImportPreview struct {
	Domain         string   `json:"domain"`
	Source         string   `json:"source"`
	Target         string   `json:"target"`
	User           string   `json:"user"`
	Files          int      `json:"files"`
	Bytes          int64    `json:"bytes"`
	SkippedLinks   []string `json:"skipped_links"`
	Database       string   `json:"database"`
	DatabaseSource string   `json:"database_source"`
	NewConfig      string   `json:"new_config"`
	OldConfig      string   `json:"old_config"`
	Blockers       []string `json:"blockers"`
	Warnings       []string `json:"warnings"`
	Steps          []struct {
		File   string   `json:"file"`
		Action string   `json:"action"` // unlink | move | (already disabled)
		Path   string   `json:"path"`
		Stored string   `json:"stored"`
		Names  []string `json:"names"`
	} `json:"steps"`
}

// Preview asks the agent for the import plan preview (120 s timeout).
func (s *ImportService) Preview(ctx context.Context, plan *ImportPlan) (*ImportPreview, error) {
	res, err := s.agent.Call(ctx, "adopt.preview", map[string]any{
		"spec":   s.specFrom(plan),
		"source": plan.Source,
	}, 120*time.Second)
	if err != nil {
		return nil, err
	}
	var out ImportPreview
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Run starts the import; returns [website id, job id]
// (ImportService::run). A blocked preview is a user-facing error.
func (s *ImportService) Run(ctx context.Context, plan *ImportPlan, actorID int64, ownerID *int64) (int64, string, error) {
	preview, err := s.Preview(ctx, plan)
	if err != nil {
		return 0, "", err
	}
	if len(preview.Blockers) > 0 {
		return 0, "", &UserError{What: "The import is blocked.", Why: strings.Join(preview.Blockers, " ")}
	}
	row := plan.Row
	if ownerID != nil {
		row.OwnerID = sql.NullInt64{Int64: *ownerID, Valid: true}
	}
	row.Status = "importing"
	id, err := s.sites.Insert(ctx, s.serverID, &row)
	if err != nil {
		return 0, "", err
	}
	if err := s.sites.ReplaceDomains(ctx, s.serverID, id, plan.Names); err != nil {
		return 0, "", err
	}
	res, err := s.agent.Call(ctx, "adopt.run", map[string]any{
		"spec":   s.specFrom(plan),
		"source": plan.Source,
	})
	if err != nil {
		_ = s.sites.DeleteSite(ctx, id)
		return 0, "", err
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		_ = s.sites.DeleteSite(ctx, id)
		return 0, "", err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO website_imports (website_id, domain, source, database_name, job_id, user_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, row.Domain, plan.Source, preview.Database, out.JobID, actorID, now); err != nil {
		return 0, "", err
	}
	s.recordAudit(ctx, "website.import", "website", fmt.Sprintf("%d", id), "success",
		map[string]any{"domain": row.Domain, "source": plan.Source, "job": out.JobID})
	return id, out.JobID, nil
}

// ImportOf returns the website's newest import record (nil when there is
// none), mirroring ImportService::importOf.
func (s *ImportService) ImportOf(ctx context.Context, websiteID int64) (*WebsiteImport, error) {
	var imp WebsiteImport
	var by sql.NullInt64
	var finished sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, website_id, domain, source, database_name, status, error, job_id, user_id, created_at, finished_at
		 FROM website_imports WHERE website_id = ? ORDER BY id DESC LIMIT 1`, websiteID).
		Scan(&imp.ID, &imp.WebsiteID, &imp.Domain, &imp.Source, &imp.DatabaseName, &imp.Status,
			&imp.Error, &imp.JobID, &by, &imp.CreatedAt, &finished)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	imp.UserID = nullIntPtr(by)
	if finished.Valid {
		imp.FinishedAt = &finished.String
	}
	return &imp, nil
}

// Refresh finishes a running import once its job is done: activate the
// website and register the application's database, or clean up after a
// failure (ImportService::refresh). It reports whether the website still
// exists.
func (s *ImportService) Refresh(ctx context.Context, site *Website) (bool, error) {
	imp, err := s.ImportOf(ctx, site.ID)
	if err != nil {
		return false, err
	}
	if imp == nil || imp.Status != "running" {
		return true, nil
	}
	res, err := s.agent.Call(ctx, "jobs.get", map[string]any{"id": imp.JobID})
	if err != nil {
		return true, nil
	}
	var job struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(res, &job) != nil {
		return true, nil
	}
	now := time.Now().UTC().Format(time.RFC3339)
	switch job.Status {
	case "succeeded":
		if _, err := s.db.ExecContext(ctx,
			`UPDATE website_imports SET status = 'succeeded', finished_at = ? WHERE id = ?`, now, imp.ID); err != nil {
			return false, err
		}
		if err := s.sites.SetStatus(ctx, site.ID, "active", ""); err != nil {
			return false, err
		}
		if imp.DatabaseName != "" {
			var existing int
			if err := s.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM databases WHERE server_id = ? AND name = ?`, s.serverID, imp.DatabaseName).
				Scan(&existing); err != nil {
				return false, err
			}
			if existing == 0 {
				// Track the existing database (it is not moved or changed).
				names := []string{}
				if live, err := s.LiveDBNames(ctx); err == nil {
					names = live
				}
				if containsStr(names, imp.DatabaseName) {
					owner := site.OwnerID
					if _, err := s.db.ExecContext(ctx,
						`INSERT INTO databases (server_id, website_id, owner_id, name, created_at)
						 VALUES (?, ?, ?, ?, ?)`, s.serverID, site.ID, nullOwner(owner), imp.DatabaseName, now); err != nil {
						return false, err
					}
				}
			}
		}
		_ = s.discovery.Invalidate(ctx, s.serverID)
		s.recordAudit(ctx, "website.import_done", "website", fmt.Sprintf("%d", site.ID), "success",
			map[string]any{"domain": site.Domain})
		return true, nil
	case "failed":
		errText := job.Error
		if r := []rune(errText); len(r) > 500 {
			errText = string(r[:500])
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE website_imports SET status = 'failed', error = ?, finished_at = ? WHERE id = ?`,
			errText, now, imp.ID); err != nil {
			return false, err
		}
		if err := s.removeNewSite(ctx, site); err != nil {
			return false, err
		}
		s.recordAudit(ctx, "website.import_done", "website", fmt.Sprintf("%d", site.ID), "failure",
			map[string]any{"domain": site.Domain, "error": job.Error})
		return false, nil
	}
	return true, nil
}

// LiveDBNames is the database server's database names ("" on failure, as the
// refresh treats an unreachable server).
func (s *ImportService) LiveDBNames(ctx context.Context) ([]string, error) {
	res, err := s.agent.Call(ctx, "db.overview", nil)
	if err != nil {
		return nil, err
	}
	var out struct {
		Databases []struct {
			Name string `json:"name"`
		} `json:"databases"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	out2 := []string{}
	for _, d := range out.Databases {
		out2 = append(out2, d.Name)
	}
	return out2, nil
}

// removeNewSite deletes the import's new website from the server and the
// panel (ImportService::removeNewSite).
func (s *ImportService) removeNewSite(ctx context.Context, site *Website) error {
	_, _ = s.agent.Call(ctx, "sites.delete", map[string]any{
		"domain":       site.Domain,
		"user":         site.SystemUser,
		"remove_files": true,
		"remove_user":  true,
	})
	if err := s.sites.DeleteSite(ctx, site.ID); err != nil {
		return err
	}
	return s.discovery.Invalidate(ctx, s.serverID)
}

// Undo undoes a finished import: the old configuration serves the website
// again and the WhaCP copy is removed (the original files were never
// touched), mirroring ImportService::undo.
func (s *ImportService) Undo(ctx context.Context, site *Website) error {
	imp, err := s.ImportOf(ctx, site.ID)
	if err != nil {
		return err
	}
	if imp == nil || imp.Status != "succeeded" {
		return &UserError{What: "This website was not imported, or its import is not finished."}
	}
	// site is the website's full row (the callers pass FindFull results).
	if _, err := s.agent.Call(ctx, "adopt.undo", map[string]any{
		"spec":   Spec(site, false),
		"source": imp.Source,
	}); err != nil {
		return err
	}
	if imp.DatabaseName != "" {
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM databases WHERE website_id = ? AND name = ?`, site.ID, imp.DatabaseName); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE website_imports SET status = 'undone', finished_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), imp.ID); err != nil {
		return err
	}
	if err := s.removeNewSite(ctx, site); err != nil {
		return err
	}
	s.recordAudit(ctx, "website.import_undo", "website", fmt.Sprintf("%d", site.ID), "success",
		map[string]any{"domain": site.Domain, "source": imp.Source})
	return nil
}

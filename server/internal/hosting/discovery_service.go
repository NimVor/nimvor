package hosting

// This file ports DiscoveryService and DiscoverySnapshotRepository: the
// read-only server inventory. The agent performs discovery; the panel stores
// the result as a snapshot so pages load quickly and there is a history of
// what was observed. Nothing here modifies the server. The snapshot store
// keeps the raw JSON (as the PHP data column) and the helpers parse only the
// parts a page needs. The /discovery page's scoping helpers (forScope,
// summary) land with that page.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// DiscoverySnapshot is one stored inventory (discovery_snapshots row).
type DiscoverySnapshot struct {
	ID           int64
	CreatedAt    string
	CreatedBy    *int64
	WebsiteCount int
	Checksum     string
	Data         json.RawMessage
}

// DiscoverySnapshotRepository reads and writes inventory snapshots.
type DiscoverySnapshotRepository struct{ db *sql.DB }

// NewDiscoverySnapshotRepository wraps a database handle.
func NewDiscoverySnapshotRepository(db *sql.DB) *DiscoverySnapshotRepository { return &DiscoverySnapshotRepository{db: db} }

// Latest returns the newest snapshot for the server (nil when there is
// none), mirroring DiscoverySnapshotRepository::latest.
func (r *DiscoverySnapshotRepository) Latest(ctx context.Context, serverID int64) (*DiscoverySnapshot, error) {
	var s DiscoverySnapshot
	var createdBy sql.NullInt64
	var data string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, created_at, created_by, website_count, checksum, data
		 FROM discovery_snapshots WHERE server_id = ? ORDER BY id DESC LIMIT 1`, serverID).
		Scan(&s.ID, &s.CreatedAt, &createdBy, &s.WebsiteCount, &s.Checksum, &data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	s.CreatedBy = nullIntPtr(createdBy)
	s.Data = json.RawMessage(data)
	return &s, nil
}

// Store inserts a snapshot and keeps the newest keep rows, mirroring
// DiscoverySnapshotRepository::store.
func (r *DiscoverySnapshotRepository) Store(ctx context.Context, serverID int64, data json.RawMessage, websiteCount int, userID *int64, keep int) (int64, error) {
	sum := sha256.Sum256(data)
	now := time.Now().UTC().Format(time.RFC3339)
	var by any
	if userID != nil {
		by = *userID
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO discovery_snapshots (server_id, created_at, created_by, website_count, checksum, data)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		serverID, now, by, websiteCount, fmt.Sprintf("%x", sum), string(data))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	_, err = r.db.ExecContext(ctx,
		`DELETE FROM discovery_snapshots WHERE server_id = ? AND id NOT IN
		 (SELECT id FROM discovery_snapshots WHERE server_id = ? ORDER BY id DESC LIMIT ?)`,
		serverID, serverID, keep)
	return id, err
}

// DiscoveredPHP is a website's PHP-FPM binding as discovery reports it.
type DiscoveredPHP struct {
	Version  string `json:"version"`
	Pool     string `json:"pool"`
	User     string `json:"user"`
	Listen   string `json:"listen"`
	Resolved bool   `json:"resolved"`
}

// DiscoveredSSL is a website's certificate as discovery reports it.
type DiscoveredSSL struct {
	Status        string `json:"status"`
	Error         string `json:"error"`
	NotAfter      string `json:"not_after"`
	Issuer        string `json:"issuer"`
	DaysRemaining int    `json:"days_remaining"`
	CoversDomain  bool   `json:"covers_domain"`
	LetsEncrypt   bool   `json:"lets_encrypt"`
	CertFile      string `json:"cert_file"`
	KeyFile       string `json:"key_file"`
}

// DiscoveredSite is one website of the inventory (a server block in the
// Nginx configuration), as the import and discovery pages read it.
type DiscoveredSite struct {
	ID                 string        `json:"id"`
	Domain             string        `json:"domain"`
	Aliases            []string      `json:"aliases"`
	Kind               string        `json:"kind"`
	Enabled            bool          `json:"enabled"`
	HTTPS              bool          `json:"https"`
	HTTPSRedirect      bool          `json:"https_redirect"`
	DocumentRoot       string        `json:"document_root"`
	DocumentRootExists bool          `json:"document_root_exists"`
	ProxyPass          string        `json:"proxy_pass"`
	ManagedBy          string        `json:"managed_by"`
	Listen             []string      `json:"listen"`
	ConfigFiles        []string      `json:"config_files"`
	AccessLogs         []string      `json:"access_logs"`
	ErrorLogs          []string      `json:"error_logs"`
	PHP                *DiscoveredPHP `json:"php"`
	SSL                *DiscoveredSSL `json:"ssl"`
}

// DiscoveredCronJob is one system cron job of the inventory.
type DiscoveredCronJob struct {
	Schedule string `json:"schedule"`
	User     string `json:"user"`
	Command  string `json:"command"`
}

// InventoryWebsites parses the inventory's website list
// (DiscoveryService::websites).
func InventoryWebsites(data json.RawMessage) []DiscoveredSite {
	var inv struct {
		Websites struct {
			Websites []DiscoveredSite `json:"websites"`
		} `json:"websites"`
	}
	out := []DiscoveredSite{}
	if json.Unmarshal(data, &inv) == nil {
		out = inv.Websites.Websites
	}
	if out == nil {
		out = []DiscoveredSite{}
	}
	return out
}

// InventoryCronJobs parses the inventory's system cron jobs.
func InventoryCronJobs(data json.RawMessage) []DiscoveredCronJob {
	var inv struct {
		Cron struct {
			Jobs []DiscoveredCronJob `json:"jobs"`
		} `json:"cron"`
	}
	out := []DiscoveredCronJob{}
	if json.Unmarshal(data, &inv) == nil {
		out = inv.Cron.Jobs
	}
	if out == nil {
		out = []DiscoveredCronJob{}
	}
	return out
}

// InventoryWebsite looks up one discovered website by its discovery id
// (DiscoveryService::website); nil when it is not in the snapshot.
func InventoryWebsite(data json.RawMessage, id string) *DiscoveredSite {
	for i := range InventoryWebsites(data) {
		if InventoryWebsites(data)[i].ID == id {
			s := InventoryWebsites(data)[i]
			return &s
		}
	}
	return nil
}

// InventoryResult is the latest inventory (cached or freshly run).
type InventoryResult struct {
	Data      json.RawMessage
	CreatedAt string
	FromCache bool
}

// DiscoveryService reads the server inventory (read-only).
type DiscoveryService struct {
	db    *sql.DB
	agent AgentCaller
	cfg   *settings.Repository
}

// NewDiscoveryService builds the service.
func NewDiscoveryService(db *sql.DB, agent AgentCaller, cfg *settings.Repository) *DiscoveryService {
	return &DiscoveryService{db: db, agent: agent, cfg: cfg}
}

// Invalidate forces the next Inventory call to ask the agent (after a
// change), mirroring DiscoveryService::invalidate.
func (s *DiscoveryService) Invalidate(ctx context.Context, serverID int64) error {
	return s.cfg.Set(ctx, fmt.Sprintf("_discovery_invalidated_at:%d", serverID),
		time.Now().UTC().Format(time.RFC3339), 0)
}

// Inventory returns the latest inventory, refreshed from the agent when it is
// older than the configured cache time or was invalidated (or when refresh is
// true), mirroring DiscoveryService::inventory.
func (s *DiscoveryService) Inventory(ctx context.Context, serverID int64, refresh bool, userID *int64) (*InventoryResult, error) {
	latest, err := NewDiscoverySnapshotRepository(s.db).Latest(ctx, serverID)
	if err != nil {
		return nil, err
	}
	fresh := false
	if latest != nil {
		ttl, _ := s.cfg.Int(ctx, "discovery_cache_seconds", 300)
		invalidated, _ := s.cfg.String(ctx, fmt.Sprintf("_discovery_invalidated_at:%d", serverID), "")
		if created, err := time.Parse(time.RFC3339, latest.CreatedAt); err == nil {
			fresh = !created.Before(time.Now().Add(-time.Duration(ttl) * time.Second))
			if fresh && invalidated != "" {
				if inv, err := time.Parse(time.RFC3339, invalidated); err == nil {
					fresh = created.After(inv)
				} else {
					fresh = false
				}
			}
		}
	}
	if !refresh && fresh {
		return &InventoryResult{Data: latest.Data, CreatedAt: latest.CreatedAt, FromCache: true}, nil
	}
	return s.Run(ctx, serverID, userID)
}

// Run asks the agent for a fresh inventory and stores it, mirroring
// DiscoveryService::run (the discovery.run audit rides with the manual route).
func (s *DiscoveryService) Run(ctx context.Context, serverID int64, userID *int64) (*InventoryResult, error) {
	raw, err := s.agent.Call(ctx, "discovery.inventory", nil)
	if err != nil {
		return nil, err
	}
	count := 0
	if data := InventoryWebsites(raw); len(data) > 0 {
		count = len(data)
	}
	if _, err := NewDiscoverySnapshotRepository(s.db).Store(ctx, serverID, raw, count, userID, 20); err != nil {
		return nil, err
	}
	return &InventoryResult{Data: raw, CreatedAt: time.Now().UTC().Format(time.RFC3339)}, nil
}

// nullIntPtr adapts a NullInt64 to a pointer (the snapshot's created_by).
func nullIntPtr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	x := v.Int64
	return &x
}

package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// This file ports app/Services/WorkerService.php: the write path for
// background workers — long-running processes (queue workers, Node.js/Python
// apps) run by systemd as the website account (whacp-worker-<slug>.service).
// The read-only list used by the workers page lives in workers.go.

// WorkerFull is one complete worker row, hydrated like WorkerService::hydrate
// (env decoded, procs clamped to at least 1).
type WorkerFull struct {
	ID        int64
	Slug      string
	Name      string
	Directory string
	Command   string
	Env       map[string]string
	Restart   string
	MemoryMB  int
	Procs     int
	Runtime   string
	Enabled   bool
	CreatedAt string
	UpdatedAt string
}

// WorkerInput is the raw worker form data (WorkerService::validate input).
// Zero values mirror PHP's per-key defaults: Restart "" is "always", Procs 0
// is 1, Enabled nil is true.
type WorkerInput struct {
	Name      string
	Command   string
	Directory string
	Restart   string // "always" | "on-failure" | "no"
	MemoryMB  int
	Procs     *int // nil = the PHP ?? 1 default; explicit values are range-checked
	Runtime   string
	EnvText   string
	Enabled   *bool
}

// ProcsPtr wraps a procs value as an explicit (non-default) input.
func ProcsPtr(n int) *int { return &n }

// restartPolicies is WorkerService::RESTART.
var restartPolicies = map[string]bool{"always": true, "on-failure": true, "no": true}

// maxWorkerProcs is WorkerService::MAX_PROCS (matches the agent).
const maxWorkerProcs = 20

var (
	workerDirRe     = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	workerDirDotRe  = regexp.MustCompile(`(^|/)\.\.(/|$)`)
	workerRuntimeRe = regexp.MustCompile(`^(node|python):[0-9][0-9A-Za-z.]{0,31}$`)
	workerEnvKeyRe  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	workerCtrlRe    = regexp.MustCompile(`[\x00-\x08\x0a-\x1f\x7f]`)
	workerSlugRe    = regexp.MustCompile(`[^a-z0-9]+`)
)

// WorkerService writes workers for websites: validate, persist, apply on the
// server, roll back on agent failure, and audit. Mirrors WorkerService.php.
type WorkerService struct {
	db       *sql.DB
	agent    AgentCaller
	limits   *PlanLimits
	audit    Auditor
	serverID int64
}

// NewWorkerService builds the service. serverID is the active server
// (single-server in the Go panel).
func NewWorkerService(db *sql.DB, agent AgentCaller, limits *PlanLimits, audit Auditor, serverID int64) *WorkerService {
	return &WorkerService{db: db, agent: agent, limits: limits, audit: audit, serverID: serverID}
}

const workerFullSelect = `SELECT id, slug, name, directory, command, env, restart, memory_mb, procs, runtime, enabled, created_at, updated_at FROM workers`

// ForSite lists a website's workers in id order (WorkerService::forSite).
func (s *WorkerService) ForSite(ctx context.Context, websiteID int64) ([]WorkerFull, error) {
	rows, err := s.db.QueryContext(ctx, workerFullSelect+" WHERE website_id = ? ORDER BY id", websiteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkerFull{}
	for rows.Next() {
		w, err := scanWorkerFull(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Find returns one worker of a website (WorkerService::find); nil when absent.
func (s *WorkerService) Find(ctx context.Context, websiteID, id int64) (*WorkerFull, error) {
	w, err := scanWorkerFull(s.db.QueryRowContext(ctx, workerFullSelect+" WHERE id = ? AND website_id = ?", id, websiteID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func scanWorkerFull(row interface{ Scan(...any) error }) (WorkerFull, error) {
	var w WorkerFull
	var enabled int
	var env string
	if err := row.Scan(&w.ID, &w.Slug, &w.Name, &w.Directory, &w.Command, &env,
		&w.Restart, &w.MemoryMB, &w.Procs, &w.Runtime, &enabled, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return w, err
	}
	w.Enabled = enabled == 1
	w.Procs = max(1, w.Procs)
	w.Env = map[string]string{}
	if err := json.Unmarshal([]byte(env), &w.Env); err != nil || w.Env == nil {
		w.Env = map[string]string{}
	}
	return w, nil
}

// validateWorker mirrors WorkerService::validate field-for-field.
func validateWorker(in WorkerInput) (*WorkerFull, error) {
	ve := &ValidationError{}
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		ve.Add("name", "Enter a name (up to 100 characters).")
	}
	command := strings.TrimSpace(in.Command)
	if command == "" || len(command) > 2000 || workerCtrlRe.MatchString(command) {
		ve.Add("command", "Enter the command on a single line, e.g. php artisan queue:work --sleep=3.")
	}
	dir := strings.Trim(in.Directory, " /")
	if dir != "" && (!workerDirRe.MatchString(dir) || workerDirDotRe.MatchString(dir)) {
		ve.Add("directory", "Use a folder inside the website, e.g. app or public_html.")
	}
	restart := in.Restart
	if restart == "" {
		restart = "always"
	}
	if !restartPolicies[restart] {
		ve.Add("restart", "Choose a restart policy.")
	}
	mem := in.MemoryMB
	if mem < 0 || mem > 65536 {
		ve.Add("memory_mb", "Enter 0 (no limit) up to 65536 MB.")
	}
	// PHP: (int) ($input['procs'] ?? 1) — an absent field defaults to one
	// copy; an explicit value (including 0, e.g. a cleared form field) is
	// range-checked.
	procs := 1
	if in.Procs != nil {
		procs = *in.Procs
	}
	if procs < 1 || procs > maxWorkerProcs {
		ve.Add("procs", "Run 1 to "+strconv.Itoa(maxWorkerProcs)+" copies.")
	}
	runtime := strings.TrimSpace(in.Runtime)
	if runtime == "system" {
		runtime = ""
	}
	if runtime != "" && !workerRuntimeRe.MatchString(runtime) {
		ve.Add("runtime", "Choose a runtime.")
	}
	env := map[string]string{}
	for _, line := range strings.Split(in.EnvText, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || !workerEnvKeyRe.MatchString(k) {
			ve.Add("env", "Write one KEY=value per line; keys use capital letters, digits and _.")
			break
		}
		env[k] = v
	}
	if len(env) > 50 {
		ve.Add("env", "At most 50 variables.")
	}
	if ve.Any() {
		return nil, ve
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return &WorkerFull{Name: name, Directory: dir, Command: command, Restart: restart,
		MemoryMB: mem, Procs: procs, Runtime: runtime, Env: env, Enabled: enabled}, nil
}

// Save creates (id nil) or updates a worker and applies it on the server,
// rolling the row back when the agent apply fails (WorkerService::save).
func (s *WorkerService) Save(ctx context.Context, site *Website, id *int64, in WorkerInput) error {
	w, err := validateWorker(in)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	var previous *WorkerFull
	if id != nil {
		previous, err = s.Find(ctx, site.ID, *id)
		if err != nil {
			return err
		}
	}
	if id == nil && s.limits != nil && site.OwnerID.Valid {
		if err := s.limits.AssertCanCreate(ctx, "workers", &site.OwnerID.Int64, 1); err != nil {
			return err
		}
	}
	var workerID int64
	if id == nil {
		res, err := s.db.ExecContext(ctx,
			`INSERT INTO workers (server_id, website_id, slug, name, directory, command, env, restart, memory_mb, procs, runtime, enabled, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.serverID, site.ID, s.slugFor(ctx, site, w.Name), w.Name, w.Directory, w.Command,
			envJSON(w.Env), w.Restart, w.MemoryMB, w.Procs, w.Runtime, boolInt(w.Enabled), now, now)
		if err != nil {
			return err
		}
		workerID, err = res.LastInsertId()
		if err != nil {
			return err
		}
	} else {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE workers SET name = ?, directory = ?, command = ?, env = ?, restart = ?, memory_mb = ?, procs = ?, runtime = ?, enabled = ?, updated_at = ? WHERE id = ? AND website_id = ?`,
			w.Name, w.Directory, w.Command, envJSON(w.Env), w.Restart, w.MemoryMB, w.Procs, w.Runtime,
			boolInt(w.Enabled), now, *id, site.ID); err != nil {
			return err
		}
		workerID = *id
	}
	worker, err := s.Find(ctx, site.ID, workerID)
	if err != nil || worker == nil {
		return fmt.Errorf("worker: re-read after save: %w", err)
	}
	action := "worker.create"
	if previous != nil {
		action = "worker.update"
	}
	if err := s.Apply(ctx, site, *worker); err != nil {
		if previous == nil {
			_, _ = s.db.ExecContext(ctx, "DELETE FROM workers WHERE id = ?", workerID)
		} else {
			_, _ = s.db.ExecContext(ctx,
				`UPDATE workers SET name = ?, directory = ?, command = ?, env = ?, restart = ?, memory_mb = ?, procs = ?, runtime = ?, enabled = ?, updated_at = ? WHERE id = ?`,
				previous.Name, previous.Directory, previous.Command, envJSON(previous.Env), previous.Restart,
				previous.MemoryMB, previous.Procs, previous.Runtime, boolInt(previous.Enabled), previous.UpdatedAt, workerID)
		}
		s.recordAudit(ctx, action, "website", strconv.FormatInt(site.ID, 10), "failure", map[string]any{
			"domain": site.Domain, "worker": w.Name, "error": err.Error(),
		})
		return err
	}
	s.recordAudit(ctx, action, "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain": site.Domain, "worker": w.Name, "command": w.Command,
	})
	return nil
}

// recordAudit is the nil-safe audit hook (same contract as WebsiteService's).
func (s *WorkerService) recordAudit(ctx context.Context, action, targetType, target, result string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, targetType, target, result, details)
	}
}

// Apply pushes one worker to the server (WorkerService::apply). A disabled
// website or a suspended account applies the worker stopped, keeping the row.
func (s *WorkerService) Apply(ctx context.Context, site *Website, w WorkerFull) error {
	_, err := s.agent.Call(ctx, "workers.apply", map[string]any{
		"slug": w.Slug, "name": w.Name, "domain": site.Domain, "user": site.SystemUser,
		"root": site.Root, "directory": w.Directory, "command": w.Command, "env": w.Env,
		"restart": w.Restart, "memory_mb": w.MemoryMB, "procs": max(1, w.Procs),
		"runtime": w.Runtime, "enabled": w.Enabled && site.Enabled && !site.AccountSuspended,
	}, 120*time.Second)
	return err
}

// ChangeRuntime re-applies a worker on a different runtime, keeping everything
// else (WorkerService::changeRuntime). Env is re-serialized from the stored
// map; keys are sorted for determinism (PHP preserved insertion order, which
// has no Go-map equivalent).
func (s *WorkerService) ChangeRuntime(ctx context.Context, site *Website, w WorkerFull, runtime string) error {
	keys := make([]string, 0, len(w.Env))
	for k := range w.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+w.Env[k])
	}
	enabled := w.Enabled
	return s.Save(ctx, site, &w.ID, WorkerInput{
		Name: w.Name, Command: w.Command, Directory: w.Directory, Restart: w.Restart,
		MemoryMB: w.MemoryMB, Procs: ProcsPtr(w.Procs), Runtime: runtime, EnvText: strings.Join(lines, "\n"),
		Enabled: &enabled,
	})
}

// slugFor builds the unique worker unit slug (WorkerService::slug):
// <domain-first-label>-<name> with non-alnum runs collapsed to hyphens,
// lowercased, trimmed and capped at 50 chars; -2, -3, … on collision.
func (s *WorkerService) slugFor(ctx context.Context, site *Website, name string) string {
	raw := strings.SplitN(site.Domain, ".", 2)[0] + "-" + name
	base := workerSlugRe.ReplaceAllString(strings.ToLower(raw), "-")
	base = strings.Trim(base, "-")
	if len(base) > 50 {
		base = base[:50]
	}
	if base == "" {
		base = "worker"
	}
	slug := base
	for i := 2; ; i++ {
		var n int
		if err := s.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM workers WHERE server_id = ? AND slug = ?", s.serverID, slug).Scan(&n); err != nil || n == 0 {
			break
		}
		slug = base + "-" + strconv.Itoa(i)
	}
	return slug
}

// envJSON encodes the env map as a JSON object (PHP JSON_FORCE_OBJECT: an
// empty map stays an object, never an array).
func envJSON(env map[string]string) string {
	b, err := json.Marshal(env)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Unit is the worker's systemd unit name (WorkerService::hydrate).
func (w WorkerFull) Unit() string { return "whacp-worker-" + w.Slug + ".service" }

// WorkerStatus is one row of the workers.status payload: the aggregated
// state of one worker's copies.
type WorkerStatus struct {
	Unit         string
	ActiveState  string
	SubState     string
	ProcsTotal   int
	ProcsRunning int
	MemoryBytes  *int64
}

// Status reports the systemd state per unit (empty when the agent is
// unreachable), keyed by unit name (WorkerService::status).
func (s *WorkerService) Status(ctx context.Context, workers []WorkerFull) map[string]WorkerStatus {
	if len(workers) == 0 {
		return nil
	}
	slugs := make([]string, len(workers))
	for i, w := range workers {
		slugs[i] = w.Slug
	}
	raw, err := s.agent.Call(ctx, "workers.status", map[string]any{"slugs": slugs})
	if err != nil {
		return nil
	}
	var rows []struct {
		Unit         string `json:"unit"`
		ActiveState  string `json:"active_state"`
		SubState     string `json:"sub_state"`
		ProcsTotal   int    `json:"procs_total"`
		ProcsRunning int    `json:"procs_running"`
		MemoryBytes  *int64 `json:"memory_bytes"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil
	}
	out := map[string]WorkerStatus{}
	for _, r := range rows {
		if r.Unit == "" {
			continue
		}
		out[r.Unit] = WorkerStatus{Unit: r.Unit, ActiveState: r.ActiveState, SubState: r.SubState,
			ProcsTotal: r.ProcsTotal, ProcsRunning: r.ProcsRunning, MemoryBytes: r.MemoryBytes}
	}
	return out
}

// SetEnabled starts or stops a worker (WorkerService::setEnabled): the new
// state is applied on the server first, and the row follows only when the
// agent accepted it.
func (s *WorkerService) SetEnabled(ctx context.Context, site *Website, w WorkerFull, enabled bool) error {
	next := w
	next.Enabled = enabled
	if err := s.Apply(ctx, site, next); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, "UPDATE workers SET enabled = ?, updated_at = ? WHERE id = ?",
		boolInt(enabled), now, w.ID); err != nil {
		return err
	}
	action := "worker.stop"
	if enabled {
		action = "worker.start"
	}
	s.recordAudit(ctx, action, "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain": site.Domain, "worker": w.Name,
	})
	return nil
}

// Restart asks the agent to restart the worker's units (WorkerService::restart).
func (s *WorkerService) Restart(ctx context.Context, site *Website, w WorkerFull) error {
	if site.AccountSuspended {
		return &UserError{What: "The account is suspended; its workers are stopped."}
	}
	if _, err := s.agent.Call(ctx, "workers.restart", map[string]any{"slug": w.Slug}, 120*time.Second); err != nil {
		return err
	}
	s.recordAudit(ctx, "worker.restart", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain": site.Domain, "worker": w.Name,
	})
	return nil
}

// Delete removes the worker from the server and deletes its row
// (WorkerService::delete). An agent failure leaves the row in place.
func (s *WorkerService) Delete(ctx context.Context, site *Website, w WorkerFull) error {
	if _, err := s.agent.Call(ctx, "workers.remove", map[string]any{"slug": w.Slug}, 120*time.Second); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM workers WHERE id = ?", w.ID); err != nil {
		return err
	}
	s.recordAudit(ctx, "worker.delete", "website", strconv.FormatInt(site.ID, 10), "success", map[string]any{
		"domain": site.Domain, "worker": w.Name,
	})
	return nil
}

// RemoveAll removes every worker of a website from the server (called before
// the website is deleted, as the PHP website.deleting event does). Failures
// are swallowed: the deletion continues and leftover units are reported by
// discovery.
func (s *WorkerService) RemoveAll(ctx context.Context, site *Website) {
	workers, err := s.ForSite(ctx, site.ID)
	if err != nil {
		return
	}
	for i := range workers {
		_, _ = s.agent.Call(ctx, "workers.remove", map[string]any{"slug": workers[i].Slug}, 120*time.Second)
	}
}

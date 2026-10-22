package hosting

// DeployService — Git deployment (DeployService.php): a repository per
// website, cloned and updated by the agent as the website account with a
// per-site deploy key. Deployments are triggered from the panel or by a
// signed push webhook (GitHub/Gitea/Forgejo HMAC-SHA256, or a GitLab
// secret token); the same delivery is refused once it has started a
// deployment.

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
)

// deployKeyTimeout is the agent timeout for the deploy-key generation
// (DeployService::publicKey's 60 s).
const deployKeyTimeout = 60 * time.Second

// GitConfig is the website's git_repositories row (nil = not connected).
type GitConfig struct {
	WebsiteID        int64
	Repo             string
	Branch           string
	Path             string
	Hooks            []string
	AutoDeploy       bool
	WebhookToken     string
	WebhookSecretEnc string
	PublicKey        string
	CreatedAt        string
	UpdatedAt        string
}

// Deployment is one deployments row for the history (with the trigger
// user's name).
type Deployment struct {
	ID         int64
	WebsiteID  int64
	JobID      string
	Trigger    string
	Branch     string
	CommitHash string
	Message    string
	Status     string
	Error      string
	Username   string
	CreatedAt  string
}

// DeployNotif records a webhook warning/error notification (the
// NotificationService). The hosting package cannot import the notify
// package (it imports hosting), so the HTTP layer binds this.
type DeployNotif func(websiteID int64, kind, severity, title, body, link, dedupeKey string)

// DeployInput is the validated, normalised repository form
// (DeployService::validate's return).
type DeployInput struct {
	Repo       string
	Branch     string
	Path       string
	Hooks      []string
	AutoDeploy bool
}

// DeployService manages a website's Git deployment.
type DeployService struct {
	db       *sql.DB
	agent    AgentCaller
	audit    Auditor
	crypto   *crypto.Crypto
	notify   DeployNotif
	serverID int64
}

// NewDeployService builds the service.
func NewDeployService(db *sql.DB, ag AgentCaller, audit Auditor, cr *crypto.Crypto, notify DeployNotif, serverID int64) *DeployService {
	return &DeployService{db: db, agent: ag, audit: audit, crypto: cr, notify: notify, serverID: serverID}
}

func (s *DeployService) recordAudit(ctx context.Context, action string, websiteID int64, result string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, "website", fmt.Sprintf("%d", websiteID), result, details)
	}
}

// Config reads the website's repository config (nil = not connected).
func (s *DeployService) Config(ctx context.Context, websiteID int64) (*GitConfig, error) {
	var c GitConfig
	var hooks string
	var autoDeploy int
	err := s.db.QueryRowContext(ctx,
		`SELECT website_id, repo, branch, path, hooks, auto_deploy, webhook_token,
		        webhook_secret_enc, public_key, created_at, updated_at
		 FROM git_repositories WHERE website_id = ?`, websiteID).
		Scan(&c.WebsiteID, &c.Repo, &c.Branch, &c.Path, &hooks, &autoDeploy,
			&c.WebhookToken, &c.WebhookSecretEnc, &c.PublicKey, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.AutoDeploy = autoDeploy == 1
	c.Hooks = []string{}
	if err := json.Unmarshal([]byte(hooks), &c.Hooks); err != nil {
		c.Hooks = []string{}
	}
	return &c, nil
}

// WebhookSecret decrypts the stored webhook secret ("" when none).
func (s *DeployService) WebhookSecret(ctx context.Context, cfg *GitConfig) (string, error) {
	if cfg.WebhookSecretEnc == "" {
		return "", nil
	}
	plain, err := s.crypto.Decrypt(cfg.WebhookSecretEnc)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

var (
	deployRepoRe   = regexp.MustCompile(`^(https://[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._~/-]+|git@[A-Za-z0-9.-]+:[A-Za-z0-9._~/-]+|ssh://git@[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._~/-]+)$`)
	deployBranchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)
	deployPathRe   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	deployDotRe    = regexp.MustCompile(`(^|/)\.\.?(/|$)`)
	deployTokenRe  = regexp.MustCompile(`^[a-f0-9]{40}$`)
)

var deployReservedPaths = map[string]bool{"logs": true, "tmp": true, "backups": true, ".ssh": true}

// Validate checks the repository form input (DeployService::validate).
func (s *DeployService) Validate(input map[string]string) (*DeployInput, error) {
	ve := &ValidationError{}
	repo := strings.TrimSpace(input["repo"])
	if !deployRepoRe.MatchString(repo) || strings.Contains(repo, "..") {
		ve.Add("repo", "Use an https:// URL or an SSH address like git@github.com:owner/repo.git.")
	}
	branch := strings.TrimSpace(input["branch"])
	if branch == "" {
		branch = "main"
	}
	if !deployBranchRe.MatchString(branch) || strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") {
		ve.Add("branch", "Enter a valid branch name.")
	}
	path := strings.Trim(input["path"], " /")
	if path == "" || !deployPathRe.MatchString(path) || deployDotRe.MatchString(path) || deployReservedPaths[strings.Split(path, "/")[0]] {
		ve.Add("path", "Choose a folder inside the website (not logs, tmp, backups or .ssh), e.g. app.")
	}
	hooks := []string{}
	for _, line := range strings.Split(input["hooks"], "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			hooks = append(hooks, line)
		}
	}
	if len(hooks) > 20 {
		ve.Add("hooks", "At most 20 commands.")
	}
	for _, h := range hooks {
		if len(h) > 2000 {
			ve.Add("hooks", "Each command must be under 2000 characters.")
		}
	}
	if ve.Any() {
		return nil, ve
	}
	return &DeployInput{
		Repo:       repo,
		Branch:     branch,
		Path:       path,
		Hooks:      hooks,
		AutoDeploy: input["auto_deploy"] == "1",
	}, nil
}

func deployToken() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func deploySecret() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Configure creates or updates the repository config (DeployService::
// configure). A new config gets a fresh webhook token + secret.
func (s *DeployService) Configure(ctx context.Context, site *Website, input map[string]string) error {
	v, err := s.Validate(input)
	if err != nil {
		return err
	}
	hooks, err := json.Marshal(v.Hooks)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	autoDeploy := 0
	if v.AutoDeploy {
		autoDeploy = 1
	}
	existing, err := s.Config(ctx, site.ID)
	if err != nil {
		return err
	}
	if existing == nil {
		secret, err := s.crypto.Encrypt([]byte(deploySecret()))
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO git_repositories (website_id, repo, branch, path, hooks, auto_deploy, webhook_token, webhook_secret_enc, created_at, updated_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			site.ID, v.Repo, v.Branch, v.Path, string(hooks), autoDeploy, deployToken(), secret, now, now); err != nil {
			return err
		}
	} else {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE git_repositories SET repo = ?, branch = ?, path = ?, hooks = ?, auto_deploy = ?, updated_at = ? WHERE website_id = ?`,
			v.Repo, v.Branch, v.Path, string(hooks), autoDeploy, now, site.ID); err != nil {
			return err
		}
	}
	s.recordAudit(ctx, "deploy.configure", site.ID, "success", map[string]any{
		"domain": site.Domain, "repo": v.Repo, "branch": v.Branch, "hooks": len(v.Hooks),
	})
	return nil
}

// RotateWebhook generates a new webhook URL + secret (DeployService::
// rotateWebhook). Deliveries signed with the old secret are refused
// anyway; the replay table is cleared.
func (s *DeployService) RotateWebhook(ctx context.Context, site *Website) error {
	secret, err := s.crypto.Encrypt([]byte(deploySecret()))
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE git_repositories SET webhook_token = ?, webhook_secret_enc = ? WHERE website_id = ?",
		deployToken(), secret, site.ID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM webhook_deliveries WHERE website_id = ?", site.ID); err != nil {
		return err
	}
	s.recordAudit(ctx, "deploy.webhook_rotate", site.ID, "success", nil)
	return nil
}

// Remove disconnects the repository; the deployed files stay in place
// (DeployService::remove).
func (s *DeployService) Remove(ctx context.Context, site *Website) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM git_repositories WHERE website_id = ?", site.ID); err != nil {
		return err
	}
	s.recordAudit(ctx, "deploy.remove", site.ID, "success", map[string]any{"domain": site.Domain, "note": "files were left in place"})
	return nil
}

// PublicKey creates (if needed) and returns the site's public deploy key
// (DeployService::publicKey).
func (s *DeployService) PublicKey(ctx context.Context, site *Website) (string, error) {
	res, err := s.agent.Call(ctx, "deploy.key", map[string]any{
		"domain": site.Domain,
		"user":   site.SystemUser,
		"root":   site.Root,
	}, deployKeyTimeout)
	if err != nil {
		return "", err
	}
	var out struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx,
		"UPDATE git_repositories SET public_key = ? WHERE website_id = ?", out.PublicKey, site.ID); err != nil {
		return "", err
	}
	return out.PublicKey, nil
}

// Deploy starts a deployment; returns the agent job id (DeployService::
// deploy).
func (s *DeployService) Deploy(ctx context.Context, site *Website, trigger string, userID *int64) (string, error) {
	cfg, err := s.Config(ctx, site.ID)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		return "", &UserError{What: "Connect a Git repository first."}
	}
	// A deployment running within the last hour: refresh it first so the
	// state is up to date before another one starts.
	var running int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM deployments WHERE website_id = ? AND status = 'running' AND created_at > ?",
		site.ID, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)).Scan(&running); err != nil {
		return "", err
	}
	if running > 0 {
		_ = s.Refresh(ctx, site)
	}
	res, err := s.agent.Call(ctx, "deploy.run", map[string]any{
		"domain": site.Domain,
		"user":   site.SystemUser,
		"root":   site.Root,
		"repo":   cfg.Repo,
		"branch": cfg.Branch,
		"path":   cfg.Path,
		"hooks":  cfg.Hooks,
	})
	if err != nil {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO deployments (website_id, "trigger", branch, status, error, user_id, created_at, finished_at)
			 VALUES (?,?,?,?,?,?,?,?)`,
			site.ID, trigger, cfg.Branch, "failed", agentMessage(err), userID,
			time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339)); err != nil {
			return "", err
		}
		return "", err
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO deployments (website_id, job_id, "trigger", branch, status, user_id, created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		site.ID, out.JobID, trigger, cfg.Branch, "running", userID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return "", err
	}
	s.recordAudit(ctx, "deploy.run", site.ID, "success", map[string]any{
		"domain": site.Domain, "trigger": trigger, "branch": cfg.Branch, "job": out.JobID,
	})
	return out.JobID, nil
}

// Refresh updates the website's running deployments from their agent jobs
// (DeployService::refresh). A job the agent no longer knows of becomes
// "unknown".
func (s *DeployService) Refresh(ctx context.Context, site *Website) error {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, job_id FROM deployments WHERE website_id = ? AND status = 'running'", site.ID)
	if err != nil {
		return err
	}
	type running struct {
		id    int64
		jobID string
	}
	var pending []running
	for rows.Next() {
		var r running
		if err := rows.Scan(&r.id, &r.jobID); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range pending {
		res, err := s.agent.Call(ctx, "jobs.get", map[string]any{"id": d.jobID})
		if err != nil {
			var ae *agent.Error
			if errors.As(err, &ae) && ae.Code == "not_found" {
				if _, err := s.db.ExecContext(ctx,
					"UPDATE deployments SET status = 'unknown', finished_at = ? WHERE id = ?",
					time.Now().UTC().Format(time.RFC3339), d.id); err != nil {
					return err
				}
			}
			continue
		}
		var job struct {
			Status     string          `json:"status"`
			Error      string          `json:"error"`
			Result     json.RawMessage `json:"result"`
			FinishedAt *string         `json:"finished_at"`
		}
		if err := json.Unmarshal(res, &job); err != nil {
			continue
		}
		if job.Status != "succeeded" && job.Status != "failed" {
			continue
		}
		var result struct {
			Commit  string `json:"commit"`
			Message string `json:"message"`
		}
		if len(job.Result) > 0 {
			_ = json.Unmarshal(job.Result, &result)
		}
		finished := time.Now().UTC().Format(time.RFC3339)
		if job.FinishedAt != nil {
			finished = *job.FinishedAt
		}
		if rs := []rune(result.Message); len(rs) > 300 {
			result.Message = string(rs[:300])
		}
		if _, err := s.db.ExecContext(ctx,
			"UPDATE deployments SET status = ?, commit_hash = ?, message = ?, error = ?, finished_at = ? WHERE id = ?",
			job.Status, result.Commit, result.Message, job.Error, finished, d.id); err != nil {
			return err
		}
	}
	return nil
}

// History is the website's deployments, newest first (DeployService::
// history).
func (s *DeployService) History(ctx context.Context, websiteID int64, limit int) ([]Deployment, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.id, d.website_id, d.job_id, d."trigger", d.branch, d.commit_hash, d.message,
		        d.status, d.error, u.username, d.created_at
		 FROM deployments d LEFT JOIN users u ON u.id = d.user_id
		 WHERE d.website_id = ? ORDER BY d.id DESC LIMIT ?`, websiteID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		var d Deployment
		var username sql.NullString
		if err := rows.Scan(&d.ID, &d.WebsiteID, &d.JobID, &d.Trigger, &d.Branch, &d.CommitHash,
			&d.Message, &d.Status, &d.Error, &username, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Username = username.String
		out = append(out, d)
	}
	return out, rows.Err()
}

// webhookRow is the webhook lookup (the repository config + its server).
type webhookRow struct {
	WebsiteID  int64
	ServerID   int64
	Branch     string
	AutoDeploy int
	SecretEnc  string
}

// Webhook handles a signed push delivery (DeployService::webhook) and
// returns the HTTP status + message for the JSON reply.
func (s *DeployService) Webhook(ctx context.Context, token, githubSignature, gitlabToken, githubEvent string, body []byte) (int, string, error) {
	if !deployTokenRe.MatchString(token) {
		return 404, "Unknown webhook.", nil
	}
	var row webhookRow
	err := s.db.QueryRowContext(ctx,
		`SELECT g.website_id, w.server_id, g.branch, g.auto_deploy, g.webhook_secret_enc
		 FROM git_repositories g JOIN websites w ON w.id = g.website_id
		 WHERE g.webhook_token = ?`, token).
		Scan(&row.WebsiteID, &row.ServerID, &row.Branch, &row.AutoDeploy, &row.SecretEnc)
	if errors.Is(err, sql.ErrNoRows) {
		return 404, "Unknown webhook.", nil
	}
	if err != nil {
		return 500, "Internal error.", err
	}
	secret, err := s.crypto.Decrypt(row.SecretEnc)
	if err != nil {
		return 500, "Internal error.", err
	}
	valid := (githubSignature != "" && hmacEqual("sha256="+hmacSHA256(body, secret), githubSignature)) ||
		(gitlabToken != "" && hmacEqual(string(secret), gitlabToken))
	if !valid {
		s.recordAudit(ctx, "deploy.webhook", row.WebsiteID, "denied", map[string]any{"reason": "bad signature"})
		if s.notify != nil {
			// At most one notification an hour: every refused request is in
			// the audit log.
			s.notify(row.WebsiteID, "webhook", "warning", "Deploy webhook refused: invalid signature",
				"A request to the deploy webhook had a missing or wrong signature and was refused. If your Git host shows failed deliveries, copy the webhook secret again; if not, someone may be probing the URL (rotate it).",
				fmt.Sprintf("/sites/%d/git", row.WebsiteID),
				fmt.Sprintf("webhook-signature:%d:%s", row.WebsiteID, time.Now().UTC().Format("2006010215")))
		}
		return 401, "Invalid signature.", nil
	}
	if githubEvent == "ping" {
		return 200, "pong", nil
	}
	var payload struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		payload = struct {
			Ref string `json:"ref"`
		}{}
	}
	if payload.Ref != "" && payload.Ref != "refs/heads/"+row.Branch {
		return 202, "Ignored: push to " + payload.Ref + ", deploying " + row.Branch + ".", nil
	}
	if row.AutoDeploy != 1 {
		return 202, "Ignored: automatic deployment is turned off.", nil
	}
	site, err := NewWebsiteRepository(s.db).Find(ctx, row.ServerID, row.WebsiteID, nil)
	if err != nil {
		return 500, "Internal error.", err
	}
	if site == nil || row.ServerID != s.serverID {
		return 404, "Unknown webhook.", nil
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(body))
	res, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO webhook_deliveries (website_id, body_sha256, received_at) VALUES (?,?,?)",
		row.WebsiteID, hash, time.Now().UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return 500, "Internal error.", err
	}
	newDelivery, _ := res.RowsAffected()
	if newDelivery == 0 {
		s.recordAudit(ctx, "deploy.webhook", row.WebsiteID, "denied", map[string]any{"reason": "replayed delivery"})
		if s.notify != nil {
			s.notify(row.WebsiteID, "webhook", "warning", "Deploy webhook refused: delivery already processed",
				"A delivery that had already started a deployment was sent again and refused. A Git host redelivering it is harmless; otherwise someone may have captured a request (rotate the webhook).",
				fmt.Sprintf("/sites/%d/git", row.WebsiteID),
				fmt.Sprintf("webhook-replay:%d:%s", row.WebsiteID, hash))
		}
		return 409, "Ignored: this delivery was already processed. Deploy from the panel to deploy again.", nil
	}
	job, err := s.Deploy(ctx, site, "webhook", nil)
	if err != nil {
		var ue *UserError
		if errors.As(err, &ue) {
			// Nothing was deployed: the Git host may deliver it again.
			if _, err := s.db.ExecContext(ctx,
				"DELETE FROM webhook_deliveries WHERE website_id = ? AND body_sha256 = ?", row.WebsiteID, hash); err != nil {
				return 500, "Internal error.", err
			}
			if s.notify != nil {
				s.notify(row.WebsiteID, "job", "error", "Deploy "+site.Domain+" could not start",
					ue.What, fmt.Sprintf("/sites/%d/git", row.WebsiteID),
					fmt.Sprintf("webhook-start:%d:%s", row.WebsiteID, hash))
			}
			return 502, "Deployment could not start: " + ue.What, nil
		}
		return 500, "Internal error.", err
	}
	// Followed like deployments started in the panel, so its result is
	// reported.
	if err := NewJobTracker(s.db).Track(ctx, s.serverID, job, "Deploy "+site.Domain, "website", fmt.Sprintf("%d", row.WebsiteID), nil); err != nil {
		return 500, "Internal error.", err
	}
	return 202, "Deployment started (job " + job + ").", nil
}

func hmacSHA256(body, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func hmacEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

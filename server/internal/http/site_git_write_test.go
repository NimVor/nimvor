package http

// This suite drives the per-website Git deployment area (DeployController +
// DeployService): the page (connect form, summary, deploy key, webhook
// URL/secret, history with the agent refresh), configuring/updating the
// repository (validation re-renders the form), deploying now (agent job +
// tracker), rotating the webhook (fresh token, replay table cleared),
// disconnecting the repository, the role split, and the public signed
// push webhook (token/signature gates, ping, ref + auto_deploy filters,
// replay protection, the GitLab token, the agent failure path). The agent
// is the recording fake (deploy.* fixtures in startSitesAgent).

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

const (
	gitTestToken  = "0123456789abcdef0123456789abcdef01234567"
	gitTestSecret = "s3cr3t-webhook-secret-for-tests-12345678"
	gitTestJob    = "0123456789d0a001"
)

// gitWEnv seeds an administrator, a customer (bob, deploy.manage, owns
// websites 10 and 30) and a read-only viewer (carol, owns website 20).
func gitWEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'admin','Administrator','all',''),(2,'customer','Customer','own',''),(5,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'deploy.manage','dm','deploy')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(2,1),(2,3),(5,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','',''),
		(20,1,3,'carol.example','u_carol','/home/whacp/carol.example','/home/whacp/carol.example/public_html','php','8.3.1','active','',''),
		(30,1,2,'plain.example','u_plain','/home/whacp/plain.example','/home/whacp/plain.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1),(1,20,'carol.example',1),(1,30,'plain.example',1)`)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sa, socket, tokenFile := startSitesAgent(t, false)
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), sa, db
}

// gitSeedConfig inserts a git_repositories row for website 10 with the
// fixed webhook token + encrypted test secret.
func gitSeedConfig(t *testing.T, db *sql.DB, autoDeploy int) {
	t.Helper()
	enc, err := crypto.NewWithKey([32]byte{}).Encrypt([]byte(gitTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO git_repositories (website_id, repo, branch, path, hooks, auto_deploy, webhook_token, webhook_secret_enc, public_key, created_at, updated_at)
		 VALUES (10, 'https://github.com/bob/site.git', 'main', 'app', '["composer install --no-dev"]', ?, ?, ?, '', '', '')`,
		autoDeploy, gitTestToken, enc); err != nil {
		t.Fatal(err)
	}
}

// gitPushBody is a GitHub push payload.
func gitPushBody(ref string) string {
	return `{"ref":"` + ref + `","after":"abcdef1234567890"}`
}

func gitHMAC(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// gitWebhook posts a raw push delivery to the public webhook (no session).
func gitWebhook(t *testing.T, h http.Handler, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/hooks/deploy/"+token, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func gitWebhookMessage(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("webhook reply is not JSON: %q", rr.Body.String())
	}
	return out.Message
}

func TestGitPageConnect(t *testing.T) {
	h, _, _ := gitWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/git", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/git = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	if !pageHas(page, "Connect a repository") {
		t.Error("unconnected page missing the connect heading")
	}
	if pageHas(page, "id=\"deploy-summary\"") {
		t.Error("unconnected page must not show the deployment summary")
	}
	if !pageHas(page, "Deploy key") {
		// The key/webhook cards are hidden until connected.
		t.Log("note: deploy key card hidden (expected when unconnected)")
	}
}

func TestGitPageConnected(t *testing.T) {
	h, sa, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/git", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/git = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"<title>Git · bob.example · WhaCP</title>",
		"https://github.com/bob/site.git",
		"deploys automatically",
		"Deploy now",
		"/sites/10/git/deploy",
		"/sites/10/git/rotate",
		"/sites/10/git/remove",
		"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDeployKeyForTests",
		"/hooks/deploy/" + gitTestToken,
		gitTestSecret,
		"No deployments yet.",
		"composer install --no-dev",
	} {
		if !pageHas(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The show request filled the deploy key in the DB.
	var key string
	if err := db.QueryRow(`SELECT public_key FROM git_repositories WHERE website_id = 10`).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key == "" {
		t.Error("public_key was not filled by the show request")
	}
	// The agent key generation used the site's account.
	if got := agentParam(t, sa, "deploy.key", "domain"); got != "bob.example" {
		t.Errorf("deploy.key domain = %v", got)
	}
	if got := agentParam(t, sa, "deploy.key", "user"); got != "u_bob" {
		t.Errorf("deploy.key user = %v", got)
	}
}

func TestGitPageKeyError(t *testing.T) {
	h, sa, db := gitWEnv(t)
	gitSeedConfig(t, db, 0)
	sa.failActions = map[string]string{"deploy.key": "ssh-keygen is not installed"}
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/git", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/git = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "The deploy key could not be created: ssh-keygen is not installed") {
		t.Error("page missing the key error")
	}
	if pageHas(rr.Body.String(), "manual only") == false {
		t.Error("auto_deploy=0 should show 'manual only'")
	}
}

func TestGitPageReadOnly(t *testing.T) {
	h, _, _ := gitWEnv(t)
	carol := loginAsUser(t, h, "carol")
	rr := get(t, h, "/sites/20/git", carol)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer GET /sites/20/git = %d (want 403)", rr.Code)
	}
}

func TestGitConfigure(t *testing.T) {
	h, sa, db := gitWEnv(t)
	bob := loginAsUser(t, h, "bob")
	form := url.Values{
		"repo":        {"git@github.com:owner/repo.git"},
		"branch":      {"release/1.0"},
		"path":        {"web/app"},
		"hooks":       {"composer install --no-dev\nphp artisan migrate --force"},
		"auto_deploy": {"1"},
	}
	rr := postGitForm(t, h, bob, "/sites/10/git", form)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d: %s", rr.Code, rr.Body.String())
	}
	// Configuring must not call the agent (the page fetch above saw no
	// config yet, so no agent action was expected before this point).
	sa.mu.Lock()
	for _, a := range sa.actions {
		if strings.HasPrefix(a, "deploy.") {
			t.Errorf("unexpected agent action %q", a)
		}
	}
	sa.mu.Unlock()
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/git", "Repository settings saved. Add the deploy key to your Git host if the repository is private, then deploy.")

	var (
		repo, branch, path, hooks, token string
		auto                             int
		secretEnc                        string
	)
	if err := db.QueryRow(
		`SELECT repo, branch, path, hooks, webhook_token, auto_deploy, webhook_secret_enc FROM git_repositories WHERE website_id = 10`,
	).Scan(&repo, &branch, &path, &hooks, &token, &auto, &secretEnc); err != nil {
		t.Fatal(err)
	}
	if repo != "git@github.com:owner/repo.git" || branch != "release/1.0" || path != "web/app" {
		t.Errorf("stored config = %q %q %q", repo, branch, path)
	}
	var hookList []string
	if err := json.Unmarshal([]byte(hooks), &hookList); err != nil || len(hookList) != 2 || hookList[0] != "composer install --no-dev" {
		t.Errorf("stored hooks = %q", hooks)
	}
	if auto != 1 {
		t.Error("auto_deploy not stored")
	}
	if len(token) != 40 || secretEnc == "" {
		t.Errorf("webhook token/secret not generated (token %q, secret %q)", token, secretEnc)
	}

	// Updating keeps the token.
	form2 := url.Values{
		"repo":        {"git@github.com:owner/repo.git"},
		"branch":      {"main"},
		"path":        {"app"},
		"hooks":       {""},
		"auto_deploy": {""},
	}
	rr = postGitForm(t, h, bob, "/sites/10/git", form2)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update = %d: %s", rr.Code, rr.Body.String())
	}
	bob = refreshCookie(bob, rr)
	var token2 string
	if err := db.QueryRow(`SELECT webhook_token FROM git_repositories WHERE website_id = 10`).Scan(&token2); err != nil {
		t.Fatal(err)
	}
	if token2 != token {
		t.Error("updating the config must keep the webhook token")
	}
}

func TestGitConfigureValidation(t *testing.T) {
	h, _, db := gitWEnv(t)
	bob := loginAsUser(t, h, "bob")
	hooks := strings.Repeat("echo one\n", 21)
	form := url.Values{
		"repo":        {"http://bad..repo"},
		"branch":      {"--weird"},
		"path":        {"logs"},
		"hooks":       {hooks},
		"auto_deploy": {"1"},
	}
	rr := postGitForm(t, h, bob, "/sites/10/git", form)
	if rr.Code != http.StatusOK {
		t.Fatalf("rejected form = %d (want 200 re-render)", rr.Code)
	}
	page := rr.Body.String()
	for _, want := range []string{
		"Use an https:// URL or an SSH address like git@github.com:owner/repo.git.",
		"Enter a valid branch name.",
		"Choose a folder inside the website (not logs, tmp, backups or .ssh), e.g. app.",
		"At most 20 commands.",
		`value="http://bad..repo"`,
	} {
		if !pageHas(page, want) {
			t.Errorf("re-render missing %q", want)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM git_repositories WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("a rejected form must not store a config")
	}
}

func TestGitDeploy(t *testing.T) {
	h, sa, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	// The agent still knows the job it just started: the page's refresh
	// keeps the deployment "running".
	sa.jobs = map[string]string{
		gitTestJob: `{"success":true,"data":{"id":"0123456789d0a001","kind":"deploy","target":"website:10","status":"running","created_at":"2026-10-23T10:00:00Z"}}`,
	}
	bob := loginAsUser(t, h, "bob")
	rr := postGitForm(t, h, bob, "/sites/10/git/deploy", url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("deploy = %d: %s", rr.Code, rr.Body.String())
	}
	bob = refreshCookie(bob, rr)
	rr = get(t, h, "/sites/10/git", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET after deploy = %d", rr.Code)
	}
	page := rr.Body.String()
	for _, want := range []string{
		"Deployment started.",
		"Job " + gitTestJob + " started in the background",
		"running",
	} {
		if !pageHas(page, want) {
			t.Errorf("page after deploy missing %q", want)
		}
	}
	var (
		jobID, trigger, status string
		userID                 sql.NullInt64
	)
	if err := db.QueryRow(
		`SELECT job_id, "trigger", status, user_id FROM deployments WHERE website_id = 10`,
	).Scan(&jobID, &trigger, &status, &userID); err != nil {
		t.Fatal(err)
	}
	if jobID != gitTestJob || trigger != "manual" || status != "running" {
		t.Errorf("deployment row = %q %q %q", jobID, trigger, status)
	}
	if !userID.Valid || userID.Int64 != 2 {
		t.Errorf("deployment user = %v", userID)
	}
	if got := agentParam(t, sa, "deploy.run", "repo"); got != "https://github.com/bob/site.git" {
		t.Errorf("deploy.run repo = %v", got)
	}
	if got := agentParam(t, sa, "deploy.run", "branch"); got != "main" {
		t.Errorf("deploy.run branch = %v", got)
	}
	if got := agentParam(t, sa, "deploy.run", "path"); got != "app" {
		t.Errorf("deploy.run path = %v", got)
	}
	if got := agentParam(t, sa, "deploy.run", "user"); got != "u_bob" {
		t.Errorf("deploy.run user = %v", got)
	}
	// The panel-started deployment is tracked.
	var tracked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_jobs WHERE job_id = ?`, gitTestJob).Scan(&tracked); err != nil {
		t.Fatal(err)
	}
	if tracked != 1 {
		t.Error("deployment job was not tracked")
	}
}

func TestGitPostRequiresCSRF(t *testing.T) {
	h, _, _ := gitWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := postForm(h, "/sites/10/git/deploy", url.Values{}, bob)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("deploy without CSRF token = %d (want 400)", rr.Code)
	}
}

func TestGitDeployNoConfig(t *testing.T) {
	h, _, _ := gitWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := postGitForm(t, h, bob, "/sites/30/git/deploy", url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("deploy without config = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/30/git", "Connect a Git repository first.")
}

func TestGitRotate(t *testing.T) {
	h, _, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	if _, err := db.Exec(`INSERT INTO webhook_deliveries (website_id, body_sha256, received_at) VALUES (10, 'aaaa', '')`); err != nil {
		t.Fatal(err)
	}
	bob := loginAsUser(t, h, "bob")
	rr := postGitForm(t, h, bob, "/sites/10/git/rotate", url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("rotate = %d: %s", rr.Code, rr.Body.String())
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/git", "New webhook URL and secret generated. Update them on your Git host.")
	var token string
	if err := db.QueryRow(`SELECT webhook_token FROM git_repositories WHERE website_id = 10`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	if token == gitTestToken || len(token) != 40 {
		t.Errorf("rotate did not change the token (now %q)", token)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("rotate must clear the replay table")
	}
}

func TestGitRemove(t *testing.T) {
	h, _, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	bob := loginAsUser(t, h, "bob")
	rr := postGitForm(t, h, bob, "/sites/10/git/remove", url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("remove = %d: %s", rr.Code, rr.Body.String())
	}
	bob = refreshCookie(bob, rr)
	rr = get(t, h, "/sites/10/git", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET after remove = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "Repository disconnected. The deployed files were left in place.") {
		t.Error("missing the remove flash")
	}
	if !pageHas(rr.Body.String(), "Connect a repository") {
		t.Error("page after remove must show the connect form")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM git_repositories WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("remove did not delete the config")
	}
}

func TestGitHistoryRefresh(t *testing.T) {
	h, sa, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	done := "2026-10-20T10:00:00Z"
	sa.jobs = map[string]string{
		"0123456789aaaa0001": `{"success":true,"data":{"id":"0123456789aaaa0001","kind":"deploy","target":"website:10","status":"succeeded","error":"","result":{"commit":"abcdef1234567890","message":"Ship it","cloned":true},"created_at":"2026-10-20T09:59:00Z","finished_at":"` + done + `"}}`,
	}
	if _, err := db.Exec(
		`INSERT INTO deployments (website_id, job_id, "trigger", branch, status, user_id, created_at)
		 VALUES (10, '0123456789aaaa0001', 'manual', 'main', 'running', 2, '2026-10-20T09:59:00Z'),
		        (10, 'ffffffffffffffff', 'webhook', 'main', 'running', NULL, '2026-10-20T09:58:00Z')`); err != nil {
		t.Fatal(err)
	}
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/git", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/git = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"abcdef1234",
		"Ship it",
		"succeeded",
		"unknown",
	} {
		if !pageHas(page, want) {
			t.Errorf("history page missing %q", want)
		}
	}
	var (
		status, commit, message, finished string
	)
	if err := db.QueryRow(
		`SELECT status, commit_hash, message, COALESCE(finished_at,'') FROM deployments WHERE job_id = '0123456789aaaa0001'`,
	).Scan(&status, &commit, &message, &finished); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || commit != "abcdef1234567890" || message != "Ship it" || finished != done {
		t.Errorf("refreshed row = %q %q %q %q", status, commit, message, finished)
	}
	var unknown string
	if err := db.QueryRow(`SELECT status FROM deployments WHERE job_id = 'ffffffffffffffff'`).Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	if unknown != "unknown" {
		t.Errorf("agent-unknown job = %q (want unknown)", unknown)
	}
}

func TestWebhookUnknownToken(t *testing.T) {
	h, _, _ := gitWEnv(t)
	rr := gitWebhook(t, h, "not-a-token", gitPushBody("refs/heads/main"), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown token = %d", rr.Code)
	}
	if msg := gitWebhookMessage(t, rr); msg != "Unknown webhook." {
		t.Errorf("message = %q", msg)
	}
	rr = gitWebhook(t, h, strings.Repeat("0", 41), gitPushBody("refs/heads/main"), nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("41-hex token = %d (want 404)", rr.Code)
	}
}

func TestWebhookBadSignature(t *testing.T) {
	h, _, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	rr := gitWebhook(t, h, gitTestToken, gitPushBody("refs/heads/main"),
		map[string]string{"X-Hub-Signature-256": "sha256=deadbeef"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature = %d: %s", rr.Code, rr.Body.String())
	}
	if msg := gitWebhookMessage(t, rr); msg != "Invalid signature." {
		t.Errorf("message = %q", msg)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE website_id = 10 AND dedupe_key LIKE 'webhook-signature:10:%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("bad signature must notify once")
	}
}

func TestWebhookPing(t *testing.T) {
	h, _, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	rr := gitWebhook(t, h, gitTestToken, `{"zen":"keep it logically awesome"}`,
		map[string]string{
			"X-Hub-Signature-256": gitHMAC(`{"zen":"keep it logically awesome"}`, gitTestSecret),
			"X-Github-Event":      "ping",
		})
	if rr.Code != http.StatusOK {
		t.Fatalf("ping = %d", rr.Code)
	}
	if msg := gitWebhookMessage(t, rr); msg != "pong" {
		t.Errorf("message = %q", msg)
	}
}

func TestWebhookRefMismatch(t *testing.T) {
	h, _, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	body := gitPushBody("refs/heads/other")
	rr := gitWebhook(t, h, gitTestToken, body,
		map[string]string{"X-Hub-Signature-256": gitHMAC(body, gitTestSecret)})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("ref mismatch = %d", rr.Code)
	}
	if msg := gitWebhookMessage(t, rr); msg != "Ignored: push to refs/heads/other, deploying main." {
		t.Errorf("message = %q", msg)
	}
}

func TestWebhookAutoOff(t *testing.T) {
	h, _, db := gitWEnv(t)
	gitSeedConfig(t, db, 0)
	body := gitPushBody("refs/heads/main")
	rr := gitWebhook(t, h, gitTestToken, body,
		map[string]string{"X-Hub-Signature-256": gitHMAC(body, gitTestSecret)})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("auto off = %d", rr.Code)
	}
	if msg := gitWebhookMessage(t, rr); msg != "Ignored: automatic deployment is turned off." {
		t.Errorf("message = %q", msg)
	}
}

func TestWebhookSuccess(t *testing.T) {
	h, sa, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	body := gitPushBody("refs/heads/main")
	rr := gitWebhook(t, h, gitTestToken, body,
		map[string]string{"X-Hub-Signature-256": gitHMAC(body, gitTestSecret)})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("webhook deploy = %d: %s", rr.Code, rr.Body.String())
	}
	if msg := gitWebhookMessage(t, rr); msg != "Deployment started (job "+gitTestJob+")." {
		t.Errorf("message = %q", msg)
	}
	var (
		trigger, status string
		userID          sql.NullInt64
	)
	if err := db.QueryRow(`SELECT "trigger", status, user_id FROM deployments WHERE website_id = 10`).
		Scan(&trigger, &status, &userID); err != nil {
		t.Fatal(err)
	}
	if trigger != "webhook" || status != "running" || userID.Valid {
		t.Errorf("webhook deployment = %q %q user=%v", trigger, status, userID)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("delivery was not recorded")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_jobs WHERE job_id = ?`, gitTestJob).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("webhook deployment was not tracked")
	}
	if got := agentParam(t, sa, "deploy.run", "repo"); got != "https://github.com/bob/site.git" {
		t.Errorf("deploy.run repo = %v", got)
	}
}

func TestWebhookReplay(t *testing.T) {
	h, _, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	body := gitPushBody("refs/heads/main")
	hdrs := map[string]string{"X-Hub-Signature-256": gitHMAC(body, gitTestSecret)}
	rr := gitWebhook(t, h, gitTestToken, body, hdrs)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("first delivery = %d", rr.Code)
	}
	rr = gitWebhook(t, h, gitTestToken, body, hdrs)
	if rr.Code != http.StatusConflict {
		t.Fatalf("replay = %d: %s", rr.Code, rr.Body.String())
	}
	if msg := gitWebhookMessage(t, rr); msg != "Ignored: this delivery was already processed. Deploy from the panel to deploy again." {
		t.Errorf("replay message = %q", msg)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("replay must not start a second deployment (rows: %d)", n)
	}
}

func TestWebhookGitLab(t *testing.T) {
	h, _, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	body := gitPushBody("refs/heads/main")
	rr := gitWebhook(t, h, gitTestToken, body, map[string]string{"X-Gitlab-Token": gitTestSecret})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("gitlab token = %d: %s", rr.Code, rr.Body.String())
	}
	if msg := gitWebhookMessage(t, rr); msg != "Deployment started (job "+gitTestJob+")." {
		t.Errorf("message = %q", msg)
	}
	// A wrong GitLab token is refused like a bad signature.
	body2 := gitPushBody("refs/heads/main") + " "
	rr = gitWebhook(t, h, gitTestToken, body2, map[string]string{"X-Gitlab-Token": "wrong"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("wrong gitlab token = %d", rr.Code)
	}
}

func TestWebhookAgentFailure(t *testing.T) {
	h, sa, db := gitWEnv(t)
	gitSeedConfig(t, db, 1)
	sa.failActions = map[string]string{"deploy.run": "git binary not found"}
	body := gitPushBody("refs/heads/main")
	rr := gitWebhook(t, h, gitTestToken, body,
		map[string]string{"X-Hub-Signature-256": gitHMAC(body, gitTestSecret)})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("agent failure = %d: %s", rr.Code, rr.Body.String())
	}
	var status, errMsg string
	if err := db.QueryRow(`SELECT status, error FROM deployments WHERE website_id = 10`).Scan(&status, &errMsg); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || errMsg != "git binary not found" {
		t.Errorf("failed row = %q %q", status, errMsg)
	}
	// The delivery stays recorded: only user-facing failures (nothing was
	// deployed and it is safe to redeliver) are rolled back.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM webhook_deliveries WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("agent-failure delivery must stay recorded")
	}
}

// postGitForm fetches the site's git page for a fresh CSRF token, then POSTs
// the form to path.
func postGitForm(t *testing.T, h http.Handler, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	id := "10"
	if i := strings.Index(path, "/sites/"); i >= 0 {
		rest := path[i+len("/sites/"):]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			id = rest[:j]
		} else {
			id = rest
		}
	}
	token, cookie := siteFormToken(t, h, cookie, "/sites/"+id+"/git")
	form.Set("_token", token)
	return postForm(h, path, form, cookie)
}

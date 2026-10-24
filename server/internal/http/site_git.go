package http

// This file ports the per-website Git deployment area (DeployController +
// DeployService): the repository settings page (config form, deploy key,
// webhook URL/secret, deployment history), deploy now, webhook rotation,
// disconnecting the repository, and the public signed push webhook.
// Routes:
//   GET  /sites/{id}/git            (deploy.manage)
//   POST /sites/{id}/git            (deploy.manage)
//   POST /sites/{id}/git/deploy     (deploy.manage)
//   POST /sites/{id}/git/rotate     (deploy.manage)
//   POST /sites/{id}/git/remove     (deploy.manage)
//   POST /hooks/deploy/{token}      (public, signed)

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/notify"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// gitSiteOf resolves the website (nil = not found / no access). FindFull:
// the page needs the root, system user and document root.
func (s *Server) gitSiteOf(r *http.Request, id string) *hosting.Website {
	user := userFrom(r.Context())
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), n, user)
	if err != nil || site == nil {
		return nil
	}
	return site
}

// gitAudit binds the audit hook (user may be nil for the public webhook).
func (s *Server) gitAudit(r *http.Request, user *identity.User) hosting.Auditor {
	return func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		entry := audit.Entry{
			Action:     action,
			TargetType: targetType,
			TargetID:   target,
			Result:     result,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    details,
		}
		if user != nil {
			uid := user.ID
			entry.UserID = &uid
			entry.Username = user.Username
		}
		s.audit.Record(ctx, entry)
	}
}

// gitNotify binds the NotificationService for webhook warnings/errors.
func (s *Server) gitNotify(ctx context.Context) hosting.DeployNotif {
	return func(websiteID int64, kind, severity, title, body, link, dedupeKey string) {
		if rs := []rune(title); len(rs) > 200 {
			title = string(rs[:200])
		}
		if rs := []rune(body); len(rs) > 1000 {
			body = string(rs[:1000])
		}
		_, _ = notify.NewRepository(s.db).Store(ctx, sql.NullInt64{Int64: websiteID, Valid: true}, kind, severity, title, body, link, dedupeKey)
	}
}

func (s *Server) gitService(r *http.Request, user *identity.User) *hosting.DeployService {
	return hosting.NewDeployService(s.db, s.agent, s.gitAudit(r, user), s.crypto, s.gitNotify(r.Context()), s.currentServerID(r))
}

func (s *Server) gitFlashErr(w http.ResponseWriter, r *http.Request, err error) {
	sess := sessionFrom(r.Context())
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		sess.Flash("error", ue.What)
	} else {
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Message != "" {
			sess.Flash("error", ae.Message)
		} else {
			s.log.Error("git action", "error", err)
			sess.Flash("error", "The operation failed; the repository settings were left as they were.")
		}
	}
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/git", http.StatusSeeOther)
}

// gitAgentMessage is the agent's own failure message ("" = none).
func gitAgentMessage(err error) string {
	var ae *agent.Error
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	return ""
}

func (s *Server) gitFlashNotFound(w http.ResponseWriter, r *http.Request) {
	sessionFrom(r.Context()).Flash("error", "Website not found.")
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

func (s *Server) gitRedirectWith(w http.ResponseWriter, r *http.Request, msg string) {
	sessionFrom(r.Context()).Flash("success", msg)
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/git", http.StatusSeeOther)
}

// gitCSRF verifies the form's CSRF token (the panel validates every POST).
func (s *Server) gitCSRF(w http.ResponseWriter, r *http.Request) bool {
	if !session.ValidateCSRF(sessionFrom(r.Context()), formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return false
	}
	return true
}

// gitFormInput collects the repository form's fields.
func gitFormInput(r *http.Request) map[string]string {
	out := map[string]string{}
	for _, k := range []string{"repo", "branch", "path", "hooks", "auto_deploy"} {
		out[k] = r.FormValue(k)
	}
	return out
}

// gitPageData renders the Git page (the PHP's sites/git view variables).
// form/fieldErrs re-render a rejected form with its input.
func (s *Server) gitPageData(w http.ResponseWriter, r *http.Request, user *identity.User, site *hosting.Website, form map[string]string, fieldErrs map[string]string) {
	sess := sessionFrom(r.Context())
	ctx := r.Context()
	svc := s.gitService(r, user)
	cfg, err := svc.Config(ctx, site.ID)
	if err != nil {
		s.log.Error("read git config", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	keyError := ""
	if cfg != nil {
		// Refresh the running deployments (ignored when the agent is
		// unreachable), then make sure the deploy key is known.
		if err := svc.Refresh(ctx, site); err != nil {
			s.log.Debug("refresh deployments", "error", err)
		}
		if cfg.PublicKey == "" {
			key, err := svc.PublicKey(ctx, site)
			if err != nil {
				keyError = gitAgentMessage(err)
				if keyError == "" {
					keyError = "The deploy key could not be created."
					s.log.Error("create deploy key", "error", err)
				}
			} else {
				cfg.PublicKey = key
			}
		}
	}
	var webhookURL, webhookSecret string
	if cfg != nil {
		base, _ := s.settings.String(ctx, "panel_url", "")
		base = strings.TrimSuffix(base, "/")
		if base == "" {
			scheme := "http"
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			base = scheme + "://" + r.Host
		}
		webhookURL = base + "/hooks/deploy/" + cfg.WebhookToken
		secret, err := svc.WebhookSecret(ctx, cfg)
		if err != nil {
			s.log.Error("decrypt webhook secret", "error", err)
		} else {
			webhookSecret = secret
		}
	}
	history, err := svc.History(ctx, site.ID, 20)
	if err != nil {
		s.log.Error("read deployment history", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if form == nil {
		form = map[string]string{"branch": "main", "path": "app"}
		if cfg != nil {
			form = map[string]string{
				"repo":        cfg.Repo,
				"branch":      cfg.Branch,
				"path":        cfg.Path,
				"hooks":       strings.Join(cfg.Hooks, "\n"),
				"auto_deploy": boolFlag(cfg.AutoDeploy),
			}
		}
	}
	data := map[string]any{
		"User":                     user,
		"CSRF":                     session.CSRFToken(sessionFrom(r.Context())),
		"Site":                     site,
		"Cfg":                      cfg,
		"KeyError":                 keyError,
		"History":                  gitHistoryViews(history),
		"WebhookURL":               webhookURL,
		"WebhookSecret":            webhookSecret,
		"CanManage":                true, // the route itself is deploy.manage-gated
		"Form":                     form,
		"Errors":                   fieldErrs,
		"DocumentRootServesDeploy": cfg != nil && cfg.Path != "" && !strings.HasPrefix(site.DocumentRoot, strings.TrimSuffix(site.Root, "/")+"/"+cfg.Path),
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	data["Job"], _ = sess.PullFlash("job")
	s.render(w, "site_git", data)
}

// siteGit renders the Git deployment page (sites.git — deploy.manage).
func (s *Server) siteGit(w http.ResponseWriter, r *http.Request) {
	site := s.gitSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.notFound(w, r)
		return
	}
	s.gitPageData(w, r, userFrom(r.Context()), site, nil, nil)
}

// siteGitConfigure saves the repository settings (sites.git.configure —
// deploy.manage). A rejected form re-renders the page with the field
// errors and input.
func (s *Server) siteGitConfigure(w http.ResponseWriter, r *http.Request) {
	if !s.gitCSRF(w, r) {
		return
	}
	if err := r.ParseForm(); err != nil {
		s.gitFlashErr(w, r, errors.New("bad request"))
		return
	}
	site := s.gitSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.gitFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	input := gitFormInput(r)
	if err := s.gitService(r, user).Configure(r.Context(), site, input); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.gitPageData(w, r, user, site, input, ve.Fields)
			return
		}
		s.gitFlashErr(w, r, err)
		return
	}
	s.gitRedirectWith(w, r, "Repository settings saved. Add the deploy key to your Git host if the repository is private, then deploy.")
}

// siteGitDeploy starts a deployment now (sites.git.deploy — deploy.manage).
func (s *Server) siteGitDeploy(w http.ResponseWriter, r *http.Request) {
	if !s.gitCSRF(w, r) {
		return
	}
	site := s.gitSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.gitFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	job, err := s.gitService(r, user).Deploy(r.Context(), site, "manual", &user.ID)
	if err != nil {
		s.gitFlashErr(w, r, err)
		return
	}
	if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Deploy "+site.Domain, "website", fmt.Sprintf("%d", site.ID), &user.ID); err != nil {
		s.log.Error("track deploy job", "job", job, "error", err)
	}
	sessionFrom(r.Context()).Flash("job", job)
	s.gitRedirectWith(w, r, "Deployment started.")
}

// siteGitRotate regenerates the webhook URL + secret (sites.git.rotate —
// deploy.manage).
func (s *Server) siteGitRotate(w http.ResponseWriter, r *http.Request) {
	if !s.gitCSRF(w, r) {
		return
	}
	site := s.gitSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.gitFlashNotFound(w, r)
		return
	}
	if err := s.gitService(r, userFrom(r.Context())).RotateWebhook(r.Context(), site); err != nil {
		s.gitFlashErr(w, r, err)
		return
	}
	s.gitRedirectWith(w, r, "New webhook URL and secret generated. Update them on your Git host.")
}

// siteGitRemove disconnects the repository (sites.git.remove —
// deploy.manage). The deployed files stay in place.
func (s *Server) siteGitRemove(w http.ResponseWriter, r *http.Request) {
	if !s.gitCSRF(w, r) {
		return
	}
	site := s.gitSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.gitFlashNotFound(w, r)
		return
	}
	if err := s.gitService(r, userFrom(r.Context())).Remove(r.Context(), site); err != nil {
		s.gitFlashErr(w, r, err)
		return
	}
	s.gitRedirectWith(w, r, "Repository disconnected. The deployed files were left in place.")
}

// deployWebhook is the public, signed push webhook (no session, no CSRF).
// The signature check, the replay protection and the auto-deploy gate live
// in DeployService.Webhook.
func (s *Server) deployWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "Bad request."})
		return
	}
	svc := hosting.NewDeployService(s.db, s.agent, s.gitAudit(r, nil), s.crypto, s.gitNotify(r.Context()), s.currentServerID(r))
	status, message, err := svc.Webhook(r.Context(), r.PathValue("token"),
		r.Header.Get("X-Hub-Signature-256"), r.Header.Get("X-Gitlab-Token"), r.Header.Get("X-Github-Event"), body)
	if err != nil {
		s.log.Error("deploy webhook", "error", err)
	}
	writeJSON(w, status, map[string]any{"success": status < 300, "message": message})
}

// gitHistoryView is one history row with its time and commit hash
// formatted for the page.
type gitHistoryView struct {
	hosting.Deployment
	When   string
	Hash10 string
}

func gitHistoryViews(history []hosting.Deployment) []gitHistoryView {
	out := make([]gitHistoryView, 0, len(history))
	for _, d := range history {
		h10 := d.CommitHash
		if len(h10) > 10 {
			h10 = h10[:10]
		}
		out = append(out, gitHistoryView{Deployment: d, When: gitHistoryTime(d.CreatedAt), Hash10: h10})
	}
	return out
}

// boolFlag renders a bool as the form's "1"/"" value.
func boolFlag(b bool) string {
	if b {
		return "1"
	}
	return ""
}

// gitHistoryTime formats a deployments.created_at as "Y-m-d H:i UTC"
// (the PHP's gmdate('Y-m-d H:i')).
func gitHistoryTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

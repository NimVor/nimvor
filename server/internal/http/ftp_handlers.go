package http

// This file ports FtpController: the server's pure-ftpd (GET /ftp +
// POST /ftp/install|configure|disable — server.manage / packages.manage)
// and the per-website FTP/SFTP tab (GET /sites/{id}/ftp + the account
// save/delete + POST /sites/{id}/sftp — ftp.manage). The agent keeps the
// accounts; the panel only asks for them. Validation failures flash the
// first error and redirect, like the other migrated write pages.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// ftpService builds the agent-facing FTP service.
func (s *Server) ftpService() *hosting.FtpService {
	return hosting.NewFtpService(s.db, s.agent, s.currentServerID(nil))
}

// ftpLogin is an account's login name: <name>@<domain>
// (FtpController::login).
func ftpLogin(name, domain string) string {
	return strings.ToLower(strings.TrimSpace(name)) + "@" + strings.ToLower(domain)
}

// hostNameOf is the panel's host header host (the "connect with" hint),
// with any port stripped.
func hostNameOf(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	if host == "" {
		return "this server" // the PHP's fallback
	}
	return host
}

// ftpRedirect flashes the message (and optional job) and 303s back (the
// memcachedRedirectWith house pattern, any target).
func ftpRedirect(w http.ResponseWriter, r *http.Request, back string, ok bool, msg, job string) {
	sess := sessionFrom(r.Context())
	if ok {
		sess.Flash("success", msg)
	} else {
		sess.Flash("error", msg)
	}
	if job != "" {
		sess.Flash("job", job)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// ftpAgentErrMessage is an agent error's user-facing message (the views'
// agent_error partial).
func ftpAgentErrMessage(err error) string {
	if err == nil {
		return ""
	}
	var ae *agent.Error
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	return "The WhaCP agent could not be reached."
}

// ftpAudit records through the website audit hook (explicit target type).
func (s *Server) ftpAudit(r *http.Request, user *identity.User, action, targetType, target, result string, details map[string]any) {
	s.websiteAuditHook(r, user)(r.Context(), action, targetType, target, result, details)
}

// ---------------------------------------------------------------------------
// The server's FTP server (routes /ftp*).
// ---------------------------------------------------------------------------

// ftpIndex renders the FTP server page (FtpController::index).
func (s *Server) ftpIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	status, agentErr := s.ftpService().Status(r.Context(), "")
	if agentErr != nil {
		s.log.Error("ftp status", "error", agentErr)
	}
	sitesByUser := map[string]hosting.Website{}
	websites := []hosting.Website{}
	if agentErr == nil {
		if all, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user); err == nil {
			websites = all
			for _, w := range all {
				sitesByUser[w.SystemUser] = w
			}
		}
	}
	serverIP := ""
	if agentErr == nil {
		if ov := s.siteOverview(r, user); ov != nil {
			if ip, _ := ov.ServerIP(r.Context()); ip != "" {
				serverIP = ip
			}
		}
	}
	canFTP := s.can(r, "ftp.manage")
	canFiles := s.can(r, "files.manage")
	v := ftpIndexView{CanFTP: canFTP}
	sites := []ftpSiteRef{}
	if status != nil {
		v.Installed = status.Installed
		v.Configured = status.Configured
		v.Active = status.Active
		v.SetMin = ftpSettingString(status.Settings, "passive_min", "40110")
		v.SetMax = ftpSettingString(status.Settings, "passive_max", "40210")
		v.SetClients = ftpSettingString(status.Settings, "max_clients", "50")
		v.SetPerIP = ftpSettingString(status.Settings, "max_per_ip", "8")
		v.SetIP = ftpSettingString(status.Settings, "passive_ip", "")
		v.SetTLS = "required"
		if status.Settings["tls"] == "optional" {
			v.SetTLS = "optional"
		}
		if c := status.Certificate; c != nil {
			v.CertSubject = c.Subject
			v.CertNotAfter = certDay(c.NotAfter)
			v.CertSHA = certSHA(c.SHA256)
		}
		v.Others = strings.Join(status.OtherFTPServers, ", ")
		for _, w := range websites {
			sites = append(sites, ftpSiteRef{ID: w.ID, Domain: w.Domain})
		}
		for _, a := range status.Accounts {
			row := ftpIndexAccount{Name: a.Name, User: a.User, Dir: a.Dir, Quota: ftpQuota(a.QuotaMB)}
			if w, ok := sitesByUser[a.User]; ok {
				row.Website = w.Domain
				row.SiteID = w.ID
				row.Manage = canFTP
				row.LinkFiles = canFiles
			}
			v.Accounts = append(v.Accounts, row)
		}
	}
	data := map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sess),
		"V":           v,
		"Sites":       sites,
		"AgentErr":    ftpAgentErrMessage(agentErr),
		"ServerIP":    serverIP,
		"CanPackages": s.can(r, "packages.manage"),
		"Success":     pullFlashKey(sess, "success"),
		"Error":       pullFlashKey(sess, "error"),
		"Job":         pullFlashKey(sess, "job"),
	}
	s.render(w, "ftp_index", data)
}

// ftpIndexView is the server page's data, precomputed for the template
// (the settings map stays a raw agent reply; the page reads strings).
type ftpIndexView struct {
	Installed, Configured, Active bool
	CanFTP                        bool
	SetMin, SetMax, SetClients    string
	SetPerIP, SetIP, SetTLS       string
	CertSubject, CertNotAfter     string
	CertSHA                       string
	Others                        string
	Accounts                      []ftpIndexAccount
}

// ftpSiteRef is one website of the "Add FTP" menu (id + domain).
type ftpSiteRef struct {
	ID     int64
	Domain string
}

// ftpSettingString is a settings-map value as text (the PHP's $v()).
func ftpSettingString(m map[string]any, key, def string) string {
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	if f, ok := m[key].(float64); ok {
		return strconv.FormatInt(int64(f), 10)
	}
	return def
}

// certDay is the certificate's expiry day (substr(not_after, 0, 10)).
func certDay(notAfter string) string {
	if len(notAfter) >= 10 {
		return notAfter[:10]
	}
	return notAfter
}

// certSHA renders a hex SHA-256 in two-character pairs (the view's
// implode(':', str_split(…, 2))).
func certSHA(sha string) string {
	pairs := make([]string, 0, len(sha)/2)
	for i := 0; i+2 <= len(sha); i += 2 {
		pairs = append(pairs, sha[i:i+2])
	}
	return strings.Join(pairs, ":")
}

// ftpIndexAccount is one account row of the server page (precomputed for
// the template: the quota text, the joined website, the action links).
type ftpIndexAccount struct {
	Name      string
	User      string
	Website   string
	Dir       string
	Quota     string
	SiteID    int64
	Manage    bool
	LinkFiles bool
}

// ftpQuota renders the quota column ("" = not set) like the PHP
// format_bytes (one decimal, 1024-based).
func ftpQuota(mb int) string {
	if mb <= 0 {
		return ""
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f := float64(mb * 1048576)
	i := 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + " " + units[i]
}

// ftpInstall installs pure-ftpd (FtpController::install).
func (s *Server) ftpInstall(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	res, err := s.agent.Call(ctx, "packages.install", map[string]any{"name": "pure-ftpd"}, 60*time.Second)
	if err != nil {
		s.log.Error("install pure-ftpd", "error", err)
		ftpRedirect(w, r, "/ftp", false, ftpAgentErrMessage(err), "")
		return
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &out); err != nil || out.JobID == "" {
		s.log.Error("install pure-ftpd", "error", "unreadable reply")
		ftpRedirect(w, r, "/ftp", false, "The WhaCP agent answered with an unreadable reply.", "")
		return
	}
	_ = hosting.NewJobTracker(s.db).Track(ctx, s.currentServerID(r), out.JobID, "Install pure-ftpd", "server", "ftp", &user.ID)
	s.ftpAudit(r, user, "packages.install", "server", "ftp", "success", map[string]any{"name": "pure-ftpd"})
	ftpRedirect(w, r, "/ftp", true, "Installing pure-ftpd. Then set up the FTP server below.", out.JobID)
}

// ftpConfigure sets up the FTP server (FtpController::configure).
func (s *Server) ftpConfigure(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	errMsg := ""
	var (
		min, max, clients, perIP int
		ip                       string
	)
	if v, err := strconv.Atoi(r.PostFormValue("passive_min")); err != nil {
		errMsg = "Enter a range within 1024–65535, at most 5000 ports wide."
	} else {
		min = v
	}
	if errMsg == "" {
		if v, err := strconv.Atoi(r.PostFormValue("passive_max")); err != nil {
			errMsg = "Enter a range within 1024–65535, at most 5000 ports wide."
		} else {
			max = v
		}
	}
	if errMsg == "" && (min < 1024 || max > 65535 || min > max || max-min > 5000) {
		errMsg = "Enter a range within 1024–65535, at most 5000 ports wide."
	}
	ip = strings.TrimSpace(r.PostFormValue("passive_ip"))
	if errMsg == "" && ip != "" && net.ParseIP(ip).To4() == nil {
		errMsg = "Enter an IPv4 address or leave empty."
	}
	if errMsg == "" {
		if v, err := strconv.Atoi(r.PostFormValue("max_clients")); err != nil {
			errMsg = "Enter 1–1000 connections, and no more per address than in total."
		} else {
			clients = v
		}
	}
	if errMsg == "" {
		if v, err := strconv.Atoi(r.PostFormValue("max_per_ip")); err != nil {
			errMsg = "Enter 1–1000 connections, and no more per address than in total."
		} else {
			perIP = v
		}
	}
	if errMsg == "" && (clients < 1 || clients > 1000 || perIP < 1 || perIP > 1000 || perIP > clients) {
		errMsg = "Enter 1–1000 connections, and no more per address than in total."
	}
	if errMsg != "" {
		ftpRedirect(w, r, "/ftp", false, errMsg, "")
		return
	}
	tls := "required"
	if r.PostFormValue("tls") == "optional" {
		tls = "optional"
	}
	res, err := s.ftpService().Configure(ctx, map[string]any{
		"passive_min": min, "passive_max": max, "passive_ip": ip,
		"max_clients": clients, "max_per_ip": perIP, "tls": tls,
		"confirm_replace_service": r.PostFormValue("confirm_replace") == "1",
	})
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			ftpRedirect(w, r, "/ftp", false, ue.What, "")
		} else {
			s.log.Error("ftp configure", "error", err)
			ftpRedirect(w, r, "/ftp", false, ftpAgentErrMessage(err), "")
		}
		return
	}
	s.ftpAudit(r, user, "ftp.configure", "server", "ftp", "success", map[string]any{
		"passive_min": min, "passive_max": max, "passive_ip": ip,
		"max_clients": clients, "max_per_ip": perIP, "tls": tls, "stopped": res.Stopped,
	})
	msg := "The FTP server is running."
	if n := strings.TrimSpace(strings.Join(res.Notes, " ")); n != "" {
		msg += " " + n
	}
	ftpRedirect(w, r, "/ftp", true, msg, "")
}

// ftpDisable stops the FTP server (FtpController::disable).
func (s *Server) ftpDisable(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	err := s.ftpService().Disable(r.Context())
	if err != nil {
		s.log.Error("ftp disable", "error", err)
		ftpRedirect(w, r, "/ftp", false, ftpAgentErrMessage(err), "")
		return
	}
	s.ftpAudit(r, user, "ftp.disable", "server", "ftp", "success", nil)
	ftpRedirect(w, r, "/ftp", true, "The FTP server was stopped. Accounts are kept.", "")
}

// ---------------------------------------------------------------------------
// The website's FTP / SFTP tab (routes /sites/{id}/ftp*, /sites/{id}/sftp).
// ---------------------------------------------------------------------------

// siteFtp renders the website's FTP page (FtpController::show).
func (s *Server) siteFtp(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	status, err := s.ftpService().Status(r.Context(), site.SystemUser)
	if err != nil {
		s.log.Error("ftp status", "user", site.SystemUser, "error", err)
	}
	secret := ftpSecret{}
	if b, _ := sess.PullFlash("secret"); b != "" {
		_ = json.Unmarshal([]byte(b), &secret)
	}
	root := ""
	var accounts []ftpSiteAccount
	if status != nil {
		root = strings.TrimRight(status.SftpRoot, "/") + "/" + site.Domain
		for _, a := range status.Accounts {
			folder := strings.TrimPrefix(a.Dir, root)
			folder = strings.TrimLeft(folder, "/")
			if folder == a.Dir {
				folder = "" // the dir is outside the computed root
			}
			accounts = append(accounts, ftpSiteAccount{
				Name:    a.Name,
				Folder:  folder,
				Quota:   ftpQuota(a.QuotaMB),
				QuotaMB: a.QuotaMB,
			})
		}
	}
	var sftp *hosting.FtpSftp
	ftpReady := false
	tlsOptional := false
	if status != nil {
		sftp = status.SftpOf()
		ftpReady = status.Active
		tlsOptional = status.Settings["tls"] == "optional"
	}
	keysText := ""
	var keyLines []string
	if sftp != nil {
		parts := make([]string, 0, len(sftp.Keys))
		for _, k := range sftp.Keys {
			parts = append(parts, k.Key)
			_, comm, _ := strings.Cut(k.Key, " ")
			_, comm, _ = strings.Cut(comm, " ")
			keyLines = append(keyLines, strings.TrimSpace(k.Fingerprint+" "+comm))
		}
		keysText = strings.Join(parts, "\n")
	}
	back := fmt.Sprintf("/sites/%d/ftp", site.ID)
	canAdd := ftpReady && len(accounts) < hosting.MaxFtpAccounts
	data := map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sess),
		"Site":        site,
		"AgentErr":    ftpAgentErrMessage(err),
		"Accounts":    accounts,
		"Count":       len(accounts),
		"Max":         hosting.MaxFtpAccounts,
		"CanAdd":      canAdd,
		"FtpReady":    ftpReady,
		"TLSOptional": tlsOptional,
		"Sftp":        sftp,
		"KeysText":    keysText,
		"KeyLines":    keyLines,
		"Secret":      secret,
		"Host":        hostNameOf(r),
		"CanServer":   s.can(r, "server.manage"),
		"Success":     pullFlashKey(sess, "success"),
		"Error":       pullFlashKey(sess, "error"),
		"Job":         pullFlashKey(sess, "job"),
		"Back":        back,
	}
	s.render(w, "ftp_site", data)
}

// ftpSiteAccount is one account row of the website page (the folder shown
// relative to the website root, the quota text).
type ftpSiteAccount struct {
	Name    string
	Folder  string
	Quota   string
	QuotaMB int
}

// ftpSecret is the flashed one-time password (the secret partial).
type ftpSecret struct {
	Title    string `json:"title"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// siteFtpSave creates or updates a virtual account (FtpController::saveAccount).
func (s *Server) siteFtpSave(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	back := fmt.Sprintf("/sites/%d/ftp", site.ID)
	ctx := r.Context()
	domain := strings.ToLower(site.Domain)
	name := strings.ToLower(strings.TrimSpace(r.PostFormValue("name")))
	existing := r.PostFormValue("existing") == "1"
	var login string
	if existing {
		login = name
		if !strings.HasSuffix(login, "@"+domain) {
			ftpRedirect(w, r, back, false, "This account does not belong to the website.", "")
			return
		}
	} else {
		if !ftpNameRe.MatchString(name) {
			ftpRedirect(w, r, back, false, "Use up to 31 lower-case letters, digits and . _ -", "")
			return
		}
		login = ftpLogin(name, site.Domain)
		if len(login) > 64 {
			ftpRedirect(w, r, back, false, "The full login ("+login+") would be longer than 64 characters.", "")
			return
		}
	}
	dir := strings.Trim(r.PostFormValue("dir"), " /")
	if ftpDirBadRe.MatchString(dir) || len(dir) > 300 {
		ftpRedirect(w, r, back, false, "Enter a folder inside the website, e.g. public_html.", "")
		return
	}
	password := r.PostFormValue("password")
	generated := false
	if r.PostFormValue("generate") == "1" {
		password = ftpGeneratedPassword()
		generated = true
	}
	if password != "" && (len(password) < 10 || len(password) > 128) {
		ftpRedirect(w, r, back, false, "Use 10–128 characters, or generate one.", "")
		return
	}
	if !existing && password == "" {
		ftpRedirect(w, r, back, false, "Enter a password or generate one.", "")
		return
	}
	quota, _ := strconv.Atoi(r.PostFormValue("quota_mb"))
	if quota < 0 {
		quota = 0
	}
	owner := (*int64)(nil)
	if site.OwnerID.Valid {
		v := site.OwnerID.Int
		owner = &v
	}
	imp := s.ftpService()
	if !existing {
		status, err := imp.Status(ctx, site.SystemUser)
		if err != nil {
			s.log.Error("ftp status", "user", site.SystemUser, "error", err)
			ftpRedirect(w, r, back, false, ftpAgentErrMessage(err), "")
			return
		}
		if len(status.Accounts) >= hosting.MaxFtpAccounts {
			ftpRedirect(w, r, back, false, fmt.Sprintf("This website already has %d FTP accounts.", hosting.MaxFtpAccounts), "")
			return
		}
		if err := hosting.NewPlanLimits(s.db).AssertCanCreateExternal(ctx, "ftp_accounts", owner, 1,
			func(owners []int64) (int, error) { return imp.UsageCount(ctx, owners) }); err != nil {
			var ue *hosting.UserError
			if errors.As(err, &ue) {
				ftpRedirect(w, r, back, false, userFacingMsg(ue), "")
			} else {
				ftpRedirect(w, r, back, false, "The FTP account could not be created.", "")
			}
			return
		}
	}
	if err := imp.AccountSave(ctx, login, site.SystemUser, dir, quota, password); err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			ftpRedirect(w, r, back, false, ue.What, "")
		} else {
			s.log.Error("ftp account save", "login", login, "error", err)
			ftpRedirect(w, r, back, false, ftpAgentErrMessage(err), "")
		}
		return
	}
	details := map[string]any{"account": login, "dir": dir, "quota_mb": quota, "password_changed": password != ""}
	// Flashes must land before the redirect (the session cookie is sealed on
	// the first response write).
	if generated {
		b, _ := json.Marshal(ftpSecret{Title: "FTP password for " + login, User: login, Password: password})
		sessionFrom(r.Context()).Flash("secret", string(b))
	}
	sess := sessionFrom(r.Context())
	if existing {
		s.ftpAudit(r, user, "ftp.account_update", "website", fmt.Sprintf("%d", site.ID), "success", details)
		sess.Flash("success", "FTP account "+login+" saved.")
	} else {
		s.ftpAudit(r, user, "ftp.account_create", "website", fmt.Sprintf("%d", site.ID), "success", details)
		sess.Flash("success", "FTP account "+login+" created.")
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// siteFtpDelete removes a virtual account (FtpController::deleteAccount).
func (s *Server) siteFtpDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	back := fmt.Sprintf("/sites/%d/ftp", site.ID)
	login := strings.ToLower(strings.TrimSpace(r.PostFormValue("name")))
	if !strings.HasSuffix(login, "@"+strings.ToLower(site.Domain)) {
		ftpRedirect(w, r, back, false, "This account does not belong to the website.", "")
		return
	}
	if err := s.ftpService().AccountDelete(r.Context(), login, site.SystemUser); err != nil {
		s.log.Error("ftp account delete", "login", login, "error", err)
		ftpRedirect(w, r, back, false, ftpAgentErrMessage(err), "")
		return
	}
	s.ftpAudit(r, user, "ftp.account_delete", "website", fmt.Sprintf("%d", site.ID), "success", map[string]any{"account": login})
	ftpRedirect(w, r, back, true, "FTP account "+login+" deleted. Its files are kept.", "")
}

// siteSftp saves the website's SFTP access (FtpController::sftp).
func (s *Server) siteSftp(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	back := fmt.Sprintf("/sites/%d/ftp", site.ID)
	enabled := r.PostFormValue("enabled") == "1"
	keys := []string{}
	for _, k := range strings.Split(r.PostFormValue("keys"), "\n") {
		k = strings.TrimSpace(k)
		if k != "" && !strings.HasPrefix(k, "#") {
			keys = append(keys, k)
		}
	}
	passwordLogin := r.PostFormValue("password_login") == "1"
	password := r.PostFormValue("sftp_password")
	if password != "" && (len(password) < 10 || len(password) > 128) {
		ftpRedirect(w, r, back, false, "Use 10–128 characters.", "")
		return
	}
	params := map[string]any{"user": site.SystemUser, "enabled": enabled, "keys": keys, "password_login": passwordLogin}
	if password != "" {
		params["password"] = password
	}
	if err := s.ftpService().SftpConfigure(r.Context(), params); err != nil {
		s.log.Error("sftp configure", "user", site.SystemUser, "error", err)
		ftpRedirect(w, r, back, false, ftpAgentErrMessage(err), "")
		return
	}
	s.ftpAudit(r, user, "sftp.configure", "website", fmt.Sprintf("%d", site.ID), "success",
		map[string]any{"enabled": enabled, "keys": len(keys), "password_login": passwordLogin, "password_changed": password != ""})
	if enabled {
		ftpRedirect(w, r, back, true, "SFTP access saved.", "")
	} else {
		ftpRedirect(w, r, back, true, "SFTP access turned off.", "")
	}
}

// pullFlashKey reads (and consumes) a flashed string value.
func pullFlashKey(sess *session.Session, key string) string {
	v, _ := sess.PullFlash(key)
	return v
}

// ftpNameRe is the login name rule (FtpController::saveAccount).
var ftpNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,30}$`)

// ftpDirBadRe is a path escape (FtpController::saveAccount).
var ftpDirBadRe = regexp.MustCompile(`(^|/)\.\.(/|$)`)

// ftpGeneratedPassword is the one-time generated password (15 random bytes
// in a 20-character alphabet, as the PHP's rtrim(strtr(base64(…), '+/', 'Aa'))).
func ftpGeneratedPassword() string {
	b := make([]byte, 15)
	_, _ = rand.Read(b)
	s := base64.StdEncoding.EncodeToString(b)
	s = strings.NewReplacer("+", "A", "/", "a").Replace(s)
	return strings.TrimRight(s, "=")
}

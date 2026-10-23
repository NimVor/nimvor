package hosting

// This file ports the FTP area (FtpController + FtpUsage): the server's
// pure-ftpd (status / install / configure / disable) and the per-website
// virtual accounts + SFTP (the website's own system user). The agent keeps
// the accounts; the panel only asks for them.

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)


// MaxFtpAccounts is the safety limit of virtual accounts per website
// (FtpController::MAX_ACCOUNTS).
const MaxFtpAccounts = 25

// FtpAccount is one pure-ftpd virtual account (the agent's ftp.status).
type FtpAccount struct {
	Name    string `json:"name"`
	User    string `json:"user"`
	Dir     string `json:"dir"`
	QuotaMB int    `json:"quota_mb"`
}

// FtpSftpKey is one of the website's SFTP public keys.
type FtpSftpKey struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

// FtpSftp is the website's SFTP state (its own system user over SSH).
type FtpSftp struct {
	Shell         string       `json:"shell"` // disabled | jailed | normal
	PasswordLogin bool         `json:"password_login"`
	Keys          []FtpSftpKey `json:"keys"`
}

// FtpCertificate is the FTP server's TLS certificate (self-signed).
type FtpCertificate struct {
	Subject  string `json:"subject"`
	NotAfter string `json:"not_after"`
	SHA256   string `json:"sha256"`
}

// FtpStatus is the agent's ftp.status reply (settings stays a raw map —
// the view reads individual keys).
type FtpStatus struct {
	Installed       bool            `json:"installed"`
	Configured      bool            `json:"configured"`
	Active          bool            `json:"active"`
	SftpRoot        string          `json:"sftp_root"`
	Settings        map[string]any  `json:"settings"`
	Certificate     *FtpCertificate `json:"certificate"`
	OtherFTPServers []string        `json:"other_ftp_servers"`
	Accounts        []FtpAccount    `json:"accounts"`
	Sftp            []FtpSftp       `json:"sftp"`
}

// SftpOf is the status entry for one system user (the last one, as the
// view takes it); nil when the user has no SFTP state.
func (s *FtpStatus) SftpOf() *FtpSftp {
	if len(s.Sftp) == 0 {
		return nil
	}
	t := s.Sftp[len(s.Sftp)-1]
	return &t
}

// FtpService talks to the agent's FTP actions and counts this server's
// website users (the external ftp_accounts plan limit).
type FtpService struct {
	db       *sql.DB
	agent    AgentCaller
	serverID int64
}

// NewFtpService builds the service.
func NewFtpService(db *sql.DB, agent AgentCaller, serverID int64) *FtpService {
	return &FtpService{db: db, agent: agent, serverID: serverID}
}

// Status is the FTP server state; user restricts it to one system user's
// accounts and SFTP (the website page), "" for the whole server.
func (s *FtpService) Status(ctx context.Context, user string) (*FtpStatus, error) {
	params := map[string]any{}
	if user != "" {
		params["user"] = user
	}
	res, err := s.agent.Call(ctx, "ftp.status", params)
	if err != nil {
		return nil, err
	}
	var out FtpStatus
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AccountSave creates or updates a virtual account (ftp.account_save).
// password is "" for "keep the current one".
func (s *FtpService) AccountSave(ctx context.Context, login, user, dir string, quotaMB int, password string) error {
	params := map[string]any{"name": login, "user": user, "dir": dir, "quota_mb": quotaMB}
	if password != "" {
		params["password"] = password
	}
	_, err := s.agent.Call(ctx, "ftp.account_save", params)
	return err
}

// AccountDelete removes a virtual account (ftp.account_delete).
func (s *FtpService) AccountDelete(ctx context.Context, login, user string) error {
	_, err := s.agent.Call(ctx, "ftp.account_delete", map[string]any{"name": login, "user": user})
	return err
}

// ConfigureResult is the ftp.configure reply (the notes + stopped servers).
type ConfigureResult struct {
	Notes   []string `json:"notes"`
	Stopped []string `json:"stopped"`
}

// Configure sets up (or changes) the FTP server (ftp.configure, 120 s).
func (s *FtpService) Configure(ctx context.Context, params map[string]any) (*ConfigureResult, error) {
	res, err := s.agent.Call(ctx, "ftp.configure", params, 120*time.Second)
	if err != nil {
		return nil, err
	}
	var out ConfigureResult
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Disable stops the FTP server, keeping the accounts (ftp.disable).
func (s *FtpService) Disable(ctx context.Context) error {
	_, err := s.agent.Call(ctx, "ftp.disable", nil)
	return err
}

// SftpConfigure saves the website's SFTP access (sftp.configure, 60 s).
func (s *FtpService) SftpConfigure(ctx context.Context, params map[string]any) error {
	_, err := s.agent.Call(ctx, "sftp.configure", params, 60*time.Second)
	return err
}

// UsageCount is the FTP accounts of the given owners' websites — the
// accounts live on the server, so the count comes from the agent
// (FtpUsage::count).
func (s *FtpService) UsageCount(ctx context.Context, owners []int64) (int, error) {
	if len(owners) == 0 {
		return 0, nil
	}
	args := make([]any, len(owners))
	for i, o := range owners {
		args[i] = o
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT system_user FROM websites WHERE server_id = ? AND owner_id IN (`+placeholders(len(owners))+`)`,
		append([]any{s.serverID}, args...)...)
	if err != nil {
		return 0, err
	}
	users := map[string]bool{}
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			users[u] = true
		}
	}
	rows.Close()
	if len(users) == 0 {
		return 0, nil
	}
	status, err := s.Status(ctx, "")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range status.Accounts {
		if users[a.User] {
			n++
		}
	}
	return n, nil
}


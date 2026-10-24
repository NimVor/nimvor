package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
)

// AccountFile is one account backup file (PHP AccountBackupService::files).
type AccountFile struct {
	Path      string         `json:"path"`
	Name      string         `json:"name"`
	Where     string         `json:"where"`
	Size      int64          `json:"size"`
	Modified  string         `json:"modified"`
	Manifest  map[string]any `json:"manifest"`
	Problem   string         `json:"problem"`
	Removable bool           `json:"removable"`
}

// AccountForBackup is a user that can be backed up as an account.
type AccountForBackup struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Websites int    `json:"websites"`
}

// AccountBackupService lists account backup files (stub for the /backups index).
// The actual account backup create/restore routes (6 POSTs on POST /backups/account/...)
// arrive in a follow-up slice; the index page only needs the file listing.
type AccountBackupService struct {
	db       *sql.DB
	agent    AgentCaller
	serverID int64
}

// NewAccountBackupService builds the account-backup service.
func NewAccountBackupService(db *sql.DB, ag AgentCaller, serverID int64, _ Auditor) *AccountBackupService {
	return &AccountBackupService{db: db, agent: ag, serverID: serverID}
}

// Files returns account backup files visible to the admin (best effort).
func (s *AccountBackupService) Files(ctx context.Context) []AccountFile {
	if s.agent == nil {
		return nil
	}
	raw, err := s.agent.Call(ctx, "backup.account_list", nil)
	if err != nil {
		return nil
	}
	var env struct {
		Data []AccountFile `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	return env.Data
}

// Accounts returns users that can be backed up as an account.
func (s *AccountBackupService) Accounts(ctx context.Context) []AccountForBackup {
	rows, err := s.db.QueryContext(ctx,
		`SELECT u.id, u.username, COUNT(w.id) AS websites FROM users u LEFT JOIN websites w ON w.owner_id = u.id AND w.server_id = ?
		 GROUP BY u.id ORDER BY u.username`, s.serverID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []AccountForBackup
	for rows.Next() {
		var a AccountForBackup
		if err := rows.Scan(&a.ID, &a.Username, &a.Websites); err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}

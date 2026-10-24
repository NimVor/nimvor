package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
)

// StorageDetailed is one backup_storages row as the admin page shows it,
// with the agent's described location and test state.
type StorageDetailed struct {
	ID        int64
	Name      string
	Kind      string
	Where     string
	Prefix    string
	Active    bool
	TestOK    bool
	TestError string
	TestedAt  sql.NullString
	CreatedAt string
	Fields    map[string]string
}

// Storage types (BackupService::STORAGE_TYPES).
var StorageTypes = map[string]string{
	"s3":     "S3-compatible bucket",
	"sftp":   "SFTP server",
	"ftp":    "FTP server (FTPS)",
	"gdrive": "Google Drive",
	"local":  "Local folder (mounted disk or NAS)",
}

// StoragesDetailed lists storages with the described location and test state
// (PHP storages() plus the test columns).
func (s *BackupService) StoragesDetailed(ctx context.Context) ([]StorageDetailed, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, kind, config_enc, created_at, active, test_ok, test_error, tested_at
		 FROM backup_storages ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StorageDetailed
	for rows.Next() {
		var (
			d                StorageDetailed
			enc, testErr     string
			active, testOK   int
			testedAt         sql.NullString
		)
		if err := rows.Scan(&d.ID, &d.Name, &d.Kind, &enc, &d.CreatedAt, &active, &testOK, &testErr, &testedAt); err != nil {
			return nil, err
		}
		d.Active = active == 1
		d.TestOK = testOK == 1
		d.TestError = testErr
		d.TestedAt = testedAt
		cfg := s.decryptConfig(enc)
		d.Where = DescribeStorage(cfg)
		d.Prefix = asStr(cfg["prefix"])
		d.Fields = editableFields(cfg)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *BackupService) decryptConfig(enc string) map[string]any {
	cfg := map[string]any{}
	plain, err := s.crypto.Decrypt(enc)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(plain, &cfg)
	return cfg
}

// Describe returns the human location of a storage (PHP Describe).
func DescribeStorage(cfg map[string]any) string {
	switch cfg["type"] {
	case "sftp", "ftp":
		host := asStr(cfg["host"])
		user := asStr(cfg["username"])
		port := cfg["port"]
		out := fmt.Sprintf("%s://%s@%s", asStr(cfg["type"]), user, host)
		if n, ok := port.(float64); ok && n != 0 {
			out += fmt.Sprintf(":%d", int(n))
		} else if n, ok := port.(int); ok && n != 0 {
			out += fmt.Sprintf(":%d", n)
		}
		return out
	case "gdrive":
		return "Google Drive \u00b7 folder " + asStr(cfg["folder_id"])
	case "local":
		return asStr(cfg["path"])
	default:
		ep := asStr(cfg["endpoint"])
		bucket := asStr(cfg["bucket"])
		if ep != "" && bucket != "" {
			return ep + " \u00b7 " + bucket
		}
		if ep != "" {
			return ep
		}
		return bucket
	}
}

func editableFields(cfg map[string]any) map[string]string {
	keep := []string{"type", "prefix", "endpoint", "region", "bucket", "path_style", "host", "port", "username", "tls", "path"}
	out := map[string]string{}
	for _, k := range keep {
		if v, ok := cfg[k]; ok {
			switch x := v.(type) {
			case string:
				out[k] = x
			case float64:
				out[k] = fmt.Sprintf("%d", int(x))
			case bool:
				if x {
					out[k] = "1"
				}
			}
		}
	}
	return out
}

// StorageActive reports whether a storage is turned on.
func (s *BackupService) StorageActive(ctx context.Context, id int64) (bool, error) {
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT active FROM backup_storages WHERE id = ?`, id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return active == 1, err
}

// RetestStorage tests a saved storage again and records the result (PHP retestStorage).
func (s *BackupService) RetestStorage(ctx context.Context, id int64) (bool, string, error) {
	cfg, err := s.StorageConfig(ctx, id)
	if err != nil {
		return false, "", err
	}
	var testErr string
	trust, err := s.TestStorage(ctx, cfg)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			testErr = strings.TrimSpace(ae.Message + " " + ae.DetailsString())
		} else {
			testErr = err.Error()
		}
	} else if len(trust) != 0 {
		testErr = "The server presented a key that is not the one confirmed when the storage was added."
	}
	ok := testErr == ""
	q := `UPDATE backup_storages SET test_ok = ?, test_error = ?, tested_at = ?`
	if !ok {
		q += `, active = 0`
	}
	q += ` WHERE id = ?`
	if _, err := s.db.ExecContext(ctx, q, boolInt(ok), trimRunes(testErr, 500), backupNow(), id); err != nil {
		return false, "", err
	}
	s.audit(ctx, "backup.storage_test", "backup_storage", fmt.Sprintf("%d", id), mapBool(ok), map[string]any{"error": nullable(testErr)})
	return ok, testErr, nil
}

// SetStorageActive turns a storage on or off (PHP setStorageActive).
func (s *BackupService) SetStorageActive(ctx context.Context, id int64, active bool) error {
	var (
		testOK   int
		testedAt sql.NullString
		name     string
	)
	err := s.db.QueryRowContext(ctx, `SELECT name, test_ok, tested_at FROM backup_storages WHERE id = ?`, id).
		Scan(&name, &testOK, &testedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return &UserError{What: "The remote storage no longer exists."}
	}
	if err != nil {
		return err
	}
	if active && (testOK != 1 || !testedAt.Valid) {
		return &UserError{What: "Test the storage first.", Why: "Its last connection test did not succeed.", Fix: "Use \"Test\" and turn it on once the test passes."}
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE backup_storages SET active = ? WHERE id = ?`, boolInt(active), id); err != nil {
		return err
	}
	act := "backup.storage_off"
	if active {
		act = "backup.storage_on"
	}
	s.audit(ctx, act, "backup_storage", fmt.Sprintf("%d", id), "success", map[string]any{"name": name})
	return nil
}

// DeleteStorage removes a storage (PHP deleteStorage).
func (s *BackupService) DeleteStorage(ctx context.Context, id int64) error {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM backups WHERE storage_id = ? AND local = 0 AND status = 'completed'`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return &UserError{What: "Some backups are only stored there.", Why: "Deleting the storage would make them unreachable from WhaCP.", Fix: "Delete those backups first."}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM backup_storages WHERE id = ?`, id); err != nil {
		return err
	}
	s.audit(ctx, "backup.storage_delete", "backup_storage", fmt.Sprintf("%d", id), "success", nil)
	return nil
}

// BuildStorage validates the new-storage form (PHP buildStorage).
func (s *BackupService) BuildStorage(ctx context.Context, input map[string]string) (string, map[string]any, error) {
	ve := &ValidationError{}
	name := strings.TrimSpace(input["name"])
	if strings.HasPrefix(name, "__edit__") {
		// updateStorage checks name itself
	} else if name == "" || len([]rune(name)) > 60 {
		ve.Add("name", "Enter a name (up to 60 characters).")
	} else {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_storages WHERE name = ?`, name).Scan(&n); err != nil {
			return "", nil, err
		}
		if n > 0 {
			ve.Add("name", "A storage with this name exists.")
		}
	}
	typ := strings.TrimSpace(input["type"])
	if typ == "" {
		typ = "s3"
	}
	if _, ok := StorageTypes[typ]; !ok {
		ve.Add("type", "Choose a storage type.")
	}
	prefix := strings.Trim(input["prefix"], "/")
	if !regexp.MustCompile(`^[A-Za-z0-9._/-]{0,200}$`).MatchString(prefix) || strings.Contains(prefix, "..") {
		ve.Add("prefix", "Use letters, digits and . _ / -")
	}
	cfg := map[string]any{"type": typ, "prefix": prefix}
	switch typ {
	case "s3":
		endpoint := strings.TrimRight(strings.TrimSpace(input["endpoint"]), "/")
		bucket := strings.TrimSpace(input["bucket"])
		region := strings.TrimSpace(input["region"])
		if region == "" {
			region = "us-east-1"
		}
		accessKey := strings.TrimSpace(input["access_key"])
		secretKey := strings.TrimSpace(input["secret_key"])
		if !regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]+)?$`).MatchString(endpoint) &&
			!regexp.MustCompile(`^http://(127\.0\.0\.1|localhost)(:[0-9]+)?$`).MatchString(endpoint) {
			ve.Add("endpoint", "Enter the https:// endpoint, e.g. https://s3.eu-central-1.amazonaws.com.")
		}
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(bucket) {
			ve.Add("bucket", "Enter a valid bucket name.")
		}
		if !regexp.MustCompile(`^[a-z0-9-]{1,40}$`).MatchString(region) {
			ve.Add("region", "Enter a region such as eu-central-1 (or us-east-1 if unsure).")
		}
		if accessKey == "" || secretKey == "" {
			ve.Add("access_key", "Enter the access key and secret key.")
		}
		cfg["endpoint"] = endpoint
		cfg["region"] = region
		cfg["bucket"] = bucket
		cfg["access_key"] = accessKey
		cfg["secret_key"] = secretKey
		cfg["path_style"] = input["path_style"] == "1"
	case "sftp", "ftp":
		host := strings.Trim(strings.TrimSpace(input["host"]), "[]")
		portStr := strings.TrimSpace(input["port"])
		port := 0
		if portStr != "" {
			var p int
			if _, err := fmt.Sscan(portStr, &p); err == nil {
				port = p
			} else {
				ve.Add("port", "Enter a port between 1 and 65535.")
			}
		}
		if port < 0 || port > 65535 {
			ve.Add("port", "Enter a port between 1 and 65535.")
		}
		if !regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`).MatchString(host) &&
			regexp.MustCompile(`^(\d+\.\d+\.\d+\.\d+|[0-9a-fA-F:]+)$`).FindString(host) == "" {
			// allow IP check via net.ParseIP? Keep simple: if not hostname, require IP-like
			if host == "" {
				ve.Add("host", "Enter a host name or IP address.")
			}
		}
		if host == "" {
			ve.Add("host", "Enter a host name or IP address.")
		}
		username := strings.TrimSpace(input["username"])
		if !regexp.MustCompile(`^[A-Za-z0-9._@+\-]{1,100}$`).MatchString(username) {
			ve.Add("username", "Enter the user name (letters, digits and . _ @ + -).")
		}
		password := input["password"]
		privateKey := strings.TrimSpace(input["private_key"])
		if typ == "sftp" && password == "" && privateKey == "" {
			ve.Add("password", "Enter a password or paste a private key.")
		}
		if typ == "ftp" && password == "" {
			ve.Add("password", "Enter the password.")
		}
		cfg["host"] = host
		cfg["port"] = port
		cfg["username"] = username
		cfg["password"] = password
		if typ == "sftp" {
			cfg["private_key"] = privateKey
		} else {
			tls := strings.TrimSpace(input["tls"])
			if tls == "" {
				tls = "explicit"
			}
			if tls != "explicit" && tls != "implicit" && tls != "none" {
				ve.Add("tls", "Choose the encryption.")
			}
			cfg["tls"] = tls
		}
	case "local":
		path := "/" + strings.Trim(strings.TrimSpace(input["path"]), "/")
		if !(regexp.MustCompile(`^/(mnt|media)/[A-Za-z0-9._/-]+$`).MatchString(path) ||
			regexp.MustCompile(`^/(backups?|srv/backups?)(/[A-Za-z0-9._/-]+)?$`).MatchString(path)) ||
			strings.Contains(path, "..") {
			ve.Add("path", "Enter /backups (or a folder in it, also /backup, /srv/backup(s)), or a folder of a mounted disk under /mnt or /media.")
		}
		cfg["path"] = path
	case "gdrive":
		clientID := strings.TrimSpace(input["client_id"])
		clientSecret := strings.TrimSpace(input["client_secret"])
		if clientID == "" || clientSecret == "" {
			ve.Add("client_id", "Enter the OAuth client ID and secret of your Google Cloud project.")
		}
		cfg["client_id"] = clientID
		cfg["client_secret"] = clientSecret
	}
	if len(ve.Fields) > 0 {
		return "", nil, ve
	}
	return name, cfg, nil
}

// TestStorage calls backup.remote_test and returns what must be trusted (PHP testStorage).
func (s *BackupService) TestStorage(ctx context.Context, cfg map[string]any) (map[string]string, error) {
	var res struct {
		HostKey    string `json:"host_key"`
		Fingerprint string `json:"fingerprint"`
		TLSPin     string `json:"tls_pin"`
		TLSSubject string `json:"tls_subject"`
		Created    bool   `json:"created"`
		FolderID   string `json:"folder_id"`
	}
	if err := s.callAgent(ctx, "backup.remote_test", map[string]any{"remote": cfg}, durPtr(120*time.Second), &res); err != nil {
		return nil, err
	}
	trust := map[string]string{}
	if cfg["type"] == "sftp" && asStr(cfg["host_key"]) == "" && res.HostKey != "" {
		cfg["host_key"] = res.HostKey
		trust = map[string]string{"host_key": res.HostKey, "fingerprint": res.Fingerprint}
	}
	if cfg["type"] == "ftp" && asStr(cfg["tls_pin"]) == "" && res.TLSPin != "" {
		cfg["tls_pin"] = res.TLSPin
		trust = map[string]string{"tls_pin": res.TLSPin, "tls_subject": res.TLSSubject}
	}
	if cfg["type"] == "local" && res.Created {
		s.audit(ctx, "backup.storage_folder_create", "backup_storage", asStr(cfg["path"]), "success", map[string]any{"path": asStr(cfg["path"])})
	}
	if cfg["type"] == "gdrive" && res.FolderID != "" {
		cfg["folder_id"] = res.FolderID
	}
	return trust, nil
}

// InsertStorage saves a tested storage (PHP insertStorage).
func (s *BackupService) InsertStorage(ctx context.Context, name string, cfg map[string]any) (int64, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_storages WHERE name = ?`, name).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, &ValidationError{Fields: map[string]string{"name": "A storage with this name exists."}}
	}
	js, _ := json.Marshal(cfg)
	enc, err := s.crypto.Encrypt(js)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO backup_storages (name, kind, config_enc, created_at, active, test_ok, tested_at) VALUES (?, ?, ?, ?, 1, 1, ?)`,
		name, asStr(cfg["type"]), enc, backupNow(), backupNow())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	s.audit(ctx, "backup.storage_create", "backup_storage", fmt.Sprintf("%d", id), "success",
		map[string]any{"name": name, "type": asStr(cfg["type"]), "where": DescribeStorage(cfg)})
	return id, nil
}

// UpdateStorage changes a storage (PHP updateStorage).
func (s *BackupService) UpdateStorage(ctx context.Context, id int64, input map[string]string) error {
	var (
		kind, enc string
		name0     string
	)
	err := s.db.QueryRowContext(ctx, `SELECT name, kind, config_enc FROM backup_storages WHERE id = ?`, id).
		Scan(&name0, &kind, &enc)
	if errors.Is(err, sql.ErrNoRows) {
		return &UserError{What: "The remote storage no longer exists."}
	}
	if err != nil {
		return err
	}
	cfg := s.decryptConfig(enc)
	// Preserve type from stored config (PHP uses $cfg['type'] as source)
	typ := asStr(cfg["type"])
	if typ == "" {
		typ = kind
	}
	// Build form with editable fields merged
	form := map[string]string{"type": typ, "name": fmt.Sprintf("__edit__%d", id)}
	for k, v := range editableFields(cfg) {
		form[k] = v
	}
	for _, k := range []string{"prefix", "endpoint", "region", "bucket", "username", "tls", "path", "port"} {
		if v, ok := input[k]; ok {
			form[k] = v
		}
	}
	if v, ok := input["path_style"]; ok {
		form["path_style"] = v
	} else {
		delete(form, "path_style")
	}
	for _, k := range []string{"access_key", "secret_key", "password", "private_key", "client_id", "client_secret"} {
		if v, ok := input[k]; ok && strings.TrimSpace(v) != "" {
			form[k] = v
		} else if sv, ok := cfg[k]; ok {
			if s, ok := sv.(string); ok {
				form[k] = s
			}
		}
	}
	// SFTP/FTP host/port immutable
	if typ == "sftp" || typ == "ftp" {
		form["host"] = asStr(cfg["host"])
		if hv, ok := input["host"]; ok && strings.TrimSpace(hv) != "" && strings.TrimSpace(hv) != asStr(cfg["host"]) {
			return &ValidationError{Fields: map[string]string{"host": "The server of a storage cannot be changed (its key was confirmed for this server). Add a new storage instead."}}
		}
		portStr := strings.TrimSpace(input["port"])
		if portStr != "" {
			var p int
			fmt.Sscan(portStr, &p)
			if p != int(cfgPort(cfg["port"])) {
				return &ValidationError{Fields: map[string]string{"port": "The port of a storage cannot be changed. Add a new storage instead."}}
			}
		}
	}
	name := strings.TrimSpace(input["name"])
	if name == "" || len([]rune(name)) > 60 {
		return &ValidationError{Fields: map[string]string{"name": "Enter a name (up to 60 characters)."}}
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_storages WHERE name = ? AND id <> ?`, name, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return &ValidationError{Fields: map[string]string{"name": "A storage with this name exists."}}
	}
	var newCfg map[string]any
	if typ == "gdrive" {
		newCfg = cfg
		prefix := strings.Trim(input["prefix"], "/")
		if !regexp.MustCompile(`^[A-Za-z0-9._/-]{0,200}$`).MatchString(prefix) || strings.Contains(prefix, "..") {
			return &ValidationError{Fields: map[string]string{"prefix": "Use letters, digits and . _ / -"}}
		}
		newCfg["prefix"] = prefix
	} else {
		_, nc, err := s.BuildStorage(ctx, form)
		if err != nil {
			return err
		}
		newCfg = nc
		for _, k := range []string{"host_key", "tls_pin", "folder_id"} {
			if v, ok := cfg[k]; ok {
				newCfg[k] = v
			}
		}
	}
	if trust, err := s.TestStorage(ctx, newCfg); err != nil {
		return err
	} else if len(trust) != 0 {
		return &UserError{What: "The storage presented a key that was not confirmed.", Fix: "Add it as a new storage to confirm its key."}
	}
	js, _ := json.Marshal(newCfg)
	enc2, err := s.crypto.Encrypt(js)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE backup_storages SET name = ?, config_enc = ?, test_ok = 1, test_error = '', tested_at = ? WHERE id = ?`,
		name, enc2, backupNow(), id); err != nil {
		return err
	}
	s.audit(ctx, "backup.storage_update", "backup_storage", fmt.Sprintf("%d", id), "success",
		map[string]any{"name": name, "was": name0, "where": DescribeStorage(newCfg)})
	return nil
}

func cfgPort(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}

// Seal encrypts a pending storage for the session (PHP seal).
func (s *BackupService) SealPending(name string, cfg map[string]any) (string, error) {
	js, _ := json.Marshal(map[string]any{"name": name, "cfg": cfg, "at": time.Now().Unix()})
	return s.crypto.Encrypt(js)
}

// Unseal decrypts a pending storage (PHP unseal).
func (s *BackupService) UnsealPending(sealed string) (string, map[string]any, error) {
	plain, err := s.crypto.Decrypt(sealed)
	if err != nil {
		return "", nil, &UserError{What: "The storage setup expired.", Fix: "Add the storage again."}
	}
	var p struct {
		Name string         `json:"name"`
		Cfg  map[string]any `json:"cfg"`
		At   int64          `json:"at"`
	}
	if err := json.Unmarshal(plain, &p); err != nil || p.Name == "" || p.Cfg == nil || time.Now().Unix()-p.At > 1800 {
		return "", nil, &UserError{What: "The storage setup expired.", Fix: "Add the storage again."}
	}
	return p.Name, p.Cfg, nil
}

// Google redirect / auth helpers.

func (s *BackupService) GoogleRedirectURI(panelURL string) string {
	return strings.TrimRight(panelURL, "/") + "/backups/storages/google/callback"
}

func (s *BackupService) GoogleAuthURL(pendingName string, pendingCfg map[string]any, redirectURI, state string) string {
	v := url.Values{}
	v.Set("client_id", asStr(pendingCfg["client_id"]))
	v.Set("redirect_uri", redirectURI)
	v.Set("response_type", "code")
	v.Set("scope", "https://www.googleapis.com/auth/drive.file")
	v.Set("access_type", "offline")
	v.Set("prompt", "consent")
	v.Set("state", state)
	return "https://accounts.google.com/o/oauth2/v2/auth?" + v.Encode()
}

// OAuthTokenExchanger exchanges code for tokens (injectable for tests).
type OAuthTokenExchanger interface {
	Exchange(ctx context.Context, form url.Values) (map[string]any, error)
}

type googleOAuth struct {
	client *http.Client
}

func (g googleOAuth) Exchange(ctx context.Context, form url.Values) (map[string]any, error) {
	client := g.client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// GoogleComplete exchanges code for refresh_token and saves storage (PHP googleComplete).
func (s *BackupService) GoogleComplete(ctx context.Context, pendingName string, pendingCfg map[string]any, code, redirectURI string, exchanger OAuthTokenExchanger) (int64, error) {
	if exchanger == nil {
		exchanger = googleOAuth{client: http.DefaultClient}
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", asStr(pendingCfg["client_id"]))
	form.Set("client_secret", asStr(pendingCfg["client_secret"]))
	res, err := exchanger.Exchange(ctx, form)
	if err != nil {
		return 0, err
	}
	refresh, _ := res["refresh_token"].(string)
	if refresh == "" {
		desc := asStr(res["error_description"])
		if desc == "" {
			desc = asStr(res["error"])
		}
		return 0, &UserError{What: "Google did not grant offline access.", Why: desc, Fix: "Try again; if it keeps failing, remove WhaCP from your Google account's third-party access and connect again."}
	}
	cfg := map[string]any{}
	for k, v := range pendingCfg {
		cfg[k] = v
	}
	cfg["refresh_token"] = refresh
	if _, err := s.TestStorage(ctx, cfg); err != nil {
		return 0, err
	}
	return s.InsertStorage(ctx, pendingName, cfg)
}

// FindInStorage imports backups kept in a remote storage (PHP findInStorage).
func (s *BackupService) FindInStorage(ctx context.Context, storageID int64, actorID sql.NullInt64) (map[string]any, error) {
	cfg, err := s.StorageConfig(ctx, storageID)
	if err != nil {
		return nil, err
	}
	var res struct {
		Backups []struct {
			ID       string         `json:"id"`
			Kind     string         `json:"kind"`
			Domain   string         `json:"domain"`
			Manifest map[string]any `json:"manifest"`
			Skipped  string         `json:"skipped"`
		} `json:"backups"`
	}
	if err := s.callAgent(ctx, "backup.remote_import", map[string]any{"remote": cfg}, durPtr(600*time.Second), &res); err != nil {
		return nil, err
	}
	out := map[string]any{"imported": 0, "known": 0, "no_website": []string{}, "skipped": []string{}}
	imported, known := 0, 0
	noWebsiteSet := map[string]bool{}
	skipped := []string{}
	for _, b := range res.Backups {
		what := b.Domain + " " + b.ID
		if b.Kind == "server" {
			what = "server backup " + b.ID
		}
		if b.Skipped != "" || b.Manifest == nil {
			reason := b.Skipped
			if reason == "" {
				reason = "not readable"
			}
			skipped = append(skipped, strings.TrimSpace(what)+": "+reason)
			continue
		}
		m := b.Manifest
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backups WHERE server_id = ? AND backup_id = ?`, s.serverID, b.ID).Scan(&exists); err != nil {
			return nil, err
		}
		if exists > 0 {
			known++
			continue
		}
		var websiteID sql.NullInt64
		if m["kind"] == "site" {
			var wid sql.NullInt64
			err := s.db.QueryRowContext(ctx, `SELECT id FROM websites WHERE server_id = ? AND domain = ?`, s.serverID, asStr(m["domain"])).Scan(&wid)
			if err != nil || !wid.Valid {
				noWebsiteSet[asStr(m["domain"])] = true
				continue
			}
			websiteID = wid
		}
		delete(m, "meta")
		created := asStr(m["created_at"])
		if created == "" {
			created = backupNow()
		}
		mj, _ := json.Marshal(m)
		size := int64(0)
		if n, ok := m["size"].(float64); ok {
			size = int64(n)
		}
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO backups (server_id, website_id, kind, domain, backup_id, label, manifest, size, encrypted, storage_id, local, status, user_id, created_at, finished_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?, 0, 'completed', ?, ?, ?)`,
			s.serverID, nullOrZero(websiteID), asStr(m["kind"]), asStr(m["domain"]), b.ID, trimRunes(asStr(m["label"]), 100), string(mj), size, storageID, nullOrZero(actorID), created, created)
		if err != nil {
			return nil, err
		}
		imported++
	}
	noWebsite := []string{}
	for k := range noWebsiteSet {
		noWebsite = append(noWebsite, k)
	}
	out["imported"] = imported
	out["known"] = known
	out["no_website"] = noWebsite
	out["skipped"] = skipped
	s.audit(ctx, "backup.storage_scan", "backup_storage", fmt.Sprintf("%d", storageID), "success",
		map[string]any{"imported": imported, "known": known, "skipped": len(skipped), "no_website": noWebsite})
	return out, nil
}

// Server backup helpers.

// ServerCreateOptions is the server backup form.
type ServerCreateOptions struct {
	Encrypt   bool
	StorageID sql.NullInt64
	KeepLocal bool
	Label     string
}

// CreateServer starts a server configuration backup (PHP createServer).
func (s *BackupService) CreateServer(ctx context.Context, opts ServerCreateOptions, actorID sql.NullInt64) (string, error) {
	label := trimLabel(opts.Label)
	params := map[string]any{"encrypt": true, "label": label}
	if opts.StorageID.Valid {
		remote, err := s.ActiveStorageConfig(ctx, opts.StorageID.Int64)
		if err != nil {
			return "", err
		}
		params["remote"] = remote
		params["keep_local"] = opts.KeepLocal
	}
	var res struct{ JobID string `json:"job_id"` }
	if err := s.callAgent(ctx, "backup.server", params, nil, &res); err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO backups (server_id, schedule_id, kind, label, encrypted, storage_id, status, job_id, user_id, created_at)
		 VALUES (?, NULL, 'server', ?, 1, ?, 'running', ?, ?, ?)`,
		s.serverID, label, nullOrZero(opts.StorageID), res.JobID, nullOrZero(actorID), backupNow()); err != nil {
		return "", err
	}
	s.audit(ctx, "backup.server", "server", fmt.Sprintf("%d", s.serverID), "success", map[string]any{"remote": opts.StorageID.Valid})
	return res.JobID, nil
}

// ListServer lists server configuration backups (PHP list(null)).
func (s *BackupService) ListServer(ctx context.Context) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+backupColumns+` FROM backups b LEFT JOIN backup_storages s ON s.id = b.storage_id
		 WHERE b.server_id = ? AND b.kind = 'server' ORDER BY b.id DESC LIMIT 100`, s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backup
	for rows.Next() {
		b, err := scanBackupRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// FindServer finds one server backup by id (nil = not found).
func (s *BackupService) FindServer(ctx context.Context, id int64) (*Backup, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+backupColumns+` FROM backups b LEFT JOIN backup_storages s ON s.id = b.storage_id
		 WHERE b.id = ? AND b.server_id = ? AND b.kind = 'server'`, id, s.serverID)
	b, err := scanBackupRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ServerComponents are the restorable server parts (PHP SERVER_COMPONENTS).
var ServerComponents = map[string]string{
	"nginx":       "Nginx (/etc/nginx)",
	"php":         "PHP (/etc/php)",
	"mysql":       "MySQL/MariaDB settings (/etc/mysql; restarts the database)",
	"redis":       "Redis (/etc/redis; restarts Redis)",
	"letsencrypt": "Certificates (/etc/letsencrypt)",
	"fail2ban":    "Fail2ban (/etc/fail2ban)",
	"cron":        "System cron jobs (/etc/cron.d)",
}

// RestoreServer restores parts of a server backup (PHP restoreServer).
func (s *BackupService) RestoreServer(ctx context.Context, b *Backup, components []string, extractOnly bool, confirm string) (string, error) {
	if b.Status != "completed" || b.Kind != "server" {
		return "", &UserError{What: "Only completed server backups can be restored."}
	}
	// dedup + intersect with known components
	seen := map[string]bool{}
	filtered := []string{}
	for _, c := range components {
		if _, ok := ServerComponents[c]; !ok {
			continue
		}
		if !seen[c] {
			seen[c] = true
			filtered = append(filtered, c)
		}
	}
	if !extractOnly && len(filtered) == 0 {
		return "", &UserError{What: "Choose what to restore, or extract the backup for review."}
	}
	if !extractOnly && strings.ToLower(strings.TrimSpace(confirm)) != "restore" {
		return "", &UserError{What: "Type \"restore\" to confirm."}
	}
	params := map[string]any{"id": b.BackupID, "components": filtered, "extract_only": extractOnly, "confirm": true}
	if b.StorageID.Valid {
		remote, err := s.StorageConfig(ctx, b.StorageID.Int64)
		if err != nil {
			return "", err
		}
		params["remote"] = remote
	}
	var res struct{ JobID string `json:"job_id"` }
	if err := s.callAgent(ctx, "backup.server_restore", params, nil, &res); err != nil {
		return "", err
	}
	action := "backup.server_restore"
	if extractOnly {
		action = "backup.server_extract"
	}
	s.audit(ctx, action, "server", fmt.Sprintf("%d", s.serverID), "success", map[string]any{"backup": b.BackupID, "components": filtered})
	return res.JobID, nil
}

// KeyInfo holds the backup encryption key status (PHP keyInfo).
type KeyInfo struct {
	Exists bool   `json:"exists"`
	KeyID  string `json:"key_id"`
	Path   string `json:"path"`
}

// KeyInfo returns the agent's backup key status (nil = agent unreachable).
func (s *BackupService) KeyInfo(ctx context.Context) *KeyInfo {
	var out KeyInfo
	if err := s.callAgent(ctx, "backup.key_info", nil, nil, &out); err != nil {
		return nil
	}
	return &out
}

// ExportKey exports the backup key encrypted with a passphrase (PHP exportKey).
func (s *BackupService) ExportKey(ctx context.Context, passphrase string) (string, string, error) {
	var res struct {
		KeyID string `json:"key_id"`
		File  string `json:"file"`
	}
	if err := s.callAgent(ctx, "backup.key_export", map[string]any{"passphrase": passphrase}, durPtr(60*time.Second), &res); err != nil {
		return "", "", err
	}
	s.audit(ctx, "backup.key_export", "server", fmt.Sprintf("%d", s.serverID), "success", map[string]any{"key_id": res.KeyID})
	return res.KeyID, res.File, nil
}

// ImportKey installs a backup key from a file (PHP importKey).
func (s *BackupService) ImportKey(ctx context.Context, fileContent, passphrase string) (string, bool, error) {
	var res struct {
		KeyID   string `json:"key_id"`
		Already bool   `json:"already"`
	}
	err := s.callAgent(ctx, "backup.key_import", map[string]any{"file": fileContent, "passphrase": passphrase}, durPtr(60*time.Second), &res)
	if err != nil {
		var ae *agent.Error
		msg := ""
		if errors.As(err, &ae) {
			msg = ae.Message + " " + ae.DetailsString()
		} else {
			msg = err.Error()
		}
		s.audit(ctx, "backup.key_import", "server", fmt.Sprintf("%d", s.serverID), "failure", map[string]any{"error": trimRunes(msg, 300)})
		return "", false, err
	}
	s.audit(ctx, "backup.key_import", "server", fmt.Sprintf("%d", s.serverID), "success", map[string]any{"key_id": res.KeyID, "already": res.Already})
	return res.KeyID, res.Already, nil
}

// ListAllSite lists all site backups on this server (for admin index).
func (s *BackupService) ListAllSite(ctx context.Context, limit int) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+backupColumns+` FROM backups b LEFT JOIN backup_storages s ON s.id = b.storage_id
		 WHERE b.server_id = ? AND b.kind = 'site' ORDER BY b.id DESC LIMIT ?`, s.serverID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backup
	for rows.Next() {
		b, err := scanBackupRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListForUserSQL is the admin/customer website backups list using the websites scope.
// It mirrors PHP listForUser via the websites table join.
func (s *BackupService) ListForUserSQL(ctx context.Context, serverID int64, scopeCond string, scopeArgs []any, limit int) ([]Backup, error) {
	args := append([]any{serverID}, scopeArgs...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+backupColumns+` FROM backups b JOIN websites w ON w.id = b.website_id LEFT JOIN backup_storages s ON s.id = b.storage_id
		 WHERE b.server_id = ? AND b.kind = 'site' AND `+scopeCond+` ORDER BY b.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backup
	for rows.Next() {
		b, err := scanBackupRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// helpers

func durPtr(d time.Duration) *time.Duration { return &d }

func mapBool(ok bool) string {
	if ok {
		return "success"
	}
	return "failure"
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}



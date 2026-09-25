package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Google endpoints (overridden in tests).
var (
	driveTokenURL = "https://oauth2.googleapis.com/token"
	driveAPI      = "https://www.googleapis.com"
)

// Drive stores backups in one Google Drive folder with the drive.file
// scope (WhaCP only sees files it created). Every file carries its key in
// appProperties.whacp_key, which is how it is found again.
type Drive struct {
	R      Remote
	Client *http.Client
	// ChunkSize is the resumable upload chunk (a multiple of 256 KiB).
	ChunkSize int64

	mu    sync.Mutex
	token string
	until time.Time
}

// NewDrive returns a client for a validated remote.
func NewDrive(r Remote) *Drive {
	return &Drive{R: r, Client: &http.Client{Timeout: 30 * time.Minute}, ChunkSize: 8 << 20}
}

func (d *Drive) accessToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token != "" && time.Now().Before(d.until) {
		return d.token, nil
	}
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {d.R.ClientID}, "client_secret": {d.R.ClientSecret}, "refresh_token": {d.R.RefreshToken}}
	req, _ := http.NewRequestWithContext(ctx, "POST", driveTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := d.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Google sign-in failed: %v", err)
	}
	defer res.Body.Close()
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body)
	if res.StatusCode != 200 || body.AccessToken == "" {
		if body.Error == "invalid_grant" {
			return "", errors.New("Google no longer accepts the authorisation (revoked or expired); connect Google Drive again")
		}
		return "", fmt.Errorf("Google sign-in failed: %s %s", body.Error, body.Description)
	}
	d.token = body.AccessToken
	d.until = time.Now().Add(time.Duration(max(body.ExpiresIn-60, 30)) * time.Second)
	return d.token, nil
}

func (d *Drive) do(ctx context.Context, method, u string, body io.Reader, headers map[string]string) (*http.Response, error) {
	tok, err := d.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return d.Client.Do(req)
}

func apiError(res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		return fmt.Errorf("Google Drive: %s (HTTP %d)", e.Error.Message, res.StatusCode)
	}
	return fmt.Errorf("Google Drive: HTTP %d", res.StatusCode)
}

func (d *Drive) folder() string {
	if d.R.FolderID == "" {
		return "root"
	}
	return d.R.FolderID
}

type driveFile struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	AppProperties map[string]string `json:"appProperties"`
}

// files lists WhaCP files in the folder whose key starts with prefix.
func (d *Drive) files(ctx context.Context, prefix string, exact bool) ([]driveFile, error) {
	q := fmt.Sprintf("'%s' in parents and trashed = false", d.folder())
	if exact {
		q += fmt.Sprintf(" and appProperties has { key='whacp_key' and value='%s' }", strings.ReplaceAll(prefix, "'", `\'`))
	}
	var out []driveFile
	page := ""
	for i := 0; i < 1000; i++ {
		v := url.Values{"q": {q}, "fields": {"nextPageToken,files(id,name,appProperties)"}, "pageSize": {"1000"}, "supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"}}
		if page != "" {
			v.Set("pageToken", page)
		}
		res, err := d.do(ctx, "GET", driveAPI+"/drive/v3/files?"+v.Encode(), nil, nil)
		if err != nil {
			return nil, err
		}
		var body struct {
			Next  string      `json:"nextPageToken"`
			Files []driveFile `json:"files"`
		}
		if res.StatusCode != 200 {
			err = apiError(res)
			res.Body.Close()
			return nil, err
		}
		err = json.NewDecoder(res.Body).Decode(&body)
		res.Body.Close()
		if err != nil {
			return nil, err
		}
		for _, f := range body.Files {
			if k := f.AppProperties["whacp_key"]; k != "" && strings.HasPrefix(k, prefix) {
				out = append(out, f)
			}
		}
		if page = body.Next; page == "" {
			break
		}
	}
	return out, nil
}

// Put uploads (replacing any file with the same key) with a resumable
// upload session.
func (d *Drive) Put(ctx context.Context, key, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	old, err := d.files(ctx, key, true)
	if err != nil {
		return err
	}
	meta, _ := json.Marshal(map[string]any{"name": path.Base(key), "parents": []string{d.folder()}, "appProperties": map[string]string{"whacp_key": key}})
	res, err := d.do(ctx, "POST", driveAPI+"/upload/drive/v3/files?uploadType=resumable&supportsAllDrives=true", bytes.NewReader(meta), map[string]string{
		"Content-Type": "application/json; charset=UTF-8", "X-Upload-Content-Type": "application/octet-stream", "X-Upload-Content-Length": strconv.FormatInt(info.Size(), 10),
	})
	if err != nil {
		return err
	}
	session := res.Header.Get("Location")
	if res.StatusCode != 200 || session == "" {
		err = apiError(res)
		res.Body.Close()
		return err
	}
	res.Body.Close()
	size := info.Size()
	var off int64
	for {
		n := min(d.ChunkSize, size-off)
		hdr := map[string]string{"Content-Length": strconv.FormatInt(n, 10)}
		if size == 0 {
			hdr["Content-Range"] = "bytes */0"
		} else {
			hdr["Content-Range"] = fmt.Sprintf("bytes %d-%d/%d", off, off+n-1, size)
		}
		req, _ := http.NewRequestWithContext(ctx, "PUT", session, io.NewSectionReader(f, off, n))
		req.ContentLength = n
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := d.Client.Do(req)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
		res.Body.Close()
		switch {
		case res.StatusCode == 200 || res.StatusCode == 201:
			// Replace older copies only once the new one is complete.
			for _, o := range old {
				d.deleteID(ctx, o.ID)
			}
			return nil
		case res.StatusCode == 308:
			// The server says how much it has; continue from there (no
			// Range header means it has nothing yet).
			off = 0
			if r := res.Header.Get("Range"); r != "" {
				if i := strings.LastIndex(r, "-"); i >= 0 {
					end, _ := strconv.ParseInt(r[i+1:], 10, 64)
					off = end + 1
				}
			}
		default:
			return fmt.Errorf("Google Drive upload failed: HTTP %d", res.StatusCode)
		}
	}
}

func (d *Drive) Get(ctx context.Context, key string, w io.Writer) error {
	fs, err := d.files(ctx, key, true)
	if err != nil {
		return err
	}
	if len(fs) == 0 {
		return fmt.Errorf("%s is not in the Google Drive folder", key)
	}
	res, err := d.do(ctx, "GET", driveAPI+"/drive/v3/files/"+url.PathEscape(fs[0].ID)+"?alt=media&supportsAllDrives=true", nil, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return apiError(res)
	}
	_, err = io.Copy(w, res.Body)
	return err
}

func (d *Drive) deleteID(ctx context.Context, id string) error {
	res, err := d.do(ctx, "DELETE", driveAPI+"/drive/v3/files/"+url.PathEscape(id)+"?supportsAllDrives=true", nil, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 204 && res.StatusCode != 200 && res.StatusCode != 404 {
		return apiError(res)
	}
	return nil
}

func (d *Drive) Delete(ctx context.Context, key string) error {
	fs, err := d.files(ctx, key, true)
	if err != nil {
		return err
	}
	for _, f := range fs {
		if err := d.deleteID(ctx, f.ID); err != nil {
			return err
		}
	}
	return nil
}

func (d *Drive) List(ctx context.Context, prefix string) ([]string, error) {
	fs, err := d.files(ctx, prefix, false)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range fs {
		k := f.AppProperties["whacp_key"]
		if rest := strings.TrimPrefix(k, prefix); !strings.Contains(rest, "/") {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

// RemoveDir is a no-op: files live flat in one folder.
func (d *Drive) RemoveDir(ctx context.Context, prefix string) error { return nil }

// CreateFolder creates the backup folder (used when connecting, so the
// drive.file scope covers it) and returns its ID.
func (d *Drive) CreateFolder(ctx context.Context, name string) (string, error) {
	meta, _ := json.Marshal(map[string]any{"name": name, "mimeType": "application/vnd.google-apps.folder"})
	res, err := d.do(ctx, "POST", driveAPI+"/drive/v3/files?supportsAllDrives=true&fields=id", bytes.NewReader(meta), map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", apiError(res)
	}
	var f driveFile
	if err := json.NewDecoder(res.Body).Decode(&f); err != nil || f.ID == "" {
		return "", errors.New("Google Drive did not return the folder ID")
	}
	return f.ID, nil
}

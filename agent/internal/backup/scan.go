package backup

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// manifestMAC authenticates the parts of a manifest a restore relies on
// (where the backup belongs and which files, with their checksums, it is
// made of) with a key derived from the backup key. A backup found in remote
// storage is imported only with a valid MAC: someone who can write to the
// storage cannot move a backup to another website or change its files.
func manifestMAC(key []byte, m *Manifest) string {
	type macFile struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Database string `json:"database"`
		Size     int64  `json:"size"`
		SHA256   string `json:"sha256"`
		Unpacked int64  `json:"unpacked"`
	}
	in := struct {
		Version   int       `json:"version"`
		ID        string    `json:"id"`
		Kind      string    `json:"kind"`
		Domain    string    `json:"domain"`
		CreatedAt time.Time `json:"created_at"`
		Encrypted bool      `json:"encrypted"`
		KeyID     string    `json:"key_id"`
		Meta      string    `json:"meta"`
		Files     []macFile `json:"files"`
	}{m.Version, m.ID, m.Kind, m.Domain, m.CreatedAt, m.Encrypted, m.KeyID, string(m.Meta), nil}
	for _, f := range m.Files {
		in.Files = append(in.Files, macFile{f.Name, f.Kind, f.Database, f.Size, f.SHA256, f.Unpacked})
	}
	b, _ := json.Marshal(in)
	derive := hmac.New(sha256.New, key)
	derive.Write([]byte("whacp-manifest-mac-v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write(b)
	return hex.EncodeToString(mac.Sum(nil))
}

// maxManifest bounds a manifest read from remote storage.
const maxManifest = 1 << 20

// candidateManifests lists where manifests can be below base, for storages
// that list folder entries one level at a time (list returns full keys of
// the entries directly inside a folder).
func candidateManifests(ctx context.Context, base string, list func(ctx context.Context, prefix string) ([]string, error)) ([]string, error) {
	var out []string
	ids, err := list(ctx, base+"server/")
	if err != nil {
		return nil, err
	}
	for _, k := range ids {
		if idRe.MatchString(path.Base(k)) {
			out = append(out, k+"/manifest.json")
		}
	}
	domains, err := list(ctx, base+"sites/")
	if err != nil {
		return nil, err
	}
	for _, d := range domains {
		if validate.Domain("domain", path.Base(d)) != nil {
			continue
		}
		ids, err := list(ctx, d+"/")
		if err != nil {
			return nil, err
		}
		for _, k := range ids {
			if idRe.MatchString(path.Base(k)) {
				out = append(out, k+"/manifest.json")
			}
		}
	}
	return out, nil
}

// Manifests returns the manifest keys below base.
func (s *S3) Manifests(ctx context.Context, base string) ([]string, error) {
	keys, err := s.List(ctx, base) // recursive for S3
	return withSuffix(keys, "/manifest.json"), err
}

// Manifests returns the manifest keys below base.
func (d *Drive) Manifests(ctx context.Context, base string) ([]string, error) {
	fs, err := d.files(ctx, base, false)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, f := range fs {
		if k := f.AppProperties["whacp_key"]; strings.HasPrefix(k, base) {
			keys = append(keys, k)
		}
	}
	return withSuffix(keys, "/manifest.json"), nil
}

// Manifests returns the manifest keys below base (SFTP/FTP list files and
// folders alike).
func (f *fileRemote) Manifests(ctx context.Context, base string) ([]string, error) {
	return candidateManifests(ctx, base, f.List)
}

// Manifests returns the manifest keys below base.
func (l *localDir) Manifests(ctx context.Context, base string) ([]string, error) {
	root, err := l.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dirs := func(_ context.Context, prefix string) ([]string, error) {
		dir := strings.TrimSuffix(prefix, "/")
		entries, err := fs.ReadDir(root.FS(), dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var out []string
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, path.Join(dir, e.Name()))
			}
		}
		return out, nil
	}
	return candidateManifests(ctx, base, dirs)
}

func withSuffix(keys []string, suffix string) []string {
	var out []string
	for _, k := range keys {
		if strings.HasSuffix(k, suffix) {
			out = append(out, k)
		}
	}
	return out
}

// Found is a backup found in remote storage.
type Found struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Domain    string    `json:"domain,omitempty"`
	Label     string    `json:"label,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"`
	Manifest  *Manifest `json:"manifest,omitempty"`
	// Skipped says why the backup was not imported ("" when it was).
	Skipped string `json:"skipped,omitempty"`
}

// ImportRemote finds the backups in remote storage and makes those this
// server can trust restorable here: encrypted with this server's backup key
// and carrying a valid manifest MAC, stored where their manifest says.
// Their manifests are written to the local backup folders (marked as kept
// in remote storage only); backups already known here are left as they are.
func (s Service) ImportRemote(ctx context.Context, remote *Remote) ([]Found, error) {
	key, err := s.key()
	if err != nil {
		return nil, fmt.Errorf("the backup key could not be loaded: %v", err)
	}
	st := remote.Open(s.Env)
	lister, ok := st.(interface {
		Manifests(ctx context.Context, base string) ([]string, error)
	})
	if !ok {
		return nil, fmt.Errorf("this storage cannot be searched")
	}
	base := remote.Key("")
	if base != "" && !strings.HasSuffix(base, "/") {
		base += "/"
	}
	keys, err := lister.Manifests(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("the storage could not be listed: %v", err)
	}
	out := []Found{}
	for _, k := range keys {
		if len(out) >= 5000 {
			break
		}
		var buf bytes.Buffer
		if err := st.Get(ctx, k, &limitWriter{w: &buf, n: maxManifest}); err != nil {
			continue // a folder without a manifest, or not readable
		}
		var m Manifest
		if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
			out = append(out, Found{Skipped: k + ": not a WhaCP manifest"})
			continue
		}
		f := Found{ID: m.ID, Kind: m.Kind, Domain: m.Domain, Label: m.Label, CreatedAt: m.CreatedAt, Size: m.Size}
		switch {
		case !idRe.MatchString(m.ID) || (m.Kind != "site" && m.Kind != "server") || (m.Kind == "site" && validate.Domain("domain", m.Domain) != nil):
			f.Skipped = "not a valid WhaCP backup"
		case remoteKey(remote, &m)+"/manifest.json" != k:
			f.Skipped = "stored somewhere other than its manifest says"
		case !m.Encrypted:
			f.Skipped = "not encrypted: only encrypted backups are imported"
		case m.KeyID != KeyID(key):
			f.Skipped = "encrypted with another backup key (" + m.KeyID + ")"
		case m.Mac == "" || !hmac.Equal([]byte(m.Mac), []byte(manifestMAC(key, &m))):
			f.Skipped = "its manifest is not authenticated by this backup key (made before 1.0.0, or changed)"
		}
		if f.Skipped == "" {
			for _, e := range m.Files {
				if !nameRe.MatchString(e.Name) {
					f.Skipped = "not a valid WhaCP backup"
				}
			}
		}
		if f.Skipped != "" {
			out = append(out, f)
			continue
		}
		dir := s.dir(m.Domain, m.ID)
		if s.Env.Root.Exists(filepath.Join(dir, "manifest.json")) {
			if local, _, err := s.Load(m.Domain, m.ID); err == nil {
				f.Manifest = local
			}
			out = append(out, f)
			continue
		}
		m.Local = false
		m.Remote = &RemoteRef{Type: remote.Type, Endpoint: remote.Endpoint, Bucket: remote.Bucket, Key: strings.TrimSuffix(k, "/manifest.json")}
		if remote.Type != "s3" {
			m.Remote.Endpoint = remote.Describe()
		}
		if _, err := s.Env.Writer.MkdirAll(dir, 0o700, agentfs.RootOwner); err != nil {
			return nil, err
		}
		real, err := s.real(dir)
		if err != nil {
			return nil, err
		}
		if err := writeManifest(real, &m); err != nil {
			os.Remove(real)
			return nil, err
		}
		f.Manifest = &m
		out = append(out, f)
	}
	return out, nil
}

// limitWriter fails once more than n bytes are written.
type limitWriter struct {
	w io.Writer
	n int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		return 0, fmt.Errorf("manifest too large")
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}

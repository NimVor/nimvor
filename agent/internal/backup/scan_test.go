package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Recovery onto a new server: with the old server's backup key, the
// backups in remote storage are found and restored; backups this server
// cannot trust are listed with the reason and not imported.
func TestRecoverOntoANewServer(t *testing.T) {
	ctx := context.Background()
	f, srv := newFakeS3(t)
	remote := &Remote{Endpoint: srv.URL, Bucket: "bucket-1", AccessKey: "AK", SecretKey: "SK", PathStyle: true, Prefix: "srv1"}
	if err := remote.Validate(); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	keep := false

	// The old server: an encrypted and an unencrypted website backup and a
	// server backup, kept in remote storage only.
	h1, s1 := siteHost(t)
	cred1, _, _ := h1.Env.Credential("example")
	enc, err := s1.CreateSite(ctx, SiteRequest{Domain: "example.com", User: "example", Root: "/home/nimvor/example.com", Files: true, Databases: []string{"shop"}, Encrypt: true, Remote: remote, KeepLocal: &keep}, cred1, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if enc.Mac == "" {
		t.Fatal("encrypted backup without a manifest MAC")
	}
	plain, err := s1.CreateSite(ctx, SiteRequest{Domain: "example.com", User: "example", Root: "/home/nimvor/example.com", Files: true, Remote: remote, KeepLocal: &keep}, cred1, &log)
	if err != nil {
		t.Fatal(err)
	}
	server, err := s1.CreateServer(ctx, true, "nightly", remote, false, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}

	// Someone with write access to the storage moves the encrypted backup
	// to another website: once with its manifest unchanged, once with the
	// domain in the manifest changed to match.
	prefix := "srv1/sites/example.com/" + enc.ID + "/"
	for k, v := range f.objects {
		if strings.HasPrefix(k, prefix) {
			rest := strings.TrimPrefix(k, prefix)
			f.objects["srv1/sites/moved.test/"+enc.ID+"/"+rest] = append([]byte{}, v...)
			if rest == "manifest.json" {
				v = bytes.Replace(v, []byte(`"domain": "example.com"`), []byte(`"domain": "forged.test"`), 1)
			}
			f.objects["srv1/sites/forged.test/"+enc.ID+"/"+rest] = append([]byte{}, v...)
		}
	}

	// A new server without the old key trusts nothing.
	h3, s3 := siteHost(t)
	_ = h3
	found, err := s3.ImportRemote(ctx, remote)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range found {
		if b.Skipped == "" {
			t.Fatalf("imported without the key: %+v", b)
		}
	}

	// The new server with the old key.
	h2, s2 := siteHost(t)
	oldKey, _ := s1.real(s1.keyPath())
	newKey, _ := s2.real(s2.keyPath())
	b, _ := os.ReadFile(oldKey)
	os.MkdirAll(filepath.Dir(newKey), 0o700)
	if err := os.WriteFile(newKey, b, 0o600); err != nil {
		t.Fatal(err)
	}
	found, err = s2.ImportRemote(ctx, remote)
	if err != nil {
		t.Fatal(err)
	}
	has := func(domain, id, skipped string) bool {
		for _, b := range found {
			if b.Domain == domain && b.ID == id && strings.Contains(b.Skipped, skipped) && (skipped != "" || (b.Skipped == "" && b.Manifest != nil)) {
				return true
			}
		}
		return false
	}
	for _, c := range []struct{ domain, id, skipped, what string }{
		{"example.com", enc.ID, "", "encrypted website backup imported"},
		{"", server.ID, "", "server backup imported"},
		{"example.com", plain.ID, "not encrypted", "unencrypted backup refused"},
		{"example.com", enc.ID, "somewhere other than its manifest says", "backup moved to another website refused"},
		{"forged.test", enc.ID, "not authenticated", "manifest changed to another website refused"},
	} {
		if !has(c.domain, c.id, c.skipped) {
			t.Errorf("%s: not found in %+v", c.what, found)
		}
	}
	if h2.Exists("var/backups/nimvor/sites/forged.test") || h2.Exists("var/backups/nimvor/sites/moved.test") {
		t.Error("an untrusted backup was recorded")
	}

	// The imported backup restores on the new server from the storage.
	cred2, _, _ := h2.Env.Credential("example")
	if _, err := s2.Restore(ctx, RestoreRequest{Domain: "example.com", User: "example", Root: "/home/nimvor/example.com", ID: enc.ID, Files: true, Databases: map[string]string{"shop": "shop"}, Remote: remote}, cred2, &log); err != nil {
		t.Fatalf("restore on the new server: %v\n%s", err, log.String())
	}
	if !h2.Runner.Called("tar -C " + filepath.Join(h2.Base, "home/nimvor/example.com") + " --no-same-owner -xf -") {
		t.Errorf("files not restored: %v", h2.Runner.Calls)
	}

	// Searching again changes nothing and still lists the backup.
	again, _ := s2.ImportRemote(ctx, remote)
	var m Manifest
	raw, _ := os.ReadFile(filepath.Join(h2.Base, "var/backups/nimvor/sites/example.com", enc.ID, "manifest.json"))
	json.Unmarshal(raw, &m)
	if len(again) != len(found) || m.Local || m.Remote == nil || m.Mac != enc.Mac {
		t.Fatalf("second search: %d vs %d, manifest %+v", len(again), len(found), m)
	}
}

// Storages listing one folder level at a time are searched two levels
// deep (server/<id>, sites/<domain>/<id>).
func TestCandidateManifests(t *testing.T) {
	tree := map[string][]string{
		"p/server/":            {"p/server/20260901T120000Z-abcdef", "p/server/junk"},
		"p/sites/":             {"p/sites/example.com", "p/sites/bad domain"},
		"p/sites/example.com/": {"p/sites/example.com/20260901T120000Z-012345"},
	}
	list := func(_ context.Context, prefix string) ([]string, error) { return tree[prefix], nil }
	got, err := candidateManifests(context.Background(), "p/", list)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"p/server/20260901T120000Z-abcdef/manifest.json", "p/sites/example.com/20260901T120000Z-012345/manifest.json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v", got)
	}
}

// The MAC of a manifest with meta (the panel always sends some) still
// matches after the manifest is written (indented) and read back, also
// with characters encoding/json escapes; manifests made before this fix
// from compact meta without such characters verify too.
func TestManifestMACSurvivesStorage(t *testing.T) {
	h, s := siteHost(t)
	cred, _, _ := h.Env.Credential("example")
	var log bytes.Buffer
	for _, meta := range []string{`{"website":{"domain":"example.com","x":"<a&b>"},"panel_version":1}`, `{"b":1,"a":[1,2]}`} {
		m, err := s.CreateSite(context.Background(), SiteRequest{Domain: "example.com", User: "example", Root: "/home/nimvor/example.com", Files: true, Encrypt: true, Meta: json.RawMessage(meta)}, cred, &log)
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := s.Load("example.com", m.ID)
		if err != nil {
			t.Fatal(err)
		}
		key, _ := s.key()
		if got.Mac != manifestMAC(key, got) {
			t.Errorf("meta %s: the stored manifest does not verify", meta)
		}
	}
	// Before the fix the MAC covered the meta bytes as sent.
	key, _ := s.key()
	old := &Manifest{Version: 1, ID: "20260901T120000Z-abcdef", Kind: "site", Domain: "example.com", Encrypted: true, KeyID: KeyID(key), Meta: json.RawMessage(`{"website":{"domain":"example.com"},"panel_version":1}`)}
	old.Mac = manifestMAC(key, old)
	b, _ := json.MarshalIndent(old, "", "  ")
	var back Manifest
	json.Unmarshal(b, &back)
	if back.Mac != manifestMAC(key, &back) {
		t.Error("a manifest made before the fix does not verify")
	}
}

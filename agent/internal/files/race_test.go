package files

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestRaceTOCTOUFileManager is the concurrency case of security item 181:
// the "check a path, then do a privileged operation on it" pattern must not
// be defeatable by swapping the path for a symbolic link in between. While
// several goroutines run privileged file-manager operations (chmod, write,
// delete, read) on a path inside the website, other goroutines repeatedly
// swap that path between a regular file and symlinks pointing at a secret
// outside the website and at /etc. After the storm nothing outside the site
// may have been read back, created, modified, deleted or had its mode
// changed. The file manager resolves every path through an os.Root handle,
// which never traverses a symlink out of the root, so each racing operation
// either works on the in-site file or fails safely.
//
// Run with -race for the strongest signal:
//
//	go test -race -run TestRaceTOCTOUFileManager ./internal/files
func TestRaceTOCTOUFileManager(t *testing.T) {
	h, r, root := setup(t)

	// The victims outside the website.
	secret := filepath.Join(h.Base, "etc/secret") // seeded by setup: mode 0600, "root only"
	outside := filepath.Join(h.Base, "home/whacp/other.test")
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "data.txt"), []byte("other-site-data"), 0o600)

	target := filepath.Join(root, "public_html", "target")
	os.WriteFile(target, []byte("in-site"), 0o644)

	before := snapshot(t, h.Base, root)
	if before[secret] == "" || before[filepath.Join(outside, "data.txt")] == "" {
		t.Fatal("outside fingerprint missing the secrets (test would be meaningless)")
	}

	callAction := func(name string, params map[string]any) (any, error) {
		params["domain"] = "site.test"
		b, _ := json.Marshal(params)
		a, err := r.Lookup(name)
		if err != nil {
			return nil, err
		}
		return a.Handler(context.Background(), b)
	}

	const rounds = 400
	stop := make(chan struct{})
	var swappers, operators sync.WaitGroup

	// One swapper churns public_html/target between a regular file and
	// symlinks pointing outside the website, until the operators are done.
	// It is a single goroutine, and it recreates the regular file with
	// O_CREATE|O_EXCL (which refuses to open or follow an existing symlink),
	// so the test itself never writes through a link — only the product code
	// under test ever touches the racing path through its own resolver.
	links := []string{secret, "/etc", outside, filepath.Join(outside, "data.txt")}
	swappers.Add(1)
	go func() {
		defer swappers.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			os.Remove(target)
			os.Symlink(links[i%len(links)], target)
			i++
			os.Remove(target)
			if f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
				f.WriteString("in-site")
				f.Close()
			}
		}
	}()

	// Operators: run privileged operations against the racing path for a
	// bounded number of rounds.
	ops := []func(){
		func() { callAction("files.chmod", map[string]any{"path": "public_html/target", "mode": "0777"}) },
		func() {
			callAction("files.write", map[string]any{"path": "public_html/target", "content": "pwned-by-race"})
		},
		func() { callAction("files.read", map[string]any{"path": "public_html/target"}) },
		func() { callAction("files.delete", map[string]any{"paths": []string{"public_html/target"}}) },
		func() {
			callAction("files.rename", map[string]any{"from": "public_html/target", "to": "public_html/moved"})
			callAction("files.rename", map[string]any{"from": "public_html/moved", "to": "public_html/target"})
		},
	}
	for _, op := range ops {
		operators.Add(1)
		go func(op func()) {
			defer operators.Done()
			for i := 0; i < rounds; i++ {
				op()
			}
		}(op)
	}

	operators.Wait() // all privileged operations have run
	close(stop)      // tell the swappers to finish
	swappers.Wait()

	// Restore a clean regular file so the final snapshot of the site is sane
	// (we only assert on the outside).
	os.Remove(target)

	after := snapshot(t, h.Base, root)
	for p, v := range before {
		if after[p] != v {
			t.Errorf("outside the site, %s changed (%q → %q)", p, v, after[p])
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			t.Errorf("outside the site, %s was created", p)
		}
	}
}

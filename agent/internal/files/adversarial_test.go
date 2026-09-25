package files

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostile paths that must never reach outside the website root.
var hostile = []string{
	"../", "../../", "../../../etc/passwd", "..", "/etc/passwd", "/root", "/etc", "/proc/self/environ",
	"/sys/kernel", "/dev/null", "/tmp/../etc", "public_html/../../other.test/secret.txt",
	"%2e%2e/%2e%2e/etc/passwd", "%252e%252e%252fetc%252fpasswd", "..%2f..%2fetc%2fpasswd", "public_html/\x00../../etc/passwd",
	"public_html/etc-link/passwd", "public_html/secret-link", "public_html/other-link/secret.txt", "public_html/other-link",
	"./../other.test", "public_html/./../../../etc/shadow",
}

// snapshot fingerprints everything outside the site root.
func snapshot(t *testing.T, base, site string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || strings.HasPrefix(p, site+"/") || p == site {
			return nil
		}
		info, _ := os.Lstat(p)
		sum := ""
		if info != nil && info.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			h := sha256.Sum256(b)
			sum = hex.EncodeToString(h[:])
		}
		if info != nil {
			out[p] = info.Mode().String() + sum
		}
		return nil
	})
	return out
}

// Every file-manager action, called with every hostile path, neither reads
// nor changes anything outside the website.
func TestAdversarialPathsForEveryAction(t *testing.T) {
	h, r, root := setup(t)
	other := filepath.Join(h.Base, "home/whacp/other.test")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "secret.txt"), []byte("other site"), 0o600)
	os.Symlink(other, filepath.Join(root, "public_html/other-link"))
	before := snapshot(t, h.Base, root)
	if _, ok := before[filepath.Join(h.Base, "etc/secret")]; !ok || before[filepath.Join(other, "secret.txt")] == "" {
		t.Fatal("the outside fingerprint misses the secrets (test would be meaningless)")
	}
	if res, err := do(t, r, "files.read", map[string]any{"path": "public_html/index.php"}); err != nil || !strings.Contains(fmtAny(res), "echo") {
		t.Fatalf("control read failed: %v %v", res, err)
	}
	data := base64.StdEncoding.EncodeToString([]byte("pwned"))
	leaked := func(res any) bool {
		s := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(fmtAny(res), " ", "")))
		return strings.Contains(s, "rootonly") || strings.Contains(s, "othersite") || strings.Contains(s, "root:x:0:0")
	}
	for _, p := range hostile {
		calls := map[string]map[string]any{
			"files.list":     {"path": p},
			"files.read":     {"path": p},
			"files.download": {"path": p},
			"files.search":   {"path": p, "query": "root"},
			"files.write":    {"path": p, "content": "pwned"},
			"files.upload":   {"path": p, "data": data, "final": true},
			"files.mkdir":    {"path": p + "/newdir"},
			"files.rename":   {"from": p, "to": "public_html/moved"},
			"files.copy":     {"paths": []string{p}, "dest": "public_html"},
			"files.delete":   {"paths": []string{p}},
			"files.compress": {"paths": []string{p}, "dest": "public_html/out.zip"},
			"files.extract":  {"path": p, "dest": "public_html"},
			"files.chmod":    {"path": p, "mode": "0777"},
		}
		// Moves and copies into a hostile destination too.
		calls["files.rename#to"] = map[string]any{"from": "public_html/index.php", "to": p}
		calls["files.copy#to"] = map[string]any{"paths": []string{"public_html/index.php"}, "dest": p}
		for name, params := range calls {
			action := strings.Split(name, "#")[0]
			res, err := do(t, r, action, params)
			if err == nil && leaked(res) {
				t.Errorf("%s %q returned data from outside the site", name, p)
			}
		}
	}
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

func fmtAny(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	default:
		b, _ := jsonMarshal(x)
		s := string(b)
		// Downloads are base64.
		if strings.Contains(s, `"data":"`) {
			if i := strings.Index(s, `"data":"`); i >= 0 {
				rest := s[i+8:]
				if j := strings.Index(rest, `"`); j >= 0 {
					if d, err := base64.StdEncoding.DecodeString(rest[:j]); err == nil {
						s += string(d)
					}
				}
			}
		}
		return s
	}
}

var jsonMarshal = func(v any) ([]byte, error) { return json.Marshal(v) }

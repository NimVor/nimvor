package tlsprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestDefaultsForNewAndExistingServers(t *testing.T) {
	h := testhost.New(t)
	if p := Load(h.Env); p != Default || !strings.Contains(Render(p), "ECDHE-ECDSA-AES128-GCM-SHA256") {
		t.Fatalf("a new server gets Mozilla intermediate: %+v", p)
	}
	// A snippet written before (no saved profile) is reported as it is.
	f := filepath.Join(h.Base, strings.TrimPrefix(Path(h.Env), "/"))
	os.MkdirAll(filepath.Dir(f), 0o755)
	os.WriteFile(f, []byte(Render(Legacy)), 0o644)
	if p := Load(h.Env); p != Legacy {
		t.Fatalf("existing snippet: %+v", p)
	}
	for _, p := range []Profile{Default, Legacy, {Protocols: "tls13", Ciphers: "intermediate"}} {
		if strings.Contains(Render(p), "TLSv1 ") || strings.Contains(Render(p), "TLSv1.1") {
			t.Errorf("old protocol in %+v", p)
		}
	}
}

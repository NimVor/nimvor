package firewall

import (
	"path/filepath"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

func TestDetectUFW(t *testing.T) {
	abs, _ := filepath.Abs("../../tests/fixtures/ubuntu-host")
	root, _ := filesystem.NewRoot(abs)
	st := Detect(root, platform.Detect(root).Paths())
	if st.Backend != "ufw" || !st.Enabled {
		t.Fatalf("status = %+v", st)
	}
	if ufwEnabled([]byte("ENABLED=no\n")) {
		t.Error("ENABLED=no must be disabled")
	}
}

package sysmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestGraphicalLoginOff(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	set, _ := r.Lookup("server.graphical_set")
	if _, err := set.Handler(context.Background(), json.RawMessage(`{"enabled":false}`)); err == nil {
		t.Fatal("accepted without a display manager")
	}
	os.Symlink("/usr/lib/systemd/system/gdm.service", filepath.Join(h.Base, "etc/systemd/system/display-manager.service"))
	if _, err := set.Handler(context.Background(), json.RawMessage(`{"enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"systemctl set-default -- multi-user.target", "systemctl stop -- gdm.service"} {
		if !h.Runner.Called(c) {
			t.Fatalf("%s missing: %v", c, h.Runner.Calls)
		}
	}
	if _, err := set.Handler(context.Background(), json.RawMessage(`{"enabled":true}`)); err != nil || !h.Runner.Called("systemctl set-default -- graphical.target") {
		t.Fatalf("on: %v %v", err, h.Runner.Calls)
	}
}

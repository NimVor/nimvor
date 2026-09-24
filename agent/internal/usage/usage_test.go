package usage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestUsage(t *testing.T) {
	h := testhost.New(t)
	root := filepath.Join(h.Base, "home/whacp/shop.test")
	os.MkdirAll(filepath.Join(root, "public_html/a"), 0o755)
	os.WriteFile(filepath.Join(root, "public_html/index.php"), make([]byte, 1000), 0o644)
	os.WriteFile(filepath.Join(root, "public_html/a/b.txt"), make([]byte, 24), 0o644)
	os.WriteFile(filepath.Join(h.Base, "big"), make([]byte, 1<<20), 0o644)
	os.Symlink(filepath.Join(h.Base, "big"), filepath.Join(root, "public_html/link")) // not followed
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("sites.usage")
	res, err := a.Handler(context.Background(), json.RawMessage(`{"domains":["shop.test","missing.test"]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := res.([]Result)
	if got[0].Bytes != 1024 || got[0].Files != 2 || !got[1].NotFound {
		t.Fatalf("usage = %+v", got)
	}
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"domains":["../etc"]}`)); err == nil {
		t.Error("invalid domain accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	if _, _, partial, _ := Measure(ctx, root); !partial {
		t.Error("expected a partial result after the deadline")
	}
}

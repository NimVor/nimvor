package discovery

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

// An aaPanel-style server keeps Nginx and PHP under /www/server; a
// HestiaCP-style vhost is included from /home and uses an upstream.
func TestOtherPanelLayout(t *testing.T) {
	abs, _ := filepath.Abs("../../tests/fixtures/aapanel-host")
	root, _ := filesystem.NewRoot(abs)
	svc := Service{Root: root, Platform: platform.Detect(root), Runner: &execx.FakeRunner{}, Now: time.Now}
	res := svc.Websites(context.Background())

	sites := map[string]Website{}
	for _, w := range res.Websites {
		sites[w.Domain] = w
	}
	blog, ok := sites["blog.test"]
	if !ok {
		t.Fatalf("aaPanel site not discovered: %+v (warnings %v)", res.Websites, res.Warnings)
	}
	if blog.ManagedBy != "aaPanel" || blog.PHP == nil || blog.PHP.Version != "8.3" || !blog.PHP.Resolved {
		t.Errorf("blog = %+v php=%+v", blog, blog.PHP)
	}
	alice, ok := sites["alice.test"]
	if !ok {
		t.Fatalf("site included from /home not discovered")
	}
	if alice.PHP == nil || alice.PHP.Listen != "unix:/tmp/php-cgi-83.sock" || !alice.PHP.Resolved {
		t.Errorf("upstream not resolved: %+v", alice.PHP)
	}
}

package wordpress

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// WP-CLI is pinned to one release: the agent downloads exactly this file
// and refuses anything whose SHA-512 differs from the one written here, so
// a compromised download location cannot hand the agent other code. It is
// kept root-owned and only ever run by PHP as a website account.
const WPCLIVersion = "2.12.0"

// wpcliSHA512 is the pinned checksum (a variable only so tests can serve
// their own file).
var wpcliSHA512 = "be928f6b8ca1e8dfb9d2f4b75a13aa4aee0896f8a9a0a1c45cd5d2c98605e6172e6d014dda2e27f88c98befc16c040cbb2bd1bfa121510ea5cdf5f6a30fe8832"

const (
	// WPCLIPath is where the verified copy is kept.
	WPCLIPath   = "/usr/local/lib/whacp/wp-cli.phar"
	maxPharSize = 32 << 20
)

// Source says where WP-CLI is downloaded from (tests point it elsewhere).
type Source struct {
	URL    string
	Client *http.Client
}

// DefaultSource is WP-CLI's GitHub release.
var DefaultSource = Source{
	URL:    "https://github.com/wp-cli/wp-cli/releases/download/v" + WPCLIVersion + "/wp-cli-" + WPCLIVersion + ".phar",
	Client: &http.Client{Timeout: 3 * time.Minute},
}

var pharMu sync.Mutex

func sha512Hex(b []byte) string {
	s := sha512.Sum512(b)
	return hex.EncodeToString(s[:])
}

// verified remembers the copy last found intact (size and change time).
var verified struct {
	size  int64
	ctime syscall.Timespec
}

// pharReady says whether the verified WP-CLI copy is in place: a regular
// root-owned file nobody else can write, with the pinned checksum.
func pharReady(env *host.Env) bool {
	p, err := env.Root.Path(WPCLIPath)
	if err != nil {
		return false
	}
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	st, ok := fi.Sys().(*syscall.Stat_t)
	if err != nil || !ok || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o022 != 0 || (os.Geteuid() == 0 && st.Uid != 0) || fi.Size() > maxPharSize {
		return false
	}
	verMu.Lock()
	defer verMu.Unlock()
	if verified.size == fi.Size() && verified.ctime == st.Ctim {
		return true
	}
	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil || hex.EncodeToString(h.Sum(nil)) != wpcliSHA512 {
		return false
	}
	verified.size, verified.ctime = fi.Size(), st.Ctim
	return true
}

var verMu sync.Mutex

// ensurePhar downloads and verifies WP-CLI once.
func ensurePhar(ctx context.Context, env *host.Env, src Source) error {
	pharMu.Lock()
	defer pharMu.Unlock()
	if pharReady(env) {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return err
	}
	res, err := src.Client.Do(req)
	if err != nil {
		return fmt.Errorf("WP-CLI could not be downloaded: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("WP-CLI could not be downloaded (HTTP %d)", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, maxPharSize+1))
	if err != nil || len(b) > maxPharSize {
		return fmt.Errorf("WP-CLI could not be downloaded")
	}
	if got := sha512Hex(b); got != wpcliSHA512 {
		return fmt.Errorf("the WP-CLI download does not match the pinned checksum (got %.16s…); nothing was installed", got)
	}
	set := env.Changes("wp-cli")
	set.Mkdir("/usr/local/lib/whacp", 0o755, agentfs.RootOwner)
	set.Write(WPCLIPath, b, 0o644, agentfs.RootOwner)
	if _, err := set.Commit(nil); err != nil {
		return fmt.Errorf("WP-CLI could not be saved: %v", err)
	}
	env.Log.Info("installed WP-CLI", "version", WPCLIVersion, "path", WPCLIPath)
	return nil
}

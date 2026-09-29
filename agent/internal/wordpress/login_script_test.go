package wordpress

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The one-time sign-in file refuses anything but HTTPS GET requests before
// loading WordPress, and deletes itself on the first request either way.
func TestLoginScriptOnlyOverHTTPS(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php is not installed")
	}
	run := func(env ...string) (string, bool) {
		dir := t.TempDir()
		exp := time.Now().Add(time.Minute).Unix()
		f := filepath.Join(dir, "whacp-login-x.php")
		os.WriteFile(f, []byte(fmt.Sprintf(loginScript, exp, exp, 1)), 0o600)
		cmd := exec.Command(php, f)
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
		out, _ := cmd.CombinedOutput()
		_, statErr := os.Stat(f)
		return string(out), os.IsNotExist(statErr)
	}
	for _, env := range [][]string{{"REQUEST_METHOD=GET"}, {"REQUEST_METHOD=GET", "HTTPS=off"}, {"REQUEST_METHOD=POST", "HTTPS=on"}} {
		out, gone := run(env...)
		if strings.Contains(out, "wp-load.php") || !gone {
			t.Errorf("%v: WordPress was loaded (%q) or the file kept (%v)", env, out, !gone)
		}
	}
	// Over HTTPS it goes on to load WordPress (missing here).
	if out, gone := run("REQUEST_METHOD=GET", "HTTPS=on"); !strings.Contains(out, "wp-load.php") || !gone {
		t.Fatalf("HTTPS request refused: %q", out)
	}
}

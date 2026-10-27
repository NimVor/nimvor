package http

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A bare number as the agent call's timeout is a time.Duration of that many
// nanoseconds: the call fails at once with "context deadline exceeded".
func TestAgentCallsNeverUseBareNumericTimeouts(t *testing.T) {
	bare := regexp.MustCompile(`\.Call\(.*, [0-9]+\)`)
	err := filepath.Walk("..", func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if bare.MatchString(line) && !strings.Contains(line, "time.") {
				t.Errorf("%s:%d passes a bare number as a timeout: %s", p, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServicesControlReachesTheAgent(t *testing.T) {
	s, _, sa := rolesEnvServerAgent(t, true)
	h := s.Handler()
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/services")
	rr := postForm(h, "/services/control", url.Values{"_token": {token}, "unit": {"nginx.service"}, "op": {"reload"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("services/control = %d: %s", rr.Code, rr.Body.String())
	}
	if !containsAction(sa.called(), "services.control") {
		t.Errorf("the agent was not asked: %v", sa.called())
	}
	flash := get(t, h, "/services", refreshCookie(admin, rr)).Body.String()
	if pageHas(flash, "could not be reached") {
		t.Errorf("the call failed: %s", visibleText(flash))
	}
}

// The panel never runs commands itself: every privileged or host operation is
// a named agent action.
func TestPanelNeverImportsOsExec(t *testing.T) {
	err := filepath.Walk("../..", func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), `"os/exec"`) {
			t.Errorf("%s imports os/exec", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

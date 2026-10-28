package cron

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

func TestDiscoverFixture(t *testing.T) {
	abs, _ := filepath.Abs("../../tests/fixtures/ubuntu-host")
	root, _ := filesystem.NewRoot(abs)
	res := Discover(root, platform.Detect(root).Paths())
	// crontab: 1, cron.d/certbot: 1 (ignored.dpkg-old skipped), deploy: 3
	if len(res.Jobs) != 5 {
		t.Fatalf("jobs = %d: %+v", len(res.Jobs), res.Jobs)
	}
	b, _ := json.Marshal(res)
	for _, secret := range []string{"S3cretPass", "abc123def"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("secret %q leaked", secret)
		}
	}
	var daily Job
	for _, j := range res.Jobs {
		if j.Schedule == "@daily" {
			daily = j
		}
	}
	if daily.User != "deploy" {
		t.Errorf("user crontab owner not applied: %+v", daily)
	}
}

func TestParseSystemFormat(t *testing.T) {
	jobs := Parse([]byte("MAILTO=root\n*/5 * * * * www-data php /x.php\n@reboot root /bin/true\nbroken line\n"), "/etc/crontab", "")
	if len(jobs) != 2 || jobs[0].User != "www-data" || jobs[0].Schedule != "*/5 * * * *" || jobs[1].Schedule != "@reboot" {
		t.Fatalf("jobs = %+v", jobs)
	}
}

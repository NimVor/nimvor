package logging

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := map[string]string{
		"mysqldump -u root -pS3cret db":              "S3cret",
		"mysql --password=hunter2 db":                "hunter2",
		"tool --token abcdef123":                     "abcdef123",
		"DB_PASSWORD=topsecret php artisan":          "topsecret",
		"curl https://user:pa55@example.com/x":       "pa55",
		"curl -H 'Authorization: Bearer eyJabc.def'": "eyJabc.def",
		"wget https://x.test/hook?token=zzz999&a=1":  "zzz999",
	}
	for in, secret := range cases {
		out := Redact(in)
		if strings.Contains(out, secret) {
			t.Errorf("Redact(%q) = %q still contains secret", in, out)
		}
		if !strings.Contains(out, Redacted) {
			t.Errorf("Redact(%q) = %q has no placeholder", in, out)
		}
	}
	if got := Redact("php /var/www/artisan schedule:run"); got != "php /var/www/artisan schedule:run" {
		t.Errorf("benign command changed: %q", got)
	}
}

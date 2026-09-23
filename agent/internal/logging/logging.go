// Package logging configures structured logging and secret redaction.
package logging

import (
	"io"
	"log/slog"
	"regexp"
	"strings"
)

// New returns a JSON slog logger writing to w at the given level.
func New(w io.Writer, level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl}))
}

// secretPatterns match common ways secrets appear inline in commands and
// configuration values (cron lines, systemd ExecStart, URLs, ...).
var secretPatterns = []*regexp.Regexp{
	// mysql -pSECRET / mysqldump -p'SECRET'
	regexp.MustCompile(`(\s-p)(\S+)`),
	// --password=SECRET, --pass SECRET, --token=..., --api-key ...
	regexp.MustCompile(`(?i)(--?(?:password|passwd|pass|pwd|token|secret|api[-_]?key|auth)(?:=|\s+))(\S+)`),
	// KEY=value environment assignments with sensitive names.
	regexp.MustCompile(`(?i)(\b[A-Z0-9_]*(?:PASSWORD|PASSWD|SECRET|TOKEN|API_KEY|APIKEY|ACCESS_KEY)[A-Z0-9_]*=)(\S+)`),
	// scheme://user:password@host
	regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+:)([^\s@]+)(@)`),
	// Authorization: Bearer xxx
	regexp.MustCompile(`(?i)(bearer\s+)([A-Za-z0-9._~+/=-]+)`),
	// query-string style secrets: ?token=... &key=...
	regexp.MustCompile(`(?i)([?&](?:token|key|secret|password|pass|auth)=)([^&\s]+)`),
}

// Redacted is the placeholder used for removed secrets.
const Redacted = "[REDACTED]"

// Redact removes likely secrets from s. It is intentionally aggressive:
// losing a little context is preferable to leaking a credential.
func Redact(s string) string {
	if s == "" {
		return s
	}
	for _, re := range secretPatterns {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			sub := re.FindStringSubmatch(m)
			if len(sub) < 3 {
				return m
			}
			out := sub[1] + Redacted
			if len(sub) > 3 {
				out += strings.Join(sub[3:], "")
			}
			return out
		})
	}
	return s
}

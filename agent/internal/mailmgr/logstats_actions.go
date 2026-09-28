package mailmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Plain mail logs, when the journal has none (rsyslog, Postfix maillog_file).
var mailLogFiles = []string{"/var/log/mail.log", "/var/log/maillog", "/var/log/postfix.log"}

// maxLogBytes bounds how much of a log file is read (the newest part).
const maxLogBytes = 256 << 20

func (e env) mailLog(ctx context.Context, since, until time.Time) ([]LogLine, string) {
	out, err := e.Runner.Run(ctx, "journalctl", "--no-pager", "-o", "json", "SYSLOG_FACILITY=2",
		"--since", since.UTC().Format("2006-01-02 15:04:05 UTC"), "--until", until.UTC().Format("2006-01-02 15:04:05 UTC"))
	if err == nil {
		if lines := ParseJournal(out); len(lines) > 0 {
			return lines, "journal"
		}
	}
	for _, f := range mailLogFiles {
		b, err := e.readTail(f, maxLogBytes)
		if err == nil && len(b) > 0 {
			return ParseSyslog(b, e.Now()), f
		}
	}
	return nil, ""
}

// readTail returns at most max bytes from the end of a file, starting at
// a line boundary.
func (e env) readTail(hostPath string, max int64) ([]byte, error) {
	real, err := e.Root.Path(hostPath)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", hostPath)
	}
	off := info.Size() - max
	if off < 0 {
		off = 0
	}
	b, err := io.ReadAll(io.NewSectionReader(f, off, info.Size()-off))
	if err != nil {
		return nil, err
	}
	if off > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b, nil
}

func registerLogStats(r *actions.Registry, e env) {
	r.Register(actions.Action{Name: "mail.report", Description: "Mail statistics and delivery records for a time range from Postfix's and Dovecot's logs: received, sent, delivered, deferred, bounced, rejected, logins; relayers (authenticated users, local accounts, clients) and sending domains; deliveries filtered by address and status.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Since   string `json:"since"`
			Until   string `json:"until"`
			Address string `json:"address"`
			Status  string `json:"status"`
			Limit   int    `json:"limit"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		now := e.Now()
		until, since := now, now.Add(-24*time.Hour)
		if in.Until != "" {
			t, err := time.Parse(time.RFC3339, in.Until)
			if err != nil {
				return nil, actions.InvalidParams("until must be an RFC 3339 time")
			}
			until = t
		}
		if in.Since != "" {
			t, err := time.Parse(time.RFC3339, in.Since)
			if err != nil {
				return nil, actions.InvalidParams("since must be an RFC 3339 time")
			}
			since = t
		}
		if !since.Before(until) || until.Sub(since) > 93*24*time.Hour {
			return nil, actions.InvalidParams("the range must be positive and at most 93 days")
		}
		switch in.Status {
		case "", "sent", "delivered", "deferred", "bounced", "rejected":
		default:
			return nil, actions.InvalidParams("status must be sent, delivered, deferred, bounced or rejected")
		}
		if len(in.Address) > 254 {
			return nil, actions.InvalidParams("address is too long")
		}
		lines, source := e.mailLog(ctx, since, until)
		rep := Analyze(lines, since, until, in.Address, in.Status, in.Limit)
		return map[string]any{"report": rep, "source": source, "since": since.UTC(), "until": until.UTC()}, nil
	}})
}

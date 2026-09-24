// Package logs exposes named log sources with tail, follow and download.
//
// Callers never pass file paths: they pass a source ID such as
// "site:example.com:access.log", "nginx:error" or "journal:nginx.service",
// which the agent maps to a fixed location.
package logs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/services"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

const (
	maxLines  = 5000
	maxFollow = 1 << 20
	chunk     = 2 << 20
)

// Source is one log.
type Source struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Group  string `json:"group"`
	Kind   string `json:"kind"` // file or journal
	Size   int64  `json:"size"`
	Exists bool   `json:"exists"`
	path   string
	unit   string
}

var logName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}\.log$`)

// Resolve maps a source ID to a Source (path or journal unit).
func Resolve(env *host.Env, id string) (Source, error) {
	parts := strings.SplitN(id, ":", 3)
	s := Source{ID: id, Kind: "file"}
	switch parts[0] {
	case "site":
		if len(parts) != 3 || validate.Domain("domain", parts[1]) != nil || !logName.MatchString(parts[2]) {
			return s, actions.InvalidParams("source: invalid website log")
		}
		s.path = filepath.Join(env.Cfg.SitesRoot, parts[1], "logs", parts[2])
		s.Label, s.Group = parts[2], parts[1]
	case "nginx":
		if len(parts) != 2 || (parts[1] != "access" && parts[1] != "error") {
			return s, actions.InvalidParams("source: invalid nginx log")
		}
		s.path, s.Label, s.Group = "/var/log/nginx/"+parts[1]+".log", "Nginx "+parts[1]+" log", "Web server"
	case "php":
		if len(parts) != 2 || validate.PHPVersion("v", parts[1]) != nil {
			return s, actions.InvalidParams("source: invalid PHP log")
		}
		s.path, s.Label, s.Group = "/var/log/php"+parts[1]+"-fpm.log", "PHP "+parts[1]+" FPM", "PHP"
	case "system":
		files := map[string]string{"auth": "/var/log/auth.log", "syslog": "/var/log/syslog", "mysql": "/var/log/mysql/error.log", "fail2ban": "/var/log/fail2ban.log", "ufw": "/var/log/ufw.log", "letsencrypt": "/var/log/letsencrypt/letsencrypt.log"}
		p, ok := files[safePart(parts, 1)]
		if !ok {
			return s, actions.InvalidParams("source: unknown system log")
		}
		s.path, s.Label, s.Group = p, filepath.Base(p), "System"
	case "journal":
		unit := safePart(parts, 1)
		if validate.Unit("unit", unit) != nil || (!services.Allowed(unit) && unit != "whacp-agent.service" && unit != "certbot.service" && !strings.HasPrefix(unit, "whacp-worker-")) {
			return s, actions.InvalidParams("source: this unit's journal is not available")
		}
		s.Kind, s.unit, s.Label, s.Group = "journal", unit, unit, "Services"
	default:
		return s, actions.InvalidParams("source: unknown log source")
	}
	if s.Kind == "file" {
		if info, err := env.Root.Stat(s.path); err == nil && info.Mode().IsRegular() {
			s.Exists, s.Size = true, info.Size()
		}
	} else {
		s.Exists = true
	}
	return s, nil
}

func safePart(p []string, i int) string {
	if len(p) > i {
		return p[i]
	}
	return ""
}

// Sources lists available logs (for a domain, or server-wide).
func Sources(ctx context.Context, env *host.Env, domain string) []Source {
	var ids []string
	if domain != "" {
		matches, _ := env.Root.Glob(filepath.Join(env.Cfg.SitesRoot, domain, "logs", "*.log"))
		for _, m := range matches {
			ids = append(ids, "site:"+domain+":"+filepath.Base(m))
		}
	} else {
		ids = append(ids, "nginx:access", "nginx:error")
		for _, v := range env.Discovery.PHP().Versions {
			ids = append(ids, "php:"+v.Version)
		}
		for _, k := range []string{"auth", "syslog", "mysql", "fail2ban", "ufw", "letsencrypt"} {
			ids = append(ids, "system:"+k)
		}
		for _, u := range []string{"whacp-agent.service", "nginx.service", "mariadb.service", "mysql.service", "redis-server.service", "cron.service", "fail2ban.service", "ssh.service"} {
			ids = append(ids, "journal:"+u)
		}
	}
	out := []Source{}
	for _, id := range ids {
		if s, err := Resolve(env, id); err == nil && (s.Exists || s.Kind == "journal") {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out
}

// TailResult is returned by logs.tail.
type TailResult struct {
	Lines   []string `json:"lines"`
	Offset  int64    `json:"offset"`
	Cursor  string   `json:"cursor,omitempty"`
	Rotated bool     `json:"rotated"`
	Size    int64    `json:"size"`
}

// Tail returns the last n lines, or (with follow) the lines written since
// offset/cursor. filter keeps lines containing the text (case-insensitive).
func Tail(ctx context.Context, env *host.Env, s Source, n int, filter string, follow bool, offset int64, cursor string) (*TailResult, error) {
	if n <= 0 || n > maxLines {
		n = 200
	}
	if s.Kind == "journal" {
		return tailJournal(ctx, env, s, n, filter, follow, cursor)
	}
	real, err := env.Root.Path(s.path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(real)
	if err != nil {
		return &TailResult{Lines: []string{}}, nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	res := &TailResult{Size: info.Size(), Lines: []string{}}
	if follow && offset >= 0 && offset <= info.Size() {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
		b, _ := io.ReadAll(io.LimitReader(f, maxFollow))
		// Only return complete lines; the rest is read next time.
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			b = b[:i+1]
		} else {
			b = nil
		}
		res.Offset = offset + int64(len(b))
		res.Lines = filterLines(splitLines(b), filter)
		return res, nil
	}
	res.Rotated = follow && offset > info.Size()
	lines, err := lastLines(f, info.Size(), n, filter)
	if err != nil {
		return nil, err
	}
	res.Lines, res.Offset = lines, info.Size()
	return res, nil
}

// lastLines reads backwards in blocks until it has n (matching) lines.
func lastLines(f *os.File, size int64, n int, filter string) ([]string, error) {
	const block = 64 << 10
	var buf []byte
	pos := size
	var lines []string
	for pos > 0 && len(lines) <= n && size-pos < 64<<20 {
		start := pos - block
		if start < 0 {
			start = 0
		}
		part := make([]byte, pos-start)
		if _, err := f.ReadAt(part, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		buf = append(part, buf...)
		pos = start
		all := splitLines(buf)
		if pos > 0 && len(all) > 0 {
			all = all[1:] // first line may be partial
		}
		lines = filterLines(all, filter)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func splitLines(b []byte) []string {
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}

func filterLines(lines []string, filter string) []string {
	if filter == "" {
		return lines
	}
	f := strings.ToLower(filter)
	out := lines[:0:0]
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l), f) {
			out = append(out, l)
		}
	}
	return out
}

var cursorRe = regexp.MustCompile(`^[A-Za-z0-9=;_-]{1,500}$`)

func tailJournal(ctx context.Context, env *host.Env, s Source, n int, filter string, follow bool, cursor string) (*TailResult, error) {
	args := []string{"--no-pager", "-o", "short-iso", "--show-cursor", "-u", s.unit}
	if follow && cursor != "" {
		if !cursorRe.MatchString(cursor) {
			return nil, actions.InvalidParams("cursor: invalid")
		}
		args = append(args, "--after-cursor="+cursor)
	} else {
		args = append(args, "-n", strconv.Itoa(n))
	}
	out, err := env.Runner.Run(ctx, "journalctl", args...)
	if err != nil && len(out) == 0 {
		return nil, actions.Failed("The journal could not be read.", "")
	}
	res := &TailResult{Lines: []string{}, Cursor: cursor}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var lines []string
	for sc.Scan() {
		l := sc.Text()
		if c, ok := strings.CutPrefix(l, "-- cursor: "); ok {
			res.Cursor = c
			continue
		}
		if l == "-- No entries --" {
			continue
		}
		lines = append(lines, l)
	}
	res.Lines = filterLines(lines, filter)
	return res, nil
}

// Register adds log actions.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "logs.sources", Description: "Available log sources (server-wide or for one website).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Domain != "" {
			if err := validate.Domain("domain", in.Domain); err != nil {
				return nil, host.Invalid(err)
			}
		}
		return Sources(ctx, env, in.Domain), nil
	}})
	r.Register(actions.Action{Name: "logs.tail", Description: "Last lines of a log, or new lines since an offset/cursor.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Source string `json:"source"`
			Lines  int    `json:"lines"`
			Filter string `json:"filter"`
			Follow bool   `json:"follow"`
			Offset int64  `json:"offset"`
			Cursor string `json:"cursor"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Filter) > 200 {
			return nil, actions.InvalidParams("filter: at most 200 characters")
		}
		s, err := Resolve(env, in.Source)
		if err != nil {
			return nil, err
		}
		return Tail(ctx, env, s, in.Lines, in.Filter, in.Follow, in.Offset, in.Cursor)
	}})
	r.Register(actions.Action{Name: "logs.download", Description: "Read a chunk of a log file (base64).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Source string `json:"source"`
			Offset int64  `json:"offset"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		s, err := Resolve(env, in.Source)
		if err != nil {
			return nil, err
		}
		if s.Kind != "file" || !s.Exists {
			return nil, actions.NotFound("This log cannot be downloaded.")
		}
		real, _ := env.Root.Path(s.path)
		f, err := os.Open(real)
		if err != nil {
			return nil, actions.NotFound("The log file is not readable.")
		}
		defer f.Close()
		buf := make([]byte, chunk)
		n, err := f.ReadAt(buf, in.Offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, actions.Failed(fmt.Sprintf("reading the log failed at offset %d", in.Offset), "")
		}
		return map[string]any{"data": base64.StdEncoding.EncodeToString(buf[:n]), "size": s.Size, "eof": in.Offset+int64(n) >= s.Size, "name": filepath.Base(s.path)}, nil
	}})
}

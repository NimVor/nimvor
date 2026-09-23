// Package cron discovers existing cron jobs (read-only).
//
// Commands are passed through secret redaction before they leave the agent,
// because cron lines frequently contain inline passwords.
package cron

import (
	"bufio"
	"bytes"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/logging"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Job is one cron entry.
type Job struct {
	Source   string `json:"source"`
	Line     int    `json:"line"`
	Schedule string `json:"schedule"`
	User     string `json:"user"`
	Command  string `json:"command"`
}

// Result lists discovered jobs.
type Result struct {
	Jobs     []Job    `json:"jobs"`
	Warnings []string `json:"warnings"`
}

var validCronDName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var validUser = regexp.MustCompile(`^[a-z_][a-z0-9_.-]*\$?$`)

// Discover reads the system crontab, /etc/cron.d and per-user crontabs.
func Discover(root filesystem.Root, paths platform.Paths) Result {
	res := Result{Jobs: []Job{}, Warnings: []string{}}
	if data, err := root.ReadFile(paths.SystemCrontab); err == nil {
		res.Jobs = append(res.Jobs, Parse(data, paths.SystemCrontab, "")...)
	}
	if entries, err := root.ReadDir(paths.CronDDir); err == nil {
		for _, e := range entries {
			// cron ignores files with dots or other characters in the name.
			if e.IsDir() || !validCronDName.MatchString(e.Name()) {
				continue
			}
			p := filepath.Join(paths.CronDDir, e.Name())
			if data, err := root.ReadFile(p); err == nil {
				res.Jobs = append(res.Jobs, Parse(data, p, "")...)
			}
		}
	}
	if paths.UserCrontabDir != "" {
		entries, err := root.ReadDir(paths.UserCrontabDir)
		if err != nil && root.Exists(paths.UserCrontabDir) {
			res.Warnings = append(res.Warnings, "user crontabs are not readable")
		}
		for _, e := range entries {
			if e.IsDir() || !validUser.MatchString(e.Name()) {
				continue
			}
			p := filepath.Join(paths.UserCrontabDir, e.Name())
			if data, err := root.ReadFile(p); err == nil {
				res.Jobs = append(res.Jobs, Parse(data, p, e.Name())...)
			}
		}
	}
	return res
}

// Parse parses crontab content. If owner is empty the file is in system
// format (with a user column); otherwise it is a user crontab.
func Parse(data []byte, source, owner string) []Job {
	var jobs []Job
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		// Environment assignments (NAME=value) are not jobs.
		if eq := strings.IndexByte(f[0], '='); eq > 0 && !strings.HasPrefix(f[0], "@") {
			continue
		}
		var schedule string
		var rest []string
		if strings.HasPrefix(f[0], "@") {
			schedule, rest = f[0], f[1:]
		} else {
			if len(f) < 6 {
				continue
			}
			schedule, rest = strings.Join(f[:5], " "), f[5:]
		}
		user := owner
		if owner == "" {
			if len(rest) < 2 {
				continue
			}
			user, rest = rest[0], rest[1:]
		}
		if len(rest) == 0 {
			continue
		}
		jobs = append(jobs, Job{
			Source:   source,
			Line:     n,
			Schedule: schedule,
			User:     user,
			Command:  logging.Redact(strings.Join(rest, " ")),
		})
	}
	return jobs
}

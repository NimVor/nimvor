// Package cronmgr manages website cron jobs.
//
// Each website gets one file, /etc/cron.d/whacp-site-<domain>, that WhaCP
// owns completely. Jobs always run as the website's (unprivileged) account
// inside the website directory and append their output to
// <root>/logs/cron-<id>.log, where the log viewer can show it.
package cronmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Job is one scheduled command.
type Job struct {
	ID       int    `json:"id"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Enabled  bool   `json:"enabled"`
}

// SiteJobs is the complete cron state of one website.
type SiteJobs struct {
	Domain string `json:"domain"`
	User   string `json:"user"`
	Root   string `json:"root"`
	Jobs   []Job  `json:"jobs"`
}

// FileName returns the cron.d file for a domain (cron ignores names with dots).
func FileName(domain string) string {
	return "whacp-site-" + strings.ReplaceAll(domain, ".", "_")
}

func (s *SiteJobs) validate(env *host.Env) error {
	if err := validate.Domain("domain", s.Domain); err != nil {
		return err
	}
	if err := validate.Username("user", s.User); err != nil {
		return err
	}
	if s.Root == "" {
		s.Root = filepath.Join(env.Cfg.SitesRoot, s.Domain)
	}
	if err := validate.Within("root", env.Cfg.SitesRoot, s.Root); err != nil {
		return err
	}
	if len(s.Jobs) > 200 {
		return &validate.Error{Field: "jobs", Message: "at most 200 jobs per website"}
	}
	seen := map[int]bool{}
	for i, j := range s.Jobs {
		if j.ID <= 0 || seen[j.ID] {
			return &validate.Error{Field: fmt.Sprintf("jobs[%d].id", i), Message: "must be a unique positive number"}
		}
		seen[j.ID] = true
		if err := validate.CronSchedule(fmt.Sprintf("jobs[%d].schedule", i), j.Schedule); err != nil {
			return err
		}
		if err := validate.Command(fmt.Sprintf("jobs[%d].command", i), j.Command); err != nil {
			return err
		}
	}
	return nil
}

// Render produces the cron.d file. "%" is escaped because cron turns it
// into a newline.
func Render(s SiteJobs) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Managed by WhaCP. Do not edit: changes are overwritten from the panel.\n# Website: %s\n", s.Domain)
	b.WriteString("SHELL=/bin/sh\nPATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\nMAILTO=\"\"\n\n")
	for _, j := range s.Jobs {
		line := fmt.Sprintf("%s %s cd %s && ( %s ) >> %s 2>&1", j.Schedule, s.User, s.Root, j.Command, logFile(s.Root, j.ID))
		line = strings.ReplaceAll(line, "%", `\%`)
		if !j.Enabled {
			line = "# disabled: " + line
		}
		fmt.Fprintf(&b, "# job %d\n%s\n", j.ID, line)
	}
	return b.String()
}

func logFile(root string, id int) string {
	return filepath.Join(root, "logs", fmt.Sprintf("cron-%d.log", id))
}

// Register adds cron actions.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "cron.apply", Mutating: true, Description: "Write the complete cron schedule of a website.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in SiteJobs
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		if _, _, err := env.Credential(in.User); err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		path := filepath.Join(env.M.CronDir, FileName(in.Domain))
		set := env.Changes("cron-" + in.Domain)
		if len(in.Jobs) == 0 {
			set.Remove(path)
		} else {
			set.Write(path, []byte(Render(in)), 0o644, agentfs.RootOwner)
		}
		a, err := set.Apply()
		if err != nil {
			return nil, host.FailWithValidation("The cron schedule was not saved", err)
		}
		return map[string]any{"file": path, "changed": len(a.Changed) > 0}, nil
	}})
	r.Register(actions.Action{Name: "cron.run", Mutating: true, Description: "Run a cron command now as the website user (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			SiteJobs
			ID      int    `json:"id"`
			Command string `json:"command"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		in.Jobs = []Job{{ID: in.ID, Schedule: "@daily", Command: in.Command}}
		if err := in.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		cred, _, err := env.Credential(in.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		logPath, _ := env.Root.Path(logFile(in.Root, in.ID))
		dir, _ := env.Root.Path(in.Root)
		j := env.Jobs.Start("cron.run", "site:"+in.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			fmt.Fprintf(log, "$ %s\n", in.Command)
			w := log
			if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0); err == nil {
				// Append to the job's log only if it already exists (it is
				// owned by the site user; never create files as root there).
				defer f.Close()
				fmt.Fprintf(f, "[%s] manual run from WhaCP\n", time.Now().UTC().Format(time.RFC3339))
				w = io.MultiWriter(log, f)
			}
			_, err := env.Runner.RunWith(ctx, "sh", execx.Opts{As: cred, Dir: dir, Output: w, Timeout: time.Hour}, "-c", in.Command)
			if err != nil {
				return nil, fmt.Errorf("the command exited with an error")
			}
			return nil, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
}

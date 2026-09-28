// Package cronmgr manages website cron jobs.
//
// Each website gets one file, /etc/cron.d/whacp-site-<domain>, that WhaCP
// owns completely. Jobs always run as the website's (unprivileged) account
// inside the website directory and append their output to
// <root>/logs/cron-<id>.log, framed by "### whacp-run" lines (start, end
// with exit code) so the panel can list the runs. A job can also have its
// output mailed (MAILTO for that job, output through tee).
package cronmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	// Email receives the job's output; empty: not mailed.
	Email string `json:"email,omitempty"`
}

// RunMarker starts the lines that frame each run in a job's log.
const RunMarker = "### whacp-run"

// emailRe is deliberately narrow: the address goes into a MAILTO line.
var emailRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}@[A-Za-z0-9-]{1,63}(\.[A-Za-z0-9-]{1,63})+$`)

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
		if j.Email != "" && (len(j.Email) > 254 || !emailRe.MatchString(j.Email)) {
			return &validate.Error{Field: fmt.Sprintf("jobs[%d].email", i), Message: "must be a plain email address"}
		}
	}
	return nil
}

// ScriptDir holds the job scripts of accounts with resource limits.
const ScriptDir = "/etc/whacp-cron"

// jobBody is what a run executes as the website account: the command,
// framed in its log (or mailed through tee).
func jobBody(s SiteJobs, j Job) (body, out string) {
	// Each run is framed in the log: start time, then end time and exit code.
	run := fmt.Sprintf(`{ echo "%s start $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ)"; ( %s ); rc=$?; echo "%s end $(date -u +%%Y-%%m-%%dT%%H:%%M:%%SZ) exit=$rc"; }`, RunMarker, j.Command, RunMarker)
	out = ">> " + logFile(s.Root, j.ID) + " 2>&1"
	if j.Email != "" {
		// cron mails what the job prints: keep it in the log too.
		out = "2>&1 | tee -a " + logFile(s.Root, j.ID)
	}
	return run, out
}

// Render produces the cron.d file. "%" is escaped because cron turns it
// into a newline.
func Render(s SiteJobs) string {
	out, _ := render(s, "")
	return out
}

// ScriptPath is a job's script (accounts with resource limits).
func ScriptPath(domain string, id int) string {
	return filepath.Join(ScriptDir, FileName(domain), fmt.Sprintf("%d.sh", id))
}

// render returns the cron.d file and, for an account with resource limits
// (slice set), the job scripts. Such jobs are started by root's cron line
// with systemd-run as the account, in its slice: root's line holds only
// validated names and numbers; the command is in the script, which only
// root and the account can read and which runs as the account.
func render(s SiteJobs, slice string) (string, map[string]string) {
	var b strings.Builder
	scripts := map[string]string{}
	fmt.Fprintf(&b, "# Managed by WhaCP. Do not edit: changes are overwritten from the panel.\n# Website: %s\n", s.Domain)
	b.WriteString("SHELL=/bin/sh\nPATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\nMAILTO=\"\"\n\n")
	for _, j := range s.Jobs {
		run, out := jobBody(s, j)
		mailto := `MAILTO=""`
		if j.Email != "" {
			mailto = `MAILTO="` + j.Email + `"`
		}
		var line string
		if slice == "" {
			line = fmt.Sprintf("%s %s cd %s && %s %s", j.Schedule, s.User, s.Root, run, out)
			line = strings.ReplaceAll(line, "%", `\%`)
		} else {
			scripts[ScriptPath(s.Domain, j.ID)] = fmt.Sprintf("# Managed by WhaCP: job %d of %s (runs as %s in %s).\ncd %s || exit 1\n%s %s\n", j.ID, s.Domain, s.User, slice, s.Root, run, out)
			line = fmt.Sprintf("%s root /usr/bin/systemd-run --quiet --collect --wait --pipe --uid=%s --gid=%s --slice=%s --working-directory=%s --property=NoNewPrivileges=yes -- /bin/sh %s", j.Schedule, s.User, s.User, slice, s.Root, ScriptPath(s.Domain, j.ID))
		}
		if !j.Enabled {
			line = "# disabled: " + line
		}
		fmt.Fprintf(&b, "# job %d\n%s\n%s\n", j.ID, mailto, line)
	}
	return b.String(), scripts
}

func logFile(root string, id int) string {
	return filepath.Join(root, "logs", fmt.Sprintf("cron-%d.log", id))
}

// openSiteRoot opens a website folder (not through a symlink) as an os.Root.
func openSiteRoot(env *host.Env, root string) (*os.Root, error) {
	real, err := env.Root.Path(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(real); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("the website folder does not exist")
	}
	return os.OpenRoot(real)
}

// openLogForAppend opens an existing job log for appending, confined to the
// website folder; nil when it does not exist or is not a regular file.
func openLogForAppend(env *host.Env, root string, id int) *os.File {
	r, err := openSiteRoot(env, root)
	if err != nil {
		return nil
	}
	defer r.Close()
	f, err := r.OpenFile(filepath.Join("logs", fmt.Sprintf("cron-%d.log", id)), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return nil
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil
	}
	return f
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() > 0 {
		return ee.ExitCode()
	}
	return 1
}

// maxLog is how much of the end of a job log cron.log returns.
const maxLog = 512 << 10

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
		cred, _, _ := env.Credential(in.User)
		content, scripts := render(in, cred.Slice)
		if len(in.Jobs) == 0 {
			set.Remove(path)
		} else {
			set.Write(path, []byte(content), 0o644, agentfs.RootOwner)
		}
		// Job scripts (resource-limited accounts): exactly these.
		dir := filepath.Join(ScriptDir, FileName(in.Domain))
		old, _ := env.Root.Glob(filepath.Join(dir, "*.sh"))
		for _, f := range old {
			if _, keep := scripts[f]; !keep {
				set.Remove(f)
			}
		}
		if len(scripts) > 0 {
			group := agentfs.Owner{UID: 0, GID: int(cred.GID)}
			set.Mkdir(ScriptDir, 0o755, agentfs.RootOwner)
			set.Mkdir(dir, 0o750, group)
			for f, body := range scripts {
				set.Write(f, []byte(body), 0o640, group)
			}
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
		dir, _ := env.Root.Path(in.Root)
		j := env.Jobs.Start("cron.run", "site:"+in.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			fmt.Fprintf(log, "$ %s\n", in.Command)
			w := log
			// Append to the job's log only if it already exists (it is owned
			// by the site user; never create files as root there), and only
			// inside the website: the log is the user's file, and a symlink
			// must not make root append their output anywhere else.
			f := openLogForAppend(env, in.Root, in.ID)
			if f != nil {
				defer f.Close()
				fmt.Fprintf(f, "%s start %s manual\n", RunMarker, time.Now().UTC().Format(time.RFC3339))
				w = io.MultiWriter(log, f)
			}
			_, err := env.Runner.RunWith(ctx, "sh", execx.Opts{As: cred, Dir: dir, Output: w, Timeout: time.Hour}, "-c", in.Command)
			if f != nil {
				fmt.Fprintf(f, "%s end %s exit=%d\n", RunMarker, time.Now().UTC().Format(time.RFC3339), exitCode(err))
			}
			if err != nil {
				return nil, fmt.Errorf("the command exited with an error")
			}
			return nil, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "cron.log", Description: "The end (up to 512 KiB) of a website cron job's log, read confined to the website.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			Root   string `json:"root"`
			ID     int    `json:"id"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, host.Invalid(err)
		}
		if in.Root == "" {
			in.Root = filepath.Join(env.Cfg.SitesRoot, in.Domain)
		}
		if err := validate.Within("root", env.Cfg.SitesRoot, in.Root); err != nil {
			return nil, host.Invalid(err)
		}
		if in.ID <= 0 {
			return nil, actions.InvalidParams("id must be positive")
		}
		r, err := openSiteRoot(env, in.Root)
		if err != nil {
			return map[string]any{"content": "", "size": 0, "truncated": false}, nil
		}
		defer r.Close()
		f, err := r.Open(filepath.Join("logs", fmt.Sprintf("cron-%d.log", in.ID)))
		if err != nil {
			return map[string]any{"content": "", "size": 0, "truncated": false}, nil
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, actions.InvalidParams("the log is not a file")
		}
		size := info.Size()
		start := int64(0)
		if size > maxLog {
			start = size - maxLog
		}
		buf := make([]byte, size-start)
		n, err := f.ReadAt(buf, start)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		return map[string]any{"content": strings.ToValidUTF8(string(buf[:n]), "\uFFFD"), "size": size, "truncated": start > 0}, nil
	}})
}

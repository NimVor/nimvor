package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Updating WhaCP from the panel. The installer records where it was run
// from (SourceFile). The panel looks up the newest version itself (the
// agent has no network access); the agent reports what is installed and
// runs installer/update.sh in its own transient unit (UpdateUnit), so the
// update survives the agent and the panel restarting during it.
//
// The update script runs as root, so it is only started when nobody but
// root can have changed it: the checkout, its installer folder, the
// scripts and .git must be owned by root and not writable by group or
// others. Otherwise the panel shows the command to run over SSH instead.

// SourceFile is written by install.sh.
const SourceFile = "/etc/whacp/source.json"

// UpdateUnit runs installer/update.sh; UpdateLog keeps its output.
const (
	UpdateUnit = "whacp-update.service"
	UpdateLog  = "/var/log/whacp-update.log"
)

// Source is what update status reports.
type Source struct {
	Path       string `json:"path"`
	Git        bool   `json:"git"`
	Commit     string `json:"commit"`
	Branch     string `json:"branch"`
	Remote     string `json:"remote"`
	Updatable  bool   `json:"updatable"`
	Reason     string `json:"reason,omitempty"`
	Command    string `json:"command"`
	Running    bool   `json:"running"`
	UpdateLog  string `json:"log,omitempty"`
	LastResult string `json:"last_result,omitempty"`
}

var (
	shaRe    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	branchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,100}$`)
)

// ReadSource reads the installer's record and the checkout's git state
// (from .git's files: the agent never runs git in a checkout).
func ReadSource(env *host.Env) (*Source, error) {
	b, err := env.Root.ReadFile(SourceFile)
	if err != nil {
		return nil, fmt.Errorf("the installer did not record its source folder (%s); run the installer again", SourceFile)
	}
	var rec struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(b, &rec) != nil || !filepath.IsAbs(rec.Path) || filepath.Clean(rec.Path) != rec.Path || rec.Path == "/" {
		return nil, fmt.Errorf("%s does not name an absolute folder", SourceFile)
	}
	s := &Source{Path: rec.Path, Command: "sudo " + filepath.Join(rec.Path, "installer/update.sh")}
	g := filepath.Join(rec.Path, ".git")
	if env.Root.IsDir(g) {
		s.Git = true
		head, _ := env.Root.ReadFile(filepath.Join(g, "HEAD"))
		h := strings.TrimSpace(string(head))
		if ref, ok := strings.CutPrefix(h, "ref: refs/heads/"); ok && branchRe.MatchString(ref) && !strings.Contains(ref, "..") {
			s.Branch = ref
			s.Commit = resolveRef(env, g, "refs/heads/"+ref)
		} else if shaRe.MatchString(h) {
			s.Commit = h
		}
		cfg, _ := env.Root.ReadFile(filepath.Join(g, "config"))
		s.Remote = remoteURL(string(cfg), s.Branch)
	} else {
		s.Command = "download the new release and run its installer/upgrade.sh"
	}
	s.Updatable, s.Reason = updatable(env, s)
	return s, nil
}

func resolveRef(env *host.Env, gitDir, ref string) string {
	if b, err := env.Root.ReadFile(filepath.Join(gitDir, ref)); err == nil {
		if v := strings.TrimSpace(string(b)); shaRe.MatchString(v) {
			return v
		}
	}
	packed, _ := env.Root.ReadFile(filepath.Join(gitDir, "packed-refs"))
	for _, line := range strings.Split(string(packed), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == ref && shaRe.MatchString(f[0]) {
			return f[0]
		}
	}
	return ""
}

// remoteURL is the upstream remote's URL from .git/config, without any
// credentials in it.
func remoteURL(cfg, branch string) string {
	section, remote := "", "origin"
	urls := map[string]string{}
	for _, line := range strings.Split(cfg, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "[") {
			section = l
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if branch != "" && section == `[branch "`+branch+`"]` && k == "remote" {
			remote = v
		}
		if name, ok := strings.CutPrefix(section, `[remote "`); ok && k == "url" {
			urls[strings.TrimSuffix(name, `"]`)] = v
		}
	}
	u := urls[remote]
	if p, err := url.Parse(u); err == nil && p.User != nil {
		p.User = nil
		u = p.String()
	}
	return u
}

// updatable: may the agent run this checkout's update script as root?
func updatable(env *host.Env, s *Source) (bool, string) {
	if !s.Git {
		return false, "This installation came from a release archive."
	}
	// The folders above the checkout too: whoever may write to one of them
	// could swap the whole checkout after this check.
	var parents []string
	for d := filepath.Dir(s.Path); ; d = filepath.Dir(d) {
		parents = append(parents, d)
		if d == "/" {
			break
		}
	}
	for _, p := range append(parents, s.Path, filepath.Join(s.Path, ".git"), filepath.Join(s.Path, "installer"), filepath.Join(s.Path, "installer/update.sh"), filepath.Join(s.Path, "installer/install.sh")) {
		fi, err := env.Root.Lstat(p)
		if err != nil {
			return false, p + " is missing."
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return false, p + " is a symbolic link."
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if env.Cfg.RootDir == "/" && (!ok || st.Uid != 0) {
			return false, p + " is not owned by root, so the panel does not run it as root. Update over SSH."
		}
		if fi.Mode().Perm()&0o022 != 0 {
			return false, p + " can be changed by other accounts, so the panel does not run it as root. Update over SSH."
		}
	}
	return true, ""
}

func registerSelfUpdate(r *actions.Registry, env *host.Env) {
	status := func(ctx context.Context) (any, error) {
		s, err := ReadSource(env)
		if err != nil {
			return nil, actions.Failed("The installed version could not be read.", err.Error())
		}
		s.Running = env.UnitActive(ctx, UpdateUnit)
		if b, err := env.Root.ReadFile(UpdateLog); err == nil {
			lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
			if len(lines) > 200 {
				lines = lines[len(lines)-200:]
			}
			s.UpdateLog = strings.Join(lines, "\n")
		}
		if out, err := env.Runner.Run(ctx, "systemctl", "show", "--property=Result", "--value", "--", UpdateUnit); err == nil {
			s.LastResult = strings.TrimSpace(string(out))
		}
		return s, nil
	}
	r.Register(actions.Action{Name: "whacp.update_status", Description: "WhaCP's source checkout (commit, branch, remote), whether the panel may update it, and the last update's output.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := host.Decode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		return status(ctx)
	}})
	r.Register(actions.Action{Name: "whacp.update", Mutating: true, Description: "Update WhaCP: runs installer/update.sh of the root-owned source checkout in its own unit (output in " + UpdateLog + "). The agent and the panel restart during it.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := host.Decode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		s, err := ReadSource(env)
		if err != nil {
			return nil, actions.Failed("The installed version could not be read.", err.Error())
		}
		if !s.Updatable {
			return nil, actions.Conflict(s.Reason + " Run: " + s.Command)
		}
		if env.UnitActive(ctx, UpdateUnit) {
			return nil, actions.Conflict("An update is already running.")
		}
		env.Runner.Run(ctx, "systemctl", "reset-failed", "--", UpdateUnit)
		script := filepath.Join(s.Path, "installer/update.sh")
		out, err := env.Runner.Run(ctx, "systemd-run", "--unit="+UpdateUnit, "--description=WhaCP update",
			"--property=StandardOutput=truncate:"+UpdateLog, "--property=StandardError=append:"+UpdateLog,
			"--property=TimeoutStopSec=30min", "--property=KillMode=control-group",
			"--", "/bin/bash", script)
		if err != nil {
			return nil, actions.Failed("The update could not be started.", host.Output(out))
		}
		return map[string]any{"started": true, "unit": UpdateUnit, "log": UpdateLog}, nil
	}})
}

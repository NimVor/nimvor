// Package deploy performs Git-based deployments.
//
// Everything runs as the website account, never as root: key generation,
// clone/fetch and post-deploy commands. Only https:// and SSH remotes are
// accepted, and git is told to refuse the file:// and ext:: transports.
package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Request describes one deployment.
type Request struct {
	Domain string   `json:"domain"`
	User   string   `json:"user"`
	Root   string   `json:"root"`
	Repo   string   `json:"repo"`
	Branch string   `json:"branch"`
	Path   string   `json:"path"`
	Hooks  []string `json:"hooks"`
}

func (r *Request) validate(env *host.Env) error {
	if err := validate.Domain("domain", r.Domain); err != nil {
		return err
	}
	if err := validate.Username("user", r.User); err != nil {
		return err
	}
	if r.Root == "" {
		r.Root = filepath.Join(env.Cfg.SitesRoot, r.Domain)
	}
	if err := validate.Within("root", env.Cfg.SitesRoot, r.Root); err != nil {
		return err
	}
	if err := validate.GitURL("repo", r.Repo); err != nil {
		return err
	}
	if err := validate.GitRef("branch", r.Branch); err != nil {
		return err
	}
	if r.Path == "" || r.Path == "." {
		return &validate.Error{Field: "path", Message: "choose a folder inside the website, e.g. app"}
	}
	if err := validate.RelPath("path", r.Path); err != nil {
		return err
	}
	if err := validate.Within("path", r.Root, filepath.Join(r.Root, r.Path)); err != nil {
		return err
	}
	switch strings.Split(filepath.Clean(r.Path), "/")[0] {
	case "logs", "tmp", "backups", ".ssh":
		return &validate.Error{Field: "path", Message: "cannot deploy into " + r.Path}
	}
	if len(r.Hooks) > 20 {
		return &validate.Error{Field: "hooks", Message: "at most 20 commands"}
	}
	for i, h := range r.Hooks {
		if err := validate.Command(fmt.Sprintf("hooks[%d]", i), h); err != nil {
			return err
		}
	}
	return nil
}

func keyPath(root string) string { return filepath.Join(root, ".ssh", "nimvor_deploy_ed25519") }

// gitEnv makes git use the site's deploy key and never prompt.
func gitEnv(root string) []string {
	ssh := fmt.Sprintf("ssh -i %s -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=%s",
		keyPath(root), filepath.Join(root, ".ssh", "known_hosts"))
	return []string{"GIT_SSH_COMMAND=" + ssh, "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_ALLOW_PROTOCOL=https:ssh"}
}

// gitArgs prefixes git invocations with safe configuration.
func gitArgs(args ...string) []string {
	return append([]string{"-c", "protocol.file.allow=never", "-c", "protocol.ext.allow=never", "-c", "core.hooksPath=/dev/null"}, args...)
}

// Register adds deployment actions.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "deploy.key", Mutating: true, Description: "Create (if needed) and return the website's SSH deploy key.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
			User   string `json:"user"`
			Root   string `json:"root"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		req := Request{Domain: in.Domain, User: in.User, Root: in.Root, Repo: "https://example.invalid/x", Branch: "main", Path: "app"}
		if err := req.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		cred, _, err := env.Credential(req.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		dir, _ := env.Root.Path(req.Root)
		pub := keyPath(req.Root) + ".pub"
		if !env.Root.Exists(pub) {
			script := fmt.Sprintf("umask 077 && mkdir -p %s/.ssh && ssh-keygen -q -t ed25519 -N '' -C %s -f %s",
				dir, "nimvor-deploy@"+req.Domain, keyPath(dir))
			if out, err := env.Runner.RunWith(ctx, "sh", execx.Opts{As: cred, Dir: dir}, "-c", script); err != nil {
				return nil, actions.Failed("The deploy key could not be created.", host.Output(out))
			}
		}
		b, err := env.Root.ReadFile(pub)
		if err != nil {
			return nil, actions.Failed("The deploy key could not be read.", "")
		}
		return map[string]any{"public_key": strings.TrimSpace(string(b))}, nil
	}})

	r.Register(actions.Action{Name: "deploy.run", Mutating: true, Description: "Clone or update a Git repository and run post-deploy commands (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var req Request
		if err := host.Decode(p, &req); err != nil {
			return nil, err
		}
		if err := req.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		cred, _, err := env.Credential(req.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		j := env.Jobs.Start("deploy.run", "site:"+req.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			return Run(ctx, env, req, cred, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
}

// Result of a deployment.
type Result struct {
	Commit  string `json:"commit"`
	Message string `json:"message"`
	Cloned  bool   `json:"cloned"`
}

// Run performs the deployment synchronously (called inside a job).
func Run(ctx context.Context, env *host.Env, req Request, cred *execx.Credential, log io.Writer) (*Result, error) {
	root, _ := env.Root.Path(req.Root)
	target := filepath.Join(root, req.Path)
	e := gitEnv(root)
	run := func(dir string, args ...string) (string, error) {
		fmt.Fprintf(log, "$ git %s\n", strings.Join(args, " "))
		var out bytes.Buffer
		_, err := env.Runner.RunWith(ctx, "git", execx.Opts{As: cred, Dir: dir, Env: e, Output: io.MultiWriter(log, &out), Timeout: 20 * time.Minute}, gitArgs(args...)...)
		return strings.TrimSpace(out.String()), err
	}
	res := &Result{}
	if env.Root.Exists(filepath.Join(req.Root, req.Path, ".git")) {
		if _, err := run(target, "fetch", "--prune", "origin", "--", req.Branch); err != nil {
			return nil, fmt.Errorf("git fetch failed")
		}
		if _, err := run(target, "checkout", "-B", req.Branch, "origin/"+req.Branch); err != nil {
			return nil, fmt.Errorf("git checkout failed")
		}
		if _, err := run(target, "reset", "--hard", "origin/"+req.Branch); err != nil {
			return nil, fmt.Errorf("git reset failed")
		}
	} else {
		if entries, err := env.Root.ReadDir(filepath.Join(req.Root, req.Path)); err == nil && len(entries) > 0 {
			return nil, fmt.Errorf("%s is not empty and is not a Git checkout; choose an empty folder", req.Path)
		}
		if _, err := run(root, "clone", "--branch", req.Branch, "--single-branch", "--", req.Repo, req.Path); err != nil {
			return nil, fmt.Errorf("git clone failed (check the URL, the branch and that the deploy key has access)")
		}
		res.Cloned = true
	}
	if out, err := run(target, "log", "-1", "--format=%H%x09%s"); err == nil {
		lines := strings.Split(out, "\n")
		last := lines[len(lines)-1]
		if h, msg, ok := strings.Cut(last, "\t"); ok {
			res.Commit, res.Message = h, msg
		}
	}
	for i, hook := range req.Hooks {
		fmt.Fprintf(log, "$ %s\n", hook)
		if _, err := env.Runner.RunWith(ctx, "sh", execx.Opts{As: cred, Dir: target, Output: log, Timeout: 30 * time.Minute}, "-c", hook); err != nil {
			return res, fmt.Errorf("post-deploy command %d failed", i+1)
		}
	}
	fmt.Fprintf(log, "Deployed %s (%s)\n", shortHash(res.Commit), res.Message)
	return res, nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// Package ffmpegmgr controls who may run the server's FFmpeg: its programs
// belong to root and the group nimvor-ffmpeg with mode 0750, and the panel
// decides the group's members (the accounts whose hosting plan includes
// FFmpeg). Package updates reset the modes on RHEL, so they are applied
// again after every package job and by the panel's scheduler; on Debian
// and Ubuntu dpkg-statoverride keeps them.
package ffmpegmgr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Group may run FFmpeg.
const Group = "nimvor-ffmpeg"

// Binaries are FFmpeg's programs (from the ffmpeg package on Debian and
// Ubuntu, ffmpeg-free from EPEL on RHEL).
var Binaries = []string{"/usr/bin/ffmpeg", "/usr/bin/ffprobe"}

type state struct {
	Restricted bool `json:"restricted"`
}

func statePath(env *host.Env) string { return filepath.Join(env.Cfg.StateDir, "ffmpeg.json") }

func groupEntry(env *host.Env) (gid int, members []string, ok bool) {
	b, err := env.Root.ReadFile("/etc/group")
	if err != nil {
		return 0, nil, false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) >= 4 && f[0] == Group {
			gid, err := strconv.Atoi(f[2])
			if err != nil {
				return 0, nil, false
			}
			for _, m := range strings.Split(f[3], ",") {
				if m = strings.TrimSpace(m); m != "" {
					members = append(members, m)
				}
			}
			sort.Strings(members)
			return gid, members, true
		}
	}
	return 0, nil, false
}

// Binary is the state of one program.
type Binary struct {
	Path       string `json:"path"`
	Present    bool   `json:"present"`
	Mode       string `json:"mode,omitempty"`
	Restricted bool   `json:"restricted"`
}

func inspect(env *host.Env, gid int, haveGroup bool) []Binary {
	var out []Binary
	for _, p := range Binaries {
		b := Binary{Path: p}
		real, err := env.Root.Path(p)
		var st syscall.Stat_t
		if err == nil && syscall.Lstat(real, &st) == nil && st.Mode&syscall.S_IFMT == syscall.S_IFREG {
			b.Present = true
			b.Mode = fmt.Sprintf("%04o %d:%d", st.Mode&0o7777, st.Uid, st.Gid)
			b.Restricted = haveGroup && st.Uid == 0 && int(st.Gid) == gid && st.Mode&0o7777 == 0o750
		}
		out = append(out, b)
	}
	return out
}

// restrict gives the programs to root:Group, mode 0750, on descriptors
// opened without following links (a link at the path is refused).
func restrict(ctx context.Context, env *host.Env, gid int) error {
	for _, p := range Binaries {
		real, err := env.Root.Path(p)
		if err != nil {
			return err
		}
		fd, err := syscall.Open(real, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		if err != nil {
			if errors.Is(err, syscall.ENOENT) {
				continue
			}
			return fmt.Errorf("%s: %w (a link is not changed)", p, err)
		}
		var st syscall.Stat_t
		err = syscall.Fstat(fd, &st)
		if err == nil && st.Mode&syscall.S_IFMT != syscall.S_IFREG {
			err = fmt.Errorf("not a regular file")
		}
		if err == nil && (st.Uid != 0 || int(st.Gid) != gid) {
			err = syscall.Fchown(fd, 0, gid)
		}
		if err == nil && st.Mode&0o7777 != 0o750 {
			err = syscall.Fchmod(fd, 0o750)
		}
		syscall.Close(fd)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		// Debian and Ubuntu keep it through package updates.
		if env.Platform.PackageManager() == "apt" && env.Runner.Available("dpkg-statoverride") {
			out, _ := env.Runner.Run(ctx, "dpkg-statoverride", "--list", p)
			if strings.TrimSpace(string(out)) != "root "+Group+" 750 "+p {
				env.Runner.Run(ctx, "dpkg-statoverride", "--remove", p)
				if out, err := env.Runner.Run(ctx, "dpkg-statoverride", "--add", "root", Group, "0750", p); err != nil {
					return fmt.Errorf("dpkg-statoverride %s: %s", p, host.Output(out))
				}
			}
		}
	}
	return nil
}

func ensureGroup(ctx context.Context, env *host.Env) (int, error) {
	if gid, _, ok := groupEntry(env); ok {
		return gid, nil
	}
	if out, err := env.Runner.Run(ctx, "groupadd", "--system", "--", Group); err != nil {
		return 0, fmt.Errorf("groupadd: %s", host.Output(out))
	}
	gid, _, ok := groupEntry(env)
	if !ok {
		return 0, fmt.Errorf("the %s group was not found after creating it", Group)
	}
	return gid, nil
}

// Reapply restricts the programs again when NimVor manages them (after
// package installs and updates).
func Reapply(ctx context.Context, env *host.Env) error {
	b, err := env.Root.ReadFile(statePath(env))
	if err != nil {
		return nil
	}
	var st state
	if json.Unmarshal(b, &st) != nil || !st.Restricted {
		return nil
	}
	gid, _, ok := groupEntry(env)
	if !ok {
		return nil
	}
	return restrict(ctx, env, gid)
}

func status(ctx context.Context, env *host.Env) map[string]any {
	gid, members, ok := groupEntry(env)
	bins := inspect(env, gid, ok)
	installed, restricted := false, true
	for _, b := range bins {
		if b.Present {
			installed = true
			restricted = restricted && b.Restricted
		}
	}
	version := ""
	if installed {
		if out, err := env.Runner.Run(ctx, "ffmpeg", "-hide_banner", "-version"); err == nil {
			version = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		}
	}
	pkg, _ := env.Platform.PackageName("ffmpeg")
	if members == nil {
		members = []string{}
	}
	return map[string]any{"installed": installed, "version": version, "package": pkg, "binaries": bins, "group": Group, "group_exists": ok, "members": members, "restricted": installed && restricted}
}

// Register adds ffmpeg.status and ffmpeg.access.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "ffmpeg.status", Description: "FFmpeg: installed, version, package, whether its programs are limited to the nimvor-ffmpeg group, and the group's members.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return status(ctx, env), nil
	}})

	r.Register(actions.Action{Name: "ffmpeg.uninstall", Mutating: true, Description: "Remove the FFmpeg package (job); refused when other packages need it. The group and, on Debian/Ubuntu, the mode override stay, so a later install is limited at once.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		pkg, ok := env.Platform.PackageName("ffmpeg")
		if !ok {
			return nil, actions.Conflict("FFmpeg is not available on this operating system.")
		}
		j := env.Jobs.Start("ffmpeg.uninstall", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			return nil, pkgs.RemoveNative(ctx, env, log, []string{pkg})
		})
		return map[string]any{"job_id": j.ID}, nil
	}})

	r.Register(actions.Action{Name: "ffmpeg.access", Mutating: true, Description: "Set the accounts that may run FFmpeg (the nimvor-ffmpeg group's members, exactly these) and limit FFmpeg's programs to that group.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Users []string `json:"users"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if len(in.Users) > 5000 {
			return nil, actions.InvalidParams("at most 5000 accounts")
		}
		want := map[string]bool{}
		for _, u := range in.Users {
			if err := validate.Username("users", u); err != nil {
				return nil, err
			}
			acct, err := env.LookupUser(u)
			if err != nil {
				continue // removed meanwhile
			}
			if acct.UID < 1000 {
				return nil, actions.InvalidParams(u + " is a system account")
			}
			want[u] = true
		}
		gid, err := ensureGroup(ctx, env)
		if err != nil {
			return nil, actions.Failed("The "+Group+" group could not be created.", err.Error())
		}
		_, before, _ := groupEntry(env)
		list := make([]string, 0, len(want))
		for u := range want {
			list = append(list, u)
		}
		sort.Strings(list)
		// Exactly these members (one command, so no one is left in between).
		if out, err := env.Runner.Run(ctx, "gpasswd", "--members", strings.Join(list, ","), Group); err != nil {
			return nil, actions.Failed("The FFmpeg group's members could not be set.", host.Output(out))
		}
		if err := restrict(ctx, env, gid); err != nil {
			return nil, actions.Failed("FFmpeg's programs could not be limited to the "+Group+" group.", err.Error())
		}
		b, _ := json.Marshal(state{Restricted: true})
		if _, err := env.Writer.WriteFile(statePath(env), b, 0o600, agentfs.RootOwner); err != nil {
			env.Log.Warn("ffmpeg state", "err", err)
		}
		var added, removed []string
		old := map[string]bool{}
		for _, m := range before {
			old[m] = true
			if !want[m] {
				removed = append(removed, m)
			}
		}
		for _, u := range list {
			if !old[u] {
				added = append(added, u)
			}
		}
		out := status(ctx, env)
		out["added"], out["removed"] = nonNil(added), nonNil(removed)
		return out, nil
	}})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

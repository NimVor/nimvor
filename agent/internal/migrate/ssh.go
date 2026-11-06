package migrate

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Transport runs commands on the source server. Only the adapters and the
// engine build commands (from fixed text and quoted, validated values): there
// is no way to send a command a user typed.
type Transport interface {
	// Exec runs a command line on the source and returns its output; when
	// stdout is not nil the standard output goes there instead (streamed,
	// not kept in memory).
	Exec(ctx context.Context, cmd string, stdout io.Writer) (string, error)
	// Describe is a secret-free description of the source.
	Describe() string
}

// Shq quotes one value for a POSIX shell on the source.
func Shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sshTransport runs commands with the ssh binary. Everything secret lives in
// a private folder that exists for one command and is overwritten and removed
// afterwards.
type sshTransport struct {
	src Source
	env *host.Env
}

// NewSSH returns a transport for a validated source with a pinned host key.
func NewSSH(env *host.Env, src Source) (Transport, error) {
	if err := src.Validate(true); err != nil {
		return nil, err
	}
	return &sshTransport{src: src, env: env}, nil
}

func (t *sshTransport) Describe() string {
	return fmt.Sprintf("ssh://%s@%s:%d", t.src.User, t.src.Host, t.src.Port)
}

func (t *sshTransport) tmpBase() string {
	real, err := t.env.Root.Path(filepath.Join(t.env.Cfg.StateDir, "tmp"))
	if err == nil && os.MkdirAll(real, 0o700) == nil {
		return real
	}
	return ""
}

// session is one command's private folder and the ssh options built from it.
type session struct {
	dir string
	env []string
	opt []string
}

func (t *sshTransport) hostPattern() string {
	if t.src.Port != 22 || strings.Contains(t.src.Host, ":") {
		return "[" + t.src.Host + "]:" + strconv.Itoa(t.src.Port)
	}
	return t.src.Host
}

// open writes the pinned host key, the private key and the password helper.
func (t *sshTransport) open() (*session, error) {
	dir, err := os.MkdirTemp(t.tmpBase(), "ssh-")
	if err != nil {
		return nil, err
	}
	s := &session{dir: dir, env: []string{"HOME=" + dir}}
	fail := func(err error) (*session, error) {
		s.close()
		return nil, err
	}
	kh := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(kh, []byte(t.hostPattern()+" "+t.src.HostKey+"\n"), 0o600); err != nil {
		return fail(err)
	}
	s.opt = []string{
		"-F", "/dev/null",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "UserKnownHostsFile=" + kh,
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "IdentitiesOnly=yes",
		"-o", "ConnectTimeout=20",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=4",
		"-o", "LogLevel=ERROR",
		"-p", strconv.Itoa(t.src.Port),
	}
	if t.src.PrivateKey != "" {
		kp := filepath.Join(dir, "key")
		if err := os.WriteFile(kp, []byte(strings.TrimSpace(t.src.PrivateKey)+"\n"), 0o600); err != nil {
			return fail(err)
		}
		s.opt = append(s.opt, "-i", kp, "-o", "BatchMode=yes", "-o", "PreferredAuthentications=publickey")
	} else {
		// The password reaches ssh through its askpass program, which only prints it.
		pw := filepath.Join(dir, "pw")
		if err := os.WriteFile(pw, []byte(t.src.Password+"\n"), 0o600); err != nil {
			return fail(err)
		}
		ap := filepath.Join(dir, "askpass")
		if err := os.WriteFile(ap, []byte("#!/bin/sh\nexec cat -- \"$NIMVOR_ASKPASS_FILE\"\n"), 0o700); err != nil {
			return fail(err)
		}
		s.env = append(s.env, "SSH_ASKPASS="+ap, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=nimvor:0", "NIMVOR_ASKPASS_FILE="+pw)
		s.opt = append(s.opt, "-o", "BatchMode=no", "-o", "PreferredAuthentications=password,keyboard-interactive", "-o", "NumberOfPasswordPrompts=1")
	}
	return s, nil
}

// close overwrites the secret files and removes the folder.
func (s *session) close() {
	for _, n := range []string{"key", "pw"} {
		p := filepath.Join(s.dir, n)
		if info, err := os.Stat(p); err == nil {
			_ = os.WriteFile(p, bytes.Repeat([]byte{0}, int(info.Size())), 0o600)
		}
	}
	_ = os.RemoveAll(s.dir)
}

// sshCommand is the ssh command line rsync uses with -e (no secret in it).
func (s *session) sshCommand() string {
	parts := []string{"ssh"}
	for _, o := range s.opt {
		parts = append(parts, Shq(o))
	}
	return strings.Join(parts, " ")
}

// oneLine makes a command safe for the agent's command runner, which refuses
// a newline in any argument: a command with line breaks is sent encoded and
// decoded on the source, so the runner never sees one.
func oneLine(cmd string) string {
	if !strings.ContainsAny(cmd, "\n\r") {
		return cmd
	}
	return "echo " + Shq(base64.StdEncoding.EncodeToString([]byte(cmd))) + " | base64 -d | sh"
}

func (t *sshTransport) Exec(ctx context.Context, cmd string, stdout io.Writer) (string, error) {
	cmd = oneLine(cmd)
	s, err := t.open()
	if err != nil {
		return "", err
	}
	defer s.close()
	args := append(append([]string{}, s.opt...), t.src.User+"@"+t.src.Host, "--", cmd)
	out, err := t.env.Runner.RunWith(ctx, "ssh", execx.Opts{Env: s.env, Stdout: stdout, Timeout: -1}, args...)
	text := t.redact(host.Output(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("%s", strings.TrimSpace(text))
	}
	return text, nil
}

// redact removes the password from a message.
func (t *sshTransport) redact(s string) string {
	if t.src.Password != "" {
		s = strings.ReplaceAll(s, t.src.Password, "***")
	}
	return s
}

// Rsync runs rsync from the source (pull) over the same pinned ssh login.
// extra are rsync's own options; remote is the source path; local the
// destination. Nothing secret is in the arguments.
func (t *sshTransport) Rsync(ctx context.Context, extra []string, remote, local string, stdout io.Writer) (string, error) {
	s, err := t.open()
	if err != nil {
		return "", err
	}
	defer s.close()
	args := append([]string{"-e", s.sshCommand()}, extra...)
	args = append(args, "--", t.src.User+"@"+bracket(t.src.Host)+":"+remote, local)
	out, err := t.env.Runner.RunWith(ctx, "rsync", execx.Opts{Env: s.env, Stdout: stdout, Timeout: -1}, args...)
	text := t.redact(host.Output(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("%s", strings.TrimSpace(text))
	}
	return text, nil
}

func bracket(h string) string {
	if strings.Contains(h, ":") {
		return "[" + h + "]"
	}
	return h
}

// Puller is a transport that can also pull a folder (rsync).
type Puller interface {
	Transport
	Rsync(ctx context.Context, extra []string, remote, local string, stdout io.Writer) (string, error)
}

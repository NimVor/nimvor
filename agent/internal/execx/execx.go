// Package execx runs a fixed allowlist of system binaries.
//
// Callers name a command ID; the Runner maps it to an absolute binary path
// discovered at start-up and passes arguments directly (no shell parsing).
// The only shell available is /bin/sh as an unprivileged account (UID ≥ 1000)
// — used for user-defined cron jobs, workers and deploy hooks, which by
// definition are commands the site owner chose to run as themselves.
package execx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ErrNotAllowed is returned for commands outside the allowlist.
var ErrNotAllowed = errors.New("command is not allowed")

// ErrUnavailable is returned when an allowed binary is not installed.
var ErrUnavailable = errors.New("command is not installed")

// Credential runs a command as another account.
type Credential struct {
	UID    uint32
	GID    uint32
	Groups []uint32
	Home   string
}

// Opts customises a single run.
type Opts struct {
	Stdin  io.Reader
	Env    []string // extra KEY=value entries
	Dir    string
	As     *Credential
	Output io.Writer // receives combined output as it is produced
	// Stdout, when set, receives raw standard output (not captured); the
	// returned bytes and Output then only contain standard error.
	Stdout io.Writer
	// Timeout overrides the runner default; a negative value disables the
	// per-command timeout (the caller's context still applies).
	Timeout time.Duration
}

// Runner executes allowlisted commands.
type Runner interface {
	Run(ctx context.Context, id string, args ...string) ([]byte, error)
	RunWith(ctx context.Context, id string, o Opts, args ...string) ([]byte, error)
	Available(id string) bool
}

// maxOutput bounds captured output (1 MiB); streaming Output is unbounded.
const maxOutput = 1 << 20

// defaultCandidates maps command IDs to candidate absolute paths.
var defaultCandidates = map[string][]string{
	"systemctl":              {"/usr/bin/systemctl", "/bin/systemctl"},
	"systemd-run":            {"/usr/bin/systemd-run", "/bin/systemd-run"},
	"journalctl":             {"/usr/bin/journalctl", "/bin/journalctl"},
	"nginx":                  {"/usr/sbin/nginx", "/usr/bin/nginx"},
	"mysqld":                 {"/usr/sbin/mysqld", "/usr/bin/mysqld", "/usr/libexec/mysqld"},
	"mariadbd":               {"/usr/sbin/mariadbd", "/usr/bin/mariadbd", "/usr/libexec/mariadbd"},
	"mysql":                  {"/usr/bin/mariadb", "/usr/bin/mysql"},
	"mysqldump":              {"/usr/bin/mariadb-dump", "/usr/bin/mysqldump"},
	"redis-server":           {"/usr/bin/redis-server", "/usr/sbin/redis-server"},
	"redis-cli":              {"/usr/bin/redis-cli"},
	"useradd":                {"/usr/sbin/useradd"},
	"userdel":                {"/usr/sbin/userdel"},
	"usermod":                {"/usr/sbin/usermod"},
	"groupadd":               {"/usr/sbin/groupadd"},
	"gpasswd":                {"/usr/bin/gpasswd"},
	"certbot":                {"/usr/bin/certbot", "/snap/bin/certbot"},
	"git":                    {"/usr/bin/git"},
	"ssh-keygen":             {"/usr/bin/ssh-keygen"},
	"ssh-keyscan":            {"/usr/bin/ssh-keyscan"},
	"pure-pw":                {"/usr/bin/pure-pw", "/usr/sbin/pure-pw"},
	"chpasswd":               {"/usr/sbin/chpasswd"},
	"named-checkconf":        {"/usr/bin/named-checkconf", "/usr/sbin/named-checkconf"},
	"named-checkzone":        {"/usr/bin/named-checkzone", "/usr/sbin/named-checkzone"},
	"rndc":                   {"/usr/sbin/rndc"},
	"postfix":                {"/usr/sbin/postfix"},
	"postconf":               {"/usr/sbin/postconf"},
	"doveconf":               {"/usr/bin/doveconf"},
	"doveadm":                {"/usr/bin/doveadm"},
	"dovecot":                {"/usr/sbin/dovecot"},
	"sievec":                 {"/usr/bin/sievec"},
	"rspamadm":               {"/usr/bin/rspamadm"},
	"curl":                   {"/usr/bin/curl"},
	"apt-get":                {"/usr/bin/apt-get"},
	"debconf-set-selections": {"/usr/bin/debconf-set-selections"},
	"dnf":                    {"/usr/bin/dnf"},
	"rpm":                    {"/usr/bin/rpm"},
	"needs-restarting":       {"/usr/bin/needs-restarting"},
	"firewall-cmd":           {"/usr/bin/firewall-cmd"},
	"firewall-offline-cmd":   {"/usr/bin/firewall-offline-cmd"},
	"getenforce":             {"/usr/sbin/getenforce"},
	"restorecon":             {"/usr/sbin/restorecon", "/sbin/restorecon"},
	"semanage":               {"/usr/sbin/semanage"},
	"setsebool":              {"/usr/sbin/setsebool"},
	"getsebool":              {"/usr/sbin/getsebool"},
	"dpkg-query":             {"/usr/bin/dpkg-query"},
	"apt-cache":              {"/usr/bin/apt-cache"},
	"ufw":                    {"/usr/sbin/ufw"},
	"fail2ban-client":        {"/usr/bin/fail2ban-client"},
	"sshd":                   {"/usr/sbin/sshd"},
	"hostnamectl":            {"/usr/bin/hostnamectl"},
	"timedatectl":            {"/usr/bin/timedatectl"},
	"shutdown":               {"/usr/sbin/shutdown", "/sbin/shutdown"},
	"composer":               {"/usr/local/bin/composer", "/usr/bin/composer"},
	"sh":                     {"/bin/sh", "/usr/bin/sh"},
	"tar":                    {"/usr/bin/tar", "/bin/tar"},
}

// Versioned PHP binaries are resolved on demand ("php8.3", "php-fpm8.3").
var (
	phpID    = regexp.MustCompile(`^php[5-9]\.[0-9]{1,2}$`)
	phpFPMID = regexp.MustCompile(`^php-fpm[5-9]\.[0-9]{1,2}$`)
)

// SystemRunner is the production Runner.
type SystemRunner struct {
	paths   map[string]string
	timeout time.Duration
}

// NewSystemRunner resolves the allowlist against the host filesystem.
func NewSystemRunner(timeout time.Duration) *SystemRunner {
	r := &SystemRunner{paths: map[string]string{}, timeout: timeout}
	for id, cands := range defaultCandidates {
		if p := firstExecutable(cands); p != "" {
			r.paths[id] = p
		}
	}
	return r
}

func firstExecutable(cands []string) string {
	for _, c := range cands {
		if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return c
		}
	}
	return ""
}

func (r *SystemRunner) resolve(id string) (string, error) {
	if p, ok := r.paths[id]; ok {
		return p, nil
	}
	switch {
	case phpID.MatchString(id):
		// Debian: /usr/bin/php8.3; RHEL with Remi: /opt/remi/php83/root/usr/bin/php.
		remi := "/opt/remi/php" + strings.ReplaceAll(strings.TrimPrefix(id, "php"), ".", "") + "/root/usr"
		if p := firstExecutable([]string{"/usr/bin/" + id, remi + "/bin/php"}); p != "" {
			return p, nil
		}
		return "", fmt.Errorf("%w: %s", ErrUnavailable, id)
	case phpFPMID.MatchString(id):
		remi := "/opt/remi/php" + strings.ReplaceAll(strings.TrimPrefix(id, "php-fpm"), ".", "") + "/root/usr"
		if p := firstExecutable([]string{"/usr/sbin/" + id, remi + "/sbin/php-fpm"}); p != "" {
			return p, nil
		}
		return "", fmt.Errorf("%w: %s", ErrUnavailable, id)
	}
	if _, known := defaultCandidates[id]; known {
		return "", fmt.Errorf("%w: %s", ErrUnavailable, id)
	}
	return "", fmt.Errorf("%w: %s", ErrNotAllowed, id)
}

// Available reports whether the command is allowlisted and installed.
func (r *SystemRunner) Available(id string) bool {
	_, err := r.resolve(id)
	return err == nil
}

// Run executes the command as the agent with a minimal environment.
func (r *SystemRunner) Run(ctx context.Context, id string, args ...string) ([]byte, error) {
	return r.RunWith(ctx, id, Opts{}, args...)
}

// RunWith executes the command with options.
func (r *SystemRunner) RunWith(ctx context.Context, id string, o Opts, args ...string) ([]byte, error) {
	bin, err := r.resolve(id)
	if err != nil {
		return nil, err
	}
	if err := checkArgs(id, o, args); err != nil {
		return nil, err
	}
	timeout := r.timeout
	if o.Timeout != 0 {
		timeout = o.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	env := append([]string{}, baseEnv...)
	home := "/root"
	if o.As != nil {
		home = o.As.Home
	}
	env = append(env, "HOME="+home)
	env = append(env, o.Env...)
	dir := "/"
	if o.Dir != "" {
		dir = o.Dir
	}
	var cmd *exec.Cmd
	switch {
	case o.As != nil && r.transient():
		// The agent never changes its own credentials: PID 1 starts the
		// command as the account in a transient, confined unit. Under the
		// agent's NoNewPrivileges=yes sandbox it could not switch UIDs
		// itself (F-16).
		return r.runTransient(ctx, bin, dir, env, o, timeout, args)
	case o.As != nil:
		// No systemd (development containers): switch credentials in the
		// child directly.
		cmd = exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: o.As.UID, Gid: o.As.GID, Groups: o.As.Groups}, Setsid: true}
	default:
		cmd = exec.CommandContext(ctx, bin, args...)
		cmd.Env = env
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	cmd.Stdin = o.Stdin
	var buf limitedBuffer
	buf.limit = maxOutput
	var w io.Writer = &buf
	if o.Output != nil {
		w = io.MultiWriter(&buf, o.Output)
	}
	cmd.Stdout = w
	cmd.Stderr = w
	if o.Stdout != nil {
		cmd.Stdout = o.Stdout
	}
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	return buf.Bytes(), err
}

var baseEnv = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0", "DEBIAN_FRONTEND=noninteractive"}

// transient reports whether commands as other accounts go through PID 1.
func (r *SystemRunner) transient() bool {
	if _, ok := r.paths["systemd-run"]; !ok {
		return false
	}
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

func transientUnit() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "whacp-run-" + hex.EncodeToString(b[:]) + ".service"
}

// transientHardening confines commands run as a website account: no
// capabilities at all, no privilege gain through setuid binaries, the
// system read-only and no kernel or clock changes. /tmp is the host's, as
// for the site's PHP-FPM pool and cron jobs (a private one would hide
// paths callers pass in).
//
// RestrictSUIDSGID is deliberately not set: its seccomp filter answers
// openat2() with ENOSYS, and tar 1.35 on Ubuntu 26.04 (systemd 259)
// extracts with openat2(RESOLVE_BENEATH) without falling back, so every
// restore failed. It only stops the account creating setuid/setgid files,
// which the account can do anyway through SFTP, PHP-FPM or cron, and which
// grant nothing inside the unit (NoNewPrivileges). Archives extracted as
// the account have setuid/setgid bits removed afterwards (host.StripSetID).
var transientHardening = []string{
	"NoNewPrivileges=yes",
	"CapabilityBoundingSet=",
	"ProtectSystem=full",
	"ProtectKernelTunables=yes",
	"ProtectKernelModules=yes",
	"ProtectKernelLogs=yes",
	"ProtectControlGroups=yes",
	"ProtectClock=yes",
	"RestrictRealtime=yes",
	"LockPersonality=yes",
	"KillMode=control-group",
	"TimeoutStopSec=5",
}

// pipeWrapper runs as the account inside the unit: it opens the named
// pipes (after systemd has dropped to the account, so neither PID 1 nor
// root opens them) and replaces itself with the command. Its arguments are
// the stdin, stdout and stderr paths, then the command.
const pipeWrapper = `i=$1 o=$2 e=$3; shift 3; exec "$@" <"$i" >"$o" 2>"$e"`

// TransientArgs builds the systemd-run arguments that start bin as the
// account through sh (the pipe wrapper). Standard input, output and error
// are named pipes the wrapper opens (stdin "" means /dev/null): passing
// the agent's descriptors over D-Bus (--pipe) is refused by SELinux on
// RHEL, and so is PID 1 opening them (StandardOutput=file:). systemd-run
// --wait exits with the command's status.
func TransientArgs(unit, sh, bin, dir string, env []string, as Credential, timeout time.Duration, stdin, stdout, stderr string, binds []string, args []string) []string {
	a := []string{"--quiet", "--wait", "--collect", "--service-type=exec", "--unit=" + unit,
		fmt.Sprintf("--uid=%d", as.UID), fmt.Sprintf("--gid=%d", as.GID), "--working-directory=" + dir,
		"--property=StandardInput=null"}
	if stdin == "" {
		stdin = "/dev/null"
	}
	if len(as.Groups) > 0 {
		g := make([]string, len(as.Groups))
		for i, id := range as.Groups {
			g[i] = fmt.Sprint(id)
		}
		a = append(a, "--property=SupplementaryGroups="+strings.Join(g, " "))
	}
	for _, p := range transientHardening {
		a = append(a, "--property="+p)
	}
	if timeout > 0 {
		// A backstop only: the agent's own deadline stops the unit first and
		// reports the timeout (when systemd stops it, systemd-run --wait can
		// exit 0 for the SIGTERM).
		a = append(a, fmt.Sprintf("--property=RuntimeMaxSec=%d", int((timeout+time.Second-1)/time.Second)+10))
	}
	for _, b := range binds {
		a = append(a, "--property=BindReadOnlyPaths="+b)
	}
	for _, e := range env {
		a = append(a, "--setenv="+e)
	}
	a = append(a, "--", sh, "-c", pipeWrapper, "whacp-run", stdin, stdout, stderr, bin)
	a = append(a, args...)
	for i := len(a) - len(args) - 7; i < len(a); i++ {
		// systemd expands $VAR in command lines; "$$" is a literal "$".
		a[i] = strings.ReplaceAll(a[i], "$", "$$")
	}
	return a
}

// SendmailShim is WhaCP's sendmail program (the agent binary, which
// behaves as sendmail when started under that name). Inside the units of
// commands run as an account it replaces the distribution's sendmail:
// Postfix's hands mail to setgid postdrop, which cannot work under
// NoNewPrivileges and retries forever (F-25). Empty: no replacement.
var SendmailShim string

// sendmailPaths are where programs look for sendmail.
var sendmailPaths = []string{"/usr/sbin/sendmail", "/usr/lib/sendmail", "/usr/bin/sendmail"}

// sendmailBinds mounts SendmailShim over each sendmail on this machine
// (inside the unit only; the files themselves are not changed). Paths that
// lead to the same file are mounted once.
func sendmailBinds() []string {
	if SendmailShim == "" {
		return nil
	}
	var binds []string
	seen := map[string]bool{}
	for _, p := range sendmailPaths {
		real, err := filepath.EvalSymlinks(p)
		if err != nil || seen[real] {
			continue
		}
		if fi, err := os.Stat(real); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		seen[real] = true
		binds = append(binds, SendmailShim+":"+real)
	}
	return binds
}

// RunDir holds the named pipes of commands run as other accounts. Each
// command gets its own root-owned directory (0711) that no account can
// list or change; only its pipes (0600) belong to the account, which can
// open them but never rename or replace them. RunDir is outside the
// agent's runtime directory, which website accounts cannot enter.
var RunDir = "/run/whacp-run"

// runTransient runs bin as o.As in a transient unit, streaming standard
// input and output through named pipes in RunDir.
func (r *SystemRunner) runTransient(ctx context.Context, bin, dir string, env []string, o Opts, timeout time.Duration, args []string) ([]byte, error) {
	sh, err := r.resolve("sh")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(RunDir, 0o711); err != nil {
		return nil, err
	}
	if err := os.Chmod(RunDir, 0o711); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(RunDir, "run-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o711); err != nil {
		return nil, err
	}
	in, out, errp := "", filepath.Join(tmp, "out"), filepath.Join(tmp, "err")
	if o.Stdin != nil {
		in = filepath.Join(tmp, "in")
	}
	for _, f := range []string{in, out, errp} {
		if f != "" {
			if err := syscall.Mkfifo(f, 0o600); err != nil {
				return nil, err
			}
			if err := os.Lchown(f, int(o.As.UID), int(o.As.GID)); err != nil {
				return nil, err
			}
		}
	}
	var buf limitedBuffer
	buf.limit = maxOutput
	var w io.Writer = &buf
	if o.Output != nil {
		w = io.MultiWriter(&buf, o.Output)
	}
	w = &lockedWriter{w: w}
	stdout := w
	if o.Stdout != nil {
		stdout = o.Stdout
	}
	unit := transientUnit()
	cmd := exec.CommandContext(ctx, r.paths["systemd-run"], TransientArgs(unit, sh, bin, dir, env, *o.As, timeout, in, out, errp, sendmailBinds(), args)...)
	cmd.Env = baseEnv
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = w, w // systemd-run's own messages
	cmd.Cancel = func() error {
		// Stopping systemd-run alone would leave the unit running.
		if sc, ok := r.paths["systemctl"]; ok {
			stop := exec.Command(sc, "stop", "--no-block", unit)
			stop.Env = baseEnv
			_ = stop.Run()
		}
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 5 * time.Second
	var wg sync.WaitGroup
	// Each goroutine blocks opening its end of a pipe until the command
	// opens the other; opened is closed once that open has returned.
	type pipe struct {
		path   string
		flag   int // the counterpart end, to release a goroutine
		opened chan struct{}
	}
	var pipes []pipe
	copyFrom := func(path string, dst io.Writer) {
		p := pipe{path, os.O_WRONLY | syscall.O_NONBLOCK, make(chan struct{})}
		pipes = append(pipes, p)
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := openPipe(path, os.O_RDONLY)
			close(p.opened)
			if err == nil {
				io.Copy(dst, f)
				f.Close()
			}
		}()
	}
	copyFrom(out, stdout)
	copyFrom(errp, w)
	if in != "" {
		p := pipe{in, os.O_RDONLY | syscall.O_NONBLOCK, make(chan struct{})}
		pipes = append(pipes, p)
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := openPipe(in, os.O_WRONLY)
			close(p.opened)
			if err == nil {
				io.Copy(f, o.Stdin)
				f.Close()
			}
		}()
	}
	runErr := cmd.Run()
	if runErr == nil && ctx.Err() != nil {
		runErr = fmt.Errorf("stopped: %w", ctx.Err())
	}
	// If the command never opened a pipe (it failed to start), release the
	// goroutine still waiting to open it: hold the other end open until its
	// open has returned. (A single open-and-close could come before the
	// goroutine's open and be missed.)
	for _, p := range pipes {
		releasePipe(p.path, p.flag, p.opened)
	}
	wg.Wait()
	return buf.Bytes(), runErr
}

// releasePipe opens the other end of a pipe (flag) until opened is
// closed, then closes it. Opening a pipe's write end without blocking
// fails until a reader is waiting, so that is retried.
func releasePipe(path string, flag int, opened <-chan struct{}) {
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-opened:
			return
		case <-deadline:
			return
		default:
		}
		if f, err := openPipe(path, flag); err == nil {
			select {
			case <-opened:
			case <-deadline:
			}
			f.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// openPipe opens one of the command's named pipes, never following a
// symbolic link and refusing anything that is not a pipe.
func openPipe(path string, flag int) (*os.File, error) {
	f, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		f.Close()
		return nil, fmt.Errorf("%s is not a named pipe", path)
	}
	return f, nil
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func checkArgs(id string, o Opts, args []string) error {
	for _, a := range args {
		if strings.ContainsRune(a, 0) || (id != "sh" && strings.ContainsAny(a, "\n\r")) {
			return fmt.Errorf("invalid argument for %s", id)
		}
	}
	for _, e := range o.Env {
		if !strings.Contains(e, "=") || strings.ContainsAny(e, "\x00\n") {
			return fmt.Errorf("invalid environment entry for %s", id)
		}
	}
	// A shell, and tar (which reads and writes website files), only ever
	// run as an unprivileged account.
	if (id == "sh" || id == "tar") && (o.As == nil || o.As.UID < 1000 || o.As.GID < 1000) {
		return fmt.Errorf("%w: %s may only run as an unprivileged account", ErrNotAllowed, id)
	}
	return nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.Len()
	if room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// FakeRunner is a test double returning canned output keyed by
// "id arg1 arg2 ...".
type FakeRunner struct {
	Outputs map[string]string
	Errors  map[string]error
	Calls   []string
	Stdins  []string
	Opts    []Opts
	// Handler, when set, handles calls not found in Outputs.
	Handler func(id string, o Opts, args []string) (string, error)
	// Unavailable marks command IDs as not installed.
	Unavailable map[string]bool
	mu          sync.Mutex
}

// Available reports true if any output is registered for id (or a handler exists).
func (f *FakeRunner) Available(id string) bool {
	if f.Unavailable[id] {
		return false
	}
	if f.Handler != nil {
		return true
	}
	for k := range f.Outputs {
		if k == id || strings.HasPrefix(k, id+" ") {
			return true
		}
	}
	return false
}

// Run returns the canned output for the call.
func (f *FakeRunner) Run(ctx context.Context, id string, args ...string) ([]byte, error) {
	return f.RunWith(ctx, id, Opts{}, args...)
}

// RunWith records stdin/options and returns the canned output.
func (f *FakeRunner) RunWith(_ context.Context, id string, o Opts, args ...string) ([]byte, error) {
	if err := checkArgs(id, o, args); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimSpace(id + " " + strings.Join(args, " "))
	f.Calls = append(f.Calls, key)
	f.Opts = append(f.Opts, o)
	if o.Stdin != nil {
		b, _ := io.ReadAll(o.Stdin)
		f.Stdins = append(f.Stdins, string(b))
	}
	if err, ok := f.Errors[key]; ok {
		out := f.Outputs[key]
		if o.Output != nil {
			io.WriteString(o.Output, out)
		}
		return []byte(out), err
	}
	out, ok := f.Outputs[key]
	if !ok {
		if f.Handler != nil {
			s, err := f.Handler(id, o, args)
			if o.Stdout != nil {
				io.WriteString(o.Stdout, s)
				return nil, err
			}
			if o.Output != nil {
				io.WriteString(o.Output, s)
			}
			return []byte(s), err
		}
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, id)
	}
	if o.Stdout != nil {
		io.WriteString(o.Stdout, out)
		return nil, nil
	}
	if o.Output != nil {
		io.WriteString(o.Output, out)
	}
	return []byte(out), nil
}

// Called reports whether a call with this exact key was made.
func (f *FakeRunner) Called(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Calls {
		if c == key {
			return true
		}
	}
	return false
}

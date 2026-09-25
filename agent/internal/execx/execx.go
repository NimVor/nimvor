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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0", "DEBIAN_FRONTEND=noninteractive"}
	home := "/root"
	if o.As != nil {
		home = o.As.Home
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: o.As.UID, Gid: o.As.GID, Groups: o.As.Groups}, Setsid: true}
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	cmd.Env = append(cmd.Env, "HOME="+home)
	cmd.Env = append(cmd.Env, o.Env...)
	cmd.Dir = "/"
	if o.Dir != "" {
		cmd.Dir = o.Dir
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

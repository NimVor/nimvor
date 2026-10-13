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
	// Slice runs the command in the account's slice (resource limits).
	Slice string
	// Capabilities (root only, see Opts.Capabilities) is the unit's
	// capability bounding set instead of none.
	Capabilities []string
	// Package runs a package transaction as root outside the agent's
	// sandbox (see packages.go).
	Package bool
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
	// Capabilities runs the command as root in a transient unit started by
	// PID 1 whose capability bounding set is exactly these: for the few
	// commands that need a capability the agent itself does not hold
	// (CAP_SYS_ADMIN for disk quotas). Only the commands in
	// PrivilegedCommands may, only with PrivilegedCapabilities, never with
	// As. The agent's own capability set is unchanged.
	Capabilities []string
}

// PrivilegedCommands may run with Opts.Capabilities.
var PrivilegedCommands = map[string]bool{"setquota": true, "repquota": true, "quotaon": true, "quotacheck": true, "mount": true, "sysctl": true, "whacp-agent": true, "strace": true}

// PrivilegedCapabilities are the capabilities Opts.Capabilities may name.
var PrivilegedCapabilities = map[string]bool{"CAP_SYS_ADMIN": true, "CAP_DAC_READ_SEARCH": true, "CAP_SYS_PTRACE": true, "CAP_NET_ADMIN": true}

// SysctlFile is the only file sysctl may load with added capabilities
// (the security overview's kernel settings).
const SysctlFile = "/etc/sysctl.d/90-whacp-hardening.conf"

var (
	sysctlSetRe   = regexp.MustCompile(`^(kernel|fs|net)\.[a-z0-9_.]{1,80}=-?[0-9]{1,10}$`)
	remountOptRe  = regexp.MustCompile(`^(usrquota|grpquota|prjquota|usrjquota=aquota\.user|grpjquota=aquota\.group|jqfmt=vfsv[01]|hidepid=[02]|gid=[0-9]{1,10})$`)
	mountTargetRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]*$`)
	pidRe         = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
)

// privilegedArgs: what the commands run with added capabilities may be
// given (they run as root with CAP_SYS_ADMIN): sysctl only loads WhaCP's
// file or sets one numeric kernel value; mount only remounts with quota or
// /proc visibility options.
func privilegedArgs(id string, args []string) bool {
	switch id {
	case "sysctl":
		return (len(args) == 2 && args[0] == "-p" && args[1] == SysctlFile) || (len(args) == 2 && args[0] == "-w" && sysctlSetRe.MatchString(args[1]))
	case "whacp-agent":
		// The process manager's read-only scan modes only.
		return (len(args) == 1 && args[0] == "-proc-scan") || (len(args) == 2 && args[0] == "-proc-detail" && pidRe.MatchString(args[1]))
	case "strace":
		// Watching one process (the process manager's trace) only.
		return len(args) == 7 && strings.Join(args[:6], " ") == "-f -tt -qq -s 200 -p" && pidRe.MatchString(args[6])
	case "mount":
		if len(args) != 3 || args[0] != "-o" || !strings.HasPrefix(args[1], "remount,") || !mountTargetRe.MatchString(args[2]) || strings.Contains(args[2], "..") {
			return false
		}
		for _, o := range strings.Split(strings.TrimPrefix(args[1], "remount,"), ",") {
			if !remountOptRe.MatchString(o) {
				return false
			}
		}
		return true
	}
	return true
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
	"systemctl":   {"/usr/bin/systemctl", "/bin/systemctl"},
	"systemd-run": {"/usr/bin/systemd-run", "/bin/systemd-run"},
	// The agent itself (the process manager's privileged scan).
	"whacp-agent": {"/usr/local/bin/whacp-agent"},
	"strace":      {"/usr/bin/strace"},
	"journalctl":  {"/usr/bin/journalctl", "/bin/journalctl"},
	"nginx":       {"/usr/sbin/nginx", "/usr/bin/nginx"},
	"ip":          {"/usr/sbin/ip", "/sbin/ip", "/usr/bin/ip", "/bin/ip"},
	// PAM's password checker (web terminal sign-in; password on stdin).
	"unix_chkpwd":  {"/usr/sbin/unix_chkpwd", "/sbin/unix_chkpwd"},
	"mysqld":       {"/usr/sbin/mysqld", "/usr/bin/mysqld", "/usr/libexec/mysqld"},
	"mariadbd":     {"/usr/sbin/mariadbd", "/usr/bin/mariadbd", "/usr/libexec/mariadbd"},
	"mysql":        {"/usr/bin/mariadb", "/usr/bin/mysql"},
	"mysqldump":    {"/usr/bin/mariadb-dump", "/usr/bin/mysqldump"},
	"mongosh":      {"/usr/bin/mongosh", "/usr/local/bin/mongosh"},
	"mongodump":    {"/usr/bin/mongodump", "/usr/local/bin/mongodump"},
	"mongorestore": {"/usr/bin/mongorestore", "/usr/local/bin/mongorestore"},
	// Valkey (RHEL 10) replaces Redis with the same protocol and options.
	"redis-server":           {"/usr/bin/redis-server", "/usr/sbin/redis-server", "/usr/bin/valkey-server"},
	"redis-cli":              {"/usr/bin/redis-cli", "/usr/bin/valkey-cli"},
	"mysqladmin":             {"/usr/bin/mysqladmin", "/usr/bin/mariadb-admin"},
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
	"postqueue":              {"/usr/sbin/postqueue"},
	"postsuper":              {"/usr/sbin/postsuper"},
	"postcat":                {"/usr/sbin/postcat"},
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
	"dpkg":                   {"/usr/bin/dpkg"},
	"phpenmod":               {"/usr/sbin/phpenmod"},
	"phpdismod":              {"/usr/sbin/phpdismod"},
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
	"perl":                   {"/usr/bin/perl"},
	"clamscan":               {"/usr/bin/clamscan"},
	"ffmpeg":                 {"/usr/bin/ffmpeg"},
	"dpkg-statoverride":      {"/usr/bin/dpkg-statoverride"},
	// Disk quotas (the quota package); mount only remounts with quota
	// options, grubby adds the XFS root quota boot option.
	"setquota":   {"/usr/sbin/setquota", "/sbin/setquota"},
	"repquota":   {"/usr/sbin/repquota", "/sbin/repquota"},
	"quotaon":    {"/usr/sbin/quotaon", "/sbin/quotaon"},
	"quotacheck": {"/usr/sbin/quotacheck", "/sbin/quotacheck"},
	"mount":      {"/usr/bin/mount", "/bin/mount"},
	// Kernel settings of the security overview (privilegedArgs).
	"sysctl": {"/usr/sbin/sysctl", "/sbin/sysctl"},
	"grubby": {"/usr/sbin/grubby", "/sbin/grubby"},
	// Building the ModSecurity connector (only as WhaCP's build account).
	"make": {"/usr/bin/make"},
	// SMTP restrictions: only WhaCP's own table and rules file (checkArgs).
	"nft": {"/usr/sbin/nft", "/sbin/nft"},
}

// NftRulesFile, NftHulkFile and NftEgressFile are the only files nft may
// load (SMTP restrictions; brute force protection blocks; website
// accounts' outgoing connections).
const (
	NftRulesFile  = "/etc/whacp/smtp-restrict.nft"
	NftHulkFile   = "/etc/whacp/hulk.nft"
	NftEgressFile = "/etc/whacp/egress.nft"
	// NftFloodFile: the firewall's flood and connection limits (Security →
	// Firewall → Protection), loaded by its own unit.
	NftFloodFile = "/etc/whacp/flood.nft"
)

// nftAllowed are the only nft command lines WhaCP runs.
func nftAllowed(args []string) bool {
	switch strings.Join(args, " ") {
	case "-c -f " + NftRulesFile, "-f " + NftRulesFile, "list table inet whacp_smtp", "delete table inet whacp_smtp", "--version",
		"-c -f " + NftHulkFile, "-f " + NftHulkFile, "list table inet whacp_hulk", "delete table inet whacp_hulk",
		"-c -f " + NftEgressFile, "-f " + NftEgressFile, "list table inet whacp_egress", "delete table inet whacp_egress",
		"-c -f " + NftFloodFile, "list table inet whacp_flood":
		return true
	}
	return false
}

// Versioned PHP binaries are resolved on demand ("php8.3", "php-fpm8.3").
var (
	phpID    = regexp.MustCompile(`^php[5-9]\.[0-9]{1,2}$`)
	phpFPMID = regexp.MustCompile(`^php-fpm[5-9]\.[0-9]{1,2}$`)
)

// SystemRunner is the production Runner.
type SystemRunner struct {
	mu      sync.RWMutex
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

func isExecutable(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

func firstExecutable(cands []string) string {
	for _, c := range cands {
		if isExecutable(c) {
			return c
		}
	}
	return ""
}

// path returns the allowlisted command's binary. The allowlist is looked up
// again when the binary is not where it was: a program installed after the
// agent started (firewalld or UFW from the Packages page, ClamAV, …) is
// found without restarting the agent, and one removed is reported missing.
func (r *SystemRunner) path(id string) (string, bool) {
	cands, known := defaultCandidates[id]
	r.mu.RLock()
	p, ok := r.paths[id]
	r.mu.RUnlock()
	if ok && (!known || isExecutable(p)) {
		return p, true
	}
	if !known {
		return "", false
	}
	p = firstExecutable(cands)
	r.mu.Lock()
	defer r.mu.Unlock()
	if p == "" {
		delete(r.paths, id)
		return "", false
	}
	r.paths[id] = p
	return p, true
}

func (r *SystemRunner) resolve(id string) (string, error) {
	if p, ok := r.path(id); ok {
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

func (r *SystemRunner) mustPath(id string) string {
	p, _ := r.path(id)
	return p
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
	if o.As == nil && len(o.Capabilities) == 0 && PackageTransaction(id, args) {
		// One package transaction at a time, also across agent restarts.
		release, err := acquirePackageSlot(ctx, o.Output)
		if err != nil {
			return nil, err
		}
		defer release()
		if r.transient() {
			if err := r.waitPackageUnits(ctx, o.Output); err != nil {
				return nil, err
			}
			o.As = &Credential{Home: "/root", Package: true}
			return r.runTransient(ctx, bin, dir, env, o, timeout, args)
		}
		// No systemd (development containers): no sandbox to leave.
	}
	var cmd *exec.Cmd
	switch {
	case len(o.Capabilities) > 0 && r.transient():
		// Through PID 1: the agent keeps its own narrow capability set.
		o.As = &Credential{Home: "/root", Capabilities: o.Capabilities}
		return r.runTransient(ctx, bin, dir, env, o, timeout, args)
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
	if _, ok := r.path("systemd-run"); !ok {
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

// PrivilegedHardening confines a root command given capabilities (see
// Opts.Capabilities): only those capabilities, no privilege gain through
// setuid binaries, and no private mount namespace (a quota remount must
// change the system's mount, not a copy).
func PrivilegedHardening(caps []string) []string {
	return []string{
		"NoNewPrivileges=yes",
		"CapabilityBoundingSet=" + strings.Join(caps, " "),
		"RestrictRealtime=yes",
		"LockPersonality=yes",
		"KillMode=control-group",
		"TimeoutStopSec=5",
	}
}

// TransientHardening is the sandbox of commands run as an account (a
// copy; the web terminal applies it to its sessions too).
func TransientHardening() []string { return append([]string(nil), transientHardening...) }

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
	hardening := transientHardening
	switch {
	case as.Package:
		hardening = packageHardening
		binds = nil
		a = append(a, "--description=WhaCP package transaction")
	case len(as.Capabilities) > 0:
		hardening = PrivilegedHardening(as.Capabilities)
		binds = nil
	}
	for _, p := range hardening {
		a = append(a, "--property="+p)
	}
	if as.Slice != "" {
		a = append(a, "--slice="+as.Slice)
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
	// A package transaction's output keeps being read after the agent
	// stops waiting (a closed pipe would kill dpkg or rpm half-way), into
	// nothing once detached.
	dw := &detachWriter{w: w}
	if o.As.Package {
		w = dw
	}
	stdout := w
	if o.Stdout != nil {
		stdout = o.Stdout
	}
	unit := transientUnit()
	if o.As.Package {
		unit = PackageUnitPrefix + strings.TrimPrefix(unit, "whacp-run-")
	}
	cmd := exec.CommandContext(ctx, r.mustPath("systemd-run"), TransientArgs(unit, sh, bin, dir, env, *o.As, timeout, in, out, errp, sendmailBinds(), args)...)
	cmd.Env = baseEnv
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = w, w // systemd-run's own messages
	cmd.Cancel = func() error {
		if o.As.Package {
			// Never stop a package transaction half-way (a broken dpkg or
			// RPM state is worse than a late one): the unit finishes on
			// its own, and the next transaction waits for it.
			return cmd.Process.Kill()
		}
		// Stopping systemd-run alone would leave the unit running.
		if sc, ok := r.path("systemctl"); ok {
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
	if o.As.Package && ctx.Err() != nil {
		runErr = fmt.Errorf("stopped waiting; the package transaction continues in %s: %w", unit, ctx.Err())
	}
	// If the command never opened a pipe (it failed to start), release the
	// goroutine still waiting to open it: hold the other end open until its
	// open has returned. (A single open-and-close could come before the
	// goroutine's open and be missed.)
	for _, p := range pipes {
		releasePipe(p.path, p.flag, p.opened)
	}
	if o.As.Package && ctx.Err() != nil {
		dw.detach()
		return dw.snapshot(&buf), runErr
	}
	wg.Wait()
	return buf.Bytes(), runErr
}

// detachWriter forwards writes until detached, then discards them.
type detachWriter struct {
	mu       sync.Mutex
	w        io.Writer
	detached bool
}

func (d *detachWriter) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.detached {
		return len(p), nil
	}
	return d.w.Write(p)
}

func (d *detachWriter) detach() {
	d.mu.Lock()
	d.detached = true
	d.mu.Unlock()
}

// snapshot copies b once no more writes reach it.
func (d *detachWriter) snapshot(b *limitedBuffer) []byte {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]byte(nil), b.Bytes()...)
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
	// A shell, perl (AWStats), tar (which reads and writes website files)
	// and clamscan (which reads untrusted files) only ever run as an
	// unprivileged account.
	if (id == "sh" || id == "perl" || id == "tar" || id == "clamscan" || id == "make") && (o.As == nil || o.As.UID < 1000 || o.As.GID < 1000) {
		return fmt.Errorf("%w: %s may only run as an unprivileged account", ErrNotAllowed, id)
	}
	if id == "nft" && !nftAllowed(args) {
		return fmt.Errorf("%w: nft %s", ErrNotAllowed, strings.Join(args, " "))
	}
	if len(o.Capabilities) > 0 {
		if !PrivilegedCommands[id] || o.As != nil {
			return fmt.Errorf("%w: %s may not run with added capabilities", ErrNotAllowed, id)
		}
		for _, c := range o.Capabilities {
			if !PrivilegedCapabilities[c] {
				return fmt.Errorf("%w: capability %s", ErrNotAllowed, c)
			}
		}
		if !privilegedArgs(id, args) {
			return fmt.Errorf("%w: %s %s", ErrNotAllowed, id, strings.Join(args, " "))
		}
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

// Index is the position of the first call with this exact key (-1: none).
func (f *FakeRunner) Index(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, c := range f.Calls {
		if c == key {
			return i
		}
	}
	return -1
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

// ExitCode reports whether err is the command exiting with status code
// (also through systemd-run --wait, which passes the status on).
func ExitCode(err error, code int) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == code
}

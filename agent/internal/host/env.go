// Package host bundles what modifying actions need: the confined writer,
// the command runner, the job manager and helpers implementing the
// validate-then-reload discipline.
package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/changeset"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/discovery"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/logging"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
	"github.com/mchkhawarshahzad/whacp/agent/internal/users"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Env is shared by all modifying actions.
type Env struct {
	Cfg       config.Config
	Root      filesystem.Root
	Writer    agentfs.Writer
	Platform  platform.Platform
	M         platform.Managed
	Runner    execx.Runner
	Jobs      *jobs.Manager
	Log       *slog.Logger
	Discovery discovery.Service
	Now       func() time.Time
}

// New builds an Env with the write policy derived from the platform
// layout and configuration.
func New(cfg config.Config, root filesystem.Root, p platform.Platform, r execx.Runner, j *jobs.Manager, log *slog.Logger) *Env {
	m := p.Managed()
	if cfg.WebUser != "" {
		m.WebUser = cfg.WebUser
	}
	patterns := []string{
		filepath.Join(m.NginxAvailable, "whacp-*.conf"),
		filepath.Join(m.NginxEnabled, "whacp-*.conf"),
		filepath.Join(m.PoolDir("*"), "whacp-*.conf"),
		filepath.Join(m.CronDir, "whacp-*"),
		filepath.Join(m.UnitDir, "whacp-worker-*.service"),
		filepath.Join(m.UnitDir, "whacp-fpm-*.service"),
		"/etc/whacp-fpm/**",
		"/etc/whacp-cron/**",
		m.SSHDropIn,
		m.SFTPDropIn,
		m.SFTPKeysDir + "/**",
		// Jailed shells: the sandboxing login shell and, where AppArmor
		// restricts user namespaces, the group-only bubblewrap copy.
		"/usr/local/sbin/whacp-jailsh",
		"/usr/local/lib/whacp/bwrap",
		// WordPress toolkit: the pinned, checksum-verified WP-CLI.
		"/usr/local/lib/whacp",
		"/usr/local/lib/whacp/wp-cli.phar",
		// Disk quotas: only the quota options of the websites' file system entry.
		"/etc/fstab",
		m.FTPDir + "/**",
		m.DNSZoneDir + "/**",
		m.DNSConfFile,
		m.MailDir + "/**",
		m.VmailDir + "/**",
		m.PostfixDir + "/main.cf",
		m.PostfixDir + "/master.cf",
		m.DovecotAuthConf,
		m.DovecotDropIn,
		filepath.Join(filepath.Dir(m.DovecotDropIn), "99-whacp-sieve.conf"),
		filepath.Join(filepath.Dir(m.DovecotDropIn), "99-whacp-tuning.conf"),
		"/etc/rspamd/local.d/*",
		// MariaDB/MySQL server settings WhaCP manages (its own drop-in only).
		"/etc/mysql/mariadb.conf.d/zz-whacp.cnf",
		"/etc/mysql/mysql.conf.d/zz-whacp.cnf",
		"/etc/my.cnf.d/zz-whacp.cnf",
		m.WebmailDir + "/**",
		"/etc/tmpfiles.d/whacp-*.conf",
		m.NginxAvailable,
		filepath.Join(filepath.Dir(m.NginxAvailable), "whacp-tls.conf"),
		m.WebmailData + "/**",
		filepath.Join(m.UnitDir, "whacp-opendkim.service"),
		filepath.Join(m.UnitDir, "whacp-ftpd.service"),
		filepath.Join(m.UnitDir, "whacp-greylist.service"),
		filepath.Join(m.UnitDir, "whacp-greylist.socket"),
		m.Fail2banJail,
		m.ACMERoot + "/**",
		cfg.SitesRoot + "/**",
		cfg.StateDir + "/**",
		cfg.BackupDir + "/**",
		WorkersDir + "/**",
		// RHEL: switching a PHP extension edits its php.d file; WhaCP's
		// php.ini settings are php.d/99-whacp.ini there.
		"/etc/opt/remi/php*/php.d/*.ini",
		"/etc/php/*/fpm/conf.d/99-whacp.ini",
		"/etc/php/*/cli/conf.d/99-whacp.ini",
		// Website statistics: WhaCP's own AWStats configurations.
		"/etc/awstats/awstats.whacp-*.conf",
	}
	return &Env{
		Cfg: cfg, Root: root, Platform: p, M: m, Runner: r, Jobs: j, Log: log,
		Writer:    agentfs.Writer{Root: root, Policy: agentfs.Policy{Patterns: patterns}},
		Discovery: discovery.Service{Root: root, Platform: p, Runner: r},
		Now:       func() time.Time { return time.Now().UTC() },
	}
}

// Changes starts a changeset whose originals are kept under the state dir.
func (e *Env) Changes(label string) *changeset.Set {
	return changeset.New(e.Writer, filepath.Join(e.Cfg.StateDir, "config-backups"), label)
}

// Decode strictly decodes params and maps validation errors.
func Decode(params json.RawMessage, v any) error {
	return actions.DecodeParams(params, v)
}

// Invalid converts a validation error into an action error.
func Invalid(err error) error {
	var ve *validate.Error
	if errors.As(err, &ve) {
		return actions.InvalidParams(ve.Error())
	}
	return err
}

// Output trims and redacts command output for Error.Details.
func Output(b []byte) string {
	s := strings.TrimSpace(logging.Redact(string(b)))
	if len(s) > 4000 {
		s = s[:4000] + "…"
	}
	return s
}

// Why explains a failed command: its output, or when it printed nothing,
// the error itself (exit status, not found, timed out), which would
// otherwise leave the reason blank.
func Why(out []byte, err error) string {
	if s := Output(out); s != "" {
		return s
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

// IPv6 reports whether the host has IPv6 enabled (listening on [::] fails
// otherwise).
func (e *Env) IPv6() bool {
	// procfs reports size 0 for every file, so read the content.
	b, err := os.ReadFile(filepath.Join(e.Cfg.ProcDir, "net/if_inet6"))
	return err == nil && len(bytes.TrimSpace(b)) > 0
}

// PrecheckNginx verifies the current configuration before WhaCP changes it,
// so a pre-existing problem is not blamed on (or hidden by) the change.
func (e *Env) PrecheckNginx(ctx context.Context) error {
	out, err := e.Runner.Run(ctx, "nginx", "-t")
	if err != nil {
		return actions.Failed("The current Nginx configuration is already invalid, before any change by WhaCP. Fix it first (run nginx -t on the server); WhaCP did not change anything.", Output(out))
	}
	return nil
}

// NginxTest runs "nginx -t" and returns a ValidationError on failure.
func (e *Env) NginxTest(ctx context.Context) error {
	out, err := e.Runner.Run(ctx, "nginx", "-t")
	if err != nil {
		return &changeset.ValidationError{Step: "nginx -t", Output: Output(out), Err: err}
	}
	return nil
}

// FPMTest validates the PHP-FPM configuration of version v.
func (e *Env) FPMTest(ctx context.Context, v string) error {
	id := fmt.Sprintf(e.M.PHPFPMBinary, v)
	if !e.Runner.Available(id) {
		return &changeset.ValidationError{Step: "php-fpm" + v + " -t", Err: fmt.Errorf("PHP %s FPM is not installed", v)}
	}
	out, err := e.Runner.Run(ctx, id, "-t")
	if err != nil {
		return &changeset.ValidationError{Step: "php-fpm" + v + " -t", Output: Output(out), Err: err}
	}
	return nil
}

// NginxAtLeast reports whether the installed Nginx is at least maj.min.patch.
func (e *Env) NginxAtLeast(ctx context.Context, maj, min, patch int) bool {
	out, _ := e.Runner.Run(ctx, "nginx", "-v")
	mm := nginxVersionRe.FindStringSubmatch(string(out))
	if mm == nil {
		return false
	}
	v := [3]int{}
	for i := range v {
		v[i], _ = strconv.Atoi(mm[i+1])
	}
	want := [3]int{maj, min, patch}
	for i := range v {
		if v[i] != want[i] {
			return v[i] > want[i]
		}
	}
	return true
}

var nginxVersionRe = regexp.MustCompile(`nginx/([0-9]+)\.([0-9]+)\.([0-9]+)`)

// UnitActive reports whether a systemd unit is active.
func (e *Env) UnitActive(ctx context.Context, unit string) bool {
	out, _ := e.Runner.Run(ctx, "systemctl", "is-active", "--", unit)
	return strings.TrimSpace(string(out)) == "active"
}

// Systemctl runs a lifecycle verb on a unit.
func (e *Env) Systemctl(ctx context.Context, verb, unit string) error {
	switch verb {
	case "start", "stop", "restart", "reload", "enable", "disable", "try-reload-or-restart":
	default:
		return fmt.Errorf("unsupported systemctl verb %q", verb)
	}
	if err := validate.Unit("unit", unit); err != nil {
		return err
	}
	out, err := e.Runner.Run(ctx, "systemctl", verb, "--", unit)
	if err != nil {
		return fmt.Errorf("systemctl %s %s failed: %s", verb, unit, Output(out))
	}
	return nil
}

// JournalTail returns the last lines a unit logged (for error details;
// empty when the journal is unavailable).
func (e *Env) JournalTail(ctx context.Context, unit string, lines int) string {
	if validate.Unit("unit", unit) != nil || !e.Runner.Available("journalctl") {
		return ""
	}
	out, err := e.Runner.Run(ctx, "journalctl", "--no-pager", "-o", "cat", "-n", fmt.Sprint(lines), "-u", unit)
	if err != nil {
		return ""
	}
	return Output(out)
}

// Describe lists each path's mode, owner and SELinux label (for error
// details when a service cannot read what WhaCP wrote).
func (e *Env) Describe(paths ...string) string {
	var b strings.Builder
	for _, hp := range paths {
		real, err := e.Root.Path(hp)
		if err != nil {
			continue
		}
		fi, err := os.Lstat(real)
		if err != nil {
			fmt.Fprintf(&b, "%s: %v\n", hp, err)
			continue
		}
		owner := ""
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			owner = fmt.Sprintf("%d:%d", st.Uid, st.Gid)
		}
		label := make([]byte, 256)
		n, _ := syscall.Getxattr(real, "security.selinux", label)
		if n < 0 {
			n = 0
		}
		fmt.Fprintf(&b, "%s %s %s %s\n", fi.Mode(), owner, strings.TrimRight(string(label[:n]), "\x00"), hp)
	}
	return b.String()
}

// DaemonReload reloads systemd unit files.
func (e *Env) DaemonReload(ctx context.Context) error {
	out, err := e.Runner.Run(ctx, "systemctl", "daemon-reload")
	if err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %s", Output(out))
	}
	return nil
}

// ReloadNginx tests the configuration and reloads Nginx if it is running.
func (e *Env) ReloadNginx(ctx context.Context) error {
	if err := e.NginxTest(ctx); err != nil {
		return err
	}
	if !e.UnitActive(ctx, e.M.NginxUnit) {
		e.Log.Warn("nginx is not running; configuration written but not loaded")
		return nil
	}
	return e.Systemctl(ctx, "reload", e.M.NginxUnit)
}

// ReloadFPM reloads (or starts) PHP-FPM for version v after a config test.
func (e *Env) ReloadFPM(ctx context.Context, v string) error {
	if err := e.FPMTest(ctx, v); err != nil {
		return err
	}
	unit := e.M.FPMUnit(v)
	if e.UnitActive(ctx, unit) {
		return e.Systemctl(ctx, "reload", unit)
	}
	return e.Systemctl(ctx, "start", unit)
}

// Accounts reads /etc/passwd through the root.
func (e *Env) Accounts() ([]users.User, error) {
	return users.Discover(e.Root, e.Platform.Paths().PasswdFile)
}

// LookupUser returns the account with this name.
func (e *Env) LookupUser(name string) (users.User, error) {
	all, err := e.Accounts()
	if err != nil {
		return users.User{}, err
	}
	for _, u := range all {
		if u.Name == name {
			return u, nil
		}
	}
	return users.User{}, fmt.Errorf("user %s does not exist", name)
}

// Credential returns an execx credential for an unprivileged site user.
func (e *Env) Credential(name string) (*execx.Credential, users.User, error) {
	u, err := e.LookupUser(name)
	if err != nil {
		return nil, u, err
	}
	if u.UID < 1000 || u.UID >= 60000 {
		return nil, u, fmt.Errorf("user %s is a system account; WhaCP only runs commands as regular accounts", name)
	}
	return &execx.Credential{UID: uint32(u.UID), GID: uint32(u.GID), Home: u.Home, Slice: e.AccountSlice(name)}, u, nil
}

// AccountSlice is the slice a system user's processes run in when its
// hosting account has resource limits ("" otherwise). The limits package
// keeps the assignment in the state directory.
func (e *Env) AccountSlice(user string) string {
	b, err := e.Root.ReadFile(filepath.Join(e.Cfg.StateDir, "limits.json"))
	if err != nil {
		return ""
	}
	var st struct {
		Accounts map[string]struct {
			Users []string `json:"users"`
		} `json:"accounts"`
	}
	if json.Unmarshal(b, &st) != nil {
		return ""
	}
	for acct, a := range st.Accounts {
		for _, u := range a.Users {
			if u == user {
				return "whacp-" + acct + ".slice"
			}
		}
	}
	return ""
}

// WebUser returns the account Nginx runs as.
func (e *Env) WebUser() (users.User, error) { return e.LookupUser(e.M.WebUser) }

// FailWithValidation converts changeset validation errors to user-facing
// action errors (with the redacted tool output as details).
func FailWithValidation(what string, err error) error {
	var ve *changeset.ValidationError
	if errors.As(err, &ve) {
		return actions.Failed(fmt.Sprintf("%s: %s rejected the new configuration. The previous configuration was restored; nothing was changed.", what, ve.Step), ve.Output)
	}
	var ae *actions.Error
	if errors.As(err, &ae) {
		return ae
	}
	return actions.Failed(what+": "+logging.Redact(err.Error()), "")
}

// Run is a small helper returning combined output and a redacted error.
func (e *Env) Run(ctx context.Context, id string, args ...string) (string, error) {
	out, err := e.Runner.Run(ctx, id, args...)
	if err != nil {
		return Output(out), fmt.Errorf("%s failed: %s", id, Output(out))
	}
	return string(bytes.TrimSpace(out)), nil
}

// StripSetID removes setuid and setgid bits from everything under dir,
// running as the account that owns it (after extracting an archive as that
// account: backups restored, applications installed).
func (e *Env) StripSetID(ctx context.Context, cred *execx.Credential, dir string) error {
	out, err := e.Runner.RunWith(ctx, "sh", execx.Opts{As: cred, Dir: dir, Timeout: -1},
		"-c", `find "$1" -xdev -perm /6000 ! -type l -exec chmod ug-s {} +`, "strip-setid", dir)
	if err != nil {
		return fmt.Errorf("removing setuid/setgid bits: %v %s", err, Output(out))
	}
	return nil
}

// WorkersDir holds the launcher scripts of background workers. The worker
// runs as the website account, which cannot enter the state directory
// (0700), so the launchers are kept here: the directory is 0711 root (no
// listing) and each launcher is 0640 root:<account's group>, readable by
// its own account only (it carries the worker's environment variables).
const WorkersDir = "/var/lib/whacp-workers"

// TrashDir is the name of the trash folder WhaCP keeps inside the folder
// that holds the data it removes (the websites root, the mail root).
const TrashDir = ".whacp-trash"

// MoveToTrash moves path into <parent>/.whacp-trash/<name>-<time>, a root-only
// folder on the same filesystem as the data, so the move is a rename (the
// websites or mail may be on their own disk) and keeps owners, modes and
// security labels. It returns the new location.
func (e *Env) MoveToTrash(path, parent, name string) (string, error) {
	trash := filepath.Join(parent, TrashDir)
	if _, err := e.Writer.MkdirAll(trash, 0o700, agentfs.RootOwner); err != nil {
		return "", err
	}
	dest := filepath.Join(trash, name+"-"+e.Now().Format("20060102T150405Z"))
	return dest, e.Writer.Rename(path, dest)
}

// webConfig serialises changes to the web server configuration (Nginx
// sites, PHP-FPM pools and the reloads that load them). Each change writes
// its files, validates the whole configuration (nginx -t, php-fpm -t) and
// reloads or rolls back; two at once could each see the other's half-written
// files and fail, or roll back, for no reason of their own.
var webConfig sync.Mutex

// LockWebConfig takes the web configuration lock; call the result to release
// it. Taken by action handlers (and jobs), never by the helpers they call.
func (e *Env) LockWebConfig() func() {
	webConfig.Lock()
	return webConfig.Unlock
}

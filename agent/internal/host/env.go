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
	"strings"
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
		filepath.Join(fmt.Sprintf(m.PHPPoolDir, "*"), "whacp-*.conf"),
		filepath.Join(m.CronDir, "whacp-*"),
		filepath.Join(m.UnitDir, "whacp-worker-*.service"),
		m.SSHDropIn,
		m.SFTPDropIn,
		m.SFTPKeysDir + "/**",
		m.FTPDir + "/**",
		m.DNSZoneDir + "/**",
		m.DNSConfFile,
		m.MailDir + "/**",
		m.VmailDir + "/**",
		m.PostfixDir + "/main.cf",
		m.PostfixDir + "/master.cf",
		m.DovecotAuthConf,
		m.DovecotDropIn,
		filepath.Join(m.UnitDir, "whacp-opendkim.service"),
		filepath.Join(m.UnitDir, "whacp-ftpd.service"),
		m.Fail2banJail,
		m.ACMERoot + "/**",
		cfg.SitesRoot + "/**",
		cfg.StateDir + "/**",
		cfg.BackupDir + "/**",
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
	unit := fmt.Sprintf(e.M.PHPFPMUnit, v)
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
	return &execx.Credential{UID: uint32(u.UID), GID: uint32(u.GID), Home: u.Home}, u, nil
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

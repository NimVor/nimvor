// Package statsmgr runs AWStats for websites.
//
// AWStats never runs as root here: each website's statistics are updated
// every six hours by a cron line running as the website's own account,
// reading its access log and keeping its data in <root>/.awstats. The
// configuration (/etc/awstats/awstats.nimvor-<domain>.conf) is written by
// the agent and owned by root. The panel reads the data files (plain
// text, confined to the website) and draws its own report: AWStats' HTML
// output, which the website's account could alter, is never shown.
package statsmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/sites"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// ConfigDir holds the AWStats configurations on Debian and RHEL alike.
const ConfigDir = "/etc/awstats"

// DataDir is where a website's statistics are kept, relative to its root.
const DataDir = ".awstats"

// maxData caps a month's data file returned to the panel.
const maxData = 16 << 20

// awstatsPaths are where the distributions install awstats.pl.
var awstatsPaths = []string{"/usr/lib/cgi-bin/awstats.pl", "/usr/share/awstats/wwwroot/cgi-bin/awstats.pl"}

var monthRe = regexp.MustCompile(`^awstats(0[1-9]|1[0-2])([0-9]{4})\.nimvor-[a-z0-9.-]+\.txt$`)

// Site identifies a website.
type Site struct {
	Domain  string   `json:"domain"`
	Aliases []string `json:"aliases"`
	User    string   `json:"user"`
	Root    string   `json:"root"`
}

func (s *Site) check(env *host.Env) error {
	if err := validate.Domain("domain", s.Domain); err != nil {
		return err
	}
	for i, a := range s.Aliases {
		if err := validate.Domain(fmt.Sprintf("aliases[%d]", i), a); err != nil {
			return err
		}
	}
	if s.Root == "" {
		s.Root = filepath.Join(env.Cfg.SitesRoot, s.Domain)
	}
	return validate.Within("root", env.Cfg.SitesRoot, s.Root)
}

func configName(domain string) string { return "nimvor-" + domain }

func configPath(domain string) string {
	return filepath.Join(ConfigDir, "awstats."+configName(domain)+".conf")
}

func cronPath(env *host.Env, domain string) string {
	return filepath.Join(env.M.CronDir, "nimvor-awstats-"+strings.ReplaceAll(domain, ".", "_"))
}

// program is awstats.pl on this server, or "".
func program(env *host.Env) string {
	for _, p := range awstatsPaths {
		if env.Root.Exists(p) {
			return p
		}
	}
	return ""
}

// Config renders a website's AWStats configuration.
func Config(s Site) string {
	hosts := append([]string{s.Domain}, s.Aliases...)
	return strings.Join([]string{
		"# Managed by NimVor (website statistics). Do not edit: changes are overwritten.",
		`LogFile="` + filepath.Join(s.Root, "logs", "access.log") + `"`,
		"LogType=W",
		"LogFormat=1",
		`SiteDomain="` + s.Domain + `"`,
		`HostAliases="` + strings.Join(hosts, " ") + ` localhost 127.0.0.1"`,
		`DirData="` + filepath.Join(s.Root, DataDir) + `"`,
		`DirIcons="/awstats-icon"`,
		"DNSLookup=0",
		"AllowToUpdateStatsFromBrowser=0",
		"AllowFullYearView=2",
		"SaveDatabaseFilesWithPermissionsForEveryone=0",
		"PurgeLogFile=0",
		"KeepBackupOfHistoricFiles=0",
		"",
	}, "\n")
}

// CronLine runs the update every six hours as the website's account (the
// minute is spread by domain).
func CronLine(s Site, prog string) string {
	m := crc32.ChecksumIEEE([]byte(s.Domain)) % 60
	return fmt.Sprintf("# Managed by NimVor (website statistics of %s).\n%d */6 * * * %s /usr/bin/perl %s -config=%s -update >/dev/null 2>&1\n", s.Domain, m, s.User, prog, configName(s.Domain))
}

// siteRoot opens the website folder (not through a symlink).
func siteRoot(env *host.Env, root string) (*os.Root, error) {
	real, err := env.Root.Path(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Lstat(real); err != nil || !info.IsDir() {
		return nil, actions.NotFound("The website folder does not exist.")
	}
	return os.OpenRoot(real)
}

// Register adds the statistics actions.
func Register(r *actions.Registry, env *host.Env) {
	decode := func(p json.RawMessage) (Site, error) {
		var s Site
		if err := host.Decode(p, &s); err != nil {
			return s, err
		}
		if err := s.check(env); err != nil {
			return s, host.Invalid(err)
		}
		return s, nil
	}
	r.Register(actions.Action{Name: "stats.status", Description: "Website statistics: AWStats installed, turned on for the website, months with data and the last update.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		s, err := decode(p)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"installed": program(env) != "", "enabled": env.Root.Exists(configPath(s.Domain)), "months": []string{}, "updated_at": nil}
		root, err := siteRoot(env, s.Root)
		if err != nil {
			return out, nil
		}
		defer root.Close()
		entries, _ := fs(root)
		var months []string
		var last time.Time
		for _, e := range entries {
			m := monthRe.FindStringSubmatch(e.Name())
			if m == nil || !strings.HasSuffix(e.Name(), "."+configName(s.Domain)+".txt") {
				continue
			}
			months = append(months, m[2]+m[1])
			if info, err := e.Info(); err == nil && info.ModTime().After(last) {
				last = info.ModTime()
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(months)))
		if months != nil {
			out["months"] = months
		}
		if !last.IsZero() {
			out["updated_at"] = last.UTC().Format(time.RFC3339)
		}
		return out, nil
	}})
	r.Register(actions.Action{Name: "stats.enable", Mutating: true, Description: "Turn website statistics on (AWStats configuration, data folder, update every 6 hours as the website's account) or off (the data is kept).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Site
			Enabled bool `json:"enabled"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.Site.check(env); err != nil {
			return nil, host.Invalid(err)
		}
		set := env.Changes("stats-" + in.Domain)
		if !in.Enabled {
			set.Remove(configPath(in.Domain))
			set.Remove(cronPath(env, in.Domain))
			if _, err := set.Apply(); err != nil {
				return nil, host.FailWithValidation("Statistics were not turned off", err)
			}
			return map[string]any{"enabled": false}, nil
		}
		prog := program(env)
		if prog == "" {
			return nil, actions.Conflict("AWStats is not installed on this server.")
		}
		_, u, err := env.Credential(in.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		root, err := siteRoot(env, in.Root)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		// The data folder belongs to the website's account (AWStats runs as it).
		if info, err := root.Lstat(DataDir); err == nil && !info.IsDir() {
			return nil, actions.Conflict(DataDir + " in the website folder is not a folder.")
		} else if err != nil {
			if err := root.Mkdir(DataDir, 0o750); err != nil {
				return nil, actions.Failed("Could not create the statistics folder.", err.Error())
			}
			d, err := root.Open(DataDir)
			if err != nil {
				return nil, actions.Failed("Could not create the statistics folder.", err.Error())
			}
			err = d.Chown(u.UID, u.GID) // on the handle: confined like the rest
			d.Close()
			if err != nil {
				return nil, actions.Failed("Could not create the statistics folder.", err.Error())
			}
		}
		if err := sites.AccessLogReadable(env, in.Root, u.GID); err != nil {
			return nil, actions.Failed("Could not let the website's account read its access log.", err.Error())
		}
		set.Write(configPath(in.Domain), []byte(Config(in.Site)), 0o644, agentfs.RootOwner)
		set.Write(cronPath(env, in.Domain), []byte(CronLine(in.Site, prog)), 0o644, agentfs.RootOwner)
		if _, err := set.Apply(); err != nil {
			return nil, host.FailWithValidation("Statistics were not turned on", err)
		}
		return map[string]any{"enabled": true}, nil
	}})
	r.Register(actions.Action{Name: "stats.update", Mutating: true, Description: "Update a website's statistics now, as its account (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		s, err := decode(p)
		if err != nil {
			return nil, err
		}
		prog := program(env)
		if prog == "" || !env.Root.Exists(configPath(s.Domain)) {
			return nil, actions.Conflict("Statistics are not turned on for " + s.Domain + ".")
		}
		cred, _, err := env.Credential(s.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		if err := sites.AccessLogReadable(env, s.Root, int(cred.GID)); err != nil {
			return nil, actions.Failed("Could not let the website's account read its access log.", err.Error())
		}
		dir, _ := env.Root.Path(s.Root)
		j := env.Jobs.Start("stats.update", "site:"+s.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			_, err := env.Runner.RunWith(ctx, "perl", execx.Opts{As: cred, Dir: dir, Output: log, Timeout: time.Hour}, prog, "-config="+configName(s.Domain), "-update")
			if err != nil {
				return nil, fmt.Errorf("AWStats could not update the statistics: %w", err)
			}
			return nil, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "stats.data", Description: "One month (YYYYMM) of a website's AWStats data (text), read confined to the website.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Site
			Month string `json:"month"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.Site.check(env); err != nil {
			return nil, host.Invalid(err)
		}
		if !regexp.MustCompile(`^[0-9]{4}(0[1-9]|1[0-2])$`).MatchString(in.Month) {
			return nil, actions.InvalidParams("month must be YYYYMM")
		}
		root, err := siteRoot(env, in.Root)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		name := filepath.Join(DataDir, "awstats"+in.Month[4:]+in.Month[:4]+"."+configName(in.Domain)+".txt")
		f, err := root.Open(name)
		if err != nil {
			return map[string]any{"content": "", "found": false}, nil
		}
		defer f.Close()
		if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
			return nil, actions.InvalidParams("the statistics file is not a file")
		}
		b, err := io.ReadAll(io.LimitReader(f, maxData))
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		return map[string]any{"content": strings.ToValidUTF8(string(b), "�"), "found": true}, nil
	}})
}

// fs lists the website's data folder.
func fs(root *os.Root) ([]os.DirEntry, error) {
	d, err := root.Open(DataDir)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	return d.ReadDir(-1)
}

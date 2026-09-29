package execx

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SimRunner simulates system tools for development against a fixture tree
// (root_dir != "/"). It never executes anything. Account changes are
// applied to <Base>/etc/passwd so the rest of the agent behaves normally.
type SimRunner struct {
	Base    string
	NextUID int
	mu      sync.Mutex
	Log     io.Writer
}

// Available reports every allowlisted command as installed.
func (s *SimRunner) Available(id string) bool {
	_, known := defaultCandidates[id]
	return known || phpID.MatchString(id) || phpFPMID.MatchString(id)
}

// Run simulates a command.
func (s *SimRunner) Run(ctx context.Context, id string, args ...string) ([]byte, error) {
	return s.RunWith(ctx, id, Opts{}, args...)
}

// RunWith simulates a command and returns plausible output.
func (s *SimRunner) RunWith(_ context.Context, id string, o Opts, args ...string) ([]byte, error) {
	if !s.Available(id) {
		return nil, fmt.Errorf("%w: %s", ErrNotAllowed, id)
	}
	if err := checkArgs(id, o, args); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(id + " " + strings.Join(args, " "))
	if s.Log != nil {
		fmt.Fprintf(s.Log, "[simulated] %s\n", key)
	}
	out := "[simulated] " + key + "\n"
	if phpID.MatchString(id) {
		if sim, ok := simWordPress(o, args); ok {
			out = sim
			if o.Stdout != nil {
				io.WriteString(o.Stdout, out)
			}
			return []byte(out), nil
		}
	}
	switch {
	case key == "nginx -v":
		out = "nginx version: nginx/1.24.0 (simulated)"
	case id == "nginx" && len(args) > 0 && args[0] == "-t":
		out = "nginx: configuration file test is successful (simulated)"
	case id == "systemctl" && len(args) > 0 && args[0] == "is-active":
		out = "active"
	case id == "useradd":
		s.addUser(args[len(args)-1])
	case id == "userdel":
		s.delUser(args[len(args)-1])
	}
	if o.Output != nil {
		io.WriteString(o.Output, out)
	}
	return []byte(out), nil
}

func (s *SimRunner) passwd() string { return filepath.Join(s.Base, "etc/passwd") }

func (s *SimRunner) addUser(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := os.ReadFile(s.passwd())
	if strings.Contains(string(b), "\n"+name+":") || strings.HasPrefix(string(b), name+":") {
		return
	}
	uid := s.NextUID
	if uid == 0 {
		uid = 1500
	}
	for strings.Contains(string(b), fmt.Sprintf(":%d:%d:", uid, uid)) {
		uid++
	}
	s.NextUID = uid + 1
	gid := uid
	if os.Geteuid() != 0 {
		uid, gid = os.Getuid(), os.Getgid() // cannot chown to others
	}
	f, err := os.OpenFile(s.passwd(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s:x:%d:%d:simulated:/home/whacp:/usr/sbin/nologin\n", name, uid, gid)
}

func (s *SimRunner) delUser(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := os.ReadFile(s.passwd())
	var keep []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.HasPrefix(l, name+":") {
			keep = append(keep, l)
		}
	}
	os.WriteFile(s.passwd(), []byte(strings.Join(keep, "\n")+"\n"), 0o644)
}

// simWordPress answers the agent's WordPress programs with a plausible
// installation (development only: the WordPress toolkit's pages can be
// used and tested without WordPress, WP-CLI or a database).
func simWordPress(o Opts, args []string) (string, bool) {
	var code string
	if o.Stdin != nil {
		b, _ := io.ReadAll(io.LimitReader(o.Stdin, 1<<20))
		code = string(b)
	}
	all := strings.Join(args, " ")
	env := func(k string) string {
		for _, e := range o.Env {
			if v, ok := strings.CutPrefix(e, k+"="); ok {
				return v
			}
		}
		return ""
	}
	const m = "\nWHACP-JSON "
	switch {
	case strings.Contains(code, "wp-includes/version.php") && strings.Contains(code, "installs"):
		root := env("WHACP_ROOT")
		if _, err := os.Stat(filepath.Join(root, "public_html", "wp-settings.php")); err != nil {
			return m + `{"installs":[]}` + "\n", true
		}
		return m + `{"installs":[{"dir":"` + filepath.Join(root, "public_html") + `","version":"6.8.1","configured":true,"db_name":"","db_host":"localhost","table_prefix":"wp_","debug":false,"file_edit_disabled":false,"config_mode":"0644","maintenance":false}]}` + "\n", true
	case strings.Contains(code, "wp_version_check"):
		return m + `{"version":"6.8.1","php":"8.3.6","mysql":"10.11.8-MariaDB","multisite":false,"siteurl":"https://example.test","home":"https://example.test","title":"Simulated WordPress","search_engines":true,` +
			`"core":{"latest":"6.8.3","php_needed":"7.2.24","checked_at":1790000000},` +
			`"plugins":[{"name":"akismet","title":"Akismet Anti-spam","version":"5.3","active":true,"update":"5.4","auto_update":false},{"name":"contact-form-7","title":"Contact Form 7","version":"5.9","active":true,"update":"","auto_update":true}],` +
			`"themes":[{"name":"twentytwentyfour","title":"Twenty Twenty-Four","version":"1.2","active":true,"update":"","auto_update":false}],` +
			`"admins":[{"id":1,"login":"owner"}],"admin_login_exists":false,"constants":{"WP_DEBUG":false,"DISALLOW_FILE_EDIT":false,"DISABLE_WP_CRON":false},` +
			`"maintenance":false,"db":{"name":"sim_wp","host":"localhost","prefix":"wp_","tables":12,"bytes":2097152},"config_mode":"0644"}` + "\n", true
	case strings.Contains(code, "keys_set"):
		dir := ""
		for _, a := range args {
			if v, ok := strings.CutPrefix(a, "--path="); ok {
				dir = v
			}
		}
		return m + `{"constants":{"DISALLOW_FILE_EDIT":{"defined":false,"raw":""},"CONCATENATE_SCRIPTS":{"defined":false,"raw":""},"WP_DEBUG":{"defined":true,"raw":"false"}},` +
			`"options":{"default_ping_status":"open","default_pingback_flag":"1"},"config_mode":"0644","config_path":"` + filepath.Join(dir, "wp-config.php") + `","keys_set":true}` + "\n", true
	case strings.Contains(code, "debug.log"):
		return m + `{"path":"` + env("WHACP_ROOT") + `/public_html/wp-content/debug.log","exists":true,"lines":["[01-Sep-2026 12:00:00 UTC] PHP Notice: simulated notice"],"size":64}` + "\n", true
	case strings.Contains(code, "wp_set_password"):
		return m + `{"ok":true,"login":"owner"}` + "\n", true
	case strings.Contains(code, "SET PASSWORD"):
		return m + `{"ok":true,"user":"sim_wp"}` + "\n", true
	case strings.Contains(all, "option get home"):
		return "https://example.test\n", true
	case strings.Contains(all, "wp-cli.phar"):
		return "Success: simulated.\n", true
	}
	return "", false
}

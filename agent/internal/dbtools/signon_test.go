package dbtools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func signonSetup(t *testing.T) (*testhost.Host, env, *actions.Registry) {
	t.Helper()
	h, e := setup(t)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "php8.3" && len(args) == 1 && args[0] == "-m" {
			return "[PHP Modules]\nmysqli\nmbstring\ndom\nxml\nsession\njson\nctype\ncurl\n", nil, true
		}
		if id == "mysql" && len(args) > 0 && args[len(args)-1] != "--help" {
			return "1\n", nil, true // the database exists
		}
		return "", nil, false
	}
	serveTools(t)
	old := probe
	probe = func(context.Context, string) error { return nil }
	t.Cleanup(func() { probe = old })
	pma, _ := toolByID("phpmyadmin")
	if _, err := e.install(context.Background(), pma, io.Discard); err != nil {
		t.Fatal(err)
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	return h, e, r
}

func callAction(t *testing.T, r *actions.Registry, name string, params any) (map[string]any, error) {
	t.Helper()
	a, _ := r.Lookup(name)
	b, _ := json.Marshal(params)
	res, err := a.Handler(context.Background(), b)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(out, &m)
	return m, nil
}

func sqlSeen(h *testhost.Host) string {
	return strings.Join(h.Runner.Stdins, "\n")
}

func TestSignonWritesAOneTimeTokenAndAScopedAccount(t *testing.T) {
	h, _, r := signonSetup(t)
	res, err := callAction(t, r, "dbtools.signon", map[string]any{"tool": "phpmyadmin", "database": "shop"})
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`^/dbtools/phpmyadmin/nimvor-signon\.php\?t=([a-f0-9]{32})$`).FindStringSubmatch(res["path"].(string))
	if m == nil {
		t.Fatalf("path = %v", res["path"])
	}
	var tok struct {
		User, Password, DB string
		Exp                int64
	}
	raw := h.Read("var/lib/nimvor-dbtools/tmp/signon/" + m[1] + ".json")
	if err := json.Unmarshal([]byte(strings.NewReplacer(`"user"`, `"User"`, `"password"`, `"Password"`, `"db"`, `"DB"`, `"exp"`, `"Exp"`).Replace(raw)), &tok); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok.User, "nimvor_t_") || len(tok.Password) < 32 || tok.DB != "shop" || tok.Exp != time.Date(2026, 9, 1, 12, 1, 0, 0, time.UTC).Unix() {
		t.Errorf("token = %+v", tok)
	}
	if st, err := os.Stat(filepath.Join(h.Base, "var/lib/nimvor-dbtools/tmp/signon/"+m[1]+".json")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("token file mode: %v %v", st, err)
	}
	sql := sqlSeen(h)
	for _, want := range []string{"CREATE USER IF NOT EXISTS '" + tok.User + "'@'localhost'", "ACCOUNT UNLOCK", "GRANT ALL PRIVILEGES ON `shop`.* TO '" + tok.User + "'@'localhost'"} {
		if !strings.Contains(sql, want) {
			t.Errorf("SQL lacks %q:\n%s", want, sql)
		}
	}
	if strings.Contains(sql, "ON *.*") || strings.Contains(sql, "GRANT OPTION") {
		t.Error("the account must have privileges on the one database only")
	}
}

func TestSignonRefusesWhatItShould(t *testing.T) {
	_, _, r := signonSetup(t)
	for name, p := range map[string]map[string]any{
		"unknown tool":      {"tool": "evil", "database": "shop"},
		"bad database name": {"tool": "phpmyadmin", "database": "shop; DROP DATABASE mysql"},
		"not installed":     {"tool": "adminer", "database": "shop"},
	} {
		if _, err := callAction(t, r, "dbtools.signon", p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestSweepLocksExpiredAccountsAndRemovesOldTokens(t *testing.T) {
	h, e, r := signonSetup(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	e.Env.Now = func() time.Time { return now }
	res, err := callAction(t, r, "dbtools.signon", map[string]any{"tool": "phpmyadmin", "database": "shop"})
	if err != nil {
		t.Fatal(err)
	}
	token := res["path"].(string)[strings.LastIndex(res["path"].(string), "=")+1:]
	tokenFile := filepath.Join(h.Base, "var/lib/nimvor-dbtools/tmp/signon/"+token+".json")

	// An hour later the account is still open and the token (never used) is still fresh enough to keep.
	now = now.Add(time.Hour)
	if out, err := callAction(t, r, "dbtools.signon_sweep", struct{}{}); err != nil || out["locked"].(float64) != 0 {
		t.Fatalf("early sweep: %v %v", out, err)
	}
	// After the two hours it is locked and forgotten.
	now = now.Add(61 * time.Minute)
	out, err := callAction(t, r, "dbtools.signon_sweep", struct{}{})
	if err != nil || out["locked"].(float64) != 1 {
		t.Fatalf("sweep: %v %v", out, err)
	}
	if !strings.Contains(sqlSeen(h), "ACCOUNT LOCK") {
		t.Error("the account was not locked")
	}
	if len(e.loadSignon().Accounts) != 0 {
		t.Error("the account is still listed")
	}
	// A token older than ten minutes (by its file time) is removed; the test host's clock is the real one for files.
	old := time.Now().Add(-time.Hour)
	if err := os.WriteFile(tokenFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(tokenFile, old, old)
	if _, err := callAction(t, r, "dbtools.signon_sweep", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tokenFile); err == nil {
		t.Error("an old unused token was kept")
	}
}

func TestRemovingTheLastToolLocksEverySignonAccount(t *testing.T) {
	h, e, r := signonSetup(t)
	if _, err := callAction(t, r, "dbtools.signon", map[string]any{"tool": "phpmyadmin", "database": "shop"}); err != nil {
		t.Fatal(err)
	}
	pma, _ := toolByID("phpmyadmin")
	if _, err := e.remove(context.Background(), pma); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sqlSeen(h), "ACCOUNT LOCK") || len(e.loadSignon().Accounts) != 0 {
		t.Error("sign-in accounts were left open after removing the last tool")
	}
}

// The 404 on a real server: nginx only runs a tool's front PHP files whose name matches the snippet's pattern, and
// the sign-in page's name has a hyphen. Check every PHP file NimVor writes for a tool against that pattern.
func TestTheSnippetRunsEveryFrontFileTheToolsWrite(t *testing.T) {
	for _, tool := range Tools {
		re := regexp.MustCompile(`^/dbtools/(` + tool.ID + `)/` + frontPHP + `$`)
		for name := range tool.Def.Files {
			if !strings.HasSuffix(name, ".php") || strings.Contains(name, "/") {
				continue
			}
			if name == "config.inc.php" { // settings, never run directly
				if re.MatchString("/dbtools/" + tool.ID + "/" + name) {
					t.Errorf("%s: %s must not be a front file", tool.ID, name)
				}
				continue
			}
			if !re.MatchString("/dbtools/" + tool.ID + "/" + name) {
				t.Errorf("%s: nginx would answer 404 for /dbtools/%s/%s", tool.ID, tool.ID, name)
			}
		}
	}
	re := regexp.MustCompile(`^/dbtools/(phpmyadmin)/` + frontPHP + `$`)
	for _, bad := range []string{"/dbtools/phpmyadmin/libraries/classes/Core.php", "/dbtools/phpmyadmin/config.inc.php", "/dbtools/phpmyadmin/.php", "/dbtools/phpmyadmin/x.php/y", "/dbtools/phpmyadmin/a b.php"} {
		if re.MatchString(bad) {
			t.Errorf("%s must not run", bad)
		}
	}
}

// A snippet written by an earlier NimVor is brought up to date by the scheduler's sweep, validated and reloaded.
func TestSweepRefreshesAnOutdatedSnippet(t *testing.T) {
	h, e, r := signonSetup(t)
	path := "etc/nginx/nimvor-dbtools.d/dbtools.conf"
	old := strings.Replace(h.Read(path), "[A-Za-z0-9_-]+", "[A-Za-z0-9_]+", 1)
	if err := os.WriteFile(filepath.Join(h.Base, path), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := callAction(t, r, "dbtools.signon_sweep", struct{}{})
	if err != nil || res["refreshed"] != true {
		t.Fatalf("sweep: %v %v", res, err)
	}
	if got := h.Read(path); !strings.Contains(got, "[A-Za-z0-9_-]+") || strings.Contains(got, "[A-Za-z0-9_]+") {
		t.Errorf("the snippet was not refreshed:\n%s", got)
	}
	// Up to date: nothing is rewritten or reloaded again.
	res, _ = callAction(t, r, "dbtools.signon_sweep", struct{}{})
	if _, again := res["refreshed"]; again {
		t.Error("an up-to-date snippet was refreshed again")
	}

	// An Nginx that refuses the new snippet leaves the old one as it was.
	if err := os.WriteFile(filepath.Join(h.Base, path), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	h.Fail["nginx -t"] = "nginx: [emerg] test failure"
	h.FailFrom["nginx -t"] = 1
	res, err = callAction(t, r, "dbtools.signon_sweep", struct{}{})
	if err != nil {
		t.Fatalf("a failed refresh must not fail the sweep: %v", err)
	}
	if _, did := res["refreshed"]; did || h.Read(path) != old {
		t.Error("a snippet Nginx refused was kept")
	}
	_ = e
}

// On a real server phpMyAdmin's page loaded but nothing responded to clicks: its JavaScript fetches its text strings
// from js/messages.php, a PHP file below the tool's top folder that the first snippet refused to run.
func TestPhpMyAdminsMessagesFileRunsAndNothingElseBelowTheTopFolder(t *testing.T) {
	_, e := setup(t)
	snippet := e.renderSnippet([]string{"adminer", "phpmyadmin"})
	if !strings.Contains(snippet, `location ~ ^/dbtools/phpmyadmin/js/messages\.php$ {`) {
		t.Fatalf("no location for js/messages.php:\n%s", snippet)
	}
	if strings.Contains(e.renderSnippet([]string{"adminer"}), "messages") {
		t.Error("the messages file is for phpMyAdmin only")
	}
	// Every PHP location is placed before the rule that answers 404 for any other PHP file.
	if i, j := strings.Index(snippet, `messages\.php$`), strings.Index(snippet, `location ~* \.php$`); i < 0 || j < 0 || i > j {
		t.Error("the 404 rule for other PHP files must come last")
	}
	re := regexp.MustCompile(`^/dbtools/phpmyadmin/` + phpmyadminMessages + `$`)
	if !re.MatchString("/dbtools/phpmyadmin/js/messages.php") {
		t.Error("js/messages.php does not match")
	}
	for _, bad := range []string{"/dbtools/phpmyadmin/js/other.php", "/dbtools/phpmyadmin/js/messages.php/x", "/dbtools/phpmyadmin/js/../index.php", "/dbtools/phpmyadmin/libraries/messages.php", "/dbtools/adminer/js/messages.php"} {
		if re.MatchString(bad) {
			t.Errorf("%s must not run", bad)
		}
	}
}

// Adminer sets the cookie it encrypts the password with only when it shows its own sign-in page, so the sign-in page asks
// for that page before it posts.
func TestAdminersSignInPageLoadsAdminerFirst(t *testing.T) {
	var body string
	for _, tool := range Tools {
		if tool.ID == "adminer" {
			body = tool.Def.Files["nimvor-signon.php"]
		}
	}
	for _, want := range []string{"fetch('index.php', { credentials: 'same-origin' }).then(go, go);", `<form method="post" action="index.php">`, `name="auth[password]"`, "<noscript>"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Adminer sign-in page lacks %q", want)
		}
	}
}

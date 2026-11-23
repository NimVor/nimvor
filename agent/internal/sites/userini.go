package sites

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Customer-written PHP configuration files.
//
// PHP-FPM reads `.user.ini` itself: from the folder of the running script
// up to the document root, cached for user_ini.cache_ttl seconds. It never
// reads a `php.ini` from a website's folder: php.ini is the server's file.
// NimVor therefore supports a customer's `php.ini` by reading it (never
// executing it), checking every line against the list below and writing the
// accepted directives into the website's own pool file, in the place of the
// website-level values (see poolLines). The pool is the only per-website
// PHP configuration PHP-FPM offers.
//
// Precedence, lowest to highest:
//
//	1. the server's PHP configuration (php.ini and conf.d of the version)
//	2. NimVor's website settings (plan defaults and the panel's PHP options)
//	3. the website folder's php.ini, then the document root's php.ini
//	4. .user.ini (applied by PHP itself, so it can only change what PHP
//	   allows at that level)
//
// Directives NimVor sets with php_admin_value (open_basedir, the temp
// folders, disable_functions, the error log, sendmail_path, …) are locked:
// PHP does not let any file below the pool change them, and a php.ini
// directive that would is reported, not applied.

const (
	// UserININame and PHPININame are the files a customer may create.
	UserININame = ".user.ini"
	PHPININame  = "php.ini"
	// maxCustomerINI bounds a customer's file; longer files are refused.
	maxCustomerINI = 64 * 1024
	maxINILines    = 1000
)

// Directive states shown to the customer.
const (
	StateApplied     = "applied"     // in effect
	StateLocked      = "locked"      // NimVor or the server fixes it
	StateUnsupported = "unsupported" // not a setting this file may change
	StateInvalid     = "invalid"     // a value PHP would not accept
	StateOverridden  = "overridden"  // a later file or line replaces it
	StateSystem      = "system"      // PHP does not read it from .user.ini
	StateUnchecked   = "unchecked"   // not known to NimVor (an extension's setting)
)

// customerExtra are directives a website's php.ini may set besides the
// panel's PHPDirectives (which has the plan's own four as separate fields).
var customerExtra = map[string]iniDirective{
	"memory_limit":            {kind: iniPattern, re: regexp.MustCompile(`^[0-9]{1,6}[KMG]?$`)},
	"upload_max_filesize":     {kind: iniSize},
	"max_execution_time":      {kind: iniInt, lo: 0, hi: 86400},
	"max_input_nesting_level": {kind: iniInt, lo: 1, hi: 1024},
	"default_charset":         {kind: iniEnum, enum: []string{"UTF-8", "ISO-8859-1", "windows-1252"}},
}

// userINISystemOnly are directives PHP ignores in a .user.ini because they
// are system-level (PHP_INI_SYSTEM). Prefixes end with a dot.
var userINISystemOnly = map[string]bool{
	"allow_url_include": true, "enable_dl": true, "sys_temp_dir": true, "sendmail_path": true,
	"user_ini.filename": true, "user_ini.cache_ttl": true, "extension": true, "zend_extension": true,
	"doc_root": true, "user_dir": true, "realpath_cache_size": true, "realpath_cache_ttl": true,
	"file_uploads": true, "upload_tmp_dir": true, "allow_url_fopen": true, "expose_php": true,
	"disable_functions": true, "disable_classes": true, "mysqli.max_links": true,
}

var userINISystemPrefixes = []string{"opcache.", "zend_extension"}

// customerDirective returns the rule for a directive a customer's php.ini
// may set, or false.
func customerDirective(key string) (iniDirective, bool) {
	if d, ok := PHPDirectives[key]; ok {
		return d, true
	}
	d, ok := customerExtra[key]
	return d, ok
}

// Directive is one line of a customer's file and what became of it.
type Directive struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Line   int    `json:"line"`
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// ConfigFile is a customer's file as NimVor found it.
type ConfigFile struct {
	Name       string      `json:"name"`  // php.ini or .user.ini
	Where      string      `json:"where"` // document root or website folder
	Path       string      `json:"path"`
	Exists     bool        `json:"exists"`
	Size       int64       `json:"size,omitempty"`
	Modified   time.Time   `json:"modified,omitempty"`
	Problem    string      `json:"problem,omitempty"` // the file could not be read or is not safe to read
	Directives []Directive `json:"directives"`
}

var (
	iniKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]{0,100}$`)
)

// parseCustomerINI reads the text of a php.ini or .user.ini (the subset
// NimVor understands: [PHP], name = value, ; comments) without
// interpreting it. Unusable lines become Directives with a reason, so
// nothing is silently ignored.
func parseCustomerINI(b []byte) []Directive {
	var out []Directive
	if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		return []Directive{{State: StateInvalid, Reason: "The file is not plain text."}}
	}
	text := strings.TrimPrefix(string(b), "\xef\xbb\xbf")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > maxINILines {
		return []Directive{{State: StateInvalid, Reason: fmt.Sprintf("The file has more than %d lines.", maxINILines)}}
	}
	skipping := false
	for i, raw := range lines {
		n := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			sec := strings.ToLower(strings.TrimSpace(strings.SplitN(strings.TrimPrefix(line, "["), "]", 2)[0]))
			if sec == "php" {
				skipping = false
				continue
			}
			skipping = true
			out = append(out, Directive{Key: line, Line: n, State: StateUnsupported, Reason: "Only the [PHP] section is supported; the lines below this section are not read."})
			continue
		}
		if skipping {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			out = append(out, Directive{Key: line, Line: n, State: StateInvalid, Reason: "Expected name = value."})
			continue
		}
		key = strings.TrimSpace(key)
		if !iniKeyRe.MatchString(key) {
			out = append(out, Directive{Key: key, Line: n, State: StateUnsupported, Reason: "Not a plain setting name (array settings and special syntax are not supported)."})
			continue
		}
		v, reason := iniValue(val)
		if reason != "" {
			out = append(out, Directive{Key: key, Value: strings.TrimSpace(val), Line: n, State: StateInvalid, Reason: reason})
			continue
		}
		out = append(out, Directive{Key: key, Value: v, Line: n})
	}
	return out
}

// iniValue extracts the value of one line: a double- or single-quoted
// string, or text up to a ";" comment. Characters that PHP would expand or
// that could carry a second directive are refused.
func iniValue(raw string) (string, string) {
	v := strings.TrimSpace(raw)
	if v != "" && (v[0] == '"' || v[0] == '\'') {
		q := v[0]
		end := strings.IndexByte(v[1:], q)
		if end < 0 {
			return "", "The quotes are not closed."
		}
		rest := strings.TrimSpace(v[end+2:])
		if rest != "" && !strings.HasPrefix(rest, ";") {
			return "", "Unexpected text after the closing quote."
		}
		v = v[1 : end+1]
	} else if i := strings.IndexByte(v, ';'); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	if v == "" {
		return "", "An empty value is not supported; remove the line to use the default."
	}
	if strings.ContainsAny(v, "$`\\\"'\n\r{}") {
		return "", "The value contains a character that is not allowed (variables, quotes and backslashes are not expanded)."
	}
	return v, ""
}

// classifyCustomer decides what a php.ini directive becomes. root is the
// website's folder; locked are the keys NimVor fixes with php_admin_value.
// It returns the normalised value when the directive is accepted.
func classifyCustomer(d Directive, root string, locked map[string]bool) (Directive, bool) {
	if d.State != "" {
		return d, false
	}
	switch d.Key {
	case "extension", "zend_extension":
		d.State, d.Reason = StateUnsupported, "Extensions are installed by the server administrator."
		return d, false
	}
	if locked[d.Key] {
		d.State, d.Reason = StateLocked, "This setting is fixed by NimVor for the website's safety and cannot be changed here."
		return d, false
	}
	rule, ok := customerDirective(d.Key)
	if !ok {
		d.State, d.Reason = StateUnsupported, "Not a PHP setting that a website's php.ini may change."
		return d, false
	}
	if rule.admin {
		d.State, d.Reason = StateLocked, "This is a server-level PHP setting; a website cannot change it."
		return d, false
	}
	v, err := normalizeOne(d.Key, rule, d.Value, root)
	if err != nil {
		d.State, d.Reason = StateInvalid, err.Error()
		return d, false
	}
	d.Value, d.State = v, StateApplied
	return d, true
}

// CustomerLayer is what a website's php.ini files contribute to its pool.
type CustomerLayer struct {
	// Values are the accepted php.ini directives, nearest file last.
	Values map[string]string
	Files  []ConfigFile
}

// readCustomerFile reads a regular file written by the website's account.
// It never follows a symbolic link, refuses a file another account owns and
// bounds its size: the agent runs as root and the folder is the customer's.
func readCustomerFile(path string, uid int) (data []byte, info os.FileInfo, problem string) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, nil, ""
	}
	if !st.Mode().IsRegular() {
		return nil, st, "It is not a regular file (links are not followed)."
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && uid >= 0 && int(sys.Uid) != uid && sys.Uid != 0 {
		return nil, st, "It is owned by another account."
	}
	if st.Size() > maxCustomerINI {
		return nil, st, fmt.Sprintf("It is larger than %d KiB.", maxCustomerINI/1024)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, st, "It could not be opened."
	}
	defer f.Close()
	fst, err := f.Stat()
	if err != nil || !fst.Mode().IsRegular() || !os.SameFile(st, fst) {
		return nil, st, "It changed while being read."
	}
	buf := make([]byte, maxCustomerINI+1)
	n, _ := f.Read(buf)
	if n > maxCustomerINI {
		return nil, st, fmt.Sprintf("It is larger than %d KiB.", maxCustomerINI/1024)
	}
	return buf[:n], fst, ""
}

// pathMapper turns a host path into the path to open (the identity on a
// real server; the agent's root mapping in tests).
type pathMapper func(string) string

// folderIsPlain reports whether dir is inside root and no part of it is a
// symbolic link (a customer can replace public_html with a link).
func folderIsPlain(root, dir string, rp pathMapper) bool {
	root, dir = filepath.Clean(root), filepath.Clean(dir)
	if dir != root && !strings.HasPrefix(dir, root+"/") {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(rp(root))
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(rp(dir))
	return err == nil && real == filepath.Join(realRoot, strings.TrimPrefix(dir, root))
}

// siteINIPaths are the places a customer's files are looked for, in order
// of increasing precedence for php.ini.
func siteINIPaths(s Spec) (phpINI []ConfigFile, userINI ConfigFile) {
	docroot := filepath.Clean(s.DocumentRoot)
	root := filepath.Clean(s.Root)
	if root != docroot {
		phpINI = append(phpINI, ConfigFile{Name: PHPININame, Where: "website folder", Path: filepath.Join(root, PHPININame)})
	}
	phpINI = append(phpINI, ConfigFile{Name: PHPININame, Where: "document root", Path: filepath.Join(docroot, PHPININame)})
	userINI = ConfigFile{Name: UserININame, Where: "document root", Path: filepath.Join(docroot, UserININame)}
	return phpINI, userINI
}

// lockedKeys are the directives of a rendered pool fixed with
// php_admin_value (a file below the pool cannot change them).
func lockedKeys(lines []iniLine) map[string]bool {
	out := map[string]bool{}
	for _, l := range lines {
		if l.admin {
			out[l.key] = true
		}
	}
	return out
}

// loadFile fills a ConfigFile from disk.
func loadFile(f ConfigFile, s Spec, uid int, rp pathMapper) (ConfigFile, []byte) {
	if !folderIsPlain(s.Root, filepath.Dir(f.Path), rp) {
		if _, err := os.Lstat(rp(f.Path)); err == nil || !os.IsNotExist(err) {
			f.Exists, f.Problem = true, "Its folder is not an ordinary folder inside the website."
		}
		return f, nil
	}
	data, st, problem := readCustomerFile(rp(f.Path), uid)
	if st != nil {
		f.Exists, f.Size, f.Modified = true, st.Size(), st.ModTime().UTC().Truncate(time.Second)
	}
	f.Problem = problem
	return f, data
}

// BuildCustomerLayer reads a website's php.ini files (website folder first,
// then the document root, which wins) and classifies each directive.
// locked are the keys the pool fixes; uid is the website account's.
func BuildCustomerLayer(s Spec, locked map[string]bool, uid int, rp pathMapper) CustomerLayer {
	layer := CustomerLayer{Values: map[string]string{}}
	files, _ := siteINIPaths(s)
	type origin struct{ file, idx int }
	set := map[string]origin{}
	for fi, f := range files {
		f, data := loadFile(f, s, uid, rp)
		if f.Exists && f.Problem == "" {
			f.Directives = []Directive{}
			seen := map[string]int{}
			for _, d := range parseCustomerINI(data) {
				d, ok := classifyCustomer(d, s.Root, locked)
				if d.Key != "" {
					if prev, dup := seen[d.Key]; dup && ok {
						// PHP uses the last assignment inside one file.
						f.Directives[prev].State, f.Directives[prev].Reason = StateOverridden, fmt.Sprintf("Set again on line %d.", d.Line)
					}
				}
				f.Directives = append(f.Directives, d)
				if ok {
					seen[d.Key] = len(f.Directives) - 1
					if o, dup := set[d.Key]; dup && o.file != fi {
						layer.Files[o.file].Directives[o.idx].State = StateOverridden
						layer.Files[o.file].Directives[o.idx].Reason = "The " + f.Where + " php.ini sets it too and is read later."
					}
					set[d.Key] = origin{fi, len(f.Directives) - 1}
					layer.Values[d.Key] = d.Value
				}
			}
		}
		layer.Files = append(layer.Files, f)
	}
	return layer
}

// UserINIFile reads a website's .user.ini for display. PHP applies it by
// itself; the states say what PHP does with each line.
func UserINIFile(s Spec, locked map[string]bool, uid int, rp pathMapper) ConfigFile {
	_, uf := siteINIPaths(s)
	f, data := loadFile(uf, s, uid, rp)
	if !f.Exists || f.Problem != "" {
		return f
	}
	f.Directives = []Directive{}
	seen := map[string]int{}
	for _, d := range parseCustomerINI(data) {
		if d.State == "" {
			switch {
			case locked[d.Key]:
				d.State, d.Reason = StateLocked, "NimVor fixes this setting for the website's safety; PHP ignores the line."
			case userINISystemOnly[d.Key] || hasPrefixAny(d.Key, userINISystemPrefixes) || adminDirective(d.Key):
				d.State, d.Reason = StateSystem, "PHP only reads this setting from the server's configuration; a .user.ini cannot change it."
			default:
				if rule, ok := customerDirective(d.Key); ok {
					if v, err := normalizeOne(d.Key, rule, d.Value, s.Root); err != nil {
						d.State, d.Reason = StateInvalid, err.Error()+" PHP may ignore or misread the line."
					} else {
						d.Value, d.State = v, StateApplied
					}
				} else {
					d.State, d.Reason = StateUnchecked, "NimVor does not know this setting. PHP applies it only if a loaded extension defines it."
				}
			}
		}
		if d.Key != "" && (d.State == StateApplied || d.State == StateUnchecked) {
			if prev, dup := seen[d.Key]; dup {
				f.Directives[prev].State, f.Directives[prev].Reason = StateOverridden, fmt.Sprintf("Set again on line %d.", d.Line)
			}
			seen[d.Key] = len(f.Directives)
		}
		f.Directives = append(f.Directives, d)
	}
	return f
}

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func adminDirective(key string) bool {
	d, ok := PHPDirectives[key]
	return ok && d.admin
}

// EffectiveSetting is the value PHP will use for one directive and where it
// comes from.
type EffectiveSetting struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"` // nimvor, nimvor-locked, php.ini, .user.ini
	Detail string `json:"detail,omitempty"`
}

// Effective merges the layers for display: NimVor's pool lines, the
// accepted php.ini values and the .user.ini lines PHP applies. Settings
// only the server's PHP configuration provides are not listed.
func Effective(base []iniLine, layer CustomerLayer, user ConfigFile) []EffectiveSetting {
	rows := map[string]EffectiveSetting{}
	locked := lockedKeys(base)
	for _, l := range base {
		src := "nimvor"
		if l.admin {
			src = "nimvor-locked"
		}
		rows[l.key] = EffectiveSetting{Key: l.key, Value: l.value, Source: src}
	}
	for _, f := range layer.Files {
		for _, d := range f.Directives {
			if d.State == StateApplied && !locked[d.Key] {
				rows[d.Key] = EffectiveSetting{Key: d.Key, Value: d.Value, Source: PHPININame, Detail: f.Where}
			}
		}
	}
	for _, d := range user.Directives {
		if (d.State == StateApplied || d.State == StateUnchecked) && !locked[d.Key] {
			rows[d.Key] = EffectiveSetting{Key: d.Key, Value: d.Value, Source: UserININame, Detail: user.Where}
		}
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]EffectiveSetting, 0, len(keys))
	for _, k := range keys {
		out = append(out, rows[k])
	}
	return out
}

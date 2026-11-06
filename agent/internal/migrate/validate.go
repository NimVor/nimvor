package migrate

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
)

// Check results.
const (
	Pass = "pass"
	Warn = "warn"
	Fail = "fail"
)

// Check is one validation result.
type Check struct {
	Item   string `json:"item"` // what was checked (a website, a database)
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// ValidationReport is the result of a validation pass.
type ValidationReport struct {
	At     time.Time `json:"at"`
	Checks []Check   `json:"checks"`
	Passed int       `json:"passed"`
	Warned int       `json:"warned"`
	Failed int       `json:"failed"`
	// Limits says what a pass does not check, so a green report is not read as more.
	Limits []string `json:"limits"`
}

// maxOwnerCheck bounds how many created files are checked for ownership.
const maxOwnerCheck = 5000

// Validate checks what a migration left in this server. It reads only: it opens no
// connection to the source and changes nothing. It can be run again at any time.
func (e *Engine) Validate(ctx context.Context, id string) (*ValidationReport, error) {
	m, err := e.Store.Load(id)
	if err != nil {
		return nil, err
	}
	if m.Status == Created {
		return nil, fmt.Errorf("the migration has not run yet")
	}
	if m.Status == RolledBack {
		return nil, fmt.Errorf("the migration was rolled back")
	}
	rep := &ValidationReport{At: time.Now().UTC(), Checks: []Check{}, Limits: []string{
		"PHP execution, PHP extensions, application configuration, SSL certificates and DNS are not checked.",
		"Files and messages are checked against what the copy recorded (present, unchanged, owned by the website's account or by vmail); their content was compared to the source by checksum when they were copied.",
		"Mail is checked as files only: logging in to a mailbox, IMAP and the mailbox's password, quota, aliases and forwarders are not tested.",
		"The HTTP check asks this server's own web server for the site with its host name, so it works before DNS is switched; a placeholder or a PHP error page still counts as an answer.",
	}}
	add := func(item, name, status, detail string) {
		rep.Checks = append(rep.Checks, Check{Item: item, Name: name, Status: status, Detail: detail})
		switch status {
		case Pass:
			rep.Passed++
		case Warn:
			rep.Warned++
		default:
			rep.Failed++
		}
	}
	man := e.Store.LoadManifest(id)
	byID := map[string]Task{}
	for _, t := range m.Tasks {
		byID[t.ID] = t
	}
	for _, f := range m.Plan.Files {
		item := f.Domain
		t := byID["files:"+f.DestRoot]
		if t.Status != Done {
			add(item, "Files copied", Fail, "the copy is not finished ("+t.Status+")")
			continue
		}
		real, err := e.Env.Root.Path(f.DestRoot)
		info, serr := os.Stat(real)
		if err != nil || serr != nil || !info.IsDir() {
			add(item, "Document root", Fail, f.DestRoot+" does not exist")
			continue
		}
		add(item, "Document root", Pass, f.DestRoot+" exists")
		if t.Differences > 0 {
			add(item, "Files match the source", Warn, fmt.Sprintf("%d file(s) differ from the source after the last copy: they changed on the source while it was copied (run Synchronise to bring them over), or were edited here since (a synchronisation never replaces a file that is newer here)", t.Differences))
		} else {
			add(item, "Files match the source", Pass, "a checksum comparison with the source found no difference")
		}
		e.checkFiles(rep, add, item, f, man["files:"+f.DestRoot], real)
		e.checkHTTP(ctx, add, item, f.Domain)
	}
	for _, mt := range m.Plan.Mail {
		item := mt.Address
		t := byID["mail:"+mt.Address]
		if t.Status != Done {
			add(item, "Mailbox copied", Fail, "the copy is not finished ("+t.Status+")")
			continue
		}
		real, uid, _, err := mailDest(e.Env, mt.Address)
		if err != nil {
			add(item, "Mailbox", Fail, err.Error())
			continue
		}
		add(item, "Mailbox", Pass, "the NimVor mailbox exists")
		if t.Differences > 0 {
			add(item, "Messages match the source", Warn, fmt.Sprintf("%d message file(s) differ from the source after the last copy: new mail arrived on the source while it was copied (run Synchronise to bring it over), or messages were changed here since", t.Differences))
		} else {
			add(item, "Messages match the source", Pass, "a checksum comparison with the source found no difference")
		}
		e.checkTree(add, item, real, man["mail:"+mt.Address], uid, "vmail")
	}
	for _, d := range m.Plan.Databases {
		item := d.Dest
		t := byID["db:"+d.Dest]
		if t.Status != Done {
			add(item, "Database imported", Fail, "the import is not finished ("+t.Status+")")
			continue
		}
		n, err := e.DB.Tables(ctx, d.Dest)
		if err != nil {
			add(item, "Database reachable", Fail, err.Error())
			continue
		}
		add(item, "Database reachable", Pass, "the database answers")
		switch {
		case t.Tables == 0:
			add(item, "Table count", Warn, "the number of imported tables was not recorded")
		case n == t.Tables:
			add(item, "Table count", Pass, fmt.Sprintf("%d table(s), as imported", n))
		default:
			add(item, "Table count", Fail, fmt.Sprintf("%d table(s) now, %d were imported: it changed since the migration", n, t.Tables))
		}
	}
	m.Validation = rep
	return rep, e.Store.Save(m)
}

func (e *Engine) checkFiles(rep *ValidationReport, add func(item, name, status, detail string), item string, f FileTask, fm *FileManifest, real string) {
	uid := -1
	if _, u, err := e.Env.Credential(f.DestUser); err == nil {
		uid = u.UID
	}
	e.checkTree(add, item, real, fm, uid, f.DestUser)
}

// checkTree checks the files a copy created in a folder: present, unchanged, owned by uid (-1: not checked).
func (e *Engine) checkTree(add func(item, name, status, detail string), item, real string, fm *FileManifest, uid int, owner string) {
	if fm == nil || len(fm.Created) == 0 {
		add(item, "Created files", Warn, "no record of the files the copy created (nothing was new, or an earlier version of the agent)")
		return
	}
	root, err := os.OpenRoot(real)
	if err != nil {
		add(item, "Created files", Fail, err.Error())
		return
	}
	defer root.Close()
	missing, changed, wrongOwner, checked := 0, 0, 0, 0
	for _, c := range fm.Created {
		info, err := root.Lstat(c.Path)
		if err != nil && renamedSibling(root, c.Path) {
			changed++ // a mail message that was read or flagged is renamed, not lost
			continue
		}
		if err != nil {
			missing++
			continue
		}
		if info.Size() != c.Size || info.ModTime().UnixNano() != c.MTime {
			changed++
		}
		if checked < maxOwnerCheck && uid > 0 {
			if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
				wrongOwner++
			}
		}
		checked++
	}
	if missing > 0 {
		add(item, "Created files present", Fail, fmt.Sprintf("%d of %d file(s) the copy created are missing", missing, len(fm.Created)))
	} else {
		add(item, "Created files present", Pass, fmt.Sprintf("all %d file(s) the copy created are there", len(fm.Created)))
	}
	if changed > 0 {
		add(item, "Created files unchanged", Warn, fmt.Sprintf("%d file(s) changed since the copy (normal if it has been in use; mail flags change when messages are read)", changed))
	}
	if os.Geteuid() == 0 && uid > 0 {
		if wrongOwner > 0 {
			add(item, "File ownership", Fail, fmt.Sprintf("%d of %d checked file(s) are not owned by %s", wrongOwner, checked, owner))
		} else {
			add(item, "File ownership", Pass, fmt.Sprintf("%d file(s) checked, all owned by %s", checked, owner))
		}
	}
}

// checkHTTP asks this server's own web server for the site, by name, over HTTP.
func (e *Engine) checkHTTP(ctx context.Context, add func(item, name, status, detail string), item, domain string) {
	if !domainName.MatchString(domain) {
		return
	}
	if !e.Env.Runner.Available("curl") {
		add(item, "HTTP answer", Warn, "curl is not installed here, so the site was not requested")
		return
	}
	out, err := e.Env.Runner.RunWith(ctx, "curl", execx.Opts{Timeout: 25 * time.Second}, "--disable", "--silent", "--show-error", "--max-time", "15", "--output", "/dev/null",
		"--write-out", "%{http_code}", "--resolve", domain+":80:127.0.0.1", "--", "http://"+domain+"/")
	code := strings.TrimSpace(string(out))
	switch {
	case err != nil || len(code) != 3:
		add(item, "HTTP answer", Warn, "the site did not answer on this server's web server ("+strings.TrimSpace(fmt.Sprint(err))+"): is the website enabled in NimVor?")
	case code[0] == '2' || code[0] == '3':
		add(item, "HTTP answer", Pass, "the site answers with HTTP "+code)
	default:
		add(item, "HTTP answer", Warn, "the site answers with HTTP "+code+": open it and check what it shows")
	}
}

package migrate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// MailTask copies the messages of one mailbox into an existing NimVor mailbox.
//
// What moves is the Maildir: every message and folder, with the flags in the file names. What
// does not: the mailbox's password, quota, aliases and forwarders (the NimVor mailbox has its
// own, set up in NimVor first). Dovecot is told to resynchronise the mailbox afterwards.
type MailTask struct {
	Address       string `json:"address"`        // the NimVor mailbox, local@domain
	SourceMaildir string `json:"source_maildir"` // the mailbox's Maildir on the source
	SizeKB        int64  `json:"size_kb"`
}

var mailAddrRe = regexp.MustCompile(`^([a-z0-9][a-z0-9._+-]{0,63})@([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+)$`)

// mailExcludes are Dovecot's own caches: they are rebuilt, and the source's would be wrong here.
var mailExcludes = []string{"dovecot.index*", "dovecot.list.index*", "dovecot.mailbox.log", "dovecot-uidlist.lock", "maildirsize", "courierimap*", "dovecot-keywords.lock"}

// mailDest returns the mailbox's Maildir in this server and the vmail account that owns it.
// The mailbox must already exist (NimVor made it): a migration fills mailboxes, it makes none.
func mailDest(env *host.Env, addr string) (real string, uid, gid int, err error) {
	m := mailAddrRe.FindStringSubmatch(addr)
	if m == nil {
		return "", 0, 0, fmt.Errorf("%q is not a mailbox address", addr)
	}
	vm, err := env.LookupUser("vmail")
	if err != nil {
		return "", 0, 0, fmt.Errorf("this server has no mail accounts yet (the vmail account is missing): set up email first")
	}
	base := strings.TrimSuffix(env.M.VmailDir, "/")
	host := filepath.Join(base, m[2], m[1], "Maildir")
	real, err = env.Root.Path(host)
	if err != nil {
		return "", 0, 0, err
	}
	info, err := os.Lstat(real)
	if err != nil || !info.IsDir() {
		return "", 0, 0, fmt.Errorf("the mailbox %s does not exist here: create it in NimVor first", addr)
	}
	if resolved, err := filepath.EvalSymlinks(real); err != nil || resolved != real {
		return "", 0, 0, fmt.Errorf("the mailbox folder of %s must not be or contain a symbolic link", addr)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != vm.UID && os.Geteuid() == 0 {
		return "", 0, 0, fmt.Errorf("the mailbox folder of %s does not belong to the vmail account", addr)
	}
	return real, vm.UID, vm.GID, nil
}

// maildirHasMessages reports whether any folder of a Maildir holds a message.
func maildirHasMessages(real string) bool {
	found := false
	filepath.WalkDir(real, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if d.Type().IsRegular() {
			dir := filepath.Base(filepath.Dir(p))
			if dir == "cur" || dir == "new" {
				found = true
			}
		}
		return nil
	})
	return found
}

func mailIndex(m *Migration, id string) int {
	for i, t := range m.Plan.Mail {
		if "mail:"+t.Address == id {
			return i
		}
	}
	return 0
}

func (e *Engine) runMail(ctx context.Context, m *Migration, t Puller, i int, update func(int, func(*Task)), log io.Writer) error {
	mt := m.Plan.Mail[mailIndex(m, m.Tasks[i].ID)]
	real, uid, gid, err := mailDest(e.Env, mt.Address)
	if err != nil {
		return fatal{err}
	}
	resync := m.Tasks[i].Checkpoint == "resync"
	first := m.Tasks[i].Attempts <= 1 && m.Tasks[i].Bytes == 0 && m.Tasks[i].Checkpoint == ""
	if first && !m.Plan.Config.AllowOverwrite && maildirHasMessages(real) {
		return fatal{fmt.Errorf("the mailbox %s already has messages. Migrating into it could mix them up; choose an empty mailbox, or allow overwriting explicitly", mt.Address)}
	}
	if free := freeKB(real); free < reserveKB {
		return fatal{fmt.Errorf("the disk of the mail storage has only %s free; NimVor keeps %s free, so nothing was copied", kb(free), kb(reserveKB))}
	}
	update(i, func(t *Task) { t.Checkpoint = "copying" })
	args := []string{"-a", "-s", "-i", "-8", "--partial", "--safe-links", "--stats", "--no-specials", "--no-devices", fmt.Sprintf("--chown=%d:%d", uid, gid)}
	if m.Plan.Config.Compress {
		args = append(args, "-z")
	}
	if resync {
		args = append(args, "--update")
	}
	for _, x := range mailExcludes {
		args = append(args, "--exclude="+x)
	}
	var out strings.Builder
	src := strings.TrimSuffix(mt.SourceMaildir, "/") + "/"
	dst := strings.TrimSuffix(real, "/") + "/"
	if _, err := t.Rsync(ctx, args, src, dst, &out); err != nil {
		return fmt.Errorf("the messages could not be copied: %v", err)
	}
	files, bytes := int64(0), int64(0)
	if mm := rsyncFiles.FindStringSubmatch(out.String()); mm != nil {
		files = num(mm[1])
	}
	if mm := rsyncBytes.FindStringSubmatch(out.String()); mm != nil {
		bytes = num(mm[1])
	}
	if err := e.recordCreated(m.ID, m.Tasks[i].ID, real, out.String()); err != nil {
		return fmt.Errorf("what was copied could not be recorded: %v", err)
	}
	vargs := []string{"-a", "-s", "-n", "-c", "--safe-links", "--itemize-changes", "--no-specials", "--no-devices", "--no-owner", "--no-group"}
	for _, x := range mailExcludes {
		vargs = append(vargs, "--exclude="+x)
	}
	var vout strings.Builder
	if _, err := t.Rsync(ctx, vargs, src, dst, &vout); err != nil {
		return fmt.Errorf("the copy could not be verified: %v", err)
	}
	diff := 0
	for _, l := range strings.Split(vout.String(), "\n") {
		if strings.HasPrefix(l, ">f") {
			diff++
		}
	}
	// Dovecot rebuilds its indexes from the files. If it is not running here the messages are
	// still in place and are read when it starts, so this is a note, not a failure.
	note := ""
	if e.Env.Runner.Available("doveadm") {
		if _, err := e.Env.Runner.Run(ctx, "doveadm", "force-resync", "-u", mt.Address, "*"); err != nil {
			note = "; Dovecot could not resynchronise the mailbox now (it will when it next opens it)"
		}
	} else {
		note = "; Dovecot is not installed here, so the mailbox was not resynchronised"
	}
	update(i, func(t *Task) {
		t.Files, t.Bytes, t.Differences, t.Checkpoint = files, bytes, diff, "copied"
		t.Detail = fmt.Sprintf("%d message file(s), %d bytes copied; %d differ after verification%s", files, bytes, diff, note)
	})
	return nil
}

// renamedSibling reports whether a Maildir message that is no longer at path is still in its folder
// under another name: Maildir keeps a message's flags in its file name (…:2,S once read), so reading
// or flagging a message renames it.
func renamedSibling(root *os.Root, p string) bool {
	base := filepath.Base(p)
	i := strings.Index(base, ":2,")
	if i < 0 {
		return false
	}
	dir, err := root.Open(filepath.Dir(p))
	if err != nil {
		return false
	}
	defer dir.Close()
	ents, err := dir.ReadDir(-1)
	if err != nil {
		return false
	}
	prefix := base[:i] + ":2,"
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), prefix) {
			return true
		}
	}
	return false
}

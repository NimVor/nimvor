package sites

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/safewalk"
	"github.com/mchkhawarshahzad/whacp/agent/internal/selinux"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Website permission repair: files and folders of a website that do not
// belong to its account, belong to a group that is neither the account's
// nor the web server's, can be written by anyone, carry set-user/group-ID
// bits, or cannot be read by their owner. Everything else (e.g. a
// deliberately private 0600 wp-config.php, or group-writable folders) is
// left as it is.

// Problem kinds.
const (
	ProbOwner      = "owner"       // not owned by the website's account
	ProbGroup      = "group"       // another account's group
	ProbWorldWrite = "world_write" // anyone can write
	ProbSetID      = "setid"       // set-user-ID or set-group-ID on a file
	ProbUnreadable = "unreadable"  // the owner cannot read (or enter)
)

// PermIssue is one entry that needs repair.
type PermIssue struct {
	Path     string   `json:"path"`
	Problems []string `json:"problems"`
	Mode     string   `json:"mode"`
}

// PermReport is the result of a check or repair.
type PermReport struct {
	Checked int            `json:"checked"`
	Issues  int            `json:"issues"`
	Fixed   int            `json:"fixed"`
	Counts  map[string]int `json:"counts"`
	Sample  []PermIssue    `json:"sample"`
	Skipped []string       `json:"skipped,omitempty"`
	SkipN   int            `json:"skipped_count"`
	DryRun  bool           `json:"dry_run"`
}

// permRules decides what an entry should be.
type permRules struct {
	uid, gid, webGID int
	docRel           string // document root relative to the website folder ("" when it is the folder)
}

func (r permRules) inDocroot(rel string) bool {
	return r.docRel == "" || rel == r.docRel || strings.HasPrefix(rel, r.docRel+"/")
}

// want returns the owner, group and mode an entry should have and why.
func (r permRules) want(rel string, dir bool, st syscall.Stat_t) (uid, gid int, mode uint32, problems []string) {
	uid, gid, mode = int(st.Uid), int(st.Gid), st.Mode&0o7777
	if uid != r.uid {
		problems = append(problems, ProbOwner)
		uid = r.uid
	}
	if gid != r.gid && gid != r.webGID {
		problems = append(problems, ProbGroup)
		gid = r.gid
		if r.inDocroot(rel) {
			gid = r.webGID
		}
	}
	if mode&0o002 != 0 {
		problems = append(problems, ProbWorldWrite)
		mode &^= 0o002
	}
	if !dir && mode&0o6000 != 0 {
		problems = append(problems, ProbSetID)
		mode &^= 0o6000
	}
	if dir && mode&0o4000 != 0 {
		problems = append(problems, ProbSetID)
		mode &^= 0o4000
	}
	need := uint32(0o400)
	if dir {
		need = 0o500
	}
	if mode&need != need {
		problems = append(problems, ProbUnreadable)
		mode |= need
	}
	return
}

// RepairPermissions checks (dryRun) or repairs the tree root/.
func RepairPermissions(rootFD int, name string, r permRules, dryRun bool) (PermReport, error) {
	rep := PermReport{Counts: map[string]int{}, Sample: []PermIssue{}, DryRun: dryRun}
	res, err := safewalk.Walk(rootFD, name, func(e *safewalk.Entry) error {
		rep.Checked++
		rel := strings.TrimPrefix(strings.TrimPrefix(e.Rel, name), "/")
		// A second name of a file the account does not own (a hard link
		// made into the website) is never given to the account.
		if !e.Dir && e.Stat.Nlink > 1 && int(e.Stat.Uid) != r.uid {
			rep.SkipN++
			if len(rep.Skipped) < 50 {
				rep.Skipped = append(rep.Skipped, e.Rel)
			}
			return nil
		}
		uid, gid, mode, problems := r.want(rel, e.Dir, e.Stat)
		if len(problems) == 0 {
			return nil
		}
		rep.Issues++
		for _, p := range problems {
			rep.Counts[p]++
		}
		if len(rep.Sample) < 200 {
			rep.Sample = append(rep.Sample, PermIssue{Path: e.Rel, Problems: problems, Mode: modeString(e.Stat.Mode&0o7777, e.Dir)})
		}
		if dryRun {
			return nil
		}
		if uid != int(e.Stat.Uid) || gid != int(e.Stat.Gid) {
			if err := syscall.Fchown(e.FD, uid, gid); err != nil {
				return err
			}
		}
		// chown clears set-ID bits of files; set the mode after it.
		if err := syscall.Fchmod(e.FD, mode); err != nil {
			return err
		}
		rep.Fixed++
		return nil
	})
	rep.Skipped = append(rep.Skipped, res.Skipped...)
	rep.SkipN += res.Count
	sort.SliceStable(rep.Sample, func(i, j int) bool { return rep.Sample[i].Path < rep.Sample[j].Path })
	return rep, err
}

func modeString(m uint32, dir bool) string {
	return (os.FileMode(m&0o777) | map[bool]os.FileMode{true: os.ModeDir}[dir]).String()
}

func (m *Manager) registerRepair(r *actions.Registry) {
	e := m.Env
	r.Register(actions.Action{Name: "sites.permissions", Mutating: true, Timeout: 30 * time.Minute, Description: "Check (dry_run) or repair the ownership and permissions of a website's files: the account's owner, its or the web server's group, no world-writable files, no set-ID files, readable by the owner. Links are never followed.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Domain       string `json:"domain"`
			User         string `json:"user"`
			DocumentRoot string `json:"document_root"`
			DryRun       bool   `json:"dry_run"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, err
		}
		if err := validate.Username("user", in.User); err != nil {
			return nil, err
		}
		u, err := e.LookupUser(in.User)
		if err != nil || u.UID == 0 {
			return nil, actions.NotFound("The website's account does not exist.")
		}
		web, err := e.WebUser()
		if err != nil {
			return nil, actions.Failed("The web server account "+e.M.WebUser+" does not exist.", "")
		}
		siteRoot := filepath.Join(e.Cfg.SitesRoot, in.Domain)
		docRel := "public_html"
		if in.DocumentRoot != "" {
			rel, err := filepath.Rel(siteRoot, filepath.Clean(in.DocumentRoot))
			if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				return nil, actions.InvalidParams("document_root must be inside the website folder")
			}
			if rel == "." {
				rel = ""
			}
			docRel = filepath.ToSlash(rel)
		}
		base, err := e.Root.Path(e.Cfg.SitesRoot)
		if err != nil {
			return nil, err
		}
		rfd, err := syscall.Open(base, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
		if err != nil {
			return nil, actions.Failed("The websites folder could not be opened.", err.Error())
		}
		defer syscall.Close(rfd)
		// The website folder must already be the account's: a repair never
		// gives one account's files to another.
		var st syscall.Stat_t
		dfd, err := syscall.Openat(rfd, in.Domain, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
		if err != nil {
			return nil, actions.NotFound("The website folder does not exist.")
		}
		err = syscall.Fstat(dfd, &st)
		syscall.Close(dfd)
		if err != nil {
			return nil, actions.NotFound("The website folder does not exist.")
		}
		if int(st.Uid) != u.UID {
			return nil, actions.Conflict("The website folder is not owned by " + in.User + "; it is not repaired.")
		}
		if e.Cfg.SimulateCommands {
			return PermReport{Counts: map[string]int{}, Sample: []PermIssue{}, DryRun: in.DryRun}, nil
		}
		rep, err := RepairPermissions(rfd, in.Domain, permRules{uid: u.UID, gid: u.GID, webGID: web.GID, docRel: docRel}, in.DryRun)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, actions.NotFound("The website folder does not exist.")
			}
			return nil, actions.Failed("The permissions could not be repaired.", err.Error())
		}
		if !in.DryRun && rep.Fixed > 0 {
			if err := selinux.Restore(ctx, e, siteRoot); err != nil {
				e.Log.Warn("selinux", "err", err)
			}
		}
		return rep, nil
	}})
}

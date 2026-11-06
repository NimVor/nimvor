package migrate

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// RollbackReport is what a rollback removed and what it left, and why.
type RollbackReport struct {
	At               time.Time `json:"at"`
	FilesRemoved     int       `json:"files_removed"`
	FilesGone        int       `json:"files_gone"` // already deleted by someone
	DirsRemoved      int       `json:"dirs_removed"`
	DatabasesEmptied []string  `json:"databases_emptied"`
	// Kept lists what was left alone and why: newer data is never removed.
	Kept  []string `json:"kept"`
	Notes []string `json:"notes"`
}

// itemized reads rsync's --itemize-changes lines: new files, new folders and updated files.
// A line is "<11 flag characters> <name>"; a file or folder is new when its flags end in
// nine "+".
func itemized(out string) (files, dirs []string, overwritten int) {
	for _, l := range strings.Split(out, "\n") {
		if len(l) < 13 || l[11] != ' ' {
			continue
		}
		flags, name := l[:11], l[12:]
		kind := flags[1]
		if kind != 'f' && kind != 'd' {
			continue
		}
		if name == "./" || name == "." || strings.Contains(name, `\#`) || strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			continue
		}
		switch {
		case flags[2:] == "+++++++++" && kind == 'f':
			files = append(files, name)
		case flags[2:] == "+++++++++" && kind == 'd':
			dirs = append(dirs, strings.TrimSuffix(name, "/"))
		case kind == 'f' && flags[0] == '>':
			overwritten++
		}
	}
	return
}

// recordCreated adds what a copy created to the migration's manifest, with the size and
// time the copy left on each file, so a rollback can tell whether it was changed since.
func (e *Engine) recordCreated(id, task, real, out string) error {
	files, dirs, over := itemized(out)
	man := e.Store.LoadManifest(id)
	fm := man[task]
	if fm == nil {
		fm = &FileManifest{}
		man[task] = fm
	}
	known := map[string]bool{}
	for _, c := range fm.Created {
		known[c.Path] = true
	}
	root, err := os.OpenRoot(real)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, f := range files {
		if known[f] {
			continue
		}
		info, err := root.Lstat(f)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		fm.Created = append(fm.Created, CreatedFile{Path: f, Size: info.Size(), MTime: info.ModTime().UnixNano()})
	}
	seen := map[string]bool{}
	for _, d := range fm.Dirs {
		seen[d] = true
	}
	for _, d := range dirs {
		if !seen[d] {
			fm.Dirs = append(fm.Dirs, d)
		}
	}
	fm.Overwritten += over
	return e.Store.SaveManifest(id, man)
}

// Rollback undoes what a migration did in the destination, as far as it can be done
// without risk to newer data:
//
//   - files the migration created are removed only while they are exactly as the copy
//     left them (size and time); a file that was changed since is kept and reported;
//   - folders the migration created are removed only when they are empty;
//   - a database is emptied only when emptyDatabases is set (an explicit confirmation),
//     only if it had no table before the import, and not if any table was written to
//     after the import: then it is kept and reported;
//   - files that were overwritten (only possible with overwriting allowed) cannot be
//     restored, because no copy of them was kept: that is reported.
//
// Nothing on the source is touched, and no login is needed.
func (e *Engine) Rollback(ctx context.Context, id string, emptyDatabases bool, log io.Writer) (*RollbackReport, error) {
	m, err := e.Store.Load(id)
	if err != nil {
		return nil, err
	}
	switch m.Status {
	case Completed, Failed, Cancelled, Paused, Interrupted:
	case RolledBack:
		return nil, fmt.Errorf("this migration was already rolled back")
	default:
		return nil, fmt.Errorf("a migration that is %s cannot be rolled back", m.Status)
	}
	e.mu.Lock()
	if _, busy := e.running[id]; busy {
		e.mu.Unlock()
		return nil, fmt.Errorf("the migration is running: pause or cancel it first")
	}
	e.running[id] = func() {}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.running, id)
		e.mu.Unlock()
	}()

	rep := &RollbackReport{At: time.Now().UTC(), DatabasesEmptied: []string{}, Kept: []string{}, Notes: []string{}}
	man := e.Store.LoadManifest(id)
	for _, f := range m.Plan.Files {
		taskID := "files:" + f.DestRoot
		fm := man[taskID]
		if fm == nil {
			continue
		}
		if err := checkDest(e.Env, &f); err != nil {
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s was not touched: %v", f.DestRoot, err))
			continue
		}
		real, _ := e.Env.Root.Path(f.DestRoot)
		root, err := os.OpenRoot(real)
		if err != nil {
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s could not be opened: %v", f.DestRoot, err))
			continue
		}
		for i := len(fm.Created) - 1; i >= 0; i-- {
			c := fm.Created[i]
			info, err := root.Lstat(c.Path)
			switch {
			case os.IsNotExist(err):
				rep.FilesGone++
			case err != nil:
				rep.Kept = append(rep.Kept, f.DestRoot+"/"+c.Path+" (could not be read)")
			case !info.Mode().IsRegular() || info.Size() != c.Size || info.ModTime().UnixNano() != c.MTime:
				rep.Kept = append(rep.Kept, f.DestRoot+"/"+c.Path+" (changed since the migration: newer data is never removed)")
			default:
				if err := root.Remove(c.Path); err != nil {
					rep.Kept = append(rep.Kept, f.DestRoot+"/"+c.Path+" ("+err.Error()+")")
				} else {
					rep.FilesRemoved++
				}
			}
		}
		dirs := append([]string{}, fm.Dirs...)
		sort.Slice(dirs, func(a, b int) bool { return strings.Count(dirs[a], "/") > strings.Count(dirs[b], "/") })
		for _, d := range dirs {
			if err := root.Remove(path.Clean(d)); err == nil {
				rep.DirsRemoved++
			}
		}
		root.Close()
		if fm.Overwritten > 0 {
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s: %d existing file(s) were overwritten by the copy and cannot be restored (no copy of them was kept).", f.DestRoot, fm.Overwritten))
		}
	}
	for i, t := range m.Tasks {
		if t.Kind != "db" || (t.Checkpoint != "importing" && t.Checkpoint != "imported") {
			continue
		}
		d := m.Plan.Databases[dbIndex(m, t.ID)]
		switch {
		case !emptyDatabases:
			rep.Notes = append(rep.Notes, fmt.Sprintf("The database %s was left as it is. Confirm emptying databases to remove what was imported.", d.Dest))
		case !t.StartedEmpty:
			rep.Kept = append(rep.Kept, d.Dest+" (it had tables before the migration, so what is yours cannot be told apart from what was imported)")
		default:
			since := time.Now().UTC()
			if t.Finished != nil {
				since = *t.Finished
			} else if t.Started != nil {
				since = *t.Started
			}
			newer, err := e.DB.Reset(ctx, d.Dest, since)
			switch {
			case err != nil:
				rep.Kept = append(rep.Kept, d.Dest+" ("+err.Error()+")")
			case newer:
				rep.Kept = append(rep.Kept, d.Dest+" (a table was written to after the import: newer data is never removed)")
			default:
				rep.DatabasesEmptied = append(rep.DatabasesEmptied, d.Dest)
				m.Tasks[i].Checkpoint = ""
			}
		}
	}
	fmt.Fprintf(log, "Rollback: %d file(s) removed, %d folder(s) removed, %d database(s) emptied, %d kept.\n", rep.FilesRemoved, rep.DirsRemoved, len(rep.DatabasesEmptied), len(rep.Kept))
	now := time.Now().UTC()
	m.Status, m.Finished, m.Rollback, m.Error = RolledBack, &now, rep, ""
	m.Remaining = nil
	e.cleanTemp(id)
	return rep, e.Store.Save(m)
}

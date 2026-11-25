// Package swapmgr shows the server's swap memory and creates, resizes and removes its swap file.
//
// Only two fixed files are ever managed, /swapfile (what NimVor creates) and /swap.img (what Ubuntu's installer
// makes), so no path comes from the caller. Changing swap can take memory away from running programs, so every
// change follows the same order and undoes itself when a step fails:
//
//  1. Detect: the file system must hold swap files (ext4 or xfs, not btrfs, ZFS or a network or memory file system),
//     there must be enough free disk space for the new file next to the old one, and turning the old swap off must fit
//     in free memory (otherwise the kernel's out-of-memory killer could stop the panel or a website).
//  2. Build: the new file is made beside the old one (<path>.new), with mode 0600, allocated and formatted. Nothing in
//     use is touched until it is ready.
//  3. Switch: the old swap is turned off, the files are swapped by renaming, the new one is turned on. If that fails
//     the old file is put back and turned on again.
//  4. Persist: /etc/fstab gets one swap line for the file, its other lines unchanged; the file is restored on failure.
//  5. Verify: /proc/swaps must list the file at the size asked for.
package swapmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/quota"
)

// minMB is the smallest swap file (a variable so tests need not allocate 256 MB).
var minMB int64 = 256

const (
	// MaxMB bounds a swap file.
	MaxMB = 65536
	mib   = int64(1) << 20
	gib   = int64(1) << 30
)

// Files are the only swap files NimVor manages.
var Files = []string{"/swapfile", "/swap.img"}

func managed(p string) bool {
	for _, f := range Files {
		if p == f {
			return true
		}
	}
	return false
}

// Entry is one active swap area from /proc/swaps.
type Entry struct {
	Path      string `json:"path"`
	Type      string `json:"type"` // file or partition
	SizeBytes int64  `json:"size_bytes"`
	UsedBytes int64  `json:"used_bytes"`
	Priority  int    `json:"priority"`
	Managed   bool   `json:"managed"` // NimVor may resize or remove it
}

// Status is what the Swap page shows.
type Status struct {
	Entries      []Entry `json:"entries"`
	MemTotal     int64   `json:"mem_total_bytes"`
	MemAvailable int64   `json:"mem_available_bytes"`
	SwapTotal    int64   `json:"swap_total_bytes"`
	SwapUsed     int64   `json:"swap_used_bytes"`
	Swappiness   int     `json:"swappiness"`
	FSType       string  `json:"fs_type"`
	FSFreeBytes  int64   `json:"fs_free_bytes"`
	Target       string  `json:"target"`    // the file a change would use
	InFstab      bool    `json:"in_fstab"`  // the target is listed in /etc/fstab
	Supported    bool    `json:"supported"` // a swap file can be made here
	Reason       string  `json:"reason"`    // why not
	MaxMB        int64   `json:"max_mb"`    // the largest file a change may ask for now
	MinMB        int     `json:"min_mb"`
	Simulated    bool    `json:"simulated"`
}

func readInt(env *host.Env, path string) int64 {
	b, err := env.Root.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n
}

func meminfo(env *host.Env) (total, available int64) {
	b, err := env.Root.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(f[0], 10, 64)
		switch k {
		case "MemTotal":
			total = n * 1024
		case "MemAvailable":
			available = n * 1024
		}
	}
	return
}

// ParseSwaps reads /proc/swaps (sizes there are in KiB).
func ParseSwaps(data string) []Entry {
	var out []Entry
	for i, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 5 {
			continue
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		used, _ := strconv.ParseInt(f[3], 10, 64)
		prio, _ := strconv.Atoi(f[4])
		out = append(out, Entry{Path: f[0], Type: f[1], SizeBytes: size * 1024, UsedBytes: used * 1024, Priority: prio, Managed: managed(f[0]) && f[1] == "file"})
	}
	return out
}

func entries(env *host.Env) []Entry {
	b, err := env.Root.ReadFile("/proc/swaps")
	if err != nil {
		return nil
	}
	return ParseSwaps(string(b))
}

// fstabHas reports whether /etc/fstab has an active swap line for path.
func fstabHas(fstab, path string) bool {
	for _, line := range strings.Split(fstab, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && !strings.HasPrefix(f[0], "#") && f[0] == path && f[2] == "swap" {
			return true
		}
	}
	return false
}

// fstabWith returns fstab with one swap line for path and every other line unchanged (add) or without the swap
// lines of path (remove).
func fstabWith(fstab, path string, add bool) string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(fstab, "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && !strings.HasPrefix(f[0], "#") && f[0] == path && f[2] == "swap" {
			continue
		}
		out = append(out, line)
	}
	if add {
		out = append(out, path+" none swap sw 0 0")
	}
	return strings.Join(out, "\n") + "\n"
}

// fsOf finds the file system that holds "/" (where both files live).
func fsOf(env *host.Env) (fstype string, free int64) {
	if b, err := env.Root.ReadFile("/proc/self/mountinfo"); err == nil {
		if m, ok := quota.FindMount(string(b), "/"); ok {
			fstype = m.FSType
		}
	}
	if real, err := env.Root.Path("/"); err == nil {
		var st syscall.Statfs_t
		if syscall.Statfs(real, &st) == nil {
			free = int64(st.Bavail) * int64(st.Bsize)
		}
	}
	return
}

// Read reports the swap state and what a change may do now.
func Read(env *host.Env) Status {
	s := Status{Entries: entries(env), MinMB: int(minMB), Simulated: env.Cfg.SimulateCommands}
	if s.Entries == nil {
		s.Entries = []Entry{}
	}
	for _, e := range s.Entries {
		s.SwapTotal += e.SizeBytes
		s.SwapUsed += e.UsedBytes
	}
	s.MemTotal, s.MemAvailable = meminfo(env)
	s.Swappiness = int(readInt(env, "/proc/sys/vm/swappiness"))
	s.FSType, s.FSFreeBytes = fsOf(env)
	// The file a change uses: the managed file already in use, else /swapfile.
	s.Target = "/swapfile"
	for _, e := range s.Entries {
		if e.Managed {
			s.Target = e.Path
		}
	}
	if b, err := env.Root.ReadFile("/etc/fstab"); err == nil {
		s.InFstab = fstabHas(string(b), s.Target)
	}
	s.Supported, s.Reason = true, ""
	switch s.FSType {
	case "ext2", "ext3", "ext4", "xfs":
	case "":
		if !s.Simulated {
			s.Supported, s.Reason = false, "The file system of the server could not be read."
		}
	default:
		s.Supported, s.Reason = false, "A swap file cannot be made on a "+s.FSType+" file system here. Use a swap partition or another file system."
	}
	// Room for the new file next to the old one, and 1 GiB or 5% of the disk left over for the system.
	if s.Supported && s.FSFreeBytes > 0 {
		s.MaxMB = (s.FSFreeBytes - gib) / mib
		if s.MaxMB > MaxMB {
			s.MaxMB = MaxMB
		}
		if s.MaxMB < 0 {
			s.MaxMB = 0
		}
	}
	return s
}

func real(env *host.Env, p string) (string, error) { return env.Root.Path(p) }

func swapCmd(ctx context.Context, env *host.Env, id, path string) error {
	out, err := env.Runner.RunWith(ctx, id, execx.Opts{Capabilities: []string{"CAP_SYS_ADMIN"}, Timeout: 4 * time.Minute}, path)
	if err != nil {
		return fmt.Errorf("%s: %s", id, strings.TrimSpace(host.Output(out)))
	}
	return nil
}

// memoryAllowsSwapOff: the pages in swap go back to memory when swap is turned off; there must be room for them and
// for the system to breathe, or the kernel's out-of-memory killer starts stopping programs.
func memoryAllowsSwapOff(env *host.Env, used int64) error {
	total, avail := meminfo(env)
	if total == 0 {
		return nil
	}
	need := used + total/10 + 256*mib
	if avail < need {
		return actions.Failed("There is not enough free memory to turn the current swap off safely.",
			fmt.Sprintf("%d MiB of swap is in use and %d MiB of memory is free; at least %d MiB is needed. Stop something that uses a lot of memory, or try again when the server is quieter.", used/mib, avail/mib, need/mib))
	}
	return nil
}

func build(ctx context.Context, env *host.Env, path string, size int64) error {
	rp, err := real(env, path)
	if err != nil {
		return err
	}
	_ = os.Remove(rp)
	f, err := os.OpenFile(rp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := syscall.Fallocate(int(f.Fd()), 0, 0, size); err != nil {
		f.Close()
		_ = os.Remove(rp)
		return actions.Failed("The disk could not reserve space for the swap file.", err.Error())
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(rp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(rp)
		return err
	}
	if err := os.Chmod(rp, 0o600); err != nil {
		_ = os.Remove(rp)
		return err
	}
	if out, err := env.Runner.Run(ctx, "mkswap", path); err != nil {
		_ = os.Remove(rp)
		return actions.Failed("The swap file could not be formatted.", host.Output(out))
	}
	return nil
}

// Set creates the swap file or changes its size to sizeMB.
func Set(ctx context.Context, env *host.Env, sizeMB int64) (Status, error) {
	if sizeMB < minMB || sizeMB > MaxMB {
		return Status{}, actions.InvalidParams(fmt.Sprintf("size_mb: %d to %d", minMB, MaxMB))
	}
	st := Read(env)
	if !st.Supported {
		return st, actions.Failed("Swap cannot be changed here.", st.Reason)
	}
	path, size := st.Target, sizeMB*mib
	if env.Cfg.SimulateCommands {
		return st, nil
	}
	if st.MaxMB > 0 && sizeMB > st.MaxMB {
		return st, actions.InvalidParams(fmt.Sprintf("size_mb: the disk has room for at most %d MB now (the new file is made next to the old one, and 1 GB stays free)", st.MaxMB))
	}
	// The same size again is a no-op, not a rebuild.
	var cur *Entry
	for i := range st.Entries {
		if st.Entries[i].Path == path {
			cur = &st.Entries[i]
		}
	}
	if cur != nil && cur.SizeBytes >= size-4096 && cur.SizeBytes <= size+4096 && st.InFstab {
		return st, nil
	}
	if cur != nil {
		if err := memoryAllowsSwapOff(env, cur.UsedBytes); err != nil {
			return st, err
		}
	}
	tmp, old := path+".new", path+".old"
	cleanup := func() {
		for _, p := range []string{tmp, old} {
			if rp, err := real(env, p); err == nil {
				_ = os.Remove(rp)
			}
		}
	}
	cleanup()
	// 2. Build beside the old file.
	if err := build(ctx, env, tmp, size); err != nil {
		cleanup()
		return st, err
	}
	// 3. Switch.
	if cur != nil {
		if err := swapCmd(ctx, env, "swapoff", path); err != nil {
			cleanup()
			return st, actions.Failed("The current swap could not be turned off, so nothing was changed.", err.Error())
		}
	}
	rpath, _ := real(env, path)
	rtmp, _ := real(env, tmp)
	rold, _ := real(env, old)
	hadOld := false
	if _, err := os.Stat(rpath); err == nil {
		if err := os.Rename(rpath, rold); err != nil {
			if cur != nil {
				_ = swapCmd(ctx, env, "swapon", path)
			}
			cleanup()
			return st, actions.Failed("The old swap file could not be set aside, so nothing was changed.", err.Error())
		}
		hadOld = true
	}
	back := func(why string, err error) (Status, error) {
		_ = os.Remove(rpath)
		if hadOld {
			_ = os.Rename(rold, rpath)
			if cur != nil {
				_ = swapCmd(ctx, env, "swapon", path)
			}
		}
		cleanup()
		return Read(env), actions.Failed(why+" The previous swap was put back.", err.Error())
	}
	if err := os.Rename(rtmp, rpath); err != nil {
		return back("The new swap file could not be put in place.", err)
	}
	if err := swapCmd(ctx, env, "swapon", path); err != nil {
		return back("The new swap file could not be turned on.", err)
	}
	// 4. Persist.
	if err := persist(env, path, true); err != nil {
		// The file works until the next restart; say so rather than undo working swap.
		cleanup()
		return Read(env), actions.Failed("Swap is on, but /etc/fstab could not be updated, so it would not come back after a restart.", err.Error())
	}
	cleanup()
	// 5. Verify.
	after := Read(env)
	for _, e := range after.Entries {
		if e.Path == path && e.SizeBytes >= size-int64(1<<20) {
			return after, nil
		}
	}
	return after, actions.Failed("Swap was changed, but it does not show up at the expected size.", "Check /proc/swaps and the Swap page.")
}

func persist(env *host.Env, path string, add bool) error {
	b, _ := env.Root.ReadFile("/etc/fstab")
	next := fstabWith(string(b), path, add)
	if next == string(b) {
		return nil
	}
	_, err := env.Writer.WriteFile("/etc/fstab", []byte(next), 0o644, agentfs.RootOwner)
	return err
}

// Remove turns the managed swap file off and deletes it.
func Remove(ctx context.Context, env *host.Env) (Status, error) {
	st := Read(env)
	var cur *Entry
	for i := range st.Entries {
		if st.Entries[i].Managed {
			cur = &st.Entries[i]
		}
	}
	var path string
	if cur != nil {
		path = cur.Path
	} else if rp, err := real(env, "/swapfile"); err == nil {
		if _, err := os.Stat(rp); err == nil {
			path = "/swapfile"
		}
	}
	if path == "" {
		return st, actions.Failed("There is no swap file managed by NimVor to remove.", "Swap on a partition, or a swap file elsewhere, is left alone.")
	}
	if env.Cfg.SimulateCommands {
		return st, nil
	}
	if cur != nil {
		if err := memoryAllowsSwapOff(env, cur.UsedBytes); err != nil {
			return st, err
		}
		if err := swapCmd(ctx, env, "swapoff", path); err != nil {
			return st, actions.Failed("Swap could not be turned off, so nothing was changed.", err.Error())
		}
	}
	if err := persist(env, path, false); err != nil {
		if cur != nil {
			_ = swapCmd(ctx, env, "swapon", path)
		}
		return Read(env), actions.Failed("/etc/fstab could not be updated, so nothing was changed.", err.Error())
	}
	if rp, err := real(env, path); err == nil {
		if err := os.Remove(rp); err != nil && !os.IsNotExist(err) {
			return Read(env), actions.Failed("Swap is off, but the file could not be deleted.", err.Error())
		}
	}
	return Read(env), nil
}

// Register adds the swap actions.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "swap.status", Description: "Swap memory: the active swap areas, memory and swap use, the file system, and the size a change may ask for (read-only).", Handler: func(context.Context, json.RawMessage) (any, error) {
		return Read(env), nil
	}})
	r.Register(actions.Action{Name: "swap.set", Mutating: true, Timeout: 10 * time.Minute, Description: "Create NimVor's swap file (/swapfile) or change the size of the swap file in use (/swapfile or /swap.img). The new file is built beside the old one, the swap is switched over, /etc/fstab is updated and the result is verified; on failure the previous swap is put back. Refused when the file system cannot hold swap, the disk is too full, or memory is too tight to turn the old swap off.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			SizeMB int64 `json:"size_mb"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return Set(ctx, env, in.SizeMB)
	}})
	r.Register(actions.Action{Name: "swap.remove", Mutating: true, Timeout: 10 * time.Minute, Description: "Turn off and delete the swap file NimVor manages (/swapfile or /swap.img) and remove its /etc/fstab line. Refused when memory is too tight to turn it off. Swap partitions are never touched.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return Remove(ctx, env)
	}})
}

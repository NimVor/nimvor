package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
)

// ListenUnix creates the agent socket with restrictive permissions.
//
// A stale socket from a previous run is removed, but any other kind of file
// at the path is left untouched and reported as an error.
func ListenUnix(path string, mode os.FileMode, group string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket; refusing to remove it", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, mode); err != nil {
		ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}
	if group != "" {
		gid, err := lookupGID(group)
		if err != nil {
			ln.Close()
			return nil, err
		}
		if err := os.Chown(path, -1, gid); err != nil {
			ln.Close()
			return nil, fmt.Errorf("chown socket: %w", err)
		}
		// The directory must be traversable by the group.
		_ = os.Chown(dir, -1, gid)
	}
	return ln, nil
}

func lookupGID(group string) (int, error) {
	g, err := user.LookupGroup(group)
	if err != nil {
		return -1, fmt.Errorf("socket group %q: %w", group, err)
	}
	return strconv.Atoi(g.Gid)
}

// GroupMembersUIDs returns the UIDs whose primary group is group, plus
// root. It is used for the default peer policy.
func GroupMembersUIDs(group string) map[int]bool {
	allowed := map[int]bool{0: true}
	if group == "" {
		return allowed
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return allowed
	}
	if u, err := user.Lookup(group); err == nil && u.Gid == g.Gid {
		if uid, err := strconv.Atoi(u.Uid); err == nil {
			allowed[uid] = true
		}
	}
	return allowed
}

package sites

import (
	"io/fs"
	"syscall"
)

// inodeOf is the inode of a file (0 when unknown).
func inodeOf(fi fs.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

// ownerOf is the owning user id of a file (-1 when unknown).
func ownerOf(fi fs.FileInfo) int {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}

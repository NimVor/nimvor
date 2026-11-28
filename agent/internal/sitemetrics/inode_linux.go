package sitemetrics

import (
	"io/fs"
	"syscall"
)

func inode(i fs.FileInfo) uint64 {
	if st, ok := i.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

func ownerUID(i fs.FileInfo) (uint32, bool) {
	if st, ok := i.Sys().(*syscall.Stat_t); ok {
		return st.Uid, true
	}
	return 0, false
}

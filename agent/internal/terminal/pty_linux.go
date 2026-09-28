//go:build linux

package terminal

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func ioctl(fd, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// openPTY allocates a pseudo terminal. It returns the master (non-blocking,
// so closing it ends a pending read) and the path of the slave.
func openPTY() (*os.File, string, error) {
	fd, err := syscall.Open("/dev/ptmx", syscall.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	var unlock int32
	if err := ioctl(uintptr(fd), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		syscall.Close(fd)
		return nil, "", err
	}
	var n uint32
	if err := ioctl(uintptr(fd), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		syscall.Close(fd)
		return nil, "", err
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return nil, "", err
	}
	return os.NewFile(uintptr(fd), "/dev/ptmx"), fmt.Sprintf("/dev/pts/%d", n), nil
}

// setSize sets the terminal's size (the shell gets SIGWINCH).
func setSize(master *os.File, cols, rows int) error {
	ws := struct{ Row, Col, X, Y uint16 }{Row: uint16(rows), Col: uint16(cols)}
	raw, err := master.SyscallConn()
	if err != nil {
		return err
	}
	var ierr error
	if err := raw.Control(func(fd uintptr) {
		ierr = ioctl(fd, syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
	}); err != nil {
		return err
	}
	return ierr
}

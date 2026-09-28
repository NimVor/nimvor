//go:build !linux

package terminal

import (
	"errors"
	"os"
)

func openPTY() (*os.File, string, error) {
	return nil, "", errors.New("pseudo terminals are only supported on Linux")
}

func setSize(*os.File, int, int) error { return nil }

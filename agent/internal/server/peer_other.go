//go:build !linux

package server

import (
	"errors"
	"net"
)

// peerUID is only implemented on Linux; elsewhere peers are rejected when a
// PeerAllowed policy is configured.
func peerUID(_ *net.UnixConn) (int, error) {
	return -1, errors.New("peer credentials are not supported on this platform")
}

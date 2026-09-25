package dnsmgr

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// querySOA asks a server (host:port, UDP) for a zone's SOA serial, to check
// that BIND serves what was just written.
func querySOA(ctx context.Context, server, zone string) (uint32, error) {
	var id [2]byte
	rand.Read(id[:])
	msg := []byte{id[0], id[1], 0x00, 0x00, 0, 1, 0, 0, 0, 0, 0, 0} // no recursion desired, 1 question
	for _, l := range strings.Split(strings.TrimSuffix(zone, "."), ".") {
		if len(l) == 0 || len(l) > 63 {
			return 0, fmt.Errorf("invalid zone name")
		}
		msg = append(msg, byte(len(l)))
		msg = append(msg, l...)
	}
	msg = append(msg, 0, 0, 6, 0, 1) // QTYPE SOA, QCLASS IN
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "udp", server)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(msg); err != nil {
		return 0, err
	}
	buf := make([]byte, 4096)
	n, err := c.Read(buf)
	if err != nil {
		return 0, err
	}
	return parseSOAResponse(buf[:n], id)
}

func parseSOAResponse(b []byte, id [2]byte) (uint32, error) {
	if len(b) < 12 || b[0] != id[0] || b[1] != id[1] {
		return 0, errors.New("unexpected DNS reply")
	}
	if rcode := b[3] & 0x0f; rcode != 0 {
		return 0, fmt.Errorf("DNS error code %d (the zone is not loaded)", rcode)
	}
	if b[2]&0x04 == 0 {
		return 0, errors.New("the answer is not authoritative (the zone is not loaded)")
	}
	qd, an := binary.BigEndian.Uint16(b[4:]), binary.BigEndian.Uint16(b[6:])
	off := 12
	for i := 0; i < int(qd); i++ {
		var err error
		if off, err = skipName(b, off); err != nil {
			return 0, err
		}
		off += 4
	}
	for i := 0; i < int(an); i++ {
		var err error
		if off, err = skipName(b, off); err != nil {
			return 0, err
		}
		if off+10 > len(b) {
			return 0, errors.New("short DNS reply")
		}
		typ := binary.BigEndian.Uint16(b[off:])
		rdlen := int(binary.BigEndian.Uint16(b[off+8:]))
		off += 10
		if off+rdlen > len(b) {
			return 0, errors.New("short DNS reply")
		}
		if typ == 6 {
			p := off
			for j := 0; j < 2; j++ { // MNAME, RNAME
				if p, err = skipName(b, p); err != nil {
					return 0, err
				}
			}
			if p+4 > len(b) {
				return 0, errors.New("short SOA record")
			}
			return binary.BigEndian.Uint32(b[p:]), nil
		}
		off += rdlen
	}
	return 0, errors.New("no SOA record in the answer")
}

func skipName(b []byte, off int) (int, error) {
	for steps := 0; steps < 128; steps++ {
		if off >= len(b) {
			return 0, errors.New("short DNS name")
		}
		l := int(b[off])
		switch {
		case l == 0:
			return off + 1, nil
		case l&0xc0 == 0xc0:
			return off + 2, nil
		}
		off += 1 + l
	}
	return 0, errors.New("DNS name too long")
}

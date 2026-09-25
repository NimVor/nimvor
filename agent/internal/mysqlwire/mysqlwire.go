// Package mysqlwire is a minimal MySQL/MariaDB client (text protocol over
// a local Unix socket) used by the database browser. It supports
// mysql_native_password and caching_sha2_password authentication,
// multi-statement queries and exact NULL/binary handling, which the mysql
// command-line client's output cannot express.
package mysqlwire

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // required by mysql_native_password
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	capLongPassword    = 0x1
	capLongFlag        = 0x4
	capConnectWithDB   = 0x8
	capProtocol41      = 0x200
	capTransactions    = 0x2000
	capSecureConn      = 0x8000
	capMultiStatements = 0x10000
	capMultiResults    = 0x20000
	capPluginAuth      = 0x80000
	capPluginAuthLenc  = 0x200000

	statusMoreResults = 0x0008

	charsetUTF8MB4 = 45 // utf8mb4_general_ci, known to MySQL and MariaDB
	charsetBinary  = 63

	maxPacket = 1<<24 - 1
)

// Error is a server error (ERR packet).
type Error struct {
	Code     uint16
	SQLState string
	Message  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("ERROR %d (%s): %s", e.Code, e.SQLState, e.Message)
}

// Column describes a result column.
type Column struct {
	Name     string `json:"name"`
	Table    string `json:"table,omitempty"`
	Type     byte   `json:"type"`
	Flags    uint16 `json:"flags"`
	Charset  uint16 `json:"-"`
	Length   uint32 `json:"length"`
	Decimals byte   `json:"decimals"`
}

// Binary reports whether values are bytes rather than text.
func (c Column) Binary() bool {
	switch c.Type {
	case 0xf9, 0xfa, 0xfb, 0xfc, 0xfd, 0xfe, 0x0f: // blobs, var_string, string, varchar
		return c.Charset == charsetBinary
	case 0xff: // geometry
		return true
	}
	return false
}

// Numeric reports whether the column holds numbers.
func (c Column) Numeric() bool {
	switch c.Type {
	case 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x08, 0x09, 0x10, 0xf6:
		return true
	}
	return false
}

// Result is one statement's result: columns and rows for a result set,
// or affected rows for other statements.
type Result struct {
	Columns      []Column
	Rows         [][]*[]byte // nil entry = NULL
	Truncated    bool        // more rows than the limit
	AffectedRows uint64
	InsertID     uint64
	Warnings     uint16
	Info         string
}

// Conn is one connection.
type Conn struct {
	c        net.Conn
	r        *bufio.Reader
	seq      byte
	ThreadID uint32
	Server   string
}

// Options for Dial.
type Options struct {
	Socket   string
	User     string
	Password string
	Database string
	Timeout  time.Duration
}

// Dial connects over a Unix socket and authenticates.
func Dial(ctx context.Context, o Options) (*Conn, error) {
	d := net.Dialer{Timeout: o.Timeout}
	nc, err := d.DialContext(ctx, "unix", o.Socket)
	if err != nil {
		return nil, err
	}
	c := &Conn{c: nc, r: bufio.NewReaderSize(nc, 64<<10)}
	if dl, ok := ctx.Deadline(); ok {
		nc.SetDeadline(dl)
	}
	if err := c.handshake(o); err != nil {
		nc.Close()
		return nil, err
	}
	nc.SetDeadline(time.Time{})
	return c, nil
}

// Close ends the connection.
func (c *Conn) Close() error {
	c.seq = 0
	c.writePacket([]byte{0x01}) // COM_QUIT
	return c.c.Close()
}

// SetDeadline bounds the next operations.
func (c *Conn) SetDeadline(t time.Time) { c.c.SetDeadline(t) }

func (c *Conn) readPacket() ([]byte, error) {
	var out []byte
	for {
		var h [4]byte
		if _, err := io.ReadFull(c.r, h[:]); err != nil {
			return nil, err
		}
		n := int(h[0]) | int(h[1])<<8 | int(h[2])<<16
		c.seq = h[3] + 1
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return nil, err
		}
		out = append(out, buf...)
		if n < maxPacket {
			return out, nil
		}
	}
}

func (c *Conn) writePacket(p []byte) error {
	for {
		n := min(len(p), maxPacket)
		h := []byte{byte(n), byte(n >> 8), byte(n >> 16), c.seq}
		c.seq++
		if _, err := c.c.Write(append(h, p[:n]...)); err != nil {
			return err
		}
		p = p[n:]
		if n < maxPacket {
			return nil
		}
	}
}

func parseErr(p []byte) error {
	e := &Error{}
	if len(p) >= 3 {
		e.Code = binary.LittleEndian.Uint16(p[1:3])
		rest := p[3:]
		if len(rest) >= 6 && rest[0] == '#' {
			e.SQLState = string(rest[1:6])
			rest = rest[6:]
		}
		e.Message = string(rest)
	}
	return e
}

func nulString(p []byte) (string, []byte) {
	i := bytes.IndexByte(p, 0)
	if i < 0 {
		return string(p), nil
	}
	return string(p[:i]), p[i+1:]
}

func (c *Conn) handshake(o Options) error {
	p, err := c.readPacket()
	if err != nil {
		return err
	}
	if len(p) > 0 && p[0] == 0xff {
		return parseErr(p)
	}
	if len(p) < 1 || p[0] != 10 {
		return errors.New("unsupported MySQL protocol")
	}
	c.Server, p = nulString(p[1:])
	if len(p) < 4+8+1+2 {
		return errors.New("short handshake")
	}
	c.ThreadID = binary.LittleEndian.Uint32(p)
	nonce := append([]byte{}, p[4:12]...)
	p = p[13:]
	caps := uint32(binary.LittleEndian.Uint16(p))
	p = p[2:]
	plugin := "mysql_native_password"
	if len(p) >= 16 {
		caps |= uint32(binary.LittleEndian.Uint16(p[3:5])) << 16
		authLen := int(p[5])
		p = p[16:]
		if caps&capSecureConn != 0 {
			n := max(13, authLen-8)
			if n > len(p) {
				n = len(p)
			}
			nonce = append(nonce, bytes.TrimRight(p[:n], "\x00")...)
			p = p[n:]
		}
		if caps&capPluginAuth != 0 && len(p) > 0 {
			plugin, _ = nulString(p)
		}
	}
	if caps&capProtocol41 == 0 {
		return errors.New("server too old")
	}
	want := uint32(capLongPassword | capLongFlag | capProtocol41 | capTransactions | capSecureConn | capMultiStatements | capMultiResults | capPluginAuth | capPluginAuthLenc)
	if o.Database != "" {
		want |= capConnectWithDB
	}
	auth := scramble(plugin, o.Password, nonce)
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, want)
	binary.Write(&b, binary.LittleEndian, uint32(maxPacket))
	b.WriteByte(charsetUTF8MB4)
	b.Write(make([]byte, 23))
	b.WriteString(o.User)
	b.WriteByte(0)
	b.Write(lenenc(uint64(len(auth))))
	b.Write(auth)
	if o.Database != "" {
		b.WriteString(o.Database)
		b.WriteByte(0)
	}
	b.WriteString(plugin)
	b.WriteByte(0)
	if err := c.writePacket(b.Bytes()); err != nil {
		return err
	}
	for i := 0; i < 5; i++ {
		p, err := c.readPacket()
		if err != nil {
			return err
		}
		if len(p) == 0 {
			return errors.New("empty authentication reply")
		}
		switch p[0] {
		case 0x00:
			return nil
		case 0xff:
			return parseErr(p)
		case 0xfe: // auth switch
			plugin, p = nulString(p[1:])
			nonce = bytes.TrimRight(p, "\x00")
			if err := c.writePacket(scramble(plugin, o.Password, nonce)); err != nil {
				return err
			}
		case 0x01: // more data (caching_sha2_password)
			if len(p) >= 2 && p[1] == 3 {
				continue // fast auth succeeded; OK follows
			}
			if len(p) >= 2 && p[1] == 4 {
				// Full authentication: the password may be sent as is over
				// a Unix socket (a secure transport).
				if err := c.writePacket(append([]byte(o.Password), 0)); err != nil {
					return err
				}
				continue
			}
			return errors.New("unsupported authentication step")
		default:
			return errors.New("unexpected authentication reply")
		}
	}
	return errors.New("authentication did not complete")
}

func scramble(plugin, password string, nonce []byte) []byte {
	if password == "" {
		return nil
	}
	switch plugin {
	case "caching_sha2_password":
		h1 := sha256.Sum256([]byte(password))
		h2 := sha256.Sum256(h1[:])
		h3 := sha256.Sum256(append(h2[:], nonce...))
		out := make([]byte, 32)
		for i := range out {
			out[i] = h1[i] ^ h3[i]
		}
		return out
	case "mysql_clear_password":
		return append([]byte(password), 0)
	default: // mysql_native_password
		h1 := sha1.Sum([]byte(password)) //nolint:gosec
		h2 := sha1.Sum(h1[:])            //nolint:gosec
		h3 := sha1.Sum(append(append([]byte{}, nonce...), h2[:]...))
		out := make([]byte, 20)
		for i := range out {
			out[i] = h1[i] ^ h3[i]
		}
		return out
	}
}

func lenenc(n uint64) []byte {
	switch {
	case n < 251:
		return []byte{byte(n)}
	case n < 1<<16:
		return []byte{0xfc, byte(n), byte(n >> 8)}
	case n < 1<<24:
		return []byte{0xfd, byte(n), byte(n >> 8), byte(n >> 16)}
	}
	b := make([]byte, 9)
	b[0] = 0xfe
	binary.LittleEndian.PutUint64(b[1:], n)
	return b
}

// readLenenc returns the integer, whether it is NULL, and the rest.
func readLenenc(p []byte) (uint64, bool, []byte, error) {
	if len(p) == 0 {
		return 0, false, nil, io.ErrUnexpectedEOF
	}
	switch p[0] {
	case 0xfb:
		return 0, true, p[1:], nil
	case 0xfc:
		if len(p) < 3 {
			return 0, false, nil, io.ErrUnexpectedEOF
		}
		return uint64(binary.LittleEndian.Uint16(p[1:])), false, p[3:], nil
	case 0xfd:
		if len(p) < 4 {
			return 0, false, nil, io.ErrUnexpectedEOF
		}
		return uint64(p[1]) | uint64(p[2])<<8 | uint64(p[3])<<16, false, p[4:], nil
	case 0xfe:
		if len(p) < 9 {
			return 0, false, nil, io.ErrUnexpectedEOF
		}
		return binary.LittleEndian.Uint64(p[1:]), false, p[9:], nil
	}
	return uint64(p[0]), false, p[1:], nil
}

func readLenencBytes(p []byte) ([]byte, bool, []byte, error) {
	n, null, rest, err := readLenenc(p)
	if err != nil || null {
		return nil, null, rest, err
	}
	if uint64(len(rest)) < n {
		return nil, false, nil, io.ErrUnexpectedEOF
	}
	return rest[:n], false, rest[n:], nil
}

// Limits bound what Query keeps in memory.
type Limits struct {
	MaxRows  int // per result set
	MaxBytes int // all values together
}

// Query runs one or more statements (multi-statement) and returns every
// result. On a server error, the results before it are returned with the
// error. Rows beyond the limits are read and discarded.
func (c *Conn) Query(sql string, lim Limits) ([]Result, error) {
	c.seq = 0
	if err := c.writePacket(append([]byte{0x03}, sql...)); err != nil {
		return nil, err
	}
	var out []Result
	used := 0
	for {
		p, err := c.readPacket()
		if err != nil {
			return out, err
		}
		if len(p) == 0 {
			return out, errors.New("empty reply")
		}
		var res Result
		more := false
		switch p[0] {
		case 0xff:
			return out, parseErr(p)
		case 0x00:
			rest := p[1:]
			res.AffectedRows, _, rest, _ = readLenenc(rest)
			res.InsertID, _, rest, _ = readLenenc(rest)
			if len(rest) >= 4 {
				more = binary.LittleEndian.Uint16(rest)&statusMoreResults != 0
				res.Warnings = binary.LittleEndian.Uint16(rest[2:])
				// Info is length-prefixed by some servers.
				if info := rest[4:]; len(info) > 0 && int(info[0]) == len(info)-1 {
					res.Info = string(info[1:])
				} else {
					res.Info = string(info)
				}
			}
		case 0xfb:
			return out, errors.New("LOAD DATA LOCAL is not supported")
		default:
			n, _, _, err := readLenenc(p)
			if err != nil {
				return out, err
			}
			for i := uint64(0); i < n; i++ {
				cp, err := c.readPacket()
				if err != nil {
					return out, err
				}
				col, err := parseColumn(cp)
				if err != nil {
					return out, err
				}
				res.Columns = append(res.Columns, col)
			}
			if p, err = c.readPacket(); err != nil { // EOF after columns
				return out, err
			}
			for {
				rp, err := c.readPacket()
				if err != nil {
					return out, err
				}
				if rp[0] == 0xff {
					out = append(out, res)
					return out, parseErr(rp)
				}
				if rp[0] == 0xfe && len(rp) < 9 {
					if len(rp) >= 5 {
						more = binary.LittleEndian.Uint16(rp[3:])&statusMoreResults != 0
						res.Warnings = binary.LittleEndian.Uint16(rp[1:])
					}
					break
				}
				if (lim.MaxRows > 0 && len(res.Rows) >= lim.MaxRows) || (lim.MaxBytes > 0 && used >= lim.MaxBytes) {
					res.Truncated = true
					continue
				}
				row := make([]*[]byte, 0, len(res.Columns))
				rest := rp
				for range res.Columns {
					var v []byte
					var null bool
					v, null, rest, err = readLenencBytes(rest)
					if err != nil {
						return out, err
					}
					if null {
						row = append(row, nil)
					} else {
						cp := append([]byte{}, v...)
						used += len(cp)
						row = append(row, &cp)
					}
				}
				res.Rows = append(res.Rows, row)
			}
		}
		out = append(out, res)
		if !more {
			return out, nil
		}
	}
}

func parseColumn(p []byte) (Column, error) {
	var parts [6][]byte
	var err error
	rest := p
	for i := range parts {
		if parts[i], _, rest, err = readLenencBytes(rest); err != nil {
			return Column{}, err
		}
	}
	_, _, rest, err = readLenenc(rest) // length of fixed fields (0x0c)
	if err != nil || len(rest) < 10 {
		return Column{}, io.ErrUnexpectedEOF
	}
	return Column{
		Table:    string(parts[2]),
		Name:     string(parts[4]),
		Charset:  binary.LittleEndian.Uint16(rest),
		Length:   binary.LittleEndian.Uint32(rest[2:]),
		Type:     rest[6],
		Flags:    binary.LittleEndian.Uint16(rest[7:]),
		Decimals: rest[9],
	}, nil
}

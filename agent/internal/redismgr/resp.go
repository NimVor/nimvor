package redismgr

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// maxBulk bounds any single reply read from Redis.
const maxBulk = 16 << 20

// ErrReply is an error reply from Redis ("-ERR ...").
type ErrReply string

func (e ErrReply) Error() string { return string(e) }

// conn is a minimal RESP2 connection: binary-safe and without redis-cli, so
// values with newlines or arbitrary bytes round-trip exactly and the
// password is sent in the protocol, never in process arguments.
type conn struct {
	c net.Conn
	r *bufio.Reader
}

// dial connects to the local Redis (Unix socket when configured, otherwise
// 127.0.0.1:port) and authenticates with the configured password.
func (c Client) dial(ctx context.Context) (*conn, error) {
	port, socket, pass := c.conf()
	if c.pass != nil {
		pass = *c.pass
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	var nc net.Conn
	var err error
	if socket != "" && c.Env.Root.Exists(socket) {
		real, perr := c.Env.Root.Path(socket)
		if perr != nil {
			return nil, perr
		}
		nc, err = d.DialContext(ctx, "unix", real)
	} else {
		nc, err = d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
	}
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Redis: %w", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	nc.SetDeadline(deadline)
	x := &conn{c: nc, r: bufio.NewReaderSize(nc, 64<<10)}
	if pass != "" {
		if _, err := x.do("AUTH", pass); err != nil {
			nc.Close()
			return nil, errors.New("Redis rejected the configured password")
		}
	}
	return x, nil
}

func (x *conn) Close() error { return x.c.Close() }

// do sends one command and returns its reply: string (simple), []byte
// (bulk), int64, []any (array) or nil.
func (x *conn) do(args ...string) (any, error) {
	buf := make([]byte, 0, 64)
	buf = append(buf, '*')
	buf = strconv.AppendInt(buf, int64(len(args)), 10)
	buf = append(buf, '\r', '\n')
	for _, a := range args {
		buf = append(buf, '$')
		buf = strconv.AppendInt(buf, int64(len(a)), 10)
		buf = append(buf, '\r', '\n')
		buf = append(buf, a...)
		buf = append(buf, '\r', '\n')
	}
	if _, err := x.c.Write(buf); err != nil {
		return nil, err
	}
	return x.read(0)
}

func (x *conn) line() ([]byte, error) {
	l, err := x.r.ReadSlice('\n')
	if err != nil {
		return nil, err
	}
	if len(l) < 3 || l[len(l)-2] != '\r' {
		return nil, errors.New("malformed reply from Redis")
	}
	return l[:len(l)-2], nil
}

func (x *conn) read(depth int) (any, error) {
	if depth > 8 {
		return nil, errors.New("reply nested too deeply")
	}
	l, err := x.line()
	if err != nil {
		return nil, err
	}
	body := string(l[1:])
	switch l[0] {
	case '+':
		return body, nil
	case '-':
		return nil, ErrReply(body)
	case ':':
		return strconv.ParseInt(body, 10, 64)
	case '$':
		n, err := strconv.Atoi(body)
		if err != nil || n < -1 || n > maxBulk {
			return nil, errors.New("reply too large")
		}
		if n == -1 {
			return nil, nil
		}
		b := make([]byte, n+2)
		if _, err := io.ReadFull(x.r, b); err != nil {
			return nil, err
		}
		return b[:n], nil
	case '*':
		n, err := strconv.Atoi(body)
		if err != nil || n < -1 || n > 1<<20 {
			return nil, errors.New("reply too large")
		}
		if n == -1 {
			return nil, nil
		}
		out := make([]any, n)
		for i := range out {
			if out[i], err = x.read(depth + 1); err != nil {
				var er ErrReply
				if !errors.As(err, &er) {
					return nil, err
				}
				out[i] = err
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("unexpected reply type %q", l[0])
}

// Reply helpers.
func asBytes(v any) []byte {
	switch t := v.(type) {
	case []byte:
		return t
	case string:
		return []byte(t)
	}
	return nil
}

func asInt(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case []byte:
		n, _ := strconv.ParseInt(string(t), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	}
	return 0
}

func asArray(v any) []any {
	a, _ := v.([]any)
	return a
}

package terminal

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// A minimal WebSocket server (RFC 6455) for the terminal: text and binary
// messages up to maxMessage bytes, ping/pong and close. Client frames must
// be masked; extensions and subprotocols are not negotiated.

const (
	opCont   = 0x0
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA

	maxMessage = 64 << 10
	wsGUID     = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

var errProtocol = errors.New("websocket protocol error")

type wsConn struct {
	c      net.Conn
	r      *bufio.Reader
	mu     sync.Mutex // serialises writes
	closed bool       // a close frame was sent: nothing may follow it
}

var errClosed = errors.New("websocket closed")

func headerHas(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, p := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(p), token) {
				return true
			}
		}
	}
	return false
}

func acceptKey(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// upgrade completes the handshake. It writes an HTTP error and returns nil
// when the request is not a valid WebSocket handshake.
func upgrade(w http.ResponseWriter, r *http.Request) *wsConn {
	key := r.Header.Get("Sec-WebSocket-Key")
	if raw, err := base64.StdEncoding.DecodeString(key); r.Method != http.MethodGet || err != nil || len(raw) != 16 ||
		!headerHas(r.Header, "Connection", "upgrade") || !headerHas(r.Header, "Upgrade", "websocket") ||
		r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "WebSocket handshake expected.", http.StatusBadRequest)
		return nil
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Not supported.", http.StatusInternalServerError)
		return nil
	}
	c, rw, err := hj.Hijack()
	if err != nil {
		return nil
	}
	_ = c.SetDeadline(time.Time{})
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + acceptKey(key) + "\r\n\r\n"
	if _, err := rw.WriteString(resp); err != nil || rw.Flush() != nil {
		c.Close()
		return nil
	}
	return &wsConn{c: c, r: rw.Reader}
}

// readFrame reads one frame (payload unmasked).
func (ws *wsConn) readFrame() (fin bool, op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(ws.r, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	if h[0]&0x70 != 0 { // no extensions were negotiated
		return false, 0, nil, errProtocol
	}
	op = h[0] & 0x0F
	if h[1]&0x80 == 0 { // clients must mask
		return false, 0, nil, errProtocol
	}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(ws.r, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(ws.r, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if op >= opClose && (n > 125 || !fin) {
		return false, 0, nil, errProtocol
	}
	if n > maxMessage {
		return false, 0, nil, errProtocol
	}
	var mask [4]byte
	if _, err = io.ReadFull(ws.r, mask[:]); err != nil {
		return
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(ws.r, payload); err != nil {
		return
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return fin, op, payload, nil
}

// ReadMessage returns the next text or binary message, answering pings
// on the way. io.EOF means the client closed the connection.
func (ws *wsConn) ReadMessage() (op byte, msg []byte, err error) {
	for {
		fin, fop, p, err := ws.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch fop {
		case opPing:
			if err := ws.write(opPong, p); err != nil {
				return 0, nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			// The caller ends the session and answers with Close.
			return 0, nil, io.EOF
		case opText, opBinary:
			if op != 0 {
				return 0, nil, errProtocol // a new message inside a fragmented one
			}
			op = fop
		case opCont:
			if op == 0 {
				return 0, nil, errProtocol
			}
		default:
			return 0, nil, errProtocol
		}
		if len(msg)+len(p) > maxMessage {
			return 0, nil, errProtocol
		}
		msg = append(msg, p...)
		if fin {
			return op, msg, nil
		}
	}
}

func (ws *wsConn) write(op byte, p []byte) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.closed {
		return errClosed
	}
	if op == opClose {
		ws.closed = true
	}
	h := make([]byte, 0, 10+len(p))
	h = append(h, 0x80|op)
	switch n := len(p); {
	case n < 126:
		h = append(h, byte(n))
	case n <= 0xFFFF:
		h = append(h, 126, byte(n>>8), byte(n))
	default:
		h = append(h, 127)
		h = binary.BigEndian.AppendUint64(h, uint64(n))
	}
	h = append(h, p...)
	_ = ws.c.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err := ws.c.Write(h)
	return err
}

// Close sends a close frame with a status code and closes the connection.
func (ws *wsConn) Close(code uint16, reason string) {
	p := binary.BigEndian.AppendUint16(nil, code)
	if len(reason) > 120 {
		reason = reason[:120]
	}
	_ = ws.write(opClose, append(p, reason...))
	ws.c.Close()
}

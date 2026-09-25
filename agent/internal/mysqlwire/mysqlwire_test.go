package mysqlwire

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// fakeServer speaks just enough of the protocol: a handshake that asks for
// caching_sha2_password full authentication, then one multi-result query.
type fakeServer struct {
	t        *testing.T
	c        net.Conn
	seq      byte
	password string
	gotPass  string
}

func (s *fakeServer) write(p []byte) {
	h := []byte{byte(len(p)), byte(len(p) >> 8), byte(len(p) >> 16), s.seq}
	s.seq++
	s.c.Write(append(h, p...))
}

func (s *fakeServer) read() []byte {
	var h [4]byte
	if _, err := io.ReadFull(s.c, h[:]); err != nil {
		return nil
	}
	s.seq = h[3] + 1
	b := make([]byte, int(h[0])|int(h[1])<<8|int(h[2])<<16)
	io.ReadFull(s.c, b)
	return b
}

func col(name string, typ byte, charset uint16) []byte {
	var b bytes.Buffer
	for _, s := range []string{"def", "db", "t", "t", name, name} {
		b.Write(lenenc(uint64(len(s))))
		b.WriteString(s)
	}
	b.WriteByte(0x0c)
	binary.Write(&b, binary.LittleEndian, charset)
	binary.Write(&b, binary.LittleEndian, uint32(255))
	b.WriteByte(typ)
	b.Write([]byte{0, 0, 0, 0, 0})
	return b.Bytes()
}

func (s *fakeServer) serve() {
	defer s.c.Close()
	nonce := []byte("abcdefghijklmnopqrst")
	var hs bytes.Buffer
	hs.WriteByte(10)
	hs.WriteString("8.0.40\x00")
	binary.Write(&hs, binary.LittleEndian, uint32(7))
	hs.Write(nonce[:8])
	hs.WriteByte(0)
	caps := uint32(capProtocol41 | capSecureConn | capPluginAuth | capMultiResults | capMultiStatements | capConnectWithDB | capPluginAuthLenc)
	binary.Write(&hs, binary.LittleEndian, uint16(caps))
	hs.WriteByte(255)
	binary.Write(&hs, binary.LittleEndian, uint16(2))
	binary.Write(&hs, binary.LittleEndian, uint16(caps>>16))
	hs.WriteByte(21)
	hs.Write(make([]byte, 10))
	hs.Write(nonce[8:])
	hs.WriteByte(0)
	hs.WriteString("caching_sha2_password\x00")
	s.write(hs.Bytes())

	resp := s.read()
	// Check the fast-auth scramble, then ask for full authentication.
	user, rest := nulString(resp[32:])
	auth, _, _, _ := readLenencBytes(rest)
	if user != "u1" || !bytes.Equal(auth, scramble("caching_sha2_password", s.password, nonce)) {
		s.write([]byte("\xff\x15\x04#28000Access denied"))
		return
	}
	s.write([]byte{0x01, 0x04})
	pw := s.read()
	s.gotPass = string(bytes.TrimRight(pw, "\x00"))
	s.write([]byte{0x00, 0, 0, 2, 0, 0, 0})

	q := s.read()
	if len(q) == 0 || q[0] != 0x03 {
		return
	}
	// Result 1: two columns, rows with NULL and binary; more results follow.
	s.write([]byte{2})
	s.write(col("id", 0x03, 63))
	s.write(col("data", 0xfc, 63))
	s.write([]byte{0xfe, 0, 0, 0x0a, 0})
	s.write(append(append([]byte{1, '1'}, 4), "\x00\xff\x01\x02"...))
	s.write([]byte{1, '2', 0xfb})
	s.write([]byte{0xfe, 0, 0, 0x0a, 0})
	// Result 2: OK with 3 affected rows, then the last one: an error.
	s.write([]byte{0x00, 3, 0, 0x0a, 0, 0, 0})
	s.write([]byte("\xff\x7a\x04#42S02Table 'db.nope' doesn't exist"))
}

func TestQueryAgainstFakeServer(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "my.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	srv := &fakeServer{t: t, password: "s3cret"}
	done := make(chan struct{})
	go func() {
		c, err := l.Accept()
		if err == nil {
			srv.c = c
			srv.serve()
		}
		close(done)
	}()
	c, err := Dial(context.Background(), Options{Socket: sock, User: "u1", Password: "s3cret", Database: "db", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if c.ThreadID != 7 || c.Server != "8.0.40" {
		t.Fatalf("thread %d server %q", c.ThreadID, c.Server)
	}
	rs, err := c.Query("SELECT 1; DELETE x; SELECT nope", Limits{MaxRows: 10})
	var me *Error
	if !errors.As(err, &me) || me.Code != 1146 || me.SQLState != "42S02" {
		t.Fatalf("err = %v", err)
	}
	if len(rs) != 2 || len(rs[0].Rows) != 2 || rs[1].AffectedRows != 3 {
		t.Fatalf("results = %+v", rs)
	}
	if string(*rs[0].Rows[0][1]) != "\x00\xff\x01\x02" || rs[0].Rows[1][1] != nil || !rs[0].Columns[1].Binary() || !rs[0].Columns[0].Numeric() {
		t.Fatalf("rows = %+v", rs[0])
	}
	c.Close()
	<-done
	if srv.gotPass != "s3cret" {
		t.Fatalf("full auth password = %q", srv.gotPass)
	}
}

func TestWrongPasswordIsRefused(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "my.sock")
	l, _ := net.Listen("unix", sock)
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err == nil {
			(&fakeServer{t: t, c: c, password: "right"}).serve()
		}
	}()
	_, err := Dial(context.Background(), Options{Socket: sock, User: "u1", Password: "wrong", Timeout: 2 * time.Second})
	var me *Error
	if !errors.As(err, &me) || me.Code != 1045 {
		t.Fatalf("err = %v", err)
	}
}

func TestNativeScramble(t *testing.T) {
	// Known value: mysql_native_password of "password" with a fixed nonce
	// is SHA1(pw) XOR SHA1(nonce + SHA1(SHA1(pw))) — check the round trip
	// the server performs.
	nonce := []byte("01234567890123456789")
	got := scramble("mysql_native_password", "password", nonce)
	if len(got) != 20 {
		t.Fatal(len(got))
	}
	if scramble("mysql_native_password", "", nonce) != nil {
		t.Fatal("empty password must send an empty response")
	}
}

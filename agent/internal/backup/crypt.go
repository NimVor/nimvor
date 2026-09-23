package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Encrypted backup files use a chunked AES-256-GCM stream so arbitrarily
// large archives are encrypted and authenticated without holding them in
// memory:
//
//	magic "WHACPBK1" | 4-byte random nonce prefix
//	repeated: 4-byte big-endian ciphertext length | ciphertext
//
// Chunk n uses nonce prefix||uint64(n) and additional data {final flag},
// so reordering, truncation and appending are all detected.
const (
	magic     = "WHACPBK1"
	chunkSize = 64 << 10
)

var errCorrupt = errors.New("the backup file is damaged or was encrypted with a different key")

// KeyID is a short, non-secret fingerprint of a key.
func KeyID(key []byte) string {
	h := sha256.Sum256(append([]byte("whacp-backup-key-id:"), key...))
	return hex.EncodeToString(h[:8])
}

// LoadOrCreateKey returns the 32-byte backup key stored at path (a host
// path), creating it (0600) on first use.
func LoadOrCreateKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) != 32 {
			return nil, fmt.Errorf("%s is not a valid backup key", path)
		}
		return b, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, err
	}
	return key, f.Close()
}

type encWriter struct {
	w      io.Writer
	aead   cipher.AEAD
	prefix [4]byte
	n      uint64
	buf    []byte
	closed bool
}

// NewEncryptWriter returns a writer that encrypts to w; Close must be
// called to write the final chunk (it does not close w).
func NewEncryptWriter(w io.Writer, key []byte) (io.WriteCloser, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	e := &encWriter{w: w, aead: aead, buf: make([]byte, 0, chunkSize)}
	if _, err := rand.Read(e.prefix[:]); err != nil {
		return nil, err
	}
	if _, err := w.Write(append([]byte(magic), e.prefix[:]...)); err != nil {
		return nil, err
	}
	return e, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("backup key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(prefix [4]byte, n uint64) []byte {
	b := make([]byte, 12)
	copy(b, prefix[:])
	binary.BigEndian.PutUint64(b[4:], n)
	return b
}

func (e *encWriter) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errors.New("write after close")
	}
	total := len(p)
	for len(p) > 0 {
		room := chunkSize - len(e.buf)
		if room == 0 {
			if err := e.flush(false); err != nil {
				return total - len(p), err
			}
			continue
		}
		if room > len(p) {
			room = len(p)
		}
		e.buf = append(e.buf, p[:room]...)
		p = p[room:]
	}
	return total, nil
}

func (e *encWriter) flush(final bool) error {
	ad := []byte{0}
	if final {
		ad[0] = 1
	}
	ct := e.aead.Seal(nil, nonce(e.prefix, e.n), e.buf, ad)
	e.n++
	e.buf = e.buf[:0]
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(ct)))
	if _, err := e.w.Write(l[:]); err != nil {
		return err
	}
	_, err := e.w.Write(ct)
	return err
}

func (e *encWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.flush(true)
}

type decReader struct {
	r      io.Reader
	aead   cipher.AEAD
	prefix [4]byte
	n      uint64
	buf    []byte
	final  bool
}

// NewDecryptReader authenticates and decrypts a stream made by
// NewEncryptWriter. A truncated or modified stream returns an error.
func NewDecryptReader(r io.Reader, key []byte) (io.Reader, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	head := make([]byte, len(magic)+4)
	if _, err := io.ReadFull(r, head); err != nil || string(head[:len(magic)]) != magic {
		return nil, errCorrupt
	}
	d := &decReader{r: r, aead: aead}
	copy(d.prefix[:], head[len(magic):])
	return d, nil
}

// IsEncrypted reports whether the first bytes carry the encryption header.
func IsEncrypted(head []byte) bool {
	return len(head) >= len(magic) && string(head[:len(magic)]) == magic
}

func (d *decReader) Read(p []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.final {
			return 0, io.EOF
		}
		var l [4]byte
		if _, err := io.ReadFull(d.r, l[:]); err != nil {
			return 0, errCorrupt // stream ended before the final chunk
		}
		size := binary.BigEndian.Uint32(l[:])
		if size < uint32(d.aead.Overhead()) || size > chunkSize+uint32(d.aead.Overhead()) {
			return 0, errCorrupt
		}
		ct := make([]byte, size)
		if _, err := io.ReadFull(d.r, ct); err != nil {
			return 0, errCorrupt
		}
		pt, err := d.aead.Open(nil, nonce(d.prefix, d.n), ct, []byte{0})
		if err != nil {
			pt, err = d.aead.Open(nil, nonce(d.prefix, d.n), ct, []byte{1})
			if err != nil {
				return 0, errCorrupt
			}
			d.final = true
			if n, _ := d.r.Read(make([]byte, 1)); n > 0 {
				return 0, errCorrupt // data after the final chunk
			}
		}
		d.n++
		d.buf = pt
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}

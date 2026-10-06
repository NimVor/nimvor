package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// A backup key file for safekeeping: the 32-byte key encrypted with a key
// derived from a passphrase the administrator chooses (PBKDF2-SHA256,
// AES-256-GCM, the key ID as additional data). The file alone does not
// decrypt any backup; with the passphrase it is imported on a new server to
// restore encrypted backups there.
const (
	keyFileFormat     = "whacp-backup-key"
	keyFileIterations = 600_000
	minPassphrase     = 12
	maxPassphrase     = 1024
	maxKeyFile        = 4 << 10
)

// KeyFile is the JSON document of an exported backup key.
type KeyFile struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	KeyID      string `json:"key_id"`
	KDF        string `json:"kdf"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

// ErrKeyPassphrase reports a wrong passphrase (or a damaged key file).
var ErrKeyPassphrase = errors.New("the passphrase is not correct, or the key file is damaged")

func checkPassphrase(p string) error {
	if !utf8.ValidString(p) || utf8.RuneCountInString(p) < minPassphrase || len(p) > maxPassphrase {
		return fmt.Errorf("the passphrase must have at least %d characters", minPassphrase)
	}
	return nil
}

func keyFileAEAD(passphrase string, salt []byte, iterations int) (cipher.AEAD, error) {
	kek, err := pbkdf2.Key(sha256.New, passphrase, salt, iterations, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ExportKey wraps key with passphrase.
func ExportKey(key []byte, passphrase string) (*KeyFile, error) {
	if len(key) != 32 {
		return nil, errors.New("backup key must be 32 bytes")
	}
	if err := checkPassphrase(passphrase); err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	aead, err := keyFileAEAD(passphrase, salt, keyFileIterations)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	id := KeyID(key)
	ct := aead.Seal(nil, nonce, key, []byte(keyFileFormat+"|"+id))
	return &KeyFile{
		Format: keyFileFormat, Version: 1, KeyID: id, KDF: "pbkdf2-sha256", Iterations: keyFileIterations,
		Salt: base64.StdEncoding.EncodeToString(salt), Nonce: base64.StdEncoding.EncodeToString(nonce), Ciphertext: base64.StdEncoding.EncodeToString(ct),
	}, nil
}

// UnwrapKey returns the backup key held in an exported key file.
func UnwrapKey(doc []byte, passphrase string) ([]byte, error) {
	if len(doc) > maxKeyFile {
		return nil, errors.New("not a WhaCP backup key file")
	}
	var kf KeyFile
	if err := json.Unmarshal(doc, &kf); err != nil || kf.Format != keyFileFormat || kf.Version != 1 || kf.KDF != "pbkdf2-sha256" {
		return nil, errors.New("not a WhaCP backup key file")
	}
	// Bounded: a file cannot make the agent spin for minutes.
	if kf.Iterations < 100_000 || kf.Iterations > 5_000_000 {
		return nil, errors.New("not a WhaCP backup key file")
	}
	salt, err1 := base64.StdEncoding.DecodeString(kf.Salt)
	nonce, err2 := base64.StdEncoding.DecodeString(kf.Nonce)
	ct, err3 := base64.StdEncoding.DecodeString(kf.Ciphertext)
	if err1 != nil || err2 != nil || err3 != nil || len(salt) < 16 {
		return nil, errors.New("not a WhaCP backup key file")
	}
	if len(passphrase) > maxPassphrase {
		return nil, ErrKeyPassphrase
	}
	aead, err := keyFileAEAD(passphrase, salt, kf.Iterations)
	if err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() {
		return nil, errors.New("not a WhaCP backup key file")
	}
	key, err := aead.Open(nil, nonce, ct, []byte(keyFileFormat+"|"+kf.KeyID))
	if err != nil || len(key) != 32 || KeyID(key) != kf.KeyID {
		return nil, ErrKeyPassphrase
	}
	return key, nil
}

// existingKey returns the key stored at path, nil when there is none.
func existingKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("%s is not a valid backup key", path)
	}
	return b, nil
}

// InstallKey stores key at path (a real path) when the server has no backup
// key yet. A server that already has a different key keeps it: replacing
// it would make its own encrypted backups unreadable.
func InstallKey(path string, key []byte) (already bool, err error) {
	cur, err := existingKey(path)
	if err != nil {
		return false, err
	}
	if cur != nil {
		if KeyID(cur) == KeyID(key) {
			return true, nil
		}
		return false, fmt.Errorf("this server already has a different backup key (key id %s); its own encrypted backups need it, so it is not replaced", KeyID(cur))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		os.Remove(path)
		return false, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return false, err
	}
	return false, f.Close()
}

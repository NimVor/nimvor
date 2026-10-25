// Package webauthn implements WebAuthn security-key ceremonies as a second
// sign-in factor and for in-profile registration/deletion. The logic is a
// direct port of panel/app/Security/WebAuthn.php so that existing keys
// continue to work and the database schema is unchanged: ES256, RS256 and
// Ed25519 keys are accepted; attestation is "none"; challenges live in the
// session for five minutes; signature counters that go backwards are
// refused as a cloned key.
package webauthn

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxKeys mirrors WebAuthn::MAX_KEYS.
const MaxKeys = 10

// TTL is the lifetime of a WebAuthn challenge in the session.
const TTL = 300 * time.Second

// Service owns the DB handle.
type Service struct {
	db *sql.DB
}

// New returns a WebAuthn service against the given database.
func New(db *sql.DB) *Service { return &Service{db: db} }

// Credential is one row from webauthn_credentials.
type Credential struct {
	ID         int64
	Name       string
	CredID     string
	PublicKey  string
	SignCount  int64
	CreatedAt  string
	LastUsedAt sql.NullString
}

// Credentials returns all keys registered to userID.
func (s *Service) Credentials(userID int64) ([]Credential, error) {
	rows, err := s.db.Query("SELECT id, name, credential_id, public_key, sign_count, created_at, last_used_at FROM webauthn_credentials WHERE user_id = ? ORDER BY id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Credential
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.ID, &c.Name, &c.CredID, &c.PublicKey, &c.SignCount, &c.CreatedAt, &c.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Has reports whether the user has at least one key registered.
func (s *Service) Has(userID int64) (bool, error) {
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?", userID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// Delete removes a key by id for the given user. Returns (false, nil) when
// no such row exists.
func (s *Service) Delete(userID, id int64) (bool, string, error) {
	var name string
	err := s.db.QueryRow("SELECT name FROM webauthn_credentials WHERE id = ? AND user_id = ?", id, userID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if _, err := s.db.Exec("DELETE FROM webauthn_credentials WHERE id = ?", id); err != nil {
		return false, "", err
	}
	return true, name, nil
}

// DeleteAll removes every key for the user (used during 2FA reset).
func (s *Service) DeleteAll(userID int64) error {
	_, err := s.db.Exec("DELETE FROM webauthn_credentials WHERE user_id = ?", userID)
	return err
}

// Challenge is the pending ceremony stored in the session.
type Challenge struct {
	Purpose   string `json:"purpose"`
	UserID    int64  `json:"user"`
	Challenge string `json:"challenge"` // base64url
	At        int64  `json:"at"`        // unix seconds
}

// NewChallenge generates a 32-byte random challenge and returns it (as
// base64url) together with the session payload to persist.
func NewChallenge(purpose string, userID int64) (string, Challenge) {
	b := randomBytes(32)
	ch := B64U(b)
	return ch, Challenge{Purpose: purpose, UserID: userID, Challenge: ch, At: time.Now().Unix()}
}

// TakeChallenge validates the stored challenge (purpose + user + TTL) and
// returns the b64url challenge string. It does NOT remove it; callers must
// clear the session key themselves (single-use is enforced by the handler
// flow because Finish/Verify succeeds only once and the caller removes the
// key before invoking the risky operation).
func TakeChallenge(c Challenge, purpose string, userID int64) (string, error) {
	if c.Purpose != purpose || c.UserID != userID {
		return "", errors.New("the request expired. Try again.")
	}
	if time.Now().Unix()-c.At > int64(TTL/time.Second) {
		return "", errors.New("the request expired. Try again.")
	}
	return c.Challenge, nil
}

// RegUser is the shape RegistrationOptions/Register need.
type RegUser struct {
	ID          int64
	Username    string
	DisplayName string
}

// RegistrationOptions builds the navigator.credentials.create() payload
// (mirrors WebAuthn::registrationOptions).
func (s *Service) RegistrationOptions(user RegUser, rpID, rpName string) (map[string]any, Challenge, error) {
	chB64, sess := NewChallenge("register", user.ID)

	rows, err := s.db.Query("SELECT credential_id FROM webauthn_credentials WHERE user_id = ?", user.ID)
	if err != nil {
		return nil, Challenge{}, err
	}
	var exclude []map[string]any
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			rows.Close()
			return nil, Challenge{}, err
		}
		exclude = append(exclude, map[string]any{"type": "public-key", "id": cid})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, Challenge{}, err
	}

	sum := sha256.Sum256([]byte("whacp-user|" + strconv.FormatInt(user.ID, 10)))
	name := user.DisplayName
	if name == "" {
		name = user.Username
	}
	return map[string]any{
		"challenge": chB64,
		"rp":        map[string]any{"id": rpID, "name": rpName},
		"user": map[string]any{
			"id":          B64U(sum[:]),
			"name":        user.Username,
			"displayName": name,
		},
		"pubKeyCredParams": []map[string]any{
			{"type": "public-key", "alg": -7},
			{"type": "public-key", "alg": -8},
			{"type": "public-key", "alg": -257},
		},
		"timeout":     120000,
		"attestation": "none",
		"authenticatorSelection": map[string]any{
			"userVerification": "preferred",
			"residentKey":      "discouraged",
		},
		"excludeCredentials": exclude,
	}, sess, nil
}

// LoginOptions builds the navigator.credentials.get() payload for the
// pending sign-in (mirrors WebAuthn::loginOptions).
func (s *Service) LoginOptions(userID int64, rpID string) (map[string]any, Challenge, error) {
	chB64, sess := NewChallenge("login", userID)

	rows, err := s.db.Query("SELECT credential_id FROM webauthn_credentials WHERE user_id = ?", userID)
	if err != nil {
		return nil, Challenge{}, err
	}
	var allow []map[string]any
	for rows.Next() {
		var cid string
		if err := rows.Scan(&cid); err != nil {
			rows.Close()
			return nil, Challenge{}, err
		}
		allow = append(allow, map[string]any{"type": "public-key", "id": cid})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, Challenge{}, err
	}

	return map[string]any{
		"challenge":        chB64,
		"rpId":             rpID,
		"allowCredentials": allow,
		"timeout":          120000,
		"userVerification": "preferred",
	}, sess, nil
}

// Register verifies a navigator.credentials.create() response and stores
// the new key. name is the user-supplied label; clientData/attestation are
// the b64url fields posted by the browser. challengeB64 is the taken
// (consumed) b64url challenge from the session. Mirrors WebAuthn::register.
func (s *Service) Register(user RegUser, origin, rpID, name, clientDataB64, attestationB64, challengeB64 string) error {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return errors.New("give the key a name (up to 60 characters).")
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?", user.ID).Scan(&n); err != nil {
		return err
	}
	if n >= MaxKeys {
		return fmt.Errorf("you can register at most %d keys.", MaxKeys)
	}
	clientDataRaw, err := UnB64U(clientDataB64)
	if err != nil {
		return errors.New("the key sent an unreadable response.")
	}
	if _, err := checkClientData(clientDataRaw, "webauthn.create", challengeB64, origin); err != nil {
		return err
	}
	att, err := UnB64U(attestationB64)
	if err != nil {
		return errors.New("the key sent an unreadable response.")
	}
	// The browser returns attestationObject (CBOR), which contains authData
	// + fmt + attStmt. "none" attestation means fmt="none" and attStmt is
	// empty (or the whole object may just be {authData, fmt, attStmt}).
	authDataRaw, err := extractAuthDataFromAttestation(att)
	if err != nil {
		return errors.New("the key sent an unreadable response.")
	}
	parsed, err := ParseAuthData(authDataRaw, rpID, true)
	if err != nil {
		return err
	}
	credID := B64U(parsed.CredentialID)
	var exists int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM webauthn_credentials WHERE credential_id = ?", credID).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return errors.New("this key is registered already.")
	}
	pubStr, err := CoseToKey(parsed.PublicKey)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		"INSERT INTO webauthn_credentials (user_id, credential_id, public_key, sign_count, name, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		user.ID, credID, pubStr, int64(parsed.SignCount), name, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return err
	}
	return nil
}

// Verify checks a navigator.credentials.get() assertion and updates the
// credential's counter + last_used_at on success. Returns the matched
// credential's name. credId/clientData/authenticatorData/signature are
// b64url-encoded (raw for signature). Mirrors WebAuthn::verify.
func (s *Service) Verify(userID int64, origin, rpID, credIDB64, clientDataB64, authDataB64, sigB64, challengeB64 string) (string, error) {
	clientRaw, err := UnB64U(clientDataB64)
	if err != nil {
		return "", errors.New("the key sent an unreadable response.")
	}
	if _, err := checkClientData(clientRaw, "webauthn.get", challengeB64, origin); err != nil {
		return "", err
	}
	authRaw, err := UnB64U(authDataB64)
	if err != nil {
		return "", errors.New("the key sent an unreadable response.")
	}
	parsed, err := ParseAuthData(authRaw, rpID, false)
	if err != nil {
		return "", err
	}
	sigRaw, err := UnB64U(sigB64)
	if err != nil {
		return "", errors.New("the key sent an unreadable response.")
	}

	var (
		id        int64
		name      string
		pubKey    string
		storedCnt int64
	)
	err = s.db.QueryRow(
		"SELECT id, name, public_key, sign_count FROM webauthn_credentials WHERE user_id = ? AND credential_id = ?",
		userID, credIDB64,
	).Scan(&id, &name, &pubKey, &storedCnt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("this key is not registered for your account.")
	}
	if err != nil {
		return "", err
	}
	signed := append(append([]byte{}, authRaw...), sha256Sum(clientRaw)...)
	ok, err := VerifySignature(pubKey, signed, sigRaw)
	if err != nil || !ok {
		return "", errors.New("the key's signature is not valid.")
	}
	// Clone detection: counters both non-zero and new <= stored → cloned.
	if (parsed.SignCount != 0 || storedCnt != 0) && parsed.SignCount <= uint32(storedCnt) {
		return "", errCloned
	}
	if _, err := s.db.Exec(
		"UPDATE webauthn_credentials SET sign_count = ?, last_used_at = ? WHERE id = ?",
		int64(parsed.SignCount), time.Now().UTC().Format(time.RFC3339), id,
	); err != nil {
		return "", err
	}
	return name, nil
}

// ErrCloned is returned when the authenticator counter went backwards
// (suggests a cloned key). Callers should audit auth.webauthn_clone.
var ErrCloned = errors.New("cloned")

func init() {
	// keep errCloned private alias for backwards compat if any caller checked
}

var errCloned = errors.New("this key's counter went backwards, which suggests a copied key. It was refused; contact an administrator.")

// --- Encoding ---------------------------------------------------------

// B64U base64url-encodes without padding (WebAuthn format).
func B64U(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// UnB64U decodes base64url with or without padding.
func UnB64U(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrBadEncoding
	}
	if pad := (4 - len(s)%4) % 4; pad > 0 {
		s += strings.Repeat("=", pad)
	}
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, ErrBadEncoding
	}
	return b, nil
}

// ErrBadEncoding is returned for invalid base64url input.
var ErrBadEncoding = errors.New("invalid encoding")

// ClientData is the decoded CollectedClientData.
type ClientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

func checkClientData(raw []byte, wantType, wantChallenge, origin string) (*ClientData, error) {
	var cd ClientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return nil, errors.New("the key sent an unexpected response.")
	}
	if cd.Type != wantType {
		return nil, errors.New("the key sent an unexpected response.")
	}
	if cd.Challenge != wantChallenge {
		return nil, errors.New("the request expired. Try again.")
	}
	if cd.Origin != origin {
		return nil, fmt.Errorf("the key answered for a different address (%s).", trimForErr(cd.Origin))
	}
	if cd.CrossOrigin {
		return nil, errors.New("cross-origin use is not allowed.")
	}
	return &cd, nil
}

func trimForErr(s string) string {
	if len(s) > 100 {
		return s[:100]
	}
	return s
}

// AuthData is the decoded authenticator data.
type AuthData struct {
	RelyingPartyIDHash []byte
	Flags              uint8
	SignCount          uint32
	CredentialID       []byte
	PublicKey          map[any]any
}

// ParseAuthData parses authenticator data per WebAuthn §6.1. If attested is
// true, also reads the attested credential (AAGUID+idLen+id+COSE key).
func ParseAuthData(d []byte, rpID string, attested bool) (*AuthData, error) {
	if len(d) < 37 {
		return nil, errors.New("the key sent an unreadable response.")
	}
	wantHash := sha256.Sum256([]byte(rpID))
	if !constantTimeEq(d[:32], wantHash[:]) {
		return nil, errors.New("the key answered for a different site.")
	}
	flags := d[32]
	if flags&0x01 == 0 {
		return nil, errors.New("touch the key to confirm.")
	}
	count := binary.BigEndian.Uint32(d[33:37])
	out := &AuthData{
		RelyingPartyIDHash: d[:32],
		Flags:              flags,
		SignCount:          count,
	}
	if !attested {
		return out, nil
	}
	// Attested credential data: AAGUID(16) + credIdLen(2) + credId + publicKey(CBOR)
	if flags&0x40 == 0 || len(d) < 55+16 {
		return nil, errors.New("the key did not send its public key.")
	}
	// offset 37 is where attested credential starts.
	idLen := int(binary.BigEndian.Uint16(d[37+16 : 37+18]))
	if idLen < 16 || idLen > 1023 || 37+18+idLen > len(d) {
		return nil, errors.New("the key sent an unreadable response.")
	}
	out.CredentialID = d[37+18 : 37+18+idLen]
	key, _, err := cborDecode(d, 37+18+idLen, 0)
	if err != nil {
		return nil, errors.New("the key sent an unreadable public key.")
	}
	m, ok := key.(map[any]any)
	if !ok {
		return nil, errors.New("the key sent an unreadable public key.")
	}
	out.PublicKey = m
	return out, nil
}

// extractAuthDataFromAttestation pulls authData out of an attestation
// object CBOR (fmt + attStmt + authData). For "none" attestation, attStmt
// is an empty map; we just need the authData field.
func extractAuthDataFromAttestation(b []byte) ([]byte, error) {
	v, _, err := cborDecode(b, 0, 0)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[any]any)
	if !ok {
		return nil, errors.New("bad attestation")
	}
	ad, ok := m["authData"]
	if !ok {
		// try text key
		for k, vv := range m {
			if ks, isStr := k.(string); isStr && ks == "authData" {
				ad = vv
				ok = true
				break
			}
		}
	}
	if !ok {
		return nil, errors.New("authData missing")
	}
	raw, ok := ad.([]byte)
	if !ok {
		return nil, errors.New("authData not bytes")
	}
	return raw, nil
}

// CoseToKey converts a COSE public-key map to the stored form: PEM for
// ES256/RS256 or "ed25519:<b64>" for Ed25519.
func CoseToKey(k map[any]any) (string, error) {
	i64 := func(key any) (int64, bool) {
		v, ok := k[key]
		if !ok {
			return 0, false
		}
		switch n := v.(type) {
		case int64:
			return n, true
		case int:
			return int64(n), true
		case uint64:
			return int64(n), true
		}
		return 0, false
	}
	bytes := func(key any) ([]byte, bool) {
		v, ok := k[key]
		if !ok {
			return nil, false
		}
		b, ok := v.([]byte)
		return b, ok
	}
	kty, ok1 := i64(1)
	alg, ok2 := i64(3)
	if !ok1 || !ok2 {
		return "", errors.New("unsupported key")
	}
	if kty == 2 && alg == -7 {
		crv, _ := i64(-1)
		x, xOK := bytes(-2)
		y, yOK := bytes(-3)
		if crv == 1 && xOK && yOK && len(x) == 32 && len(y) == 32 {
			der, err := marshalES256PublicKey(x, y)
			if err != nil {
				return "", err
			}
			return pemBlock("PUBLIC KEY", der), nil
		}
	}
	if kty == 3 && alg == -257 {
		n, nOK := bytes(-1)
		e, eOK := bytes(-2)
		if nOK && eOK && len(n) >= 256 {
			der, err := marshalRSAPublicKey(n, e)
			if err != nil {
				return "", err
			}
			return pemBlock("PUBLIC KEY", der), nil
		}
	}
	if kty == 1 && alg == -8 {
		crv, _ := i64(-1)
		pk, ok := bytes(-2)
		if crv == 6 && ok && len(pk) == 32 {
			return "ed25519:" + base64.StdEncoding.EncodeToString(pk), nil
		}
	}
	return "", errors.New("this key uses an algorithm WhaCP does not support (ES256, RS256 and Ed25519 are).")
}

// VerifySignature verifies authData||sha256(clientData) against a stored
// key (PEM ES256/RS256 or "ed25519:<b64>").
func VerifySignature(storedKey string, data, sig []byte) (bool, error) {
	if strings.HasPrefix(storedKey, "ed25519:") {
		pk, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(storedKey, "ed25519:"))
		if err != nil || len(pk) != ed25519.PublicKeySize {
			return false, nil
		}
		if len(sig) != ed25519.SignatureSize {
			return false, nil
		}
		return ed25519.Verify(pk, data, sig), nil
	}
	block, _ := pem.Decode([]byte(storedKey))
	if block == nil {
		return false, nil
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return false, nil
	}
	h := sha256.Sum256(data)
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		return ecdsa.VerifyASN1(k, h[:], sig), nil
	case *rsa.PublicKey:
		if err := rsa.VerifyPKCS1v15(k, crypto.SHA256, h[:], sig); err == nil {
			return true, nil
		}
		return false, nil
	}
	return false, nil
}

// --- CBOR (RFC 8949) minimal decoder -----------------------------------

func cborDecode(data []byte, offset, depth int) (any, int, error) {
	if depth > 16 {
		return nil, 0, errors.New("CBOR nested too deeply")
	}
	if offset >= len(data) {
		return nil, 0, errors.New("CBOR data too short")
	}
	b := data[offset]
	offset++
	major := b >> 5
	info := b & 0x1f
	var arg uint64
	if info < 24 {
		arg = uint64(info)
	} else if info <= 27 {
		n := 1 << (info - 24)
		if offset+n > len(data) {
			return nil, 0, errors.New("CBOR data too short")
		}
		arg = 0
		for i := 0; i < n; i++ {
			arg = (arg << 8) | uint64(data[offset+i])
		}
		offset += n
	} else {
		return nil, 0, errors.New("CBOR indefinite lengths are not supported")
	}
	switch major {
	case 0:
		return int64(arg), offset, nil
	case 1:
		return -1 - int64(arg), offset, nil
	case 2, 3:
		if int(arg) < 0 || uint64(offset)+arg > uint64(len(data)) {
			return nil, 0, errors.New("CBOR data too short")
		}
		out := make([]byte, arg)
		copy(out, data[offset:offset+int(arg)])
		return out, offset + int(arg), nil
	case 4:
		out := make([]any, arg)
		for i := uint64(0); i < arg; i++ {
			v, next, err := cborDecode(data, offset, depth+1)
			if err != nil {
				return nil, 0, err
			}
			out[i] = v
			offset = next
		}
		return out, offset, nil
	case 5:
		out := map[any]any{}
		for i := uint64(0); i < arg; i++ {
			k, next, err := cborDecode(data, offset, depth+1)
			if err != nil {
				return nil, 0, err
			}
			v, next, err := cborDecode(data, next, depth+1)
			if err != nil {
				return nil, 0, err
			}
			switch k.(type) {
			case int64, []byte, string:
			default:
				return nil, 0, errors.New("CBOR map key type not supported")
			}
			out[k] = v
			offset = next
		}
		return out, offset, nil
	case 7:
		switch info {
		case 20:
			return false, offset, nil
		case 21:
			return true, offset, nil
		case 22:
			return nil, offset, nil
		}
		return nil, 0, errors.New("CBOR simple value not supported")
	}
	return nil, 0, errors.New("CBOR tags are not supported")
}

// --- DER helpers (ES256 + RS256 SPKI) ----------------------------------

func marshalES256PublicKey(x, y []byte) ([]byte, error) {
	point := make([]byte, 1+len(x)+len(y))
	point[0] = 0x04
	copy(point[1:], x)
	copy(point[1+len(x):], y)
	algID := derSequence(
		derOID([]byte{0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x02, 0x01}),
		derOID([]byte{0x2A, 0x86, 0x48, 0xCE, 0x3D, 0x03, 0x01, 0x07}),
	)
	return derSequence(algID, derBitString(point)), nil
}

func marshalRSAPublicKey(n, e []byte) ([]byte, error) {
	algID := derSequence(
		derOID([]byte{0x2A, 0x86, 0x48, 0x86, 0xF7, 0x0D, 0x01, 0x01, 0x01}),
		derNull(),
	)
	pk := derSequence(derUnsigned(n), derUnsigned(e))
	return derSequence(algID, derBitString(pk)), nil
}

func derSequence(parts ...[]byte) []byte { return derTag(0x30, parts...) }
func derOID(oid []byte) []byte           { return derTag(0x06, oid) }
func derNull() []byte                    { return []byte{0x05, 0x00} }
func derBitString(b []byte) []byte       { return derTag(0x03, append([]byte{0x00}, b...)) }

func derUnsigned(b []byte) []byte {
	b = bytes.TrimLeft(b, "\x00")
	if len(b) == 0 {
		b = []byte{0x00}
	} else if b[0]&0x80 != 0 {
		b = append([]byte{0x00}, b...)
	}
	return derTag(0x02, b)
}

func derTag(tag byte, parts ...[]byte) []byte {
	var body []byte
	for _, p := range parts {
		body = append(body, p...)
	}
	l := len(body)
	var lenb []byte
	if l < 128 {
		lenb = []byte{byte(l)}
	} else {
		var tmp []byte
		for n := l; n > 0; n >>= 8 {
			tmp = append([]byte{byte(n)}, tmp...)
		}
		lenb = append([]byte{0x80 | byte(len(tmp))}, tmp...)
	}
	out := make([]byte, 0, 1+len(lenb)+len(body))
	out = append(out, tag)
	out = append(out, lenb...)
	out = append(out, body...)
	return out
}

func pemBlock(typ string, der []byte) string {
	var b strings.Builder
	b.WriteString("-----BEGIN ")
	b.WriteString(typ)
	b.WriteString("-----\n")
	s := base64.StdEncoding.EncodeToString(der)
	for len(s) > 64 {
		b.WriteString(s[:64])
		b.WriteByte('\n')
		s = s[64:]
	}
	if s != "" {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	b.WriteString("-----END ")
	b.WriteString(typ)
	b.WriteString("-----\n")
	return b.String()
}

func constantTimeEq(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

// randomBytes uses crypto/rand via the stdlib.
func randomBytes(n int) []byte {
	// avoid importing crypto/rand at top of every file; small helper
	return randomBytesFill(n)
}

// ParseBigIntFromHex is exported for tests.
func ParseBigIntFromHex(s string) *big.Int {
	b, _ := hex.DecodeString(s)
	return new(big.Int).SetBytes(b)
}

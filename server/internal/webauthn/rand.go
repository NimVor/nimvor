package webauthn

import "crypto/rand"

func randomBytesFill(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("webauthn: crypto/rand failed: " + err.Error())
	}
	return b
}

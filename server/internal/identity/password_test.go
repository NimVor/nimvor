package identity

import "testing"

// These hashes were produced by PHP's password_hash() (the same function the
// PHP panel uses), so this test proves the Go verifier accepts credentials
// created by the existing application. Regenerate with:
//
//	php -r 'echo password_hash("correct horse battery staple", PASSWORD_ARGON2ID);'
//	php -r 'echo password_hash("correct horse battery staple", PASSWORD_BCRYPT);'
const (
	phpArgon2id = "$argon2id$v=19$m=65536,t=4,p=1$a2l2TkJFMDJkS09HYTY3Ug$3jdx5kNrMWKaMSPWkDAAShBk+JlgksFdItavD55w/ts"
	phpBcrypt   = "$2y$12$sp6JdAda1944mzdfaPremuyBOlkGRMt4.2n7Xw70QoaYncFLiC7.K"
	correctPW   = "correct horse battery staple"
)

func TestVerifyPasswordMatchesPhpHashes(t *testing.T) {
	cases := []struct {
		name string
		hash string
	}{
		{"argon2id (PHP default in this product)", phpArgon2id},
		{"bcrypt ($2y$ prefix from PHP)", phpBcrypt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !VerifyPassword(c.hash, correctPW) {
				t.Errorf("correct password rejected for %s", c.name)
			}
			if VerifyPassword(c.hash, "wrong password") {
				t.Errorf("wrong password accepted for %s", c.name)
			}
			if VerifyPassword(c.hash, "") {
				t.Errorf("empty password accepted for %s", c.name)
			}
		})
	}
}

func TestVerifyPasswordRejectsGarbage(t *testing.T) {
	for _, h := range []string{"", "plaintext", "$unknown$abc", "$argon2id$bad", phpArgon2id[:20]} {
		if VerifyPassword(h, correctPW) {
			t.Errorf("garbage hash %q was accepted", h)
		}
	}
}
